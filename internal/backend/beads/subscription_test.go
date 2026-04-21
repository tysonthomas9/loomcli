package beads

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/notify"
)

// mockMutationSource is a test double implementing mutationSource.
type mockMutationSource struct {
	waitFn func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error)
}

func (m *mockMutationSource) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	if m.waitFn != nil {
		return m.waitFn(ctx, sinceMs, timeoutMs)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// testBridgeConfig returns a BridgeConfig with short timeouts for fast tests.
func testBridgeConfig(wsID string) BridgeConfig {
	return BridgeConfig{
		WorkspaceID: wsID,
		WaitTimeout: 50 * time.Millisecond,
		RetryDelay:  5 * time.Millisecond,
	}
}

// collectEvents subscribes to the bus and collects events until count is reached or timeout.
func collectEvents(bus *notify.Bus, wsID string, count int, timeout time.Duration) []notify.Event {
	sub := bus.Subscribe(wsID, "issue")
	defer sub.Close()

	var events []notify.Event
	deadline := time.After(timeout)
	for {
		select {
		case e := <-sub.Events():
			events = append(events, e)
			if len(events) >= count {
				return events
			}
		case <-deadline:
			return events
		}
	}
}

func TestMutationBridge_PublishesMutationsToBus(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ts1 := time.Date(2026, 1, 1, 0, 0, 0, 100e6, time.UTC)
	ts2 := time.Date(2026, 1, 1, 0, 0, 0, 200e6, time.UTC)

	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			if callCount.Add(1) == 1 {
				return []backend.MutationData{
					{Type: backend.MutationCreate, IssueID: "a", Timestamp: ts1},
					{Type: backend.MutationUpdate, IssueID: "b", Timestamp: ts2},
				}, nil
			}
			// Block on subsequent calls to avoid busy loop.
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	defer bridge.Stop()

	events := collectEvents(bus, "ws-1", 2, time.Second)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Topic != "issue.create" {
		t.Errorf("event[0].Topic = %q, want %q", events[0].Topic, "issue.create")
	}
	if events[1].Topic != "issue.update" {
		t.Errorf("event[1].Topic = %q, want %q", events[1].Topic, "issue.update")
	}

	// Verify payloads.
	md0 := events[0].Payload.(backend.MutationData)
	if md0.IssueID != "a" {
		t.Errorf("event[0].Payload.IssueID = %q, want %q", md0.IssueID, "a")
	}
	md1 := events[1].Payload.(backend.MutationData)
	if md1.IssueID != "b" {
		t.Errorf("event[1].Payload.IssueID = %q, want %q", md1.IssueID, "b")
	}
}

func TestMutationBridge_AdvancesLastSince(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ts1 := time.UnixMilli(100)
	ts2 := time.UnixMilli(200)

	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			if callCount.Add(1) == 1 {
				return []backend.MutationData{
					{Type: "create", IssueID: "a", Timestamp: ts1},
					{Type: "update", IssueID: "b", Timestamp: ts2},
				}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	defer bridge.Stop()

	// Wait for events to be processed.
	collectEvents(bus, "ws-1", 2, time.Second)

	got := bridge.LastSince()
	want := int64(201) // maxTimestamp(200) + 1
	if got != want {
		t.Errorf("LastSince() = %d, want %d", got, want)
	}
}

func TestMutationBridge_RetriesOnUnavailable(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ts := time.UnixMilli(50)
	var callCount atomic.Int32

	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			n := callCount.Add(1)
			if n <= 2 {
				return nil, backend.ErrUnavailable("WaitForMutations", "connection refused", nil)
			}
			if n == 3 {
				return []backend.MutationData{
					{Type: "create", IssueID: "a", Timestamp: ts},
				}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	defer bridge.Stop()

	events := collectEvents(bus, "ws-1", 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event after retries, got %d", len(events))
	}
}

func TestMutationBridge_StopsCleanly(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()

	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK — stopped cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds")
	}
}

func TestMutationBridge_StopCancelsWait(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ctxCanceled := make(chan struct{})
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			<-ctx.Done()
			close(ctxCanceled)
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()

	// Give goroutine time to enter WaitForMutations.
	time.Sleep(20 * time.Millisecond)

	bridge.Stop()

	select {
	case <-ctxCanceled:
		// OK — context was canceled.
	case <-time.After(2 * time.Second):
		t.Fatal("context was not canceled within 2 seconds")
	}
}

func TestMutationBridge_DoubleStartSafe(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			callCount.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	bridge.Start() // Should be no-op.
	defer bridge.Stop()

	time.Sleep(30 * time.Millisecond)

	// Only one goroutine should have been spawned, so only one call to waitFn.
	if n := callCount.Load(); n != 1 {
		t.Errorf("expected 1 goroutine call, got %d", n)
	}
}

func TestMutationBridge_DoubleStopSafe(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()

	// Should not panic.
	bridge.Stop()
	bridge.Stop()
}

func TestMutationBridge_NilSourcePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil source")
		}
	}()
	NewMutationBridge(nil, &notify.NopPublisher{}, DefaultBridgeConfig())
}

func TestMutationBridge_NilPublisherPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil publisher")
		}
	}()
	src := &mockMutationSource{}
	NewMutationBridge(src, nil, DefaultBridgeConfig())
}

func TestMutationBridge_EmptyMutationsSkipped(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			if callCount.Add(1) == 1 {
				return []backend.MutationData{}, nil // empty
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	defer bridge.Stop()

	// Give it a moment.
	time.Sleep(50 * time.Millisecond)

	if ls := bridge.LastSince(); ls != 0 {
		t.Errorf("LastSince() = %d, want 0 (unchanged)", ls)
	}
}

func TestMutationBridge_WorkspaceIDOnEvents(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ts := time.UnixMilli(100)
	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			if callCount.Add(1) == 1 {
				return []backend.MutationData{
					{Type: "create", IssueID: "a", Timestamp: ts},
				}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-42"))
	bridge.Start()
	defer bridge.Stop()

	events := collectEvents(bus, "ws-42", 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].WorkspaceID != "ws-42" {
		t.Errorf("WorkspaceID = %q, want %q", events[0].WorkspaceID, "ws-42")
	}
}

func TestMutationBridge_ContextCancellationExits(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			return nil, context.Canceled
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))

	// Manually set context to be already canceled.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bridge.mu.Lock()
	bridge.started = true
	bridge.cancel = cancel
	bridge.mu.Unlock()

	bridge.wg.Add(1)
	go bridge.run(ctx)

	done := make(chan struct{})
	go func() {
		bridge.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// OK.
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit on canceled context")
	}
}

func TestMutationTopic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"create", "issue.create"},
		{"update", "issue.update"},
		{"delete", "issue.delete"},
		{"status", "issue.status"},
		{"comment", "issue.comment"},
		{"refresh", "issue.refresh"},
		{"session_change", "issue.session_change"},
		{"custom_type", "issue.custom_type"},
		{"", "issue.unknown"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("type=%q", tt.input), func(t *testing.T) {
			got := mutationTopic(tt.input)
			if got != tt.want {
				t.Errorf("mutationTopic(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestMutationBridge_DefaultConfig(t *testing.T) {
	cfg := DefaultBridgeConfig()
	if cfg.WaitTimeout != 30*time.Second {
		t.Errorf("WaitTimeout = %v, want 30s", cfg.WaitTimeout)
	}
	if cfg.RetryDelay != 2*time.Second {
		t.Errorf("RetryDelay = %v, want 2s", cfg.RetryDelay)
	}
}

func TestMutationBridge_StopBeforeStart(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	src := &mockMutationSource{}
	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))

	// Stop without Start should not panic or deadlock.
	done := make(chan struct{})
	go func() {
		bridge.Stop()
		close(done)
	}()

	select {
	case <-done:
		// OK.
	case <-time.After(time.Second):
		t.Fatal("Stop() deadlocked when called before Start()")
	}
}

func TestMutationBridge_ConcurrentLastSince(t *testing.T) {
	bus := notify.New()
	defer bus.Close()

	ts := time.UnixMilli(500)
	var callCount atomic.Int32
	src := &mockMutationSource{
		waitFn: func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
			if callCount.Add(1) == 1 {
				return []backend.MutationData{
					{Type: "update", IssueID: "a", Timestamp: ts},
				}, nil
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	bridge := NewMutationBridge(src, bus, testBridgeConfig("ws-1"))
	bridge.Start()
	defer bridge.Stop()

	// Wait for processing.
	collectEvents(bus, "ws-1", 1, time.Second)

	// Concurrent reads should not race.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = bridge.LastSince()
		}()
	}
	wg.Wait()
}
