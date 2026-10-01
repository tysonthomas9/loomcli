package loomagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// EventTurnCompleted is the saved Loom event for a native turn.completed.
const EventTurnCompleted = "agent.turn_completed"

// RunFeed waits feedRetry before it reopens the feed, doubling up to
// feedRetryMax while reads keep failing; a read that ingested events resets it.
var feedRetry, feedRetryMax = 200 * time.Millisecond, 30 * time.Second

// errFeedClosed is a feed that ended while ctx was still live.
var errFeedClosed = errors.New("loomagent: the harness feed closed")

// RunFeed ingests the named harness's live feed until ctx ends (§5.2): each
// completed native event of an agent's session is saved in agent_events and
// then applied to its slots and turn. After every (re)connect and on each
// feed.gap it backfills every live agent's session from its native history,
// so events the feed missed, or that came while Loom was down, are saved
// once (stable EventIDs) and never invented from the live bus. A failed save
// or history read, or a closed feed, reopens the feed after a backoff, so
// the backfill after the reconnect reads past nothing unsaved. A warning is
// logged when the failure changes, not on every retry.
func (s *Service) RunFeed(ctx context.Context, harness string) {
	wait, last := feedRetry, ""
	for h := s.harnesses[harness]; h != nil && ctx.Err() == nil; {
		read, err := s.readFeed(ctx, harness, h)
		if ctx.Err() != nil {
			return
		}
		if read {
			wait, last = feedRetry, ""
		}
		if err.Error() != last {
			last = err.Error()
			slog.Warn("loomagent: event ingestion stopped; reopening the feed to backfill", "harness", harness, "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(2*wait, feedRetryMax)
	}
}

// readFeed backfills, then ingests h's feed until it ends or a save fails;
// it always returns an error, and read reports whether it ingested a live
// native event of an owned session (a feed.gap or another session's event
// is not one).
func (s *Service) readFeed(ctx context.Context, harness string, h loomharness.Harness) (read bool, err error) {
	f, err := h.Feed(ctx)
	if err != nil {
		return false, errors.Join(err, s.harnessAttention(ctx, harness, true))
	}
	defer func() { _ = f.Close() }()
	if err := s.backfill(ctx, harness); err != nil { // after subscribing, so nothing falls between
		return false, err
	}
	if err := s.harnessAttention(ctx, harness, false); err != nil {
		return false, err
	}
	for e := range f.Events() {
		ok := false
		if e.Type == loomharness.EventFeedGap {
			s.events.Notify(loomstore.Event{Kind: KindFeedGap, Payload: json.RawMessage(strconv.Quote(harness))})
			err = s.backfill(ctx, harness)
		} else {
			ok, err = s.ingest(ctx, harness, e)
		}
		if err != nil {
			return read, err
		}
		read = read || ok
	}
	return read, errFeedClosed
}

// backfill ingests the native history of every live agent's current session.
// A session the harness no longer has is left to Reconcile (session_missing).
func (s *Service) backfill(ctx context.Context, harness string) error {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness})
	if err != nil {
		return err
	}
	for _, a := range agents {
		if a.HarnessSessionID == nil {
			continue
		}
		s.rebuild(a.AgentID, true) // the replay shows which open asks remain
		err := s.replay(ctx, harness, a)
		if err == nil {
			err = s.loseAsks(ctx, a.AgentID)
		}
		s.rebuild(a.AgentID, false) // a failed or partial replay loses no ask
		if err != nil && !errors.Is(err, loomharness.ErrSessionNotFound) {
			return err
		}
	}
	return nil
}

// replay ingests a's whole current native history.
func (s *Service) replay(ctx context.Context, harness string, a loomstore.Agent) error {
	sess := s.harnesses[harness].Session(loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID})
	for after := ""; ; {
		page, err := sess.Messages(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, e := range page.Events {
			if e.Type == loomharness.EventDelta {
				continue // live only: a subscriber had it, or missed it with the gap
			}
			if _, err := s.ingest(ctx, harness, e); err != nil {
				return err
			}
		}
		if after = page.Next; after == "" {
			return nil
		}
	}
}

// harnessAttention raises Attention harness_unavailable on each live agent
// of harness (raise), or clears it and retries their waiting messages. Only
// that harness's agents are touched; one already showing another Attention
// keeps it.
func (s *Service) harnessAttention(ctx context.Context, harness string, raise bool) error {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness})
	if err != nil {
		return err
	}
	for _, a := range agents {
		if raise == (a.AttentionReason == nil) && (raise || deref(a.AttentionReason) == AttentionHarnessUnavailable) {
			err = errors.Join(err, s.flipAttention(ctx, a.AgentID, raise))
		}
	}
	return err
}

func (s *Service) flipAttention(ctx context.Context, agentID string, raise bool) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || (a.AttentionReason == nil) != raise {
		return err
	}
	if raise {
		_, err = s.raiseAttention(ctx, a, AttentionHarnessUnavailable)
		return err
	}
	if deref(a.AttentionReason) != AttentionHarnessUnavailable {
		return nil
	}
	if a, err = s.clearAttention(ctx, a); err != nil {
		return err
	}
	_, err = s.wake(ctx, a)
	return err
}

// ingest saves one native event of an owned session, then applies it. A
// repeat (live after backfill, or backfill after live) saves nothing new, and
// HarnessEvent ignores events of a turn that is not running. ok reports
// that e belonged to a live agent's session and was handled.
func (s *Service) ingest(ctx context.Context, harness string, e loomharness.Event) (ok bool, err error) {
	id, err := s.store.NativeSessionOwner(ctx, harness, e.Session.Root, e.Session.NativeID)
	if errors.Is(err, loomstore.ErrNotFound) {
		return false, nil // not an agent's session
	}
	if err != nil {
		return false, err
	}
	if _, err := s.live(ctx, id); isCode(err, CodeAgentNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if kind, ok := savedKinds[e.Type]; ok {
		if _, err := s.events.Append(ctx, nativeRow(id, kind, e)); err != nil {
			return false, err
		}
	} else if e.Type == loomharness.EventDelta {
		s.events.Notify(nativeRow(id, KindDelta, e))
	}
	return true, s.HarnessEvent(ctx, id, e)
}

// savedKinds maps the completed native events Phase 1 saves to their Loom
// kind. Deltas and item starts stay live only.
var savedKinds = map[loomharness.EventType]string{
	loomharness.EventMessageDelivered: string(loomharness.EventMessageDelivered),
	loomharness.EventTurnStarted:      string(loomharness.EventTurnStarted),
	loomharness.EventItemCompleted:    string(loomharness.EventItemCompleted),
	loomharness.EventUsage:            string(loomharness.EventUsage),
	loomharness.EventTurnCompleted:    EventTurnCompleted,
	loomharness.EventAskOpened:        string(loomharness.EventAskOpened),
	loomharness.EventAskResolved:      string(loomharness.EventAskResolved),
	loomharness.EventAskLost:          string(loomharness.EventAskLost),
	loomharness.EventTurnResumed:      string(loomharness.EventTurnResumed),
	loomharness.EventSubagentStarted:  string(loomharness.EventSubagentStarted),
}

// nativeRow is e as a saved row. Its EventID uses only ids the live feed and
// a catch-up read share (the port contract): the session's Root and NativeID
// and the event's own id: InputKey for a delivery, AskID for an ask, TurnID
// for a turn start or end, else ItemID (a usage its step's id, a turn.resumed
// its resume's id), else the native Seq. TurnID is never part of an item's id.
func nativeRow(agentID, kind string, e loomharness.Event) loomstore.Event {
	key := e.ItemID
	switch e.Type {
	case loomharness.EventMessageDelivered:
		key = e.InputKey
	case loomharness.EventAskOpened, loomharness.EventAskResolved, loomharness.EventAskLost:
		key = e.AskID
	case loomharness.EventTurnStarted, loomharness.EventTurnCompleted:
		key = e.TurnID
	}
	if key == "" {
		key = "seq:" + strconv.FormatInt(e.Seq, 10)
	}
	b, _ := json.Marshal(struct {
		Session    string `json:"session"`
		ItemID     string `json:"itemId,omitempty"`
		ItemKind   string `json:"itemKind,omitempty"`
		InputKey   string `json:"inputKey,omitempty"`
		AskID      string `json:"askId,omitempty"`
		Text       string `json:"text,omitempty"`
		StopReason string `json:"stopReason,omitempty"`
	}{e.Session.NativeID, e.ItemID, e.ItemKind, e.InputKey, e.AskID, e.Text, e.StopReason})
	return loomstore.Event{AgentID: agentID, Kind: kind, TurnID: e.TurnID, Payload: b,
		EventID: kind + ":" + e.Session.Root + ":" + e.Session.NativeID + ":" + key}
}

// emit saves a Loom event in agent_events, then publishes it on the Bus (the
// Bus is delivery only). Its EventID is a hash of the event itself, so the
// same event saved again is one row, and the Bus copy names its row.
func (s *Service) emit(ctx context.Context, e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	e.EventID = e.Type + ":" + hex.EncodeToString(sum[:16])
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: e.AgentID, EventID: e.EventID,
		Kind: e.Type, TurnID: e.TurnID, Payload: b}); err != nil {
		return err
	}
	s.Bus.publish(e)
	return nil
}

func isCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}
