package fleetdb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/fleethttp"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// IssueJournalReader is the OPTIONAL store.IssueJournalReader capability,
// served by the fleet-db backend only (memstore deliberately omits it — the
// A4 bridge is capability-gated on its presence). It is implemented on the
// triggerEventStore so it rides the same TriggerEventStore type assertion as
// TriggerEventAppender; there is no fleet-db change, the issue journal is just
// the entity_type=issue slice of GET /events/mutations.
var _ store.IssueJournalReader = (*triggerEventStore)(nil)
var _ store.AuditJournalReader = (*triggerEventStore)(nil)

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
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Actor       string            `json:"actor"`
	Action      string            `json:"action"`
	EntityType  string            `json:"entity_type"`
	EntityID    string            `json:"entity_id"`
	WorkspaceID string            `json:"workspace_id"`
	Before      string            `json:"before,omitempty"`
	After       string            `json:"after,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

func (e journalEventWire) auditEvent() store.AuditEvent {
	return store.AuditEvent{
		ID:          e.ID,
		Timestamp:   e.Timestamp,
		Actor:       e.Actor,
		Action:      e.Action,
		EntityType:  e.EntityType,
		EntityID:    e.EntityID,
		WorkspaceID: e.WorkspaceID,
		Before:      e.Before,
		After:       e.After,
		Metadata:    e.Metadata,
	}
}

// ListAuditEvents fetches complete fleet-db mutation events strictly after
// afterCursor, oldest first. Filters are sent to fleet-db and remain part of
// the capability so history and follow share exact-match semantics.
func (s *triggerEventStore) ListAuditEvents(
	ctx context.Context,
	ws, afterCursor string,
	limit int,
	filter store.AuditEventFilter,
) ([]store.AuditEvent, string, bool, error) {
	q := auditEventQuery(afterCursor, filter)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/events/mutations", q)

	var resp mutationsResponse
	if err := s.client.do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, "", false, err
	}
	events := make([]store.AuditEvent, 0, len(resp.Events))
	for _, event := range resp.Events {
		events = append(events, event.auditEvent())
	}
	nextCursor := resp.Cursor
	if nextCursor == "" {
		nextCursor = afterCursor
	}
	return events, nextCursor, resp.HasMore, nil
}

// SubscribeAuditEvents opens fleet-db's SSE mutation stream. The HTTP and SSE
// framing live at the fleet-db infrastructure seam; callers consume typed
// AuditEvents and never assemble fleet-db URLs or parse stream frames.
func (s *triggerEventStore) SubscribeAuditEvents(
	ctx context.Context,
	ws, afterCursor string,
	filter store.AuditEventFilter,
) (<-chan store.AuditEvent, <-chan error) {
	events := make(chan store.AuditEvent, 64)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := s.readAuditEventStream(ctx, ws, afterCursor, filter, events); err != nil && ctx.Err() == nil {
			errs <- err
		}
	}()
	return events, errs
}

//nolint:funlen // Keeping SSE framing and terminal stream handling together makes reconnect semantics explicit.
func (s *triggerEventStore) readAuditEventStream(
	ctx context.Context,
	ws, afterCursor string,
	filter store.AuditEventFilter,
	out chan<- store.AuditEvent,
) error {
	q := auditEventQuery("", filter)
	path := withQuery("/api/v1/"+pathEscape(ws)+"/events/stream", q)
	s.client.mu.RLock()
	auth := fleethttp.Auth{
		BearerToken: s.client.authToken,
		APIKey:      s.client.apiKey,
		Actor:       s.client.actor,
	}
	s.client.mu.RUnlock()
	req, err := fleethttp.BuildJSONRequest(ctx, http.MethodGet, s.client.baseURL+path, auth, nil)
	if err != nil {
		return fmt.Errorf("fleetdb: build audit stream request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if afterCursor = strings.TrimSpace(afterCursor); afterCursor != "" {
		req.Header.Set("Last-Event-ID", afterCursor)
	}

	// The shared fleet client intentionally has a finite request timeout for
	// normal RPCs and long polls. Clone it with no whole-request timeout for
	// this context-owned stream while retaining its pooled transport.
	streamClient := *s.client.http
	streamClient.Timeout = 0
	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("fleetdb: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		if readErr != nil {
			return fmt.Errorf("fleetdb: GET %s: HTTP %d (read body: %w)", path, resp.StatusCode, readErr)
		}
		return classifyHTTPError(http.MethodGet, path, resp.StatusCode, body)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), maxResponseBody)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		var wire journalEventWire
		if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &wire); err != nil {
			return fmt.Errorf("fleetdb: decode audit stream event: %w", err)
		}
		data = data[:0]
		select {
		case out <- wire.auditEvent():
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("fleetdb: read audit stream: %w", err)
	}
	return flush()
}

func auditEventQuery(afterCursor string, filter store.AuditEventFilter) url.Values {
	q := url.Values{}
	if afterCursor != "" {
		q.Set("since", afterCursor)
	} else {
		q.Set("since", "0")
	}
	if filter.EntityID != "" {
		q.Set("entity_id", filter.EntityID)
	}
	if filter.Actor != "" {
		q.Set("actor", filter.Actor)
	}
	return q
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
