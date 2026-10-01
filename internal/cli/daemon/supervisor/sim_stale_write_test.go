package supervisor

// Deterministic replay of the real stale-worker issue write (task e034de04)
// through the FleetDB HTTP seam. What runs for real: the Loom FleetDB adapter
// (internal/backend/fleet) and the supervisor's claimTask /
// claimIssueForAgent / releaseAssignedTaskClaim. What is scripted: the worker
// process (fleetsim.ScriptedWorker issuing `loom data close` through the real
// adapter Close), and the post-mortem recovery's lock release, issued as the
// same adapter call RecoverWorktree.releaseFleetIssueLock makes
// (ReleaseIssueLock(task, agentName)). RecoverWorktree itself is not executed:
// it resolves the ambient workspace and runs git clean.
//
// Actor split, made explicit: the supervisor claims and releases with
// X-Actor=<worktree> (local-coder, local-coder2); the worker's CLI writes go
// out as X-Actor=operator@local. The interposer attributes every request to
// the attempt that sent it, which is how the oracle can call the stale
// assign/close superseded even though FleetDB only sees operator@local.
//
// Not claimed: response bodies (not captured; the fake's are source-derived),
// a matched clean control, S9 resetTask, ownership_lost, or any server guard.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
)

const (
	simIssue     = "LOCALMODE-3"
	simOperator  = "operator@local"
	simOldActor  = "local-coder"
	simNewActor  = "local-coder2"
	simWorkspace = "LOCALMODE"
	// simDaemonActor is the daemon process X-Actor of the local-mode stack
	// (test/local-mode/docker-compose.yml LOOM_FLEET_DB_ACTOR). It only appears
	// on reads and on writes that use the configured actor; the captured
	// writes all carry explicit actors.
	simDaemonActor = "local-mode-harness@fixture.local"
)

// simAttempt is one agent attempt: its own Supervisor value (so the
// interposer can attribute requests per attempt) driving the real claim and
// release code, sharing the Sim's fake clock.
type simAttempt struct {
	name string
	s    *Supervisor
	ap   *AgentProcess
}

func newSimAttempt(sim *fleetsim.Sim, name, worktree string) *simAttempt {
	sim.BindAttempt(name, worktree)
	return &simAttempt{
		name: name,
		s:    &Supervisor{IssueBackend: sim.Backend(name, simDaemonActor), Clock: sim.Clock, WorkspaceID: simWorkspace, NodeID: "node-" + worktree},
		ap:   &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}},
	}
}

func (a *simAttempt) claim(sim *fleetsim.Sim, actor string) {
	sim.Go(actor, func() error {
		if !a.s.claimTask(a.ap, "") {
			return fmt.Errorf("claimTask failed: %v", a.ap.LastError)
		}
		return nil
	})
}

func seedSimIssue(sim *fleetsim.Sim) {
	sim.Server.Seed(fleetsim.Issue{ID: simIssue, Title: "Local mode coder task", Design: "approved design", Priority: 2})
}

// preSegment drives the part of the run before the captured segment: the old
// attempt claims, its 300s lock expires while its worker is paused, and the
// claim reaper reverts the issue to open/unassigned. For run1 the reaper time
// is the recorded lock_expired instant and the old claim time matches the old
// session ID (04:17:33); for run2 neither was recorded, so both are placed
// relative to the successor claim and are synthetic.
func preSegment(t *testing.T, run fleetsim.ObservedRun) (*fleetsim.Sim, *simAttempt) {
	t.Helper()
	reapAt := run.ReaperLockExpired
	if reapAt.IsZero() {
		reapAt = run.Writes[0].At.Add(-90 * time.Second)
	}
	oldClaimAt := reapAt.Add(-fleetsim.DefaultLockTTL - 17*time.Second)

	sim := fleetsim.New(oldClaimAt.Add(-time.Second), simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	old := newSimAttempt(sim, run.Name+"/old", simOldActor)

	sim.Clock.Set(oldClaimAt)
	old.claim(sim, "old-claim")
	sim.DrainFIFO()
	if err := sim.Err("old-claim"); err != nil || old.ap.AssignedTaskID != simIssue {
		t.Fatalf("old claim: err=%v assigned=%q", err, old.ap.AssignedTaskID)
	}
	if h := sim.Server.LockHolder(simIssue); h != simOldActor {
		t.Fatalf("lock holder after old claim = %q", h)
	}

	sim.Clock.Set(reapAt)
	if got := sim.Server.ReapStaleClaims(); len(got) != 1 || got[0] != simIssue {
		t.Fatalf("reaper reverted %v at %s, want [%s]", got, reapAt, simIssue)
	}
	return sim, old
}

func deliverAt(t *testing.T, sim *fleetsim.Sim, m fleetsim.Match, at time.Time, what string) fleetsim.Record {
	t.Helper()
	rec, ok := sim.DeliverNext(m, fleetsim.Apply, at)
	if !ok {
		t.Fatalf("no pending request for %s; pending=%+v", what, sim.Pending())
	}
	return rec
}

// S2 (issue side: worker paused past the lock TTL) and S10 (agent writes via
// `loom data`, accepted): calibrated against both captured runs.
func TestFleetSim_S2_S10_ReplaysObservedStaleWorkerWrite(t *testing.T) {
	for _, run := range fleetsim.ObservedRuns {
		t.Run(run.Name, func(t *testing.T) {
			sim, old := preSegment(t, run)
			w := run.Writes

			// Successor supervisor claims through the real ready+claim path.
			succ := newSimAttempt(sim, run.Name+"/successor", simNewActor)
			sim.Clock.Set(w[0].At)
			succ.claim(sim, "successor-claim")
			sim.DrainFIFO()
			if err := sim.Err("successor-claim"); err != nil || succ.ap.AssignedTaskID != simIssue {
				t.Fatalf("successor claim: err=%v assigned=%q", err, succ.ap.AssignedTaskID)
			}

			// Paused old worker resumes and runs `loom data close`.
			worker := fleetsim.NewScriptedWorker(sim, old.name, simOperator,
				fleetsim.CloseStep(simIssue, run.OldSession, "Local mode dogfood implementation completed."))
			var exitCode int
			sim.Go("old-worker", func() error {
				var err error
				exitCode, err = worker.Run(context.Background())
				return err
			})
			deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/assign"), w[1].At, "stale assign")
			deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/close"), w[2].At, "stale close")
			if !sim.Finished("old-worker") || exitCode != 0 || sim.Err("old-worker") != nil {
				t.Fatalf("old worker exit=%d err=%v, want clean exit 0", exitCode, sim.Err("old-worker"))
			}

			// Old supervisor finalizes: the real releaseAssignedTaskClaim, then
			// recovery's ReleaseIssueLock(task, agent) adapter call.
			sim.Go("old-finalize", func() error {
				old.s.releaseAssignedTaskClaim(old.ap, simIssue)
				return nil
			})
			deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/release-lock"), w[3].At, "finalize release-lock")
			var recoverErr error
			sim.Go("old-recover-release", func() error {
				recoverErr = sim.Backend(old.name, simDaemonActor).ReleaseIssueLock(context.Background(), simIssue, simOldActor)
				return nil
			})
			deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/release-lock"), w[4].At, "recovery release-lock")
			if !backend.IsKind(recoverErr, backend.KindConflict) {
				t.Fatalf("recovery release-lock err = %v, want conflict (409)", recoverErr)
			}

			if left := sim.Pending(); len(left) != 0 {
				t.Fatalf("unexpected extra requests: %+v", left)
			}
			if u := sim.Server.Unmodeled(); len(u) != 0 {
				t.Fatalf("scenario left the calibrated surface: %v", u)
			}

			if diffs := fleetsim.CompareWrites(run, sim.Records()); len(diffs) != 0 {
				t.Fatalf("simulated write sequence diverges from capture:\n%s", strings.Join(diffs, "\n"))
			}
			final, holder, _ := sim.Server.Snapshot(simIssue)
			if diffs := fleetsim.CompareFinal(run, final); len(diffs) != 0 {
				t.Fatalf("final state diverges from capture:\n%s", strings.Join(diffs, "\n"))
			}
			// The successor's lock survives the close (CloseIssue does not
			// release it) — that is why both old release-locks are 409.
			if holder != simNewActor {
				t.Fatalf("lock holder at end = %q, want %q", holder, simNewActor)
			}
			// The successor's supervisor still believes it owns a task that
			// the superseded attempt closed.
			if succ.ap.AssignedTaskID != simIssue {
				t.Fatalf("successor AssignedTaskID = %q", succ.ap.AssignedTaskID)
			}

			rep := sim.Check()
			if len(rep.Violations) != 2 {
				t.Fatalf("violations = %v, want stale assign and close", rep.Violations)
			}
			for i, suffix := range []string{"/assign", "/close"} {
				v := rep.Violations[i]
				if v.Invariant != fleetsim.InvNoSupersededWrite || v.Attempt != old.name || !strings.Contains(v.Detail, suffix) {
					t.Fatalf("violation %d = %s", i, v)
				}
			}
			if len(rep.ActorSplits) != 2 {
				t.Fatalf("actor splits = %+v, want the two worker writes", rep.ActorSplits)
			}
			for _, sp := range rep.ActorSplits {
				if sp.WireActor != simOperator || sp.ClaimActor != simOldActor {
					t.Fatalf("actor split = %+v, want wire %s vs claim %s", sp, simOperator, simOldActor)
				}
			}
		})
	}
}

// simRaceOutcome runs the post-reaper state with the successor's claim and
// the old attempt's (worker close → supervisor release) racing, under a
// seeded apply order.
type simRaceOutcome struct {
	trace     string
	violation bool
	anyFlag   bool
	staleOK   bool // some stale assign/close was accepted
	claimed   bool
	// stale writes applied after the successor's claim
	staleAfterClaim bool
}

func runSeededRace(t *testing.T, seed int64) simRaceOutcome {
	t.Helper()
	run := fleetsim.ObservedRuns[0]
	sim, old := preSegment(t, run)
	succ := newSimAttempt(sim, "successor", simNewActor)
	worker := fleetsim.NewScriptedWorker(sim, old.name, simOperator,
		fleetsim.CloseStep(simIssue, run.OldSession, "Local mode dogfood implementation completed."))

	succ.claim(sim, "successor-claim")
	sim.Go("old-attempt", func() error {
		_, _ = worker.Run(context.Background())
		old.s.releaseAssignedTaskClaim(old.ap, simIssue)
		return nil
	})
	sim.DrainSeeded(seed, 10*time.Millisecond)
	if u := sim.Server.Unmodeled(); len(u) != 0 {
		t.Fatalf("seed %d: unmodeled %v", seed, u)
	}

	var out simRaceOutcome
	claimIdx := -1
	var b strings.Builder
	for i, r := range sim.Records() {
		fmt.Fprintf(&b, "%s#%d %s %s %s %d %s\n", r.Attempt, r.AttemptSeq, r.AppliedAt.Format(time.RFC3339Nano), r.Method, r.Path, r.Status, r.Actor)
		if r.Attempt == "successor" && strings.HasSuffix(r.Path, "/claim") && r.Status == 200 {
			claimIdx = i
		}
		stale := r.Attempt == old.name && r.Status/100 == 2 &&
			(strings.HasSuffix(r.Path, "/assign") || strings.HasSuffix(r.Path, "/close"))
		if stale {
			out.staleOK = true
		}
		if stale && claimIdx >= 0 {
			out.staleAfterClaim = true
		}
	}
	out.trace = b.String()
	out.claimed = succ.ap.AssignedTaskID == simIssue
	rep := sim.Check()
	out.violation = rep.Has(fleetsim.InvNoSupersededWrite)
	out.anyFlag = len(rep.Violations) > 0
	return out
}

// S2/S6 across seeded interleavings: the oracle flags NoSupersededWrite
// exactly when a stale worker write is applied after the successor's claim;
// every accepted stale write is flagged (NoWriteWithoutAuthority when no
// successor has claimed yet); both outcomes occur; every seed replays
// byte-identically.
func TestFleetSim_S2_S6_SeededInterleavingsMatchOracle(t *testing.T) {
	var violating, clean int
	for seed := int64(1); seed <= 64; seed++ {
		got := runSeededRace(t, seed)
		if got.violation != got.staleAfterClaim {
			t.Fatalf("seed %d: violation=%v but staleAfterClaim=%v\n%s", seed, got.violation, got.staleAfterClaim, got.trace)
		}
		if got.staleOK != got.anyFlag {
			t.Fatalf("seed %d: accepted stale write=%v but flagged=%v\n%s", seed, got.staleOK, got.anyFlag, got.trace)
		}
		if got.violation && !got.claimed {
			t.Fatalf("seed %d: violation without a successor claim\n%s", seed, got.trace)
		}
		if got.violation {
			violating++
		} else {
			clean++
		}
		if again := runSeededRace(t, seed); again.trace != got.trace {
			t.Fatalf("seed %d is not deterministic:\n%s\nvs\n%s", seed, got.trace, again.trace)
		}
	}
	if violating == 0 || clean == 0 {
		t.Fatalf("seed sweep explored only one outcome: violating=%d clean=%d", violating, clean)
	}
	t.Logf("64 seeds: %d violate NoSupersededWrite, %d clean", violating, clean)
}

// Late apply: the old worker's assign is SENT before the successor claims
// (lower sequence number, earlier fake send time) but the interposer holds it
// until after the claim applies. The server accepts it anyway.
// S2 with a delayed request: see the comment above.
func TestFleetSim_S2_LateAppliedStaleWriteIsAccepted(t *testing.T) {
	run := fleetsim.ObservedRuns[0]
	sim, old := preSegment(t, run)
	worker := fleetsim.NewScriptedWorker(sim, old.name, simOperator,
		fleetsim.CloseStep(simIssue, run.OldSession, "Local mode dogfood implementation completed."))
	sim.Go("old-worker", func() error { _, err := worker.Run(context.Background()); return err })
	held := sim.Pending()
	if len(held) != 1 || !strings.HasSuffix(held[0].Path, "/assign") {
		t.Fatalf("pending = %+v, want the old assign", held)
	}

	succ := newSimAttempt(sim, "successor", simNewActor)
	sim.Clock.Set(run.Writes[0].At)
	succ.claim(sim, "successor-claim")
	for !sim.Finished("successor-claim") {
		deliverAt(t, sim, fleetsim.Req("successor", "", ""), time.Time{}, "successor request")
	}
	if succ.ap.AssignedTaskID != simIssue {
		t.Fatalf("successor did not claim: %v", succ.ap.LastError)
	}
	late := deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/assign"), run.Writes[1].At, "late assign")
	if !late.SentAt.Before(run.Writes[0].At) || late.Status != 200 {
		t.Fatalf("late assign sent=%s status=%d", late.SentAt, late.Status)
	}
	if late.HolderBefore != simNewActor {
		t.Fatalf("late assign applied while holder=%q", late.HolderBefore)
	}
	deliverAt(t, sim, fleetsim.Req(old.name, "POST", "/close"), run.Writes[2].At, "close")
	rep := sim.Check()
	if len(rep.Violations) != 2 || rep.Violations[0].Seq != held[0].Seq {
		t.Fatalf("violations = %v, want the late assign first", rep.Violations)
	}
}

// Design exploration only (Guards are not FleetDB behavior): a server guard
// keyed on X-Actor rejects the stale writes, but it also rejects the
// legitimate owner's own worker, because both workers write as
// operator@local. Attempt-scoped writes need an attempt token on the wire, not
// an X-Actor comparison.
func TestFleetSim_GuardExploration_XActorGuardCannotSeparateAttempts(t *testing.T) {
	run := fleetsim.ObservedRuns[0]
	reapAt := run.ReaperLockExpired
	sim := fleetsim.New(reapAt.Add(-fleetsim.DefaultLockTTL-18*time.Second), simWorkspace,
		fleetsim.Guards{RejectNonHolderWorkflowWrites: true})
	seedSimIssue(sim)
	old := newSimAttempt(sim, "old", simOldActor)
	sim.Clock.Set(reapAt.Add(-fleetsim.DefaultLockTTL - 17*time.Second))
	old.claim(sim, "old-claim")
	sim.DrainFIFO()
	sim.Clock.Set(reapAt)
	sim.Server.ReapStaleClaims()
	succ := newSimAttempt(sim, "successor", simNewActor)
	sim.Clock.Set(run.Writes[0].At)
	succ.claim(sim, "successor-claim")
	sim.DrainFIFO()

	for _, attempt := range []string{"old", "successor"} {
		w := fleetsim.NewScriptedWorker(sim, attempt, simOperator,
			fleetsim.CloseStep(simIssue, "", "Local mode dogfood implementation completed."))
		var code int
		name := attempt + "-worker"
		sim.Go(name, func() error {
			var err error
			code, err = w.Run(context.Background())
			return err
		})
		sim.DrainFIFO()
		if code != 1 || !backend.IsKind(sim.Err(name), backend.KindConflict) {
			t.Fatalf("%s worker exit=%d err=%v, want guard rejection", attempt, code, sim.Err(name))
		}
	}
	final, holder, _ := sim.Server.Snapshot(simIssue)
	if final.Status != "in_progress" || final.Assignee != simNewActor || holder != simNewActor {
		t.Fatalf("final = %+v holder=%q", final, holder)
	}
	if rep := sim.Check(); len(rep.Violations) != 0 {
		t.Fatalf("violations under guard: %v", rep.Violations)
	}
}
