package issues

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleGetIssueEvents_DefaultTailReportsFullTotal(t *testing.T) {
	svc, closeServer := newPagedHistoryIssueService(t, 295)
	defer closeServer()

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events", nil)
	req.SetPathValue("id", "test-123")
	recorder := httptest.NewRecorder()

	HandleGetIssueEvents(svc).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Success     bool           `json:"success"`
		Data        []*types.Event `json:"data"`
		Cursor      string         `json:"cursor"`
		HasMore     bool           `json:"has_more"`
		TotalEvents int            `json:"total_events"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success {
		t.Fatal("success = false, want true")
	}
	if got := len(response.Data); got != 100 {
		t.Fatalf("event count = %d, want 100", got)
	}
	if got := response.Data[0].ID; got != "event-196" {
		t.Errorf("first event ID = %q, want event-196", got)
	}
	if got := response.Data[len(response.Data)-1].ID; got != "event-295" {
		t.Errorf("last event ID = %q, want event-295", got)
	}
	if !response.HasMore {
		t.Error("has_more = false, want true for a truncated newest tail")
	}
	if response.Cursor != "" {
		t.Errorf("cursor = %q, want empty for a newest-tail response", response.Cursor)
	}
	if response.TotalEvents != 295 {
		t.Errorf("total_events = %d, want 295", response.TotalEvents)
	}
}

func TestHandleGetIssueEvents_Limit500ReturnsCompleteHistory(t *testing.T) {
	svc, closeServer := newPagedHistoryIssueService(t, 295)
	defer closeServer()

	req := httptest.NewRequest(http.MethodGet, "/api/issues/test-123/events?limit=500", nil)
	req.SetPathValue("id", "test-123")
	recorder := httptest.NewRecorder()

	HandleGetIssueEvents(svc).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var response struct {
		Data        []*types.Event `json:"data"`
		HasMore     bool           `json:"has_more"`
		TotalEvents int            `json:"total_events"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := len(response.Data); got != 295 {
		t.Fatalf("event count = %d, want 295", got)
	}
	if got := response.Data[0].ID; got != "event-001" {
		t.Errorf("first event ID = %q, want event-001", got)
	}
	if got := response.Data[len(response.Data)-1].ID; got != "event-295" {
		t.Errorf("last event ID = %q, want event-295", got)
	}
	if response.TotalEvents != 295 {
		t.Errorf("total_events = %d, want 295", response.TotalEvents)
	}
	if response.HasMore {
		t.Error("has_more = true, want false for an untrimmed newest tail")
	}
}

func TestHandleGetIssueEvents_SinceWalksForwardPagesExactlyOnce(t *testing.T) {
	svc, closeServer := newPagedHistoryIssueService(t, 295)
	defer closeServer()

	since := "0"
	seen := make(map[string]bool, 295)
	pageLengths := make([]int, 0, 2)
	nextExpectedEvent := 1
	for page := 0; page < 5; page++ {
		target := "/api/issues/test-123/events?limit=500&since=" + url.QueryEscape(since)
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.SetPathValue("id", "test-123")
		recorder := httptest.NewRecorder()

		HandleGetIssueEvents(svc).ServeHTTP(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Fatalf("page %d status = %d, want %d: %s", page+1, recorder.Code, http.StatusOK, recorder.Body.String())
		}
		var response EventListResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode page %d response: %v", page+1, err)
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode page %d envelope: %v", page+1, err)
		}
		if _, ok := envelope["has_more"]; !ok {
			t.Fatalf("page %d response omitted has_more", page+1)
		}
		if response.TotalEvents != 295 {
			t.Fatalf("page %d total_events = %d, want 295", page+1, response.TotalEvents)
		}

		pageLengths = append(pageLengths, len(response.Data))
		for _, event := range response.Data {
			wantID := fmt.Sprintf("event-%03d", nextExpectedEvent)
			if event.ID != wantID {
				t.Fatalf("event order at position %d = %q, want %q", nextExpectedEvent, event.ID, wantID)
			}
			if seen[event.ID] {
				t.Fatalf("event %q returned more than once", event.ID)
			}
			seen[event.ID] = true
			nextExpectedEvent++
		}

		if !response.HasMore {
			break
		}
		if response.Cursor == "" || response.Cursor == since {
			t.Fatalf("page %d has_more without an advancing cursor: %q", page+1, response.Cursor)
		}
		since = response.Cursor
	}

	if got, want := fmt.Sprint(pageLengths), "[200 95]"; got != want {
		t.Errorf("page lengths = %s, want %s", got, want)
	}
	if got := len(seen); got != 295 {
		t.Errorf("unique event count = %d, want 295", got)
	}
	for index := 1; index <= 295; index++ {
		id := fmt.Sprintf("event-%03d", index)
		if !seen[id] {
			t.Errorf("missing event %q", id)
		}
	}
}

func newPagedHistoryIssueService(t *testing.T, eventCount int) (service.IssueService, func()) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/test-123/history") {
			http.NotFound(w, r)
			return
		}

		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if limit > 200 {
			http.Error(w, "limit exceeds 200", http.StatusBadRequest)
			return
		}

		start := 0
		if since := r.URL.Query().Get("since"); since != "" {
			parsed, parseErr := strconv.Atoi(strings.TrimPrefix(since, "cursor-"))
			if parseErr != nil {
				http.Error(w, "invalid cursor", http.StatusBadRequest)
				return
			}
			start = parsed
		}
		end := min(start+limit, eventCount)
		history := make([]map[string]any, 0, end-start)
		for index := start; index < end; index++ {
			history = append(history, map[string]any{
				"id":        fmt.Sprintf("event-%03d", index+1),
				"timestamp": time.Unix(int64(index), 0).UTC(),
				"actor":     "agent",
				"action":    "issue.updated",
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"history":      history,
				"cursor":       fmt.Sprintf("cursor-%d", end),
				"has_more":     end < eventCount,
				"total_events": eventCount,
			},
		})
	}))

	fleetBackend, err := fleet.New(fleet.Config{
		BaseURL:     server.URL,
		WorkspaceID: "test-ws",
	})
	if err != nil {
		server.Close()
		t.Fatalf("create FleetBackend: %v", err)
	}

	svc := service.NewIssueServiceWithBackend(
		nil,
		nil,
		nil,
		func(context.Context) backend.IssueBackend { return fleetBackend },
	)
	return svc, server.Close
}
