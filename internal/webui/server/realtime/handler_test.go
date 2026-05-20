package realtime

import (
	"context"
	"errors"
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

func TestHandler_FleetDBOnlyReconnectCanMoveBetweenServeProcesses(t *testing.T) {
	const workspaceID = "ws-fleet"
	durableEvents := []rpc.MutationEvent{
		{Cursor: "1700000000800-0", Type: "update", IssueID: "before-disconnect", Timestamp: time.Date(2026, 5, 1, 12, 6, 0, 0, time.UTC)},
		{Cursor: "1700000000900-0", Type: "update", IssueID: "missed-on-other-process", Timestamp: time.Date(2026, 5, 1, 12, 7, 0, 0, time.UTC)},
	}
	getSince := func(wsID string, since string) []rpc.MutationEvent {
		if wsID != workspaceID {
			t.Errorf("workspace = %q, want %q", wsID, workspaceID)
		}
		var out []rpc.MutationEvent
		for _, event := range durableEvents {
			if event.Cursor > since {
				out = append(out, event)
			}
		}
		return out
	}
	newProcess := func() *Handler {
		h := NewHandler(HandlerConfig{
			Hub:               NewHub(),
			GetMutationsSince: getSince,
			WorkspaceFromCtx:  func(context.Context) string { return workspaceID },
		})
		h.heartbeatInterval = time.Hour
		return h
	}

	firstProcessBody := serveSSEOnce(t, newProcess(), func(req *http.Request) {
		q := req.URL.Query()
		q.Set("since", "1700000000000-0")
		req.URL.RawQuery = q.Encode()
	})
	if !strings.Contains(firstProcessBody, "id: 1700000000800-0\n") {
		t.Fatalf("first process did not emit durable cursor:\n%s", firstProcessBody)
	}

	secondProcessBody := serveSSEOnce(t, newProcess(), func(req *http.Request) {
		req.Header.Set("Last-Event-ID", "1700000000800-0")
	})
	if strings.Contains(secondProcessBody, "before-disconnect") {
		t.Fatalf("second process replayed already-acknowledged mutation:\n%s", secondProcessBody)
	}
	if !strings.Contains(secondProcessBody, `"issue_id":"missed-on-other-process"`) {
		t.Fatalf("second process did not catch up missed durable mutation:\n%s", secondProcessBody)
	}
	if !strings.Contains(secondProcessBody, "id: 1700000000900-0\n") {
		t.Fatalf("second process did not preserve fleet-db cursor id:\n%s", secondProcessBody)
	}
}

func TestHandlerValidateAuthBranches(t *testing.T) {
	h := NewHandler(HandlerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rr := httptest.NewRecorder()
	if !h.validateAuth(rr, req) {
		t.Fatal("open mode validateAuth returned false")
	}

	store, err := NewTokenStore()
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	defer store.Stop()
	h = NewHandler(HandlerConfig{
		TokenStore:       store,
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})

	rr = httptest.NewRecorder()
	if h.validateAuth(rr, req) {
		t.Fatal("missing token validateAuth returned true")
	}
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "authentication required") {
		t.Fatalf("missing token response = %d %q", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/events?token=bad", nil)
	rr = httptest.NewRecorder()
	if h.validateAuth(rr, req) {
		t.Fatal("bad token validateAuth returned true")
	}
	if rr.Code != http.StatusUnauthorized || !strings.Contains(rr.Body.String(), "invalid or expired token") {
		t.Fatalf("bad token response = %d %q", rr.Code, rr.Body.String())
	}

	token, err := store.Generate("user-1", "ws-1")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/events?token="+token, nil)
	rr = httptest.NewRecorder()
	if !h.validateAuth(rr, req) {
		t.Fatalf("valid token validateAuth returned false: %d %q", rr.Code, rr.Body.String())
	}
}

func TestHandlerServeHTTPAuthenticatedCallback(t *testing.T) {
	store, err := NewTokenStore()
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	defer store.Stop()
	token, err := store.Generate("user-1", "ws-auth")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var gotWorkspace string
	h := NewHandler(HandlerConfig{
		Hub:              NewHub(),
		TokenStore:       store,
		WorkspaceFromCtx: func(context.Context) string { return "ws-auth" },
		OnAuthenticated: func(_ context.Context, ws string) {
			gotWorkspace = ws
		},
	})
	h.heartbeatInterval = time.Hour

	body := serveSSEOnce(t, h, func(req *http.Request) {
		q := req.URL.Query()
		q.Set("token", token)
		req.URL.RawQuery = q.Encode()
	})
	if gotWorkspace != "ws-auth" {
		t.Fatalf("OnAuthenticated workspace = %q, want ws-auth", gotWorkspace)
	}
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("authenticated SSE did not connect:\n%s", body)
	}
}

func TestHandlerServeHTTPEdgeBranches(t *testing.T) {
	t.Run("auth rejection stops before stream setup", func(t *testing.T) {
		store, err := NewTokenStore()
		if err != nil {
			t.Fatalf("NewTokenStore: %v", err)
		}
		defer store.Stop()

		h := NewHandler(HandlerConfig{Hub: NewHub(), TokenStore: store})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("writer without flusher", func(t *testing.T) {
		h := NewHandler(HandlerConfig{Hub: NewHub()})
		w := &nonFlushingResponseWriter{header: http.Header{}}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
		if w.status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.status)
		}
	})

	t.Run("request canceled before register", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h := NewHandler(HandlerConfig{Hub: NewHub()})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx))
		if rr.Body.String() != "" {
			t.Fatalf("pre-canceled request wrote body %q", rr.Body.String())
		}
	})

	t.Run("catchup write failure", func(t *testing.T) {
		h := NewHandler(HandlerConfig{
			Hub: NewHub(),
			GetMutationsSince: func(string, string) []rpc.MutationEvent {
				return []rpc.MutationEvent{{Cursor: "1-0", Type: "update", IssueID: "task-1", Timestamp: time.Now().UTC()}}
			},
			WorkspaceFromCtx: func(context.Context) string { return "WS" },
		})
		w := &failingFlushWriter{header: http.Header{}, failOnWrite: 1}
		req := httptest.NewRequest(http.MethodGet, "/events?since=0-0", nil)
		h.ServeHTTP(w, req)
		if w.writes != 1 {
			t.Fatalf("writes = %d, want catch-up failure on first write", w.writes)
		}
	})

	t.Run("retry write failure", func(t *testing.T) {
		h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "WS" }})
		w := &failingFlushWriter{header: http.Header{}, failOnWrite: 1}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
		if w.writes != 1 {
			t.Fatalf("writes = %d, want retry failure on first write", w.writes)
		}
	})

	t.Run("connected write failure", func(t *testing.T) {
		h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "WS" }})
		w := &failingFlushWriter{header: http.Header{}, failOnWrite: 2}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
		if w.writes != 2 {
			t.Fatalf("writes = %d, want connected failure on second write", w.writes)
		}
	})

	t.Run("heartbeat write failure records disconnect error", func(t *testing.T) {
		h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "WS" }})
		h.heartbeatInterval = time.Millisecond
		w := &failingFlushWriter{header: http.Header{}, failOnWrite: 3}
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
		if w.writes < 3 {
			t.Fatalf("writes = %d, want heartbeat failure after handshake", w.writes)
		}
	})
}

func TestHandlerStreamLoopServerClose(t *testing.T) {
	h := NewHandler(HandlerConfig{})
	h.heartbeatInterval = time.Hour
	rr := httptest.NewRecorder()
	sw, err := NewWriter(rr)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	client := NewClient(1, 1, "", nil, "ws-1")
	close(client.send)

	reason, err := h.streamLoop(sw, client, context.Background())
	if err != nil {
		t.Fatalf("streamLoop err = %v", err)
	}
	if reason != disconnectReasonServerClose {
		t.Fatalf("streamLoop reason = %q, want %q", reason, disconnectReasonServerClose)
	}
}

func TestHandlerStreamLoopErrorBranches(t *testing.T) {
	t.Run("mutation write error", func(t *testing.T) {
		h := NewHandler(HandlerConfig{})
		h.heartbeatInterval = time.Hour
		w := &failingFlushWriter{header: http.Header{}, failOnWrite: 1}
		sw, err := NewWriter(w)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		client := NewClient(1, 1, "", nil, "WS")
		client.send <- &MutationPayload{Type: "update", IssueID: "task-1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
		reason, err := h.streamLoop(sw, client, context.Background())
		if err == nil || reason != disconnectReasonError {
			t.Fatalf("streamLoop reason=%q err=%v, want write error", reason, err)
		}
	})

	t.Run("deadline context is an error disconnect", func(t *testing.T) {
		h := NewHandler(HandlerConfig{})
		rr := httptest.NewRecorder()
		sw, err := NewWriter(rr)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 0)
		defer cancel()
		reason, err := h.streamLoop(sw, NewClient(1, 1, "", nil, "WS"), ctx)
		if !errors.Is(err, context.DeadlineExceeded) || reason != disconnectReasonError {
			t.Fatalf("streamLoop reason=%q err=%v, want deadline error", reason, err)
		}
	})

	t.Run("event id falls back when timestamp is stale", func(t *testing.T) {
		eventIDCounter.Store(1000)
		id := eventIDForMutation(&MutationPayload{Timestamp: time.Unix(0, 0).UTC().Format(time.RFC3339Nano)})
		if id != "1001" {
			t.Fatalf("eventIDForMutation stale timestamp = %q, want 1001", id)
		}
	})
}

type nonFlushingResponseWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (w *nonFlushingResponseWriter) Header() http.Header { return w.header }

func (w *nonFlushingResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *nonFlushingResponseWriter) WriteHeader(status int) { w.status = status }

type failingFlushWriter struct {
	header      http.Header
	status      int
	writes      int
	failOnWrite int
	body        strings.Builder
}

func (w *failingFlushWriter) Header() http.Header { return w.header }

func (w *failingFlushWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.failOnWrite == w.writes {
		return 0, errors.New("forced write failure")
	}
	return w.body.Write(data)
}

func (w *failingFlushWriter) WriteHeader(status int) { w.status = status }

func (w *failingFlushWriter) Flush() {}

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
