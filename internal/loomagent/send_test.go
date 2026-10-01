package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

var (
	user  = ActorRef{Kind: "user", ID: "u"}
	child = ActorRef{Kind: "agent", ID: "c1"}
)

func sendReq(agentID, requestID, text string, from ActorRef) SendRequest {
	return SendRequest{Envelope: Envelope{RequestID: requestID}, AgentID: agentID, Text: text, Source: "user_chat", Actor: from}
}

func mustSendMsg(t *testing.T, s *Service, req SendRequest) SendResult {
	t.Helper()
	r, err := s.Send(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// waiting is the agent's waiting text by sender, in delivery order.
func waiting(t *testing.T, s *Service, agentID string) []string {
	t.Helper()
	slots, err := s.store.Slots(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, sl := range slots {
		if sl.State == loomstore.SlotWaiting {
			out = append(out, sl.Sender+"="+sl.Body)
		}
	}
	return out
}

// deliverNext hands over and delivers the agent's next waiting slot, as the
// 1.6 dispatcher will.
func deliverNext(t *testing.T, s *Service, agentID string) loomstore.Slot {
	t.Helper()
	ctx := context.Background()
	sl, err := s.store.HandNext(ctx, agentID, func(sl loomstore.Slot) string { return "k-" + sl.RequestID })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.MarkDelivered(ctx, agentID, sl.Sender, sl.RequestID); err != nil {
		t.Fatal(err)
	}
	return sl
}

func serviceAt(t *testing.T, path string, agents ...loomstore.Agent) *Service {
	t.Helper()
	st, err := loomstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, a := range agents {
		if err := st.InsertAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	return New(ServiceConfig{Store: st, Events: NewEventLog(st)})
}

// TestSendBusyReplacesOnlySendersBody: a busy agent keeps one waiting
// message per sender; a sender's second Send replaces only its own text and
// keeps its place, and each fill or edit emits message.waiting.
func TestSendBusyReplacesOnlySendersBody(t *testing.T) {
	for _, state := range []string{StateActive, StateWaiting, StateIdle} {
		t.Run(state, func(t *testing.T) {
			s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", state))
			sub := s.Bus.Subscribe("a1")
			r1 := mustSendMsg(t, s, sendReq("a1", "r1", "check the tests", user))
			mustSendMsg(t, s, sendReq("a1", "c1", "task_completed:c1:1", child))
			r2 := mustSendMsg(t, s, sendReq("a1", "r2", "check the tests\nand the lint", user))
			if r1.State != "waiting" || r1.Replaced || r2.State != "waiting" || !r2.Replaced || r1.MessageID == r2.MessageID {
				t.Fatalf("results = %+v, %+v", r1, r2)
			}
			if got := waiting(t, s, "a1"); !slices.Equal(got, []string{"user:u=check the tests\nand the lint", "agent:c1=task_completed:c1:1"}) {
				t.Fatalf("waiting = %q", got)
			}
			if got := types(drain(sub)); !slices.Equal(got, []string{EventWaiting, EventWaiting, EventWaiting}) {
				t.Fatalf("events = %v", got)
			}
			if a := s.get(t, "a1"); a.State != state {
				t.Fatalf("Send changed the state to %s; only the dispatcher hands over", a.State)
			}
		})
	}
}

// TestSendStaleReceiptRetryIsNoOp covers the §4.9 scenarios: a retry of an
// older Send after it was replaced, delivered or withdrawn, or after a
// restart, returns its original result and has no effect.
func TestSendStaleReceiptRetryIsNoOp(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, svcAgent("a1", "persistent", StateActive))
	sub := s.Bus.Subscribe("a1")
	retry := func(s *Service, req SendRequest, want SendResult, wantWaiting []string) {
		t.Helper()
		drain(sub)
		got, err := s.Send(ctx, req)
		if err != nil || got != want {
			t.Fatalf("retry %s = %+v, %v; want %+v", req.RequestID, got, err, want)
		}
		if w := waiting(t, s, "a1"); !slices.Equal(w, wantWaiting) {
			t.Fatalf("after retry %s waiting = %q; want %q", req.RequestID, w, wantWaiting)
		}
		if ev := drain(sub); len(ev) != 0 {
			t.Fatalf("retry %s emitted %v", req.RequestID, types(ev))
		}
	}

	// Replaced: R1 filled the slot, R2 replaced it.
	r1 := mustSendMsg(t, s, sendReq("a1", "r1", "one", user))
	r2 := mustSendMsg(t, s, sendReq("a1", "r2", "two", user))
	retry(s, sendReq("a1", "r1", "one", user), r1, []string{"user:u=two"})
	retry(s, sendReq("a1", "r2", "two", user), r2, []string{"user:u=two"}) // the latest Send too

	// Delivered: R2 was delivered, R3 waits.
	deliverNext(t, s, "a1")
	r3 := mustSendMsg(t, s, sendReq("a1", "r3", "three", user))
	retry(s, sendReq("a1", "r2", "two", user), r2, []string{"user:u=three"})
	retry(s, sendReq("a1", "r1", "one", user), r1, []string{"user:u=three"})

	// Withdrawn: R3 was withdrawn.
	if w, err := s.Withdraw(ctx, WithdrawRequest{AgentID: "a1", Actor: user}); err != nil || w.Result != loomstore.Withdrawn {
		t.Fatalf("Withdraw = %+v, %v", w, err)
	}
	retry(s, sendReq("a1", "r3", "three", user), r3, nil)

	// Restart: receipts survive; a retry still has no effect.
	r4 := mustSendMsg(t, s, sendReq("a1", "r4", "four", user))
	s2 := serviceAt(t, path)
	sub = s2.Bus.Subscribe("a1")
	for req, want := range map[string]SendResult{"r1": r1, "r2": r2, "r3": r3, "r4": r4} {
		retry(s2, sendReq("a1", req, "stale", user), want, []string{"user:u=four"})
	}
}

// TestSendConcurrentRetriesOneEffect: many concurrent Sends of one RequestID
// fill the slot once and all return the same result.
func TestSendConcurrentRetriesOneEffect(t *testing.T) {
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateActive))
	sub := s.Bus.Subscribe("a1")
	var wg sync.WaitGroup
	results := make([]SendResult, 16)
	errs := make([]error, 16)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.Send(context.Background(), sendReq("a1", "r1", "hello", user))
		}()
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || results[i] != results[0] || results[0].Replaced {
			t.Fatalf("Send %d = %+v, %v; first %+v", i, results[i], errs[i], results[0])
		}
	}
	if got := types(drain(sub)); !slices.Equal(got, []string{EventWaiting}) {
		t.Fatalf("events = %v; want one message.waiting", got)
	}
}

// TestWithdrawWaitingVsHanded: Withdraw clears only the caller's waiting
// message; a handed one is already_handed; each clear emits message.withdrawn.
func TestWithdrawWaitingVsHanded(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateActive))
	sub := s.Bus.Subscribe("a1")
	withdraw := func(from ActorRef, want string) {
		t.Helper()
		got, err := s.Withdraw(ctx, WithdrawRequest{AgentID: "a1", Actor: from})
		if err != nil || got.Result != want {
			t.Fatalf("Withdraw(%v) = %+v, %v; want %s", from, got, err, want)
		}
	}
	withdraw(user, loomstore.NothingWaiting)
	mustSendMsg(t, s, sendReq("a1", "r1", "mine", user))
	mustSendMsg(t, s, sendReq("a1", "c1", "theirs", child))
	if _, err := s.store.HandNext(ctx, "a1", func(loomstore.Slot) string { return "k" }); err != nil {
		t.Fatal(err)
	}
	drain(sub)
	withdraw(user, loomstore.AlreadyHanded)
	withdraw(child, loomstore.Withdrawn)
	withdraw(child, loomstore.NothingWaiting)
	if got := drain(sub); len(got) != 1 || got[0].Type != EventWithdrawn || got[0].Reason != "agent:c1" {
		t.Fatalf("events = %+v", got)
	}
	if got := waiting(t, s, "a1"); len(got) != 0 {
		t.Fatalf("waiting = %q", got)
	}
	// A new Send while the sender's message is handed and not yet delivered
	// can't overwrite it.
	if _, err := s.Send(ctx, sendReq("a1", "r2", "more", user)); !isCode(err, CodeAgentBusy) {
		t.Fatalf("Send over a handed message = %v; want agent_busy", err)
	}
}

// TestSendStateChecks: refused Sends store nothing; a creating agent that
// can't finish Create fails with harness_unavailable.
func TestSendStateChecks(t *testing.T) {
	ctx := context.Background()
	creating := svcAgent("creating", "persistent", StateCreating) // create_step 0: Create never finished step 1
	deleted := svcAgent("deleted", "persistent", StateIdle)
	deleted.DeletedAt = sp(loomstore.Stamp(time.Now()))
	s := newService(t, ServiceConfig{}, svcAgent("archived", "persistent", StateArchived),
		svcAgent("stopping", "persistent", StateStopping), creating, deleted)
	for id, code := range map[string]Code{"archived": CodeAgentArchived, "stopping": CodeAgentArchived,
		"creating": CodeHarnessUnavailable, "deleted": CodeAgentNotFound, "missing": CodeAgentNotFound} {
		if _, err := s.Send(ctx, sendReq(id, "r1", "hi", user)); !isCode(err, code) {
			t.Fatalf("Send to %s = %v; want %s", id, err, code)
		}
		if _, err := s.store.GetReceipt(ctx, id, "r1"); !errors.Is(err, loomstore.ErrNotFound) {
			t.Fatalf("refused Send to %s stored a receipt: %v", id, err)
		}
		if sl, _ := s.store.Slots(ctx, id); len(sl) != 0 {
			t.Fatalf("refused Send to %s stored a slot: %+v", id, sl)
		}
	}
	if _, err := s.Send(ctx, SendRequest{AgentID: "archived"}); !isCode(err, CodePresetInvalid) {
		t.Fatalf("Send without a RequestID = %v", err)
	}
	// A retry is answered from its receipt before any state check.
	a := svcAgent("later", "persistent", StateActive)
	s2 := newService(t, ServiceConfig{}, a)
	r := mustSendMsg(t, s2, sendReq("later", "r1", "hi", user))
	if err := s2.Archive(ctx, ArchiveRequest{AgentID: "later", Reason: ArchiveCancelled}); err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Send(ctx, sendReq("later", "r1", "hi", user)); err != nil || got != r {
		t.Fatalf("retry after Archive = %+v, %v; want %+v", got, err, r)
	}
}

// TestSendNewBackgroundAttemptCancelsExpiry: a Send to a finished background
// single task starts its next attempt and clears its R29 deadline in the
// receipt's transaction; a retry starts nothing; after a purge Send fails
// with history_expired.
func TestSendNewBackgroundAttemptCancelsExpiry(t *testing.T) {
	ctx := context.Background()
	old := loomstore.Stamp(time.Now().Add(-loomstore.HistoryRetention - time.Hour))
	task := func(id string) loomstore.Agent {
		a := svcAgent(id, "single_task", StateFinished)
		a.InteractionMode, a.RoleKind, a.Outcome, a.FinishedAt = "background", "daemon", sp("failed"), sp(old)
		return a
	}
	s := newService(t, ServiceConfig{}, task("t1"), task("t2"))
	sub := s.Bus.Subscribe("t1")
	r := mustSendMsg(t, s, sendReq("t1", "retry-1", "try again", ActorRef{Kind: "system", ID: "daemon"}))
	a := s.get(t, "t1")
	if a.State != StateActive || a.Attempt != 2 || a.Outcome != nil || a.FinishedAt != nil || r.State != "waiting" {
		t.Fatalf("after Send: state %s attempt %d outcome %v finished_at %v result %+v", a.State, a.Attempt, a.Outcome, a.FinishedAt, r)
	}
	if got := types(drain(sub)); !slices.Equal(got, []string{EventStateChanged, EventWaiting}) {
		t.Fatalf("events = %v", got)
	}
	if err := s.store.MarkHistoryPurged(ctx, "t1", time.Now()); !errors.Is(err, loomstore.ErrNotDue) {
		t.Fatalf("sweep after the new attempt = %v; want ErrNotDue", err)
	}
	if got, err := s.Send(ctx, sendReq("t1", "retry-1", "try again", ActorRef{Kind: "system", ID: "daemon"})); err != nil || got != r {
		t.Fatalf("retry = %+v, %v", got, err)
	}
	if a := s.get(t, "t1"); a.Attempt != 2 {
		t.Fatalf("a retry started attempt %d", a.Attempt)
	}

	if err := s.store.MarkHistoryPurged(ctx, "t2", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, sendReq("t2", "retry-1", "try again", user)); !isCode(err, CodeHistoryExpired) {
		t.Fatalf("Send after purge = %v; want history_expired", err)
	}
	if a := s.get(t, "t2"); a.State != StateFinished || a.Attempt != 1 {
		t.Fatalf("a refused Send changed the agent: %s attempt %d", a.State, a.Attempt)
	}
}

// TestSendPayloadNearRESTLimitLossless: Send adds no text cap. A request whose
// JSON body is just under the existing 1 MiB REST guard (internal/webui
// maxRequestBody), with multi-byte UTF-8 and characters JSON must escape,
// decodes and is stored byte for byte; a slot edit holding several distinct
// child completion records is stored and replaced losslessly.
func TestSendPayloadNearRESTLimitLossless(t *testing.T) {
	const restBodyLimit = 1 << 20
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateActive))
	unit := "résumé ✓ 日本語 \"quoted\"\n\t<tag>&\\ 🚀 "
	encode := func(text string) []byte {
		b, _ := json.Marshal(map[string]any{"agentId": "a1", "requestId": "r1", "text": text, "source": "user_chat"})
		return b
	}
	per := len(encode(unit+unit)) - len(encode(unit)) // escaped bytes per unit
	text := strings.Repeat(unit, (restBodyLimit-len(encode("")))/per)
	body, _ := json.Marshal(map[string]any{"agentId": "a1", "requestId": "r1", "text": text, "source": "user_chat"})
	if len(body) > restBodyLimit || len(body) < restBodyLimit-len(unit)*4 || len(text) < 512*1024 || !utf8.ValidString(text) {
		t.Fatalf("fixture: body %d bytes, text %d bytes", len(body), len(text))
	}
	var in struct{ AgentID, RequestID, Text, Source string }
	if err := json.Unmarshal(body, &in); err != nil {
		t.Fatal(err)
	}
	mustSendMsg(t, s, sendReq(in.AgentID, in.RequestID, in.Text, user))
	if got := waiting(t, s, "a1"); len(got) != 1 || got[0] != "user:u="+text {
		t.Fatalf("stored text differs: %d bytes; want %d", len(strings.TrimPrefix(got[0], "user:u=")), len(text))
	}

	// One child's slot holds distinct short completion records; an edit adds
	// a record and keeps the first, byte for byte.
	first := "task_completed:c1:1 outcome=failed summary=\"tests failed in pkg/x\""
	both := first + "\n" + "task_completed:c1:2 outcome=done summary=\"fixed; PR #7\""
	mustSendMsg(t, s, sendReq("a1", "c1-1", first, child))
	if r := mustSendMsg(t, s, sendReq("a1", "c1-2", both, child)); !r.Replaced {
		t.Fatalf("second record = %+v; want Replaced", r)
	}
	if got := waiting(t, s, "a1"); !slices.Equal(got, []string{"user:u=" + text, "agent:c1=" + both}) {
		t.Fatalf("child slot = %q", got[len(got)-1])
	}
}

// TestSendToCreatingRunsCreateStep: Create crashed before opening the
// session; Send finishes Create (step 3) itself, then stores the message
// behind the creator's first message.
func TestSendToCreatingRunsCreateStep(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "open")
	req := leadReq("r1")
	req.FirstMessage = "hello"
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
		t.Fatal("did not crash")
	}
	s := e.service(ServiceConfig{})
	created, err := e.st.FindCreated(ctx, s.workspaceID, req.ExternalKey, req.RequestID)
	if err != nil || created.State != StateCreating {
		t.Fatalf("crashed Create left %+v, %v", created, err)
	}
	r, err := s.Send(ctx, sendReq(created.AgentID, "s1", "and this", user))
	if err != nil || r.State != "waiting" {
		t.Fatalf("Send = %+v, %v", r, err)
	}
	a := s.get(t, created.AgentID)
	owned, _ := e.st.NativeSessions(ctx, a.AgentID)
	if a.State != StateIdle || a.CreateStep != stepDone || len(owned) != 1 {
		t.Fatalf("after Send: state %s step %d owned %d", a.State, a.CreateStep, len(owned))
	}
	if got := waiting(t, s, a.AgentID); len(got) != 2 || !strings.HasSuffix(got[0], "=hello") || got[1] != "user:u=and this" {
		t.Fatalf("waiting = %q", got)
	}
}
