package supervisor

// Expected reproductions of enforcement-map rows (task 3c748679) at the
// FleetDB HTTP seam. Each test pins CURRENT behavior — the "today" column of
// the map — so a fix must flip the assertion deliberately. Scope (coordinator
// decision, 2026-10-01): S1, issue-side S2, S5, S6, S8, S9, S10. Deferred to a
// separate follow-up: S3, S4, S7, S11, S12, S13 and the control-plane,
// lock-file and completion-hook portions of rows not exercised here.
//
// Row → test:
//   S1  TestFleetSim_S1_OwnershipFailsOpen
//   S2  TestFleetSim_S2_S10_ReplaysObservedStaleWorkerWrite (calibrated),
//       TestFleetSim_S2_S6_SeededInterleavingsMatchOracle,
//       TestFleetSim_S2_LateAppliedStaleWriteIsAccepted
//   S5  TestFleetSim_S5_SameActorReclaimInheritsSilently
//   S6  TestFleetSim_S6_WriteAfterReleaseBeforeReacquireIsAccepted
//   S8  TestFleetSim_S8_WorkerHeartbeatOwnershipLostIsIgnored
//   S9  TestFleetSim_S9_DelayedResetClearsSuccessorClaim
//   S10 TestFleetSim_S2_S10_ReplaysObservedStaleWorkerWrite (calibrated),
//       TestFleetSim_S10_WorkerWritesUnclaimedIssue
//
// Only S2/S10's replay is calibrated against a real capture (e034de04). Every
// other pin rests on the Loom code under test plus the fake's source-derived
// FleetDB semantics; none is verified against a real FleetDB, and response
// bodies are never observed ones.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
)

const simCloseReason = "Local mode dogfood implementation completed."

// S1, today: spawn proceeds (fail open) when the control store is absent, and
// when the server does not support ownership leases (404 → ErrNotFound).
func TestFleetSim_S1_OwnershipFailsOpen(t *testing.T) {
	t.Run("store absent", func(t *testing.T) {
		s := &Supervisor{WorkspaceID: simWorkspace}
		ap := &AgentProcess{}
		ap.Entry.Worktree = simOldActor
		if got := s.acquireAgentOwnership(ap); got != ownershipAcquired {
			t.Fatalf("acquire with nil ControlStore = %v, want acquired (fail open)", got)
		}
	})
	t.Run("ownership unsupported", func(t *testing.T) {
		sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{OwnershipLeasesUnsupported: true})
		a := newSimAttempt(sim, "a1", simOldActor)
		a.s.ControlStore = sim.ControlStore("a1", simDaemonActor)
		var got ownershipAcquireOutcome
		sim.Go("acquire", func() error { got = a.s.acquireAgentOwnership(a.ap); return nil })
		rec := deliverAt(t, sim, fleetsim.Req("a1", "POST", "/agent-ownership-leases/"+simOldActor+"/acquire"), time.Time{}, "acquire")
		sim.Quiesce()
		if rec.Status != 404 {
			t.Fatalf("acquire status = %d, want 404", rec.Status)
		}
		if got != ownershipAcquired {
			t.Fatalf("acquire outcome = %v, want acquired (fail open)", got)
		}
		if a.ap.OwnershipLeaseToken != "" || a.ap.OwnershipFencingToken != 0 {
			t.Fatalf("lease state set without a lease: token=%q fence=%d", a.ap.OwnershipLeaseToken, a.ap.OwnershipFencingToken)
		}
	})
}

// S5, today: a new attempt of the same agent re-claims its own in_progress
// task through the resume path; FleetDB treats it as an idempotent same-actor
// re-claim (200, no event), so nothing records the generation change. The old
// attempt's late write is then indistinguishable on the wire and accepted.
func TestFleetSim_S5_SameActorReclaimInheritsSilently(t *testing.T) {
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newSimAttempt(sim, "a1", simOldActor)
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	worker := fleetsim.NewScriptedWorker(sim, "a1", simOperator, fleetsim.CloseStep(simIssue, "", simCloseReason))
	sim.Go("a1-worker", func() error { _, err := worker.Run(context.Background()); return err })
	sim.Pending() // a1's assign is in flight when the daemon restarts

	eventsBefore := len(sim.Server.Events())
	sim.Clock.Advance(time.Minute)
	a2 := newSimAttempt(sim, "a2", simOldActor)
	a2.ap.ResumeTaskID = simIssue
	a2.claim(sim, "a2-claim")
	claim := deliverAt(t, sim, fleetsim.Req("a2", "POST", "/claim"), time.Time{}, "a2 re-claim")
	if claim.Status != 200 || a2.ap.AssignedTaskID != simIssue {
		t.Fatalf("a2 re-claim status=%d assigned=%q", claim.Status, a2.ap.AssignedTaskID)
	}
	if got := len(sim.Server.Events()); got != eventsBefore {
		t.Fatalf("re-claim emitted %d events, want none (idempotent same-actor claim)", got-eventsBefore)
	}

	deliverAt(t, sim, fleetsim.Req("a1", "POST", "/assign"), time.Time{}, "a1 assign")
	closeRec := deliverAt(t, sim, fleetsim.Req("a1", "POST", "/close"), time.Time{}, "a1 close")
	if closeRec.Status != 200 {
		t.Fatalf("a1 late close status = %d, want accepted", closeRec.Status)
	}
	rep := sim.Check()
	if !rep.Has(fleetsim.InvNoSupersededWrite) {
		t.Fatalf("oracle missed a1's write under a2's authority: %v", rep.Violations)
	}
	for _, v := range rep.Violations {
		if v.Attempt != "a1" {
			t.Fatalf("unexpected violation %s", v)
		}
	}
}

// S6, today: a write sent before the attempt's lock release but applied after
// it — with no reacquire in between — is accepted with nobody holding
// authority.
func TestFleetSim_S6_WriteAfterReleaseBeforeReacquireIsAccepted(t *testing.T) {
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newSimAttempt(sim, "a1", simOldActor)
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()

	worker := fleetsim.NewScriptedWorker(sim, "a1", simOperator, fleetsim.CloseStep(simIssue, "", simCloseReason))
	sim.Go("a1-worker", func() error { _, err := worker.Run(context.Background()); return err })
	sim.Pending()
	sim.Go("a1-finalize", func() error { a1.s.releaseAssignedTaskClaim(a1.ap, simIssue); return nil })
	rel := deliverAt(t, sim, fleetsim.Req("a1", "POST", "/release-lock"), time.Time{}, "release-lock")
	if rel.Status != 204 || sim.Server.LockHolder(simIssue) != "" {
		t.Fatalf("release-lock status=%d holder=%q", rel.Status, sim.Server.LockHolder(simIssue))
	}
	deliverAt(t, sim, fleetsim.Req("a1", "POST", "/assign"), time.Time{}, "late assign")
	closeRec := deliverAt(t, sim, fleetsim.Req("a1", "POST", "/close"), time.Time{}, "late close")
	if closeRec.Status != 200 || closeRec.HolderBefore != "" {
		t.Fatalf("late close status=%d holderBefore=%q, want accepted with no lock", closeRec.Status, closeRec.HolderBefore)
	}
	rep := sim.Check()
	if len(rep.Violations) != 2 {
		t.Fatalf("violations = %v, want late assign and close", rep.Violations)
	}
	for _, v := range rep.Violations {
		if v.Invariant != fleetsim.InvNoWriteWithoutAuthority {
			t.Fatalf("violation %s, want %s", v, fleetsim.InvNoWriteWithoutAuthority)
		}
	}
}

// S8, today: after a successor took the task, the old attempt's worker
// heartbeat gets HTTP 200 with success=false/ownership_lost (body shape from
// the pinned storage/worker.go, not observed). Loom's control-plane client
// discards the body, so the supervisor neither stops the agent nor records an
// error, and keeps heartbeating.
func TestFleetSim_S8_WorkerHeartbeatOwnershipLostIsIgnored(t *testing.T) {
	run := fleetsim.ObservedRuns[0]
	sim, old := preSegment(t, run)
	succ := newSimAttempt(sim, "successor", simNewActor)
	succ.claim(sim, "successor-claim")
	sim.DrainFIFO()
	if task, ok := sim.Server.WorkerTask(simOldActor); !ok || task != simIssue {
		t.Fatalf("old worker registry = %q,%v; want still pointing at %s", task, ok, simIssue)
	}

	old.s.ControlStore = sim.ControlStore(old.name, simDaemonActor)
	old.s.Shutdown = make(chan struct{})
	interval := 30 * time.Second
	stop := old.s.startWorkerHeartbeatEvery(old.ap, interval)
	var stopOnce sync.Once
	defer stopOnce.Do(stop)

	for i := 1; i <= 2; i++ {
		sim.Clock.BlockUntilWaiters(1)
		sim.Clock.Advance(interval)
		ps := sim.AwaitPending(1)
		if !strings.HasSuffix(ps[0].Path, "/workers/"+simOldActor+"/heartbeat") {
			t.Fatalf("beat %d: pending = %+v", i, ps)
		}
		rec := sim.Deliver(ps[0].Seq, fleetsim.Apply, time.Time{})
		if rec.Status != 200 || !strings.Contains(rec.RespBody, `"ownership_lost"`) {
			t.Fatalf("beat %d: status=%d body=%s", i, rec.Status, rec.RespBody)
		}
	}
	stopOnce.Do(stop)

	old.ap.Mu.Lock()
	defer old.ap.Mu.Unlock()
	if old.ap.LastError != nil || old.ap.StopReason != "" {
		t.Fatalf("supervisor reacted to ownership_lost: err=%v stop=%q", old.ap.LastError, old.ap.StopReason)
	}
}

// S9, today (not observed in any real run; e034de04 leaves S9 unverified):
// a1's lock is released at finalize, the reaper reopens the task, a2 claims
// it, then a1's delayed post-mortem resetTask lands. resetTask's adapter calls
// are Get then Update(status=open, assignee=""); FleetBackend.Update reads the
// CURRENT assignee and posts /release as that actor, so a1's reset releases
// a2's claim under a2's identity. resetTask itself (unexported, inside
// agent.RecoverWorktree) is replayed as those exact adapter calls.
func TestFleetSim_S9_DelayedResetClearsSuccessorClaim(t *testing.T) {
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newSimAttempt(sim, "a1", simOldActor)
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	sim.Go("a1-finalize", func() error { a1.s.releaseAssignedTaskClaim(a1.ap, simIssue); return nil })
	sim.DrainFIFO()
	sim.Clock.Advance(fleetsim.ReaperGrace)
	if got := sim.Server.ReapStaleClaims(); len(got) != 1 {
		t.Fatalf("reaper reverted %v", got)
	}

	a2 := newSimAttempt(sim, "a2", simNewActor)
	a2.claim(sim, "a2-claim")
	sim.DrainFIFO()
	if a2.ap.AssignedTaskID != simIssue || sim.Server.LockHolder(simIssue) != simNewActor {
		t.Fatalf("a2 claim failed: %v", a2.ap.LastError)
	}

	ib := sim.Backend("a1", simDaemonActor)
	var resetErr error
	sim.Go("a1-reset", func() error {
		ctx := context.Background()
		if d, err := ib.Get(ctx, simIssue); err == nil && d != nil &&
			(d.Status == "review" || d.Status == "closed" || d.Status == "blocked") {
			return nil
		}
		open, empty := "open", ""
		resetErr = ib.Update(ctx, simIssue, backend.UpdateParams{Status: &open, Assignee: &empty})
		return nil
	})
	sim.DrainFIFO()
	if resetErr != nil {
		t.Fatalf("reset err = %v", resetErr)
	}

	var release *fleetsim.Record
	for _, r := range sim.Records() {
		if r.Attempt == "a1" && strings.HasSuffix(r.Path, "/release") {
			rr := r
			release = &rr
		}
	}
	if release == nil || release.Status != 204 || release.Actor != simNewActor {
		t.Fatalf("a1 release = %+v, want 204 sent as X-Actor %s", release, simNewActor)
	}
	final, holder, _ := sim.Server.Snapshot(simIssue)
	if final.Status != "open" || final.Assignee != "" || holder != "" {
		t.Fatalf("final = %+v holder=%q, want a2's claim cleared", final, holder)
	}
	if a2.ap.AssignedTaskID != simIssue {
		t.Fatal("a2 no longer believes it owns the task")
	}
	rep := sim.Check()
	if !rep.Has(fleetsim.InvNoSupersededWrite) {
		t.Fatalf("oracle missed a1's release under a2's authority: %v", rep.Violations)
	}
	impersonated := false
	for _, sp := range rep.ActorSplits {
		if sp.Attempt == "a1" && sp.WireActor == simNewActor && strings.HasSuffix(sp.Path, "/release") {
			impersonated = true
		}
	}
	if !impersonated {
		t.Fatalf("actor splits = %+v, want a1 writing as %s", rep.ActorSplits, simNewActor)
	}
}

// S10, today: an agent's `loom data close` on an issue its attempt never
// claimed is accepted. (The IPC half of S10 — daemon_ipc issue binding — is
// not on this seam and is not exercised here.)
func TestFleetSim_S10_WorkerWritesUnclaimedIssue(t *testing.T) {
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	sim.Server.Seed(fleetsim.Issue{ID: "LOCALMODE-4", Title: "other", Design: "d", Priority: 3})
	a1 := newSimAttempt(sim, "a1", simOldActor)
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	if a1.ap.AssignedTaskID != simIssue {
		t.Fatalf("a1 claimed %q", a1.ap.AssignedTaskID)
	}
	w := fleetsim.NewScriptedWorker(sim, "a1", simOperator, fleetsim.CloseStep("LOCALMODE-4", "", simCloseReason))
	var code int
	sim.Go("a1-worker", func() error {
		var err error
		code, err = w.Run(context.Background())
		return err
	})
	sim.DrainFIFO()
	other, _, _ := sim.Server.Snapshot("LOCALMODE-4")
	if code != 0 || other.Status != "closed" {
		t.Fatalf("worker exit=%d other=%+v, want accepted close", code, other)
	}
	rep := sim.Check()
	if len(rep.Violations) != 2 {
		t.Fatalf("violations = %v, want assign and close on LOCALMODE-4", rep.Violations)
	}
	for _, v := range rep.Violations {
		if v.Invariant != fleetsim.InvNoWriteWithoutAuthority || !strings.Contains(v.Detail, "LOCALMODE-4") {
			t.Fatalf("violation %s", v)
		}
	}
}
