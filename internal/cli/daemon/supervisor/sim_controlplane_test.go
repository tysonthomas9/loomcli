package supervisor

// Control-plane expected reproductions of enforcement-map rows (task 3c748679)
// at the FleetDB HTTP seam: S2 (control-plane portions), S3, S4, S7, S13. S12
// runs in package daemon (daemon_ipc_sim_test.go) because it needs the real
// IPC lease gate. Each test pins CURRENT behavior and has an in-order or
// fault-free control that must stay clean.
//
// What runs for real: the Loom control-plane client (internal/infra/fleetdb),
// the Loom issue adapter, and these supervisor methods —
// acquireAgentOwnership, releaseAgentOwnership, heartbeatAgentOwnership
// (with its verify / re-acquire / bounded ride-out / kill path),
// doOwnershipHeartbeat, claimTask, createControlPlaneAgentSession,
// markControlPlaneAgentSessionRunning, takeAgentSessionForFinalize and
// completeControlPlaneAgentSession. Not executed: superviseAgent's loop, the
// spawn itself, finalizeAgentSession's local-session/transcript half, and the
// startup orphan sweep (local process handling, not on this seam).
//
// FAKE-ONLY. Every FleetDB answer in this file comes from the fake's
// source-derived control-plane model (fleetsim/controlplane.go, fleet-db
// 40e8431d); no control-plane request was ever captured from a real FleetDB,
// so none of these pins is server conformance evidence.
//
// Row → test:
//   S2  TestFleetSim_S2_SlowLocalClockRidesOutPastServerExpiry (+ control),
//       TestFleetSim_S2_PausedDaemonSpawnsAfterOwnershipMoved (+ control)
//   S3  TestFleetSim_S3_DelayedReleaseEndsSameOwnerReacquire (+ control)
//   S4  TestFleetSim_S4_LostFinalizeStrandsUntilLeaseReaper (+ control)
//   S7  TestFleetSim_S7_DelayedSessionWritesAfterFinalizeOrSupersession (+ control)
//   S13 TestFleetSim_S13_RestartedDaemonWaitsOutOldLeaseWhileOrphanWrites (+ control)

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor/fleetsim"
	"github.com/tysonthomas9/loomcli/internal/clock"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Owner IDs as resolveNodeID shapes them (loom-supervisor-<host>-<pid>). A
// restarted daemon on the same host gets a new pid, hence a new owner.
const (
	simNodeA        = "loom-supervisor-host-a-101"
	simNodeARestart = "loom-supervisor-host-a-102"
	simNodeB        = "loom-supervisor-host-b-202"
	simSession      = "sess-a1"
)

// newCPAttempt is a simAttempt whose supervisor also has the real
// control-plane client. live marks the agent process as running for the
// verify-before-kill path (Cmd non-nil, Pid non-zero, nothing spawned;
// StopAgent's Process==nil guard keeps kills side-effect-free).
func newCPAttempt(sim *fleetsim.Sim, name, worktree, node string, clk clock.Clock, live bool) *simAttempt {
	sim.BindAttempt(name, worktree)
	if clk == nil {
		clk = sim.Clock
	}
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig { return &cfgpkg.DaemonConfig{} },
		IssueBackend:   sim.Backend(name, simDaemonActor),
		ControlStore:   sim.ControlStore(name, simDaemonActor),
		Clock:          clk,
		WorkspaceID:    simWorkspace,
		NodeID:         node,
		Shutdown:       make(chan struct{}),
		EmitEvent:      func(events.Event) {},
	}
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: worktree, Role: "task"}}
	if live {
		ap.Cmd, ap.Pid = &exec.Cmd{}, 4242
	}
	return &simAttempt{name: name, s: s, ap: ap}
}

// runActor runs fn as a Sim actor and applies every request it makes, in
// order, at the current fake time.
func runActor(t *testing.T, sim *fleetsim.Sim, name string, fn func()) {
	t.Helper()
	sim.Go(name, func() error { fn(); return nil })
	sim.DrainFIFO()
	if !sim.Finished(name) {
		t.Fatalf("actor %s did not finish", name)
	}
}

func acquireOwnership(t *testing.T, sim *fleetsim.Sim, a *simAttempt, name string) (ownershipAcquireOutcome, fleetsim.Record) {
	t.Helper()
	var got ownershipAcquireOutcome
	sim.Go(name, func() error { got = a.s.acquireAgentOwnership(a.ap); return nil })
	rec := deliverAt(t, sim, fleetsim.Req(a.name, "POST", "/acquire"), time.Time{}, name)
	sim.Quiesce()
	return got, rec
}

// pendingFor returns the earliest pending request matching m.
func pendingFor(t *testing.T, sim *fleetsim.Sim, m fleetsim.Match, what string) fleetsim.Pending {
	t.Helper()
	for _, p := range sim.Pending() {
		if m(p) {
			return p
		}
	}
	t.Fatalf("no pending request for %s; pending=%+v", what, sim.Pending())
	return fleetsim.Pending{}
}

// sessionPatch matches a PATCH of the session asking for status.
func sessionPatch(attempt, status string) fleetsim.Match {
	return func(p fleetsim.Pending) bool {
		return p.Attempt == attempt && p.Method == "PATCH" && strings.HasPrefix(p.Path, "/agent-sessions/") &&
			strings.Contains(p.Body, `"status":"`+status+`"`)
	}
}

// heartbeatOwnership runs the real heartbeatAgentOwnership once, resolving
// each request it sends with d (in send order) until it returns.
func heartbeatOwnership(t *testing.T, sim *fleetsim.Sim, a *simAttempt, name string, d fleetsim.Delivery) (bool, []fleetsim.Record) {
	t.Helper()
	var keep bool
	sim.Go(name, func() error { keep = a.s.heartbeatAgentOwnership(a.ap, defaultLeaseTTL); return nil })
	var recs []fleetsim.Record
	for !sim.Finished(name) {
		ps := sim.Pending()
		if len(ps) == 0 {
			t.Fatalf("%s: blocked with nothing pending", name)
		}
		dd := d
		if !strings.Contains(ps[0].Path, "/heartbeat") {
			dd = fleetsim.Apply // the verify path's re-acquire reaches the server
		}
		recs = append(recs, sim.Deliver(ps[0].Seq, dd, time.Time{}))
	}
	return keep, recs
}

func onlyViolations(t *testing.T, rep fleetsim.Report, want map[string]int) {
	t.Helper()
	got := map[string]int{}
	for _, v := range rep.Violations {
		got[v.Invariant]++
	}
	if len(got) != len(want) {
		t.Fatalf("violations = %v, want counts %v", rep.Violations, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("violations = %v, want counts %v", rep.Violations, want)
		}
	}
}

// rateClock is a local clock that runs at num/den of the fake (server) clock
// from epoch: a slow local clock (a_fail_drift). Only Now is skewed; the
// paths driven here read nothing else.
type rateClock struct {
	base     *clock.Fake
	epoch    time.Time
	num, den int64
}

func (c rateClock) Now() time.Time {
	el := c.base.Now().Sub(c.epoch)
	return c.epoch.Add(time.Duration(int64(el) * c.num / c.den))
}
func (c rateClock) NewTimer(d time.Duration) clock.Timer   { return c.base.NewTimer(d) }
func (c rateClock) NewTicker(d time.Duration) clock.Ticker { return c.base.NewTicker(d) }
func (c rateClock) After(d time.Duration) <-chan time.Time { return c.base.After(d) }

// S2 (a_fail_drift), today: the bounded fail-open ride-out measures validity
// on the LOCAL clock. With the local clock running at 0.9x, heartbeats lost to
// a partition are ridden out until local elapsed reaches the 30m TTL — server
// time T0+33m20s — although the server lease expired at T0+30m and another
// daemon acquired it then. Kill lands exactly at local TTL.
func TestFleetSim_S2_SlowLocalClockRidesOutPastServerExpiry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		num, den int64
		killAt   time.Duration // server time since T0 of the first refusing ride-out
	}{
		{"slow clock 0.9x (reproduction)", 9, 10, 33*time.Minute + 20*time.Second},
		{"true clock (control)", 1, 1, 30 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
			t0 := clockSeamEpoch
			a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, rateClock{sim.Clock, t0, tc.num, tc.den}, true)
			if got, rec := acquireOwnership(t, sim, a1, "a1-acquire"); got != ownershipAcquired || rec.Status != 200 {
				t.Fatalf("a1 acquire = %v status %d", got, rec.Status)
			}
			b := newCPAttempt(sim, "b", simOldActor, simNodeB, nil, true)

			// Partition: from here every a1 heartbeat (and its retry) is dropped.
			sim.Clock.Set(t0.Add(29*time.Minute + 59*time.Second))
			if got, rec := acquireOwnership(t, sim, b, "b-acquire-early"); got == ownershipAcquired || rec.Status != 409 {
				t.Fatalf("b acquire before server expiry = %v status %d, want refused 409", got, rec.Status)
			}
			if keep, _ := heartbeatOwnership(t, sim, a1, "a1-hb-1", fleetsim.Drop); !keep {
				t.Fatal("a1 killed before its local validity ran out")
			}
			sim.Observe("a1", simOldActor, "a1 supervisor rides out an inconclusive heartbeat")

			sim.Clock.Set(t0.Add(30 * time.Minute))
			var bAcquired bool
			if tc.num != tc.den {
				got, rec := acquireOwnership(t, sim, b, "b-acquire")
				if got != ownershipAcquired || rec.Status != 200 {
					t.Fatalf("b acquire at server expiry = %v status %d", got, rec.Status)
				}
				bAcquired = true
			}
			if before := tc.killAt - time.Second; before > 30*time.Minute {
				sim.Clock.Set(t0.Add(before))
				if keep, _ := heartbeatOwnership(t, sim, a1, "a1-hb-2", fleetsim.Drop); !keep {
					t.Fatalf("a1 killed at server T0+%s, before local TTL", before)
				}
				sim.Observe("a1", simOldActor, "a1 supervisor still rides out after the successor acquired")
			}
			sim.Clock.Set(t0.Add(tc.killAt))
			if keep, _ := heartbeatOwnership(t, sim, a1, "a1-hb-kill", fleetsim.Drop); keep {
				t.Fatalf("a1 still running at server T0+%s", tc.killAt)
			}
			a1.ap.Mu.Lock()
			lastErr := a1.ap.LastError
			a1.ap.Mu.Unlock()
			if lastErr == nil || !strings.Contains(lastErr.Message, "ownership_unverifiable") {
				t.Fatalf("a1 LastError = %v, want ownership_unverifiable kill", lastErr)
			}
			if want := t0.Add(30 * time.Minute); !lastErr.Timestamp.Equal(want) {
				t.Fatalf("kill stamped at local %s, want local TTL %s", lastErr.Timestamp, want)
			}
			if !bAcquired {
				if got, rec := acquireOwnership(t, sim, b, "b-acquire"); got != ownershipAcquired || rec.Status != 200 {
					t.Fatalf("b acquire after a1's kill = %v status %d", got, rec.Status)
				}
			}

			rep := sim.Check()
			if tc.num == tc.den {
				onlyViolations(t, rep, map[string]int{})
				return
			}
			onlyViolations(t, rep, map[string]int{fleetsim.InvNoOwnershipOverlap: 1})
		})
	}
}

// S2 (a_fail_unguarded: pause past TTL, then spawn), today: a daemon paused
// between session create and spawn resumes after its ownership lease expired
// and another daemon acquired it. Nothing re-checks ownership before spawn,
// so the spawn's control-plane write (session → running) is accepted. The
// resumed heartbeat then gets 403 (token no longer the server's), the
// verify path's re-acquire gets 409 already_claimed — which the client maps
// to domain.ErrAlreadyClaimed, a sentinel acquireAgentOwnership does not
// treat as "held by other" — so the kill is classified
// ownership_unverifiable rather than verifiably_lost. Here validity had
// already run out, so the kill still happens at once; see S3 for where the
// misclassification extends the overlap. The schedule (spawn's write before
// the overdue heartbeat) is one of the two orders the resumed goroutines can
// take.
func TestFleetSim_S2_PausedDaemonSpawnsAfterOwnershipMoved(t *testing.T) {
	for _, paused := range []bool{true, false} {
		name := "control: heartbeats keep ownership"
		if paused {
			name = "paused past TTL (reproduction)"
		}
		t.Run(name, func(t *testing.T) {
			sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
			t0 := clockSeamEpoch
			a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, nil, true)
			acquireOwnership(t, sim, a1, "a1-acquire")
			createSession(t, sim, a1)
			b := newCPAttempt(sim, "b", simOldActor, simNodeB, nil, true)

			if !paused {
				for m := 10; m <= 30; m += 10 {
					sim.Clock.Set(t0.Add(time.Duration(m) * time.Minute))
					if keep, recs := heartbeatOwnership(t, sim, a1, "a1-hb-"+time.Duration(m).String(), fleetsim.Apply); !keep || recs[0].Status != 200 {
						t.Fatalf("a1 heartbeat at +%dm: keep=%v %+v", m, keep, recs)
					}
				}
			}
			sim.Clock.Set(t0.Add(30*time.Minute + 30*time.Second))
			got, rec := acquireOwnership(t, sim, b, "b-acquire")
			if paused != (rec.Status == 200) || paused != (got == ownershipAcquired) {
				t.Fatalf("b acquire = %v status %d (paused=%v)", got, rec.Status, paused)
			}

			sim.Clock.Set(t0.Add(31 * time.Minute))
			sim.Observe("a1", simOldActor, "a1 spawns its agent")
			runActor(t, sim, "a1-spawn-running", func() { a1.s.markControlPlaneAgentSessionRunning(a1.ap) })
			sess, _ := sim.Server.SessionSnapshot(simSession)
			if sess.Status != "running" {
				t.Fatalf("session = %s, want running accepted", sess.Status)
			}
			keep, recs := heartbeatOwnership(t, sim, a1, "a1-hb-resume", fleetsim.Apply)
			if !paused {
				if !keep || recs[0].Status != 200 {
					t.Fatalf("control heartbeat: keep=%v %+v", keep, recs)
				}
				onlyViolations(t, sim.Check(), map[string]int{})
				return
			}
			if keep || len(recs) != 2 || recs[0].Status != 403 || recs[1].Status != 409 {
				t.Fatalf("resumed heartbeat: keep=%v recs=%+v, want 403 then re-acquire 409 and a kill", keep, recs)
			}
			if !strings.Contains(recs[1].RespBody, `"already_claimed"`) {
				t.Fatalf("re-acquire body = %s", recs[1].RespBody)
			}
			a1.ap.Mu.Lock()
			lastErr := a1.ap.LastError
			a1.ap.Mu.Unlock()
			if lastErr == nil || !strings.Contains(lastErr.Message, "ownership_unverifiable") {
				t.Fatalf("a1 LastError = %v, want ownership_unverifiable (409 already_claimed read as inconclusive)", lastErr)
			}
			onlyViolations(t, sim.Check(), map[string]int{
				fleetsim.InvNoOwnershipOverlap:       1,
				fleetsim.InvNoSupersededSessionWrite: 1,
			})
		})
	}
}

// S3 (a_fail_token), today: run 1's ownership release times out client-side
// and is still in flight when run 2 of the same agent re-acquires on the same
// daemon. The server keeps the token on a live same-owner re-acquire (only
// the fence moves), and release checks the token alone, so run 1's late
// release ends run 2's lease. Another daemon then acquires; run 2 keeps its
// token and agent. Its next heartbeat gets 403 and the verify path's
// re-acquire gets 409 already_claimed, which acquireAgentOwnership reads as
// inconclusive (domain.ErrAlreadyClaimed is not ErrAlreadyExists/ErrConflict),
// so run 2 rides out its local validity instead of being killed as
// verifiably_lost.
func TestFleetSim_S3_DelayedReleaseEndsSameOwnerReacquire(t *testing.T) {
	for _, late := range []bool{true, false} {
		name := "control: release applies in order"
		if late {
			name = "release applies after re-acquire (reproduction)"
		}
		t.Run(name, func(t *testing.T) {
			sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
			a := newCPAttempt(sim, "run1", simOldActor, simNodeA, nil, true)
			sim.BindAttempt("run2", simOldActor)
			acquireOwnership(t, sim, a, "run1-acquire")
			token1 := a.ap.OwnershipLeaseToken

			sim.Clock.Advance(10 * time.Minute)
			sim.Go("run1-release", func() error { a.s.releaseAgentOwnership(a.ap); return nil })
			rel := pendingFor(t, sim, fleetsim.Req("run1", "POST", "/release"), "run1 release")
			if late {
				sim.Clock.Advance(controlPlaneOperationTimeout)
				sim.Abandon(rel.Seq)
			} else {
				sim.Deliver(rel.Seq, fleetsim.Apply, time.Time{})
			}
			if !sim.Finished("run1-release") || a.ap.OwnershipLeaseToken != "" {
				t.Fatalf("run1 release did not return / clear local state")
			}

			sim.Clock.Advance(defaultOwnershipRetryInterval)
			a.name, a.s.ControlStore = "run2", sim.ControlStore("run2", simDaemonActor)
			if got, rec := acquireOwnership(t, sim, a, "run2-acquire"); got != ownershipAcquired || rec.Status != 200 {
				t.Fatalf("run2 acquire = %v status %d", got, rec.Status)
			}
			if late != (a.ap.OwnershipLeaseToken == token1) || a.ap.OwnershipFencingToken != 2 {
				t.Fatalf("run2 token reused=%v fence=%d (late=%v)", a.ap.OwnershipLeaseToken == token1, a.ap.OwnershipFencingToken, late)
			}
			if late {
				lateRel := deliverAt(t, sim, fleetsim.Req("run1", "POST", "/release"), time.Time{}, "late release")
				if lateRel.Status != 200 || lateRel.OwnerAuthorityBefore != "run2" {
					t.Fatalf("late release status=%d authority=%q", lateRel.Status, lateRel.OwnerAuthorityBefore)
				}
				if l, _ := sim.Server.OwnershipSnapshot(simOldActor); l.Status != "released" {
					t.Fatalf("run2 lease status = %s, want released", l.Status)
				}
			}

			b := newCPAttempt(sim, "b", simOldActor, simNodeB, nil, true)
			sim.Clock.Advance(time.Second)
			got, rec := acquireOwnership(t, sim, b, "b-acquire")
			if late != (rec.Status == 200) || late != (got == ownershipAcquired) {
				t.Fatalf("b acquire = %v status %d (late=%v)", got, rec.Status, late)
			}
			if a.ap.OwnershipLeaseToken == "" {
				t.Fatal("run2 lost its local ownership state")
			}
			sim.Observe("run2", simOldActor, "run2 supervisor holds the ownership token; its agent runs")

			sim.Clock.Advance(20 * time.Second)
			keep, recs := heartbeatOwnership(t, sim, a, "run2-hb", fleetsim.Apply)
			if !late {
				if !keep || recs[0].Status != 200 {
					t.Fatalf("control heartbeat keep=%v %+v", keep, recs)
				}
				onlyViolations(t, sim.Check(), map[string]int{})
				return
			}
			if !keep || len(recs) != 2 || recs[0].Status != 403 || recs[1].Status != 409 {
				t.Fatalf("run2 heartbeat keep=%v recs=%+v, want 403, re-acquire 409, ride-out", keep, recs)
			}
			sim.Observe("run2", simOldActor, "run2 rides out after the verify path saw another owner")
			a.ap.Mu.Lock()
			lastErr := a.ap.LastError
			a.ap.Mu.Unlock()
			if lastErr != nil {
				t.Fatalf("run2 killed (%v); today it rides out", lastErr)
			}
			onlyViolations(t, sim.Check(), map[string]int{
				fleetsim.InvNoForeignOwnershipRelease: 1,
				fleetsim.InvNoOwnershipOverlap:        2,
			})
		})
	}
}

// s4Setup: a1 owns the agent, has claimed the task, created its session and
// session lease at T0, and marked the session running.
func s4Setup(t *testing.T) (*fleetsim.Sim, *simAttempt) {
	t.Helper()
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, nil, true)
	acquireOwnership(t, sim, a1, "a1-acquire")
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	createSession(t, sim, a1)
	runActor(t, sim, "a1-running", func() { a1.s.markControlPlaneAgentSessionRunning(a1.ap) })
	return sim, a1
}

// createSession is createAgentSession's control-plane half: the local session
// ID is set first (as createAgentSession does), then the real
// createControlPlaneAgentSession creates the session and its lease.
func createSession(t *testing.T, sim *fleetsim.Sim, a *simAttempt) {
	t.Helper()
	a.ap.AgentSessionID = simSession
	runActor(t, sim, a.name+"-session", func() { a.s.createControlPlaneAgentSession(a.ap, simSession, "", "implementation", 0) })
	if a.ap.AgentLeaseID != simSession+"-lease" || a.ap.AgentLeaseToken == "" {
		t.Fatalf("session lease not recorded: %q", a.ap.AgentLeaseID)
	}
}

func finalizeInput(a *simAttempt) agentSessionCompletionInput {
	st := takeAgentSessionForFinalize(a.ap)
	return agentSessionCompletionInput{sessionID: st.sessionID, leaseID: st.leaseID, leaseToken: st.leaseToken,
		exitCode: 0, taskID: simIssue}
}

// S4 (c_stranded_session / gap L1), today: Loom clears its session and lease
// IDs before the finalize write and never retries it, so a finalize lost on
// the wire, or a crash before it, leaves the session non-terminal. On the
// pinned server the only thing that ends it is the background lease reaper,
// which retires it as expired/lease_lost — never the run's own outcome — once
// no unexpired lease vouches for it: at once after a lost finalize (Loom
// still released the lease), or exactly at the lease's expiry after a crash.
// With the reaper disabled (FLEET_LEASE_REAPER_INTERVAL=0) it stays running.
// A finalize that is delayed past ownership expiry (daemon paused after exit)
// is not rejected today: it lands as completed (NoStaleCompleted).
//
// Note against the map's "Now" column ("session stranded"): with the pinned
// server's default reaper the strand is bounded and ends as expired.
func TestFleetSim_S4_LostFinalizeStrandsUntilLeaseReaper(t *testing.T) {
	t0 := clockSeamEpoch
	t.Run("finalize lost on the wire", func(t *testing.T) {
		sim, a1 := s4Setup(t)
		sim.Clock.Set(t0.Add(20 * time.Minute))
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		sim.Go("a1-finalize", func() error { a1.s.completeControlPlaneAgentSession(a1.ap, in); return nil })
		if rec := sim.Deliver(pendingFor(t, sim, sessionPatch("a1", "completed"), "sfin").Seq, fleetsim.Drop, time.Time{}); rec.Delivery != fleetsim.Drop {
			t.Fatal("sfin not dropped")
		}
		sim.DrainFIFO()
		if a1.ap.AgentSessionID != "" || a1.ap.AgentLeaseToken != "" {
			t.Fatal("Loom kept session state for a retry")
		}
		if l, _ := sim.Server.LeaseSnapshot(simSession + "-lease"); l.Status != "released" {
			t.Fatalf("lease = %s, want released by finalize", l.Status)
		}
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "running" {
			t.Fatalf("session = %s, want running (stranded)", s.Status)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvSessionTerminates: 1})
		res := sim.Server.ReapControlPlane()
		s, _ := sim.Server.SessionSnapshot(simSession)
		if res.SessionsRetired[simSession] != "expired" || s.Status != "expired" || s.ErrorClass != "lease_lost" {
			t.Fatalf("reaper = %+v session = %+v, want expired/lease_lost", res, s)
		}
		onlyViolations(t, sim.Check(), map[string]int{})
	})
	t.Run("crash before finalize", func(t *testing.T) {
		sim, _ := s4Setup(t)
		sim.Clock.Set(t0.Add(time.Minute))
		sim.EndAttempt("a1")
		l, _ := sim.Server.LeaseSnapshot(simSession + "-lease")
		sim.Clock.Set(l.ExpiresAt.Add(-time.Second))
		if res := sim.Server.ReapControlPlane(); len(res.SessionsRetired) != 0 {
			t.Fatalf("reaper retired %v while the lease still vouched", res.SessionsRetired)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvSessionTerminates: 1})
		sim.Clock.Set(l.ExpiresAt)
		res := sim.Server.ReapControlPlane()
		if len(res.LeasesExpired) != 1 || res.SessionsRetired[simSession] != "expired" {
			t.Fatalf("reaper at lease expiry = %+v", res)
		}
		onlyViolations(t, sim.Check(), map[string]int{})
	})
	t.Run("crash before finalize, reaper disabled", func(t *testing.T) {
		sim, _ := s4Setup(t)
		sim.EndAttempt("a1")
		sim.Clock.Advance(24 * time.Hour)
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "running" {
			t.Fatalf("session = %s", s.Status)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvSessionTerminates: 1})
	})
	t.Run("finalize after ownership expired", func(t *testing.T) {
		sim, a1 := s4Setup(t)
		sim.Clock.Set(t0.Add(31 * time.Minute)) // daemon paused after exit; no heartbeats
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		runActor(t, sim, "a1-finalize", func() { a1.s.completeControlPlaneAgentSession(a1.ap, in) })
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "completed" {
			t.Fatalf("session = %s, want completed accepted", s.Status)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvNoStaleCompleted: 1})
	})
	t.Run("control: finalize under live ownership", func(t *testing.T) {
		sim, a1 := s4Setup(t)
		sim.Clock.Set(t0.Add(20 * time.Minute))
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		runActor(t, sim, "a1-finalize", func() { a1.s.completeControlPlaneAgentSession(a1.ap, in) })
		if res := sim.Server.ReapControlPlane(); len(res.SessionsRetired) != 0 {
			t.Fatalf("reaper retired %v", res.SessionsRetired)
		}
		s, _ := sim.Server.SessionSnapshot(simSession)
		if s.Status != "completed" || s.ExitCode == nil || *s.ExitCode != 0 {
			t.Fatalf("session = %+v", s)
		}
		onlyViolations(t, sim.Check(), map[string]int{})
	})
}

// S7 (c_fail_no_guard, c_fail_session_unfenced, c_fail_stale_finalize),
// today: the session update is an unconditional read-modify-write.
//   - A spawn's "running" write that timed out client-side and lands after
//     finalize moves the completed session back to running; the reaper later
//     retires it as expired, so the run's completed outcome is lost.
//   - A delayed "running" write lands after another daemon took ownership.
//   - A delayed finalize lands as completed after another daemon took
//     ownership (no coercion to failed/ownership_lost).
func TestFleetSim_S7_DelayedSessionWritesAfterFinalizeOrSupersession(t *testing.T) {
	t0 := clockSeamEpoch
	t.Run("running lands after finalize", func(t *testing.T) {
		sim, a1 := s7Setup(t)
		srun := s7AbandonRunning(t, sim, a1)
		sim.Clock.Set(t0.Add(10 * time.Minute))
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		sim.Go("a1-finalize", func() error { a1.s.completeControlPlaneAgentSession(a1.ap, in); return nil })
		deliverAt(t, sim, sessionPatch("a1", "completed"), time.Time{}, "sfin")
		for _, suffix := range []string{"-lease/release", "/release-lock", "/workers/" + simOldActor} {
			deliverAt(t, sim, fleetsim.Req("a1", "", suffix), time.Time{}, suffix)
		}
		sim.Go("a1-release-ownership", func() error { a1.s.releaseAgentOwnership(a1.ap); return nil })
		deliverAt(t, sim, fleetsim.Req("a1", "POST", "/agent-ownership-leases/"+simOldActor+"/release"), time.Time{}, "ownership release")
		late := sim.Deliver(srun.Seq, fleetsim.Apply, time.Time{})
		if late.Status != 200 || late.SessionStatusBefore != "completed" {
			t.Fatalf("late running status=%d before=%q", late.Status, late.SessionStatusBefore)
		}
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "running" {
			t.Fatalf("session = %s, want running (terminal overwritten)", s.Status)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvTerminalSessionOnce: 1, fleetsim.InvSessionTerminates: 1})
		res := sim.Server.ReapControlPlane()
		s, _ := sim.Server.SessionSnapshot(simSession)
		if res.SessionsRetired[simSession] != "expired" || s.ErrorClass != "lease_lost" {
			t.Fatalf("reaper = %+v session = %+v; want the completed run retired as expired", res, s)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvTerminalSessionOnce: 1})
	})
	t.Run("running lands after supersession", func(t *testing.T) {
		sim, a1 := s7Setup(t)
		srun := s7AbandonRunning(t, sim, a1)
		sim.Clock.Set(t0.Add(30*time.Minute + time.Second)) // a1's daemon paused; lease lapsed
		b := newCPAttempt(sim, "b", simOldActor, simNodeB, nil, true)
		if got, _ := acquireOwnership(t, sim, b, "b-acquire"); got != ownershipAcquired {
			t.Fatal("b could not acquire")
		}
		late := sim.Deliver(srun.Seq, fleetsim.Apply, time.Time{})
		if late.Status != 200 || late.OwnerAuthorityBefore != "b" {
			t.Fatalf("late running status=%d authority=%q", late.Status, late.OwnerAuthorityBefore)
		}
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvNoSupersededSessionWrite: 1})
	})
	t.Run("finalize lands after supersession", func(t *testing.T) {
		sim, a1 := s7Setup(t)
		runActor(t, sim, "a1-running", func() { a1.s.markControlPlaneAgentSessionRunning(a1.ap) })
		sim.Clock.Set(t0.Add(10 * time.Minute))
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		sim.Go("a1-finalize", func() error { a1.s.completeControlPlaneAgentSession(a1.ap, in); return nil })
		sfin := pendingFor(t, sim, sessionPatch("a1", "completed"), "sfin")
		sim.Clock.Advance(controlPlaneOperationTimeout)
		sim.Abandon(sfin.Seq)
		sim.Clock.Set(t0.Add(30*time.Minute + time.Second)) // finalize's remaining calls stall too
		b := newCPAttempt(sim, "b", simOldActor, simNodeB, nil, true)
		if got, _ := acquireOwnership(t, sim, b, "b-acquire"); got != ownershipAcquired {
			t.Fatal("b could not acquire")
		}
		late := sim.Deliver(sfin.Seq, fleetsim.Apply, time.Time{})
		if late.Status != 200 {
			t.Fatalf("late finalize status = %d", late.Status)
		}
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "completed" {
			t.Fatalf("session = %s, want completed (not coerced)", s.Status)
		}
		sim.DrainFIFO()
		onlyViolations(t, sim.Check(), map[string]int{fleetsim.InvNoStaleCompleted: 1})
	})
	t.Run("control: running then finalize in order", func(t *testing.T) {
		sim, a1 := s7Setup(t)
		runActor(t, sim, "a1-running", func() { a1.s.markControlPlaneAgentSessionRunning(a1.ap) })
		sim.Clock.Set(t0.Add(10 * time.Minute))
		sim.EndAttempt("a1")
		in := finalizeInput(a1)
		runActor(t, sim, "a1-finalize", func() { a1.s.completeControlPlaneAgentSession(a1.ap, in) })
		runActor(t, sim, "a1-release-ownership", func() { a1.s.releaseAgentOwnership(a1.ap) })
		if res := sim.Server.ReapControlPlane(); len(res.SessionsRetired) != 0 {
			t.Fatalf("reaper retired %v", res.SessionsRetired)
		}
		if s, _ := sim.Server.SessionSnapshot(simSession); s.Status != "completed" {
			t.Fatalf("session = %s", s.Status)
		}
		onlyViolations(t, sim.Check(), map[string]int{})
	})
}

// s7Setup: a1 owns, has claimed and created its session (status starting).
func s7Setup(t *testing.T) (*fleetsim.Sim, *simAttempt) {
	t.Helper()
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	seedSimIssue(sim)
	a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, nil, true)
	acquireOwnership(t, sim, a1, "a1-acquire")
	a1.claim(sim, "a1-claim")
	sim.DrainFIFO()
	createSession(t, sim, a1)
	return sim, a1
}

// s7AbandonRunning sends the spawn's running write and lets its client
// deadline expire with the request still in flight.
func s7AbandonRunning(t *testing.T, sim *fleetsim.Sim, a1 *simAttempt) fleetsim.Pending {
	t.Helper()
	sim.Go("a1-running", func() error { a1.s.markControlPlaneAgentSessionRunning(a1.ap); return nil })
	srun := pendingFor(t, sim, sessionPatch("a1", "running"), "srun")
	sim.Clock.Advance(controlPlaneOperationTimeout)
	sim.Abandon(srun.Seq)
	if !sim.Finished("a1-running") {
		t.Fatal("spawn's running write did not return after its deadline")
	}
	return srun
}

// S13 (a_fail_crash), today: daemon 1 crashes holding the agent's 30m
// ownership lease; its agent survives as an orphan (the model's assumption:
// the startup sweep only kills PPID==1 processes in managed worktrees on the
// same host, best-effort). The restarted daemon has a new owner ID, so every
// acquire is refused (409 already_claimed, classified inconclusive — not
// held-by-other) until exactly the old lease's expiry; then it acquires,
// re-claims the task as the same claim actor, and the orphan's held writes
// are accepted under the new attempt's authority.
func TestFleetSim_S13_RestartedDaemonWaitsOutOldLeaseWhileOrphanWrites(t *testing.T) {
	for _, crash := range []bool{true, false} {
		name := "control: graceful shutdown releases ownership"
		if crash {
			name = "crash leaves orphan (reproduction)"
		}
		t.Run(name, func(t *testing.T) {
			t0 := clockSeamEpoch
			sim := fleetsim.New(t0, simWorkspace, fleetsim.Guards{})
			seedSimIssue(sim)
			a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, nil, true)
			acquireOwnership(t, sim, a1, "a1-acquire")
			a1.claim(sim, "a1-claim")
			sim.DrainFIFO()

			if crash {
				w := fleetsim.NewScriptedWorker(sim, "a1", simOperator, fleetsim.CloseStep(simIssue, "", simCloseReason))
				sim.Go("a1-orphan", func() error { _, err := w.Run(context.Background()); return err })
				pendingFor(t, sim, fleetsim.Req("a1", "POST", "/assign"), "orphan assign")
			} else {
				sim.Clock.Advance(30 * time.Second)
				runActor(t, sim, "a1-release-claim", func() { a1.s.releaseAssignedTaskClaim(a1.ap, simIssue) })
				runActor(t, sim, "a1-release-ownership", func() { a1.s.releaseAgentOwnership(a1.ap) })
				sim.EndAttempt("a1")
			}

			a2 := newCPAttempt(sim, "a2", simOldActor, simNodeARestart, nil, true)
			sim.Clock.Set(t0.Add(time.Minute))
			got, rec := acquireOwnership(t, sim, a2, "a2-acquire-1")
			if !crash {
				if got != ownershipAcquired || rec.Status != 200 {
					t.Fatalf("restart acquire after graceful release = %v %d", got, rec.Status)
				}
				onlyViolations(t, sim.Check(), map[string]int{})
				return
			}
			if got != ownershipAcquireInconclusive || rec.Status != 409 || !strings.Contains(rec.RespBody, "already_claimed") {
				t.Fatalf("restart acquire = %v %d %s, want 409 already_claimed classified inconclusive", got, rec.Status, rec.RespBody)
			}
			// The claim reaper (every 30s by default) reverts the task once the
			// dead daemon's 300s claim lock has lapsed and the 60s grace passed.
			sim.Clock.Set(t0.Add(fleetsim.DefaultLockTTL + 30*time.Second))
			if got := sim.Server.ReapStaleClaims(); len(got) != 1 {
				t.Fatalf("claim reaper reverted %v", got)
			}
			sim.Clock.Set(t0.Add(defaultLeaseTTL - time.Second))
			if got, _ := acquireOwnership(t, sim, a2, "a2-acquire-2"); got == ownershipAcquired {
				t.Fatal("restart acquired before the old lease expired")
			}
			sim.Clock.Set(t0.Add(defaultLeaseTTL))
			if got, rec := acquireOwnership(t, sim, a2, "a2-acquire-3"); got != ownershipAcquired || rec.Status != 200 {
				t.Fatalf("restart acquire at old lease expiry = %v %d", got, rec.Status)
			}
			sim.Observe("a1", simOldActor, "orphaned agent process still running")
			a2.claim(sim, "a2-claim")
			drainAttempt(t, sim, "a2", "a2-claim")
			claim, _ := lastRecord(sim, "a2", "/claim")
			if claim.Status != 200 || a2.ap.AssignedTaskID != simIssue {
				t.Fatalf("a2 claim status=%d assigned=%q", claim.Status, a2.ap.AssignedTaskID)
			}
			sim.DrainFIFO()
			closeRec, _ := lastRecord(sim, "a1", "/close")
			if closeRec.Status != 200 {
				t.Fatalf("orphan close = %d, want accepted", closeRec.Status)
			}
			onlyViolations(t, sim.Check(), map[string]int{
				fleetsim.InvNoOwnershipOverlap: 1,
				fleetsim.InvNoSupersededWrite:  2,
			})
		})
	}
}

// drainAttempt applies attempt's pending requests in send order until actor
// finishes, leaving every other attempt's requests parked.
func drainAttempt(t *testing.T, sim *fleetsim.Sim, attempt, actor string) {
	t.Helper()
	for !sim.Finished(actor) {
		var next *fleetsim.Pending
		for _, p := range sim.Pending() {
			if p.Attempt == attempt && !p.Abandoned {
				next = &p
				break
			}
		}
		if next == nil {
			t.Fatalf("%s blocked with no pending request of %s", actor, attempt)
		}
		sim.Deliver(next.Seq, fleetsim.Apply, time.Time{})
	}
}

func ownershipAcquireArgs(agent, owner string) store.AgentOwnershipLeaseAcquire {
	return store.AgentOwnershipLeaseAcquire{WorkspaceKey: simWorkspace, AgentID: agent, OwnerID: owner,
		RuntimeProvider: domain.RuntimeProviderLocal, NodeID: owner, TTL: defaultLeaseTTL}
}

func lastRecord(sim *fleetsim.Sim, attempt, suffix string) (fleetsim.Record, bool) {
	var out fleetsim.Record
	found := false
	for _, r := range sim.Records() {
		if r.Attempt == attempt && strings.HasSuffix(r.Path, suffix) {
			out, found = r, true
		}
	}
	return out, found
}

// The fake's 409 on a contested ownership acquire carries code
// already_claimed (fleet-db writeStorageError for ErrAlreadyClaimed). Pin
// that the real Loom client turns it into domain.ErrAlreadyClaimed, which the
// supervisor's ownership classification does not recognize.
func TestFleetSim_OwnershipContestMapsToErrAlreadyClaimed(t *testing.T) {
	sim := fleetsim.New(clockSeamEpoch, simWorkspace, fleetsim.Guards{})
	a1 := newCPAttempt(sim, "a1", simOldActor, simNodeA, nil, true)
	acquireOwnership(t, sim, a1, "a1-acquire")
	cs := sim.ControlStore("b", simDaemonActor)
	var err error
	runActor(t, sim, "b-raw-acquire", func() {
		_, err = cs.AgentOwnershipLeases().Acquire(context.Background(), ownershipAcquireArgs(simOldActor, simNodeB))
	})
	if !errors.Is(err, domain.ErrAlreadyClaimed) || errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) || isTypedDomainError(err) {
		t.Fatalf("contested acquire err = %v; want ErrAlreadyClaimed only (untyped for the verify path)", err)
	}
}
