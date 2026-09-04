package supervisor

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

func heldSupervisor(t *testing.T, hold *ClaimHold) (*Supervisor, *clitest.MockIssueBackend) {
	t.Helper()
	mock := clitest.NewMockIssueBackend()
	s := &Supervisor{IssueBackend: mock}
	if hold != nil {
		if err := s.SetClaimHold(hold); err != nil {
			t.Fatalf("SetClaimHold: %v", err)
		}
	}
	return s, mock
}

func newHold(actor, reason string, ttl time.Duration) *ClaimHold {
	h := &ClaimHold{Held: true, Actor: actor, Reason: reason, Since: time.Now()}
	if ttl > 0 {
		h.ExpiresAt = h.Since.Add(ttl)
	}
	return h
}

func TestGateClaimsHeld_BlocksPreFlightBeforeAnyBackendCall(t *testing.T) {
	s, mock := heldSupervisor(t, newHold("union-autodeploy", "deploy union tips", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}

	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true while claims are held")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("backend was consulted while held: %#v", mock.Calls)
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld", ap.LastError)
	}
	if ap.LastNoWork {
		t.Fatal("LastNoWork set by a claim hold; it must stay false")
	}
}

func TestGateClaimsHeld_LeavesEveryCounterUntouched(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "maintenance", time.Hour))
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		RestartCount:   4,
		RateRetryCount: 2,
		NoWorkCount:    7,
		BlockCount:     1,
		StopReason:     "",
	}

	if s.gateClaimsHeld(ap) {
		t.Fatal("gateClaimsHeld returned true while held")
	}
	// The restart layer sees the gate outcome next; it must move nothing.
	ap.Mu.Lock()
	s.applyClaimsHeldRestart(ap)
	ap.Mu.Unlock()

	if ap.RestartCount != 4 || ap.RateRetryCount != 2 || ap.NoWorkCount != 7 || ap.BlockCount != 1 {
		t.Fatalf("counters moved: restart=%d rate=%d nowork=%d block=%d",
			ap.RestartCount, ap.RateRetryCount, ap.NoWorkCount, ap.BlockCount)
	}
	if ap.StopReason != "" {
		t.Fatalf("StopReason = %q, want empty (the agent is gated, not stopped)", ap.StopReason)
	}
}

func TestClaimHold_NeverWritesYieldFileOrTouchesProcess(t *testing.T) {
	dir := t.TempDir()
	s, _ := heldSupervisor(t, nil)
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}, WorktreePath: dir, Pid: os.Getpid()}

	if err := s.SetClaimHold(newHold("union-autodeploy", "deploy", time.Hour)); err != nil {
		t.Fatalf("SetClaimHold: %v", err)
	}
	if s.gateClaimsHeld(ap) {
		t.Fatal("gateClaimsHeld returned true while held")
	}

	if IsYieldRequested(dir) {
		t.Fatal("a claim hold wrote a yield file; it must gate the claim path only")
	}
	if ap.Pid != os.Getpid() {
		t.Fatalf("Pid changed to %d; a hold must never touch a running process", ap.Pid)
	}
	if !lockfile.IsProcessRunning(ap.Pid) {
		t.Fatal("the running process died; a hold must never signal an in-flight run")
	}
}

func TestClaimHold_ExpiredDoesNotGateAndClearsFile(t *testing.T) {
	s, mock := heldSupervisor(t, nil)
	cleared := 0
	s.PersistClaimHold = func(h *ClaimHold) error {
		if h == nil {
			cleared++
		}
		return nil
	}
	s.LoadClaimHold(&ClaimHold{
		Held: true, Actor: "oleh", Reason: "stale",
		Since:     time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Minute),
	})

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("an expired hold gated the agent")
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("expired hold was not cleared in memory")
	}
	if cleared == 0 {
		t.Fatal("expired hold did not clear the persisted file")
	}
	// The WARN-and-clear path must fire once, not per observation.
	for i := 0; i < 3; i++ {
		_ = s.ClaimHoldSnapshot()
	}
	if cleared != 1 {
		t.Fatalf("persist(nil) called %d times, want exactly 1", cleared)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("hold evaluation touched the backend: %#v", mock.Calls)
	}
}

func TestClaimHold_ReleaseReEnablesClaiming(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if s.gateClaimsHeld(ap) {
		t.Fatal("gate did not hold")
	}
	if err := s.ReleaseClaimHold("oleh", false); err != nil {
		t.Fatalf("ReleaseClaimHold: %v", err)
	}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("gate still held after release")
	}
}

func TestClaimHold_ForeignReleaseRefusedWithoutForce(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("union-autodeploy", "deploy", time.Hour))

	err := s.ReleaseClaimHold("someone-else", false)
	if err == nil {
		t.Fatal("foreign release succeeded without --force")
	}
	if s.ClaimHoldSnapshot() == nil {
		t.Fatal("refused release still cleared the hold")
	}
	if err := s.ReleaseClaimHold("someone-else", true); err != nil {
		t.Fatalf("forced release failed: %v", err)
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("forced release did not clear the hold")
	}
}

func TestClaimHold_PersistFailureStillAppliesHold(t *testing.T) {
	s, _ := heldSupervisor(t, nil)
	want := errors.New("read-only file system")
	s.PersistClaimHold = func(*ClaimHold) error { return want }

	err := s.SetClaimHold(newHold("oleh", "deploy", time.Hour))
	if !errors.Is(err, want) {
		t.Fatalf("SetClaimHold err = %v, want %v", err, want)
	}
	if h := s.ClaimHoldSnapshot(); !h.Active(time.Now()) {
		t.Fatal("hold was not applied in memory after a persist failure")
	}
}

func TestClaimHold_SnapshotIsACopy(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	snap := s.ClaimHoldSnapshot()
	snap.Actor = "mutated"
	if again := s.ClaimHoldSnapshot(); again.Actor != "oleh" {
		t.Fatalf("Actor = %q; snapshot aliased the supervisor's hold", again.Actor)
	}
}

func TestClaimHold_ActiveIsNilSafe(t *testing.T) {
	var nilHold *ClaimHold
	if nilHold.Active(time.Now()) {
		t.Fatal("nil hold reported active")
	}
	if (&ClaimHold{Held: false}).Active(time.Now()) {
		t.Fatal("released hold reported active")
	}
	indefinite := &ClaimHold{Held: true, Since: time.Now()}
	if !indefinite.Active(time.Now().Add(100 * time.Hour)) {
		t.Fatal("indefinite hold expired")
	}
}

func TestClaimHold_StatusExposesGatedFlag(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("oleh", "deploy", time.Hour))
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	s.Agents = append(s.Agents, ap)

	if s.gateClaimsHeld(ap) {
		t.Fatal("gate did not hold")
	}
	gated, running := s.claimHoldGateCounts()
	if gated != 1 {
		t.Fatalf("gated = %d, want 1", gated)
	}
	if running != 0 {
		t.Fatalf("running = %d, want 0 (no process was ever spawned)", running)
	}
	if !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatal("ClaimsGated is derived from LastError.Class; it was not recorded")
	}
}

func TestClaimsHeld_RestartPolicyIsUncountedFixedRecheck(t *testing.T) {
	d := agentpolicy.Decide(agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome))
	if d.Decision != agentpolicy.RetryUncounted || d.Backoff != agentpolicy.BPClaimsHeld {
		t.Fatalf("disposition = %+v, want RetryUncounted/BPClaimsHeld", d)
	}
	if agentpolicy.QuarantineEligible(agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome)) {
		t.Fatal("ClaimsHeld is quarantine-eligible; a quiesce is not a task fault")
	}

	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	if got := s.claimHoldRecheckBackoff(); got != defaultClaimHoldRecheckInterval {
		t.Fatalf("default recheck = %v, want %v", got, defaultClaimHoldRecheckInterval)
	}
	s.claimHoldRecheckInterval = 10 * time.Millisecond
	if got := s.claimHoldRecheckBackoff(); got != 10*time.Millisecond {
		t.Fatalf("override recheck = %v", got)
	}

	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"},
		LastExitCode: 0,
		LastError:    &agenterr.AgentError{Class: agenterr.OutcomeFromDomain(agenterr.ClaimsHeldOutcome)},
		RestartCount: 3,
		NoWorkCount:  5,
	}
	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false for a claim-hold gate; the agent must keep re-checking")
	}
	if ap.RestartCount != 3 || ap.NoWorkCount != 5 || ap.StopReason != "" {
		t.Fatalf("shouldRestart moved state: restart=%d nowork=%d stop=%q",
			ap.RestartCount, ap.NoWorkCount, ap.StopReason)
	}
	if got := s.computeBackoff(ap); got != 10*time.Millisecond {
		t.Fatalf("computeBackoff = %v, want the fixed claim-hold recheck", got)
	}
}

// ── reload from disk ────────────────────────────────────────────────────────
//
// claim-hold.json is the durable source of truth, so an external edit must
// reach a daemon that already hydrated. These exercise the injected
// ReloadClaimHold seam directly; no file and no daemon are involved.

func TestClaimHoldSnapshotAdoptsExternalRelease(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("union-autodeploy", "deploy union tips", time.Hour))
	persisted := 0
	s.PersistClaimHold = func(*ClaimHold) error { persisted++; return nil }
	// Someone removed the file: the hold is gone on disk.
	s.ReloadClaimHold = func() (*ClaimHold, bool, error) { return nil, true, nil }

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("gateClaimsHeld still gated after the file released the hold")
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("in-memory hold survived an external release")
	}
	if persisted != 0 {
		t.Fatalf("adoption re-persisted %d time(s); the value came from the file", persisted)
	}
}

func TestClaimHoldSnapshotIgnoresOwnWrite(t *testing.T) {
	s, _ := heldSupervisor(t, nil)
	calls := 0
	// The store filters the daemon's own writes out: nothing changed.
	s.ReloadClaimHold = func() (*ClaimHold, bool, error) { calls++; return nil, false, nil }

	if err := s.SetClaimHold(newHold("oleh", "maintenance", time.Hour)); err != nil {
		t.Fatalf("SetClaimHold: %v", err)
	}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	for i := 0; i < 5; i++ {
		if s.gateClaimsHeld(ap) {
			t.Fatal("the supervisor's own write was adopted as an external release")
		}
	}
	if s.ClaimHoldSnapshot() == nil {
		t.Fatal("hold disappeared without the file changing")
	}
	if calls != 1 {
		t.Fatalf("ReloadClaimHold called %d times within %s, want exactly 1", calls, claimHoldReloadInterval)
	}
}

func TestClaimHoldReloadDoesNotResurrectExpired(t *testing.T) {
	s, _ := heldSupervisor(t, nil)
	persisted := 0
	s.PersistClaimHold = func(*ClaimHold) error { persisted++; return nil }
	// A stale record left on disk by a daemon that died before clearing it.
	s.ReloadClaimHold = func() (*ClaimHold, bool, error) {
		return &ClaimHold{
			Held: true, Actor: "oleh", Reason: "stale",
			Since:     time.Now().Add(-2 * time.Hour),
			ExpiresAt: time.Now().Add(-time.Minute),
		}, true, nil
	}

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if !s.gateClaimsHeld(ap) {
		t.Fatal("an expired on-disk hold was resurrected by the reload")
	}
	if s.ClaimHoldSnapshot() != nil {
		t.Fatal("expired on-disk hold adopted as an active hold")
	}
	if persisted != 0 {
		t.Fatalf("adoption re-persisted %d time(s); the value came from the file", persisted)
	}
}

func TestClaimHoldReloadErrorKeepsHold(t *testing.T) {
	s, _ := heldSupervisor(t, newHold("union-autodeploy", "deploy union tips", time.Hour))
	s.ReloadClaimHold = func() (*ClaimHold, bool, error) {
		return nil, false, errors.New("stat: permission denied")
	}

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "falcon", Role: "task"}}
	if s.gateClaimsHeld(ap) {
		t.Fatal("a failed reload dropped the hold; it must fail closed")
	}
	h := s.ClaimHoldSnapshot()
	if h == nil || h.Actor != "union-autodeploy" {
		t.Fatalf("ClaimHoldSnapshot() = %#v, want the in-memory hold kept", h)
	}
}

// ── repo scope ──────────────────────────────────────────────────────────────
//
// A scoped hold cannot decide at the gate (there is no candidate yet), so it
// splits: the gate carries the scope, and the claim path filters the ready
// queue against it. These drive both halves of that split.

// scopedHold is a held-for-these-repos hold with a generous TTL.
func scopedHold(actor, reason string, repos ...string) *ClaimHold {
	h := newHold(actor, reason, time.Hour)
	h.Repos = repos
	return h
}

// runClaimCycle runs the two gates a repo scope spans, in the order
// preFlightSetup runs them: gateClaimsHeld (which stashes the scope) and then
// claimTask (which applies it). Returns whether the agent got work.
func runClaimCycle(s *Supervisor, ap *AgentProcess) bool {
	if !s.gateClaimsHeld(ap) {
		return false
	}
	return s.claimTask(ap, "")
}

func scopedAgent(worktree string, repos ...string) *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: worktree, Role: "task", Repos: repos},
		RoleConfig: cfgpkg.RoleConfig{TaskFilter: "has_design"},
	}
}

func readyIssue(id, repo string, priority int) backend.IssueData {
	return backend.IssueData{
		ID: id, IssueType: "task", Status: "open", Priority: priority,
		Title: id, Design: "plan", SourceRepo: repo,
	}
}

func TestClaimHoldScope_UnheldRepoStillClaims(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "loomcli", 1)}
	ap := scopedAgent("falcon")

	if !runClaimCycle(s, ap) {
		t.Fatalf("a loomcli task was refused under a fleet-db-only hold: %#v", ap.LastError)
	}
	if ap.AssignedTaskID != "PUPPET-1" {
		t.Fatalf("AssignedTaskID = %q, want PUPPET-1", ap.AssignedTaskID)
	}
}

// The regression the scope must never cause: an UNSCOPED hold is still the
// zero-backend-call gate, because that is the case it was built for — quiescing
// the workspace while fleet-db itself is being redeployed.
func TestClaimHoldScope_UnscopedHoldStillIssuesNoReadyQuery(t *testing.T) {
	s, mock := heldSupervisor(t, newHold("union-autodeploy", "deploy union tips", time.Hour))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "loomcli", 1)}
	ap := scopedAgent("falcon")

	if runClaimCycle(s, ap) {
		t.Fatal("an unscoped hold let the agent claim")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("an unscoped hold consulted the backend: %#v", mock.Calls)
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld", ap.LastError)
	}
	if len(ap.HeldRepos) != 0 {
		t.Fatalf("HeldRepos = %v; an unscoped hold has no scope to carry", ap.HeldRepos)
	}
}

func TestClaimHoldScope_AllCandidatesHeldReportsHeldNotNoWork(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{
		readyIssue("PUPPET-1", "fleet-db", 1),
		readyIssue("PUPPET-2", "fleet-db", 2),
	}
	ap := scopedAgent("falcon")

	if runClaimCycle(s, ap) {
		t.Fatal("a held repo's task was claimed")
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld (an emptied queue is not an empty board)", ap.LastError)
	}
	if !strings.Contains(ap.LastError.Message, "fleet-db") {
		t.Fatalf("message = %q, want it to name the held repo", ap.LastError.Message)
	}
	if mock.CallCount("ClaimIssue") != 0 {
		t.Fatalf("a held candidate was claimed anyway: %#v", mock.Calls)
	}
}

// The scope must be a HARD pre-filter. Repo affinity in routing is soft — a
// mismatch still scores above zero and stays selectable — so a held task that
// outranks every alternative must be removed before selection, not down-scored.
func TestClaimHoldScope_HeldTaskNeverSelectedEvenWhenItOutscores(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{
		readyIssue("PUPPET-HELD", "fleet-db", 0), // top priority, and held
		readyIssue("PUPPET-FREE", "loomcli", 4),
	}
	ap := scopedAgent("falcon")

	if !runClaimCycle(s, ap) {
		t.Fatalf("claim refused: %#v", ap.LastError)
	}
	if ap.AssignedTaskID != "PUPPET-FREE" {
		t.Fatalf("AssignedTaskID = %q, want PUPPET-FREE; the hold must be a filter, not a score", ap.AssignedTaskID)
	}
}

// An issue that names no repo cannot be matched against a repo-named hold.
// Deliberate: guessing would gate work the hold never claimed to cover.
func TestClaimHoldScope_IssueWithoutSourceRepoIsClaimable(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-9", "", 1)}
	ap := scopedAgent("falcon")

	if !runClaimCycle(s, ap) {
		t.Fatalf("an issue with no source_repo was gated by a repo-scoped hold: %#v", ap.LastError)
	}
	if ap.AssignedTaskID != "PUPPET-9" {
		t.Fatalf("AssignedTaskID = %q, want PUPPET-9", ap.AssignedTaskID)
	}
}

// An agent bound only to held repos has nothing claimable whatever the board
// holds, so it is gated at Level 1 and pays no backend call.
func TestClaimHoldScope_AgentBoundToHeldReposGatesWithoutBackendCall(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "loomcli", 1)}
	ap := scopedAgent("falcon", "fleet-db")

	if runClaimCycle(s, ap) {
		t.Fatal("an agent bound only to a held repo was allowed to claim")
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("Level 1 should gate before any backend call: %#v", mock.Calls)
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld", ap.LastError)
	}
}

// A partially-held agent still proceeds: one of its repos is claimable.
func TestClaimHoldScope_PartiallyBoundAgentProceedsToTheFilter(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "loomcli", 1)}
	ap := scopedAgent("falcon", "fleet-db", "loomcli")

	if !runClaimCycle(s, ap) {
		t.Fatalf("an agent bound to one held and one free repo was gated: %#v", ap.LastError)
	}
	if ap.AssignedTaskID != "PUPPET-1" {
		t.Fatalf("AssignedTaskID = %q, want PUPPET-1", ap.AssignedTaskID)
	}
}

// Resume is exempt: re-claiming the task this agent already holds is not
// starting new work, which is the only thing a hold refuses.
func TestClaimHoldScope_ResumeSurvivesAHoldOnItsOwnRepo(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	ap := scopedAgent("falcon")
	ap.ResumeTaskID = "PUPPET-RESUME"

	if !runClaimCycle(s, ap) {
		t.Fatalf("resume was refused under a repo-scoped hold: %#v", ap.LastError)
	}
	if ap.AssignedTaskID != "PUPPET-RESUME" {
		t.Fatalf("AssignedTaskID = %q, want PUPPET-RESUME", ap.AssignedTaskID)
	}
	if mock.CallCount("Ready") != 0 {
		t.Fatalf("resume consulted the ready queue: %#v", mock.Calls)
	}
}

// A REQUESTED task is new work, so the hold applies to it exactly as it applies
// to the queue.
func TestClaimHoldScope_RequestedTaskInAHeldRepoIsRefused(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "fleet-db", 1)}
	ap := scopedAgent("falcon")
	ap.RequestedTaskID = "PUPPET-1"

	if runClaimCycle(s, ap) {
		t.Fatal("a requested task in a held repo was claimed")
	}
	if ap.LastError == nil || !ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome) {
		t.Fatalf("LastError = %#v, want ClaimsHeld", ap.LastError)
	}
	if mock.CallCount("ClaimIssue") != 0 {
		t.Fatalf("the held requested task was claimed anyway: %#v", mock.Calls)
	}
}

func TestClaimHoldScope_ClearedBetweenCycles(t *testing.T) {
	s, mock := heldSupervisor(t, scopedHold("union-autodeploy", "deploy fleet-db", "fleet-db"))
	mock.ReadyResult = []backend.IssueData{readyIssue("PUPPET-1", "fleet-db", 1)}
	ap := scopedAgent("falcon")

	if runClaimCycle(s, ap) {
		t.Fatal("a held repo's task was claimed")
	}
	if len(ap.HeldRepos) != 1 {
		t.Fatalf("HeldRepos = %v, want the cycle's scope", ap.HeldRepos)
	}
	s.clearAgentSessionState(ap)
	if ap.HeldRepos != nil {
		t.Fatalf("HeldRepos = %v after clearAgentSessionState; it is per-cycle", ap.HeldRepos)
	}
	// With the hold gone the same queue is claimable again.
	if err := s.ReleaseClaimHold("union-autodeploy", false); err != nil {
		t.Fatalf("ReleaseClaimHold: %v", err)
	}
	if !runClaimCycle(s, ap) {
		t.Fatalf("the released hold still filtered the queue: %#v", ap.LastError)
	}
}

func TestClaimHold_CloneDeepCopiesRepos(t *testing.T) {
	src := scopedHold("union-autodeploy", "deploy", "fleet-db", "loomcli")
	clone := src.clone()
	src.Repos[0] = "mutated"

	if clone.Repos[0] != "fleet-db" {
		t.Fatalf("clone.Repos = %v; clone aliased the caller's backing array", clone.Repos)
	}
	var nilHold *ClaimHold
	if nilHold.clone() != nil {
		t.Fatal("cloning a nil hold produced a non-nil hold")
	}
}

func TestClaimHold_HoldsRepoAndScoped(t *testing.T) {
	var nilHold *ClaimHold
	unscoped := newHold("oleh", "deploy", time.Hour)
	scoped := scopedHold("oleh", "deploy", "fleet-db")
	inactive := &ClaimHold{Held: false, Repos: []string{"fleet-db"}}

	cases := []struct {
		name string
		hold *ClaimHold
		repo string
		want bool
	}{
		{"nil hold holds nothing", nilHold, "fleet-db", false},
		{"a released hold holds nothing", inactive, "fleet-db", false},
		{"an unscoped hold holds every named repo", unscoped, "fleet-db", true},
		{"a scoped hold holds its own repo", scoped, "fleet-db", true},
		{"a scoped hold does not hold another repo", scoped, "loomcli", false},
		{"no repo is never held", scoped, "", false},
		{"no repo is never held, even unscoped", unscoped, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.hold.HoldsRepo(tc.repo); got != tc.want {
				t.Fatalf("HoldsRepo(%q) = %v, want %v", tc.repo, got, tc.want)
			}
		})
	}

	if nilHold.Scoped() || unscoped.Scoped() {
		t.Fatal("a nil or unscoped hold reported a scope")
	}
	if !scoped.Scoped() {
		t.Fatal("a hold naming repos reported no scope")
	}
	if got := ClaimHoldScopeLabel(unscoped); got != "all" {
		t.Fatalf("ClaimHoldScopeLabel(unscoped) = %q, want \"all\"", got)
	}
	if got := ClaimHoldScopeLabel(scopedHold("oleh", "d", "fleet-db", "loomcli")); got != "fleet-db, loomcli" {
		t.Fatalf("ClaimHoldScopeLabel = %q", got)
	}
}
