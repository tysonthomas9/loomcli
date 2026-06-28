package fleetdb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

// IssueJournalReader is the OPTIONAL store.IssueJournalReader capability,
// served by the fleet-db backend only (memstore deliberately omits it — the
// A4 bridge is capability-gated on its presence). It is implemented on the
// triggerEventStore so it rides the same TriggerEventStore type assertion as
// TriggerEventAppender; there is no fleet-db change, the issue journal is just
// the entity_type=issue slice of GET /events/mutations.
var _ store.IssueJournalReader = (*triggerEventStore)(nil)

// mutationsResponse mirrors fleet-db's MutationsResponse (api/mutations.go).
// Wire is snake_case v1; cursor is the opaque resume token echoed back, and
// has_more reports a full page.
type mutationsResponse struct {
	Events  []journalEventWire `json:"events"`
	Cursor  string             `json:"cursor"`
	HasMore bool               `json:"has_more"`
}

// journalEventWire mirrors fleet-db's EventResponse (api/event_types.go). The
// before/after fields arrive as JSON-encoded strings (the entity snapshot is
// itself serialized JSON, not an inline object); after is unwrapped into raw
// JSON for the projection and before is dropped.
type journalEventWire struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	Actor      string            `json:"actor"`
	Action     string            `json:"action"`
	EntityType string            `json:"entity_type"`
	EntityID   string            `json:"entity_id"`
	After      string            `json:"after"`
	Metadata   map[string]string `json:"metadata"`
}

// ListIssueEvents fetches issue mutations strictly after afterCursor, oldest
// first, by polling GET /api/v1/{ws}/events/mutations?entity_type=issue with a
// since-cursor and limit. The cursor is opaque and passed through verbatim;
// "" maps to fleet-db's "0" beginning-of-stream sentinel. nextCursor is the
// response Cursor (the resume position), and hasMore is the response has_more.
//
// A malformed after-state on any one event is skipped (its After left nil)
// rather than failing the whole batch — a single poisoned snapshot must not
// stall the bridge's forward progress.
func (s *triggerEventStore) ListIssueEvents(ctx context.Context, ws, afterCursor string, limit int) ([]store.JournalEvent, string, bool, error) {
	q := url.Values{}
	q.Set("entity_type", "issue")
	since := afterCursor
	if since == "" {
		since = "0"
	}
	q.Set("since", since)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/events/mutations", q)

	var resp mutationsResponse
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, "", false, err
	}

	events := make([]store.JournalEvent, 0, len(resp.Events))
	for _, e := range resp.Events {
		events = append(events, store.JournalEvent{
			ID:        e.ID,
			Action:    e.Action,
			Actor:     e.Actor,
			EntityID:  e.EntityID,
			Timestamp: e.Timestamp,
			After:     unwrapAfter(e.After),
			Metadata:  e.Metadata,
		})
	}

	// fleet-db echoes the client's cursor when the batch is empty, so the
	// caller's afterCursor is the natural resume point; prefer the server's
	// value when present.
	nextCursor := resp.Cursor
	if nextCursor == "" {
		nextCursor = afterCursor
	}
	return events, nextCursor, resp.HasMore, nil
}

// unwrapAfter turns fleet-db's JSON-encoded after-state string into raw JSON.
// Empty or malformed input yields nil — a missing/poisoned snapshot is skipped
// rather than propagated as a hard error (see ListIssueEvents).
func unwrapAfter(after string) json.RawMessage {
	if after == "" {
		return nil
	}
	raw := json.RawMessage(after)
	if !json.Valid(raw) {
		return nil
	}
	return bytes.TrimSpace(raw)
}
