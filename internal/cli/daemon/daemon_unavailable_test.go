package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newUnavailableTestDaemon builds a daemon with the given config agents and an
// empty supervisor — no agent processes, which is the state boot leaves behind
// when every entry failed to construct.
func newUnavailableTestDaemon(t *testing.T, entries []AgentEntry) *Daemon {
	t.Helper()
	d := &Daemon{config: makeDaemonConfig(entries, nil)}
	d.sup = &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		Concurrency:    supervisor.NewConcurrencyTracker(nil),
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*supervisor.AgentProcess, 0),
		Shutdown:       make(chan struct{}),
		EmitEvent:      func(events.Event) {},
	}
	t.Cleanup(func() { d.sup.Concurrency.Close() })
	return d
}

func TestNewUnavailableAgent_CarriesReasonAndHint(t *testing.T) {
	entry := AgentEntry{Worktree: "ghost", Role: "plan", Repos: []string{"loomcli"}}
	err := errors.New(`agent[2] worktree "ghost": 'ghost' is not a worktree, repo, or workspace name`)

	u := newUnavailableAgent(entry, err)

	if u.Worktree != "ghost" || u.Role != "plan" {
		t.Errorf("identity = %q/%q, want ghost/plan", u.Worktree, u.Role)
	}
	if u.Reason != err.Error() {
		t.Errorf("Reason = %q, want the NewAgent error text", u.Reason)
	}
	if u.Hint == "" {
		t.Error("Hint is empty; every unavailable row needs an operator next step")
	}
	if u.Since.IsZero() {
		t.Error("Since is zero, want the time the failure was recorded")
	}

	roleErr := errors.New("agent[0]: role \"nope\": prompt file not found")
	roleHint := newUnavailableAgent(AgentEntry{Worktree: "ghost", Role: "nope"}, roleErr).Hint
	if roleHint == u.Hint {
		t.Errorf("role failure reused the worktree hint %q", roleHint)
	}
}

func TestUnavailableAgent_ToDaemonAgentStatus(t *testing.T) {
	u := UnavailableAgent{
		Worktree: "ghost",
		Role:     "plan",
		Repo:     "loomcli",
		Reason:   "worktree does not resolve",
		Hint:     "create the worktree",
	}

	das := u.toDaemonAgentStatus()

	if das.Status != "unavailable" {
		t.Errorf("Status = %q, want unavailable", das.Status)
	}
	if das.PID != 0 {
		t.Errorf("PID = %d, want 0 for an agent that never ran", das.PID)
	}
	if das.Detail != u.Reason || das.Hint != u.Hint {
		t.Errorf("Detail/Hint = %q/%q, want %q/%q", das.Detail, das.Hint, u.Reason, u.Hint)
	}
	if das.Worktree != "ghost" || das.Role != "plan" || das.Repo != "loomcli" {
		t.Errorf("identity = %q/%q/%q, want ghost/plan/loomcli", das.Worktree, das.Role, das.Repo)
	}
}

func TestRetryUnavailableAgents_DropsEntriesRemovedFromConfig(t *testing.T) {
	d := newUnavailableTestDaemon(t, nil) // config carries no agents at all
	d.unavailable = []UnavailableAgent{{Worktree: "ghost", Role: "plan"}}

	d.retryUnavailableAgents()

	if got := d.UnavailableAgents(); len(got) != 0 {
		t.Fatalf("UnavailableAgents() = %#v, want empty after the entry left the config", got)
	}
}

func TestRetryUnavailableAgents_SkipsManuallyStoppedAgents(t *testing.T) {
	entry := AgentEntry{Worktree: "ghost", Role: "plan"}
	d := newUnavailableTestDaemon(t, []AgentEntry{entry})
	d.unavailable = []UnavailableAgent{{Worktree: "ghost", Role: "plan"}}
	d.sup.StoppedAgents["ghost"] = struct{}{}

	d.retryUnavailableAgents()

	if got := d.UnavailableAgents(); len(got) != 1 || got[0].Worktree != "ghost" {
		t.Fatalf("UnavailableAgents() = %#v, want ghost still listed", got)
	}
	if n := len(d.sup.Agents); n != 0 {
		t.Fatalf("len(sup.Agents) = %d, want 0: a manually stopped agent must not be resurrected", n)
	}
}

func TestReportUnavailableAgents_Throttles(t *testing.T) {
	d := newUnavailableTestDaemon(t, nil)
	d.unavailable = []UnavailableAgent{{Worktree: "ghost", Role: "plan"}}

	for i := 0; i < unavailableReportInterval; i++ {
		d.reportUnavailableAgents()
	}
	if d.unavailableReportTick != unavailableReportInterval {
		t.Fatalf("tick counter = %d after %d calls, want %d",
			d.unavailableReportTick, unavailableReportInterval, unavailableReportInterval)
	}

	// Emptying the list resets the counter so recovery followed by a new
	// failure reports immediately instead of waiting out the old throttle.
	d.unavailable = nil
	d.reportUnavailableAgents()
	if d.unavailableReportTick != 0 {
		t.Fatalf("tick counter = %d with nothing unavailable, want 0", d.unavailableReportTick)
	}
}

// TestInitSupervisorAgents_MissingWorktreeDoesNotFailTheOthers is the
// regression test for the 2026-08-17 incident: one agent entry whose worktree
// did not exist made NewDaemon return an error, which crash-looped the whole
// workspace under PM2.
func TestInitSupervisorAgents_MissingWorktreeDoesNotFailTheOthers(t *testing.T) {
	tmpDir := t.TempDir()
	wtDir := filepath.Join(tmpDir, "worktrees", "good")
	if err := os.MkdirAll(filepath.Join(wtDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	cfg := makeDaemonConfig([]AgentEntry{
		{Worktree: wtDir, Role: "plan"},
		{Worktree: "ghost-does-not-exist", Role: "plan"},
	}, nil)

	d, err := NewDaemon(cfg, tmpDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v, want a daemon that boots around the broken entry", err)
	}
	if n := len(d.sup.Agents); n != 1 {
		t.Fatalf("len(sup.Agents) = %d, want 1 (the resolvable agent)", n)
	}
	unavailable := d.UnavailableAgents()
	if len(unavailable) != 1 {
		t.Fatalf("UnavailableAgents() = %#v, want exactly the broken entry", unavailable)
	}
	if unavailable[0].Worktree != "ghost-does-not-exist" {
		t.Errorf("unavailable agent = %q, want ghost-does-not-exist", unavailable[0].Worktree)
	}
	if unavailable[0].Reason == "" {
		t.Error("unavailable agent has no Reason; the operator needs the resolver error")
	}
}

func TestReloadAndReconcile_RetriesUnavailableAgentsWhenHashUnchanged(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))

	cfg, err := config.LoadDaemonConfig(tmpDir)
	if err != nil {
		t.Skipf("LoadDaemonConfig unavailable in this environment: %v", err)
	}

	d := newUnavailableTestDaemon(t, nil)
	d.projectDir = tmpDir
	d.config = cfg
	d.configHash = computeConfigHash(cfg)
	d.unavailable = []UnavailableAgent{{Worktree: "ghost", Role: "plan"}}

	d.reloadAndReconcile()

	// The config hash is unchanged, so the early return fires — the retry must
	// still have run, which here means dropping an entry the config no longer
	// carries. A worktree appearing on disk changes no hash either, and that is
	// the recovery this path exists for.
	if got := d.UnavailableAgents(); len(got) != 0 {
		t.Fatalf("UnavailableAgents() = %#v, want the retry to have run on the unchanged-hash path", got)
	}
}
