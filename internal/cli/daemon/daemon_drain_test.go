package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	testNodeThis  = "loom-supervisor-h-222"
	testNodeOther = "loom-supervisor-h-111"
	testWorkspace = "WS"
)

// newDrainTestSupervisor builds a supervisor with a pinned node ID so
// supersession is deterministic. NodeID is set directly rather than left to
// ResolveNodeID's host+PID fallback, which would differ per run.
func newDrainTestSupervisor(st store.Store, nodeID string) *supervisor.Supervisor {
	return &supervisor.Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		WorkspaceID:    testWorkspace,
		ControlStore:   st,
		NodeID:         nodeID,
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*supervisor.AgentProcess, 0),
		Concurrency:    supervisor.NewConcurrencyTracker(nil),
		EmitEvent:      func(events.Event) {},
	}
}

// seedDrainAgent puts an agent in the store with the given drain shape.
func seedDrainAgent(t *testing.T, st *memstore.Store, name string, desired domain.AgentDesiredState, nodeID string, expires *time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: testWorkspace, Name: "task"}); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("seed role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey:   testWorkspace,
		Name:           name,
		RoleName:       "task",
		DesiredState:   desired,
		DrainNodeID:    nodeID,
		DrainExpiresAt: expires,
	}); err != nil {
		t.Fatalf("seed agent %s: %v", name, err)
	}
}

func getStoredAgent(t *testing.T, st *memstore.Store, name string) *domain.Agent {
	t.Helper()
	a, err := st.Agents().Get(context.Background(), testWorkspace, name)
	if err != nil {
		t.Fatalf("get agent %s: %v", name, err)
	}
	return a
}

func TestParseYieldTTL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"empty falls back to the default", "", 2 * time.Hour},
		{"garbage falls back to the default", "not-a-number", 2 * time.Hour},
		{"negative falls back to the default", "-30", 2 * time.Hour},
		{"300 seconds is five minutes", "300", 5 * time.Minute},
		{"zero means no expiry", "0", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseYieldTTL(tc.raw); got != tc.want {
				t.Errorf("parseYieldTTL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestMarkAgentYieldAcceptedStampsAbsentAgent covers the failure that made a
// yield look successful while recording nothing: the agent has no supervised
// process, so every "is it running" check would have bailed out first.
func TestMarkAgentYieldAcceptedStampsAbsentAgent(t *testing.T) {
	st := memstore.New()
	seedDrainAgent(t, st, "falcon", "", "", nil)

	d := &Daemon{
		store:  st,
		sup:    newDrainTestSupervisor(st, testNodeThis),
		config: &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{Worktree: "falcon", Role: "task"}}},
	}
	// The agent is deliberately absent from sup.Agents.
	before := time.Now().UTC()
	d.markAgentYieldAccepted("falcon", 2*time.Hour)

	got := getStoredAgent(t, st, "falcon")
	if got.DesiredState != domain.AgentDesiredDraining {
		t.Errorf("desired_state = %q, want draining", got.DesiredState)
	}
	if got.DrainNodeID != testNodeThis {
		t.Errorf("drain_node_id = %q, want %q", got.DrainNodeID, testNodeThis)
	}
	if got.DrainExpiresAt == nil {
		t.Fatal("drain_expires_at not set")
	}
	wantMin, wantMax := before.Add(2*time.Hour), time.Now().UTC().Add(2*time.Hour+time.Minute)
	if got.DrainExpiresAt.Before(wantMin) || got.DrainExpiresAt.After(wantMax) {
		t.Errorf("drain_expires_at = %v, want ~now+2h", got.DrainExpiresAt)
	}

	// The in-memory mirror must agree, so the predicate parks before the
	// next 30s config poll rather than after it.
	if d.config.Agents[0].DrainNodeID != testNodeThis {
		t.Errorf("config mirror drain_node_id = %q, want %q", d.config.Agents[0].DrainNodeID, testNodeThis)
	}
}

func TestMarkAgentYieldAcceptedZeroTTLLeavesNoExpiry(t *testing.T) {
	st := memstore.New()
	seedDrainAgent(t, st, "nova", "", "", nil)

	d := &Daemon{
		store:  st,
		sup:    newDrainTestSupervisor(st, testNodeThis),
		config: &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{Worktree: "nova", Role: "task"}}},
	}
	d.markAgentYieldAccepted("nova", 0)

	got := getStoredAgent(t, st, "nova")
	if got.DesiredState != domain.AgentDesiredDraining {
		t.Errorf("desired_state = %q, want draining", got.DesiredState)
	}
	if got.DrainExpiresAt != nil {
		t.Errorf("drain_expires_at = %v, want nil for --until-restart", got.DrainExpiresAt)
	}
	if got.DrainNodeID != testNodeThis {
		t.Errorf("drain_node_id = %q, want %q", got.DrainNodeID, testNodeThis)
	}
}

// TestReconcileStaleDrainsIncidentRegression is the end-to-end regression for
// the 6.5h fleet-wide park: a drain issued by a supervisor that has since
// restarted must not survive the restart.
func TestReconcileStaleDrainsIncidentRegression(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)

	tests := []struct {
		name          string
		drainNodeID   string
		drainExpires  *time.Time
		wantSupervise bool
		wantLog       string
	}{
		{
			name:        "drain from a previous supervisor is superseded",
			drainNodeID: testNodeOther, drainExpires: &future,
			wantSupervise: true, wantLog: "superseded",
		},
		{
			// The shape every agent parked by a pre-change yield has.
			name:          "drain with no metadata is untargeted and released",
			wantSupervise: true, wantLog: "untargeted",
		},
		{
			name:        "expired drain is released",
			drainNodeID: testNodeThis, drainExpires: &past,
			wantSupervise: true, wantLog: "expired",
		},
		{
			name:        "drain from this supervisor still parks",
			drainNodeID: testNodeThis, drainExpires: &future,
			wantSupervise: false, wantLog: "agent parked",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := memstore.New()
			seedDrainAgent(t, st, "falcon", domain.AgentDesiredDraining, tc.drainNodeID, tc.drainExpires)
			sup := newDrainTestSupervisor(st, testNodeThis)
			cfg := &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
				Worktree:       "falcon",
				Role:           "task",
				DesiredState:   domain.AgentDesiredDraining,
				DrainNodeID:    tc.drainNodeID,
				DrainExpiresAt: tc.drainExpires,
			}}}

			// The supervision decision is asserted through the predicate
			// initSupervisorAgents itself consults; building a real
			// AgentProcess would need on-disk worktrees this test has no
			// use for. The park path additionally runs initSupervisorAgents
			// below, where it returns before ever touching a worktree.
			out := captureLogOutput(t, func() {
				reconcileStaleDrains(sup, st, cfg)
				if !tc.wantSupervise {
					parked, err := initSupervisorAgents(sup, cfg.Agents, cfg.Roles)
					if err != nil {
						t.Fatalf("initSupervisorAgents: %v", err)
					}
					if len(parked) != 1 {
						t.Errorf("parked = %+v, want exactly one parked agent", parked)
					}
				}
			})

			supervised := cfg.Agents[0].ShouldSuperviseWithRoles(cfg.Roles, sup.ResolveNodeID(), time.Now())
			if supervised != tc.wantSupervise {
				t.Errorf("supervised = %v, want %v", supervised, tc.wantSupervise)
			}
			if !strings.Contains(out, tc.wantLog) {
				t.Errorf("log missing %q:\n%s", tc.wantLog, out)
			}

			stored := getStoredAgent(t, st, "falcon")
			if tc.wantSupervise {
				// Released: cleared in config AND in the store.
				if cfg.Agents[0].DesiredState != "" || cfg.Agents[0].DrainNodeID != "" || cfg.Agents[0].DrainExpiresAt != nil {
					t.Errorf("config drain not cleared: %+v", cfg.Agents[0])
				}
				if stored.DesiredState == domain.AgentDesiredDraining {
					t.Errorf("stored desired_state still draining")
				}
				if stored.DrainNodeID != "" || stored.DrainExpiresAt != nil {
					t.Errorf("stored drain metadata survived: %q / %v", stored.DrainNodeID, stored.DrainExpiresAt)
				}
			} else {
				// Parked: left completely untouched, with a resume hint.
				if stored.DesiredState != domain.AgentDesiredDraining || stored.DrainNodeID != tc.drainNodeID {
					t.Errorf("active drain was modified: %+v", stored)
				}
				if !strings.Contains(out, "loom data agent start falcon") {
					t.Errorf("parked log missing the resume command:\n%s", out)
				}
			}
		})
	}
}

// TestReconcileStaleDrainsDegradesSafely: every store failure mode must leave
// the agent parked rather than fail daemon construction.
func TestReconcileStaleDrainsDegradesSafely(t *testing.T) {
	expired := time.Now().UTC().Add(-time.Hour)

	t.Run("nil store", func(t *testing.T) {
		sup := newDrainTestSupervisor(nil, testNodeThis)
		cfg := &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
			Worktree: "falcon", Role: "task",
			DesiredState: domain.AgentDesiredDraining, DrainExpiresAt: &expired,
		}}}
		reconcileStaleDrains(sup, nil, cfg)
		// The in-memory clear still happens: it needs no store at all.
		if cfg.Agents[0].DesiredState != "" {
			t.Errorf("config drain not cleared with a nil store: %+v", cfg.Agents[0])
		}
	})

	t.Run("erroring store leaves the agent parked", func(t *testing.T) {
		st := memstore.New() // agent deliberately never created → Update errors
		sup := newDrainTestSupervisor(st, testNodeThis)
		cfg := &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
			Worktree: "ghost", Role: "task",
			DesiredState: domain.AgentDesiredDraining, DrainExpiresAt: &expired,
		}}}
		out := captureLogOutput(t, func() { reconcileStaleDrains(sup, st, cfg) })
		if !strings.Contains(out, "clearing stale agent drain in store failed") {
			t.Errorf("store error not logged:\n%s", out)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		reconcileStaleDrains(newDrainTestSupervisor(nil, testNodeThis), nil, nil)
	})
}

// TestReconcileTickNeverWrites pins I2: clearing happens only during startup
// reconciliation. A yield issued seconds before a tick must survive it.
func TestReconcileTickNeverWrites(t *testing.T) {
	st := memstore.New()
	future := time.Now().UTC().Add(2 * time.Hour)
	seedDrainAgent(t, st, "falcon", domain.AgentDesiredDraining, testNodeThis, &future)

	d := &Daemon{
		store: st,
		sup:   newDrainTestSupervisor(st, testNodeThis),
		config: &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
			Worktree: "falcon", Role: "task",
			DesiredState: domain.AgentDesiredDraining,
			DrainNodeID:  testNodeThis, DrainExpiresAt: &future,
		}}},
	}

	out := captureLogOutput(t, func() {
		for i := 0; i < 25; i++ {
			d.reportParkedAgents()
		}
	})

	stored := getStoredAgent(t, st, "falcon")
	if stored.DesiredState != domain.AgentDesiredDraining ||
		stored.DrainNodeID != testNodeThis || stored.DrainExpiresAt == nil {
		t.Errorf("a reconcile tick modified the drain: %+v", stored)
	}
	if d.config.Agents[0].DesiredState != domain.AgentDesiredDraining {
		t.Errorf("a reconcile tick modified the config: %+v", d.config.Agents[0])
	}
	if !strings.Contains(out, "agent parked: not claiming") {
		t.Errorf("parked agent was never reported:\n%s", out)
	}
	if !strings.Contains(out, "loom data agent start falcon") {
		t.Errorf("parked report missing the resume command:\n%s", out)
	}
}

// TestReportParkedAgentsThrottles pins I9: throttled, but never silent.
func TestReportParkedAgentsThrottles(t *testing.T) {
	st := memstore.New()
	future := time.Now().UTC().Add(2 * time.Hour)
	d := &Daemon{
		store: st,
		sup:   newDrainTestSupervisor(st, testNodeThis),
		config: &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
			Worktree: "falcon", Role: "task",
			DesiredState: domain.AgentDesiredDraining,
			DrainNodeID:  testNodeThis, DrainExpiresAt: &future,
		}}},
	}

	count := func(s string) int { return strings.Count(s, "agent parked: not claiming") }

	first := captureLogOutput(t, func() { d.reportParkedAgents() })
	if count(first) != 1 {
		t.Fatalf("first tick logged %d lines, want 1", count(first))
	}
	next := captureLogOutput(t, func() {
		for i := 0; i < parkedReportEveryNTicks-1; i++ {
			d.reportParkedAgents()
		}
	})
	if count(next) != 0 {
		t.Errorf("throttled window logged %d lines, want 0", count(next))
	}
	after := captureLogOutput(t, func() { d.reportParkedAgents() })
	if count(after) != 1 {
		t.Errorf("tick after the throttle window logged %d lines, want 1", count(after))
	}
}

// TestStoppedIsUnchanged pins I4: stopped stays an indefinite park with no TTL
// and no supersession, so startup reconciliation must not touch it.
func TestStoppedIsUnchanged(t *testing.T) {
	st := memstore.New()
	seedDrainAgent(t, st, "nova", domain.AgentDesiredStopped, "", nil)
	sup := newDrainTestSupervisor(st, testNodeThis)
	cfg := &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
		Worktree: "nova", Role: "task", DesiredState: domain.AgentDesiredStopped,
	}}}

	reconcileStaleDrains(sup, st, cfg)

	if cfg.Agents[0].DesiredState != domain.AgentDesiredStopped {
		t.Errorf("stopped was cleared by drain reconciliation: %+v", cfg.Agents[0])
	}
	parked, err := initSupervisorAgents(sup, cfg.Agents, cfg.Roles)
	if err != nil {
		t.Fatalf("initSupervisorAgents: %v", err)
	}
	if len(parked) != 1 {
		t.Errorf("stopped agent was supervised; want it parked")
	}
}

func TestYieldTTLFromArgs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"absent args", "", 2 * time.Hour},
		{"malformed json", "{", 2 * time.Hour},
		{"no ttl key", `{"other":"x"}`, 2 * time.Hour},
		{"explicit ttl", `{"ttl_seconds":"300"}`, 5 * time.Minute},
		{"until restart", `{"ttl_seconds":"0"}`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := yieldTTLFromArgs([]byte(tc.raw)); got != tc.want {
				t.Errorf("yieldTTLFromArgs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestStatusToIconIncludesParked(t *testing.T) {
	if got := statusToIcon("parked"); got != "◌" {
		t.Errorf("statusToIcon(parked) = %q, want ◌", got)
	}
	// The pre-existing vocabulary must be untouched.
	for status, want := range map[string]string{
		"running": "●", "starting": "◐", "stopped": "○", "failed": "✗", "weird": "?",
	} {
		if got := statusToIcon(status); got != want {
			t.Errorf("statusToIcon(%q) = %q, want %q", status, got, want)
		}
	}
}

// TestParkedAgentReachesTheStateFile: a parked agent must appear in the state
// file (and so in the Agents count) instead of disappearing with the fleet.
func TestParkedAgentReachesTheStateFile(t *testing.T) {
	expires := time.Date(2026, 8, 16, 14, 0, 0, 0, time.UTC)
	parked := []ParkedAgent{{
		Worktree: "falcon", Role: "task",
		DesiredState: domain.AgentDesiredDraining, DrainExpiresAt: &expires,
		ResumeCommand: "loom data agent start falcon",
	}}

	path := t.TempDir() + "/daemon-agents.json"
	if err := writeStateFile(path, time.Now(), nil, parked, nil, nil, 3); err != nil {
		t.Fatalf("writeStateFile: %v", err)
	}
	state, err := ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if len(state.Agents) != 1 {
		t.Fatalf("state has %d agents, want the parked one carried through", len(state.Agents))
	}
	got := state.Agents[0]
	if got.Status != "parked" || got.Worktree != "falcon" {
		t.Errorf("parked agent = %+v", got)
	}
	if got.ResumeCommand != "loom data agent start falcon" {
		t.Errorf("ResumeCommand = %q", got.ResumeCommand)
	}
	if got.DrainExpiresAt == nil || !got.DrainExpiresAt.Equal(expires) {
		t.Errorf("DrainExpiresAt = %v, want %v", got.DrainExpiresAt, expires)
	}
}

// TestMarkAgentStartAcceptedClearsDrainMirror: after a start, the local config
// mirror must carry no drain metadata, matching fleet-db's derived clear.
func TestMarkAgentStartAcceptedClearsDrainMirror(t *testing.T) {
	st := memstore.New()
	future := time.Now().UTC().Add(time.Hour)
	seedDrainAgent(t, st, "falcon", domain.AgentDesiredDraining, testNodeThis, &future)

	d := &Daemon{
		store: st,
		sup:   newDrainTestSupervisor(st, testNodeThis),
		config: &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{
			Worktree: "falcon", Role: "task",
			DesiredState: domain.AgentDesiredDraining,
			DrainNodeID:  testNodeThis, DrainExpiresAt: &future,
		}}},
	}

	d.markAgentStartAccepted("falcon")

	entry := d.config.Agents[0]
	if entry.DesiredState != domain.AgentDesiredRunning {
		t.Errorf("desired_state = %q, want running", entry.DesiredState)
	}
	if entry.DrainNodeID != "" || entry.DrainExpiresAt != nil {
		t.Errorf("drain metadata survived a start: %q / %v", entry.DrainNodeID, entry.DrainExpiresAt)
	}
	// fleet-db derives the same clear server-side.
	stored := getStoredAgent(t, st, "falcon")
	if stored.DrainNodeID != "" || stored.DrainExpiresAt != nil {
		t.Errorf("stored drain metadata survived a start: %q / %v", stored.DrainNodeID, stored.DrainExpiresAt)
	}
}

// ---------------------------------------------------------------------------
// applyRestartPolicyDefaults YieldTimeout test
// ---------------------------------------------------------------------------

func TestApplyRestartPolicyDefaults_YieldTimeout(t *testing.T) {
	t.Run("nil gets default", func(t *testing.T) {
		rp := RestartPolicy{}
		applyRestartPolicyDefaults(&rp)

		if rp.YieldTimeout == nil {
			t.Fatal("YieldTimeout is nil after applyDefaults, want DefaultYieldTimeout")
		}
		if *rp.YieldTimeout != DefaultYieldTimeout {
			t.Errorf("YieldTimeout = %d, want %d", *rp.YieldTimeout, DefaultYieldTimeout)
		}
	})

	t.Run("already set is preserved", func(t *testing.T) {
		rp := RestartPolicy{YieldTimeout: intPtr(200)}
		applyRestartPolicyDefaults(&rp)

		if *rp.YieldTimeout != 200 {
			t.Errorf("YieldTimeout = %d, want 200 (should not be overwritten)", *rp.YieldTimeout)
		}
	})
}

// ---------------------------------------------------------------------------
// applyRestartPolicyDefaults SigtermTimeout test
// ---------------------------------------------------------------------------

func TestApplyRestartPolicyDefaults_SigtermTimeout(t *testing.T) {
	t.Run("nil gets default", func(t *testing.T) {
		rp := RestartPolicy{}
		applyRestartPolicyDefaults(&rp)

		if rp.SigtermTimeout == nil {
			t.Fatal("SigtermTimeout is nil after applyDefaults, want DefaultSigtermTimeout")
		}
		if *rp.SigtermTimeout != DefaultSigtermTimeout {
			t.Errorf("SigtermTimeout = %d, want %d", *rp.SigtermTimeout, DefaultSigtermTimeout)
		}
	})

	t.Run("already set is preserved", func(t *testing.T) {
		rp := RestartPolicy{SigtermTimeout: intPtr(200)}
		applyRestartPolicyDefaults(&rp)

		if *rp.SigtermTimeout != 200 {
			t.Errorf("SigtermTimeout = %d, want 200 (should not be overwritten)", *rp.SigtermTimeout)
		}
	})
}
