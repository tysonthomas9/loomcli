package subscription

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	// mutationWaitTimeout is the long-poll timeout passed to the Work Items
	// mutation stream. FleetDB caps the server-side
	// timeout at 10 s (see fleet-db internal/api/mutations.go
	// mutationsMaxTimeout) to bound how long XREAD BLOCK 0 holds a
	// Redis pool connection. Anything > 10 s is rejected as a
	// validation error. Browser SSE reconnect cadence is dominated by
	// the FE's exponential backoff anyway, so this 10 s ceiling is
	// invisible to clients.
	mutationWaitTimeout = 10 * time.Second

	// mutationRetryDelay is the backoff applied after a non-cancellation
	// stream error (for example, a transient HTTP failure).
	mutationRetryDelay = 2 * time.Second

	// mutationEmptyPollDelay is a client-side backoff after an empty mutation
	// poll. Healthy fleet-db long-polls usually return after the server-side
	// wait timeout, so this is invisible in the steady state. When the
	// downstream Redis pool is under pressure and empty polls return early,
	// this prevents the subscriber from immediately re-entering the pool.
	mutationEmptyPollDelay = time.Second

	// mutationCatchUpTimeout caps the catch-up call used by the
	// SSE reconnect catch-up path. Catch-up runs synchronously inside the
	// SSE handler; bound it tightly so a slow backend cannot stall the
	// initial event stream open.
	mutationCatchUpTimeout = 5 * time.Second
)

// WorkItemMutationSubscriber bridges Work Items mutations onto the shared
// realtime.Hub. One goroutine runs per active workspace; the loop exits when
// ctx is canceled by Stop.
type WorkItemMutationSubscriber struct {
	stream      workitems.MutationStream
	hub         *realtime.Hub
	workspaceID string

	wg sync.WaitGroup

	mu         sync.RWMutex
	lastCursor string

	startOnce sync.Once
	stopOnce  sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

// NewWorkItemMutationSubscriber creates a subscriber that long-polls the given
// Work Items stream and broadcasts its mutations to hub. workspaceID is
// stamped onto every outgoing MutationPayload so the hub's per-client
// workspace filter routes events correctly. stream and hub must not be nil.
func NewWorkItemMutationSubscriber(stream workitems.MutationStream, hub *realtime.Hub, workspaceID string) *WorkItemMutationSubscriber {
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // Stop owns cancellation for the subscriber lifetime.
	return &WorkItemMutationSubscriber{
		stream:      stream,
		hub:         hub,
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins the long-poll loop in a background goroutine.
// Safe to call multiple times — only the first call spawns a goroutine.
func (s *WorkItemMutationSubscriber) Start() {
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.loop()
		slog.Info("work item mutation subscription started", "workspace", s.workspaceID)
	})
}

// Stop gracefully tears down the subscriber. Cancels the embedded context
// (unblocks any in-flight WaitForMutations) and waits for the goroutine
// to exit. Safe to call multiple times.
func (s *WorkItemMutationSubscriber) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
		slog.Info("work item mutation subscription stopped", "workspace", s.workspaceID)
	})
}

// GetMutationsAfter returns mutations after the opaque cursor. It is used by
// the SSE reconnect catch-up path and bounded by mutationCatchUpTimeout.
// It returns nil on stream error so the caller can fall through to the
// connected event without aborting the SSE stream.
//
// Method name matches the workspaceSubscriber interface in multi.go.
func (s *WorkItemMutationSubscriber) GetMutationsAfter(cursor string) []workitems.Mutation {
	if s.stream == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), mutationCatchUpTimeout)
	defer cancel()
	var (
		muts []workitems.Mutation
		err  error
	)
	muts, err = s.stream.GetMutationsAfter(ctx, cursor)
	if err != nil {
		slog.Error("work item mutation catch-up failed", "workspace", s.workspaceID, "err", err)
		return nil
	}
	return muts
}

// loop is the long-poll body. Blocks on WaitForMutationsAfter for up to
// mutationWaitTimeout per iteration. On each non-empty response it advances
// the opaque cursor before broadcasting, so concurrent catch-up cannot fetch
// events that are already being broadcast.
//
//nolint:gocognit,funlen // Subscription retry/cursor bookkeeping is easier to audit in one loop.
func (s *WorkItemMutationSubscriber) loop() {
	defer s.wg.Done()

	for {
		if s.ctx.Err() != nil {
			return
		}

		s.mu.RLock()
		cursor := s.lastCursor
		s.mu.RUnlock()
		if cursor == "" {
			cursor = "0"
		}

		timeoutMs := int64(mutationWaitTimeout / time.Millisecond)
		// Wrap the long-poll in a per-call deadline that exceeds the
		// server-side timeout by a slack window. With the shared HTTP
		// client's Timeout set to 65s (see fleet.SharedHTTPClient), this
		// per-call context is what actually unblocks WaitForMutations on
		// timeout — eliminating the 30s vs 30s race that surfaced as
		// `context canceled` log spam on every empty long-poll.
		muts, err := s.waitForMutations(cursor, timeoutMs)
		if err != nil {
			// Cancellation is the expected exit path on Stop(); don't
			// retry-spin on it.
			if errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
				return
			}
			slog.Error("work item mutation wait failed", "workspace", s.workspaceID, "err", err)
			s.waitWithCancel(mutationRetryDelay)
			continue
		}

		if len(muts) == 0 {
			// Long-poll returned no mutations. Apply a small client-side
			// back-off before re-entering: under fleet-db pool pressure
			// the server returns early empty 200s (well before its 30s
			// timeout), and a tight loop competes with normal workspace API
			// traffic for the downstream Redis pool. This caps re-entry in that
			// degraded mode while still being invisible in the steady
			// state where the server honors the full long-poll window.
			s.waitWithCancel(mutationEmptyPollDelay)
			continue
		}

		// Advance the exact FleetDB cursor before the first broadcast.
		lastCursor := ""
		for _, m := range muts {
			if m.Cursor != "" {
				lastCursor = m.Cursor
			}
		}
		if lastCursor != "" {
			s.mu.Lock()
			if lastCursor != "" {
				s.lastCursor = lastCursor
			}
			s.mu.Unlock()
		}

		for _, m := range muts {
			payload := realtime.WorkItemMutationToPayload(m, s.workspaceID)
			s.hub.Broadcast(payload)
		}
		slog.Info("broadcast work item mutations to SSE clients",
			"workspace", s.workspaceID, "count", len(muts), "clients", s.hub.ClientCount())
	}
}

func (s *WorkItemMutationSubscriber) waitForMutations(cursor string, timeoutMs int64) ([]workitems.Mutation, error) {
	reqCtx, reqCancel := context.WithTimeout(s.ctx, mutationWaitTimeout+10*time.Second)
	defer reqCancel()
	return s.stream.WaitForMutationsAfter(reqCtx, cursor, timeoutMs)
}

// waitWithCancel sleeps for d or until the embedded context is canceled,
// whichever comes first.
func (s *WorkItemMutationSubscriber) waitWithCancel(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
	case <-t.C:
	}
}
