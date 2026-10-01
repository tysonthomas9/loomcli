//go:build daemon_bugreplay

package realtime

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	hub.RegisterClient(NewClient(1, ClientSendBuf, "0", nil, "ws-replay"))
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

func TestBugReplay640a(t *testing.T) {
	handler := NewHandler(HandlerConfig{GetMutationsSince: func(_, _ string) ([]rpc.MutationEvent, error) {
		return []rpc.MutationEvent{{Cursor: "2-0", Type: "update", IssueID: "other-repo", SourceRepo: "repo-b"}}, nil
	}})
	response := httptest.NewRecorder()
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(1, ClientSendBuf, "1-0", []string{"repo-a"}, "ws-replay")
	if err := handler.sendCatchUp(writer, client, "1-0", "ws-replay", client.sourceRepos); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "event: mutation\n") ||
		!strings.Contains(response.Body.String(), "id: 2-0\nevent: checkpoint\n") {
		t.Fatalf("filtered tail did not advance checkpoint without leaking payload: %s", response.Body.String())
	}
}

func TestBugReplay612a(t *testing.T) {
	hub := NewHub()
	client := NewClient(1, 1, "0", nil, "ws-replay")
	hub.RegisterClient(client)
	hub.fanOutMutation(&MutationPayload{Cursor: "1-0", Type: "update", WorkspaceID: "ws-replay"})
	hub.fanOutMutation(&MutationPayload{Cursor: "2-0", Type: "update", WorkspaceID: "ws-replay"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response := &cancelOnResyncWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Handler{}).streamLoop(writer, client, ctx); err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Body.String(), "event: resync\n") {
		t.Fatalf("full client buffer ended without a resync frame: %s", response.Body.String())
	}
}

type cancelOnResyncWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelOnResyncWriter) Write(frame []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(frame)
	if strings.Contains(string(frame), "event: resync\n") {
		w.cancel()
	}
	return n, err
}

func TestBugReplay644(t *testing.T) {
	const resumeCursor = "2-0"
	handler := NewHandler(HandlerConfig{GetMutationsSince: func(_, since string) ([]rpc.MutationEvent, error) {
		if since != resumeCursor {
			t.Fatalf("catch-up used %q, want %q", since, resumeCursor)
		}
		return nil, nil
	}})
	client := NewClient(1, ClientSendBuf, resumeCursor, nil, "ws-replay")
	response := httptest.NewRecorder()
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.sendCatchUp(writer, client, resumeCursor, "ws-replay", nil); err != nil {
		t.Fatal(err)
	}
	client.send <- &MutationPayload{Cursor: "1-0", Type: "update", WorkspaceID: "ws-replay"}
	close(client.send)
	if _, err := handler.streamLoop(writer, client, context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "id: 1-0\n") {
		t.Fatalf("stale live frame moved resume cursor behind %s: %s", resumeCursor, response.Body.String())
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

// stalledResponse models a browser connection whose TCP window never opens:
// Write blocks until a write deadline passes, then fails like net.Conn does.
type stalledResponse struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	deadline time.Time
	release  chan struct{}
}

func (w *stalledResponse) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	w.deadline = deadline
	w.mu.Unlock()
	return nil
}

func (w *stalledResponse) Write([]byte) (int, error) {
	for {
		w.mu.Lock()
		deadline := w.deadline
		w.mu.Unlock()
		if !deadline.IsZero() && time.Now().After(deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		select {
		case <-w.release:
			return 0, io.ErrClosedPipe
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestBugReplay612b(t *testing.T) {
	response := &stalledResponse{ResponseRecorder: httptest.NewRecorder(), release: make(chan struct{})}
	defer close(response.release)
	writer, err := NewWriter(response)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- writer.WriteEventNoID("mutation", `{"type":"update"}`) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stalled write reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("#612b: write to a stalled browser connection had no deadline and blocked the writer indefinitely")
	}
}

// TestBugReplay670 resumes with a cursor minted by an earlier FleetDB source
// incarnation. The restored source has only lower cursors, so an unbound
// resume value replays nothing and the browser is told it is connected.
func TestBugReplay670(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(_, since string) ([]rpc.MutationEvent, error) {
			if since != "5-0" {
				t.Fatalf("since = %q, want the browser's old-incarnation cursor 5-0", since)
			}
			return nil, nil // Restored log holds 1-0..3-0, all "before" 5-0.
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-replay" },
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "5-0")
	response := &cancelOnConnectedWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), "event: connected\n") {
		t.Fatalf("#670: resume cursor with no source identity was accepted; restored events 1-0..3-0 are skipped: %s", response.Body.String())
	}
}

func TestBugReplay672(t *testing.T) {
	handler := NewHandler(HandlerConfig{
		Hub: NewHub(),
		GetMutationsSince: func(string, string) ([]rpc.MutationEvent, error) {
			return nil, errors.New(`fleet mutations: 410 cursor_expired: cursor "1-0" is below retention floor`)
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-replay" },
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "1-0")
	response := &cancelOnConnectedWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Body.String(), "event: ") {
		t.Fatalf("#672: expired resume cursor ended the stream with no recovery frame; the browser retries the same cursor: %q", response.Body.String())
	}
}

type cancelOnConnectedWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *cancelOnConnectedWriter) Write(frame []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(frame)
	if strings.Contains(string(frame), "event: connected\n") {
		w.cancel()
	}
	return n, err
}
