package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// summaryCap bounds the final message a task_completed record quotes; the
// lead reads the rest from the child's history and branch.
const summaryCap = 500

// TaskCompleted is one child attempt's completion record, saved once on the
// parent's history with EventID task_completed:<child>:<attempt>.
type TaskCompleted struct {
	Child   string `json:"child"`
	Attempt int64  `json:"attempt"`
	Outcome string `json:"outcome"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	Summary string `json:"summary,omitempty"`
}

func completionKey(child string, attempt int64) string {
	return KindTaskCompleted + ":" + child + ":" + strconv.FormatInt(attempt, 10)
}

// text is the record as the lead reads it in the child's slot.
func (t TaskCompleted) text() string {
	return fmt.Sprintf("%s outcome=%s branch=%s head=%s summary=%s", completionKey(t.Child, t.Attempt),
		t.Outcome, t.Branch, t.Head, strconv.Quote(t.Summary))
}

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
// kept (n.Legacy), it rebuilds them: the trailing lines of body that are
// records agentID saved for sender's child, exactly as text() wrote them,
// ending with the n.Legacy record the slot last took. That provenance is
// required, so only such a legacy slot is ever read this way; if the lines
// do not match (a redacted record), the whole body counts as records, so a
// raw record never shows as a message.
func (s *Service) slotNotices(ctx context.Context, agentID, sender, body string,
	n loomstore.SlotNotices) (loomstore.SlotNotices, error) {
	if n.Legacy == "" {
		return n, nil
	}
	last, ok := parseCompletionKey(n.Legacy)
	if !ok || "agent:"+last.Child != sender {
		return loomstore.SlotNotices{}, nil
	}
	byText := map[string]string{}
	q := loomstore.EventQuery{AgentID: agentID, Kinds: []string{KindTaskCompleted}, Limit: 500}
	for {
		page, err := s.store.ListEvents(ctx, q)
		if err != nil {
			return n, err
		}
		for _, e := range page.Events {
			var rec TaskCompleted
			if json.Unmarshal(e.Payload, &rec) == nil && rec.Child == last.Child {
				byText[rec.text()] = e.EventID
			}
		}
		if !page.More {
			break
		}
		q.After, q.Snapshot = page.Next, page.SnapshotSeq
	}
	lines := strings.Split(body, "\n")
	i, keys := len(lines), []string{}
	for i > 0 && byText[lines[i-1]] != "" {
		keys = append([]string{byText[lines[i-1]]}, keys...)
		i--
	}
	if len(keys) == 0 || keys[len(keys)-1] != n.Legacy {
		return loomstore.SlotNotices{Keys: []string{n.Legacy}}, nil
	}
	at := 0
	for _, l := range lines[:i] {
		at += len(l) + 1
	}
	return loomstore.SlotNotices{Keys: keys, At: at}, nil
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
	if a.WorktreePath != nil && s.workspace != nil { // nothing is saved without the port's branch and head
		repo, err := s.repoPath(ctx, a.Repo)
		if err != nil {
			return err
		}
		st, err := s.workspace.Status(ctx, WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef),
			Branch: deref(a.Branch), Detached: a.Branch == nil})
		if err != nil {
			return fmt.Errorf("loomagent: task_completed workspace status: %w", err)
		}
		rec.Branch, rec.Head = st.Branch, st.HEAD
	}
	summary, err := s.store.LastMessage(ctx, a.AgentID)
	if err != nil {
		return err
	}
	rec.Summary = clip(summary, summaryCap)
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
	byChild, order := map[string][]loomstore.Notice{}, []string{}
	for _, e := range events {
		var rec TaskCompleted
		if err := json.Unmarshal(e.Payload, &rec); err != nil {
			return err
		}
		if _, ok := byChild[rec.Child]; !ok {
			order = append(order, rec.Child)
		}
		byChild[rec.Child] = append(byChild[rec.Child], loomstore.Notice{Key: e.EventID, Text: rec.text()})
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
