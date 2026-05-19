package realtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWriterFrameHelpers(t *testing.T) {
	rr := httptest.NewRecorder()
	sw, err := NewWriter(rr)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	if err := sw.WriteEvent(42, "mutation", `{"ok":true}`); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if err := sw.WriteComment("heartbeat"); err != nil {
		t.Fatalf("WriteComment: %v", err)
	}

	body := rr.Body.String()
	for _, want := range []string{"id: 42", "event: mutation", `data: {"ok":true}`, ": heartbeat"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body missing %q:\n%s", want, body)
		}
	}
}

func TestParseLastSinceMillis(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/events?since=200", nil)
	req.Header.Set("Last-Event-ID", "100")
	if got := ParseLastSinceMillis(req); got != 200 {
		t.Fatalf("query since should win when larger, got %d", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/events?since=bad", nil)
	req.Header.Set("Last-Event-ID", "300")
	if got := ParseLastSinceMillis(req); got != 300 {
		t.Fatalf("valid header should survive invalid query, got %d", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Last-Event-ID", "bad")
	if got := ParseLastSinceMillis(req); got != 0 {
		t.Fatalf("invalid cursors should return zero, got %d", got)
	}
}

func TestHubRetryDrainAndWorkspaceCount(t *testing.T) {
	h := NewHub()
	c1 := NewClient(1, ClientSendBuf, "", nil, "ws-1")
	c2 := NewClient(2, ClientSendBuf, "", nil, "ws-2")
	h.addClient(c1)
	h.addClient(c2)
	if got := h.ClientCountForWorkspace("ws-1"); got != 1 {
		t.Fatalf("ClientCountForWorkspace(ws-1) = %d, want 1", got)
	}
	if h.GetUptime() < 0 {
		t.Fatal("GetUptime returned a negative duration")
	}

	h.retryQueue = []*MutationPayload{
		{Type: "update", IssueID: "a", WorkspaceID: "ws-1"},
		{Type: "update", IssueID: "b", WorkspaceID: "ws-1"},
	}
	h.drainRetryQueue()
	if got := h.GetRetryQueueDepth(); got != 0 {
		t.Fatalf("retry queue depth after drain = %d, want 0", got)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-h.broadcast:
		default:
			t.Fatalf("broadcast channel missing drained item %d", i)
		}
	}

	for i := 0; i < cap(h.broadcast); i++ {
		h.broadcast <- &MutationPayload{Type: "fill", WorkspaceID: "ws-1"}
	}
	h.retryQueue = []*MutationPayload{
		{Type: "queued-1", WorkspaceID: "ws-1"},
		{Type: "queued-2", WorkspaceID: "ws-1"},
	}
	h.drainRetryQueue()
	if got := h.GetRetryQueueDepth(); got != 2 {
		t.Fatalf("retry queue should remain full when broadcast is full, got %d", got)
	}

	h.removeClient(c1)
	h.removeClient(c2)
}

func TestEventIDForMutationBranches(t *testing.T) {
	if got := eventIDForMutation(&MutationPayload{Cursor: "cursor-1"}); got != "cursor-1" {
		t.Fatalf("cursor event id = %q", got)
	}
	if got := eventIDForMutation(nil); got == "" {
		t.Fatal("nil mutation should still allocate an event id")
	}
	if got := eventIDForMutation(&MutationPayload{Timestamp: "not-a-time"}); got == "" {
		t.Fatal("invalid timestamp should allocate an event id")
	}

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	got := eventIDForMutation(&MutationPayload{Timestamp: future})
	if got == "" {
		t.Fatal("future timestamp should become an event id")
	}
}
