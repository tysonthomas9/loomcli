package subscription

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	// backendWaitTimeout is the long-poll timeout passed to
	// IssueBackend.WaitForMutations. fleet-db caps the server-side
	// timeout at 10 s (see fleet-db internal/api/mutations.go
	// mutationsMaxTimeout) to bound how long XREAD BLOCK 0 holds a
	// Redis pool connection. Anything > 10 s is rejected as a
	// validation error. Browser SSE reconnect cadence is dominated by
	// the FE's exponential backoff anyway, so this 10 s ceiling is
	// invisible to clients.
	backendWaitTimeout = 10 * time.Second

	// backendRetryDelay is the backoff applied after a non-cancellation
	// error from WaitForMutations (e.g., transient HTTP failure). Matches
	// the beads path's subscriptionRetryDelay for parity.
	backendRetryDelay = 2 * time.Second

	// backendCatchUpTimeout caps the GetMutations call used by the
	// SSE reconnect catch-up path. Catch-up runs synchronously inside the
	// SSE handler; bound it tightly so a slow backend cannot stall the
	// initial event stream open.
	backendCatchUpTimeout = 5 * time.Second
)

// BackendMutationSubscriber is the fleet-mode sibling of DaemonSubscriber.
// It sources mutation events from a backend.IssueBackend.WaitForMutations
// long-poll and bridges them onto the same realtime.Hub the bd daemon path
// uses. One goroutine per workspace; the loop exits when ctx is canceled
// (Stop()).
type BackendMutationSubscriber struct {
	backend     backend.IssueBackend
	hub         *realtime.Hub
	workspaceID string

	wg sync.WaitGroup

	mu         sync.RWMutex
	lastSince  int64
	lastCursor string

	startOnce sync.Once
	stopOnce  sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

// NewBackendMutationSubscriber creates a subscriber that long-polls the given
// IssueBackend for mutations and broadcasts them to hub. workspaceID is
// stamped onto every outgoing MutationPayload so the hub's per-client
// workspace filter routes events correctly. b and hub must not be nil.
func NewBackendMutationSubscriber(b backend.IssueBackend, hub *realtime.Hub, workspaceID string) *BackendMutationSubscriber {
	ctx, cancel := context.WithCancel(context.Background())
	return &BackendMutationSubscriber{
		backend:     b,
		hub:         hub,
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start begins the long-poll loop in a background goroutine.
// Safe to call multiple times — only the first call spawns a goroutine.
func (s *BackendMutationSubscriber) Start() {
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.loop()
		slog.Info("backend mutation subscription started", "workspace", s.workspaceID)
	})
}

// Stop gracefully tears down the subscriber. Cancels the embedded context
// (unblocks any in-flight WaitForMutations) and waits for the goroutine
// to exit. Safe to call multiple times.
func (s *BackendMutationSubscriber) Stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
		slog.Info("backend mutation subscription stopped", "workspace", s.workspaceID)
	})
}

// GetMutationDataSince returns mutations from the backend with timestamps
// strictly greater than since (ms epoch). Used by the SSE reconnect
// catch-up path; runs synchronously and is bounded by backendCatchUpTimeout.
// Returns nil on backend error so the caller can fall through to the
// connected event without aborting the SSE stream.
//
// Method name matches the workspaceSubscriber interface in multi.go (the
// daemon-path sibling has its own *DaemonSubscriber.GetMutationsSince
// returning []rpc.MutationEvent, which is depended on by older tests; the
// two cannot share a name).
func (s *BackendMutationSubscriber) GetMutationDataSince(since string) []backend.MutationData {
	if s.backend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), backendCatchUpTimeout)
	defer cancel()
	var (
		muts []backend.MutationData
		err  error
	)
	if cursorBackend, ok := s.backend.(backend.CursorMutationBackend); ok {
		muts, err = cursorBackend.GetMutationsAfter(ctx, since)
	} else {
		muts, err = s.backend.GetMutations(ctx, parseCursorMillis(since))
	}
	if err != nil {
		slog.Error("backend GetMutations error", "workspace", s.workspaceID, "err", err)
		return nil
	}
	return muts
}

// loop is the long-poll body. Blocks on WaitForMutations for up to
// backendWaitTimeout per iteration. On each non-empty response it advances
// lastSince to the max timestamp BEFORE broadcasting (mirrors the daemon
// path's invariant: a concurrent reader of lastSince must never see a
// stale cursor while events from that batch are still being broadcast).
func (s *BackendMutationSubscriber) loop() {
	defer s.wg.Done()

	for {
		if s.ctx.Err() != nil {
			return
		}

		s.mu.RLock()
		since := s.lastSince
		cursor := s.lastCursor
		s.mu.RUnlock()
		if cursor == "" {
			cursor = "0"
		}

		timeoutMs := int64(backendWaitTimeout / time.Millisecond)
		// Wrap the long-poll in a per-call deadline that exceeds the
		// server-side timeout by a slack window. With the shared HTTP
		// client's Timeout set to 65s (see fleet.SharedHTTPClient), this
		// per-call context is what actually unblocks WaitForMutations on
		// timeout — eliminating the 30s vs 30s race that surfaced as
		// `context canceled` log spam on every empty long-poll.
		reqCtx, reqCancel := context.WithTimeout(s.ctx, backendWaitTimeout+10*time.Second)
		var muts []backend.MutationData
		var err error
		if cursorBackend, ok := s.backend.(backend.CursorMutationBackend); ok {
			muts, err = cursorBackend.WaitForMutationsAfter(reqCtx, cursor, timeoutMs)
		} else {
			muts, err = s.backend.WaitForMutations(reqCtx, since, timeoutMs)
		}
		reqCancel()
		if err != nil {
			// Cancellation is the expected exit path on Stop(); don't
			// retry-spin on it.
			if errors.Is(err, context.Canceled) || s.ctx.Err() != nil {
				return
			}
			slog.Error("backend WaitForMutations error", "workspace", s.workspaceID, "err", err)
			s.waitWithCancel(backendRetryDelay)
			continue
		}

		if len(muts) == 0 {
			// Long-poll returned no mutations. Apply a small client-side
			// back-off before re-entering: under fleet-db pool pressure
			// the server returns early empty 200s (well before its 30s
			// timeout), and a tight loop pegs both subscriber CPU and the
			// downstream Redis pool. 250ms caps re-entry at 4/s in that
			// degraded mode while still being invisible in the steady
			// state where the server honors the full long-poll window.
			s.waitWithCancel(250 * time.Millisecond)
			continue
		}

		// Compute max timestamp first so lastSince is advanced before
		// the first broadcast. A concurrent GetMutationsSince(N) would
		// otherwise re-fetch events still in flight.
		var maxMs int64
		lastCursor := ""
		for _, m := range muts {
			ms := m.Timestamp.UnixMilli()
			if ms > maxMs {
				maxMs = ms
			}
			if m.Cursor != "" {
				lastCursor = m.Cursor
			}
		}
		if maxMs > 0 || lastCursor != "" {
			s.mu.Lock()
			if maxMs > s.lastSince {
				s.lastSince = maxMs
			}
			if lastCursor != "" {
				s.lastCursor = lastCursor
			}
			s.mu.Unlock()
		}

		for _, m := range muts {
			payload := realtime.BackendMutationToPayload(m, s.workspaceID)
			s.hub.Broadcast(payload)
		}
		slog.Info("broadcast backend mutations to SSE clients",
			"workspace", s.workspaceID, "count", len(muts), "clients", s.hub.ClientCount())
	}
}

// waitWithCancel sleeps for d or until the embedded context is canceled,
// whichever comes first. Mirrors DaemonSubscriber.waitWithDone but keys
// off ctx.Done() because Stop() cancels ctx before closing done.
func (s *BackendMutationSubscriber) waitWithCancel(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
	case <-t.C:
	}
}
