package loomagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// onAsk is a restarted service whose lead waits on ask a1, its harness
// wrapped by w.
func onAsk(t *testing.T, e *createEnv, w tweaked) (*Service, loomstore.Agent) {
	t.Helper()
	a := waitingOnAsk(t, e)
	s := e.service(ServiceConfig{})
	w.Harness = e.h
	s.harnesses["opencode"] = w
	reconcile(t, s)
	return s, a
}

func answer(a loomstore.Agent, requestID, decision string) RespondRequest {
	return RespondRequest{Envelope: Envelope{RequestID: requestID}, AgentID: a.AgentID, AskID: "a1", Decision: decision}
}

// claimState is the state of r1's claim on a's a1, or "" when it has none.
func claimState(t *testing.T, s *Service, a loomstore.Agent) string {
	t.Helper()
	c, err := s.store.AskClaim(context.Background(), a.AgentID, "a1", "r1")
	if errors.Is(err, loomstore.ErrNotFound) {
		return ""
	} else if err != nil {
		t.Fatal(err)
	}
	return c.State
}

// TestRespondRetrySameRequestId: a retry of an answered Respond returns its
// result and replies nothing more.
func TestRespondRetrySameRequestId(t *testing.T) {
	ctx := context.Background()
	var replies []loomharness.Reply
	s, a := onAsk(t, newCreateEnv(t), tweaked{replies: &replies})
	for i := range 2 {
		if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil {
			t.Fatalf("Respond %d = %v", i, err)
		}
	}
	if len(replies) != 1 {
		t.Fatalf("replies = %d; want 1", len(replies))
	}
}

// TestRespondRetryChangedPayload: the same request with another answer is a
// conflict and sends nothing.
func TestRespondRetryChangedPayload(t *testing.T) {
	ctx := context.Background()
	var replies []loomharness.Reply
	s, a := onAsk(t, newCreateEnv(t), tweaked{replies: &replies})
	if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "deny")), CodeConflict)
	if len(replies) != 1 {
		t.Fatalf("replies = %d; want 1", len(replies))
	}
}

// TestRespondCompetingRequestIds: two requests race to answer one ask; one
// Reply is sent, and the loser gets already_answered naming the winner.
func TestRespondCompetingRequestIds(t *testing.T) {
	ctx := context.Background()
	var replies []loomharness.Reply
	s, a := onAsk(t, newCreateEnv(t), tweaked{replies: &replies})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, id := range []string{"r1", "r2"} {
		wg.Go(func() { errs[i] = s.Respond(ctx, answer(a, id, "allow_once")) })
	}
	wg.Wait()
	win := slices.IndexFunc(errs, func(err error) bool { return err == nil })
	if win < 0 || len(replies) != 1 {
		t.Fatalf("errs %v replies %d; want one answer and one Reply", errs, len(replies))
	}
	if e := wantCode(t, errs[1-win], CodeAlreadyAnswered); e.Message != fmt.Sprintf("a1 answered by r%d", win+1) {
		t.Fatalf("loser = %q; want the winner named", e.Message)
	}
}

// TestRespondCrashBeforeReply: Loom crashed after saving the claim, before
// the Reply. After the restart the ask is still pending natively, so the
// claim is released and the ask can be answered, once; with no native
// evidence either way the claim is terminal and nothing is sent.
func TestRespondCrashBeforeReply(t *testing.T) {
	ctx := context.Background()
	for _, hidden := range []bool{false, true} {
		e := newCreateEnv(t)
		var replies []loomharness.Reply
		s, a := onAsk(t, e, tweaked{replies: &replies})
		if !crashDispatchAt(t, "claimed")(func() { _ = s.Respond(ctx, answer(a, "r1", "allow_once")) }) {
			t.Fatal("did not crash")
		}
		w := tweaked{Harness: e.h, replies: &replies}
		if hidden {
			w.gone = "a1" // the history shows nothing of a1
		}
		s = e.service(ServiceConfig{}) // the restart
		s.harnesses["opencode"] = w
		reconcile(t, s)
		if hidden {
			if st, r := claimState(t, s, a), deref(s.get(t, a.AgentID).AttentionReason); st != loomstore.ClaimUnknown || r != AttentionReplyUnknown {
				t.Fatalf("hidden: claim %q Attention %q; want unknown and reply_unknown", st, r)
			}
			wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeReplyUnknown)
			if len(replies) != 0 {
				t.Fatalf("hidden: replies = %d; want none", len(replies))
			}
			continue
		}
		if st := claimState(t, s, a); st != loomstore.ClaimReleased || len(replies) != 0 {
			t.Fatalf("claim %q replies %d; want released and none", st, len(replies))
		}
		if err := s.Respond(ctx, answer(a, "r2", "allow_once")); err != nil || len(replies) != 1 {
			t.Fatalf("Respond after restart = %v, replies %d; want one", err, len(replies))
		}
	}
}

// TestRespondCrashAfterReplyBeforeAck: the harness took the Reply and Loom
// crashed before saving it. The backfill proves it: the claim is replied, a
// retry returns success, and there was exactly one Reply.
func TestRespondCrashAfterReplyBeforeAck(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var replies []loomharness.Reply
	s, a := onAsk(t, e, tweaked{replies: &replies})
	if !crashDispatchAt(t, "replied")(func() { _ = s.Respond(ctx, answer(a, "r1", "allow_once")) }) {
		t.Fatal("did not crash")
	}
	s = e.service(ServiceConfig{}) // the restart
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies}
	reconcile(t, s)
	if st := claimState(t, s, a); st != loomstore.ClaimReplied {
		t.Fatalf("claim %q; want replied", st)
	}
	if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil {
		t.Fatalf("retry = %v; want success", err)
	}
	wantCode(t, s.Respond(ctx, answer(a, "r2", "allow_once")), CodeAlreadyAnswered)
	if len(replies) != 1 {
		t.Fatalf("replies = %d; want 1", len(replies))
	}
}

// landedThenFails is a harness whose Reply lands and then fails, as a
// connection lost before the answer's acknowledgement.
type landedThenFails struct{ loomharness.Harness }

func (h landedThenFails) Session(ref loomharness.NativeRef) loomharness.Session {
	return landedThenFailsSession{h.Harness.Session(ref)}
}

type landedThenFailsSession struct{ loomharness.Session }

func (x landedThenFailsSession) Reply(ctx context.Context, askID string, r loomharness.Reply) error {
	if err := x.Session.Reply(ctx, askID, r); err != nil {
		return err
	}
	return errors.New("connection reset")
}

// TestRespondAmbiguousReplyLanded: a Reply that failed after it may have
// gone out, which the native history shows resolved, is a success.
func TestRespondAmbiguousReplyLanded(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var replies []loomharness.Reply
	s, a := onAsk(t, e, tweaked{replies: &replies})
	s.harnesses["opencode"] = tweaked{Harness: landedThenFails{e.h}, replies: &replies}
	for i := range 2 {
		if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil {
			t.Fatalf("Respond %d = %v; want success", i, err)
		}
	}
	if st := claimState(t, s, a); st != loomstore.ClaimReplied || len(replies) != 1 {
		t.Fatalf("claim %q replies %d; want replied and one", st, len(replies))
	}
}

// TestRespondAmbiguousReplyPending: a Reply that failed after it may have
// gone out, whose ask is still pending natively, was not answered: the
// claim is released and the ask is answered by the next Respond.
func TestRespondAmbiguousReplyPending(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var replies []loomharness.Reply
	s, a := onAsk(t, e, tweaked{replies: &replies})
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies, replyErr: errors.New("connection reset")}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeHarnessError)
	if st, r := claimState(t, s, a), deref(s.get(t, a.AgentID).AttentionReason); st != loomstore.ClaimReleased || r != "" {
		t.Fatalf("claim %q Attention %q; want released and none", st, r)
	}
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies}
	if err := s.Respond(ctx, answer(a, "r2", "allow_once")); err != nil || len(replies) != 2 {
		t.Fatalf("Respond = %v replies %d; want success and two", err, len(replies))
	}
}

// TestRespondAmbiguousReplyUnknown: a Reply that failed after it may have
// gone out, with no native evidence either way, is terminal: the agent
// shows reply_unknown, and no later Respond sends a Reply.
func TestRespondAmbiguousReplyUnknown(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var replies []loomharness.Reply
	s, a := onAsk(t, e, tweaked{replies: &replies})
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies, replyErr: errors.New("connection reset"), gone: "a1"}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeReplyUnknown)
	if st, r := claimState(t, s, a), deref(s.get(t, a.AgentID).AttentionReason); st != loomstore.ClaimUnknown || r != AttentionReplyUnknown {
		t.Fatalf("claim %q Attention %q; want unknown and reply_unknown", st, r)
	}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeReplyUnknown)
	wantCode(t, s.Respond(ctx, answer(a, "r2", "allow_once")), CodeAlreadyAnswered)
	if len(replies) != 1 {
		t.Fatalf("replies = %d; want 1", len(replies))
	}
}

// TestRespondNotSentReleasesClaim: a Reply that failed before it was sent
// releases the claim without looking for evidence, and shows no Attention.
func TestRespondNotSentReleasesClaim(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var replies []loomharness.Reply
	s, a := onAsk(t, e, tweaked{replies: &replies})
	notSent := fmt.Errorf("dial: %w: %w", loomharness.ErrNotSent, loomharness.ErrUnavailable)
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies, replyErr: notSent, gone: "a1"}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeHarnessUnavailable)
	if st, r := claimState(t, s, a), deref(s.get(t, a.AgentID).AttentionReason); st != loomstore.ClaimReleased || r != "" {
		t.Fatalf("claim %q Attention %q; want released and none", st, r)
	}
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies}
	if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil || len(replies) != 2 {
		t.Fatalf("Respond = %v replies %d; want success and two", err, len(replies))
	}
}

// TestRespondReusedAskIDLaterTurn: codex reuses an ask ID on a later turn.
// That is a new ask: its claim is new, and it is answered. A delayed retry
// of the first turn's request gets its own outcome and answers nothing.
func TestRespondReusedAskIDLaterTurn(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	var replies []loomharness.Reply
	s.harnesses["opencode"] = tweaked{Harness: e.h, replies: &replies}
	stop := runFeed(t, s, "opencode")
	defer stop()
	a, _ := newLead(t, e, s, "alpha")
	e.h.Harness.(*fake.Harness).Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Delta: "one"}}},
		fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Delta: "two"}}})
	for i, id := range []string{"r1", "r2"} {
		mustSendMsg(t, s, sendReq(a.AgentID, fmt.Sprintf("u%d", i), "go", user))
		drained(t, s, "a1 opens", func() bool { return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) })
		if i == 1 { // r1 retried late, after the turn it answered
			if err := s.Respond(ctx, answer(a, "r1", "allow_once")); err != nil || len(replies) != 1 {
				t.Fatalf("late retry of r1 = %v, replies %d; want its success and no new Reply", err, len(replies))
			}
		}
		if err := s.Respond(ctx, answer(a, id, "allow_once")); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
		drained(t, s, "the turn ends", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	}
	if len(replies) != 2 {
		t.Fatalf("replies = %d; want one per turn", len(replies))
	}
}

// lateResolve is a harness whose first history read leaves out every
// ask.resolved, as one read just before an ask resolved.
type lateResolve struct {
	loomharness.Harness
	reads *atomic.Int32
}

func (h lateResolve) Session(ref loomharness.NativeRef) loomharness.Session {
	return lateResolveSession{h.Harness.Session(ref), h.reads}
}

type lateResolveSession struct {
	loomharness.Session
	reads *atomic.Int32
}

func (x lateResolveSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	page, err := x.Session.Messages(ctx, after, limit)
	if x.reads.Add(1) == 1 {
		page.Events = slices.DeleteFunc(page.Events, func(e loomharness.Event) bool { return e.Type == loomharness.EventAskResolved })
	}
	return page, err
}

// TestRestartSettledClaimClosesAsk: Loom crashed after a1's Reply landed.
// The backfill read the history just before a1 resolved, so a1 is open
// again; settling its claim as replied also closes it, leaving the turn
// waiting on a2 only.
func TestRestartSettledClaimClosesAsk(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	stop := runFeed(t, s, "opencode")
	a, _ := newLead(t, e, s, "alpha")
	e.h.Harness.(*fake.Harness).Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Ask: "a2"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	drained(t, s, "a1 opens", func() bool { return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) })
	stop()
	if !crashDispatchAt(t, "replied")(func() { _ = s.Respond(ctx, answer(a, "r1", "allow_once")) }) {
		t.Fatal("did not crash")
	}
	s = e.service(ServiceConfig{}) // the restart
	s.harnesses["opencode"] = lateResolve{e.h, &atomic.Int32{}}
	reconcile(t, s)
	if st, got := claimState(t, s, a), askIDs(t, s, a.AgentID); st != loomstore.ClaimReplied || !slices.Equal(got, []string{"a2:approval"}) {
		t.Fatalf("claim %q open asks %v; want replied and only a2", st, got)
	}
}

// notSentOnce is a harness whose first Reply fails before it is sent.
type notSentOnce struct {
	loomharness.Harness
	calls *atomic.Int32
}

func (h notSentOnce) Session(ref loomharness.NativeRef) loomharness.Session {
	return notSentOnceSession{h.Harness.Session(ref), h.calls}
}

type notSentOnceSession struct {
	loomharness.Session
	calls *atomic.Int32
}

func (x notSentOnceSession) Reply(ctx context.Context, askID string, r loomharness.Reply) error {
	if x.calls.Add(1) == 1 {
		return fmt.Errorf("dial: %w", loomharness.ErrNotSent)
	}
	return x.Session.Reply(ctx, askID, r)
}

// TestRespondReleasedRetryLaterTurn: r1's Reply on turn 1 was never sent,
// and r0 then answered that ask. Turn 2 reuses the ask ID. A late retry of
// r1 is for turn 1's ask, which is gone: ask_not_found, and no Reply.
func TestRespondReleasedRetryLaterTurn(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	calls := &atomic.Int32{}
	s.harnesses["opencode"] = notSentOnce{e.h, calls}
	stop := runFeed(t, s, "opencode")
	defer stop()
	a, _ := newLead(t, e, s, "alpha")
	e.h.Harness.(*fake.Harness).Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Delta: "one"}}},
		fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Delta: "two"}}})
	for i := range 2 {
		mustSendMsg(t, s, sendReq(a.AgentID, fmt.Sprintf("u%d", i), "go", user))
		drained(t, s, "a1 opens", func() bool { return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) })
		if i == 0 {
			wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeHarnessError)
			if err := s.Respond(ctx, answer(a, "r0", "allow_once")); err != nil {
				t.Fatal(err)
			}
		} else {
			wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeAskNotFound)
			if err := s.Respond(ctx, answer(a, "r2", "allow_once")); err != nil {
				t.Fatal(err)
			}
		}
		drained(t, s, "the turn ends", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("Replies = %d; want 3 (r1 unsent, r0, r2)", n)
	}
}

// TestRespondLeftClaimOwnOutcome: r1's claim, on an earlier turn's ask with
// this ID, was left pending; r2 has since answered the open ask. A retry of
// r1 settles r1's own claim (no evidence: reply_unknown), never taking r2's
// outcome, and sends nothing.
func TestRespondLeftClaimOwnOutcome(t *testing.T) {
	ctx := context.Background()
	var replies []loomharness.Reply
	s, a := onAsk(t, newCreateEnv(t), tweaked{replies: &replies})
	turn := s.openAsks(a.AgentID)[0].TurnID
	for _, c := range []loomstore.AskClaim{
		{AgentID: a.AgentID, AskID: "a1", TurnID: "t-old", RequestID: "r1", PayloadHash: answerHash(answer(a, "r1", "allow_once"))},
		{AgentID: a.AgentID, AskID: "a1", TurnID: turn, RequestID: "r2", PayloadHash: answerHash(answer(a, "r2", "allow_once"))},
	} {
		if _, won, err := s.store.ClaimAsk(ctx, c); err != nil || !won {
			t.Fatalf("claim %s: won %v, %v", c.RequestID, won, err)
		}
	}
	if err := s.store.SettleAskClaim(ctx, loomstore.AskClaim{AgentID: a.AgentID, AskID: "a1", TurnID: turn, RequestID: "r2"}, loomstore.ClaimReplied); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Respond(ctx, answer(a, "r1", "allow_once")), CodeReplyUnknown)
	if len(replies) != 0 {
		t.Fatalf("replies = %d; want none", len(replies))
	}
}
