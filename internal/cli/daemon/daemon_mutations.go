package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/notify"
)

// Control socket operation names for mutation queries.
const (
	ctrlOpGetMutations     = "get_mutations"
	ctrlOpWaitForMutations = "wait_for_mutations" //nolint:gosec // not a credential

	defaultMutationBufferCapacity = 1024
	maxWaitTimeout                = 60 * time.Second
)

// GetMutationsArgs are the arguments for the get_mutations control socket operation.
type GetMutationsArgs struct {
	Since int64 `json:"since"` // Unix milliseconds
}

// WaitForMutationsArgs are the arguments for the wait_for_mutations control socket operation.
type WaitForMutationsArgs struct {
	Since   int64 `json:"since"`   // Unix milliseconds
	Timeout int64 `json:"timeout"` // Timeout in milliseconds (max 60000)
}

// MutationBuffer accumulates IPC mutations from a notify.Bus for control socket queries.
// It stores mutations in a fixed-capacity ring buffer and supports timestamp-based queries.
type MutationBuffer struct {
	mu       sync.RWMutex
	buf      []backend.MutationData // ring buffer
	head     int                    // next write position
	count    int                    // number of entries in buffer
	capacity int

	notify   chan struct{}        // signaled on every write (non-blocking)
	sub      *notify.Subscription // bus subscription
	wg       sync.WaitGroup
	done     chan struct{}
	stopOnce sync.Once
}

// NewMutationBuffer creates a mutation buffer that subscribes to the given bus
// for events with topic prefix "issue" scoped to the given workspace.
func NewMutationBuffer(capacity int, bus *notify.Bus, workspaceID string) *MutationBuffer {
	if capacity < 1 {
		capacity = 1
	}
	sub := bus.Subscribe(workspaceID, "issue")
	if sub == nil {
		return nil
	}
	return &MutationBuffer{
		buf:      make([]backend.MutationData, capacity),
		capacity: capacity,
		notify:   make(chan struct{}, 1),
		sub:      sub,
		done:     make(chan struct{}),
	}
}

// Start begins the background goroutine that reads from the subscription and appends to the ring buffer.
func (b *MutationBuffer) Start() {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.run()
	}()
}

// Stop signals the goroutine to exit, closes the subscription, and waits for cleanup.
// Safe to call multiple times.
func (b *MutationBuffer) Stop() {
	b.stopOnce.Do(func() {
		close(b.done)
		b.sub.Close()
		b.wg.Wait()
	})
}

// GetSince returns mutations with timestamp at or after sinceMs (Unix
// milliseconds). The timestamp-only control socket cursor is inclusive to avoid
// dropping multiple mutations that share the same millisecond; callers should
// dedupe repeated mutations when reconnecting from a millisecond cursor.
// Returns nil if the buffer is empty or no mutations match.
func (b *MutationBuffer) GetSince(sinceMs int64) []backend.MutationData {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil
	}

	sinceTime := time.UnixMilli(sinceMs)
	var result []backend.MutationData

	// Scan from oldest to newest
	start := b.head - b.count
	if start < 0 {
		start += b.capacity
	}
	for i := 0; i < b.count; i++ {
		idx := (start + i) % b.capacity
		if !b.buf[idx].Timestamp.Before(sinceTime) {
			result = append(result, b.buf[idx])
		}
	}
	return result
}

// WaitSince returns mutations after sinceMs, blocking up to timeout if none are immediately available.
// Returns empty slice on timeout or context cancellation.
//
// One span per call (`daemon.mutations.cycle`) — one drain cycle of the
// mutation buffer, even when the call returns zero mutations on timeout
// or shutdown. The span ends when this function returns.
func (b *MutationBuffer) WaitSince(ctx context.Context, sinceMs int64, timeout time.Duration) []backend.MutationData {
	cycleStart := time.Now()
	ctx, span := startMutationsCycleSpan(ctx)
	defer span.End()
	span.SetAttributes(attribute.Int64("since_ms", sinceMs))

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	finalize := func(out []backend.MutationData) []backend.MutationData {
		span.SetAttributes(
			attribute.Int("mutations.count", len(out)),
			attribute.Int64("cycle.duration_ms", time.Since(cycleStart).Milliseconds()),
		)
		return out
	}

	for {
		if result := b.GetSince(sinceMs); len(result) > 0 {
			return finalize(result)
		}

		select {
		case <-b.notify:
			// Notification received — loop back to re-check buffer
		case <-timer.C:
			return finalize(nil)
		case <-ctx.Done():
			return finalize(nil)
		case <-b.done:
			return finalize(nil)
		}
	}
}

// append adds a mutation to the ring buffer and signals waiters.
func (b *MutationBuffer) append(m backend.MutationData) {
	b.mu.Lock()
	b.buf[b.head] = m
	b.head = (b.head + 1) % b.capacity
	if b.count < b.capacity {
		b.count++
	}
	b.mu.Unlock()

	// Non-blocking signal to any WaitSince callers
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

// run is the background goroutine that reads events from the subscription.
func (b *MutationBuffer) run() {
	ch := b.sub.Events()
	for {
		select {
		case <-b.done:
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			m, ok := evt.Payload.(backend.MutationData)
			if !ok {
				continue
			}
			b.append(m)
		}
	}
}

// handleControlGetMutations handles the get_mutations control socket operation.
func (d *Daemon) handleControlGetMutations(args json.RawMessage) DaemonControlResponse {
	if d.mutBuf == nil {
		return DaemonControlResponse{Error: "mutation tracking not available"}
	}

	var sinceMs int64
	if len(args) > 0 && string(args) != "null" {
		var a GetMutationsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return DaemonControlResponse{Error: "invalid get_mutations args: " + err.Error()}
		}
		sinceMs = a.Since
	}

	mutations := d.mutBuf.GetSince(sinceMs)
	if mutations == nil {
		mutations = []backend.MutationData{}
	}

	data, err := json.Marshal(mutations)
	if err != nil {
		return DaemonControlResponse{Error: "failed to marshal mutations: " + err.Error()}
	}
	return DaemonControlResponse{Success: true, Data: data}
}

// handleControlWaitForMutations handles the wait_for_mutations control socket operation.
func (d *Daemon) handleControlWaitForMutations(args json.RawMessage) DaemonControlResponse {
	if d.mutBuf == nil {
		return DaemonControlResponse{Error: "mutation tracking not available"}
	}

	var sinceMs int64
	timeout := 30 * time.Second // default
	if len(args) > 0 && string(args) != "null" {
		var a WaitForMutationsArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return DaemonControlResponse{Error: "invalid wait_for_mutations args: " + err.Error()}
		}
		sinceMs = a.Since
		if a.Timeout > 0 {
			timeout = time.Duration(a.Timeout) * time.Millisecond
		}
	}

	// Clamp to max
	if timeout > maxWaitTimeout {
		timeout = maxWaitTimeout
	}

	mutations := d.mutBuf.WaitSince(cmdstore.RootContext(), sinceMs, timeout)
	if mutations == nil {
		mutations = []backend.MutationData{}
	}

	data, err := json.Marshal(mutations)
	if err != nil {
		return DaemonControlResponse{Error: "failed to marshal mutations: " + err.Error()}
	}
	return DaemonControlResponse{Success: true, Data: data}
}

// wireDaemonNotifyBus creates a notification bus and mutation buffer for the daemon.
// Called from runDaemon after the Daemon is constructed.
func wireDaemonNotifyBus(d *Daemon) {
	bus := notify.New()
	d.notifyBus = bus
	if mutBuf := NewMutationBuffer(defaultMutationBufferCapacity, bus, d.sup.WorkspaceID); mutBuf != nil {
		d.mutBuf = mutBuf
		mutBuf.Start()
	}
}
