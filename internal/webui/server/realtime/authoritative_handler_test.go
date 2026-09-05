package realtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func runAuthoritativeHandler(t *testing.T, h *Handler, since string, writer *recordingFrameWriter) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	go h.hub.Run()
	t.Cleanup(h.hub.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	if since != "" {
		req.Header.Set("Last-Event-ID", since)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), req) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("handler did not stop")
		}
	})
	return cancel, done
}

func TestAuthoritativeHandlerFreshHeadAndPeriodicReconciliation(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" },
		OnAuthenticated: func(context.Context, string) (string, error) { return "stale-activation", nil },
		OpenMutationSource: openFixtureMutationSource(func(_ context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, since)
			if since != "$" || limit != 1 {
				t.Errorf("invalid head query")
			}
			if len(calls) == 1 {
				return backend.MutationPage{Cursor: "head"}, nil
			}
			return backend.MutationPage{Cursor: "next"}, nil
		}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, since)
			if since == through {
				return backend.MutationPage{Cursor: since}, nil
			}
			return backend.MutationPage{Events: []backend.MutationData{{Cursor: "next", Type: "update", IssueID: "issue", Timestamp: time.Now()}}, Cursor: "next"}, nil
		}),
	})
	h.heartbeatInterval = time.Millisecond
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "", writer)
	for {
		select {
		case frame := <-writer.written:
			if frame.id == "next" {
				cancel()
				<-done
				mu.Lock()
				defer mu.Unlock()
				if len(calls) < 3 || calls[1] != "head" || calls[2] != "$" {
					t.Fatalf("read chain %v", calls)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("periodic read did not recover missing wakeup")
		}
	}
}

func TestAuthoritativeHandlerPagesContinueBeforeConnected(t *testing.T) {
	calls := 0
	h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, "two"), func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		calls++
		if calls == 1 {
			return backend.MutationPage{Events: []backend.MutationData{{Cursor: "one", Type: "update", IssueID: "one", Timestamp: time.Now()}}, Cursor: "one", HasMore: true}, nil
		}
		if since != "one" {
			t.Errorf("second page starts %q", since)
		}
		return backend.MutationPage{Events: []backend.MutationData{{Cursor: "two", Type: "update", IssueID: "two", Timestamp: time.Now()}}, Cursor: "two"}, nil
	})})
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "start", writer)
	select {
	case <-writer.connected:
	case <-time.After(time.Second):
		t.Fatal("missing connected")
	}
	cancel()
	<-done
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.frames) != 4 || writer.frames[0].id != "one" || writer.frames[1].id != "two" || writer.frames[3].event != "connected" {
		t.Fatalf("frames %+v", writer.frames)
	}
}

func TestAuthoritativeHandlerSourceFailureNeverAdvancesCheckpoint(t *testing.T) {
	for _, sourceErr := range []error{errors.New("source failed"), backend.ErrMutationCursorExpired} {
		t.Run(sourceErr.Error(), func(t *testing.T) {
			h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, "end"), func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{}, sourceErr
			})})
			writer := newRecordingFrameWriter()
			_, done := runAuthoritativeHandler(t, h, "saved", writer)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("error did not close connection")
			}
			writer.mu.Lock()
			defer writer.mu.Unlock()
			if len(writer.frames) != 1 || writer.frames[0].id != "" || writer.frames[0].event != "resync" {
				t.Fatalf("frames %+v", writer.frames)
			}
		})
	}
}

func TestAuthoritativeHandlerReorderedNotificationsOnlyWakeSource(t *testing.T) {
	var mu sync.Mutex
	published := false
	h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(func(_ context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
		mu.Lock()
		defer mu.Unlock()
		if since != "$" || limit != 1 {
			t.Errorf("invalid head query")
		}
		if published {
			return backend.MutationPage{Cursor: "two"}, nil
		}
		return backend.MutationPage{Cursor: "start"}, nil
	}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		mu.Lock()
		defer mu.Unlock()
		if !published || since == "two" {
			return backend.MutationPage{Cursor: since}, nil
		}
		if since != "start" {
			t.Errorf("read cursor from notification: %q", since)
		}
		return backend.MutationPage{Events: []backend.MutationData{{Cursor: "one", Type: "update", IssueID: "one", Timestamp: time.Now()}, {Cursor: "two", Type: "update", IssueID: "two", Timestamp: time.Now()}}, Cursor: "two"}, nil
	})})
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "start", writer)
	select {
	case <-writer.connected:
	case <-time.After(time.Second):
		t.Fatal("missing connected")
	}
	mu.Lock()
	published = true
	mu.Unlock()
	h.hub.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "two", IssueID: "forged-two"})
	h.hub.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "one", IssueID: "forged-one"})
	var ids []string
	for {
		select {
		case frame := <-writer.written:
			if frame.event == "mutation" {
				ids = append(ids, frame.id)
			}
			if len(ids) == 2 {
				cancel()
				<-done
				if ids[0] != "one" || ids[1] != "two" {
					t.Fatalf("mutation order %v", ids)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("durable notifications did not wake reader")
		}
	}
}

func TestAuthoritativeHandlerRejectsMalformedFreshHead(t *testing.T) {
	for name, page := range map[string]backend.MutationPage{
		"empty": {}, "selector": {Cursor: "$"}, "more": {Cursor: "head", HasMore: true}, "events": {Cursor: "head", Events: []backend.MutationData{{Cursor: "head"}}},
	} {
		t.Run(name, func(t *testing.T) {
			h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) { return page, nil }, func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				t.Error("malformed head reached bounded read")
				return backend.MutationPage{}, nil
			})})
			writer := newRecordingFrameWriter()
			_, done := runAuthoritativeHandler(t, h, "", writer)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("invalid head did not close connection")
			}
			writer.mu.Lock()
			defer writer.mu.Unlock()
			if len(writer.frames) != 1 || writer.frames[0].id != "" || writer.frames[0].event != "resync" {
				t.Fatalf("frames %+v", writer.frames)
			}
		})
	}
}
