package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
	backendfleet "github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// This is an HTTP adapter/handler proof with a deterministic Fleet HTTP fake,
// not a Fleet storage or deployed browser integration test.
func TestHandler_FleetHTTPReplay201BeforeConnectedWithQueuedOverlap(t *testing.T) {
	const workspace = "ws-replay"
	cursor := func(n int) string {
		return "c1." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("700-%d", n)))
	}
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	var calls atomic.Int32
	fleetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "c1.JA" {
			head := 201
			if calls.Load() >= 3 {
				head = 202
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": cursor(head), "has_more": false})
			return
		}
		page := int(calls.Add(1))
		start := (page - 1) * 100
		if page == 4 {
			start = 201
		}
		if page > 4 || r.URL.Query().Get("since") != cursor(start) || r.URL.Query().Get("limit") != "100" {
			t.Errorf("unexpected Fleet request page=%d path=%s query=%s", page, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected cursor/page", http.StatusBadRequest)
			return
		}
		wantFence := 201
		if page == 4 {
			wantFence = 202
		}
		if r.URL.Query().Get("through") != cursor(wantFence) {
			t.Errorf("wrong fixed replay fence %q", r.URL.Query().Get("through"))
		}
		end := min(start+100, 201)
		if page == 4 {
			end = 202
		}
		if page == 3 {
			for _, n := range []int{1, 201, 202} {
				hub.Broadcast(&MutationPayload{Cursor: cursor(n), Type: backend.MutationUpdate, IssueID: fmt.Sprintf("issue-%d", n), WorkspaceID: workspace})
			}
			// Replay has not returned: prove durable overlap has queued an authoritative read wakeup.
			require.Eventually(t, func() bool {
				hub.mu.RLock()
				defer hub.mu.RUnlock()
				for client := range hub.clients {
					if client.workspaceID == workspace && len(client.wake) == 1 {
						return true
					}
				}
				return false
			}, 2*time.Second, time.Millisecond)
		}
		events := make([]map[string]any, 0, end-start)
		for n := start + 1; n <= end; n++ {
			events = append(events, map[string]any{"id": cursor(n), "action": "issue.update", "entity_type": "issue", "entity_id": fmt.Sprintf("issue-%d", n), "timestamp": "2026-09-05T00:00:00Z", "after": `{"title":"replay"}`})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"events": events, "cursor": cursor(end), "has_more": end < 201}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(fleetServer.Close)
	fleetBackend, err := backendfleet.New(backendfleet.Config{BaseURL: fleetServer.URL, WorkspaceID: workspace})
	require.NoError(t, err)
	writer := newRecordingFrameWriter()
	writer.written = make(chan recordedFrame, 256)
	handler := NewHandler(HandlerConfig{Hub: hub, WorkspaceFromCtx: func(context.Context) string { return workspace }, GetMutationPage: func(ctx context.Context, ws, since string, limit int) (backend.MutationPage, error) {
		if ws != workspace {
			return backend.MutationPage{}, fmt.Errorf("wrong workspace %s", ws)
		}
		return fleetBackend.GetMutationsAfter(ctx, since, limit)
	}, GetMutationPageThrough: func(ctx context.Context, ws, since, through string, limit int) (backend.MutationPage, error) {
		if ws != workspace {
			return backend.MutationPage{}, fmt.Errorf("wrong workspace %s", ws)
		}
		return fleetBackend.GetMutationsThrough(ctx, since, through, limit)
	}})
	handler.writerFactory = func(http.ResponseWriter) (frameWriter, error) { return writer, nil }
	handler.heartbeatInterval = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", cursor(0))
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(httptest.NewRecorder(), request) }()
	sawSentinel := false
	for !sawSentinel {
		select {
		case frame := <-writer.written:
			sawSentinel = frame.event == "mutation" && frame.id == cursor(202)
		case <-ctx.Done():
			t.Fatal("timed out waiting for live sentinel")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop")
	}
	require.Equal(t, int32(4), calls.Load())
	next, barriers := 1, 0
	for _, frame := range writer.snapshot() {
		switch frame.event {
		case "resync":
			t.Fatalf("unexpected resync: %+v", frame)
		case "connected":
			require.Equal(t, 202, next, "all 201 replay records must precede connected")
			barriers++
		case "mutation":
			require.Equal(t, cursor(next), frame.id, "ordered unique durable IDs, including both overlap duplicates")
			if next <= 201 {
				require.Zero(t, barriers, "replay arrived after handshake")
			} else {
				require.Equal(t, 1, barriers, "live sentinel arrived before handshake")
			}
			next++
		}
	}
	require.Equal(t, 203, next)
	require.Equal(t, 1, barriers)
}
