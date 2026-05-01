package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func TestHandler_FleetDBOnlyReconnectCatchUpUsesLastEventID(t *testing.T) {
	const workspaceID = "ws-fleet"
	const cursor = "1700000000000-0"

	var (
		mu       sync.Mutex
		gotWS    string
		gotSince string
	)
	h := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(wsID string, since string) []rpc.MutationEvent {
			mu.Lock()
			gotWS = wsID
			gotSince = since
			mu.Unlock()
			return []rpc.MutationEvent{{
				Cursor:    "1700000000100-0",
				Type:      "update",
				IssueID:   "task-1",
				Title:     "missed mutation",
				Timestamp: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
			}}
		},
		WorkspaceFromCtx: func(context.Context) string { return workspaceID },
	})
	h.heartbeatInterval = time.Hour

	body := serveSSEOnce(t, h, func(req *http.Request) {
		req.Header.Set("Last-Event-ID", cursor)
	})

	mu.Lock()
	defer mu.Unlock()
	if gotWS != workspaceID {
		t.Fatalf("workspace = %q, want %q", gotWS, workspaceID)
	}
	if gotSince != cursor {
		t.Fatalf("since = %q, want durable Last-Event-ID cursor %q", gotSince, cursor)
	}
	if !strings.Contains(body, "id: 1700000000100-0\n") {
		t.Fatalf("catch-up event did not preserve fleet-db cursor as SSE id:\n%s", body)
	}
	if !strings.Contains(body, `"issue_id":"task-1"`) {
		t.Fatalf("catch-up mutation missing from SSE body:\n%s", body)
	}
}

func TestHandler_FleetDBOnlyReconnectCatchUpUsesSinceQuery(t *testing.T) {
	const workspaceID = "ws-fleet"
	const queryCursor = "1700000000200-0"

	var gotSince string
	h := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(wsID string, since string) []rpc.MutationEvent {
			if wsID != workspaceID {
				t.Errorf("workspace = %q, want %q", wsID, workspaceID)
			}
			gotSince = since
			return []rpc.MutationEvent{
				{Cursor: "1700000000300-0", Type: "update", IssueID: "task-2", Timestamp: time.Date(2026, 5, 1, 12, 1, 0, 0, time.UTC)},
				{Cursor: "1700000000400-0", Type: "status", IssueID: "task-3", Timestamp: time.Date(2026, 5, 1, 12, 2, 0, 0, time.UTC)},
			}
		},
		WorkspaceFromCtx: func(context.Context) string { return workspaceID },
	})
	h.heartbeatInterval = time.Hour

	body := serveSSEOnce(t, h, func(req *http.Request) {
		q := req.URL.Query()
		q.Set("since", queryCursor)
		req.URL.RawQuery = q.Encode()
	})

	if gotSince != queryCursor {
		t.Fatalf("since = %q, want query cursor %q", gotSince, queryCursor)
	}
	if strings.Count(body, "event: mutation\n") != 2 {
		t.Fatalf("expected two missed catch-up mutations, got body:\n%s", body)
	}
	if !strings.Contains(body, "id: 1700000000300-0\n") || !strings.Contains(body, "id: 1700000000400-0\n") {
		t.Fatalf("catch-up mutations did not preserve durable fleet-db cursors:\n%s", body)
	}
}

func TestHandler_FleetDBOnlyCatchUpAppliesSourceRepoFilter(t *testing.T) {
	h := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(wsID string, since string) []rpc.MutationEvent {
			return []rpc.MutationEvent{
				{Cursor: "1700000000500-0", Type: "update", IssueID: "repo-a-task", SourceRepo: "repo-a", Timestamp: time.Date(2026, 5, 1, 12, 3, 0, 0, time.UTC)},
				{Cursor: "1700000000600-0", Type: "update", IssueID: "repo-b-task", SourceRepo: "repo-b", Timestamp: time.Date(2026, 5, 1, 12, 4, 0, 0, time.UTC)},
			}
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-fleet" },
	})
	h.heartbeatInterval = time.Hour

	body := serveSSEOnce(t, h, func(req *http.Request) {
		q := req.URL.Query()
		q.Set("since", "1700000000400-0")
		q.Set("source_repos", "repo-a")
		req.URL.RawQuery = q.Encode()
	})

	if !strings.Contains(body, `"issue_id":"repo-a-task"`) {
		t.Fatalf("matching repo mutation missing from catch-up body:\n%s", body)
	}
	if strings.Contains(body, "repo-b-task") {
		t.Fatalf("non-matching repo mutation leaked through catch-up filter:\n%s", body)
	}
}

func TestHandler_FleetDBOnlyCatchUpFailsClosedWithoutWorkspace(t *testing.T) {
	called := false
	h := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(wsID string, since string) []rpc.MutationEvent {
			called = true
			return []rpc.MutationEvent{
				{Cursor: "1700000000700-0", Type: "update", IssueID: "leaked-task", Timestamp: time.Date(2026, 5, 1, 12, 5, 0, 0, time.UTC)},
			}
		},
		WorkspaceFromCtx: func(context.Context) string { return "" },
	})
	h.heartbeatInterval = time.Hour

	body := serveSSEOnce(t, h, func(req *http.Request) {
		q := req.URL.Query()
		q.Set("since", "1700000000600-0")
		req.URL.RawQuery = q.Encode()
	})

	if called {
		t.Fatal("GetMutationsSince should not be called when workspace is empty")
	}
	if strings.Contains(body, "leaked-task") || strings.Contains(body, "event: mutation\n") {
		t.Fatalf("empty workspace catch-up should fail closed, got body:\n%s", body)
	}
}

func serveSSEOnce(t *testing.T, h *Handler, mutateReq func(*http.Request)) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	if mutateReq != nil {
		mutateReq(req)
	}
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after request cancellation")
	}
	return rr.Body.String()
}
