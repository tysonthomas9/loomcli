package webui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

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

// connectSSE opens a GET request to the given SSE endpoint URL and returns
// a client that can read parsed SSE events.
func connectSSE(t *testing.T, serverURL string, headers map[string]string) *sseTestClient {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, serverURL+"/api/events", nil)
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

// connectSSEWithQuery connects with a query string (e.g. "?since=12345").
func connectSSEWithQuery(t *testing.T, serverURL, query string, headers map[string]string) *sseTestClient {
	t.Helper()

	url := serverURL + "/api/events"
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

// parseMutationPayload unmarshals a MutationPayload from SSE event data.
func parseMutationPayload(data string) (*MutationPayload, error) {
	var p MutationPayload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// newLiveSSEServer creates an httptest.NewServer wired to handleSSE with the
// given hub and getMutationsSince callback. The caller must call server.Close()
// and hub.Stop() when done (use t.Cleanup).
func newLiveSSEServer(t *testing.T, hub *SSEHub, getMutationsSince func(since int64) []rpc.MutationEvent) *httptest.Server {
	t.Helper()

	handler := handleSSE(hub, getMutationsSince)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", handler)

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
	hub := NewSSEHub()
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
	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	server := newLiveSSEServer(t, hub, nil)

	client := connectSSE(t, server.URL, nil)
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

	// Broadcast a mutation
	hub.Broadcast(&MutationPayload{
		Type:      "create",
		IssueID:   "bd-live-1",
		Title:     "Live Test Issue",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	// Read mutation event
	evt, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read mutation event: %v", err)
	}
	if evt.Event != "mutation" {
		t.Errorf("expected event type 'mutation', got %q", evt.Event)
	}
	if evt.ID == "" {
		t.Error("expected event to have an id")
	}

	payload, err := parseMutationPayload(evt.Data)
	if err != nil {
		t.Fatalf("failed to parse mutation payload: %v", err)
	}
	if payload.Type != "create" {
		t.Errorf("expected type 'create', got %q", payload.Type)
	}
	if payload.IssueID != "bd-live-1" {
		t.Errorf("expected issue_id 'bd-live-1', got %q", payload.IssueID)
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
	hub := NewSSEHub()
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
	hub.Broadcast(&MutationPayload{
		Type:      "create",
		IssueID:   "bd-multi-1",
		Title:     "Multi-client Test",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
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
		if payload.IssueID != "bd-multi-1" {
			t.Errorf("client %d: expected issue_id 'bd-multi-1', got %q", i, payload.IssueID)
		}
	}
}

// TestSSELive_MultipleMutationTypes broadcasts create, update, status, and
// delete mutations sequentially and verifies the client receives all in order
// with correct types.
func TestSSELive_MultipleMutationTypes(t *testing.T) {
	hub := NewSSEHub()
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

	mutations := []MutationPayload{
		{Type: "create", IssueID: "bd-types-1", Title: "Created", Timestamp: time.Now().UTC().Format(time.RFC3339)},
		{Type: "update", IssueID: "bd-types-1", Title: "Updated", Timestamp: time.Now().UTC().Format(time.RFC3339)},
		{Type: "status", IssueID: "bd-types-1", OldStatus: "open", NewStatus: "in_progress", Timestamp: time.Now().UTC().Format(time.RFC3339)},
		{Type: "delete", IssueID: "bd-types-1", Timestamp: time.Now().UTC().Format(time.RFC3339)},
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
	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	catchUpEvents := []rpc.MutationEvent{
		{
			Type:      "create",
			IssueID:   "bd-catchup-1",
			Title:     "Catchup Issue 1",
			Timestamp: time.Now().UTC().Add(-2 * time.Minute),
		},
		{
			Type:      "update",
			IssueID:   "bd-catchup-2",
			Title:     "Catchup Issue 2",
			Timestamp: time.Now().UTC().Add(-1 * time.Minute),
		},
	}

	getMutationsSince := func(since int64) []rpc.MutationEvent {
		return catchUpEvents
	}

	server := newLiveSSEServer(t, hub, getMutationsSince)

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
	if p1 == nil || p1.IssueID != "bd-catchup-1" {
		t.Errorf("expected catch-up issue 'bd-catchup-1', got %v", p1)
	}

	evt2, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read second catch-up event: %v", err)
	}
	if evt2.Event != "mutation" {
		t.Errorf("expected second event to be 'mutation' (catch-up), got %q", evt2.Event)
	}
	p2, _ := parseMutationPayload(evt2.Data)
	if p2 == nil || p2.IssueID != "bd-catchup-2" {
		t.Errorf("expected catch-up issue 'bd-catchup-2', got %v", p2)
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
	hub := NewSSEHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	var capturedSince int64
	getMutationsSince := func(since int64) []rpc.MutationEvent {
		capturedSince = since
		return nil
	}

	server := newLiveSSEServer(t, hub, getMutationsSince)

	client := connectSSE(t, server.URL, map[string]string{
		"Last-Event-ID": "9876543210",
	})
	defer client.close()

	// Read connected event to ensure the handler has fully processed
	_, err := client.readEvent(3 * time.Second)
	if err != nil {
		t.Fatalf("failed to read connected event: %v", err)
	}

	if capturedSince != 9876543210 {
		t.Errorf("expected getMutationsSince called with 9876543210, got %d", capturedSince)
	}
}

// TestSSELive_MonotonicEventIDs sends multiple mutations and verifies each
// event's id field is strictly greater than the previous.
func TestSSELive_MonotonicEventIDs(t *testing.T) {
	hub := NewSSEHub()
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

	const numMutations = 5
	for i := 0; i < numMutations; i++ {
		hub.Broadcast(&MutationPayload{
			Type:      "create",
			IssueID:   fmt.Sprintf("bd-mono-%d", i),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}

	var prevID int64
	for i := 0; i < numMutations; i++ {
		evt, err := client.readEvent(3 * time.Second)
		if err != nil {
			t.Fatalf("mutation %d: failed to read: %v", i, err)
		}
		id, err := strconv.ParseInt(evt.ID, 10, 64)
		if err != nil {
			t.Fatalf("mutation %d: failed to parse id %q: %v", i, evt.ID, err)
		}
		if i > 0 && id <= prevID {
			t.Errorf("mutation %d: id %d not greater than previous %d", i, id, prevID)
		}
		prevID = id
	}
}

// TestSSELive_ClientDisconnect connects a client, disconnects (closes body),
// and verifies the hub client count drops back to 0.
func TestSSELive_ClientDisconnect(t *testing.T) {
	hub := NewSSEHub()
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
