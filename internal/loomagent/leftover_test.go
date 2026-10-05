package loomagent

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// exists reports whether h still has the native session ref.
func exists(h *fake.Harness, ref loomharness.NativeRef) bool {
	_, err := h.Session(ref).Status(context.Background())
	return !errors.Is(err, loomharness.ErrSessionNotFound)
}

// TestCreateOpenLeftoverPurgedAcrossRestart: Create's Open fails but leaves
// a session behind. Its ref is recorded as owned and purge-pending; the
// purge fails, and after a restart the dispatcher's start-up reconcile
// purges it and drops the mark, then finishes the Create with a new
// session. The leftover's ownership stays recorded (R29).
func TestCreateOpenLeftoverPurgedAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	fh.FailOpen(errors.New("open failed after creating"), true)
	fh.FailPurge(errors.New("purge down"))
	if _, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded with a failing Open")
	}
	pending, err := e.st.PurgePending(ctx, "ws")
	if err != nil || len(pending) != 1 {
		t.Fatalf("purge-pending = %v, %v; want the leftover", pending, err)
	}
	ref := loomharness.NativeRef{Root: pending[0].NativeRoot, NativeID: pending[0].NativeID}
	if !exists(fh, ref) {
		t.Fatal("the leftover is gone although its purge failed")
	}

	fh.FailOpen(nil, false)
	fh.FailPurge(nil)
	s := e.service(ServiceConfig{}) // restart
	runDispatcher(t, s)
	drained(t, s, "the leftover purged", func() bool {
		p, err := e.st.PurgePending(ctx, "ws")
		return err == nil && len(p) == 0
	})
	if exists(fh, ref) {
		t.Fatal("the purge-pending mark was dropped but the session remains")
	}
	owned, err := e.st.NativeSessions(ctx, pending[0].AgentID)
	if err != nil || len(owned) != 2 || owned[0].NativeID != ref.NativeID {
		t.Fatalf("owned = %v, %v; want the leftover kept as owned, then the working session", owned, err)
	}
}

// TestHarnessSwitchOpenLeftoverPurged: a switch whose destination Open fails
// leaving a session purges that session at once; a clean Open failure
// records nothing.
func TestHarnessSwitchOpenLeftoverPurged(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	e.fb.FailOpen(errors.New("open failed"), false)
	if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err == nil {
		t.Fatal("switch succeeded with a failing Open")
	}
	if owned, _ := e.s.store.NativeSessions(ctx, "a1"); len(owned) != 1 {
		t.Fatalf("a clean Open failure recorded %v", owned)
	}
	e.fb.FailOpen(errors.New("open failed after creating"), true)
	if _, err := e.s.Update(ctx, switchReq("r2", 1, "fb")); err == nil {
		t.Fatal("switch succeeded with a failing Open")
	}
	owned, err := e.s.store.NativeSessions(ctx, "a1")
	if err != nil || len(owned) != 2 || owned[1].Harness != "fb" {
		t.Fatalf("owned = %v, %v; want the leftover recorded", owned, err)
	}
	if exists(e.fb, loomharness.NativeRef{Root: owned[1].NativeRoot, NativeID: owned[1].NativeID}) {
		t.Fatal("the leftover was not purged")
	}
	if p, _ := e.s.store.PurgePending(ctx, "ws"); len(p) != 0 {
		t.Fatalf("purge-pending = %v after a successful purge", p)
	}
	if a := e.s.get(t, "a1"); a.Harness != "fa" {
		t.Fatalf("harness = %s; the failed switch changed it", a.Harness)
	}
}

// TestCreateOpenLeftoverSweepRacesReopen: the start-up sweep reads a
// purge-pending session, then pauses; a retried Create re-Opens the same
// session as its working one, which clears the mark. When the sweep goes on
// it reads the marks under the agent lock and leaves the session alone.
func TestCreateOpenLeftoverSweepRacesReopen(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	fh.FailOpen(errors.New("open failed after creating"), true)
	fh.FailPurge(errors.New("purge down"))
	if _, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded with a failing Open")
	}
	fh.FailOpen(nil, false)
	fh.FailPurge(nil)
	s := e.service(ServiceConfig{}) // restart
	paused, resume := make(chan struct{}), make(chan struct{})
	sweepPause = func() { close(paused); <-resume }
	t.Cleanup(func() { sweepPause = func() {} })
	swept := make(chan error, 1)
	go func() { // the dispatcher's start-up sweep
		s.resync(ctx, true)
		s.reconcileDue(ctx)
		swept <- nil
	}()
	<-paused                                  // the sweep holds the pending ref
	info, err := s.Create(ctx, leadReq("r1")) // the retry re-Opens the same session
	if err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-swept; err != nil {
		t.Fatal(err)
	}
	a := s.get(t, info.AgentID)
	ref := loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}
	if !exists(fh, ref) {
		t.Fatal("the sweep purged the agent's working session")
	}
	if p, _ := e.st.PurgePending(ctx, "ws"); len(p) != 0 {
		t.Fatalf("purge-pending = %v", p)
	}
}
