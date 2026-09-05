package subscription

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
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

type scriptedCursorBackend struct {
	*fakeBackend
	probeFn    func(context.Context) (string, bool, error)
	getPageFn  func(context.Context, string, int) (backend.MutationPage, error)
	waitPageFn func(context.Context, string, int64, int) (backend.MutationPage, error)
}

func newScriptedCursorBackend() *scriptedCursorBackend {
	return &scriptedCursorBackend{fakeBackend: newFakeBackend()}
}

func (b *scriptedCursorBackend) ProbeHead(ctx context.Context) (string, bool, error) {
	if b.probeFn != nil {
		return b.probeFn(ctx)
	}
	return "c1.head", true, nil
}

func (b *scriptedCursorBackend) GetMutationsAfter(ctx context.Context, since string, limit int) (backend.MutationPage, error) {
	if b.getPageFn != nil {
		return b.getPageFn(ctx, since, limit)
	}
	return backend.MutationPage{Events: []backend.MutationData{}, Cursor: since}, nil
}

func (b *scriptedCursorBackend) WaitForMutationsAfter(ctx context.Context, since string, timeoutMs int64, limit int) (backend.MutationPage, error) {
	if b.waitPageFn != nil {
		return b.waitPageFn(ctx, since, timeoutMs, limit)
	}
	<-ctx.Done()
	return backend.MutationPage{}, ctx.Err()
}

func TestBackendMutationSubscriber_StartReturnsBeforeSupportedProbeAndReadyReturnsHead(t *testing.T) {
	hub := realtime.NewHub()
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	waitStarted := make(chan string, 1)
	b := newScriptedCursorBackend()
	b.probeFn = func(context.Context) (string, bool, error) {
		close(probeStarted)
		<-releaseProbe
		return "c1.head", true, nil
	}
	b.waitPageFn = func(ctx context.Context, since string, timeoutMs int64, limit int) (backend.MutationPage, error) {
		if timeoutMs != int64(backendWaitTimeout/time.Millisecond) || limit != mutationPageLimit {
			t.Errorf("wait timeout/limit = %d/%d, want %d/%d", timeoutMs, limit, backendWaitTimeout/time.Millisecond, mutationPageLimit)
		}
		waitStarted <- since
		<-ctx.Done()
		return backend.MutationPage{}, ctx.Err()
	}
	sub := NewBackendMutationSubscriber(b, hub, "ws-test")
	t.Cleanup(sub.Stop)

	sub.Start()
	<-probeStarted
	close(releaseProbe)

	head, err := sub.Ready(context.Background())
	if err != nil || head != "c1.head" {
		t.Fatalf("Ready = (%q, %v), want (c1.head, nil)", head, err)
	}
	if since := <-waitStarted; since != "c1.head" {
		t.Fatalf("first live wait since = %q, want c1.head", since)
	}
}

func TestBackendMutationSubscriber_UnsupportedProbeDrainsPagesWithoutLosingEmptyTerminalCursor(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)
	client := realtime.NewClient(1, 1, "0", nil, "ws-test")
	if err := hub.RegisterClient(context.Background(), client); err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	b := newScriptedCursorBackend()
	b.probeFn = func(context.Context) (string, bool, error) { return "", false, nil }
	var (
		mu       sync.Mutex
		requests []string
	)
	b.getPageFn = func(_ context.Context, since string, limit int) (backend.MutationPage, error) {
		if limit != mutationPageLimit {
			t.Fatalf("limit = %d, want %d", limit, mutationPageLimit)
		}
		mu.Lock()
		requests = append(requests, since)
		mu.Unlock()
		switch since {
		case "c1.previous":
			return backend.MutationPage{
				Events:  []backend.MutationData{{Cursor: "c1.event", Type: backend.MutationUpdate}},
				Cursor:  "c1.next",
				HasMore: true,
			}, nil
		case "c1.next":
			return backend.MutationPage{Events: []backend.MutationData{}, Cursor: "", HasMore: false}, nil
		default:
			return backend.MutationPage{}, errors.New("unexpected cursor " + since)
		}
	}
	liveWait := make(chan string, 1)
	b.waitPageFn = func(ctx context.Context, since string, _ int64, _ int) (backend.MutationPage, error) {
		liveWait <- since
		<-ctx.Done()
		return backend.MutationPage{}, ctx.Err()
	}
	sub := newBackendMutationSubscriber(b, hub, "ws-test", "c1.previous", defaultSubscriberBudgets())
	t.Cleanup(sub.Stop)

	sub.Start()
	head, err := sub.Ready(context.Background())
	if err != nil || head != "c1.next" {
		t.Fatalf("Ready = (%q, %v), want (c1.next, nil)", head, err)
	}
	if since := <-liveWait; since != "c1.next" {
		t.Fatalf("first live wait since = %q, want c1.next", since)
	}
	if got := len(client.Send()); got != 0 {
		t.Fatalf("client received %d drained events, want no replay broadcast", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != "c1.previous" || requests[1] != "c1.next" {
		t.Fatalf("drain requests = %v, want [c1.previous c1.next]", requests)
	}
}

func TestBackendMutationSubscriber_OldFleetHTTPDrainsPagedHead(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		since := query.Get("since")
		requests <- since + ":" + query.Get("limit") + ":" + query.Get("timeout")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case since == "c1.JA":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": "invalid_parameter", "message": "invalid since parameter: expected opaque cursor token",
			}})
		case since == "0":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events": []map[string]any{{"id": "c1.ZXZlbnQx", "action": "issue.update", "entity_type": "issue", "entity_id": "task-1"}},
				"cursor": "c1.cGFnZTI", "has_more": true,
			})
		case since == "c1.cGFnZTI" && query.Get("timeout") == "":
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}, "cursor": "", "has_more": false})
		case since == "c1.cGFnZTI" && query.Get("timeout") == "10000":
			<-r.Context().Done()
		default:
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)

	fleetBackend, err := backendfleet.New(backendfleet.Config{BaseURL: server.URL, WorkspaceID: "ws-test"})
	if err != nil {
		t.Fatalf("fleet.New: %v", err)
	}
	sub := NewBackendMutationSubscriber(fleetBackend, realtime.NewHub(), "ws-test")
	t.Cleanup(sub.Stop)
	sub.Start()
	head, err := sub.Ready(context.Background())
	if err != nil || head != "c1.cGFnZTI" {
		t.Fatalf("Ready = (%q, %v), want paged head c1.cGFnZTI", head, err)
	}

	want := []string{"c1.JA:1:0", "0:100:", "c1.cGFnZTI:100:"}
	for i, expected := range want {
		if got := <-requests; got != expected {
			t.Fatalf("request %d = %q, want %q", i+1, got, expected)
		}
	}
}

func TestBackendMutationSubscriber_OldFleetHTTPDrainCapFailsReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("since") == "c1.JA" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": "invalid_parameter", "message": "invalid since parameter: expected opaque cursor token",
			}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events": []map[string]any{{"id": "c1.ZXZlbnQx"}}, "cursor": "c1.cGFydGlhbA", "has_more": true,
		})
	}))
	t.Cleanup(server.Close)

	fleetBackend, err := backendfleet.New(backendfleet.Config{BaseURL: server.URL, WorkspaceID: "ws-test"})
	if err != nil {
		t.Fatalf("fleet.New: %v", err)
	}
	budgets := defaultSubscriberBudgets()
	budgets.maxDrainPages = 1
	sub := newBackendMutationSubscriber(fleetBackend, realtime.NewHub(), "ws-test", "0", budgets)
	t.Cleanup(sub.Stop)
	sub.Start()
	if _, err := sub.Ready(context.Background()); err == nil {
		t.Fatal("Ready error = nil, want old-Fleet drain-cap failure")
	}
}

func TestBackendMutationSubscriber_DrainCapFailsReadinessAndNeverStartsLivePolling(t *testing.T) {
	hub := realtime.NewHub()
	b := newScriptedCursorBackend()
	b.probeFn = func(context.Context) (string, bool, error) { return "", false, nil }
	b.getPageFn = func(_ context.Context, since string, _ int) (backend.MutationPage, error) {
		return backend.MutationPage{
			Events:  []backend.MutationData{{Cursor: "c1.event"}},
			Cursor:  "c1.partial",
			HasMore: true,
		}, nil
	}
	liveCalled := make(chan struct{}, 1)
	b.waitPageFn = func(context.Context, string, int64, int) (backend.MutationPage, error) {
		liveCalled <- struct{}{}
		return backend.MutationPage{}, nil
	}
	budgets := defaultSubscriberBudgets()
	budgets.maxDrainPages = 1
	sub := newBackendMutationSubscriber(b, hub, "ws-test", "0", budgets)
	t.Cleanup(sub.Stop)

	sub.Start()
	if _, err := sub.Ready(context.Background()); err == nil {
		t.Fatal("Ready error = nil, want drain-cap failure")
	}
	select {
	case <-liveCalled:
		t.Fatal("live polling started from a partial drain cursor")
	default:
	}
}

func TestBackendMutationSubscriber_HasMoreRepollsImmediately(t *testing.T) {
	hub := realtime.NewHub()
	b := newScriptedCursorBackend()
	secondWait := make(chan struct{})
	var calls int
	b.waitPageFn = func(ctx context.Context, since string, timeoutMs int64, limit int) (backend.MutationPage, error) {
		calls++
		switch calls {
		case 1:
			if since != "c1.head" || timeoutMs != int64(backendWaitTimeout/time.Millisecond) || limit != mutationPageLimit {
				t.Fatalf("first wait = (%q, %d, %d)", since, timeoutMs, limit)
			}
			return backend.MutationPage{Events: []backend.MutationData{{Cursor: "c1.event"}}, Cursor: "c1.next", HasMore: true}, nil
		case 2:
			if since != "c1.next" || timeoutMs != 0 || limit != mutationPageLimit {
				t.Fatalf("second wait = (%q, %d, %d), want (c1.next, 0, %d)", since, timeoutMs, limit, mutationPageLimit)
			}
			close(secondWait)
			<-ctx.Done()
			return backend.MutationPage{}, ctx.Err()
		default:
			return backend.MutationPage{}, errors.New("unexpected extra wait")
		}
	}
	sub := NewBackendMutationSubscriber(b, hub, "ws-test")
	t.Cleanup(sub.Stop)

	sub.Start()
	if _, err := sub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	<-secondWait
}

func TestBackendMutationSubscriber_StopReleasesReadinessWaiters(t *testing.T) {
	hub := realtime.NewHub()
	probeStarted := make(chan struct{})
	b := newScriptedCursorBackend()
	b.probeFn = func(ctx context.Context) (string, bool, error) {
		close(probeStarted)
		<-ctx.Done()
		return "", false, ctx.Err()
	}
	sub := NewBackendMutationSubscriber(b, hub, "ws-test")
	sub.Start()
	<-probeStarted

	readyResult := make(chan error, 1)
	go func() {
		_, err := sub.Ready(context.Background())
		readyResult <- err
	}()
	sub.Stop()
	if err := <-readyResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("Ready error = %v, want context.Canceled", err)
	}
}
