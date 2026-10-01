package loomagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// once (stable EventIDs) and never invented from the live bus.
func (s *Service) RunFeed(ctx context.Context, harness string) {
	h := s.harnesses[harness]
	for h != nil && ctx.Err() == nil {
		if f, err := h.Feed(ctx); err == nil {
			s.backfill(ctx, harness) // after subscribing, so nothing falls between
			for e := range f.Events() {
				if e.Type == loomharness.EventFeedGap {
					s.backfill(ctx, harness)
					continue
				}
				_ = s.ingest(ctx, harness, e)
			}
			_ = f.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(feedRetry):
		}
	}
}

// backfill ingests the native history of every live agent's current session.
func (s *Service) backfill(ctx context.Context, harness string) {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness})
	if err != nil {
		return
	}
	for _, a := range agents {
		if a.HarnessSessionID == nil {
			continue
		}
		sess := s.harnesses[harness].Session(loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID})
		for after := ""; ; {
			page, err := sess.Messages(ctx, after, 100)
			if err != nil {
				break
			}
			for _, e := range page.Events {
				_ = s.ingest(ctx, harness, e)
			}
			if after = page.Next; after == "" {
				break
			}
		}
	}
}

// ingest saves one native event of an owned session, then applies it. A
// repeat (live after backfill, or backfill after live) saves nothing new, and
// HarnessEvent ignores events of a turn that is not running.
func (s *Service) ingest(ctx context.Context, harness string, e loomharness.Event) error {
	id, err := s.store.NativeSessionOwner(ctx, harness, e.Session.Root, e.Session.NativeID)
	if err != nil {
		return err
	}
	if _, err := s.live(ctx, id); err != nil {
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

// nativeRow is e as a saved row. Its EventID comes from native ids only, so
// the live feed and a catch-up read give the same row.
func nativeRow(agentID, kind string, e loomharness.Event) loomstore.Event {
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
		EventID: kind + ":" + e.Session.NativeID + ":" + e.TurnID + ":" + e.ItemID + ":" + e.InputKey + ":" + e.AskID}
}

// emit saves a Loom event in agent_events, then publishes it on the Bus. The
// Bus is delivery only; a deleted agent's events are published, not saved.
func (s *Service) emit(ctx context.Context, e Event, save bool) error {
	if save {
		var id [12]byte
		_, _ = rand.Read(id[:])
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := s.events.Append(ctx, loomstore.Event{AgentID: e.AgentID, EventID: "ev_" + hex.EncodeToString(id[:]),
			Kind: e.Type, TurnID: e.TurnID, Payload: b}); err != nil {
			return err
		}
	}
	s.Bus.publish(e)
	return nil
}
