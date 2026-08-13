// This file references NewSessionService from the root webui package which
// cannot be imported from this sub-package without creating a cycle.

package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

const testSHWSID = "test-ws-uuid"

type scrollbackResultService struct {
	stubSessionService
	result *sessioncoord.SessionScrollbackResult
}

func (service *scrollbackResultService) GetSessionScrollback(context.Context, string, string, string) (*sessioncoord.SessionScrollbackResult, error) {
	return service.result, nil
}

func setupSessionHistoryStore(t *testing.T) *localredis.SessionHistoryStore {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return localredis.NewSessionHistoryStore(rdb, nil)
}

func withSHWSContext(r *http.Request, wsID string) *http.Request {
	return r.WithContext(middleware.WithWorkspace(r.Context(), wsID))
}

func TestHandleListSessionHistory_ReturnsRecords(t *testing.T) {
	store := setupSessionHistoryStore(t)
	ctx := context.Background()

	// Add a session record.
	record := interaction.SessionHistoryRecord{
		ID:          "issue-proj-1:1700000000",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "claude",
		Status:      "active",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}
	if err := store.Add(ctx, testSHWSID, record); err != nil {
		t.Fatalf("Add: %v", err)
	}

	handler := handleListSessionHistory(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions", nil)
	req.SetPathValue("issueId", "proj.1")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                              `json:"success"`
		Data    []sessioncoord.SessionHistoryItem `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data) = %d, want 1", len(resp.Data))
	}
	if resp.Data[0].ID != "issue-proj-1:1700000000" {
		t.Errorf("data[0].ID = %q, want %q", resp.Data[0].ID, "issue-proj-1:1700000000")
	}
	if resp.Data[0].Backend != "claude" {
		t.Errorf("data[0].Backend = %q, want %q", resp.Data[0].Backend, "claude")
	}
	if resp.Data[0].ScrollbackEvidenceStatus != "content_unavailable" {
		t.Errorf("scrollback evidence status = %q", resp.Data[0].ScrollbackEvidenceStatus)
	}
}

func TestHandleListSessionHistory_EmptyArrayForUnknownIssue(t *testing.T) {
	store := setupSessionHistoryStore(t)

	handler := handleListSessionHistory(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/unknown.99/sessions", nil)
	req.SetPathValue("issueId", "unknown.99")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Success bool                              `json:"success"`
		Data    []sessioncoord.SessionHistoryItem `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.Data == nil {
		t.Fatal("expected non-nil data array")
	}
	if len(resp.Data) != 0 {
		t.Errorf("len(data) = %d, want 0", len(resp.Data))
	}
}

func TestHandleListSessionHistory_NilStore(t *testing.T) {
	handler := handleListSessionHistory(NewSessionService(nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions", nil)
	req.SetPathValue("issueId", "proj.1")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandleListSessionHistory_InvalidIssueID(t *testing.T) {
	store := setupSessionHistoryStore(t)
	handler := handleListSessionHistory(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues//sessions", nil)
	req = withSHWSContext(req, testSHWSID)
	// Do NOT set PathValue("issueId") to simulate empty issue ID.
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestHandleGetSessionScrollback_NilStore(t *testing.T) {
	handler := handleGetSessionScrollback(NewSessionService(nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions/rec-1/scrollback", nil)
	req.SetPathValue("issueId", "proj.1")
	req.SetPathValue("recordId", "rec-1")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["error"]; !ok {
		t.Error("expected error field in response")
	}
}

func TestHandleGetSessionScrollback_ReturnsContractText(t *testing.T) {
	handler := handleGetSessionScrollback(&scrollbackResultService{result: &sessioncoord.SessionScrollbackResult{
		Content: "first\nsecond", Lines: 2,
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions/rec-1/scrollback", nil)
	req.SetPathValue("issueId", "proj.1")
	req.SetPathValue("recordId", "rec-1")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK || rr.Body.String() != "first\nsecond" {
		t.Fatalf("response = %d %q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
}

func TestHandleGetSessionScrollback_RecordNotFound(t *testing.T) {
	store := setupSessionHistoryStore(t)
	handler := handleGetSessionScrollback(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions/nonexistent/scrollback", nil)
	req.SetPathValue("issueId", "proj.1")
	req.SetPathValue("recordId", "nonexistent")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "session record not found" {
		t.Errorf("error = %q, want %q", resp["error"], "session record not found")
	}
}

func TestHandleGetSessionScrollback_InvalidIssueID(t *testing.T) {
	store := setupSessionHistoryStore(t)
	handler := handleGetSessionScrollback(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues//sessions/rec-1/scrollback", nil)
	req = withSHWSContext(req, testSHWSID)
	// Do NOT set PathValue("issueId") to simulate empty issue ID.
	req.SetPathValue("recordId", "rec-1")
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestHandleGetSessionScrollback_EmptyRecordID(t *testing.T) {
	store := setupSessionHistoryStore(t)
	handler := handleGetSessionScrollback(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions//scrollback", nil)
	req.SetPathValue("issueId", "proj.1")
	req = withSHWSContext(req, testSHWSID)
	// Do NOT set PathValue("recordId") to simulate empty record ID.
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestHandleGetSessionScrollback_NoScrollbackAvailable(t *testing.T) {
	store := setupSessionHistoryStore(t)
	ctx := context.Background()

	// Add a completed record with no durable scrollback evidence.
	record := interaction.SessionHistoryRecord{
		ID:          "issue-proj-1:1700000000",
		SessionName: "issue-proj-1",
		IssueID:     "proj.1",
		Backend:     "claude",
		Status:      "completed",
		Launcher:    "user",
		StartedAt:   time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
	}
	if err := store.Add(ctx, testSHWSID, record); err != nil {
		t.Fatalf("Add: %v", err)
	}

	handler := handleGetSessionScrollback(NewSessionService(nil, store))

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+testSHWSID+"/issues/proj.1/sessions/issue-proj-1:1700000000/scrollback", nil)
	req.SetPathValue("issueId", "proj.1")
	req.SetPathValue("recordId", "issue-proj-1:1700000000")
	req = withSHWSContext(req, testSHWSID)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "no scrollback available for this session" {
		t.Errorf("error = %q, want %q", resp["error"], "no scrollback available for this session")
	}
}
