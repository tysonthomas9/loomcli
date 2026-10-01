package supervisor

// Enforcement-map row S11 (task 3c748679, row 11) at the FleetDB HTTP seam:
// "agent edits its lock file to point hooks at another issue; ownership kill
// then exit 0". Today column: hooks write to the other issue; proposed:
// refused, and hooks skipped after the kill.
//
// What runs for real: the supervisor's claimTask, classifyAgentExit
// (cli.CheckLock + runLeftClaimHeld → agent.ClaimStillHeld), killAgentForOwnership,
// and runCompletionHooks (completionHookTarget → taskIDForFinalize →
// cli.ReadLockFile, then executeCompletionHooks), all through the real Loom
// FleetDB adapter (internal/backend/fleet). The lock file is a real
// .agent.lock in a temp worktree, rewritten with cli.UpdateLockTask — the
// agent-side API, which has no owner check (internal/cli/lock.go).
//
// What is scripted: the agent process. Its `loom complete` claim release is
// replayed as the adapter call releaseClaimOnComplete makes
// (ReleaseClaim(lock.TaskID, lock.AgentName)); releaseClaimOnComplete itself
// resolves the process-default backend and is not executed. The ownership
// heartbeat → kill decision is not re-driven: each test starts at
// killAgentForOwnership with no process attached, so StopAgent is a no-op and
// the agent's exit code is supplied. The fake FleetDB is source-derived; no
// response body was observed and no real FleetDB was involved.
//
// Ordering is deterministic: every phase is one Sim actor drained FIFO before
// the next starts, and hook requests are stamped (BeginHooks /
// OwnershipKilled) at send time.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

const simOtherIssue = "LOCALMODE-4"

var errOwnershipLostForSim = errors.New("ownership lease held by another daemon")

type lockHookRun struct {
	sim *fleetsim.Sim
	a1  *simAttempt
	b1  *simAttempt
}

// newLockHookRun: attempt a1 (local-coder, close-only hook pipeline) claims
// simIssue; attempt b1 (local-coder2) claims simOtherIssue. a1's worktree has
// the lock file the agent writes after claiming.
func newLockHookRun(t *testing.T) *lockHookRun {
	t.Helper()
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newSimAttempt(sim, "a1", simOldActor)
	a1.s.ConfigSnapshot = func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} }
	a1.ap.Entry.Backend = "claude"
	a1.ap.Entry.Hooks = &domain.AgentHooks{OnComplete: []domain.AgentHookAction{{Type: domain.AgentHookActionClose}}}
	a1.ap.WorktreePath = t.TempDir()
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	if err := sim.Err("a1-claim"); err != nil || a1.ap.AssignedTaskID != simIssue {
		t.Fatalf("a1 claim: err=%v assigned=%q", err, a1.ap.AssignedTaskID)
	}
	writeLockFile(t, a1.ap.WorktreePath, &cli.LockInfo{AgentName: simOldActor, TaskID: simIssue, TaskTitle: "Local mode coder task"})

	sim.Server.Seed(fleetsim.Issue{ID: simOtherIssue, Title: "Sibling task", Design: "approved design", Priority: 2})
	b1 := newSimAttempt(sim, "b1", simNewActor)
	b1.claim(sim, "b1-claim")
	sim.DrainFIFO()
	if err := sim.Err("b1-claim"); err != nil || b1.ap.AssignedTaskID != simOtherIssue {
		t.Fatalf("b1 claim: err=%v assigned=%q", err, b1.ap.AssignedTaskID)
	}
	return &lockHookRun{sim: sim, a1: a1, b1: b1}
}

// agentEditsLock is the agent rewriting its own lock file's task.
func (r *lockHookRun) agentEditsLock(t *testing.T, taskID string) {
	t.Helper()
	if err := cli.UpdateLockTask(r.a1.ap.WorktreePath, taskID, "redirected"); err != nil {
		t.Fatalf("UpdateLockTask: %v", err)
	}
}

// agentCompletes replays `loom complete`'s claim release for whatever task the
// lock file names, as the lock's agent.
func (r *lockHookRun) agentCompletes(t *testing.T) {
	t.Helper()
	info, err := cli.ReadLockFile(r.a1.ap.WorktreePath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	r.sim.Go("a1-complete", func() error {
		return r.a1.s.IssueBackend.(backend.ClaimReleaser).ReleaseClaim(context.Background(), info.TaskID, info.AgentName)
	})
	r.sim.DrainFIFO()
	if err := r.sim.Err("a1-complete"); err != nil {
		t.Fatalf("loom complete release: %v", err)
	}
}

// exitPath runs the supervisor's post-exit sequence from spawnAndWait up to
// finalize: classifyAgentExit, then runCompletionHooks in a hook phase.
// Returns the effective exit code.
func (r *lockHookRun) exitPath(t *testing.T, exitCode int) int {
	t.Helper()
	r.sim.Go("a1-classify", func() error { r.a1.s.classifyAgentExit(r.a1.ap, exitCode); return nil })
	r.sim.DrainFIFO()
	r.sim.BeginHooks("a1")
	got := exitCode
	r.sim.Go("a1-hooks", func() error { got = r.a1.s.runCompletionHooks(r.a1.ap, exitCode); return nil })
	r.sim.DrainFIFO()
	r.sim.EndHooks("a1")
	return got
}

// wantHookClose checks the hook phase sent exactly the adapter's close pair —
// FleetBackend.Close posts /assign {"assignee":""} (its best-effort claim
// release) and then /close — both accepted on issue, and that ok holds for
// each.
func (r *lockHookRun) wantHookClose(t *testing.T, issue string, ok func(fleetsim.Record) bool) {
	t.Helper()
	hw := r.hookWrites()
	if len(hw) != 2 || !strings.HasSuffix(hw[0].Path, "/assign") || !strings.HasSuffix(hw[1].Path, "/close") {
		t.Fatalf("hook writes = %+v, want assign then close", hw)
	}
	for _, rec := range hw {
		if rec.IssueID != issue || rec.Status != 200 || !ok(rec) {
			t.Fatalf("hook write %s %s status=%d authority=%q claimed=%q afterKill=%v",
				rec.Method, rec.Path, rec.Status, rec.AuthorityBefore, rec.ClaimedBefore, rec.AfterOwnershipKill)
		}
	}
}

func (r *lockHookRun) hookWrites() []fleetsim.Record {
	var out []fleetsim.Record
	for _, rec := range r.sim.Records() {
		if rec.Hook && rec.Method != "GET" {
			out = append(out, rec)
		}
	}
	return out
}

// violationSet maps invariant → count, for exact comparison.
func violationSet(rep fleetsim.Report) map[string]int {
	m := map[string]int{}
	for _, v := range rep.Violations {
		m[v.Invariant]++
	}
	return m
}

func wantViolations(t *testing.T, rep fleetsim.Report, want map[string]int) {
	t.Helper()
	got := violationSet(rep)
	if len(got) != len(want) {
		t.Fatalf("violations = %v, want %v", rep.Violations, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("violations = %v, want %v", rep.Violations, want)
		}
	}
	for _, v := range rep.Violations {
		if v.Attempt != "a1" {
			t.Fatalf("violation attributed to %q, want a1: %s", v.Attempt, v)
		}
	}
}

// S11 lock-file redirect, today: the agent points its lock file at a sibling
// attempt's task, then runs `loom complete`. The release goes to the sibling's
// task under a1's actor and is a no-op (not a1's assignee); classify reads the
// redirected task, finds no claim of a1's on it, and calls the run clean, so
// a1's own still-held claim on simIssue escapes incomplete-run detection; the
// close hook then closes b1's task while b1 holds authority.
func TestFleetSim_S11_LockFileRedirectsCompletionHooks(t *testing.T) {
	r := newLockHookRun(t)
	r.agentEditsLock(t, simOtherIssue)
	r.agentCompletes(t)
	if code := r.exitPath(t, 0); code != 0 {
		t.Fatalf("effective exit = %d, want 0 (hooks succeeded)", code)
	}
	if r.a1.ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil: classify judged the redirected task, not a1's claim", r.a1.ap.LastError)
	}
	r.wantHookClose(t, simOtherIssue, func(rec fleetsim.Record) bool {
		return rec.AuthorityBefore == "b1" && rec.ClaimedBefore == simIssue
	})
	if is, _, _ := r.sim.Server.Snapshot(simOtherIssue); is.Status != "closed" {
		t.Fatalf("%s status = %s, want closed by a1's hook", simOtherIssue, is.Status)
	}
	if is, holder, _ := r.sim.Server.Snapshot(simIssue); is.Status != "in_progress" || is.Assignee != simOldActor || holder != simOldActor {
		t.Fatalf("%s = %s/%s holder %q, want a1's claim still held", simIssue, is.Status, is.Assignee, holder)
	}
	wantViolations(t, r.sim.Check(), map[string]int{
		fleetsim.InvHookTargetsOwnClaim: 2,
		fleetsim.InvNoSupersededWrite:   2,
	})
}

// S11 lock-file control: identical run with the lock file untouched. The
// hook closes a1's own task and b1's task is untouched. The one remaining
// violation is the row-11 baseline gap, not S11: `loom complete` released the
// claim before the supervisor's hooks ran, so even a correct hook write is
// accepted with no live authority (NoWriteWithoutAuthority).
func TestFleetSim_S11_LockFileControl(t *testing.T) {
	r := newLockHookRun(t)
	r.agentCompletes(t)
	if code := r.exitPath(t, 0); code != 0 || r.a1.ap.LastError != nil {
		t.Fatalf("exit=%d LastError=%v, want clean", code, r.a1.ap.LastError)
	}
	r.wantHookClose(t, simIssue, func(rec fleetsim.Record) bool {
		return rec.ClaimedBefore == simIssue && rec.AuthorityBefore == "" && !rec.AfterOwnershipKill
	})
	if is, holder, _ := r.sim.Server.Snapshot(simOtherIssue); is.Status != "in_progress" || is.Assignee != simNewActor || holder != simNewActor {
		t.Fatalf("%s = %s/%s holder %q, want b1's claim untouched", simOtherIssue, is.Status, is.Assignee, holder)
	}
	wantViolations(t, r.sim.Check(), map[string]int{fleetsim.InvNoWriteWithoutAuthority: 2})
}

// S11 hazard 6, today: `loom complete` has released a1's claim when the
// supervisor kills the agent for lost ownership; the agent exits 0.
// classifyAgentExit overwrites the kill's LastError with nil (exit 0, claim
// no longer held), so completionHookTarget sees a clean run and the close
// hook certifies it.
func TestFleetSim_S11_OwnershipKillThenExitZeroRunsHooks(t *testing.T) {
	r := newLockHookRun(t)
	r.agentCompletes(t)
	r.a1.s.killAgentForOwnership(r.a1.ap, "verifiably_lost", errOwnershipLostForSim)
	r.sim.OwnershipKilled("a1")
	if r.a1.ap.LastError == nil {
		t.Fatal("killAgentForOwnership left LastError nil")
	}
	if code := r.exitPath(t, 0); code != 0 {
		t.Fatalf("effective exit = %d, want 0", code)
	}
	if r.a1.ap.LastError != nil {
		t.Fatalf("LastError = %v, want nil: classify overwrote the ownership kill", r.a1.ap.LastError)
	}
	r.wantHookClose(t, simIssue, func(rec fleetsim.Record) bool {
		return rec.AfterOwnershipKill && rec.ClaimedBefore == simIssue
	})
	wantViolations(t, r.sim.Check(), map[string]int{
		fleetsim.InvNoHookAfterOwnershipKill: 2,
		fleetsim.InvNoWriteWithoutAuthority:  2,
	})
}

// S11 hazard 6 controls. Without the kill, the same run's hooks are not
// judged as post-kill (only the baseline gap remains). With the kill but the
// claim still held, classify's incomplete-run arm replaces the kill error and
// hooks are skipped — the skip follows claim state, not the kill.
func TestFleetSim_S11_OwnershipKillControls(t *testing.T) {
	t.Run("no kill", func(t *testing.T) {
		r := newLockHookRun(t)
		r.agentCompletes(t)
		if code := r.exitPath(t, 0); code != 0 || r.a1.ap.LastError != nil {
			t.Fatalf("exit=%d LastError=%v, want clean", code, r.a1.ap.LastError)
		}
		r.wantHookClose(t, simIssue, func(rec fleetsim.Record) bool { return !rec.AfterOwnershipKill })
		wantViolations(t, r.sim.Check(), map[string]int{fleetsim.InvNoWriteWithoutAuthority: 2})
	})
	t.Run("kill with claim held", func(t *testing.T) {
		r := newLockHookRun(t)
		r.a1.s.killAgentForOwnership(r.a1.ap, "verifiably_lost", errOwnershipLostForSim)
		r.sim.OwnershipKilled("a1")
		r.exitPath(t, 0)
		if !isIncompleteRun(r.a1.ap) {
			t.Fatalf("LastError = %v, want IncompleteRun (kill error replaced)", r.a1.ap.LastError)
		}
		if hw := r.hookWrites(); len(hw) != 0 {
			t.Fatalf("hook writes = %+v, want none", hw)
		}
		wantViolations(t, r.sim.Check(), map[string]int{})
	})
}
