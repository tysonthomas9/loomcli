package subscription

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

func TestEnsureActive_ConcurrentCallersShareReadiness(t *testing.T) {
	hub := realtime.NewHub()
	multi := NewMultiWorkspaceSubscriber(hub, nil)
	t.Cleanup(multi.Stop)
	b := newScriptedCursorBackend()
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	var probes atomic.Int64
	b.probeFn = func(context.Context) (string, bool, error) {
		if probes.Add(1) == 1 {
			close(probeStarted)
		}
		<-releaseProbe
		return "c1.shared-head", true, nil
	}

	const callers = 16
	start := make(chan struct{})
	results := make(chan struct {
		head string
		err  error
	}, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			head, err := multi.EnsureActive(context.Background(), "ws-shared", b, ActivationReasonSSE)
			results <- struct {
				head string
				err  error
			}{head: head, err: err}
		}()
	}
	close(start)
	<-probeStarted
	close(releaseProbe)
	wg.Wait()
	close(results)

	for result := range results {
		if result.err != nil || result.head != "c1.shared-head" {
			t.Fatalf("EnsureActive = (%q, %v), want shared head", result.head, result.err)
		}
	}
	if got := probes.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want exactly 1", got)
	}
}

func TestEnsureActive_ShutdownReleasesReadinessWaiters(t *testing.T) {
	hub := realtime.NewHub()
	multi := NewStartedMultiWorkspaceSubscriber(context.Background(), hub, nil)
	b := newScriptedCursorBackend()
	probeStarted := make(chan struct{})
	b.probeFn = func(ctx context.Context) (string, bool, error) {
		close(probeStarted)
		<-ctx.Done()
		return "", false, ctx.Err()
	}

	result := make(chan error, 1)
	go func() {
		_, err := multi.EnsureActive(context.Background(), "ws-shutdown", b, ActivationReasonSSE)
		result <- err
	}()
	<-probeStarted
	multi.Stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureActive error = %v, want context.Canceled", err)
	}
}

func TestEnsureActive_ReactivationDrainsFromLastHead(t *testing.T) {
	hub := realtime.NewHub()
	multi := NewMultiWorkspaceSubscriber(hub, nil)
	t.Cleanup(multi.Stop)

	first := newScriptedCursorBackend()
	first.probeFn = func(context.Context) (string, bool, error) { return "", false, nil }
	first.getPageFn = func(_ context.Context, since string, _ int) (backend.MutationPage, error) {
		if since != "0" {
			t.Fatalf("first activation since = %q, want 0", since)
		}
		return backend.MutationPage{Events: []backend.MutationData{}, Cursor: "c1.first-head"}, nil
	}
	head, err := multi.EnsureActive(context.Background(), "ws-reuse", first, ActivationReasonSSE)
	if err != nil || head != "c1.first-head" {
		t.Fatalf("first EnsureActive = (%q, %v)", head, err)
	}
	multi.RemoveWorkspace("ws-reuse")

	second := newScriptedCursorBackend()
	second.probeFn = func(context.Context) (string, bool, error) { return "", false, nil }
	second.getPageFn = func(_ context.Context, since string, _ int) (backend.MutationPage, error) {
		if since != "c1.first-head" {
			t.Fatalf("reactivation since = %q, want c1.first-head", since)
		}
		return backend.MutationPage{Events: []backend.MutationData{}, Cursor: "c1.second-head"}, nil
	}
	head, err = multi.EnsureActive(context.Background(), "ws-reuse", second, ActivationReasonSSE)
	if err != nil || head != "c1.second-head" {
		t.Fatalf("second EnsureActive = (%q, %v)", head, err)
	}
}
