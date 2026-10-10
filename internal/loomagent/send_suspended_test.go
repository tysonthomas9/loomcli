package loomagent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// suspender wraps the fake: once suspended, every session runs nothing
// (Status not running, Interrupt false) while the fake's history still has
// no end for its open turn, as OpenCode b30c4d0 leaves a turn whose
// permission was rejected when the feed misses that turn's end.
type suspender struct {
	*fake.Harness
	on atomic.Bool
}

func (h *suspender) Session(ref loomharness.NativeRef) loomharness.Session {
	return suspendedSession{h.Harness.Session(ref), h}
}

type suspendedSession struct {
	loomharness.Session
	h *suspender
}

func (s suspendedSession) Status(ctx context.Context) (loomharness.Status, error) {
	if s.h.on.Load() {
		return loomharness.Status{}, nil
	}
	return s.Session.Status(ctx)
}

func (s suspendedSession) Interrupt(ctx context.Context) (bool, error) {
	if s.h.on.Load() {
		return false, nil
	}
	return s.Session.Interrupt(ctx)
}

// TestSendInterruptEndsASuspendedTurn (OC1): Stop on a running turn the
// harness no longer runs, and whose history has no end, ends that turn as
// cancelled, with its open ask, and saves that end, so the agent goes idle
// and the chat shows the turn's note, instead of staying active with nothing
// to stop.
func TestSendInterruptEndsASuspendedTurn(t *testing.T) {
	e := newCreateEnv(t)
	h := &suspender{Harness: fake.New()}
	e.h.Harness = h
	s := e.service(ServiceConfig{})
	pump(t, s, e.h, e.st)
	a, _ := newLead(t, e, s, "alpha")
	h.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "t1"}, {Ask: "t1"}}})

	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	drained(t, s, "t1's ask open", func() bool { return s.get(t, a.AgentID).State == StateWaiting })
	turn := deref(s.get(t, a.AgentID).RunningTurnID)
	h.on.Store(true)
	stop := mustSendMsg(t, s, interruptReq(a.AgentID, "stop1", "", user))
	if stop.Interrupted == nil || !*stop.Interrupted {
		t.Fatalf("stop = %+v; want interrupted", stop)
	}
	got := s.get(t, a.AgentID)
	if got.State != StateIdle || got.RunningTurnID != nil || len(s.openAsks(a.AgentID)) != 0 {
		t.Fatalf("after Stop: state %s, running %v, %d open asks; want idle, no turn, no asks",
			got.State, deref(got.RunningTurnID), len(s.openAsks(a.AgentID)))
	}
	// OC2: the end is saved, so the chat shows the turn's note and keeps it on
	// a reload.
	ends := kinds(rows(t, s, a.AgentID, 0), EventTurnCompleted)
	if len(ends) != 1 || ends[0].TurnID != turn || !strings.Contains(string(ends[0].Payload), `"stopReason":"cancelled"`) {
		t.Fatalf("saved turn ends = %+v; want one for %s, cancelled", ends, turn)
	}
}
