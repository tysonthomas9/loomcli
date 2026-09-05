package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// testWorkspaceID is the default workspace ID used in SSE live tests.
const testWorkspaceID = "test-ws"

// sseEvent represents a parsed SSE event from the wire.
type sseEvent struct {
	ID    string
	Event string
	Data  string
}

// sseTestClient wraps a real HTTP connection to an SSE endpoint, providing
// helpers to parse the SSE text protocol.
type sseTestClient struct {
	resp    *http.Response
	scanner *bufio.Scanner
}

// connectSSE opens a GET request to the workspace-scoped SSE endpoint and returns
// a client that can read parsed SSE events.
func connectSSE(t *testing.T, serverURL string, headers map[string]string) *sseTestClient {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, serverURL+"/api/workspaces/"+testWorkspaceID+"/events", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 0} // No timeout for SSE
	resp, err := client.Do(req)        //nolint:gosec // G704 — test hits local httptest server
	if err != nil {
		t.Fatalf("failed to connect to SSE endpoint: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// connectSSEForWorkspace connects to the SSE endpoint for a specific workspace.
func connectSSEForWorkspace(t *testing.T, serverURL, wsID string, headers map[string]string) *sseTestClient {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, serverURL+"/api/workspaces/"+wsID+"/events", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req) //nolint:gosec // G704 — test hits local httptest server
	if err != nil {
		t.Fatalf("failed to connect to SSE endpoint: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// connectSSEWithQuery connects with a query string (e.g. "?since=12345").
func connectSSEWithQuery(t *testing.T, serverURL, query string, headers map[string]string) *sseTestClient {
	t.Helper()

	url := serverURL + "/api/workspaces/" + testWorkspaceID + "/events"
	if query != "" {
		url += "?" + query
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to connect to SSE endpoint: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	return &sseTestClient{
		resp:    resp,
		scanner: bufio.NewScanner(resp.Body),
	}
}

// readEvent reads the next SSE event from the stream, blocking until an event
// is available or the timeout expires.
func (c *sseTestClient) readEvent(timeout time.Duration) (*sseEvent, error) {
	done := make(chan *sseEvent, 1)
	errCh := make(chan error, 1)

	go func() {
		evt := &sseEvent{}
		for c.scanner.Scan() {
			line := c.scanner.Text()
			if line == "" {
				// Empty line = end of event. Only emit if we collected data.
				if evt.Event != "" || evt.Data != "" || evt.ID != "" {
					done <- evt
					return
				}
				continue
			}
			// Skip SSE comments (lines starting with ':')
			if strings.HasPrefix(line, ":") {
				continue
			}
			// Skip retry: field (not an event)
			if strings.HasPrefix(line, "retry:") {
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				evt.ID = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "event: ") {
				evt.Event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				evt.Data = strings.TrimPrefix(line, "data: ")
			}
		}
		if err := c.scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- fmt.Errorf("SSE stream ended unexpectedly")
		}
	}()

	select {
	case evt := <-done:
		return evt, nil
	case err := <-errCh:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for SSE event after %v", timeout)
	}
}

// close closes the underlying HTTP response body.
func (c *sseTestClient) close() {
	c.resp.Body.Close()
}

// parseMutationPayload unmarshals a realtime.MutationPayload from SSE event data.
func parseMutationPayload(data string) (*realtime.MutationPayload, error) {
	var p realtime.MutationPayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// newLiveSSEServer creates an httptest.NewServer wired to realtime.NewHandler with the
// given hub and mutation-page callback. The caller must call server.Close()
// and hub.Stop() when done (use t.Cleanup).
func newLiveSSEServer(t *testing.T, hub *realtime.Hub, getMutationPage func(context.Context, string, string, int) (backend.MutationPage, error)) *httptest.Server {
	t.Helper()

	var getThrough func(context.Context, string, string, string, int) (backend.MutationPage, error)
	if getMutationPage != nil {
		getThrough = func(ctx context.Context, ws, since, through string, limit int) (backend.MutationPage, error) {
			return getMutationPage(ctx, ws, since, limit)
		}
	}
	handler := realtime.NewHandler(realtime.HandlerConfig{Hub: hub, GetMutationPage: getMutationPage, GetMutationPageThrough: getThrough, WorkspaceFromCtx: middleware.WorkspaceFromContext})
	wsMux := http.NewServeMux()
	wsMux.Handle("GET /api/workspaces/{ws}/events", handler)
	// Wrap with a simple workspace existence check that always passes in tests
	mux := http.NewServeMux()
	mux.Handle("/api/workspaces/{ws}/", middleware.Workspace(func(string) bool { return true })(wsMux))

	server := httptest.NewServer(mux)
	t.Cleanup(func() {
		server.Close()
	})
	return server
}

// TestSSELive_ConnectionAndHandshake verifies that connecting to the real SSE
// endpoint returns the correct HTTP headers, the retry field, and a connected
// event with a clientId.
func TestSSELive_ConnectionAndHandshake(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	client := connectSSE(t, server.URL, nil)
	defer client.close()

	// Verify response headers
	ct := client.resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", ct, "text/event-stream")
	}
	cc := client.resp.Header.Get("Cache-Control")
	if cc != "no-cache" {
		t.Errorf("Cache-Control: got %q, want %q", cc, "no-cache")
	}
	conn := client.resp.Header.Get("Connection")
	if conn != "keep-alive" {
		t.Errorf("Connection: got %q, want %q", conn, "keep-alive")
	}
	xab := client.resp.Header.Get("X-Accel-Buffering")
	if xab != "no" {
		t.Errorf("X-Accel-Buffering: got %q, want %q", xab, "no")
	}

	// First real event should be 'connected'
	evt, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}
	if evt.Event != "connected" {
		t.Errorf("expected event type 'connected', got %q", evt.Event)
	}
	if !strings.Contains(evt.Data, `"clientId":`) {
		t.Errorf("connected event missing clientId: %s", evt.Data)
	}
}

// TestSSELive_MutationDelivery connects a client, broadcasts a create mutation
// via hub.Broadcast(), and verifies the client receives it with correct fields.
func TestSSELive_MutationDelivery(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	committed := make(chan struct{})
	server := newLiveSSEServer(t, hub, func(_ context.Context, _ string, since string, _ int) (backend.MutationPage, error) {
		if since == "$" {
			select {
			case <-committed:
				return backend.MutationPage{Cursor: "c1.live-durable"}, nil
			default:
				return backend.MutationPage{Cursor: "c1.start"}, nil
			}
		}
		if since == "c1.live-durable" {
			return backend.MutationPage{Cursor: since}, nil
		}
		select {
		case <-committed:
			return backend.MutationPage{Events: []backend.MutationData{{Type: "create", IssueID: "loom-live-1", Cursor: "c1.live-durable", Title: "Live Test Issue", Timestamp: time.Now().UTC()}}, Cursor: "c1.live-durable"}, nil
		default:
			return backend.MutationPage{Cursor: since}, nil
		}
	})

	client := connectSSE(t, server.URL, map[string]string{"Last-Event-ID": "c1.start"})
	defer client.close()

	// Read and discard the connected event
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	// Wait for client to be registered in hub
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() < 1 {
		t.Fatal("client not registered in hub")
	}

	// Commit the authoritative source, then send a deliberately stale wakeup payload.
	close(committed)
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "create",
		IssueID:     "loom-live-1",
		Cursor:      "c1.live-durable",
		Title:       "untrusted notification title",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: testWorkspaceID,
	})

	// Read mutation event
	evt, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read mutation event: %v", err)
	}
	if evt.Event != "mutation" {
		t.Errorf("expected event type 'mutation', got %q", evt.Event)
	}
	if evt.ID != "c1.live-durable" {
		t.Errorf("expected durable cursor, got %q", evt.ID)
	}

	payload, err := parseMutationPayload(evt.Data)
	if err != nil {
		t.Fatalf("failed to parse mutation payload: %v", err)
	}
	if payload.Type != "create" {
		t.Errorf("expected type 'create', got %q", payload.Type)
	}
	if payload.IssueID != "loom-live-1" {
		t.Errorf("expected issue_id 'loom-live-1', got %q", payload.IssueID)
	}
	if payload.Title != "Live Test Issue" {
		t.Errorf("expected title 'Live Test Issue', got %q", payload.Title)
	}
	if payload.Timestamp == "" {
		t.Error("expected timestamp to be set")
	}
}

// TestSSELive_MultipleClients connects 3 clients, broadcasts a mutation, and
// verifies all 3 receive it.
func TestSSELive_MultipleClients(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	const numClients = 3
	clients := make([]*sseTestClient, numClients)
	for i := 0; i < numClients; i++ {
		clients[i] = connectSSE(t, server.URL, nil)
		defer clients[i].close()

		// Consume the connected event
		_, err := clients[i].readEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("client %d: failed to read connected event: %v", i, err)
		}
	}

	// Wait for all clients to be registered
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < numClients && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() < numClients {
		t.Fatalf("expected %d clients, got %d", numClients, hub.ClientCount())
	}

	// Broadcast
	hub.Broadcast(&realtime.MutationPayload{
		Type:        "create",
		IssueID:     "loom-multi-1",
		Title:       "Multi-client Test",
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		WorkspaceID: testWorkspaceID,
	})

	// Verify all clients receive it
	for i, c := range clients {
		evt, err := c.readEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("client %d: failed to read mutation: %v", i, err)
		}
		if evt.Event != "mutation" {
			t.Errorf("client %d: expected 'mutation', got %q", i, evt.Event)
		}
		payload, err := parseMutationPayload(evt.Data)
		if err != nil {
			t.Fatalf("client %d: failed to parse payload: %v", i, err)
		}
		if payload.IssueID != "loom-multi-1" {
			t.Errorf("client %d: expected issue_id 'loom-multi-1', got %q", i, payload.IssueID)
		}
	}
}

// TestSSELive_MultipleMutationTypes broadcasts create, update, status, and
// delete mutations sequentially and verifies the client receives all in order
// with correct types.
func TestSSELive_MultipleMutationTypes(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	client := connectSSE(t, server.URL, nil)
	defer client.close()

	// Consume connected event
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	// Wait for registration
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	mutations := []realtime.MutationPayload{
		{Type: "create", IssueID: "loom-types-1", Title: "Created", Timestamp: time.Now().UTC().Format(time.RFC3339), WorkspaceID: testWorkspaceID},
		{Type: "update", IssueID: "loom-types-1", Title: "Updated", Timestamp: time.Now().UTC().Format(time.RFC3339), WorkspaceID: testWorkspaceID},
		{Type: "status", IssueID: "loom-types-1", OldStatus: "open", NewStatus: "in_progress", Timestamp: time.Now().UTC().Format(time.RFC3339), WorkspaceID: testWorkspaceID},
		{Type: "delete", IssueID: "loom-types-1", Timestamp: time.Now().UTC().Format(time.RFC3339), WorkspaceID: testWorkspaceID},
	}

	for i := range mutations {
		hub.Broadcast(&mutations[i])
	}

	// Read all 4 mutation events
	for i, expected := range mutations {
		evt, err := client.readEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("mutation %d: failed to read: %v", i, err)
		}
		if evt.Event != "mutation" {
			t.Errorf("mutation %d: expected event 'mutation', got %q", i, evt.Event)
		}
		payload, err := parseMutationPayload(evt.Data)
		if err != nil {
			t.Fatalf("mutation %d: failed to parse: %v", i, err)
		}
		if payload.Type != expected.Type {
			t.Errorf("mutation %d: expected type %q, got %q", i, expected.Type, payload.Type)
		}
		if payload.IssueID != expected.IssueID {
			t.Errorf("mutation %d: expected issue_id %q, got %q", i, expected.IssueID, payload.IssueID)
		}
	}
}

// TestSSELive_CatchUpOnReconnect configures getMutationsSince to return stored
// mutations, connects with ?since= parameter, and verifies catch-up events
// arrive before the connected event's regular stream.
func TestSSELive_CatchUpOnReconnect(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	catchUpEvents := []backend.MutationData{
		{
			Type:      "create",
			IssueID:   "loom-catchup-1",
			Cursor:    "c1.first",
			Title:     "Catchup Issue 1",
			Timestamp: time.Now().UTC().Add(-2 * time.Minute),
		},
		{
			Type:      "update",
			IssueID:   "loom-catchup-2",
			Cursor:    "c1.last",
			Title:     "Catchup Issue 2",
			Timestamp: time.Now().UTC().Add(-1 * time.Minute),
		},
	}

	getMutationPage := func(_ context.Context, _ string, since string, _ int) (backend.MutationPage, error) {
		if since == "$" {
			return backend.MutationPage{Cursor: "c1.last"}, nil
		}
		if since == "c1.last" {
			return backend.MutationPage{Cursor: since}, nil
		}
		return backend.MutationPage{Events: catchUpEvents, Cursor: "c1.last"}, nil
	}

	server := newLiveSSEServer(t, hub, getMutationPage)

	// Connect with since= to trigger catch-up
	client := connectSSEWithQuery(t, server.URL, "since=1000", nil)
	defer client.close()

	// Should receive catch-up mutation events BEFORE the connected event.
	// The handler sends catch-up events, then retry:, then connected.
	evt1, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read first catch-up event: %v", err)
	}
	if evt1.Event != "mutation" {
		t.Errorf("expected first event to be 'mutation' (catch-up), got %q", evt1.Event)
	}
	p1, _ := parseMutationPayload(evt1.Data)
	if p1 == nil || p1.IssueID != "loom-catchup-1" {
		t.Errorf("expected catch-up issue 'loom-catchup-1', got %v", p1)
	}

	evt2, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read second catch-up event: %v", err)
	}
	if evt2.Event != "mutation" {
		t.Errorf("expected second event to be 'mutation' (catch-up), got %q", evt2.Event)
	}
	p2, _ := parseMutationPayload(evt2.Data)
	if p2 == nil || p2.IssueID != "loom-catchup-2" {
		t.Errorf("expected catch-up issue 'loom-catchup-2', got %v", p2)
	}

	// Then the connected event
	evt3, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}
	if evt3.Event != "connected" {
		t.Errorf("expected 'connected' after catch-up events, got %q", evt3.Event)
	}
}

// TestSSELive_LastEventIDHeader connects with Last-Event-ID header and verifies
// getMutationsSince is called with the correct since value.
func TestSSELive_LastEventIDHeader(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	var capturedSince string
	getMutationPage := func(_ context.Context, _ string, since string, _ int) (backend.MutationPage, error) {
		if since == "$" {
			return backend.MutationPage{Cursor: "9876543210"}, nil
		}
		capturedSince = since
		return backend.MutationPage{Events: []backend.MutationData{}, Cursor: since}, nil
	}

	server := newLiveSSEServer(t, hub, getMutationPage)

	client := connectSSE(t, server.URL, map[string]string{
		"Last-Event-ID": "9876543210",
	})
	defer client.close()

	// Read connected event to ensure the handler has fully processed
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	if capturedSince != "9876543210" {
		t.Errorf("expected getMutationsSince called with 9876543210, got %s", capturedSince)
	}
}

// TestSSELive_SourceCursorIDs preserves opaque durable cursors and omits
// IDs for interleaved transient notifications.
func TestSSELive_SourceCursorIDs(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	client := connectSSE(t, server.URL, nil)
	defer client.close()

	// Consume connected event
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	// Wait for registration
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	cursors := []string{"c1.z", "", "c1.a", "", "c1.next"}
	for i, cursor := range cursors {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "create",
			IssueID:     fmt.Sprintf("loom-cursor-%d", i),
			Cursor:      cursor,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: testWorkspaceID,
		})
	}

	for i, cursor := range cursors {
		evt, err := client.readEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("mutation %d: failed to read: %v", i, err)
		}
		if evt.Event != "mutation" || evt.ID != cursor {
			t.Errorf("mutation %d: event=%q id=%q, want mutation with id=%q", i, evt.Event, evt.ID, cursor)
		}
	}
}

// TestSSELive_ClientDisconnect connects a client, disconnects (closes body),
// and verifies the hub client count drops back to 0.
func TestSSELive_ClientDisconnect(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	client := connectSSE(t, server.URL, nil)

	// Read connected event
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	// Wait for registration
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", hub.ClientCount())
	}

	// Disconnect
	client.close()

	// Wait for hub to detect disconnect and remove client
	deadline = time.Now().Add(3 * time.Second)
	for hub.ClientCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", hub.ClientCount())
	}
}

// TestSSELive_RetryFieldInStream verifies that the `retry: 5000` field appears
// in the raw SSE stream for a fresh (non-reconnecting) connection. The
// readEvent helper skips retry: lines, so this test reads raw bytes instead.
func TestSSELive_RetryFieldInStream(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	// Open a raw HTTP connection instead of using connectSSE, so we can
	// read the raw stream without the readEvent helper filtering retry:.
	resp, err := http.Get(server.URL + "/api/workspaces/" + testWorkspaceID + "/events") //nolint:gosec // G107 — test hits local httptest server
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)

	// Read lines until we see both "retry:" and "event: connected", or timeout.
	type result struct {
		retryLine   string
		retryFound  bool
		connectedAt int // line index where "event: connected" appeared
		retryAt     int // line index where "retry:" appeared
	}

	resCh := make(chan result, 1)
	go func() {
		var res result
		lineIdx := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "retry:") {
				res.retryLine = line
				res.retryAt = lineIdx
				res.retryFound = true
			}
			if line == "event: connected" {
				res.connectedAt = lineIdx
				// We have both pieces of info we need
				resCh <- res
				return
			}
			lineIdx++
		}
		// Stream ended before we found both
		resCh <- res
	}()

	select {
	case res := <-resCh:
		expectedRetry := fmt.Sprintf("retry: %d", realtime.RetryMs)
		if res.retryLine != expectedRetry {
			t.Errorf("expected %q in raw stream, got %q", expectedRetry, res.retryLine)
		}
		if res.retryFound && res.retryAt >= res.connectedAt {
			t.Errorf("expected retry: (line %d) to appear before event: connected (line %d)",
				res.retryAt, res.connectedAt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for retry field and connected event in raw stream")
	}
}
