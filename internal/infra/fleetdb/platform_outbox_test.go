package fleetdb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// outboxTestServer is the fake fleet-db for the task-run-event journal and
// outbox routes. It asserts snake_case request shapes (field names AND enum
// values) and responds with snake_case bodies like fleet-db@2ea6d00 does.
func outboxTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	outboxCreates := 0
	firstOutbox := map[string]any{
		"workspace_key": "WS",
		"outbox_id":     "outbox-1",
		"seq":           1,
		"kind":          "lead_assignment",
		"epic_id":       "WS-1",
		"target_agent":  "lead",
		"body":          "epic assigned",
		"dedupe_key":    "dk-1",
		"status":        "pending",
		"attempt":       0,
		"created_at":    now,
		"updated_at":    now,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-run-events":
			var req struct {
				EventID    string  `json:"event_id"`
				TaskRunID  string  `json:"task_run_id"`
				Type       string  `json:"type"`
				Status     string  `json:"status"`
				Attempt    int     `json:"attempt"`
				LeaseToken *string `json:"lease_token"`
			}
			decodeJSONBody(t, r, &req)
			if req.EventID != "task-run-1#1#taskRunQueued" || req.TaskRunID != "task-run-1" || req.Attempt != 1 {
				t.Errorf("append event body = %+v", req)
			}
			if req.Type != "task_run_queued" || req.Status != "queued" {
				t.Errorf("append event wire enums = type %q status %q, want snake_case type", req.Type, req.Status)
			}
			if req.LeaseToken != nil {
				t.Errorf("append event exposed lease_token = %q", *req.LeaseToken)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]any{
				"workspace_key": "WS",
				"event_id":      req.EventID,
				"seq":           7,
				"task_run_id":   req.TaskRunID,
				"type":          "task_run_queued",
				"status":        "queued",
				"attempt":       req.Attempt,
				"occurred_at":   now,
				"created_at":    now,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/task-run-events":
			q := r.URL.Query()
			if q.Get("epic_id") != "WS-1" || q.Get("driver_run_id") != "run-1" || q.Get("after_seq") != "7" || q.Get("limit") != "10" {
				t.Errorf("list events query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{
				"task_run_events": []map[string]any{{
					"workspace_key": "WS",
					"event_id":      "task-run-1#1#taskRunCompleted",
					"seq":           8,
					"epic_id":       "WS-1",
					"driver_run_id": "run-1",
					"task_run_id":   "task-run-1",
					"type":          "task_run_completed",
					"status":        "completed",
					"attempt":       1,
					"occurred_at":   now,
				}},
				"count": 1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/outbox":
			var req struct {
				OutboxID    string `json:"outbox_id"`
				Kind        string `json:"kind"`
				EpicID      string `json:"epic_id"`
				TargetAgent string `json:"target_agent"`
				Body        string `json:"body"`
				DedupeKey   string `json:"dedupe_key"`
			}
			decodeJSONBody(t, r, &req)
			if req.Kind != "lead_assignment" {
				t.Errorf("outbox create kind = %q, want snake_case lead_assignment", req.Kind)
			}
			if req.DedupeKey != "dk-1" || req.TargetAgent != "lead" {
				t.Errorf("outbox create body = %+v", req)
			}
			outboxCreates++
			if outboxCreates == 1 && req.OutboxID != "outbox-1" {
				t.Errorf("first outbox create id = %q, want outbox-1", req.OutboxID)
			}
			// fleet-db dedupes on dedupe_key: the second create returns the
			// existing row (same outbox_id/seq) with 201, not a new one.
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, firstOutbox)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/outbox/due":
			q := r.URL.Query()
			if q.Get("limit") != "5" {
				t.Errorf("due query limit = %q", q.Get("limit"))
			}
			parsed, err := time.Parse(time.RFC3339, q.Get("now"))
			if err != nil || !parsed.Equal(now) {
				t.Errorf("due query now = %q err=%v, want RFC3339 %v", q.Get("now"), err, now)
			}
			writeJSON(t, w, map[string]any{"outbox": []map[string]any{firstOutbox}, "count": 1})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/outbox/outbox-1/result":
			var req struct {
				Status         string     `json:"status"`
				Attempt        int        `json:"attempt"`
				NextRetryAt    *time.Time `json:"next_retry_at"`
				LastError      string     `json:"last_error"`
				InboxMessageID string     `json:"inbox_message_id"`
			}
			decodeJSONBody(t, r, &req)
			if req.Status != "pending" || req.Attempt != 1 || req.NextRetryAt == nil || req.LastError != "lead busy" {
				t.Errorf("result body = %+v", req)
			}
			updated := cloneJSONMap(firstOutbox)
			updated["status"] = "pending"
			updated["attempt"] = 1
			updated["next_retry_at"] = *req.NextRetryAt
			updated["last_error"] = req.LastError
			writeJSON(t, w, updated)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/outbox/missing/result":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "not_found", "message": "outbox record not found"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func cloneJSONMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func TestPlatformOutboxClientTaskRunEventRoutes(t *testing.T) {
	ts := outboxTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

	// Append derives EventID when empty and translates the camelCase
	// domain type to the snake_case wire value (and back on decode).
	event, err := client.TaskRunEvents().Append(t.Context(), store.TaskRunEventAppend{
		WorkspaceKey: "WS",
		TaskRunID:    "task-run-1",
		Type:         domain.TaskRunEventQueued,
		Status:       domain.TaskRunQueued,
		Attempt:      1,
		OccurredAt:   now,
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if event.EventID != "task-run-1#1#taskRunQueued" || event.Seq != 7 {
		t.Fatalf("Append event = %+v, want derived event id + seq 7", event)
	}
	if event.Type != domain.TaskRunEventQueued || event.Status != domain.TaskRunQueued {
		t.Fatalf("Append enums = type %q status %q, want camelCase domain values", event.Type, event.Status)
	}

	events, err := client.TaskRunEvents().ListSince(t.Context(), "WS", store.TaskRunEventFilter{
		EpicID:      "WS-1",
		DriverRunID: "run-1",
		AfterSeq:    7,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListSince: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 8 || events[0].Type != domain.TaskRunEventCompleted {
		t.Fatalf("ListSince = %+v, want one taskRunCompleted event at seq 8", events)
	}
}

func TestPlatformOutboxClientOutboxRoutes(t *testing.T) {
	ts := outboxTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)

	created, err := client.Outbox().Create(t.Context(), store.OutboxCreate{
		WorkspaceKey: "WS",
		OutboxID:     "outbox-1",
		Kind:         domain.OutboxKindLeadAssignment,
		EpicID:       "WS-1",
		TargetAgent:  "lead",
		Body:         "epic assigned",
		DedupeKey:    "dk-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.OutboxID != "outbox-1" || created.Seq != 1 || created.Kind != domain.OutboxKindLeadAssignment || created.Status != domain.OutboxStatusPending {
		t.Fatalf("Create = %+v, want pending leadAssignment outbox-1", created)
	}

	// Dedupe collision: same DedupeKey with a new OutboxID returns the
	// existing record, not a new row.
	dup, err := client.Outbox().Create(t.Context(), store.OutboxCreate{
		WorkspaceKey: "WS",
		OutboxID:     "outbox-2",
		Kind:         domain.OutboxKindLeadAssignment,
		EpicID:       "WS-1",
		TargetAgent:  "lead",
		Body:         "epic assigned",
		DedupeKey:    "dk-1",
	})
	if err != nil {
		t.Fatalf("Create duplicate: %v", err)
	}
	if dup.OutboxID != "outbox-1" || dup.Seq != 1 {
		t.Fatalf("duplicate Create = %+v, want existing outbox-1", dup)
	}

	due, err := client.Outbox().ListDue(t.Context(), "WS", store.OutboxDueFilter{Now: now, Limit: 5})
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 || due[0].OutboxID != "outbox-1" || due[0].Kind != domain.OutboxKindLeadAssignment {
		t.Fatalf("ListDue = %+v, want one leadAssignment record", due)
	}

	retryAt := now.Add(30 * time.Second)
	marked, err := client.Outbox().MarkResult(t.Context(), "WS", "outbox-1", store.OutboxDeliveryUpdate{
		Status:      domain.OutboxStatusPending,
		Attempt:     1,
		NextRetryAt: &retryAt,
		LastError:   "lead busy",
	})
	if err != nil {
		t.Fatalf("MarkResult: %v", err)
	}
	if marked.Status != domain.OutboxStatusPending || marked.Attempt != 1 || marked.NextRetryAt == nil || !marked.NextRetryAt.Equal(retryAt) {
		t.Fatalf("MarkResult = %+v, want rescheduled pending record", marked)
	}
}

// TestPlatformOutboxClientSentinelMapping checks the HTTP-status → domain
// sentinel mapping for the new routes, table-driven over the store calls.
func TestPlatformOutboxClientSentinelMapping(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		code     string
		call     func(t *testing.T, c *Client) error
		sentinel error
	}{
		{
			name:   "mark result missing record",
			status: http.StatusNotFound,
			code:   "not_found",
			call: func(t *testing.T, c *Client) error {
				_, err := c.Outbox().MarkResult(t.Context(), "WS", "missing", store.OutboxDeliveryUpdate{Status: domain.OutboxStatusDelivered})
				return err
			},
			sentinel: domain.ErrNotFound,
		},
		{
			name:   "outbox get missing record",
			status: http.StatusNotFound,
			code:   "not_found",
			call: func(t *testing.T, c *Client) error {
				_, err := c.Outbox().Get(t.Context(), "WS", "missing")
				return err
			},
			sentinel: domain.ErrNotFound,
		},
		{
			name:   "append invalid event",
			status: http.StatusBadRequest,
			code:   "invalid_parameter",
			call: func(t *testing.T, c *Client) error {
				_, err := c.TaskRunEvents().Append(t.Context(), store.TaskRunEventAppend{WorkspaceKey: "WS", TaskRunID: "task-run-1", Type: domain.TaskRunEventQueued})
				return err
			},
			sentinel: domain.ErrInvalid,
		},
		{
			name:   "outbox create invalid",
			status: http.StatusUnprocessableEntity,
			code:   "invalid_parameter",
			call: func(t *testing.T, c *Client) error {
				_, err := c.Outbox().Create(t.Context(), store.OutboxCreate{WorkspaceKey: "WS", Kind: domain.OutboxKindLeadAssignment})
				return err
			},
			sentinel: domain.ErrInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				writeJSON(t, w, map[string]any{"error": map[string]string{"code": tt.code, "message": tt.name}})
			}))
			defer ts.Close()
			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.call(t, client); !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want sentinel %v", err, tt.sentinel)
			}
		})
	}
}

// TestPlatformOutboxClientGetRoute documents the Get path shape. NOTE:
// fleet-db@2ea6d00 does not register this route yet (storage has
// GetOutboxRecord, but no GET /outbox/{outbox_id} handler) — see the
// implementation comment on outboxStore.Get.
func TestPlatformOutboxClientGetRoute(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/outbox/outbox-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, map[string]any{
			"workspace_key": "WS",
			"outbox_id":     "outbox-1",
			"seq":           1,
			"kind":          "lead_task_message",
			"target_agent":  "lead",
			"dedupe_key":    "dk-1",
			"status":        "delivered",
			"attempt":       1,
			"created_at":    now,
			"updated_at":    now,
			"delivered_at":  now,
		})
	}))
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := client.Outbox().Get(t.Context(), "WS", "outbox-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if record.Kind != domain.OutboxKindLeadTaskMessage || record.Status != domain.OutboxStatusDelivered || record.DeliveredAt == nil {
		t.Fatalf("Get = %+v, want delivered leadTaskMessage", record)
	}
}
