package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	backendfleet "github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

type recordedFrame struct {
	id    string
	event string
	data  string
}

type recordingFrameWriter struct {
	mu        sync.Mutex
	frames    []recordedFrame
	written   chan recordedFrame
	connected chan struct{}
	once      sync.Once
}

func newRecordingFrameWriter() *recordingFrameWriter {
	return &recordingFrameWriter{
		written:   make(chan recordedFrame, 32),
		connected: make(chan struct{}),
	}
}

func (w *recordingFrameWriter) WriteRetry(ms int) error {
	w.record(recordedFrame{event: "retry", data: string(rune(ms))})
	return nil
}

func (w *recordingFrameWriter) WriteEventID(id, event, data string) error {
	w.record(recordedFrame{id: id, event: event, data: data})
	return nil
}

func (w *recordingFrameWriter) WriteEventNoID(event, data string) error {
	w.record(recordedFrame{event: event, data: data})
	if event == "connected" {
		w.once.Do(func() { close(w.connected) })
	}
	return nil
}

func (w *recordingFrameWriter) WriteComment(text string) error {
	w.record(recordedFrame{event: "comment", data: text})
	return nil
}

func (w *recordingFrameWriter) record(frame recordedFrame) {
	w.mu.Lock()
	w.frames = append(w.frames, frame)
	w.mu.Unlock()
	w.written <- frame
}

func (w *recordingFrameWriter) snapshot() []recordedFrame {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedFrame(nil), w.frames...)
}

func TestHandler_RegistersBeforeActivationAndReturnsActivationErrors(t *testing.T) {
	t.Run("registration precedes activation", func(t *testing.T) {
		hub := NewHub()
		go hub.Run()
		t.Cleanup(hub.Stop)
		writer := newRecordingFrameWriter()
		h := NewHandler(HandlerConfig{
			Hub: hub,
			OnAuthenticated: func(context.Context, string) (string, error) {
				if got := hub.ClientCount(); got != 1 {
					t.Fatalf("client count during activation = %d, want 1", got)
				}
				return "c1.head", nil
			},
			WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
		})
		h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
		h.heartbeatInterval = time.Hour

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx))
			close(done)
		}()
		<-writer.connected
		cancel()
		<-done
	})

	t.Run("readiness error returns 503 before streaming", func(t *testing.T) {
		hub := NewHub()
		go hub.Run()
		t.Cleanup(hub.Stop)
		h := NewHandler(HandlerConfig{
			Hub: hub,
			OnAuthenticated: func(context.Context, string) (string, error) {
				return "", errors.New("head drain capped")
			},
			WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
		})
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events", nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rr.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body["error"] != "subscription_unavailable" {
			t.Fatalf("body = %q, want subscription_unavailable JSON", rr.Body.String())
		}
	})
}

func TestHandler_CatchUpPagesAndDeduplicatesEveryQueuedCursor(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	writer := newRecordingFrameWriter()
	var calls int
	h := NewHandler(HandlerConfig{
		Hub: hub,
		OnAuthenticated: func(context.Context, string) (string, error) {
			return "c1.head", nil
		},
		GetMutationPage: func(_ context.Context, wsID, since string, limit int) (backend.MutationPage, error) {
			if wsID != "ws-1" || limit != catchUpPageLimit {
				t.Fatalf("catch-up request workspace/limit = %q/%d", wsID, limit)
			}
			calls++
			switch calls {
			case 1:
				if since != "c1.start" {
					t.Fatalf("first since = %q, want c1.start", since)
				}
				return backend.MutationPage{
					Events:  []backend.MutationData{{Cursor: "c1.a", Type: backend.MutationUpdate, IssueID: "catch-a"}},
					Cursor:  "c1.a",
					HasMore: true,
				}, nil
			case 2:
				if since != "c1.a" {
					t.Fatalf("second since = %q, want c1.a", since)
				}
				for _, mutation := range []*MutationPayload{
					{Cursor: "c1.a", Type: backend.MutationUpdate, IssueID: "live-duplicate-a", WorkspaceID: "ws-1"},
					{Type: "terminal_session_change", IssueID: "interleaved-terminal", WorkspaceID: "ws-1"},
					{Cursor: "c1.b", Type: backend.MutationUpdate, IssueID: "live-duplicate-b", WorkspaceID: "ws-1"},
					{Cursor: "c1.live", Type: backend.MutationUpdate, IssueID: "live-sentinel", WorkspaceID: "ws-1"},
				} {
					hub.Broadcast(mutation)
				}
				return backend.MutationPage{
					Events: []backend.MutationData{{Cursor: "c1.b", Type: backend.MutationUpdate, IssueID: "catch-b"}},
					Cursor: "c1.b",
				}, nil
			default:
				return backend.MutationPage{}, errors.New("unexpected extra catch-up page")
			}
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events?since=c1.start", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()

	for {
		frame := <-writer.written
		if frame.id == "c1.live" {
			break
		}
	}
	cancel()
	<-done

	counts := make(map[string]int)
	for _, frame := range writer.snapshot() {
		if frame.event == "mutation" {
			counts[frame.id]++
		}
	}
	if counts["c1.a"] != 1 || counts["c1.b"] != 1 {
		t.Fatalf("deduplicated catch-up cursor counts = %v, want c1.a=1 c1.b=1", counts)
	}
	if counts["c1.live"] != 1 {
		t.Fatalf("live sentinel count = %d, want 1", counts["c1.live"])
	}
}

func TestHandler_CatchUpFailuresReturnResyncRequiredBeforeStreaming(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*Handler)
		page       func(context.Context, string, string, int) (backend.MutationPage, error)
		wantReason string
	}{
		{
			name: "page cap",
			configure: func(h *Handler) {
				h.catchUpMaxPages = 2
			},
			page: func(_ context.Context, _, since string, limit int) (backend.MutationPage, error) {
				if limit != catchUpPageLimit {
					t.Fatalf("limit = %d, want %d", limit, catchUpPageLimit)
				}
				return backend.MutationPage{Events: []backend.MutationData{}, Cursor: since + ".next", HasMore: true}, nil
			},
			wantReason: "cap",
		},
		{
			name: "backend error",
			page: func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{}, errors.New("unavailable")
			},
			wantReason: "error",
		},
		{
			name: "expired cursor",
			page: func(context.Context, string, string, int) (backend.MutationPage, error) {
				return backend.MutationPage{}, backend.NewBackendError(backend.KindValidation, "catchup", "expired", backend.ErrMutationCursorExpired)
			},
			wantReason: "expired",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewHub()
			go hub.Run()
			t.Cleanup(hub.Stop)
			h := NewHandler(HandlerConfig{
				Hub:              hub,
				OnAuthenticated:  func(context.Context, string) (string, error) { return "c1.head", nil },
				GetMutationPage:  tt.page,
				WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
			})
			if tt.configure != nil {
				tt.configure(h)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/events?since=c1.old", nil))
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rr.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rr.Body.String(), err)
			}
			if body["error"] != "resync_required" || body["reason"] != tt.wantReason {
				t.Fatalf("body = %v, want resync_required/%s", body, tt.wantReason)
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
		})
	}
}

func TestHandler_NumericHeaderLargerThanNumericQueryWinsCatchUp(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	var gotSince string
	h := NewHandler(HandlerConfig{
		Hub:             hub,
		OnAuthenticated: func(context.Context, string) (string, error) { return "1000", nil },
		GetMutationPage: func(_ context.Context, _ string, since string, _ int) (backend.MutationPage, error) {
			gotSince = since
			return backend.MutationPage{Events: []backend.MutationData{}, Cursor: since}, nil
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	writer := newRecordingFrameWriter()
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events?since=200", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", "300")
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-writer.connected
	cancel()
	<-done
	if gotSince != "300" {
		t.Fatalf("catch-up since = %q, want larger numeric header 300", gotSince)
	}
}

func TestHandler_NumericLastEventIDReachesFleetAsOpaqueCatchUpCursor(t *testing.T) {
	const (
		numericCursor = "1700000000000"
		opaqueCursor  = "c1.MTcwMDAwMDAwMDAwMC0w"
	)
	requestedSince := make(chan string, 1)
	fleetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		since := r.URL.Query().Get("since")
		requestedSince <- since
		w.Header().Set("Content-Type", "application/json")
		if since != opaqueCursor {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": "invalid_parameter", "message": "invalid since parameter: expected opaque cursor token",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events": []any{}, "cursor": opaqueCursor, "has_more": false,
		})
	}))
	t.Cleanup(fleetServer.Close)

	fleetBackend, err := backendfleet.New(backendfleet.Config{BaseURL: fleetServer.URL, WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("fleet.New: %v", err)
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	writer := newRecordingFrameWriter()
	h := NewHandler(HandlerConfig{
		Hub: hub,
		GetMutationPage: func(ctx context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			return fleetBackend.GetMutationsAfter(ctx, since, limit)
		},
		WorkspaceFromCtx: func(context.Context) string { return "ws-1" },
	})
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	h.heartbeatInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	req.Header.Set("Last-Event-ID", numericCursor)
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-writer.connected:
	case <-done:
		t.Fatalf("handler ended before streaming: status=%d body=%q", rr.Code, rr.Body.String())
	case <-time.After(time.Second):
		t.Fatal("handler did not complete the numeric-cursor catch-up handshake")
	}
	if got := <-requestedSince; got != opaqueCursor {
		t.Fatalf("Fleet since = %q, want %q", got, opaqueCursor)
	}
	if rr.Code == http.StatusServiceUnavailable {
		t.Fatalf("handler returned 503 for numeric Last-Event-ID: %q", rr.Body.String())
	}
	cancel()
	<-done
}
