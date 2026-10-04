package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Delegation events saved on the parent (design v2 §5.2, §10.3).
const (
	KindChildCreated  = "child.created"
	KindTaskCompleted = "task_completed"
)

// summaryCap bounds the final message a task_completed record quotes for
// the chat's one-line result; the lead reads resultCap of the final reply.
const summaryCap = 500

// resultCap bounds the child's final reply a record carries for the lead's
// notice: enough that the lead needs no agent_get for it. A longer reply
// keeps its end, where its conclusion is.
const resultCap = 4000

// TaskCompleted is one child attempt's completion record, saved once on the
// parent's history with EventID task_completed:<child>:<attempt>.
type TaskCompleted struct {
	Child   string `json:"child"`
	Attempt int64  `json:"attempt"`
	Outcome string `json:"outcome"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	Summary string `json:"summary,omitempty"`
	// Result is the child's whole final reply (every message item of its
	// last turn), its end kept within resultCap; records saved before it
	// have only Summary.
	Result string `json:"result,omitempty"`
}

// result is what the lead's notice quotes: Result, or Summary on a record
// saved before Result.
func (t TaskCompleted) result() string {
	if t.Result != "" {
		return t.Result
	}
	return t.Summary
}

func completionKey(child string, attempt int64) string {
	return KindTaskCompleted + ":" + child + ":" + strconv.FormatInt(attempt, 10)
}

// text is the record's line as releases before CL2 put it in the child's
// slot. Only the legacy rebuild (slotNotices) reads it, to find those lines
// in a slot saved before notices were kept; the lead now reads notice.
func (t TaskCompleted) text() string {
	return fmt.Sprintf("%s outcome=%s branch=%s head=%s summary=%s", completionKey(t.Child, t.Attempt),
		t.Outcome, t.Branch, t.Head, strconv.Quote(t.Summary))
}

// notice is the record as the lead reads it in the child's slot: one line
// with the child's name, its outcome, branch@head, its summary, how many of
// the lead's other children are still running, and what to do next, so the
// lead needs no agent_get for it and writes one summary once all are done.
// The line starts with the record's key; the chat finds the record by the
// slot's notice keys, never by this text.
func (t TaskCompleted) notice(name string, running int) string {
	at := t.at()
	next := "no children still running: write one combined summary of every child's result now"
	if running > 0 {
		next = fmt.Sprintf("%d other %s still running: reply in one short line or not at all, "+
			"and write one combined summary when the last one finishes", running, plural(running, "child", "children"))
	}
	return fmt.Sprintf("%s child=%s outcome=%s branch=%s still_running=%d summary=%s next=%s",
		completionKey(t.Child, t.Attempt), strconv.Quote(name), t.Outcome, at, running, strconv.Quote(t.result()),
		strconv.Quote("this notice is the result, no agent_get needed; "+next))
}

// at is the record's branch@head, the branch alone without a head.
func (t TaskCompleted) at() string {
	if t.Head == "" {
		return t.Branch
	}
	return t.Branch + "@" + t.Head
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// noticeText is the line deliverCompletions puts in a slot; legacy-store
// tests swap it for text to write slots as the prior release did.
var noticeText = TaskCompleted.notice

// Completion names one child attempt whose task_completed record is part of
// a message: the chat shows it on that record, not as message text.
type Completion struct {
	Child   string `json:"child"`
	Attempt int64  `json:"attempt"`
}

// completionsIn splits body, a slot's text, at its notices (the records
// Notify added, named by key, never found by text): it returns the text
// before them, which the sender wrote, and the task_completed records among
// them, in order. With no notices body is the sender's own text.
func completionsIn(body string, n loomstore.SlotNotices) (string, []Completion) {
	found := []Completion{}
	if len(n.Keys) == 0 {
		return body, found
	}
	for _, k := range n.Keys {
		if c, ok := parseCompletionKey(k); ok {
			found = append(found, c)
		}
	}
	msg := body
	if n.At >= 0 && n.At <= len(body) {
		msg = strings.TrimSuffix(body[:n.At], "\n")
	}
	return msg, found
}

// slotNotices is n, a slot's notices; for a slot saved before notices were
// kept (n.Legacy), it rebuilds them from the sender's receipts. The last
// record of each Notify saved the slot's body after it; walking back from
// n.Legacy, each such batch must account for the body's last lines (its
// records' text, exactly), and the receipt the sender wrote just before it
// must have saved the body that is left: a Send's message (the slot's own
// text) or an earlier batch's body (walk on). Whatever is not proven that
// way is left as the message, so the rebuild never hides a message, at
// worst leaving an unproven record's line visible in it.
func (s *Service) slotNotices(ctx context.Context, agentID, sender, body string,
	n loomstore.SlotNotices) (loomstore.SlotNotices, error) {
	if n.Legacy == "" {
		return n, nil
	}
	last, ok := parseCompletionKey(n.Legacy)
	if !ok || "agent:"+last.Child != sender {
		return loomstore.SlotNotices{}, nil
	}
	receipts, err := s.store.SenderReceipts(ctx, agentID, sender)
	if err != nil {
		return n, err
	}
	recs, err := s.records(ctx, agentID, last.Child)
	if err != nil {
		return n, err
	}
	out := loomstore.SlotNotices{}
	key := n.Legacy
	for {
		at := slices.IndexFunc(receipts, func(r loomstore.SenderReceipt) bool { return r.RequestID == key })
		if at < 0 || receipts[at].Body == nil || *receipts[at].Body != body {
			return out, nil
		}
		stamp := receipts[at].CreatedAt
		keys, i := batchLines(receipts, recs, stamp, body)
		if len(keys) == 0 || keys[len(keys)-1] != key {
			return out, nil
		}
		lines := strings.Split(body, "\n")
		out.Keys = append(keys, out.Keys...)
		if i == 0 {
			out.At = 0
			return out, nil
		}
		body = strings.Join(lines[:i], "\n")
		out.At = len(body) + 1
		prev := -1
		for j, r := range receipts {
			if r.CreatedAt < stamp && r.Body != nil {
				prev = j
			}
		}
		if prev < 0 || *receipts[prev].Body != body || !strings.HasPrefix(receipts[prev].RequestID, "task_completed:") {
			return out, nil // the sender's own message, or not proven a record
		}
		key = receipts[prev].RequestID
	}
}

// batchLines returns the keys of the records one Notify added (receipts
// stamped stamp, recs by key) in the order their lines end body, and the
// index of the first such line; no keys unless every record is accounted for.
func batchLines(receipts []loomstore.SenderReceipt, recs map[string]TaskCompleted, stamp, body string) ([]string, int) {
	batch := map[string]TaskCompleted{}
	for _, r := range receipts {
		if rec, ok := recs[r.RequestID]; ok && r.CreatedAt == stamp && strings.HasPrefix(r.RequestID, "task_completed:") {
			batch[r.RequestID] = rec
		}
	}
	lines := strings.Split(body, "\n")
	i, keys := len(lines), []string{}
	for i > 0 && len(keys) < len(batch) {
		key, _, _ := strings.Cut(lines[i-1], " ")
		rec, ok := batch[key]
		if !ok || slices.Contains(keys, key) || !rec.isLine(lines[i-1]) {
			break
		}
		keys = append([]string{key}, keys...)
		i--
	}
	if len(keys) != len(batch) {
		return nil, 0
	}
	return keys, i
}

// isLine reports whether line is t's line in a slot: text, as releases
// before CL2 wrote it, or notice, whose name, running count and next step
// were read when it was written, so only its key, outcome, branch and
// summary are checked.
func (t TaskCompleted) isLine(line string) bool {
	if line == t.text() {
		return true
	}
	at := t.at()
	rest, ok := strings.CutPrefix(line, completionKey(t.Child, t.Attempt)+" child=")
	return ok && strings.Contains(rest, " outcome="+t.Outcome+" branch="+at+" still_running=") &&
		strings.Contains(rest, " summary="+strconv.Quote(t.result())+" next=")
}

// records maps each record agentID saved for child by its key.
func (s *Service) records(ctx context.Context, agentID, child string) (map[string]TaskCompleted, error) {
	out := map[string]TaskCompleted{}
	q := loomstore.EventQuery{AgentID: agentID, Kinds: []string{KindTaskCompleted}, Limit: 500}
	for {
		page, err := s.store.ListEvents(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Events {
			var rec TaskCompleted
			if json.Unmarshal(e.Payload, &rec) == nil && rec.Child == child {
				out[e.EventID] = rec
			}
		}
		if !page.More {
			return out, nil
		}
		q.After, q.Snapshot = page.Next, page.SnapshotSeq
	}
}

// parseCompletionKey reads a record key completionKey wrote.
func parseCompletionKey(key string) (Completion, bool) {
	rest, ok := strings.CutPrefix(key, KindTaskCompleted+":")
	i := strings.LastIndexByte(rest, ':')
	if !ok || i <= 0 {
		return Completion{}, false
	}
	attempt, err := strconv.ParseInt(rest[i+1:], 10, 64)
	if err != nil {
		return Completion{}, false
	}
	return Completion{Child: rest[:i], Attempt: attempt}, true
}

// created saves agent.created once, when Create finishes, and child.created
// on a's parent.
func (s *Service) created(ctx context.Context, a loomstore.Agent) error {
	if err := s.appendEvent(ctx, a.AgentID, KindAgentCreated, KindAgentCreated,
		map[string]any{"name": a.Name, "preset": a.Preset, "harness": a.Harness}); err != nil {
		return err
	}
	if err := s.warnUnverified(ctx, a); err != nil || a.ParentAgentID == nil {
		return err
	}
	return s.appendEvent(ctx, *a.ParentAgentID, KindChildCreated, KindChildCreated+":"+a.AgentID,
		map[string]any{"child": a.AgentID, "name": a.Name, "preset": a.Preset})
}

// completed reports whether a is a child single task whose current attempt
// has ended with an outcome: finished, or archived (a cancel). Attention
// alone is not an end.
func completed(a loomstore.Agent) bool {
	return a.ParentAgentID != nil && a.Mode == "single_task" && a.Outcome != nil && a.DeletedAt == nil &&
		(a.State == StateFinished || a.State == StateArchived)
}

// recordCompletion saves a's task_completed record on its parent once per
// attempt, with the branch and head from the Workspace port (R32), and
// wakes the parent's dispatcher, which puts it in a's slot there. A repeat
// for the same attempt changes nothing, so the record keeps the branch and
// head of its first save. It takes no parent lock.
func (s *Service) recordCompletion(ctx context.Context, a loomstore.Agent) error {
	if !completed(a) {
		return nil
	}
	parent, key := *a.ParentAgentID, completionKey(a.AgentID, a.Attempt)
	if ok, err := s.store.HasEvent(ctx, parent, key); err != nil || ok {
		return err
	}
	rec := TaskCompleted{Child: a.AgentID, Attempt: a.Attempt, Outcome: *a.Outcome, Branch: deref(a.Branch)}
	if a.WorktreePath != nil && s.workspace != nil { // a failed Status saves nothing, unless retrying cannot help
		repo, err := s.repoPath(ctx, a.Repo)
		if err != nil {
			return err
		}
		st, err := s.workspace.Status(ctx, WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef),
			Branch: deref(a.Branch), Detached: a.Branch == nil})
		switch {
		case errors.Is(err, ErrWorkspaceNotOwned): // no retry clears it: tell the parent without a head
			slog.Warn("loomagent: task_completed without head; working copy not the agent's", "agent", a.AgentID, "error", err)
		case err != nil:
			return fmt.Errorf("loomagent: task_completed workspace status: %w", err)
		default:
			rec.Branch, rec.Head = st.Branch, st.HEAD
		}
	}
	summary, err := s.store.LastMessage(ctx, a.AgentID)
	if err != nil {
		return err
	}
	rec.Summary = clip(summary, summaryCap)
	reply, err := s.store.LastReply(ctx, a.AgentID)
	if err != nil {
		return err
	}
	rec.Result = clipHead(strings.Join(reply, "\n\n"), resultCap)
	if err := s.appendEvent(ctx, parent, KindTaskCompleted, key, rec); err != nil {
		return err
	}
	s.Bus.publish(Event{AgentID: parent, Type: KindTaskCompleted, Reason: a.AgentID, Outcome: rec.Outcome,
		Attempt: rec.Attempt, Time: time.Now()})
	return nil
}

// tryRecordCompletion is recordCompletion for a change already committed:
// a failure is logged and marks the record owed, for the dispatcher to retry.
func (s *Service) tryRecordCompletion(ctx context.Context, a loomstore.Agent) {
	if err := s.recordCompletion(ctx, a); err != nil {
		slog.Warn("loomagent: task_completed not saved; will retry", "agent", a.AgentID, "error", err)
		s.owed.Store(true)
	}
}

// recordCompletions saves every owed task_completed record in the
// workspace: an attempt that ended just before a crash, or whose record
// failed to save. Each is saved once.
func (s *Service) recordCompletions(ctx context.Context) {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Mode: "single_task",
		IncludeArchived: true})
	if err != nil {
		slog.Warn("loomagent: task_completed sweep", "error", err)
		s.owed.Store(true)
		return
	}
	for _, a := range agents {
		s.tryRecordCompletion(ctx, a)
	}
}

// deliverCompletions puts a's saved task_completed records that no slot has
// taken yet into each child's one slot on a, merged after a waiting record
// of that child (§10.3), with the agent lock held. An agent that takes no
// messages keeps them in its history only; a child slot still handed takes
// them after that message is delivered.
func (s *Service) deliverCompletions(ctx context.Context, a loomstore.Agent) error {
	if a.State != StateIdle && a.State != StateActive && a.State != StateWaiting {
		return nil
	}
	events, err := s.store.Unreceipted(ctx, a.AgentID, KindTaskCompleted)
	if err != nil || len(events) == 0 {
		return err
	}
	names, running, err := s.children(ctx, a.AgentID)
	if err != nil {
		return err
	}
	byChild, order := map[string][]loomstore.Notice{}, []string{}
	for _, e := range events {
		var rec TaskCompleted
		if err := json.Unmarshal(e.Payload, &rec); err != nil {
			return err
		}
		if _, ok := byChild[rec.Child]; !ok {
			order = append(order, rec.Child)
		}
		name := names[rec.Child]
		if name == "" {
			name = rec.Child
		}
		byChild[rec.Child] = append(byChild[rec.Child], loomstore.Notice{Key: e.EventID, Text: noticeText(rec, name, running)})
	}
	for _, child := range order {
		sender := senderOf(ActorRef{Kind: "agent", ID: child})
		added, err := s.store.Notify(ctx, a.AgentID, sender, "system", byChild[child],
			func(requestID string, replaced bool) (string, error) {
				b, err := json.Marshal(SendResult{MessageID: messageID(a.AgentID, sender, requestID),
					State: loomstore.SlotWaiting, Replaced: replaced})
				return string(b), err
			})
		if errors.Is(err, loomstore.ErrSlotBusy) {
			continue
		}
		if err != nil {
			return err
		}
		if added {
			if err := s.emit(ctx, Event{AgentID: a.AgentID, Type: EventWaiting, Reason: sender, Time: time.Now()}); err != nil {
				return err
			}
		}
	}
	return nil
}

// children returns the names of parent's children by id, and how many of
// its single tasks are still running: not deleted and not ended (finished
// or archived), counted when a record is put in a slot.
func (s *Service) children(ctx context.Context, parent string) (map[string]string, int, error) {
	kids, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{Parent: parent, IncludeArchived: true})
	if err != nil {
		return nil, 0, err
	}
	names, running := map[string]string{}, 0
	for _, c := range kids {
		names[c.AgentID] = c.Name
		if c.Mode == "single_task" && c.DeletedAt == nil && c.State != StateFinished && c.State != StateArchived {
			running++
		}
	}
	return names, running, nil
}

// clipHead keeps at most the last n bytes of s, the leading "…" mark
// included, starting after a space or line break within the next 64 bytes,
// else on a rune boundary.
func clipHead(s string, n int) string {
	const mark = "…"
	if len(s) <= n {
		return s
	}
	cut := len(s) - max(n-len(mark), 0)
	if i := strings.IndexAny(s[cut:min(cut+64, len(s))], " \n"); i >= 0 {
		cut += i + 1
	}
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return mark + s[cut:]
}

// clip cuts s to at most n bytes, the "…" mark included, on a rune boundary.
func clip(s string, n int) string {
	const mark = "…"
	if len(s) <= n {
		return s
	}
	n = max(n-len(mark), 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + mark
}
