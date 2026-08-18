package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestAuditHandlerReturnsLockedWireContract(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	service := &stubAuditService{
		events: []store.AuditEvent{{
			ID: "1770000000000-0", Timestamp: timestamp, Actor: "api-architect-1", Action: "issue.update",
			EntityType: "issue", EntityID: "TEAMBACKEND-1", WorkspaceID: "WS",
			Before: `{"status":"open","priority":2}`,
			After:  `{"status":"in_progress","priority":1}`,
			Metadata: map[string]string{
				"reason": "claimed",
			},
		}},
		next: "1770000000000-0",
	}
	mux := http.NewServeMux()
	NewModule(service).Register(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/audit?since=1769999999999-0&entity=TEAMBACKEND-1&actor=api-architect-1", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if service.workspace != "WS" || service.since != "1769999999999-0" || service.limit != defaultLimit || service.entity != "TEAMBACKEND-1" || service.actor != "api-architect-1" {
		t.Fatalf("service request = workspace=%q since=%q limit=%d entity=%q actor=%q", service.workspace, service.since, service.limit, service.entity, service.actor)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assertExactKeys(t, body, "success", "data")
	if body["success"] != true {
		t.Fatalf("success = %#v", body["success"])
	}
	data := body["data"].(map[string]any)
	assertExactKeys(t, data, "events", "next_cursor")
	if data["next_cursor"] != "1770000000000-0" {
		t.Fatalf("next_cursor = %#v", data["next_cursor"])
	}
	eventsBody := data["events"].([]any)
	if len(eventsBody) != 1 {
		t.Fatalf("events = %#v", eventsBody)
	}
	event := eventsBody[0].(map[string]any)
	assertExactKeys(t, event, "cursor", "timestamp", "actor", "action", "entity_type", "entity_id", "details")
	if event["timestamp"] != "2026-08-14T12:00:00Z" || event["actor"] != "api-architect-1" || event["action"] != "issue.update" {
		t.Fatalf("event identity = %#v", event)
	}
	details := event["details"].(map[string]any)
	for key, want := range map[string]any{
		"old_status": "open", "new_status": "in_progress", "old_priority": float64(2), "new_priority": float64(1), "reason": "claimed",
	} {
		if details[key] != want {
			t.Errorf("details[%q] = %#v, want %#v; details=%#v", key, details[key], want, details)
		}
	}
}

func TestAuditHandlerOmitsEmptyDetails(t *testing.T) {
	t.Parallel()
	service := &stubAuditService{events: []store.AuditEvent{{
		Timestamp: time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC),
		Actor:     "actor", Action: "future.action", EntityType: "issue", EntityID: "I-1",
	}}}
	mux := http.NewServeMux()
	NewModule(service).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/audit?limit=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("empty details were not omitted: %s", rec.Body.String())
	}
}

func TestAuditHandlerRejectsLimitOverMaximum(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	NewModule(&stubAuditService{}).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/audit?limit=501", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"kind":"validation_error"`) {
		t.Fatalf("unexpected validation error: %s", rec.Body.String())
	}
}

func TestAuditModuleUsesWorkspaceMiddleware(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	NewModule(&stubAuditService{}).
		WithWorkspaceMiddleware(middleware.Workspace(func(string) bool { return false })).
		Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/MISSING/audit", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

type stubAuditService struct {
	events                          []store.AuditEvent
	next                            string
	workspace, since, entity, actor string
	limit                           int
}

func (s *stubAuditService) ListAuditEvents(
	_ context.Context,
	workspace, since string,
	limit int,
	entity, actor string,
) ([]store.AuditEvent, string, error) {
	s.workspace, s.since, s.limit, s.entity, s.actor = workspace, since, limit, entity, actor
	return append([]store.AuditEvent(nil), s.events...), s.next, nil
}

func assertExactKeys(t *testing.T, value map[string]any, keys ...string) {
	t.Helper()
	if len(value) != len(keys) {
		t.Fatalf("keys = %#v, want %v", value, keys)
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			t.Fatalf("object %#v missing key %q", value, key)
		}
	}
}
