package beads

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/notify"
)

// mutationSource provides access to the mutation polling API.
// Both BeadsBackend and PooledBackend satisfy this interface.
type mutationSource interface {
	WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error)
}

// BridgeConfig configures a MutationBridge.
type BridgeConfig struct {
	WorkspaceID string        // Workspace ID to tag on all published events (required for scoped subscriptions).
	WaitTimeout time.Duration // Timeout for WaitForMutations long-poll (default: 30s).
	RetryDelay  time.Duration // Delay before retrying after an error (default: 2s).
	Logger      *slog.Logger  // Optional logger; defaults to slog.Default().
}

// DefaultBridgeConfig returns a BridgeConfig with production-ready defaults.
func DefaultBridgeConfig() BridgeConfig {
	return BridgeConfig{
		WaitTimeout: 30 * time.Second,
		RetryDelay:  2 * time.Second,
	}
}

// MutationBridge continuously long-polls a MutationSource for mutations via
// WaitForMutations and publishes them as notify.Event values to a notification bus.
//
// In production, the MutationSource is typically a PooledBackend (task .10).
// WaitForMutations holds one pool connection for up to WaitTimeout (30s by
// default). Size the connection pool accordingly (default pool size 10 is
// sufficient).
type MutationBridge struct {
	source MutationSource
	pub    notify.Publisher
	cfg    BridgeConfig
	logger *slog.Logger

	cancel    context.CancelFunc
	wg        sync.WaitGroup
	lastSince int64
	mu        sync.RWMutex
	started   bool
	stopOnce  sync.Once
}

// MutationSource is exported for documentation; the bridge accepts the
// unexported mutationSource to keep the API narrow while allowing external
// test implementations via the constructor.
type MutationSource = mutationSource

// NewMutationBridge creates a new mutation bridge. The source provides mutation
// data (typically a PooledBackend), and pub is the notification bus to publish to.
// Panics if source or pub is nil.
func NewMutationBridge(source MutationSource, pub notify.Publisher, cfg BridgeConfig) *MutationBridge {
	if source == nil {
		panic("beads.NewMutationBridge: source must not be nil")
	}
	if pub == nil {
		panic("beads.NewMutationBridge: pub must not be nil")
	}

	defaults := DefaultBridgeConfig()
	if cfg.WaitTimeout == 0 {
		cfg.WaitTimeout = defaults.WaitTimeout
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = defaults.RetryDelay
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &MutationBridge{
		source: source,
		pub:    pub,
		cfg:    cfg,
		logger: cfg.Logger,
	}
}

// Start begins the subscription loop in a background goroutine.
// Safe to call multiple times — only the first call starts the goroutine.
func (b *MutationBridge) Start() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.started {
		return
	}
	b.started = true

	if b.cfg.WorkspaceID == "" {
		b.logger.Warn("mutation bridge started with empty WorkspaceID; workspace-scoped subscribers will not receive events")
	}

	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	b.wg.Add(1)
	go func() {
		defer cancel() // idempotent; also called by Stop()
		b.run(ctx)
	}()

	b.logger.Info("mutation bridge started", "workspace_id", b.cfg.WorkspaceID)
}

// Stop gracefully stops the mutation bridge and waits for the goroutine to exit.
// Safe to call multiple times.
func (b *MutationBridge) Stop() {
	// Snapshot cancel under the lock to avoid racing with Start().
	b.mu.RLock()
	cancel := b.cancel
	b.mu.RUnlock()

	b.stopOnce.Do(func() {
		if cancel != nil {
			cancel()
		}
		b.wg.Wait()
		b.logger.Info("mutation bridge stopped")
	})
}

// LastSince returns the current mutation cursor (Unix ms). Callers can use this
// to implement catch-up on reconnection: call GetMutations(ctx, bridge.LastSince())
// to get any mutations that arrived while the caller was disconnected.
func (b *MutationBridge) LastSince() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastSince
}

// run is the main subscription loop.
func (b *MutationBridge) run(ctx context.Context) {
	defer b.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		b.waitForMutations(ctx)
	}
}

// waitForMutations uses the blocking WaitForMutations call.
func (b *MutationBridge) waitForMutations(ctx context.Context) {
	b.mu.RLock()
	since := b.lastSince
	b.mu.RUnlock()

	timeoutMs := int64(b.cfg.WaitTimeout / time.Millisecond)
	mutations, err := b.source.WaitForMutations(ctx, since, timeoutMs)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down
		}
		b.logger.Error("WaitForMutations error", "err", err)
		b.waitWithCtx(ctx, b.cfg.RetryDelay)
		return
	}

	b.processMutations(mutations)
}

// processMutations advances the cursor and publishes events for each mutation.
func (b *MutationBridge) processMutations(mutations []backend.MutationData) {
	if len(mutations) == 0 {
		return
	}

	// Single clock read for consistency when substituting zero timestamps.
	now := time.Now()

	// Calculate maxTimestamp across all mutations.
	var maxTimestamp int64
	for _, m := range mutations {
		ts := m.Timestamp.UnixMilli()
		if m.Timestamp.IsZero() {
			ts = now.UnixMilli()
		}
		if ts > maxTimestamp {
			maxTimestamp = ts
		}
	}

	// Update lastSince BEFORE publishing to prevent duplicate fetches.
	b.mu.Lock()
	if maxTimestamp >= b.lastSince {
		b.lastSince = maxTimestamp + 1
	}
	b.mu.Unlock()

	// Publish each mutation as a notify.Event.
	for _, m := range mutations {
		b.pub.Publish(b.mutationToEvent(m, now))
	}

	b.logger.Info("published mutations", "count", len(mutations))
}

// mutationToEvent converts a MutationData to a notify.Event.
// The now parameter is used as a fallback timestamp when m.Timestamp is zero,
// ensuring consistency with the cursor calculation in processMutations.
func (b *MutationBridge) mutationToEvent(m backend.MutationData, now time.Time) notify.Event {
	ts := m.Timestamp
	if ts.IsZero() {
		ts = now
	}
	return notify.Event{
		Topic:       mutationTopic(m.Type),
		WorkspaceID: b.cfg.WorkspaceID,
		Payload:     m,
		Timestamp:   ts,
	}
}

// mutationTopic returns the notify.Event topic for a mutation type.
// Format: "issue." + mutationType (e.g., "issue.create", "issue.status").
func mutationTopic(mutationType string) string {
	if mutationType == "" {
		return "issue.unknown"
	}
	return "issue." + mutationType
}

// waitWithCtx waits for the specified duration or until ctx is canceled.
func (b *MutationBridge) waitWithCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
