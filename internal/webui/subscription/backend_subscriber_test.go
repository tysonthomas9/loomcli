package subscription

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// fakeBackend is a minimal backend.IssueBackend stub for BackendMutationSubscriber
// tests. Only the mutation-polling methods are non-trivial; everything else
// returns ErrNotImplemented so misuse fails loudly. Concurrent calls are
// safe.
type fakeBackend struct {
	// waitFn is called by WaitForMutations. Receives ctx, since, timeoutMs.
	// If nil, returns an empty slice immediately (simulates idle long-poll).
	waitFn func(ctx context.Context, since int64, timeoutMs int64) ([]backend.MutationData, error)

	// getFn is called by GetMutations (catch-up path). Receives ctx, since.
	// If nil, returns an empty slice.
	getFn func(ctx context.Context, since int64) ([]backend.MutationData, error)

	// callCounters track invocation counts for assertions.
	waitCalls atomic.Int64
	getCalls  atomic.Int64

	// recordedSince captures the `since` argument of the most recent
	// WaitForMutations call. Used to assert lastSince advancement.
	mu             sync.Mutex
	recordedSinces []int64
}

func newFakeBackend() *fakeBackend { return &fakeBackend{} }

// recordSince stores `since` under the lock so tests can inspect it.
func (f *fakeBackend) recordSince(since int64) {
	f.mu.Lock()
	f.recordedSinces = append(f.recordedSinces, since)
	f.mu.Unlock()
}

func (f *fakeBackend) sinceHistory() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.recordedSinces))
	copy(out, f.recordedSinces)
	return out
}

func (f *fakeBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	f.waitCalls.Add(1)
	f.recordSince(sinceMs)
	if f.waitFn != nil {
		return f.waitFn(ctx, sinceMs, timeoutMs)
	}
	// Default: simulate timeout by sleeping briefly then returning empty.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
		return []backend.MutationData{}, nil
	}
}

func (f *fakeBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	f.getCalls.Add(1)
	if f.getFn != nil {
		return f.getFn(ctx, sinceMs)
	}
	return []backend.MutationData{}, nil
}

// All other backend.IssueBackend methods return ErrNotImplemented.
// These signatures must stay in sync with the interface; if the build breaks
// here, an interface method was added without updating the stub.
func (f *fakeBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	return 0, errors.New("not implemented")
}
func (f *fakeBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) ClaimIssue(ctx context.Context, params backend.ClaimIssueParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) UndeferIssue(ctx context.Context, id string) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) AddLabel(ctx context.Context, id string, label string) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	return errors.New("not implemented")
}
func (f *fakeBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeBackend) BackendName() string { return "fake" }

// newTestSubscriberEnv builds the minimal hub + fake backend + subscriber
// environment used by every test in this file. Cleanup is registered on t.
func newTestSubscriberEnv(t *testing.T) (*BackendMutationSubscriber, *fakeBackend, *realtime.Hub) {
	t.Helper()
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	fb := newFakeBackend()
	sub := NewBackendMutationSubscriber(fb, hub, "ws-test")
	t.Cleanup(sub.Stop)
	return sub, fb, hub
}

// TestBackendMutationSubscriber_StartStopLifecycle verifies that Start
// spawns a goroutine and Stop cancels the context, waits, and closes done
// in that order. The bug this guards against: closing done before
// canceling ctx would deadlock if the loop is mid-WaitForMutations.
func TestBackendMutationSubscriber_StartStopLifecycle(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	// Inject a long-blocking WaitForMutations to exercise the
	// "Stop cancels in-flight call" code path.
	blockUntil := make(chan struct{})
	fb.waitFn = func(ctx context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-blockUntil:
			return []backend.MutationData{}, nil
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

// TestBackendMutationSubscriber_DoubleStart verifies that Start is
// idempotent — only the first call spawns a goroutine. Without this, a
// stray re-activation (e.g., from the hub-level activate path racing
// with itself) would create N parallel long-pollers.
func TestBackendMutationSubscriber_DoubleStart(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	sub.Start()
	sub.Start()
	sub.Start()
	time.Sleep(50 * time.Millisecond)

	// With one goroutine, we expect exactly one outstanding wait call (the
	// loop returns and immediately re-enters; default fakeBackend sleeps
	// 20ms per call). With three goroutines we'd see ~3x as many calls.
	calls := fb.waitCalls.Load()
	if calls > 5 { // generous upper bound for one goroutine in 50ms
		t.Errorf("Start was not idempotent: %d wait calls observed (want ~1-3)", calls)
	}
}

// TestBackendMutationSubscriber_Broadcast verifies that mutations returned
// by WaitForMutations are forwarded to the realtime.Hub with the correct
// workspaceID stamp.
func TestBackendMutationSubscriber_Broadcast(t *testing.T) {
	sub, fb, hub := newTestSubscriberEnv(t)

	// Register an SSE client to receive broadcasts.
	client := realtime.NewClient(1, 64, "0", nil, "ws-test")
	hub.RegisterClient(client)
	time.Sleep(20 * time.Millisecond) // let the hub install the client

	deliveredOnce := make(chan struct{}, 1)
	ts := time.Date(2026, 4, 25, 11, 0, 0, 0, time.UTC)
	fb.waitFn = func(ctx context.Context, since int64, _ int64) ([]backend.MutationData, error) {
		// First call returns mutations; subsequent calls block until ctx
		// cancel so the test's expected count remains exactly 1.
		if fb.waitCalls.Load() == 1 {
			defer func() {
				select {
				case deliveredOnce <- struct{}{}:
				default:
				}
			}()
			return []backend.MutationData{{
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

// TestBackendMutationSubscriber_LastSinceAdvances verifies that lastSince
// monotonically advances to the max timestamp from each batch, and that
// subsequent WaitForMutations calls pass the advanced cursor.
func TestBackendMutationSubscriber_LastSinceAdvances(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	earlyMs := int64(1_700_000_000_000)
	lateMs := int64(1_700_000_000_500)
	earlyTs := time.UnixMilli(earlyMs).UTC()
	lateTs := time.UnixMilli(lateMs).UTC()

	delivered := make(chan struct{}, 1)
	fb.waitFn = func(ctx context.Context, since int64, _ int64) ([]backend.MutationData, error) {
		// First call delivers two events; subsequent calls block on ctx.
		if fb.waitCalls.Load() == 1 {
			defer func() {
				select {
				case delivered <- struct{}{}:
				default:
				}
			}()
			return []backend.MutationData{
				{Type: "create", IssueID: "loom-a", Timestamp: earlyTs},
				{Type: "update", IssueID: "loom-b", Timestamp: lateTs},
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
	var hist []int64
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
	if hist[0] != 0 {
		t.Errorf("first call should have since=0, got %d", hist[0])
	}
	if hist[1] != lateMs {
		t.Errorf("second call should have since=%d (max ts), got %d", lateMs, hist[1])
	}

	// Direct accessor sanity check.
	sub.mu.RLock()
	got := sub.lastSince
	sub.mu.RUnlock()
	if got != lateMs {
		t.Errorf("lastSince = %d, want %d", got, lateMs)
	}
}

// TestBackendMutationSubscriber_RetryOnError verifies that a transient
// error from WaitForMutations triggers a backoff, and that the loop
// recovers on the next successful call.
func TestBackendMutationSubscriber_RetryOnError(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	var called atomic.Int64
	recovered := make(chan struct{}, 1)
	fb.waitFn = func(ctx context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
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

	// backendRetryDelay is 2s in production. To keep the test fast we
	// allow up to 3s for the recovery call to fire.
	select {
	case <-recovered:
		// good
	case <-time.After(3 * time.Second):
		t.Fatalf("retry did not occur; called=%d", called.Load())
	}
}

// TestBackendMutationSubscriber_GetMutationDataSince_HappyPath verifies
// the catch-up path: a single GetMutations call, results returned as-is.
func TestBackendMutationSubscriber_GetMutationDataSince_HappyPath(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	ts := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	fb.getFn = func(_ context.Context, since int64) ([]backend.MutationData, error) {
		if since != 100 {
			t.Errorf("GetMutations called with since=%d, want 100", since)
		}
		return []backend.MutationData{
			{Type: "create", IssueID: "loom-1", Timestamp: ts},
			{Type: "update", IssueID: "loom-2", Timestamp: ts},
		}, nil
	}

	got := sub.GetMutationDataSince("100")
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].IssueID != "loom-1" || got[1].IssueID != "loom-2" {
		t.Errorf("unexpected IDs: %v", got)
	}
}

// TestBackendMutationSubscriber_GetMutationDataSince_BackendError verifies
// that an error from GetMutations is swallowed (returns nil) so the SSE
// catch-up path can proceed to the connected event without aborting the
// stream.
func TestBackendMutationSubscriber_GetMutationDataSince_BackendError(t *testing.T) {
	sub, fb, _ := newTestSubscriberEnv(t)

	fb.getFn = func(_ context.Context, _ int64) ([]backend.MutationData, error) {
		return nil, errors.New("simulated network failure")
	}

	got := sub.GetMutationDataSince("0")
	if got != nil {
		t.Errorf("expected nil on backend error, got %v", got)
	}
}

// TestBackendMutationSubscriber_GetMutationDataSince_NilBackend covers the
// guard against a nil backend (defense-in-depth — NewBackendMutationSubscriber
// can be called with a typed-nil if a misconfigured caller resolves a missing
// resource).
func TestBackendMutationSubscriber_GetMutationDataSince_NilBackend(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	sub := NewBackendMutationSubscriber(nil, hub, "ws-x")
	t.Cleanup(sub.Stop)

	if got := sub.GetMutationDataSince("0"); got != nil {
		t.Errorf("expected nil with nil backend, got %v", got)
	}
}

// TestBackendMutationSubscriber_StopWithoutStart verifies Stop is safe to
// call before Start (idempotent on the stop side, never spawns a goroutine).
func TestBackendMutationSubscriber_StopWithoutStart(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	fb := newFakeBackend()
	sub := NewBackendMutationSubscriber(fb, hub, "ws-y")
	sub.Stop() // must not deadlock; wg has nothing to wait on
	if fb.waitCalls.Load() != 0 {
		t.Errorf("expected 0 wait calls when never started, got %d", fb.waitCalls.Load())
	}
}
