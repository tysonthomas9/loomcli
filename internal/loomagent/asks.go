package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Live-only notice kinds (Seq 0, never saved) a Subscribe can get.
const (
	KindDelta   = "delta"
	KindFeedGap = "feed.gap"
	// KindToolStarted is a tool call that started; like a delta, only a
	// Subscribe that asks for deltas gets it.
	KindToolStarted = "tool.started"
)

// Ask is one open harness ask (design v2 §4.10). Loom keeps open asks in
// memory, from the live feed and each backfill of the native history.
type Ask struct {
	ID     string
	Type   string // approval | question
	About  string
	TurnID string `json:"-"`
}

// RespondRequest answers an open ask: Decision (allow_once, allow_always or
// deny) for an approval, Answer for a question.
type RespondRequest struct {
	Envelope
	AgentID, AskID   string
	Decision, Answer string
}

// openAsks returns agentID's open asks by ID.
func (s *Service) openAsks(agentID string) []Ask {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ask
	for _, a := range s.asks[agentID] {
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y Ask) int { return strings.Compare(x.ID, y.ID) })
	return out
}

// Respond checks the ask is open, resumes the session if this process has
// not yet, then replies through the harness. An
// unknown, answered or lost ask fails with ask_not_found. A decision is
// passed as given, never narrowed: a harness that cannot keep allow_always
// fails the reply, Respond returns that error and the ask stays open.
func (s *Service) Respond(ctx context.Context, req RespondRequest) error {
	defer s.lockReady(ctx, req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ask, ok := s.asks[a.AgentID][req.AskID]
	s.mu.Unlock()
	if !ok {
		return &Error{Code: CodeAskNotFound, Message: req.AskID}
	}
	r := loomharness.Reply{Answer: req.Answer}
	switch {
	case req.Decision == "allow_once":
		r.Allow = true
	case req.Decision == "allow_always":
		r.Allow, r.Always = true, true
	case req.Decision == "deny":
	case req.Decision == "" && ask.Type == "question":
	default:
		return invalid("Respond needs a Decision for an approval", "allow_once", "allow_always", "deny")
	}
	if a, err = s.resumeOnce(ctx, a); err != nil { // lazily installs the policy (§4.15)
		return err
	}
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"}
	}
	if err := sess.Reply(ctx, req.AskID, r); errors.Is(err, loomharness.ErrQuarantined) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), quarantineCleanup)
		defer cancel() // the Reply may have used up ctx; the loss must still be saved
		return errors.Join(harnessErr(err), s.quarantined(cleanup, a, ask))
	} else if err != nil {
		return harnessErr(err)
	}
	s.setAsk(a.AgentID, Ask{ID: req.AskID}, false)
	return s.syncWaiting(ctx, a)
}

// quarantineCleanup bounds saving a quarantined ask's loss, which runs even
// if the caller's context ended during the Reply.
const quarantineCleanup = 10 * time.Second

// quarantined handles a Reply its session refused as quarantined: the ask
// can no longer be answered, so it is saved as ask.lost (once) and closed,
// and a shows Attention harness_unavailable, without waiting for the native
// turn to be confirmed stopped.
func (s *Service) quarantined(ctx context.Context, a loomstore.Agent, ask Ask) error {
	ref := loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}
	row := nativeRow(a.AgentID, KindAskLost, loomharness.Event{Type: loomharness.EventAskLost, Session: ref, AskID: ask.ID, TurnID: ask.TurnID})
	if _, err := s.events.Append(ctx, row); err != nil {
		return err
	}
	s.setAsk(a.AgentID, ask, false)
	if a.AttentionReason == nil {
		var err error
		if a, err = s.raiseAttention(ctx, a, AttentionHarnessUnavailable); err != nil {
			return err
		}
	}
	return s.syncWaiting(ctx, a)
}

// setAsk opens (open) or closes an ask in agentID's table.
func (s *Service) setAsk(agentID string, ask Ask, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.asks[agentID] == nil {
		s.asks[agentID] = map[string]Ask{}
	}
	if open {
		s.asks[agentID][ask.ID] = ask
	} else {
		delete(s.asks[agentID], ask.ID)
	}
}

// askEvent applies a native ask event to a's open asks and its waiting state.
func (s *Service) askEvent(ctx context.Context, a loomstore.Agent, e loomharness.Event) error {
	s.setAsk(a.AgentID, askOf(e), e.Type == loomharness.EventAskOpened)
	return s.syncWaiting(ctx, a)
}

// askOf is the ask a native ask event names.
func askOf(e loomharness.Event) Ask {
	typ := "approval"
	if e.ItemKind == "question" {
		typ = "question"
	}
	return Ask{ID: e.AskID, Type: typ, About: e.Text, TurnID: e.TurnID}
}

// syncWaiting sets a running agent waiting{approval|input} while an ask of
// its running turn is open, and active again when none is.
func (s *Service) syncWaiting(ctx context.Context, a loomstore.Agent) error {
	if a.RunningTurnID == nil || (a.State != StateActive && a.State != StateWaiting) {
		return nil
	}
	to := a.StateOf()
	to.State, to.WaitingOn = StateActive, nil
	for _, ask := range s.openAsks(a.AgentID) {
		if ask.TurnID == *a.RunningTurnID {
			on := map[string]string{"approval": "approval", "question": "input"}[ask.Type]
			to.State, to.WaitingOn = StateWaiting, &on
			break
		}
	}
	if to.State == a.State && deref(to.WaitingOn) == deref(a.WaitingOn) {
		return nil
	}
	_, err := s.setState(ctx, a, to)
	return err
}

// endTurnAsks saves ask.lost for each ask of a's ended turn still open: a
// turn that ended cannot take its answer.
func (s *Service) endTurnAsks(ctx context.Context, a loomstore.Agent, turnID string) error {
	ref := loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}
	for _, ask := range s.openAsks(a.AgentID) {
		if ask.TurnID != turnID {
			continue
		}
		s.setAsk(a.AgentID, ask, false)
		row := nativeRow(a.AgentID, KindAskLost, loomharness.Event{Type: loomharness.EventAskLost, Session: ref, AskID: ask.ID, TurnID: turnID})
		if _, err := s.events.Append(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// askPage is how many saved ask rows loseOpen reads at a time.
var askPage = 500

// loseOpen saves one ask.lost, with its AskID, for each of a's open asks not
// in keep, then removes it from the table: an ask open in its saved log, or
// open in its table and never saved. A table ask the log shows closed is
// just removed. The log is read in bounded pages.
func (s *Service) loseOpen(ctx context.Context, a loomstore.Agent, keep map[string]*Ask) error {
	table := map[string]Ask{}
	for _, ask := range s.openAsks(a.AgentID) {
		table[ask.ID] = ask
	}
	opened, logged, err := s.savedAsks(ctx, a.AgentID, table)
	if err != nil {
		return err
	}
	ref := loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}
	for id, ask := range table {
		if _, open := opened[id]; open {
			continue
		}
		if logged[id] {
			s.setAsk(a.AgentID, ask, false) // answered or lost in the log
			continue
		}
		opened[id] = nativeRow(a.AgentID, string(loomharness.EventAskOpened), loomharness.Event{
			Type: loomharness.EventAskOpened, Session: ref, AskID: id, TurnID: ask.TurnID})
	}
	for id, r := range opened {
		if keep[id] != nil {
			continue
		}
		r.Kind, r.EventID = KindAskLost, KindAskLost+strings.TrimPrefix(r.EventID, string(loomharness.EventAskOpened))
		if _, err := s.events.Append(ctx, r); err != nil {
			return err
		}
		s.setAsk(a.AgentID, Ask{ID: id}, false)
	}
	return nil
}

// savedAsks reads agentID's saved ask rows in pages of askPage: opened is
// each ask whose last row opens it, and logged marks each ask of table that
// has a row.
func (s *Service) savedAsks(ctx context.Context, agentID string, table map[string]Ask) (opened map[string]loomstore.Event, logged map[string]bool, err error) {
	opened, logged = map[string]loomstore.Event{}, map[string]bool{}
	q := loomstore.EventQuery{AgentID: agentID, Limit: askPage,
		Kinds: []string{string(loomharness.EventAskOpened), string(loomharness.EventAskResolved), KindAskLost}}
	for {
		page, err := s.events.Page(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		for _, r := range page.Events {
			var p struct {
				AskID string `json:"askId"`
			}
			if json.Unmarshal(r.Payload, &p) != nil || p.AskID == "" {
				continue
			}
			if _, ok := table[p.AskID]; ok {
				logged[p.AskID] = true
			}
			if r.Kind == string(loomharness.EventAskOpened) {
				opened[p.AskID] = r
			} else {
				delete(opened, p.AskID)
			}
		}
		if !page.More {
			return opened, logged, nil
		}
		q.After, q.Snapshot = page.Next, page.SnapshotSeq
	}
}

// SubscribeRequest follows agents' events (design v2 §4.11). An agent with
// a cursor replays its saved rows after it (0: all); one without gets new
// events only. Kinds filters saved kinds (empty: all). Deltas adds live text
// deltas; feed.gap notices always come. A subscriber that falls behind ends
// with subscriber_lagged and reconnects from the last Seq it got.
type SubscribeRequest struct {
	AgentIDs []string
	Cursors  map[string]int64
	Kinds    []string
	Deltas   bool
}

// Subscribe starts a subscription; a cursor into purged history fails with
// cursor_expired.
func (s *Service) Subscribe(ctx context.Context, req SubscribeRequest) (*Subscription, error) {
	cursors := map[string]int64{}
	for _, id := range req.AgentIDs {
		c, ok := req.Cursors[id]
		if !ok {
			c = LiveOnly
		} else if a, err := s.agent(ctx, id); err != nil {
			return nil, err
		} else if a.HistoryPurgedAt != nil {
			return nil, &Error{Code: CodeCursorExpired, Message: id}
		}
		cursors[id] = c
	}
	sub := &Subscription{notes: true, deltas: req.Deltas, kinds: map[string]bool{}}
	for _, k := range req.Kinds {
		sub.kinds[k] = true
	}
	return s.events.subscribe(ctx, cursors, sub)
}

// ListEvents pages an agent's saved events by seq (design v2 §4.12). Purged
// history fails with history_expired, or cursor_expired for a cursor.
func (s *Service) ListEvents(ctx context.Context, q loomstore.EventQuery) (loomstore.EventPage, error) {
	a, err := s.agent(ctx, q.AgentID)
	if err != nil {
		return loomstore.EventPage{}, err
	}
	if a.HistoryPurgedAt != nil {
		if q.After > 0 {
			return loomstore.EventPage{}, &Error{Code: CodeCursorExpired, Message: q.AgentID}
		}
		return loomstore.EventPage{}, &Error{Code: CodeHistoryExpired, Message: q.AgentID}
	}
	return s.events.Page(ctx, q)
}
