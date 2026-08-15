package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func auditJournalReader(t *testing.T, client *Client) store.AuditJournalReader {
	t.Helper()
	reader, ok := client.TriggerEvents().(store.AuditJournalReader)
	if !ok {
		t.Fatalf("fleetdb TriggerEvents %T does not implement store.AuditJournalReader", client.TriggerEvents())
	}
	return reader
}

func TestListAuditEventsPreservesRawEventAndFilters(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/WS/events/mutations" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("since") != "1700000000000-0" || query.Get("limit") != "25" || query.Get("entity_id") != "ISSUE-1" || query.Get("actor") != "agent-1" {
			t.Fatalf("query = %v", query)
		}
		writeJSON(t, w, map[string]any{
			"events": []map[string]any{{
				"id": "1700000000001-0", "timestamp": "2026-08-14T12:00:00Z", "actor": "agent-1",
				"action": "issue.update", "entity_type": "issue", "entity_id": "ISSUE-1", "workspace_id": "WS",
				"before": `{"status":"open"}`, "after": `{"status":"closed"}`, "metadata": map[string]string{"reason": "done"},
			}},
			"cursor": "1700000000001-0", "has_more": true,
		})
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Actor: "tester"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	events, cursor, hasMore, err := auditJournalReader(t, client).ListAuditEvents(
		context.Background(), "WS", "1700000000000-0", 25,
		store.AuditEventFilter{EntityID: "ISSUE-1", Actor: "agent-1"},
	)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(events) != 1 || cursor != "1700000000001-0" || !hasMore {
		t.Fatalf("result = events=%+v cursor=%q hasMore=%v", events, cursor, hasMore)
	}
	event := events[0]
	if event.WorkspaceID != "WS" || event.Before != `{"status":"open"}` || event.After != `{"status":"closed"}` || event.Metadata["reason"] != "done" {
		t.Fatalf("raw event = %+v", event)
	}
}

func TestSubscribeAuditEventsDecodesFleetSSE(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/WS/events/stream" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Last-Event-ID"); got != "1700000000000-0" {
			t.Fatalf("Last-Event-ID = %q", got)
		}
		if r.URL.Query().Get("entity_id") != "ISSUE-1" || r.URL.Query().Get("actor") != "agent-1" {
			t.Fatalf("query = %v", r.URL.Query())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: mutation\n")
		_, _ = fmt.Fprint(w, "id: 1700000000001-0\n")
		_, _ = fmt.Fprint(w, `data: {"id":"1700000000001-0","timestamp":"2026-08-14T12:00:00Z","actor":"agent-1","action":"issue.claim","entity_type":"issue","entity_id":"ISSUE-1","workspace_id":"WS"}`+"\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, Actor: "tester", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	eventCh, errCh := auditJournalReader(t, client).SubscribeAuditEvents(
		ctx, "WS", "1700000000000-0", store.AuditEventFilter{EntityID: "ISSUE-1", Actor: "agent-1"},
	)
	select {
	case event := <-eventCh:
		if event.ID != "1700000000001-0" || event.Action != "issue.claim" || event.Actor != "agent-1" {
			t.Fatalf("stream event = %+v", event)
		}
	case err := <-errCh:
		t.Fatalf("stream error before event: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for stream event")
	}
}
