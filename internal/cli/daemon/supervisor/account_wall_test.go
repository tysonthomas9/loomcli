package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newAccountWallSupervisor builds a supervisor whose restart policy carries an
// explicit account-wall cooldown (nil ⇒ package default).
func newAccountWallSupervisor(cooldownSeconds *int) *Supervisor {
	return &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{
				Daemon: cfgpkg.DaemonSettings{
					RestartPolicy: cfgpkg.RestartPolicy{AccountWallCooldown: cooldownSeconds},
				},
				Backend: "codex",
			}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
}

func newAccountWallAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "heron", Role: "task", Backend: "codex"},
		RoleConfig: cfgpkg.RoleConfig{Description: "test"},
	}
}

// TestGateAccountWall_ParksAgentAndPreservesBudget is the point of the whole
// gate: a wall another agent hit must stop this one BEFORE it claims a task,
// without spending any of its restart budget.
func TestGateAccountWall_ParksAgentAndPreservesBudget(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrBilling),
		Message: "credit balance too low",
	})

	err := s.gateAccountWall(ap)
	if !errors.Is(err, ErrAccountWall) {
		t.Fatalf("gateAccountWall() = %v, want ErrAccountWall", err)
	}
	if ap.StopReason != StopReasonAccountWall {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonAccountWall)
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (a park must not erode the budget)", ap.RestartCount)
	}
	if ap.LastError == nil {
		t.Fatal("LastError must be populated")
	}
	if !ap.LastError.Class.IsClass(wrapper.ErrBilling) {
		t.Errorf("LastError.Class = %v, want Billing", ap.LastError.Class)
	}
	if ap.LastError.Message != "credit balance too low" {
		t.Errorf("LastError.Message = %q, want the recorded wall message", ap.LastError.Message)
	}
}

// TestGateAccountWall_ExpiredWallClearsPark covers the recovery branch: at
// expiry the agent resumes on its own, with its parked state cleared.
func TestGateAccountWall_ExpiredWallClearsPark(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonAccountWall
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth)}

	s.WallUntil = time.Now().Add(-time.Second)
	s.WallClass = agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	if err := s.gateAccountWall(ap); err != nil {
		t.Fatalf("gateAccountWall() = %v, want nil once the wall expired", err)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want cleared", ap.StopReason)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v, want cleared", ap.LastError)
	}
}

// TestGateAccountWall_NoWallIsNoOp guards the common case: with no wall armed
// the gate must not touch an agent that was never parked.
func TestGateAccountWall_NoWallIsNoOp(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonRateLimited

	if err := s.gateAccountWall(ap); err != nil {
		t.Fatalf("gateAccountWall() = %v, want nil", err)
	}
	if ap.StopReason != StopReasonRateLimited {
		t.Errorf("StopReason = %q, want it left alone", ap.StopReason)
	}
}

func TestRecordAccountWall(t *testing.T) {
	authOutcome := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	t.Run("non-wall class records nothing", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrTransient), &agenterr.AgentError{})
		if _, _, ok := s.accountWallActive(); ok {
			t.Error("a transient error must not park the fleet")
		}
	})

	t.Run("domain outcome records nothing", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), nil)
		if _, _, ok := s.accountWallActive(); ok {
			t.Error("NoWork must not park the fleet")
		}
	})

	t.Run("cooldown 0 disables the gate", func(t *testing.T) {
		zero := 0
		s := newAccountWallSupervisor(&zero)
		s.recordWall("", authOutcome, &agenterr.AgentError{Message: "bad key"})
		if _, _, _, _, ok := s.wallActiveFor(""); ok {
			t.Error("account_wall_cooldown: 0 must record nothing, in any scope")
		}
	})

	t.Run("default cooldown", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", authOutcome, nil)
		remaining, _, _, _, ok := s.wallActiveFor("")
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining <= defaultAccountWallCooldown-time.Minute || remaining > defaultAccountWallCooldown {
			t.Errorf("remaining = %s, want ~%s", remaining, defaultAccountWallCooldown)
		}
	})

	t.Run("RetryAfter hint wins over the default", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), &agenterr.AgentError{
			RetryAfter: 90 * time.Second,
		})
		remaining, _, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining > 90*time.Second || remaining < 80*time.Second {
			t.Errorf("remaining = %s, want ~90s from the RetryAfter hint", remaining)
		}
	})

	t.Run("absurd RetryAfter is capped", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", authOutcome, &agenterr.AgentError{RetryAfter: 24 * time.Hour})
		remaining, _, _, _, ok := s.wallActiveFor("")
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining > maxAccountWallCooldown {
			t.Errorf("remaining = %s, want capped at %s", remaining, maxAccountWallCooldown)
		}
	})

	t.Run("never shortens a live wall", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		billing := agenterr.OutcomeFromHarness(wrapper.ErrBilling)
		s.recordWall("", billing, &agenterr.AgentError{RetryAfter: 30 * time.Minute, Message: "long"})
		s.recordWall("", billing, &agenterr.AgentError{RetryAfter: time.Minute, Message: "short"})
		remaining, message, ok := s.accountWallActive()
		if !ok {
			t.Fatal("wall must be active")
		}
		if remaining < 29*time.Minute {
			t.Errorf("remaining = %s, want the longer wall preserved", remaining)
		}
		if message != "long" {
			t.Errorf("message = %q, want the longer wall's message preserved", message)
		}
	})

	t.Run("extends when the new expiry is later", func(t *testing.T) {
		s := newAccountWallSupervisor(nil)
		s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
			RetryAfter: time.Minute,
		})
		s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
			RetryAfter: 40 * time.Minute, Message: "billing",
		})
		remaining, message, _ := s.accountWallActive()
		if remaining < 39*time.Minute {
			t.Errorf("remaining = %s, want the wall extended", remaining)
		}
		if message != "billing" {
			t.Errorf("message = %q, want the later wall's message", message)
		}
	})
}

// TestGetAccountWallCooldown_EnvOverride: the env var wins over config, which
// is what lets an integration test arm and expire a wall in seconds.
func TestGetAccountWallCooldown_EnvOverride(t *testing.T) {
	cfgSeconds := 60
	s := newAccountWallSupervisor(&cfgSeconds)
	if got := s.getAccountWallCooldown(); got != time.Minute {
		t.Fatalf("config cooldown = %s, want 1m", got)
	}
	t.Setenv("LOOM_DAEMON_ACCOUNT_WALL_COOLDOWN_SECONDS", "5")
	if got := s.getAccountWallCooldown(); got != 5*time.Second {
		t.Errorf("env cooldown = %s, want 5s", got)
	}
}

// TestApplyFatalStop_RecordsAccountWall wires the fatal path to the gate: a
// billing wall stopping one agent must park the rest.
func TestApplyFatalStop_RecordsAccountWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrBilling),
		Message: "insufficient credits",
	}

	if s.shouldRestart(ap) {
		t.Fatal("a billing wall must stop the agent")
	}
	if ap.StopReason != StopReasonFatalError {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonFatalError)
	}
	remaining, message, ok := s.accountWallActive()
	if !ok {
		t.Fatal("applyFatalStop must record the account wall")
	}
	if remaining <= 0 || message != "insufficient credits" {
		t.Errorf("wall = (%s, %q), want a live wall carrying the error message", remaining, message)
	}
}

// TestRateLimitUncountedPath_RecordsAccountWall covers the class that
// otherwise retries forever, per agent: a usage-limit wall.
func TestRateLimitUncountedPath_RecordsAccountWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrRateLimited),
		Message: "usage limit reached; resets at 3pm",
	}

	if !s.shouldRestart(ap) {
		t.Fatal("a rate limit still retries uncounted for the agent that hit it")
	}
	if ap.RestartCount != 0 {
		t.Errorf("RestartCount = %d, want 0 (uncounted retry)", ap.RestartCount)
	}
	if _, _, ok := s.accountWallActive(); !ok {
		t.Error("the RetryUncounted rate-limit path must record the account wall")
	}
}

// TestAccountWall_ConcurrentAccess exercises the WallMu contract under -race.
func TestAccountWall_ConcurrentAccess(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	outcome := agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.recordWall("", outcome, &agenterr.AgentError{
				RetryAfter: time.Duration(i+1) * time.Minute,
				Message:    "wall",
			})
		}(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.accountWallActive()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.gateAccountWall(newAccountWallAgent()) //nolint:errcheck // exercising the lock, not the verdict
		}()
	}
	wg.Wait()

	if _, _, ok := s.accountWallActive(); !ok {
		t.Error("wall must be armed after concurrent records")
	}
}

// TestShouldRestart_ParkedAgentKeepsSupervising is the self-recovery contract:
// a parked agent must stay in the supervise loop with its budget intact. If it
// fell through to the fatal path instead, a wall recorded by one agent would
// permanently kill every other agent's supervise goroutine.
func TestShouldRestart_ParkedAgentKeepsSupervising(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrBilling), Message: "no credits",
	})
	if err := s.gateAccountWall(ap); !errors.Is(err, ErrAccountWall) {
		t.Fatalf("gateAccountWall() = %v, want ErrAccountWall", err)
	}

	if !s.shouldRestart(ap) {
		t.Fatal("a parked agent must keep supervising, not stop")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (park must not erode the budget)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonAccountWall {
		t.Errorf("StopReason = %q, want it to stay %q while parked", ap.StopReason, StopReasonAccountWall)
	}
}

// TestComputeBackoff_ParkedAgentSleepsTheWall: the park sleeps out the wall
// rather than an exponential backoff keyed on someone else's error, and drops
// to zero the moment the wall lifts.
func TestComputeBackoff_ParkedAgentSleepsTheWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.StopReason = StopReasonAccountWall
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrBilling)}

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		RetryAfter: 10 * time.Minute,
	})
	got := s.computeBackoff(ap)
	if got > 10*time.Minute || got < 9*time.Minute {
		t.Errorf("computeBackoff() = %s, want ~10m (the remaining wall)", got)
	}

	s.WallUntil = time.Now().Add(-time.Second)
	if got := s.computeBackoff(ap); got != 0 {
		t.Errorf("computeBackoff() = %s after expiry, want 0", got)
	}
}

// ── Scope: the rule this whole file exists for ───────────────────────────────

// newProfiledAgent builds an agent named for a worktree, alongside a
// supervisor rooted at projectDir. The pairing matters: the credential key is
// resolved from (ProjectDir, Entry.Worktree), exactly as AppendProfileEnv
// resolves the profile it will spawn with.
func newProfiledAgent(worktree string) *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: worktree, Role: "task", Backend: "codex"},
		RoleConfig: cfgpkg.RoleConfig{Description: "test"},
	}
}

// provisionProfile creates the on-disk shape ProfileCredentialKey looks for
// and returns the root it will key on. Deliberately no manifest and no harness
// binary: that resolution is stat-only, and this is where that is asserted.
func provisionProfile(t *testing.T, projectDir, agent, harness string) string {
	t.Helper()
	root := filepath.Join(projectDir, ".loom", "agent-profiles", agent)
	if err := os.MkdirAll(filepath.Join(root, harness), 0o755); err != nil {
		t.Fatalf("provisioning %s: %v", agent, err)
	}
	return root
}

// TestWallScopeFor pins the one table that decides blast radius. Auth is a
// fact about ONE login; billing and usage limits are facts about the shared
// subscription; everything else is agent-local and parks nobody.
func TestWallScopeFor(t *testing.T) {
	tests := []struct {
		name    string
		outcome agenterr.Outcome
		want    wallScope
	}{
		{"auth is a profile fact", agenterr.OutcomeFromHarness(wrapper.ErrAuth), wallScopeProfile},
		{"billing is an account fact", agenterr.OutcomeFromHarness(wrapper.ErrBilling), wallScopeAccount},
		{"usage limit is an account fact", agenterr.OutcomeFromHarness(wrapper.ErrRateLimited), wallScopeAccount},
		{"transient is nobody's wall", agenterr.OutcomeFromHarness(wrapper.ErrTransient), wallScopeNone},
		{"timeout is nobody's wall", agenterr.OutcomeFromHarness(wrapper.ErrTimeout), wallScopeNone},
		{"no work is nobody's wall", agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), wallScopeNone},
		{"zero outcome is nobody's wall", agenterr.Outcome{}, wallScopeNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wallScopeFor(tt.outcome); got != tt.want {
				t.Errorf("wallScopeFor(%s) = %s, want %s", tt.outcome, got, tt.want)
			}
		})
	}
}

// TestProfileCredentialKey covers every way an agent can end up in the shared
// bucket. It provisions no manifest and no harness binary on purpose: if
// resolution ever started verifying, these cases would fail — which is the
// point, because this runs on every poll cycle for every agent.
func TestProfileCredentialKey(t *testing.T) {
	projectDir := t.TempDir()
	claudeRoot := provisionProfile(t, projectDir, "worker-2", "claude")
	codexRoot := provisionProfile(t, projectDir, "worker-3", "codex")
	// A root with no harness subdirectory at all: the agent runs on inherited
	// config, so it belongs with everyone else who does.
	if err := os.MkdirAll(filepath.Join(projectDir, ".loom", "agent-profiles", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name  string
		dir   string
		agent string
		want  string
	}{
		{"claude profile keys on its root", projectDir, "worker-2", claudeRoot},
		{"codex profile keys on its root", projectDir, "worker-3", codexRoot},
		{"root without a harness dir is shared", projectDir, "empty", ""},
		{"no profile at all is shared", projectDir, "planner", ""},
		{"path traversal is shared, never a panic", projectDir, "..", ""},
		{"empty agent name is shared", projectDir, "", ""},
		{"empty project dir is shared", "", "worker-2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProfileCredentialKey(tt.dir, tt.agent); got != tt.want {
				t.Errorf("ProfileCredentialKey(%q, %q) = %q, want %q", tt.dir, tt.agent, got, tt.want)
			}
		})
	}
}

// TestGateProfileWall_ParksOnlyTheSharedCredential is the regression test for
// the outage: on 2026-08-31 two profiles with gutted credentials parked seven
// healthy agents. An auth wall must reach exactly the agents authenticating
// from the same profile root, and nobody else.
func TestGateProfileWall_ParksOnlyTheSharedCredential(t *testing.T) {
	projectDir := t.TempDir()
	brokenRoot := provisionProfile(t, projectDir, "worker-2", "claude")
	provisionProfile(t, projectDir, "planner", "claude")

	s := newAccountWallSupervisor(nil)
	s.ProjectDir = projectDir

	broken := newProfiledAgent("worker-2")
	// A second supervised process for the same agent — a restart in flight —
	// resolves the same key, because the key is the credential, not the object.
	sameCredential := newProfiledAgent("worker-2")
	healthy := newProfiledAgent("planner")
	healthy.RestartCount = 2

	s.recordWall(brokenRoot, agenterr.OutcomeFromHarness(wrapper.ErrAuth), &agenterr.AgentError{
		Class:   agenterr.OutcomeFromHarness(wrapper.ErrAuth),
		Message: "invalid API key",
	})

	for _, ap := range []*AgentProcess{broken, sameCredential} {
		if err := s.gateAccountWall(ap); !errors.Is(err, ErrProfileWall) {
			t.Fatalf("gateAccountWall(%s) = %v, want ErrProfileWall", ap.Entry.Worktree, err)
		}
		if ap.StopReason != StopReasonProfileWall {
			t.Errorf("%s StopReason = %q, want %q", ap.Entry.Worktree, ap.StopReason, StopReasonProfileWall)
		}
		if ap.CredentialKey != brokenRoot {
			t.Errorf("%s CredentialKey = %q, want %q", ap.Entry.Worktree, ap.CredentialKey, brokenRoot)
		}
	}

	if err := s.gateAccountWall(healthy); err != nil {
		t.Fatalf("gateAccountWall(planner) = %v, want nil — a healthy profile must keep working", err)
	}
	if healthy.StopReason != "" {
		t.Errorf("planner StopReason = %q, want it untouched", healthy.StopReason)
	}
	if healthy.LastError != nil {
		t.Errorf("planner LastError = %v, want nil", healthy.LastError)
	}
	if healthy.RestartCount != 2 {
		t.Errorf("planner RestartCount = %d, want 2 (nothing about it changed)", healthy.RestartCount)
	}
}

// TestGateProfileWall_LegacyAgentsShareTheEmptyKey: an agent with no profile
// root inherits the operator's shared harness config, so its auth failure IS
// shared. Scoping the wall to the agent instead of to the credential would
// reproduce the original bug for exactly this configuration.
func TestGateProfileWall_LegacyAgentsShareTheEmptyKey(t *testing.T) {
	projectDir := t.TempDir()
	profiled := provisionProfile(t, projectDir, "worker-2", "claude")

	s := newAccountWallSupervisor(nil)
	s.ProjectDir = projectDir

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrAuth), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth), Message: "expired login",
	})

	for _, name := range []string{"legacy-a", "legacy-b"} {
		ap := newProfiledAgent(name)
		if err := s.gateAccountWall(ap); !errors.Is(err, ErrProfileWall) {
			t.Errorf("gateAccountWall(%s) = %v, want ErrProfileWall — legacy agents share one login", name, err)
		}
		if ap.CredentialKey != "" {
			t.Errorf("%s CredentialKey = %q, want the shared empty key", name, ap.CredentialKey)
		}
	}

	// The agent that owns its own credentials is untouched by the shared wall.
	isolated := newProfiledAgent("worker-2")
	if err := s.gateAccountWall(isolated); err != nil {
		t.Errorf("gateAccountWall(worker-2) = %v, want nil (its key is %q, not the shared one)", err, profiled)
	}
}

// TestGateAccountWall_BillingStillParksEveryone is the anti-regression on
// PUPPET-106: profile isolation narrows AUTH walls and nothing else. A billing
// wall is a fact about the subscription every profile bills to.
func TestGateAccountWall_BillingStillParksEveryone(t *testing.T) {
	projectDir := t.TempDir()
	provisionProfile(t, projectDir, "worker-2", "claude")
	provisionProfile(t, projectDir, "planner", "claude")

	s := newAccountWallSupervisor(nil)
	s.ProjectDir = projectDir

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrBilling), Message: "credit balance too low",
	})

	for _, name := range []string{"worker-2", "planner", "unprofiled"} {
		ap := newProfiledAgent(name)
		if err := s.gateAccountWall(ap); !errors.Is(err, ErrAccountWall) {
			t.Errorf("gateAccountWall(%s) = %v, want ErrAccountWall", name, err)
		}
		if ap.StopReason != StopReasonAccountWall {
			t.Errorf("%s StopReason = %q, want %q", name, ap.StopReason, StopReasonAccountWall)
		}
	}
}

// TestWallActiveFor_AccountWallWinsOverProfile: when both are live the account
// wall is the answer, because it is the longer-reaching fact and the one whose
// message tells an operator what to actually go fix.
func TestWallActiveFor_AccountWallWinsOverProfile(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	s.recordWall("/root/a", agenterr.OutcomeFromHarness(wrapper.ErrAuth), &agenterr.AgentError{
		RetryAfter: 30 * time.Minute, Message: "invalid API key",
	})
	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		RetryAfter: 5 * time.Minute, Message: "credit balance too low",
	})

	_, message, class, scope, ok := s.wallActiveFor("/root/a")
	if !ok {
		t.Fatal("a wall must be active")
	}
	if scope != wallScopeAccount {
		t.Errorf("scope = %s, want account", scope)
	}
	if message != "credit balance too low" {
		t.Errorf("message = %q, want the account wall's", message)
	}
	if !class.IsClass(wrapper.ErrBilling) {
		t.Errorf("class = %s, want Billing", class)
	}
}

// TestRecordWall_ProfileWallNeverShortens carries the account wall's invariant
// over per key: a second, smaller observation must not release a parked agent
// early.
func TestRecordWall_ProfileWallNeverShortens(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	auth := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	s.recordWall("/root/a", auth, &agenterr.AgentError{RetryAfter: 30 * time.Minute, Message: "long"})
	s.recordWall("/root/a", auth, &agenterr.AgentError{RetryAfter: time.Minute, Message: "short"})

	remaining, message, _, scope, ok := s.wallActiveFor("/root/a")
	if !ok {
		t.Fatal("wall must be active")
	}
	if scope != wallScopeProfile {
		t.Errorf("scope = %s, want profile", scope)
	}
	if remaining < 29*time.Minute {
		t.Errorf("remaining = %s, want the longer wall preserved", remaining)
	}
	if message != "long" {
		t.Errorf("message = %q, want the longer wall's message preserved", message)
	}

	// A different key is a different credential and must be unaffected.
	if _, _, _, _, ok := s.wallActiveFor("/root/b"); ok {
		t.Error("/root/b must not be walled by /root/a's failure")
	}
}

// TestRecordWall_PrunesExpiredProfileWalls keeps the map bounded by the number
// of live credential sets rather than by every one ever seen.
func TestRecordWall_PrunesExpiredProfileWalls(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	auth := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	s.ProfileWalls = map[string]credentialWall{
		"/root/expired": {Until: time.Now().Add(-time.Minute), Class: auth},
	}
	s.recordWall("/root/live", auth, &agenterr.AgentError{RetryAfter: 10 * time.Minute})

	s.WallMu.Lock()
	_, stillThere := s.ProfileWalls["/root/expired"]
	n := len(s.ProfileWalls)
	s.WallMu.Unlock()

	if stillThere {
		t.Error("an expired profile wall must be pruned on the next record")
	}
	if n != 1 {
		t.Errorf("len(ProfileWalls) = %d, want 1", n)
	}
}

// TestComputeBackoff_ProfileWalledAgentSleepsItsOwnWall: a parked agent sleeps
// out ITS wall. Without the key it would sleep the ordinary backoff and
// busy-poll the gate for the whole cooldown — and an agent parked by a wall it
// does not own would sleep someone else's.
func TestComputeBackoff_ProfileWalledAgentSleepsItsOwnWall(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	auth := agenterr.OutcomeFromHarness(wrapper.ErrAuth)
	s.recordWall("/root/a", auth, &agenterr.AgentError{RetryAfter: 10 * time.Minute})

	ap := newProfiledAgent("worker-2")
	ap.StopReason = StopReasonProfileWall
	ap.CredentialKey = "/root/a"
	ap.LastError = &agenterr.AgentError{Class: auth}

	got := s.computeBackoff(ap)
	if got > 10*time.Minute || got < 9*time.Minute {
		t.Errorf("computeBackoff() = %s, want ~10m (its own remaining wall)", got)
	}

	// Same StopReason, a credential nobody walled: nothing to sleep out.
	other := newProfiledAgent("planner")
	other.StopReason = StopReasonProfileWall
	other.CredentialKey = "/root/b"
	other.LastError = &agenterr.AgentError{Class: auth}
	if got := s.computeBackoff(other); got != 0 {
		t.Errorf("computeBackoff() = %s for an unwalled credential, want 0", got)
	}
}

// TestShouldRestart_ProfileWalledAgentKeepsSupervising mirrors the account-wall
// contract: a park is not a failure, at either scope. If it fell through to the
// fatal path the agent's supervise goroutine would die for good.
func TestShouldRestart_ProfileWalledAgentKeepsSupervising(t *testing.T) {
	projectDir := t.TempDir()
	root := provisionProfile(t, projectDir, "worker-2", "claude")

	s := newAccountWallSupervisor(nil)
	s.ProjectDir = projectDir
	ap := newProfiledAgent("worker-2")
	ap.RestartCount = 2

	s.recordWall(root, agenterr.OutcomeFromHarness(wrapper.ErrAuth), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth), Message: "invalid API key",
	})
	if err := s.gateAccountWall(ap); !errors.Is(err, ErrProfileWall) {
		t.Fatalf("gateAccountWall() = %v, want ErrProfileWall", err)
	}

	if !s.shouldRestart(ap) {
		t.Fatal("a profile-walled agent must keep supervising, not stop")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (park must not erode the budget)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonProfileWall {
		t.Errorf("StopReason = %q, want it to stay %q while parked", ap.StopReason, StopReasonProfileWall)
	}
}

// TestGateProfileWall_ExpiredWallClearsPark: expiry is time-based and needs no
// sweep, and the recovery branch must clear a park of EITHER scope.
func TestGateProfileWall_ExpiredWallClearsPark(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	s.ProfileWalls = map[string]credentialWall{
		"": {Until: time.Now().Add(-time.Second), Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth)},
	}

	ap := newProfiledAgent("legacy")
	ap.StopReason = StopReasonProfileWall
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth)}

	if err := s.gateAccountWall(ap); err != nil {
		t.Fatalf("gateAccountWall() = %v, want nil once the wall expired", err)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want cleared", ap.StopReason)
	}
	if ap.LastError != nil {
		t.Errorf("LastError = %v, want cleared", ap.LastError)
	}
}

// TestWallSnapshot is what `loom daemon status` renders: every live wall, the
// account one first, each naming the credential it belongs to.
func TestWallSnapshot(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	auth := agenterr.OutcomeFromHarness(wrapper.ErrAuth)

	s.recordWall("", agenterr.OutcomeFromHarness(wrapper.ErrBilling), &agenterr.AgentError{
		RetryAfter: 30 * time.Minute, Message: "credit balance too low",
	})
	s.recordWall("/root/b", auth, &agenterr.AgentError{RetryAfter: 20 * time.Minute})
	s.recordWall("/root/a", auth, &agenterr.AgentError{RetryAfter: 10 * time.Minute})
	s.WallMu.Lock()
	s.ProfileWalls["/root/gone"] = credentialWall{Until: time.Now().Add(-time.Minute), Class: auth}
	s.WallMu.Unlock()

	got := s.WallSnapshot()
	if len(got) != 3 {
		t.Fatalf("WallSnapshot() returned %d walls, want 3 (expired ones excluded): %+v", len(got), got)
	}
	if got[0].Scope != "account" || got[0].Credential != "shared" {
		t.Errorf("first = %+v, want the account wall rendered as shared", got[0])
	}
	if got[0].Message != "credit balance too low" {
		t.Errorf("account message = %q, want the recorded one", got[0].Message)
	}
	// Profile walls follow, soonest expiry first.
	if got[1].Credential != "/root/a" || got[2].Credential != "/root/b" {
		t.Errorf("profile walls = %q, %q; want /root/a then /root/b", got[1].Credential, got[2].Credential)
	}
	for _, w := range got[1:] {
		if w.Scope != "profile" {
			t.Errorf("%s scope = %q, want profile", w.Credential, w.Scope)
		}
	}
}

// TestWall_ConcurrentAccessAcrossKeys extends the WallMu contract to the
// per-credential map: recording and reading several keys at once must stay
// race-free under -race.
func TestWall_ConcurrentAccessAcrossKeys(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	auth := agenterr.OutcomeFromHarness(wrapper.ErrAuth)
	keys := []string{"", "/root/a", "/root/b", "/root/c"}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		key := keys[i%len(keys)]
		wg.Add(3)
		go func(i int, key string) {
			defer wg.Done()
			s.recordWall(key, auth, &agenterr.AgentError{
				RetryAfter: time.Duration(i+1) * time.Minute, Message: "wall",
			})
		}(i, key)
		go func(key string) {
			defer wg.Done()
			s.wallActiveFor(key)
			s.wallBackoffFor(key)
		}(key)
		go func() {
			defer wg.Done()
			s.WallSnapshot()
		}()
	}
	wg.Wait()

	for _, key := range keys {
		if _, _, _, _, ok := s.wallActiveFor(key); !ok {
			t.Errorf("key %q must be walled after concurrent records", key)
		}
	}
}

// TestDecideRestart_TimeoutDuringWallIsUncounted is the regression test for
// PUPPET-435: on 2026-09-03 two agents were still cycling "restart budget
// exhausted, blocking" six minutes after the account wall that caused their
// timeouts had expired. A Timeout raised inside a live wall must leave the
// budget exactly where the wall park would have left it.
func TestDecideRestart_TimeoutDuringWallIsUncounted(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}

	s.WallUntil = time.Now().Add(5 * time.Minute)
	s.WallClass = agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false, want true (a shielded failure still restarts)")
	}
	if ap.RestartCount != 2 {
		t.Errorf("RestartCount = %d, want 2 (a wall-shielded failure must not erode the budget)", ap.RestartCount)
	}
	if ap.BlockCount != 0 {
		t.Errorf("BlockCount = %d, want 0", ap.BlockCount)
	}
	if ap.StopReason != "" {
		t.Errorf("StopReason = %q, want cleared so the next cycle hits the pre-flight gate", ap.StopReason)
	}
}

// TestDecideRestart_TimeoutWithNoWallStillCounts pins the behavior the shield
// must not disturb: with no wall live, a timeout is ordinary evidence about the
// agent, counts, and blocks once the budget is spent.
func TestDecideRestart_TimeoutWithNoWallStillCounts(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	ap.RestartCount = 2
	ap.LastExitCode = 1
	ap.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false, want true (still inside the budget)")
	}
	if ap.RestartCount != 3 {
		t.Fatalf("RestartCount = %d, want 3 (a timeout with no wall counts)", ap.RestartCount)
	}

	// One more failure spends the budget (getMaxRetries defaults to 3) and must
	// take the block path — the exhaustion behavior the shield sidesteps.
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart() = false, want true (Timeout blocks rather than stopping)")
	}
	if ap.StopReason != StopReasonMaxRetriesBlocked {
		t.Errorf("StopReason = %q, want %q", ap.StopReason, StopReasonMaxRetriesBlocked)
	}
	if ap.BlockCount != 1 {
		t.Errorf("BlockCount = %d, want 1", ap.BlockCount)
	}
}

// TestDecideRestart_ProfileWallAlsoShields covers the scope decision: the
// shield asks "is THIS AGENT walled" (wallActiveFor), so a profile wall on the
// agent's own credential shields it — and an agent on a different credential is
// untouched.
func TestDecideRestart_ProfileWallAlsoShields(t *testing.T) {
	projectDir := t.TempDir()
	walledRoot := provisionProfile(t, projectDir, "worker-2", "claude")
	provisionProfile(t, projectDir, "planner", "claude")

	s := newAccountWallSupervisor(nil)
	s.ProjectDir = projectDir

	s.recordWall(walledRoot, agenterr.OutcomeFromHarness(wrapper.ErrAuth), &agenterr.AgentError{
		Class: agenterr.OutcomeFromHarness(wrapper.ErrAuth), Message: "invalid API key",
	})

	walled := newProfiledAgent("worker-2")
	walled.CredentialKey = walledRoot
	walled.RestartCount = 2
	walled.LastExitCode = 1
	walled.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTransient)}

	if !s.shouldRestart(walled) {
		t.Fatal("shouldRestart(worker-2) = false, want true")
	}
	if walled.RestartCount != 2 {
		t.Errorf("worker-2 RestartCount = %d, want 2 (a profile wall shields its own credential)", walled.RestartCount)
	}

	// The negative half: a different credential is not walled, so its timeout
	// is ordinary evidence and counts.
	healthy := newProfiledAgent("planner")
	healthy.CredentialKey = filepath.Join(projectDir, ".loom", "agent-profiles", "planner")
	healthy.RestartCount = 2
	healthy.LastExitCode = 1
	healthy.LastError = &agenterr.AgentError{Class: agenterr.OutcomeFromHarness(wrapper.ErrTimeout)}

	if !s.shouldRestart(healthy) {
		t.Fatal("shouldRestart(planner) = false, want true")
	}
	if healthy.RestartCount != 3 {
		t.Errorf("planner RestartCount = %d, want 3 (another profile's wall shields nothing)", healthy.RestartCount)
	}
}

// TestWallShield_MatchesUnboundedBlockClasses pins the shielded set to exactly
// the dispositions whose exhaustion is an UNBOUNDED block (Retry / OnExhaustion
// Block / BlockBudget 0) — today {Timeout, Transient}. If a future table entry
// acquires that shape, this fails and forces an explicit decision rather than
// silently inheriting or missing the shield.
func TestWallShield_MatchesUnboundedBlockClasses(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	s.WallUntil = time.Now().Add(5 * time.Minute)
	s.WallClass = agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)

	outcomes := []agenterr.Outcome{}
	for c := wrapper.ErrNone; c <= wrapper.ErrUnknown; c++ {
		outcomes = append(outcomes, agenterr.OutcomeFromHarness(c))
	}
	for d := agenterr.DomainNone + 1; d <= agenterr.IssueBackendOutageOutcome; d++ {
		outcomes = append(outcomes, agenterr.OutcomeFromDomain(d))
	}

	for _, o := range outcomes {
		disp := agentpolicy.Decide(o)
		want := disp.Decision == agentpolicy.Retry &&
			disp.OnExhaustion == agentpolicy.Block &&
			disp.BlockBudget == 0
		if _, got := s.wallShieldsCountedRetry(ap, o); got != want {
			t.Errorf("wallShieldsCountedRetry(%s) = %v, want %v (unbounded-block shape: %v)",
				o, got, want, want)
		}
	}

	// Stated explicitly because it is a deliberate exclusion, not a fallout of
	// the table: Unknown escalates to FastFail after defaultBlockBudget cycles
	// — bounded and terminal — and it is where a deterministic crash lands, so
	// its budget erosion is the only signal a genuinely broken agent produces.
	if _, ok := s.wallShieldsCountedRetry(ap, agenterr.OutcomeFromHarness(wrapper.ErrUnknown)); ok {
		t.Error("wallShieldsCountedRetry(Unknown) = true, want false")
	}
}

// TestWallShield_NoWallDoesNotShield: the shield is a live-window check. A wall
// that expired between the failure and the decision resolves to the
// pre-existing counted behavior.
func TestWallShield_NoWallDoesNotShield(t *testing.T) {
	s := newAccountWallSupervisor(nil)
	ap := newAccountWallAgent()
	s.WallUntil = time.Now().Add(-time.Second)
	s.WallClass = agenterr.OutcomeFromHarness(wrapper.ErrRateLimited)

	if _, ok := s.wallShieldsCountedRetry(ap, agenterr.OutcomeFromHarness(wrapper.ErrTimeout)); ok {
		t.Error("wallShieldsCountedRetry(Timeout) = true with an expired wall, want false")
	}
}
