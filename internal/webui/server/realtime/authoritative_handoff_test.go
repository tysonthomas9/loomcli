package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestAuthoritativeWakeDuringFinalEmptyReadTriggersNextRead(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()
	writer := newRecordingFrameWriter()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	h := NewHandler(HandlerConfig{
		Hub:              hub,
		WorkspaceFromCtx: func(context.Context) string { return "ws" },
		GetMutationPage: func(_ context.Context, _ string, since string, limit int) (backend.MutationPage, error) {
			if since != "$" || limit != 1 {
				t.Errorf("invalid head query")
			}
			if calls.Load() == 0 {
				return backend.MutationPage{Cursor: "c1.start"}, nil
			}
			return backend.MutationPage{Cursor: "c1.committed"}, nil
		},
		GetMutationPageThrough: func(ctx context.Context, ws, since, through string, limit int) (backend.MutationPage, error) {
			if calls.Add(1) == 1 {
				if since != "c1.start" {
					t.Errorf("initial cursor = %q", since)
				}
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return backend.MutationPage{}, ctx.Err()
				}
				// Snapshot was empty before the concurrent commit/wakeup.
				return backend.MutationPage{Cursor: "c1.start"}, nil
			}
			if since == "c1.start" {
				return backend.MutationPage{Cursor: "c1.committed", Events: []backend.MutationData{{Cursor: "c1.committed", Type: "update", IssueID: "source-event"}}}, nil
			}
			return backend.MutationPage{Cursor: since}, nil
		},
	})
	h.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "c1.start")
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), request) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not enter")
	}
	// This payload is deliberately unrelated to the authoritative event. Its ID
	// must not reach the wire; it only wakes a read from the preserved checkpoint.
	hub.Broadcast(&MutationPayload{WorkspaceID: "ws", Cursor: "c1.untrusted-notification", IssueID: "wrong-event"})
	close(release)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case frame := <-writer.written:
			if frame.id == "c1.untrusted-notification" {
				t.Fatal("notification became checkpoint")
			}
			if frame.event == "mutation" {
				if frame.id != "c1.committed" {
					t.Fatalf("source id=%q", frame.id)
				}
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("handler did not exit")
				}
				return
			}
		case <-deadline:
			t.Fatal("wake during empty read was lost")
		}
	}
}
