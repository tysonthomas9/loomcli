package flue

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
)

func sseServer(t *testing.T, frames string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("missing SSE accept header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, frames)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func collect(t *testing.T, h execplane.StreamHandle) []execplane.Event {
	t.Helper()
	var out []execplane.Event
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e, ok := <-h.Events():
			if !ok {
				return out
			}
			out = append(out, e)
		case <-timeout:
			t.Fatal("stream did not close")
		}
	}
}

func TestInvoke_ParsesEventStream(t *testing.T) {
	t.Parallel()
	frames := "event: agent_start\nid: 0\ndata: {\"type\":\"agent_start\",\"instanceId\":\"EPIC-1\"}\n\n" +
		": heartbeat\n\n" +
		"event: text_delta\nid: 1\ndata: {\"type\":\"text_delta\",\"text\":\"hello\"}\n\n" +
		"event: tool_call\nid: 2\ndata: {\"type\":\"tool_call\",\"toolName\":\"advance_epic\",\"isError\":false}\n\n" +
		"event: idle\nid: 3\ndata: {\"type\":\"idle\"}\n\n"
	c := sseServer(t, frames)

	h, err := c.Invoke(context.Background(), "epic-runner", "EPIC-1", execplane.InvokeRequest{Message: "advance"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	events := collect(t, h)
	if len(events) != 4 {
		t.Fatalf("events: %+v", events)
	}
	want := []string{"agent_start", "text_delta", "tool_call", "idle"}
	for i, w := range want {
		if events[i].Type != w {
			t.Errorf("event[%d] = %s, want %s", i, events[i].Type, w)
		}
	}
	if !events[3].IsTerminal() {
		t.Error("idle should be terminal")
	}
	if h.Err() != nil {
		t.Errorf("Err: %v", h.Err())
	}
}

func TestInvoke_ErrorFrame(t *testing.T) {
	t.Parallel()
	frames := "event: error\nid: 0\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request\",\"message\":\"boom\"}}\n\n"
	c := sseServer(t, frames)
	h, err := c.Invoke(context.Background(), "epic-runner", "EPIC-1", execplane.InvokeRequest{Message: "advance"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	events := collect(t, h)
	if len(events) != 1 || events[0].Type != execplane.EventError {
		t.Fatalf("events: %+v", events)
	}
	if msg := events[0].ErrorMessage(); msg != "boom" {
		t.Errorf("ErrorMessage = %q", msg)
	}
}

func TestInvoke_TruncatedStreamSetsErrNotTerminal(t *testing.T) {
	t.Parallel()
	// Stream ends (EOF) without a terminal event — events close, no
	// terminal frame seen, Err is nil (clean EOF) but caller sees no
	// idle; the reconciler treats that as an interrupted run.
	frames := "event: agent_start\ndata: {\"type\":\"agent_start\"}\n\n"
	c := sseServer(t, frames)
	h, err := c.Invoke(context.Background(), "epic-runner", "EPIC-1", execplane.InvokeRequest{Message: "advance"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	events := collect(t, h)
	if len(events) != 1 || events[0].IsTerminal() {
		t.Fatalf("events: %+v", events)
	}
}

func TestInvoke_Non200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"type":"not_found","message":"no such agent"}}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c, _ := New(Config{BaseURL: srv.URL})
	if _, err := c.Invoke(context.Background(), "nope", "x", execplane.InvokeRequest{}); err == nil {
		t.Fatal("want error for non-200")
	}
}

func TestHealthy(t *testing.T) {
	t.Parallel()
	c := sseServer(t, "")
	if err := c.Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	bad, _ := New(Config{BaseURL: "http://127.0.0.1:1"})
	if err := bad.Healthy(context.Background()); err == nil {
		t.Fatal("want error for unreachable plane")
	}
}

func TestCancelClosesStream(t *testing.T) {
	t.Parallel()
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = fmt.Fprint(w, "event: agent_start\ndata: {\"type\":\"agent_start\"}\n\n")
		w.(http.Flusher).Flush()
		<-blocked // hold the stream open
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })
	c, _ := New(Config{BaseURL: srv.URL})

	h, err := c.Invoke(context.Background(), "epic-runner", "EPIC-1", execplane.InvokeRequest{Message: "advance"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	select {
	case e := <-h.Events():
		if e.Type != "agent_start" {
			t.Fatalf("first event: %+v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first event")
	}
	h.Cancel()
	select {
	case _, ok := <-h.Events():
		if ok {
			// A buffered event may still arrive; drain to close.
			for range h.Events() { //nolint:revive
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close after Cancel")
	}
}
