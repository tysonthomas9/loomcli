package loomstore

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestLimitResumeOptInOffByDefault (OR7): a workspace is opted out until
// set, and the setting is per workspace.
func TestLimitResumeOptInOffByDefault(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	if on, err := s.LimitResumeOn(ctx, "ws"); err != nil || on {
		t.Fatalf("unset opt-in = %t, %v; want off", on, err)
	}
	for _, set := range []bool{true, false, true} {
		if err := s.SetLimitResumeOn(ctx, "ws", set); err != nil {
			t.Fatal(err)
		}
		if on, err := s.LimitResumeOn(ctx, "ws"); err != nil || on != set {
			t.Fatalf("opt-in = %t, %v; want %t", on, err, set)
		}
	}
	if on, _ := s.LimitResumeOn(ctx, "other"); on {
		t.Fatal("another workspace's opt-in is on")
	}
}

// TestLimitResumeCrashAtSendCommit (OR7): a resume Send's receipt and its
// consumed eligibility commit together. A crash before the commit leaves
// the resume owed and no receipt; after it, the receipt and nothing owed,
// and a retry of the same request changes nothing.
func TestLimitResumeCrashAtSendCommit(t *testing.T) {
	ctx := context.Background()
	s, path := newSlotStore(t)
	if err := s.SetLimitResumeOn(ctx, "ws", true); err != nil {
		t.Fatal(err)
	}
	due := Stamp(time.Now().Add(-time.Second))
	if err := s.PutLimitResume(ctx, LimitResume{AgentID: "a1", TurnID: "t1", Attempt: 1, DueAt: due}); err != nil {
		t.Fatal(err)
	}
	in := send("loom:limit-resume", "limit-resume:a1:t1:1", "Continue where you left off.")
	in.LimitResume = true
	commitStateCrash = func() { panic("crash") }
	t.Cleanup(func() { commitStateCrash = func() {} })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("did not crash")
			}
		}()
		_, _, _ = s.Send(ctx, in)
	}()
	commitStateCrash = func() {}
	s = openAt(t, path) // restart
	if _, err := s.GetReceipt(ctx, "a1", in.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt after a crash before the commit: %v", err)
	}
	if r, err := s.GetLimitResume(ctx, "a1"); err != nil || r.DueAt != due {
		t.Fatalf("resume after a crash before the commit = %+v, %v; want still owed at %s", r, err, due)
	}
	if _, retry := mustSend(t, s, in); retry {
		t.Fatal("first committed Send reported a retry")
	}
	r, err := s.GetLimitResume(ctx, "a1")
	if err != nil || r.DueAt != "" || r.Attempt != 1 {
		t.Fatalf("resume after its Send = %+v, %v; want sent, attempt 1 kept", r, err)
	}
	if due, err := s.DueLimitResumes(ctx, "ws", time.Now()); err != nil || len(due) != 0 {
		t.Fatalf("due after the Send = %+v, %v; want none", due, err)
	}
	if err := s.PutLimitResume(ctx, LimitResume{AgentID: "a1", TurnID: "t1", Attempt: 1, DueAt: due0(t)}); err != nil {
		t.Fatal(err)
	}
	if _, retry := mustSend(t, s, in); !retry {
		t.Fatal("a retry was not answered by its receipt")
	}
	if r, _ := s.GetLimitResume(ctx, "a1"); r.DueAt == "" {
		t.Fatal("a retry consumed a resume")
	}
}

// TestLimitResumeOtherSendDrops (OR7): any Send that is not a resume ends
// the episode: it drops the resume owed in its own transaction.
func TestLimitResumeOtherSendDrops(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	if err := s.PutLimitResume(ctx, LimitResume{AgentID: "a1", TurnID: "t1", Attempt: 2, DueAt: due0(t)}); err != nil {
		t.Fatal(err)
	}
	mustSend(t, s, send("user:u", "r1", "hi"))
	if r, err := s.GetLimitResume(ctx, "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resume after a user Send = %+v, %v; want dropped", r, err)
	}
}

func due0(t *testing.T) string {
	t.Helper()
	return Stamp(time.Now().Add(-time.Second))
}

// TestLimitResumeSendChecksInItsTransaction (OR7): a resume Send commits
// only while its resume is owed and unsent and the workspace opts in, all
// read in the Send's own transaction, so an opt-out or another resume that
// commits first wins; nothing is stored then.
func TestLimitResumeSendChecksInItsTransaction(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	resume := func(req string) SlotSend {
		in := send("loom:limit-resume", req, "Continue where you left off.")
		in.LimitResume = true
		return in
	}
	if err := s.PutLimitResume(ctx, LimitResume{AgentID: "a1", TurnID: "t1", Attempt: 1, DueAt: due0(t)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLimitResumeOn(ctx, "ws", false); err != nil { // opted out after the resume was owed
		t.Fatal(err)
	}
	if _, _, err := s.Send(ctx, resume("r-off")); !errors.Is(err, ErrLimitResumeGone) {
		t.Fatalf("resume while opted out: %v; want ErrLimitResumeGone", err)
	}
	if err := s.SetLimitResumeOn(ctx, "ws", true); err != nil {
		t.Fatal(err)
	}
	mustSend(t, s, resume("r1"))
	if _, _, err := s.Send(ctx, resume("r2")); !errors.Is(err, ErrLimitResumeGone) {
		t.Fatalf("second resume of one owed: %v; want ErrLimitResumeGone", err)
	}
	for _, req := range []string{"r-off", "r2"} {
		if _, err := s.GetReceipt(ctx, "a1", req); !errors.Is(err, ErrNotFound) {
			t.Fatalf("refused resume %s left a receipt: %v", req, err)
		}
	}
}
