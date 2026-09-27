//go:build daemon_bugreplay

package realtime

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func TestBugReplay626b(t *testing.T) {
	hub := NewHub()
	client := NewClient(1, ClientSendBuf, "0", nil, "ws-replay")
	hub.RegisterClient(client)
	if hub.ClientCount() != 1 {
		t.Fatal("registration must be complete before the catch-up read starts")
	}
	hub.fanOutMutation(&MutationPayload{Cursor: "1-0", Type: "update", WorkspaceID: "ws-replay"})
	select {
	case got := <-client.send:
		if got.Cursor != "1-0" {
			t.Fatalf("live cursor = %q, want 1-0", got.Cursor)
		}
	default:
		t.Fatal("mutation committed after catch-up read was lost before hub registration")
	}
}

func TestBugReplay626c(t *testing.T) {
	const cursor = "1-0"
	client := NewClient(1, ClientSendBuf, "0", nil, "ws-replay")
	handler := NewHandler(HandlerConfig{GetMutationsSince: func(_, _ string) ([]rpc.MutationEvent, error) {
		return []rpc.MutationEvent{{Cursor: cursor, Type: "update", IssueID: "issue-1"}}, nil
	}})
	response := httptest.NewRecorder()
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	client.send <- &MutationPayload{Cursor: cursor, Type: "update", WorkspaceID: "ws-replay"}
	close(client.send)
	if err := handler.sendCatchUp(writer, client, "0", "ws-replay", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.streamLoop(writer, client, context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(response.Body.String(), "event: mutation\n"); got != 1 {
		t.Fatalf("overlapping replay and live delivery wrote %d mutation frames, want one: %s", got, response.Body.String())
	}
}

func TestBugReplay626d(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(_, _ string) ([]rpc.MutationEvent, error) {
			return nil, errors.New("durable replay failed")
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-replay" },
	})
	request := httptest.NewRequest("GET", "/events?since=0", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), "event: connected\n") {
		t.Fatalf("connected was sent after failed replay: %s", response.Body.String())
	}
}

func TestBugReplay642(t *testing.T) {
	response := httptest.NewRecorder()
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSSEEvent(writer, &MutationPayload{Type: "refresh", WorkspaceID: "ws-replay"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "id: ") {
		t.Fatalf("cursorless notification advanced Last-Event-ID: %s", response.Body.String())
	}
}

func TestBugReplay643(t *testing.T) {
	hub := NewHub()
	for i := 0; i < cap(hub.broadcast); i++ {
		hub.Broadcast(&MutationPayload{Type: "update", IssueID: "filler", WorkspaceID: "ws-replay"})
	}
	hub.Broadcast(&MutationPayload{Type: "update", IssueID: "older", WorkspaceID: "ws-replay"})
	if len(hub.retryQueue) != 1 {
		t.Fatalf("older event was not queued for retry: depth %d", len(hub.retryQueue))
	}
	<-hub.broadcast // Make one fast slot available while older waits in retry.
	hub.Broadcast(&MutationPayload{Type: "update", IssueID: "newer", WorkspaceID: "ws-replay"})
	var order []string
	for len(hub.broadcast) > 0 {
		order = append(order, (<-hub.broadcast).IssueID)
	}
	hub.drainRetryQueue()
	if len(hub.retryQueue) != 0 {
		t.Fatalf("events remain in retry queue: depth %d", len(hub.retryQueue))
	}
	for len(hub.broadcast) > 0 {
		order = append(order, (<-hub.broadcast).IssueID)
	}
	if len(order) < 2 || order[len(order)-2] != "older" || order[len(order)-1] != "newer" {
		t.Fatalf("hub delivery ended %v, want older then newer", order[len(order)-min(2, len(order)):])
	}
}

// TestBugReplay626e holds a hub broadcast while the durable event is replayed,
// then releases it to a new connection resuming from that replayed cursor.
func TestBugReplay626e(t *testing.T) {
	const cursor = "1700000000100-0"
	const workspaceID = "ws-replay"
	event := rpc.MutationEvent{
		Cursor: cursor, Type: "update", IssueID: "issue-1",
		Timestamp: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
	hub := NewHub()
	handler := NewHandler(HandlerConfig{
		Hub: hub,
		GetMutationsSince: func(wsID, since string) ([]rpc.MutationEvent, error) {
			if wsID != workspaceID {
				t.Fatalf("workspace = %q, want %q", wsID, workspaceID)
			}
			if since == cursor {
				return nil, nil
			}
			if since != "0" {
				t.Fatalf("since = %q, want 0 or %q", since, cursor)
			}
			return []rpc.MutationEvent{event}, nil
		},
	})

	first := NewClient(1, ClientSendBuf, "0", nil, workspaceID)
	hub.RegisterClient(first)
	hub.Broadcast(&MutationPayload{
		Cursor: cursor, Type: "update", IssueID: "issue-1",
		WorkspaceID: workspaceID, Timestamp: event.Timestamp.Format(time.RFC3339Nano),
	}) // Queue in the hub; do not run its dequeue loop yet.
	firstResponse := httptest.NewRecorder()
	firstWriter, err := NewWriter(firstResponse)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.sendCatchUp(firstWriter, first, "0", workspaceID, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(firstResponse.Body.String(), "event: mutation\n"); got != 1 {
		t.Fatalf("first connection applied %d mutations, want one: %s", got, firstResponse.Body.String())
	}
	if !strings.Contains(firstResponse.Body.String(), "id: "+cursor+"\n") {
		t.Fatalf("first connection did not acknowledge durable cursor: %s", firstResponse.Body.String())
	}
	hub.removeClient(first)

	second := NewClient(2, ClientSendBuf, cursor, nil, workspaceID)
	hub.RegisterClient(second)
	secondResponse := httptest.NewRecorder()
	secondWriter, err := NewWriter(secondResponse)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.sendCatchUp(secondWriter, second, cursor, workspaceID, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(secondResponse.Body.String(), "event: mutation\n"); got != 0 {
		t.Fatalf("resuming from acknowledged cursor replayed %d mutations: %s", got, secondResponse.Body.String())
	}

	hub.fanOutMutation(<-hub.broadcast) // Release the queued event to the new client.
	close(second.send)                  // End streamLoop after it processes the queued event.
	if _, err := handler.streamLoop(secondWriter, second, context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(secondResponse.Body.String(), "event: mutation\n"); got != 0 {
		t.Fatalf("#626e: event already applied before reconnect was applied %d more times: %s", got, secondResponse.Body.String())
	}
}
