package subscription

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// fakeMutationStream is a minimal Work Items mutation stream. Concurrent calls are safe.
type fakeMutationStream struct {
	// waitFn is called by WaitForMutationsAfter. Receives ctx, cursor, timeoutMs.
	// If nil, returns an empty slice immediately (simulates idle long-poll).
	waitFn func(ctx context.Context, since string, timeoutMs int64) ([]workitems.Mutation, error)

	// getFn is called by GetMutationsAfter (catch-up path). Receives ctx, cursor.
	// If nil, returns an empty slice.
	getFn func(ctx context.Context, since string) ([]workitems.Mutation, error)

	// callCounters track invocation counts for assertions.
	waitCalls atomic.Int64
	getCalls  atomic.Int64

	// recordedSince captures the `since` argument of the most recent
	// WaitForMutations call. Used to assert lastSince advancement.
	mu             sync.Mutex
	recordedSinces []string
}

func newFakeMutationStream() *fakeMutationStream { return &fakeMutationStream{} }

// recordSince stores `since` under the lock so tests can inspect it.
func (f *fakeMutationStream) recordSince(since string) {
	f.mu.Lock()
	f.recordedSinces = append(f.recordedSinces, since)
	f.mu.Unlock()
}

func (f *fakeMutationStream) sinceHistory() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.recordedSinces))
	copy(out, f.recordedSinces)
	return out
}

func (f *fakeMutationStream) WaitForMutationsAfter(ctx context.Context, since string, timeoutMs int64) ([]workitems.Mutation, error) {
	f.waitCalls.Add(1)
	f.recordSince(since)
	if f.waitFn != nil {
		return f.waitFn(ctx, since, timeoutMs)
	}
	// Default: simulate timeout by sleeping briefly then returning empty.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return []workitems.Mutation{}, nil
	}
}

func (f *fakeMutationStream) GetMutationsAfter(ctx context.Context, since string) ([]workitems.Mutation, error) {
	f.getCalls.Add(1)
	if f.getFn != nil {
		return f.getFn(ctx, since)
	}
	return []workitems.Mutation{}, nil
}

// newTestSubscriberEnv builds the minimal hub + fake stream + subscriber
// environment used by every test in this file. Cleanup is registered on t.
func newTestSubscriberEnv(t *testing.T) (*WorkItemMutationSubscriber, *fakeMutationStream, *realtime.Hub) {
	t.Helper()
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	stream := newFakeMutationStream()
	sub := NewWorkItemMutationSubscriber(stream, hub, "ws-test")
	t.Cleanup(sub.Stop)
	return sub, stream, hub
}

// TestWorkItemMutationSubscriber_StartStopLifecycle verifies that Start
// spawns a goroutine and Stop cancels the context, waits, and closes done
// in that order. The bug this guards against: closing done before
// canceling ctx would deadlock if the loop is mid-WaitForMutations.
func TestWorkItemMutationSubscriber_StartStopLifecycle(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	// Inject a long-blocking WaitForMutations to exercise the
	// "Stop cancels in-flight call" code path.
	blockUntil := make(chan struct{})
	fb.waitFn = func(ctx context.Context, _ string, _ int64) ([]workitems.Mutation, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-blockUntil:
			return []workitems.Mutation{}, nil
		}
	}

	sub.Start()
	// Give the goroutine a moment to enter WaitForMutations.
	time.Sleep(30 * time.Millisecond)
	if fb.waitCalls.Load() == 0 {
		t.Fatal("expected Start to have triggered at least one WaitForMutations call")
	}

	stopped := make(chan struct{})
	go func() {
		sub.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		// good
	case <-time.After(2 * time.Second):
		// Unblock to make the goroutine leak detectable but the test must
		// still fail.
		close(blockUntil)
		t.Fatal("Stop did not return within 2s; ctx cancel did not unblock WaitForMutations")
	}

	// Stop is idempotent.
	sub.Stop()
}

// TestWorkItemMutationSubscriber_DoubleStart verifies that Start is
// idempotent — only the first call spawns a goroutine. Without this, a
// stray re-activation (e.g., from the hub-level activate path racing
// with itself) would create N parallel long-pollers.
func TestWorkItemMutationSubscriber_DoubleStart(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	sub.Start()
	sub.Start()
	sub.Start()
	time.Sleep(50 * time.Millisecond)

	// With one goroutine, we expect exactly one outstanding wait call (the
	// loop returns and immediately re-enters; default fake stream sleeps
	// 20ms per call). With three goroutines we'd see ~3x as many calls.
	calls := fb.waitCalls.Load()
	if calls > 5 { // generous upper bound for one goroutine in 50ms
		t.Errorf("Start was not idempotent: %d wait calls observed (want ~1-3)", calls)
	}
}

// TestWorkItemMutationSubscriber_Broadcast verifies that mutations returned
// by WaitForMutations are forwarded to the realtime.Hub with the correct
// workspaceID stamp.
func TestWorkItemMutationSubscriber_Broadcast(t *testing.T) {
	sub, fb, hub := newTestSubscriberEnv(t)

	// Register an SSE client to receive broadcasts.
	client := realtime.NewClient(1, 64, "0", nil, "ws-test")
	hub.RegisterClient(client)
	time.Sleep(20 * time.Millisecond) // let the hub install the client

	deliveredOnce := make(chan struct{}, 1)
	ts := time.Date(2026, 4, 25, 11, 0, 0, 0, time.UTC)
	fb.waitFn = func(ctx context.Context, since string, _ int64) ([]workitems.Mutation, error) {
		// First call returns mutations; subsequent calls block until ctx
		// cancel so the test's expected count remains exactly 1.
		if fb.waitCalls.Load() == 1 {
			defer func() {
				select {
				case deliveredOnce <- struct{}{}:
				default:
				}
			}()
			return []workitems.Mutation{{
				Type:      "create",
				IssueID:   "loom-broadcast-1",
				Title:     "broadcast test",
				Timestamp: ts,
			}}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	sub.Start()

	select {
	case <-deliveredOnce:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForMutations was not invoked or did not return mutations")
	}

	select {
	case got := <-client.Send():
		if got.IssueID != "loom-broadcast-1" {
			t.Errorf("got IssueID %q, want loom-broadcast-1", got.IssueID)
		}
		if got.WorkspaceID != "ws-test" {
			t.Errorf("got WorkspaceID %q, want ws-test", got.WorkspaceID)
		}
		if got.Type != "create" {
			t.Errorf("got Type %q, want create", got.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not receive broadcast within 2s")
	}
}

// TestWorkItemMutationSubscriber_LastCursorAdvances verifies that the opaque
// cursor advances to the last cursor from each batch, and that
// subsequent WaitForMutations calls pass the advanced cursor.
func TestWorkItemMutationSubscriber_LastCursorAdvances(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	earlyMs := int64(1_700_000_000_000)
	lateMs := int64(1_700_000_000_500)
	earlyTs := time.UnixMilli(earlyMs).UTC()
	lateTs := time.UnixMilli(lateMs).UTC()

	delivered := make(chan struct{}, 1)
	fb.waitFn = func(ctx context.Context, since string, _ int64) ([]workitems.Mutation, error) {
		// First call delivers two events; subsequent calls block on ctx.
		if fb.waitCalls.Load() == 1 {
			defer func() {
				select {
				case delivered <- struct{}{}:
				default:
				}
			}()
			return []workitems.Mutation{
				{Cursor: "1700000000000-1", Type: "create", IssueID: "loom-a", Timestamp: earlyTs},
				{Cursor: "1700000000500-2", Type: "update", IssueID: "loom-b", Timestamp: lateTs},
			}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	sub.Start()

	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("first batch was not delivered")
	}

	// Wait briefly for the loop to invoke WaitForMutations a second time
	// with the advanced since cursor.
	deadline := time.Now().Add(2 * time.Second)
	var hist []string
	for time.Now().Before(deadline) {
		hist = fb.sinceHistory()
		if len(hist) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(hist) < 2 {
		t.Fatalf("expected at least 2 WaitForMutations calls, got %d", len(hist))
	}
	if hist[0] != "0" {
		t.Errorf("first call should have cursor=0, got %q", hist[0])
	}
	if hist[1] != "1700000000500-2" {
		t.Errorf("second call should preserve the last opaque cursor, got %q", hist[1])
	}

	// Direct accessor sanity check.
	sub.mu.RLock()
	got := sub.lastCursor
	sub.mu.RUnlock()
	if got != "1700000000500-2" {
		t.Errorf("lastCursor = %q, want opaque cursor", got)
	}
}

// TestWorkItemMutationSubscriber_RetryOnError verifies that a transient
// error from WaitForMutations triggers a backoff, and that the loop
// recovers on the next successful call.
func TestWorkItemMutationSubscriber_RetryOnError(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	var called atomic.Int64
	recovered := make(chan struct{}, 1)
	fb.waitFn = func(ctx context.Context, _ string, _ int64) ([]workitems.Mutation, error) {
		n := called.Add(1)
		if n == 1 {
			return nil, errors.New("transient http 503")
		}
		// On second call, signal recovery and then block on ctx so we
		// don't spin.
		select {
		case recovered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	sub.Start()

	// mutationRetryDelay is 2s in production. To keep the test fast we
	// allow up to 3s for the recovery call to fire.
	select {
	case <-recovered:
		// good
	case <-time.After(3 * time.Second):
		t.Fatalf("retry did not occur; called=%d", called.Load())
	}
}

// TestWorkItemMutationSubscriber_GetMutationsAfter_HappyPath verifies
// the catch-up path: a single GetMutations call, results returned as-is.
func TestWorkItemMutationSubscriber_GetMutationsAfter_HappyPath(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	ts := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	fb.getFn = func(_ context.Context, since string) ([]workitems.Mutation, error) {
		if since != "100" {
			t.Errorf("GetMutationsAfter called with cursor=%q, want 100", since)
		}
		return []workitems.Mutation{
			{Type: "create", IssueID: "loom-1", Timestamp: ts},
			{Type: "update", IssueID: "loom-2", Timestamp: ts},
		}, nil
	}

	got := sub.GetMutationsAfter("100")
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].IssueID != "loom-1" || got[1].IssueID != "loom-2" {
		t.Errorf("unexpected IDs: %v", got)
	}
}

// TestWorkItemMutationSubscriber_GetMutationsAfter_StreamError verifies
// that an error from GetMutations is swallowed (returns nil) so the SSE
// catch-up path can proceed to the connected event without aborting the
// stream.
func TestWorkItemMutationSubscriber_GetMutationsAfter_StreamError(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	fb.getFn = func(_ context.Context, _ string) ([]workitems.Mutation, error) {
		return nil, errors.New("simulated network failure")
	}

	got := sub.GetMutationsAfter("0")
	if got != nil {
		t.Errorf("expected nil on stream error, got %v", got)
	}
}

// TestWorkItemMutationSubscriber_GetMutationsAfter_NilStream covers the
// guard against a nil stream (defense-in-depth — NewWorkItemMutationSubscriber
// can be called with a typed-nil if a misconfigured caller resolves a missing
// resource).
func TestWorkItemMutationSubscriber_GetMutationsAfter_NilStream(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	sub := NewWorkItemMutationSubscriber(nil, hub, "ws-x")
	t.Cleanup(sub.Stop)

	if got := sub.GetMutationsAfter("0"); got != nil {
		t.Errorf("expected nil with nil stream, got %v", got)
	}
}

// TestWorkItemMutationSubscriber_StopWithoutStart verifies Stop is safe to
// call before Start (idempotent on the stop side, never spawns a goroutine).
func TestWorkItemMutationSubscriber_StopWithoutStart(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	stream := newFakeMutationStream()
	sub := NewWorkItemMutationSubscriber(stream, hub, "ws-y")
	sub.Stop() // must not deadlock; wg has nothing to wait on
	if stream.waitCalls.Load() != 0 {
		t.Errorf("expected 0 wait calls when never started, got %d", stream.waitCalls.Load())
	}
}
