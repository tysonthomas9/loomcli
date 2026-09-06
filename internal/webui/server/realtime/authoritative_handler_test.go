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
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.aGVhZA"}, nil
			}
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.bmV4dA"}, nil
		}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, since)
			if since == through {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: since}, nil
			}
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{{Cursor: "c2.bmV4dA", Type: "update", IssueID: "issue", Timestamp: time.Now()}}, Cursor: "c2.bmV4dA"}, nil
		}),
	})
	h.heartbeatInterval = time.Millisecond
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "", writer)
	for {
		select {
		case frame := <-writer.written:
			if frame.id == "c2.bmV4dA" {
				cancel()
				<-done
				mu.Lock()
				defer mu.Unlock()
				if len(calls) < 3 || calls[1] != "c2.aGVhZA" || calls[2] != "$" {
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
	h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, "c2.dHdv"), func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		calls++
		if calls == 1 {
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{{Cursor: "c2.b25l", Type: "update", IssueID: "c2.b25l", Timestamp: time.Now()}}, Cursor: "c2.b25l", HasMore: true}, nil
		}
		if since != "c2.b25l" {
			t.Errorf("second page starts %q", since)
		}
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{{Cursor: "c2.dHdv", Type: "update", IssueID: "c2.dHdv", Timestamp: time.Now()}}, Cursor: "c2.dHdv"}, nil
	})})
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "c2.c3RhcnQ", writer)
	select {
	case <-writer.connected:
	case <-time.After(time.Second):
		t.Fatal("missing connected")
	}
	cancel()
	<-done
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.frames) != 4 || writer.frames[0].id != "c2.b25l" || writer.frames[1].id != "c2.dHdv" || writer.frames[3].event != "connected" {
		t.Fatalf("frames %+v", writer.frames)
	}
}

func TestAuthoritativeHandlerSourceFailureNeverAdvancesCheckpoint(t *testing.T) {
	for _, sourceErr := range []error{errors.New("source failed"), backend.ErrMutationCursorExpired} {
		t.Run(sourceErr.Error(), func(t *testing.T) {
			h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(fixedReplayHead(t, "end"), func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, sourceErr
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
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.dHdv"}, nil
		}
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: "c2.c3RhcnQ"}, nil
	}, func(_ context.Context, _ string, since, through string, _ int) (backend.MutationPage, error) {
		mu.Lock()
		defer mu.Unlock()
		if !published || since == "c2.dHdv" {
			return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Cursor: since}, nil
		}
		if since != "c2.c3RhcnQ" {
			t.Errorf("read cursor from notification: %q", since)
		}
		return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ", Events: []backend.MutationData{{Cursor: "c2.b25l", Type: "update", IssueID: "c2.b25l", Timestamp: time.Now()}, {Cursor: "c2.dHdv", Type: "update", IssueID: "c2.dHdv", Timestamp: time.Now()}}, Cursor: "c2.dHdv"}, nil
	})})
	writer := newRecordingFrameWriter()
	cancel, done := runAuthoritativeHandler(t, h, "c2.c3RhcnQ", writer)
	select {
	case <-writer.connected:
	case <-time.After(time.Second):
		t.Fatal("missing connected")
	}
	mu.Lock()
	published = true
	mu.Unlock()
	h.hub.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c2.dHdv", IssueID: "forged-two"})
	h.hub.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c2.b25l", IssueID: "forged-one"})
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
				if ids[0] != "c2.b25l" || ids[1] != "c2.dHdv" {
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
		"empty": {}, "selector": {Cursor: "$"}, "more": {Cursor: "c2.aGVhZA", HasMore: true}, "events": {Cursor: "c2.aGVhZA", Events: []backend.MutationData{{Cursor: "c2.aGVhZA"}}},
	} {
		t.Run(name, func(t *testing.T) {
			page.SourceIdentity = "s1.Zml4dHVyZQ"
			h := NewHandler(HandlerConfig{Hub: NewHub(), WorkspaceFromCtx: func(context.Context) string { return "ws" }, OpenMutationSource: openFixtureMutationSource(func(context.Context, string, string, int) (backend.MutationPage, error) { return page, nil }, func(context.Context, string, string, string, int) (backend.MutationPage, error) {
				t.Error("malformed head reached bounded read")
				return backend.MutationPage{SourceIdentity: "s1.Zml4dHVyZQ"}, nil
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
