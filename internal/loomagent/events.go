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

// feedRetry is how long RunFeed waits before it opens the feed again.
var feedRetry = 200 * time.Millisecond

// RunFeed ingests the named harness's live feed until ctx ends (§5.2): each
// completed native event of an agent's session is saved in agent_events and
// then applied to its slots and turn. After every (re)connect and on each
// feed.gap it backfills every live agent's session from its native history,
// so events the feed missed, or that came while Loom was down, are saved
// once (stable EventIDs) and never invented from the live bus. A failed save
// or history read is logged and the feed is reopened, so the backfill after
// the reconnect reads past nothing unsaved.
func (s *Service) RunFeed(ctx context.Context, harness string) {
	for h := s.harnesses[harness]; h != nil && ctx.Err() == nil; {
		if err := s.readFeed(ctx, harness, h); err != nil && ctx.Err() == nil {
			slog.Warn("loomagent: event ingestion failed; reopening the feed to backfill", "harness", harness, "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(feedRetry):
		}
	}
}

// readFeed backfills, then ingests h's feed until it ends or a save fails.
func (s *Service) readFeed(ctx context.Context, harness string, h loomharness.Harness) error {
	f, err := h.Feed(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := s.backfill(ctx, harness); err != nil { // after subscribing, so nothing falls between
		return err
	}
	for e := range f.Events() {
		if e.Type == loomharness.EventFeedGap {
			err = s.backfill(ctx, harness)
		} else {
			err = s.ingest(ctx, harness, e)
		}
		if err != nil {
			return err
		}
	}
	return nil
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
		sess := s.harnesses[harness].Session(loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID})
		for after := ""; ; {
			page, err := sess.Messages(ctx, after, 100)
			if errors.Is(err, loomharness.ErrSessionNotFound) {
				break
			}
			if err != nil {
				return err
			}
			for _, e := range page.Events {
				if err := s.ingest(ctx, harness, e); err != nil {
					return err
				}
			}
			if after = page.Next; after == "" {
				break
			}
		}
	}
	return nil
}

// ingest saves one native event of an owned session, then applies it. A
// repeat (live after backfill, or backfill after live) saves nothing new, and
// HarnessEvent ignores events of a turn that is not running.
func (s *Service) ingest(ctx context.Context, harness string, e loomharness.Event) error {
	id, err := s.store.NativeSessionOwner(ctx, harness, e.Session.Root, e.Session.NativeID)
	if errors.Is(err, loomstore.ErrNotFound) {
		return nil // not an agent's session
	}
	if err != nil {
		return err
	}
	if _, err := s.live(ctx, id); isCode(err, CodeAgentNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if kind, ok := savedKinds[e.Type]; ok {
		if _, err := s.events.Append(ctx, nativeRow(id, kind, e)); err != nil {
			return err
		}
	}
	return s.HarnessEvent(ctx, id, e)
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
// for a turn boundary, else ItemID (a usage carries its step's id), else the
// native Seq. TurnID is never part of an item's id.
func nativeRow(agentID, kind string, e loomharness.Event) loomstore.Event {
	key := e.ItemID
	switch e.Type {
	case loomharness.EventMessageDelivered:
		key = e.InputKey
	case loomharness.EventAskOpened, loomharness.EventAskResolved, loomharness.EventAskLost:
		key = e.AskID
	case loomharness.EventTurnStarted, loomharness.EventTurnCompleted, loomharness.EventTurnResumed:
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
