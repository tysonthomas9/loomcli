package loomagent

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// at is the fake clock: secs seconds after agentID's last state change.
func at(t *testing.T, s *Service, agentID string, secs int) time.Time {
	t.Helper()
	changed, err := time.Parse(time.RFC3339Nano, s.get(t, agentID).UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	return changed.Add(time.Duration(secs) * time.Second)
}

func loaded(s *Service, ref loomharness.NativeRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resumed["opencode"][ref]
}

// restartedSince reports whether the fake restarted since feed was opened:
// a restart ends every open feed.
func restartedSince(feed loomharness.Feed) bool {
	for {
		select {
		case _, ok := <-feed.Events():
			if !ok {
				return true
			}
		default:
			return false
		}
	}
}

// TestIdleUnloadAndRestart: an agent idle for 30 minutes is unloaded and
// stays idle; a waiting or locked agent is never touched and holds back the
// shared restart, which runs once every agent is idle that long; the next
// message resumes the same session lazily and is delivered once.
func TestIdleUnloadAndRestart(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	pump(t, s, e.h, e.st)
	alpha, aref := newLead(t, e, s, "alpha")
	beta, bref := newLead(t, e, s, "beta")
	fh.Script(beta.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "hi"}, {Ask: "q1"}}})
	mustSendMsg(t, s, sendReq(alpha.AgentID, "u1", "first", user))
	mustSendMsg(t, s, sendReq(beta.AgentID, "u1", "first", user))
	eventually(t, "alpha idle", func() bool { return s.get(t, alpha.AgentID).State == StateIdle })
	eventually(t, "beta waiting", func() bool { return s.get(t, beta.AgentID).State == StateWaiting })
	watch, err := fh.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()

	IdleSweep(ctx, at(t, s, alpha.AgentID, 1799), []*Service{s})
	if !loaded(s, aref) {
		t.Fatal("alpha unloaded before 30 idle minutes")
	}
	unlock := s.lock(alpha.AgentID) // a Send holds the lock: alpha is busy
	IdleSweep(ctx, at(t, s, alpha.AgentID, 1800), []*Service{s})
	unlock()
	if !loaded(s, aref) {
		t.Fatal("alpha unloaded while its lock was held")
	}
	IdleSweep(ctx, at(t, s, alpha.AgentID, 1800), []*Service{s})
	if loaded(s, aref) || !loaded(s, bref) || restartedSince(watch) {
		t.Fatalf("at 30 minutes: alpha loaded %v, beta loaded %v, restarted %v; want only alpha unloaded",
			loaded(s, aref), loaded(s, bref), restartedSince(watch))
	}
	if a, b := s.get(t, alpha.AgentID), s.get(t, beta.AgentID); a.State != StateIdle || b.State != StateWaiting ||
		len(s.openAsks(beta.AgentID)) != 1 {
		t.Fatalf("states %s, %s; want idle, waiting with its ask", a.State, b.State)
	}

	if err := fh.Session(bref).Reply(ctx, "q1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "beta idle", func() bool { return s.get(t, beta.AgentID).State == StateIdle })
	IdleSweep(ctx, at(t, s, beta.AgentID, 1799), []*Service{s})
	if !loaded(s, bref) || restartedSince(watch) {
		t.Fatal("beta unloaded or the harness restarted before 30 idle minutes")
	}
	IdleSweep(ctx, at(t, s, beta.AgentID, 1800), []*Service{s})
	if loaded(s, bref) || !restartedSince(watch) {
		t.Fatal("at 30 minutes beta was not unloaded and the harness not restarted")
	}

	pump(t, s, e.h, e.st) // the restart ended the old feed
	mustSendMsg(t, s, sendReq(alpha.AgentID, "u2", "second", user))
	eventually(t, "u2 delivered", func() bool { return slotState(t, s, alpha.AgentID, "u2") == "delivered" })
	eventually(t, "alpha idle again", func() bool { return s.get(t, alpha.AgentID).State == StateIdle })
	if got := s.get(t, alpha.AgentID); *got.HarnessSessionID != aref.NativeID || turnsRun(e, aref) != 2 {
		t.Fatalf("session %s ran %d turns; want the same session %s with 2 (u1 once, u2 once)", *got.HarnessSessionID, turnsRun(e, aref), aref.NativeID)
	}
}

// TestSavedEffortSurvivesUnloadAndRestart: a create override's effort
// reaches the first turn; a PATCHed effort, which a harness keeps only in
// the live session, is set again when the session is resumed after an idle
// unload and restart, so the next turn still runs at it.
func TestSavedEffortSurvivesUnloadAndRestart(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	pump(t, s, e.h, e.st)
	info, err := s.Create(ctx, CreateRequest{Envelope: Envelope{RequestID: "alpha"}, Preset: "lead", Name: "alpha",
		Repo: "/repo", Overrides: Overrides{Harness: "opencode", Effort: "low"}})
	if err != nil {
		t.Fatal(err)
	}
	a := s.get(t, info.AgentID)
	ref := loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}
	effort := func(n int) string {
		t.Helper()
		eventually(t, "the turn ends", func() bool {
			return len(fh.Turns(ref)) == n && s.get(t, a.AgentID).State == StateIdle
		})
		return loomharness.OptionValue(fh.Turns(ref)[n-1].Options, loomharness.OptionEffort)
	}
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	if got := effort(1); got != "low" {
		t.Fatalf("first turn effort %q, want the create override's low", got)
	}
	if _, err := s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "e1"}, AgentID: a.AgentID, Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	IdleSweep(ctx, at(t, s, a.AgentID, 1800), []*Service{s})
	if loaded(s, ref) {
		t.Fatal("the idle session was not unloaded")
	}
	pump(t, s, e.h, e.st) // the restart ended the old feed
	mustSendMsg(t, s, sendReq(a.AgentID, "u2", "second", user))
	if got := effort(2); got != "high" {
		t.Fatalf("turn after unload and restart effort %q, want the saved high", got)
	}
}
