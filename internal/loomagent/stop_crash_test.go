package loomagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestStopCrashBeforeInterrupt: Loom crashes after Archive(cancelled) or
// Delete saved the agent stopping but before it interrupted the running
// turn. The restart interrupts the turn (a Delete purges its session) and
// finishes the archive or delete, on every harness; a session gone while
// Loom was down runs nothing, so the archive finishes too.
func TestStopCrashBeforeInterrupt(t *testing.T) {
	for _, name := range Harnesses {
		for _, op := range []string{"archive", "archive session gone", "delete"} {
			t.Run(name+"/"+op, func(t *testing.T) {
				ctx := context.Background()
				e := newCreateEnv(t)
				e.name = name
				fh := e.h.Harness.(*fake.Harness)
				req := leadReq("r1")
				req.Overrides.Harness = name
				s := e.service(ServiceConfig{Interrupt: func(context.Context, loomstore.Agent) error { panic("crash") }})
				info, err := s.Create(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				id, ref := info.AgentID, sessionOf(s.get(t, info.AgentID))
				fh.Script(id, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}})
				mustSendMsg(t, s, sendReq(id, "u1", "go", user))
				if !panics(func() {
					if op != "delete" {
						_ = s.Archive(ctx, ArchiveRequest{AgentID: id, Reason: ArchiveCancelled})
					} else {
						_ = s.Delete(ctx, DeleteRequest{AgentID: id})
					}
				}) {
					t.Fatal("did not crash before the interrupt")
				}
				if row := s.get(t, id); row.State != StateStopping || row.RunningTurnID == nil {
					t.Fatalf("at the crash: %s, running %v; want stopping with the turn running", row.State, row.RunningTurnID)
				}
				if op == "archive session gone" {
					if err := fh.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
						t.Fatal(err)
					}
				}
				s = e.service(ServiceConfig{}) // Loom restarts
				if err := s.Reconcile(ctx, name); err != nil {
					t.Fatal(err)
				}
				runDispatcher(t, s)
				settled(t, s)
				st, err := fh.Session(ref).Status(ctx)
				if gone := op != "archive" && errors.Is(err, loomharness.ErrSessionNotFound); !gone && (err != nil || st.Running) {
					t.Fatalf("after the restart the turn still runs (%v, %v); want it interrupted", st, err)
				}
				row, err := e.st.GetAgent(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if op == "delete" {
					deletedClean(t, e, id)
					return
				}
				if row.State != StateArchived || deref(row.ArchiveReason) != ArchiveCancelled || row.ArchivedAt == nil ||
					e.events(t, id, EventArchived) != 1 {
					t.Fatalf("after the restart: %s, reason %q, clock %v, %d archived; want archived as cancelled once",
						row.State, deref(row.ArchiveReason), row.ArchivedAt, e.events(t, id, EventArchived))
				}
			})
		}
	}
}

// TestArchiveCancelledInterruptFailRetried: a live Archive(cancelled) whose
// interrupt fails leaves the agent stopping; the reconcile queue retries it
// and finishes the archive.
func TestArchiveCancelledInterruptFailRetried(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	var s *Service
	var mu sync.Mutex
	failing := true
	s = e.service(ServiceConfig{Interrupt: func(ctx context.Context, a loomstore.Agent) error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("harness busy")
		}
		return s.sessionInterrupt(ctx, a)
	}})
	clock := useTestClock(s)
	runDispatcher(t, s)
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveCancelled}); err == nil {
		t.Fatal("Archive did not fail")
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	if clock.fire() == 0 {
		t.Fatal("no retry queued")
	}
	settled(t, s)
	if st, err := fh.Session(ref).Status(ctx); err != nil || st.Running {
		t.Fatalf("after the retry the turn still runs (%v, %v)", st, err)
	}
	if row := s.get(t, a.AgentID); row.State != StateArchived || deref(row.ArchiveReason) != ArchiveCancelled {
		t.Fatalf("after the retry: %s, reason %q; want archived as cancelled", row.State, deref(row.ArchiveReason))
	}
}

// TestSettleFinishesStoppingCancelled: settle archives a row left stopping
// as cancelled: one whose turn already ended gets no interrupt, a single task
// gets the cancelled outcome, and a turn whose session is gone runs nothing.
func TestSettleFinishesStoppingCancelled(t *testing.T) {
	for _, c := range []struct {
		name, mode string
		running    bool
		interrupt  error
	}{
		{"persistent ended", "persistent", false, nil},
		{"single task ended", "single_task", false, nil},
		{"session gone", "persistent", true, fmt.Errorf("fake s1: %w", loomharness.ErrSessionNotFound)},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := svcAgent("a1", c.mode, StateStopping)
			a.ArchiveReason = sp(ArchiveCancelled)
			if c.running {
				a.RunningTurnID = sp("turn_1")
			}
			interrupts := 0
			s := newService(t, ServiceConfig{Interrupt: func(context.Context, loomstore.Agent) error {
				interrupts++
				return c.interrupt
			}}, a)
			if err := s.reconcileAgent(context.Background(), "a1"); err != nil {
				t.Fatal(err)
			}
			row := s.get(t, "a1")
			if row.State != StateArchived || deref(row.ArchiveReason) != ArchiveCancelled || (interrupts == 1) != c.running ||
				(c.mode == "single_task") != (deref(row.Outcome) == ArchiveCancelled) {
				t.Fatalf("%s, reason %q, outcome %q, %d interrupts; want archived as cancelled",
					row.State, deref(row.ArchiveReason), deref(row.Outcome), interrupts)
			}
		})
	}
}

// TestStopCrashBeforeInterruptPendingSwitch: a harness switch and then an
// Archive(cancelled) both crash before their interrupt. Recovery stops the
// turn on the old session, where it runs, before any switch opens a new one.
func TestStopCrashBeforeInterruptPendingSwitch(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateActive)
	e.startTurn(t)
	st, err := e.fa.Session(e.old).Status(ctx)
	if err != nil || !st.Running {
		t.Fatalf("old turn not running: %v, %v", st, err)
	}
	a := e.s.get(t, "a1")
	to := a.StateOf()
	to.RunningTurn = &st.TurnID
	if _, err := e.s.setState(ctx, a, to); err != nil {
		t.Fatal(err)
	}
	e.crash = true
	if !panics(func() { _, _ = e.s.Update(ctx, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash before its interrupt")
	}
	if !panics(func() { _ = e.s.Archive(ctx, ArchiveRequest{AgentID: "a1", Reason: ArchiveCancelled}) }) {
		t.Fatal("Archive did not crash before its interrupt")
	}
	e.crash = false
	if err := e.s.reconcileAgent(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if st, err := e.fa.Session(e.old).Status(ctx); err != nil || st.Running {
		t.Fatalf("the old session's turn still runs (%v, %v)", st, err)
	}
	if row := e.s.get(t, "a1"); row.State != StateArchived {
		t.Fatalf("after recovery: %s; want archived", row.State)
	}
}
