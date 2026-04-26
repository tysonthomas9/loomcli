package subscription

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

const (
	// subscriptionTimeout is the timeout for wait_for_mutations RPC call.
	subscriptionTimeout = 30 * time.Second

	// subscriptionRetryDelay is the delay before retrying after an error.
	subscriptionRetryDelay = 2 * time.Second

	// subscriptionAcquireTimeout is the timeout for acquiring a connection.
	subscriptionAcquireTimeout = 5 * time.Second

	// fallbackPollInterval is used when wait_for_mutations is not available.
	fallbackPollInterval = 100 * time.Millisecond

	// externalPollInterval is the interval for polling external DB changes
	// (writes made by CLI tools that bypass the daemon RPC mutation tracking).
	externalPollInterval = 3 * time.Second
)

// DaemonSubscriber manages the subscription to daemon mutations and
// bridges them to the SSE hub.
type DaemonSubscriber struct {
	pool        daemon.Pool
	hub         *realtime.Hub
	done        chan struct{}
	wg          sync.WaitGroup
	lastSince   int64
	mu          sync.RWMutex
	useFallback bool      // true if wait_for_mutations is not supported
	workspaceID string    // workspace ID to tag on all outgoing MutationPayloads
	startOnce   sync.Once // guard against double-start
	stopOnce    sync.Once // guard against double-close of done channel

	// External change detection fields
	lastKnownCount   int64
	lastPollTime     time.Time
	countInitialized bool

	// Tracks last-known state of issues for granular diff in pollDBChanges.
	// Key: issue ID, Value: lightweight snapshot used to determine mutation type.
	knownIssues map[string]knownIssueState
}

// NewDaemonSubscriber creates a new daemon subscriber.
func NewDaemonSubscriber(pool daemon.Pool, hub *realtime.Hub) *DaemonSubscriber {
	return &DaemonSubscriber{
		pool: pool,
		hub:  hub,
		done: make(chan struct{}),
	}
}

// Start begins the subscription loop in a goroutine.
// Safe to call multiple times — only the first call starts goroutines.
func (s *DaemonSubscriber) Start() {
	s.startOnce.Do(func() {
		s.wg.Add(2)
		go s.subscriptionLoop()
		go s.externalChangeLoop()
		slog.Info("daemon subscription started")
	})
}

// Stop gracefully stops the subscription loop.
// Safe to call multiple times.
func (s *DaemonSubscriber) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.wg.Wait()
		slog.Info("daemon subscription stopped")
	})
}

// GetMutationsSince retrieves mutations since the given timestamp.
// This is used for SSE client reconnection catch-up.
func (s *DaemonSubscriber) GetMutationsSince(since int64) []rpc.MutationEvent {
	if s.pool == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAcquireTimeout)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		slog.Error("failed to get connection", "op", "GetMutationsSince", "err", err)
		return nil
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			s.pool.Put(client)
		} else {
			s.pool.Discard(client)
		}
	}()

	resp, err := client.GetMutations(&rpc.GetMutationsArgs{Since: since})
	if err != nil {
		slog.Error("RPC error", "op", "GetMutationsSince", "err", err)
		return nil
	}
	rpcOK = true

	if !resp.Success {
		slog.Error("RPC failed", "op", "GetMutationsSince", "err", resp.Error)
		return nil
	}

	var mutations []rpc.MutationEvent
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		slog.Error("parse error", "op", "GetMutationsSince", "err", err)
		return nil
	}

	return mutations
}

// GetMutationDataSince is the workspaceSubscriber-shaped wrapper around
// GetMutationsSince. It exists so DaemonSubscriber can sit alongside
// BackendMutationSubscriber in MultiWorkspaceSubscriber's registry without
// renaming the existing GetMutationsSince (which other tests depend on).
// Each rpc.MutationEvent is projected via realtime.RPCEventToMutationData
// so the catch-up path returns a single uniform type regardless of source.
func (s *DaemonSubscriber) GetMutationDataSince(since int64) []backend.MutationData {
	events := s.GetMutationsSince(since)
	if len(events) == 0 {
		return nil
	}
	out := make([]backend.MutationData, len(events))
	for i, e := range events {
		out[i] = realtime.RPCEventToMutationData(e)
	}
	return out
}

// subscriptionLoop continuously polls/waits for mutations from the daemon.
func (s *DaemonSubscriber) subscriptionLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.done:
			return
		default:
		}

		if s.pool == nil {
			// No pool available, wait and retry
			s.waitWithDone(subscriptionRetryDelay)
			continue
		}

		// Try to get mutations using wait_for_mutations or fallback polling
		s.mu.RLock()
		useFallback := s.useFallback
		s.mu.RUnlock()

		if useFallback {
			s.pollMutations()
		} else {
			s.waitForMutations()
		}
	}
}

// waitForMutations uses the blocking wait_for_mutations RPC.
func (s *DaemonSubscriber) waitForMutations() {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAcquireTimeout)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		// Connection error, wait and retry
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

	s.mu.RLock()
	since := s.lastSince
	s.mu.RUnlock()

	args := &rpc.WaitForMutationsArgs{
		Since:   since,
		Timeout: int64(subscriptionTimeout / time.Millisecond),
	}

	resp, err := client.WaitForMutations(args)
	if err != nil {
		// Discard the connection on error — after a timeout or error, the
		// connection may have a stale response in the pipe, making it unsafe
		// to reuse. Discarding forces a fresh connection next time.
		s.pool.Discard(client)

		// Check if this is an "unknown operation" error indicating the daemon
		// doesn't support wait_for_mutations
		if isUnknownOperationError(err) {
			slog.Warn("daemon does not support wait_for_mutations, falling back to polling")
			s.mu.Lock()
			s.useFallback = true
			s.mu.Unlock()
			return
		}

		slog.Error("wait for mutations error", "err", err)
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

	// Success — return connection to pool for reuse
	s.pool.Put(client)

	if !resp.Success {
		slog.Error("wait for mutations failed", "err", resp.Error)
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

	s.processMutationResponse(resp)
}

// pollMutations uses the non-blocking get_mutations RPC as a fallback.
func (s *DaemonSubscriber) pollMutations() {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAcquireTimeout)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		s.waitWithDone(subscriptionRetryDelay)
		return
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			s.pool.Put(client)
		} else {
			s.pool.Discard(client)
		}
	}()

	s.mu.RLock()
	since := s.lastSince
	s.mu.RUnlock()

	resp, err := client.GetMutations(&rpc.GetMutationsArgs{Since: since})
	if err != nil {
		slog.Error("get mutations error", "err", err)
		s.waitWithDone(subscriptionRetryDelay)
		return
	}
	rpcOK = true

	if !resp.Success {
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

	s.processMutationResponse(resp)

	// Wait for next poll interval
	s.waitWithDone(fallbackPollInterval)
}

// processMutationResponse handles the response from get/wait_for_mutations.
func (s *DaemonSubscriber) processMutationResponse(resp *rpc.Response) {
	var mutations []rpc.MutationEvent
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		slog.Error("failed to parse mutations", "err", err)
		return
	}

	if len(mutations) == 0 {
		return
	}

	// Calculate maxTimestamp first
	var maxTimestamp int64
	for _, m := range mutations {
		ts := m.Timestamp.UnixMilli()
		if ts > maxTimestamp {
			maxTimestamp = ts
		}
	}

	// Update lastSince BEFORE broadcasting to prevent concurrent goroutines
	// from requesting duplicate mutations with a stale since value.
	// The daemon's GetRecentMutations uses strict ">" comparison, so setting
	// lastSince = maxTimestamp ensures the next call returns mutations
	// strictly after maxTimestamp without skipping anything at maxTimestamp+1.
	if maxTimestamp > 0 {
		s.mu.Lock()
		if maxTimestamp > s.lastSince {
			s.lastSince = maxTimestamp
		}
		s.mu.Unlock()
	}

	// Broadcast each mutation to SSE clients
	for _, m := range mutations {
		payload := realtime.RPCMutationToPayload(m)
		if s.workspaceID != "" {
			payload.WorkspaceID = s.workspaceID
		}
		s.hub.Broadcast(payload)
	}

	slog.Info("broadcast mutations to SSE clients", "count", len(mutations), "clients", s.hub.ClientCount())
}

// externalChangeLoop periodically polls the database for changes made outside
// the daemon RPC mutation tracking (e.g., by the bd CLI writing directly to SQLite).
func (s *DaemonSubscriber) externalChangeLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.done:
			return
		case <-time.After(externalPollInterval):
		}

		if s.pool == nil {
			continue
		}

		s.pollDBChanges()
	}
}

// pollDBChanges checks the database for external changes by comparing issue counts
// and checking for recently updated issues via the Count RPC.
// When changes are detected, it emits granular per-issue mutations instead of a
// blanket MutationRefresh, falling back to MutationRefresh only for deletions,
// List RPC failures, or when too many issues changed at once.
func (s *DaemonSubscriber) pollDBChanges() {
	ctx, cancel := context.WithTimeout(context.Background(), subscriptionAcquireTimeout)
	defer cancel()

	client, err := s.pool.Get(ctx)
	if err != nil {
		return
	}
	rpcOK := false
	defer func() {
		if rpcOK {
			s.pool.Put(client)
		} else {
			s.pool.Discard(client)
		}
	}()

	totalCount, ok := s.fetchTotalCount(client)
	if !ok {
		return
	}
	rpcOK = true

	now := time.Now()

	// First poll: initialize state without broadcasting
	if s.initPollStateIfNeeded(client, totalCount, now) {
		return
	}

	changeDetected, deletionDetected, lastPollTime := s.detectExternalChanges(client, totalCount)

	if !changeDetected {
		s.mu.Lock()
		s.lastPollTime = now
		s.mu.Unlock()
		return
	}

	s.handleExternalChanges(client, now, totalCount, lastPollTime, deletionDetected)
}

// fetchTotalCount calls the Count RPC and returns the total issue count.
// Returns (0, false) if the RPC fails or the response cannot be parsed.
func (s *DaemonSubscriber) fetchTotalCount(client *rpc.Client) (int64, bool) {
	resp, err := client.Count(&rpc.CountArgs{})
	if err != nil {
		slog.Error("external poll: count error", "err", err)
		return 0, false
	}
	if !resp.Success {
		return 0, false
	}
	var countResult struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &countResult); err != nil {
		slog.Error("external poll: parse count error", "err", err)
		return 0, false
	}
	return countResult.Count, true
}

// initPollStateIfNeeded initializes poll state on the first invocation.
// Returns true if this was the first poll (caller should return early).
func (s *DaemonSubscriber) initPollStateIfNeeded(client *rpc.Client, totalCount int64, now time.Time) bool {
	s.mu.Lock()
	if s.countInitialized {
		s.mu.Unlock()
		return false
	}
	s.lastKnownCount = totalCount
	s.lastPollTime = now
	s.countInitialized = true
	s.mu.Unlock()
	// Build initial knownIssues snapshot (best-effort, non-blocking)
	s.loadKnownIssues(client)
	return true
}

// detectExternalChanges compares the current count against the last known count
// and checks for in-place updates via the Count(UpdatedAfter) RPC.
func (s *DaemonSubscriber) detectExternalChanges(client *rpc.Client, totalCount int64) (changed, deleted bool, lastPoll time.Time) {
	s.mu.RLock()
	lastKnown := s.lastKnownCount
	lastPoll = s.lastPollTime
	s.mu.RUnlock()

	if totalCount != lastKnown {
		return true, totalCount < lastKnown, lastPoll
	}
	// Count unchanged — check for in-place updates
	updatedAfter := lastPoll.UTC().Format(time.RFC3339)
	resp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter})
	if err == nil && resp.Success {
		var result struct {
			Count int64 `json:"count"`
		}
		if err := json.Unmarshal(resp.Data, &result); err == nil && result.Count > 0 {
			return true, false, lastPoll
		}
	}
	return false, false, lastPoll
}

// handleExternalChanges responds to detected external DB changes by emitting
// granular per-issue mutations or falling back to a blanket refresh.
func (s *DaemonSubscriber) handleExternalChanges(client *rpc.Client, now time.Time, totalCount int64, lastPollTime time.Time, deletionDetected bool) {
	// Deletion detected — fall back to global MutationRefresh.
	if deletionDetected {
		s.broadcastRefresh(now, totalCount)
		s.loadKnownIssues(client)
		return
	}

	changed := s.fetchChangedIssues(client, lastPollTime)
	if changed == nil || len(changed) > granularMutationThreshold {
		// List RPC failed or too many changes — fall back to per-repo refresh.
		s.emitPerRepoRefreshes(client, now, lastPollTime, totalCount)
		s.mu.Lock()
		s.lastKnownCount = totalCount
		s.lastPollTime = now
		s.mu.Unlock()
		if changed != nil {
			s.loadKnownIssues(client)
		}
		return
	}

	s.emitGranularMutations(changed, now, totalCount)
}

// waitWithDone waits for the specified duration or until done is signaled.
func (s *DaemonSubscriber) waitWithDone(d time.Duration) {
	select {
	case <-s.done:
	case <-time.After(d):
	}
}

// isUnknownOperationError checks if an error indicates the operation is unknown.
func isUnknownOperationError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return len(errStr) > 0 &&
		(strings.Contains(errStr, "unknown operation") || strings.Contains(errStr, "unsupported"))
}
