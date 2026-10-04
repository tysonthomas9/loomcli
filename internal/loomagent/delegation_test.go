package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"database/sql"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// childOf is a running single task of parent.
func childOf(id, parent string) loomstore.Agent {
	a := busy(id, "single_task", StateActive)
	a.ParentAgentID, a.Preset = &parent, "task"
	return a
}

// endAttempt ends c's running turn with outcome, as the harness feed does.
func endAttempt(t *testing.T, s *Service, id, outcome string) {
	t.Helper()
	a := s.get(t, id)
	if err := s.turnCompleted(context.Background(), a, loomharness.Event{TurnID: *a.RunningTurnID, StopReason: outcome}); err != nil {
		t.Fatal(err)
	}
}

// nextAttempt starts c's next attempt with a running turn through the
// store's reopen, as a lead's Send does, but stops before Send publishes its
// state change (a crash there): only what the reopen committed remains.
func nextAttempt(t *testing.T, s *Service, id string) {
	t.Helper()
	ctx := context.Background()
	a := s.get(t, id)
	req := "next-" + strconv.FormatInt(a.Attempt+1, 10)
	if _, _, err := s.store.Send(ctx, loomstore.SlotSend{AgentID: id, Sender: "agent:lead", RequestID: req, Body: "again",
		Source: "agent", Hand: true, NativeKey: "k-" + req, Reopen: true,
		Result: func(bool) (string, error) { return "{}", nil }}); err != nil {
		t.Fatal(err)
	}
	a = s.get(t, id)
	to := a.StateOf()
	to.RunningTurn = sp("turn_" + req)
	if _, err := s.setState(ctx, a, to); err != nil {
		t.Fatal(err)
	}
}

func completions(t *testing.T, s *Service, parent string) []TaskCompleted {
	t.Helper()
	var out []TaskCompleted
	for _, e := range rows(t, s, parent, 0) {
		if e.Kind != KindTaskCompleted {
			continue
		}
		var rec TaskCompleted
		if err := json.Unmarshal(e.Payload, &rec); err != nil {
			t.Fatal(err)
		}
		out = append(out, rec)
	}
	return out
}

func dispatchOK(t *testing.T, s *Service, id string) {
	t.Helper()
	if err := s.Dispatch(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

// TestTaskCompletedTwoAttemptsBeforeLeadReads: attempts 1 and 2 finish
// while the lead is busy; the child's one slot on the lead holds both
// records in order, and retrying either changes nothing.
func TestTaskCompletedTwoAttemptsBeforeLeadReads(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	endAttempt(t, s, "c1", "failed")
	dispatchOK(t, s, "L")
	nextAttempt(t, s, "c1")
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	want := []string{"agent:c1=" + `task_completed:c1:1 outcome=failed branch= head= summary=""` + "\n" +
		`task_completed:c1:2 outcome=completed branch= head= summary=""`}
	if got := waiting(t, s, "L"); !slices.Equal(got, want) {
		t.Fatalf("lead slots = %q", got)
	}
	// Retry both attempts' records and deliveries: nothing changes.
	for _, attempt := range []int64{1, 2} {
		a := s.get(t, "c1")
		a.Attempt = attempt
		if err := s.recordCompletion(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	s.recordCompletions(ctx)
	dispatchOK(t, s, "L")
	if got := waiting(t, s, "L"); !slices.Equal(got, want) {
		t.Fatalf("after retries lead slots = %q", got)
	}
	if got := completions(t, s, "L"); len(got) != 2 || got[0].Outcome != "failed" || got[1].Outcome != "completed" {
		t.Fatalf("records = %+v", got)
	}
}

// TestTaskCompletedOlderAttemptAfterDelivery: a repeated record of an
// attempt the lead already read is not delivered again, and a later
// attempt fills the slot afresh.
func TestTaskCompletedOlderAttemptAfterDelivery(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	deliverNext(t, s, "L")
	old := s.get(t, "c1")
	nextAttempt(t, s, "c1")
	if err := s.recordCompletion(ctx, old); err != nil {
		t.Fatal(err)
	}
	dispatchOK(t, s, "L")
	if got := waiting(t, s, "L"); len(got) != 0 {
		t.Fatalf("redelivered: %q", got)
	}
	endAttempt(t, s, "c1", "failed")
	dispatchOK(t, s, "L")
	if got := waiting(t, s, "L"); !slices.Equal(got, []string{"agent:c1=" +
		`task_completed:c1:2 outcome=failed branch= head= summary=""`}) {
		t.Fatalf("lead slots = %q", got)
	}
}

// TestTaskCompletedWhileSlotHanded: a record that arrives while the child's
// previous record is being handed over waits in history, then fills the
// slot once that message is delivered.
func TestTaskCompletedWhileSlotHanded(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	sl, err := s.store.HandNext(ctx, "L", func(loomstore.Slot) string { return "k1" })
	if err != nil {
		t.Fatal(err)
	}
	nextAttempt(t, s, "c1")
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	if got := waiting(t, s, "L"); len(got) != 0 {
		t.Fatalf("handed slot changed: %q", got)
	}
	if err := s.store.MarkDelivered(ctx, "L", sl.Sender, sl.RequestID); err != nil {
		t.Fatal(err)
	}
	dispatchOK(t, s, "L")
	if got := waiting(t, s, "L"); len(got) != 1 || !strings.HasPrefix(got[0], "agent:c1=task_completed:c1:2 ") {
		t.Fatalf("lead slots = %q", got)
	}
}

// TestTaskCompletedCancelledAndAttention: Attention alone does not notify;
// a cancel does, with outcome cancelled.
func TestTaskCompletedCancelledAndAttention(t *testing.T) {
	ctx := context.Background()
	noop := func(context.Context, loomstore.Agent) error { return nil }
	s := newService(t, ServiceConfig{Interrupt: noop}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	if _, err := s.raiseAttention(ctx, s.get(t, "c1"), AttentionHarnessUnavailable); err != nil {
		t.Fatal(err)
	}
	if got := completions(t, s, "L"); len(got) != 0 {
		t.Fatalf("Attention notified: %+v", got)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "c1", Reason: ArchiveCancelled}); err != nil {
		t.Fatal(err)
	}
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Outcome != ArchiveCancelled || got[0].Attempt != 1 {
		t.Fatalf("records = %+v", got)
	}
}

// TestTaskCompletedArchivedParentKeepsHistory: the record of a child that
// finishes after its lead was archived is kept in the lead's history and
// never put in a slot.
func TestTaskCompletedArchivedParentKeepsHistory(t *testing.T) {
	s := newService(t, ServiceConfig{}, svcAgent("L", "persistent", StateArchived), childOf("c1", "L"))
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	if got := completions(t, s, "L"); len(got) != 1 {
		t.Fatalf("records = %+v", got)
	}
	if slots, err := s.store.Slots(context.Background(), "L"); err != nil || len(slots) != 0 {
		t.Fatalf("archived lead slots = %+v, %v", slots, err)
	}
}

// headWorkspace is a Workspace port whose Status reports a set branch and head.
type headWorkspace struct {
	fakeWorkspace
	branch, head string
}

func (w *headWorkspace) Status(context.Context, WorkspaceSpec) (WorkspaceStatus, error) {
	return WorkspaceStatus{Branch: w.branch, HEAD: w.head}, nil
}

// TestTaskCompletedWorkspaceResult: the record takes branch and head from
// the Workspace port, not git, and a retry after the head moved returns the
// same recorded branch and head.
func TestTaskCompletedWorkspaceResult(t *testing.T) {
	ctx := context.Background()
	ws := &headWorkspace{branch: "loom/agent/c1", head: "abc123"}
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	s := newService(t, ServiceConfig{Workspace: ws}, busy("L", "persistent", StateActive), c)
	for _, text := range []string{"working", "fixed it; tests pass"} { // the record quotes the last reply
		if err := s.appendEvent(ctx, "c1", "item.completed", "item:"+text, map[string]string{"itemKind": "message", "text": text}); err != nil {
			t.Fatal(err)
		}
	}
	endAttempt(t, s, "c1", "completed")
	ws.head = "def456"
	if err := s.recordCompletion(ctx, s.get(t, "c1")); err != nil {
		t.Fatal(err)
	}
	got := completions(t, s, "L")
	if len(got) != 1 || got[0].Branch != "loom/agent/c1" || got[0].Head != "abc123" || got[0].Summary != "fixed it; tests pass" {
		t.Fatalf("records = %+v", got)
	}
	dispatchOK(t, s, "L")
	if w := waiting(t, s, "L"); len(w) != 1 || !strings.Contains(w[0], "branch=loom/agent/c1 head=abc123 ") {
		t.Fatalf("lead slots = %q", w)
	}
}

// TestTaskCompletedSweepAfterCrash: a crash after the child finished but
// before its record was saved; the start-up sweep saves it once.
func TestTaskCompletedSweepAfterCrash(t *testing.T) {
	c := childOf("c1", "L")
	c.State, c.RunningTurnID, c.Outcome = StateFinished, nil, sp("completed")
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), c)
	s.recordCompletions(context.Background())
	s.recordCompletions(context.Background())
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Attempt != 1 {
		t.Fatalf("records = %+v", got)
	}
	ids, err := s.store.PendingAgents(context.Background(), "ws")
	if err != nil || !slices.Equal(ids, []string{"L"}) {
		t.Fatalf("pending = %v, %v", ids, err)
	}
}

// TestTaskCompletedReconcileOnce: a child's turn ends while Loom is down;
// Reconcile finishes it and records task_completed on the lead once, with
// child.created before it, and a repeat Reconcile adds nothing.
func TestTaskCompletedReconcileOnce(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	info, err := s.Create(ctx, CreateRequest{Envelope: Envelope{RequestID: "c"}, Preset: "task", Name: "c",
		Parent: lead.AgentID, Repo: "/repo", Overrides: Overrides{Harness: "opencode"}, FirstMessage: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.events(t, lead.AgentID, KindChildCreated); got != 1 {
		t.Fatalf("child.created = %d", got)
	}
	runDispatcher(t, s)
	reconcile(t, s)
	reconcile(t, s)
	// The dispatcher hands the record to the idle lead without a manual Dispatch.
	drained(t, s, "the lead is handed the record", func() bool {
		slots, err := s.store.Slots(ctx, lead.AgentID)
		return err == nil && slices.ContainsFunc(slots, func(sl loomstore.Slot) bool {
			return sl.Sender == "agent:"+info.AgentID && sl.State != loomstore.SlotWaiting &&
				strings.HasPrefix(sl.Body, completionKey(info.AgentID, 0)+" outcome=completed ")
		})
	})
	got := completions(t, s, lead.AgentID)
	if len(got) != 1 || got[0].Child != info.AgentID || got[0].Attempt != s.get(t, info.AgentID).Attempt ||
		got[0].Head != "abc" {
		t.Fatalf("records = %+v", got)
	}
	if c := s.get(t, info.AgentID); c.State != StateFinished {
		t.Fatalf("child state = %s", c.State)
	}
}

// flakyStatus is a Workspace port whose Status fails while fail is set.
type flakyStatus struct {
	headWorkspace
	fail atomic.Bool
}

func (w *flakyStatus) Status(ctx context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	if w.fail.Load() {
		return WorkspaceStatus{}, errors.New("status failed")
	}
	return w.headWorkspace.Status(ctx, s)
}

// TestTaskCompletedPortFailureRetries: a failed Workspace.Status saves no
// record; the dispatcher retries it and saves one, with the port's head.
func TestTaskCompletedPortFailureRetries(t *testing.T) {
	ws := &flakyStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc123"}}
	ws.fail.Store(true)
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	s := newService(t, ServiceConfig{Workspace: ws}, busy("L", "persistent", StateActive), c)
	clk := useTestClock(s)
	endAttempt(t, s, "c1", "completed")
	if got := completions(t, s, "L"); len(got) != 0 || s.get(t, "c1").State != StateFinished {
		t.Fatalf("records after a failed status = %+v", got)
	}
	runDispatcher(t, s) // its start-up sweep fails too
	for range 3 {       // retries while the port still fails save nothing
		clk.tick(t)
	}
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 0 {
		t.Fatalf("saved while failing: %+v", got)
	}
	ws.fail.Store(false)
	clk.tick(t)
	drained(t, s, "the record is saved", func() bool { return len(completions(t, s, "L")) == 1 })
	for range 3 { // nothing is owed: more ticks save nothing more
		clk.tick(t)
	}
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Head != "abc123" || got[0].Branch != "loom/agent/c1" {
		t.Fatalf("records = %+v", got)
	}
}

// TestClipBound: the summary, its mark included, never exceeds the cap and
// never splits a rune.
func TestClipBound(t *testing.T) {
	for _, in := range []string{strings.Repeat("a", 501), strings.Repeat("é", 400), strings.Repeat("a", 499) + "€€"} {
		got := clip(in, summaryCap)
		if len(got) > summaryCap || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
			t.Fatalf("clip(%d bytes) = %d bytes, valid %v", len(in), len(got), utf8.ValidString(got))
		}
	}
	if got := clip("short", summaryCap); got != "short" {
		t.Fatalf("clip(short) = %q", got)
	}
}

// TestTaskCompletedSummaryIsCurrentAttempts: each record quotes only its own
// attempt's last reply; an attempt with none has an empty summary.
func TestTaskCompletedSummaryIsCurrentAttempts(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	reply := func(text string) {
		t.Helper()
		if err := s.appendEvent(ctx, "c1", "item.completed", "item:"+text, map[string]string{"itemKind": "message", "text": text}); err != nil {
			t.Fatal(err)
		}
	}
	reply("attempt one done")
	endAttempt(t, s, "c1", "completed")
	nextAttempt(t, s, "c1") // no reply in attempt 2
	endAttempt(t, s, "c1", "failed")
	nextAttempt(t, s, "c1")
	reply("attempt three done")
	endAttempt(t, s, "c1", "completed")
	got := completions(t, s, "L")
	if len(got) != 3 || got[0].Summary != "attempt one done" || got[1].Summary != "" || got[2].Summary != "attempt three done" {
		t.Fatalf("records = %+v", got)
	}
}

// TestTaskCompletedSummaryAfterReopenCrash: Loom crashes after a reopen
// committed but before Send published it, then restarts; the attempt with
// no reply still has an empty summary, not the previous attempt's reply.
func TestTaskCompletedSummaryAfterReopenCrash(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	if err := s.appendEvent(ctx, "c1", "item.completed", "item:old", map[string]string{"itemKind": "message", "text": "old reply"}); err != nil {
		t.Fatal(err)
	}
	endAttempt(t, s, "c1", "completed")
	nextAttempt(t, s, "c1")  // the reopen commits; Send's state change is never published
	s2 := serviceAt(t, path) // restart on the same database
	for _, e := range kinds(rows(t, s2, "c1", 0), EventStateChanged) {
		if strings.Contains(string(e.Payload), `"from":"finished"`) {
			t.Fatalf("setup: a reopen event was saved: %s", e.Payload)
		}
	}
	endAttempt(t, s2, "c1", "failed")
	s2.recordCompletions(ctx)
	got := completions(t, s2, "L")
	if len(got) != 2 || got[0].Summary != "old reply" || got[1].Attempt != 2 || got[1].Summary != "" {
		t.Fatalf("records = %+v", got)
	}
}

// TestWaitingCompletionsSplit: a child's message and its task_completed
// record merged in its one slot on the lead read back as the message, with
// the record named by child and attempt from the keys Notify stored, never
// from the text (DF1); the delivery of that slot carries the same split from
// its hand-over. A later message from the child that repeats a record line
// word for word stays the child's message, and a record alone leaves an
// empty message. The slot text the lead reads is unchanged.
func TestWaitingCompletionsSplit(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, busy("L", "persistent", StateActive), childOf("c1", "L"))
	waitingFrom := func(sender string) WaitingMessage {
		t.Helper()
		info, err := s.Get(ctx, "L")
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range info.WaitingMessages {
			if w.Sender == sender {
				return w
			}
		}
		t.Fatalf("nothing waits from %s: %+v", sender, info.WaitingMessages)
		return WaitingMessage{}
	}
	type payload struct {
		Text, Message string
		Completions   []Completion
	}
	// deliver hands the child's slot over and saves its delivery as the feed does.
	deliver := func() payload {
		t.Helper()
		sl := deliverNext(t, s, "L")
		e := loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: "k-" + sl.RequestID,
			Sender: sl.Sender, Text: sl.Body}
		row, err := s.withCompletions(ctx, "L", nativeRow("L", "message.delivered", e), e)
		if err != nil {
			t.Fatal(err)
		}
		var p payload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(row.Payload), `"completions":`) {
			t.Fatalf("an agent's delivery names no completions field: %s", row.Payload)
		}
		return p
	}

	mustSendMsg(t, s, sendReq("L", "m1", "Heads up:\nuse cursor paging", child))
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	rec := `task_completed:c1:1 outcome=completed branch= head= summary=""`
	w := waitingFrom("agent:c1")
	if w.Text != "Heads up:\nuse cursor paging\n"+rec {
		t.Fatalf("slot text = %q", w.Text)
	}
	if w.Message != "Heads up:\nuse cursor paging" || !slices.Equal(w.Completions, []Completion{{"c1", 1}}) {
		t.Fatalf("waiting split = %q %+v", w.Message, w.Completions)
	}
	if p := deliver(); p.Text != w.Text || p.Message != w.Message || !slices.Equal(p.Completions, w.Completions) {
		t.Fatalf("delivered = %+v", p)
	}

	// The child repeats its record's line in a message of its own: it is
	// still the child's message, with no record.
	mustSendMsg(t, s, sendReq("L", "m2", rec, child))
	if w := waitingFrom("agent:c1"); w.Completions != nil || w.Message != "" || w.Text != rec {
		t.Fatalf("repeated line taken for a record: %+v", w)
	}
	if p := deliver(); p.Message != rec || len(p.Completions) != 0 {
		t.Fatalf("repeated line delivered as = %+v", p)
	}

	// Only a record: nothing is left of the message.
	nextAttempt(t, s, "c1")
	endAttempt(t, s, "c1", "failed")
	dispatchOK(t, s, "L")
	if w := waitingFrom("agent:c1"); w.Message != "" || !slices.Equal(w.Completions, []Completion{{"c1", 2}}) {
		t.Fatalf("record-only waiting = %+v", w)
	}
	if p := deliver(); p.Message != "" || !slices.Equal(p.Completions, []Completion{{"c1", 2}}) {
		t.Fatalf("record-only delivered = %+v", p)
	}

	// A user's delivery is never given the fields.
	e := loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: "k-u", Sender: "user:u", Text: rec}
	row, err := s.withCompletions(ctx, "L", nativeRow("L", "message.delivered", e), e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row.Payload), "completions") {
		t.Fatalf("user delivery split: %s", row.Payload)
	}
}

// TestLegacyCompletionSlotsUpgrade: a store saved before slots kept their
// notices, holding a handed slot (a child's message and its record) and a
// waiting one (only a record), opens with the new schema; Get and the
// delivery each show the record as one completion and the child's own text
// as its message, with no raw record line, and a later plain message from
// the child stays a message.
func TestLegacyCompletionSlotsUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("L", "persistent", StateActive), childOf("c1", "L"), childOf("c2", "L"))
	useTestClock(s)
	mustSendMsg(t, s, sendReq("L", "m1", "Heads up:\nuse cursor paging", child))
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	handed := deliverNext(t, s, "L")
	endAttempt(t, s, "c2", "failed")
	dispatchOK(t, s, "L")

	rollBackNotices(t, path)

	s2 := serviceAt(t, path)
	useTestClock(s2)
	waiting := func() map[string]WaitingMessage {
		t.Helper()
		info, err := s2.Get(ctx, "L")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]WaitingMessage{}
		for _, w := range info.WaitingMessages {
			out[w.Sender] = w
		}
		return out
	}
	w := waiting()["agent:c2"]
	if w.Message != "" || !slices.Equal(w.Completions, []Completion{{"c2", 1}}) {
		t.Fatalf("legacy waiting = %+v", w)
	}
	e := loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: "k-" + handed.RequestID,
		Sender: handed.Sender, Text: handed.Body}
	row, err := s2.withCompletions(ctx, "L", nativeRow("L", "message.delivered", e), e)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Message     string
		Completions []Completion
	}
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Message != "Heads up:\nuse cursor paging" || !slices.Equal(p.Completions, []Completion{{"c1", 1}}) {
		t.Fatalf("legacy delivery = %+v", p)
	}

	// A row written since the upgrade is never read by text, even with a
	// request id shaped like a record's.
	mustSendMsg(t, s2, sendReq("L", "task_completed:c1:9", "one more thing", child))
	if w := waiting()["agent:c1"]; w.Message != "" || w.Completions != nil || w.Text != "one more thing" {
		t.Fatalf("plain message after upgrade = %+v", w)
	}
}

// rollBackNotices returns the store file at path to the schema before slots
// kept their notices, as a store saved by the prior release.
func rollBackNotices(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"ALTER TABLE agent_slots DROP COLUMN notices", "ALTER TABLE agent_send_receipts DROP COLUMN notices",
		"PRAGMA user_version = " + strconv.Itoa(v-1)} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

// TestLegacySlotRepeatedRecordMessage: before the upgrade, a child's
// attempt-1 record was delivered, the child then sent a message whose text is
// exactly that record's line, and its attempt-2 record merged after it. After
// the upgrade, Get and the delivery show the message (the repeated line) and
// one completion, attempt 2 alone.
func TestLegacySlotRepeatedRecordMessage(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	useTestClock(s)
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	deliverNext(t, s, "L")
	rec1 := completions(t, s, "L")[0].text()
	mustSendMsg(t, s, sendReq("L", "m1", rec1, child))
	nextAttempt(t, s, "c1")
	endAttempt(t, s, "c1", "completed")
	dispatchOK(t, s, "L")
	rollBackNotices(t, path)

	s2 := serviceAt(t, path)
	useTestClock(s2)
	info, err := s2.Get(ctx, "L")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.WaitingMessages) != 1 {
		t.Fatalf("waiting = %+v", info.WaitingMessages)
	}
	w := info.WaitingMessages[0]
	want := []Completion{{"c1", 2}}
	if w.Message != rec1 || !slices.Equal(w.Completions, want) {
		t.Fatalf("legacy waiting = %q %+v", w.Message, w.Completions)
	}
	sl := deliverNext(t, s2, "L")
	e := loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: "k-" + sl.RequestID, Sender: sl.Sender, Text: sl.Body}
	row, err := s2.withCompletions(ctx, "L", nativeRow("L", "message.delivered", e), e)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Message     string
		Completions []Completion
	}
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Message != rec1 || !slices.Equal(p.Completions, want) {
		t.Fatalf("legacy delivery = %+v", p)
	}
}
