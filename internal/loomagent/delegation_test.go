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
	"time"
	"unicode/utf8"

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
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunDispatcher(run) }()
	defer func() { stop(); <-done }()
	reconcile(t, s)
	reconcile(t, s)
	// The dispatcher hands the record to the idle lead without a manual Dispatch.
	eventually(t, "the lead is handed the record", func() bool {
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
	ctx := context.Background()
	defer func(d time.Duration) { completionRetry = d }(completionRetry)
	completionRetry = 10 * time.Millisecond
	ws := &flakyStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc123"}}
	ws.fail.Store(true)
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	s := newService(t, ServiceConfig{Workspace: ws}, busy("L", "persistent", StateActive), c)
	endAttempt(t, s, "c1", "completed")
	if got := completions(t, s, "L"); len(got) != 0 || s.get(t, "c1").State != StateFinished {
		t.Fatalf("records after a failed status = %+v", got)
	}
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.RunDispatcher(run) }()
	defer func() { stop(); <-done }()
	time.Sleep(5 * completionRetry) // retries while the port still fails save nothing
	if got := completions(t, s, "L"); len(got) != 0 {
		t.Fatalf("saved while failing: %+v", got)
	}
	ws.fail.Store(false)
	eventually(t, "the record is saved", func() bool { return len(completions(t, s, "L")) == 1 })
	time.Sleep(5 * completionRetry)
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
