package webui

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
	hub         *SSEHub
	done        chan struct{}
	wg          sync.WaitGroup
	lastSince   int64
	mu          sync.RWMutex
	useFallback bool      // true if wait_for_mutations is not supported
	workspaceID string    // workspace ID to tag on all outgoing MutationPayloads
	started     bool      // guard against double-start
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
func NewDaemonSubscriber(pool daemon.Pool, hub *SSEHub) *DaemonSubscriber {
	return &DaemonSubscriber{
		pool: pool,
		hub:  hub,
		done: make(chan struct{}),
	}
}

// Start begins the subscription loop in a goroutine.
// Safe to call multiple times — only the first call starts goroutines.
func (s *DaemonSubscriber) Start() {
	if s.started {
		return
	}
	s.started = true
	s.wg.Add(2)
	go s.subscriptionLoop()
	go s.externalChangeLoop()
	log.Printf("Daemon subscription started")
}

// Stop gracefully stops the subscription loop.
// Safe to call multiple times.
func (s *DaemonSubscriber) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.wg.Wait()
		log.Printf("Daemon subscription stopped")
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
		log.Printf("GetMutationsSince: failed to get connection: %v", err)
		return nil
	}
	defer s.pool.Put(client)

	resp, err := client.GetMutations(&rpc.GetMutationsArgs{Since: since})
	if err != nil {
		log.Printf("GetMutationsSince: RPC error: %v", err)
		return nil
	}

	if !resp.Success {
		log.Printf("GetMutationsSince: RPC failed: %s", resp.Error)
		return nil
	}

	var mutations []rpc.MutationEvent
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		log.Printf("GetMutationsSince: parse error: %v", err)
		return nil
	}

	return mutations
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
			log.Printf("Daemon does not support wait_for_mutations, falling back to polling")
			s.mu.Lock()
			s.useFallback = true
			s.mu.Unlock()
			return
		}

		log.Printf("WaitForMutations error: %v", err)
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

	// Success — return connection to pool for reuse
	s.pool.Put(client)

	if !resp.Success {
		log.Printf("WaitForMutations failed: %s", resp.Error)
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
	defer s.pool.Put(client)

	s.mu.RLock()
	since := s.lastSince
	s.mu.RUnlock()

	resp, err := client.GetMutations(&rpc.GetMutationsArgs{Since: since})
	if err != nil {
		log.Printf("GetMutations error: %v", err)
		s.waitWithDone(subscriptionRetryDelay)
		return
	}

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
		log.Printf("Failed to parse mutations: %v", err)
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
	if maxTimestamp > 0 {
		s.mu.Lock()
		if maxTimestamp >= s.lastSince {
			s.lastSince = maxTimestamp + 1
		}
		s.mu.Unlock()
	}

	// Broadcast each mutation to SSE clients
	for _, m := range mutations {
		payload := rpcMutationToPayload(m)
		if s.workspaceID != "" {
			payload.WorkspaceID = s.workspaceID
		}
		s.hub.Broadcast(payload)
	}

	log.Printf("Broadcast %d mutations to %d SSE clients", len(mutations), s.hub.ClientCount())
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
	defer s.pool.Put(client)

	// Get total issue count
	resp, err := client.Count(&rpc.CountArgs{})
	if err != nil {
		log.Printf("External poll: count error: %v", err)
		return
	}
	if !resp.Success {
		return
	}

	var countResult struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &countResult); err != nil {
		log.Printf("External poll: parse count error: %v", err)
		return
	}
	totalCount := countResult.Count

	now := time.Now()

	// First poll: initialize state without broadcasting
	s.mu.Lock()
	if !s.countInitialized {
		s.lastKnownCount = totalCount
		s.lastPollTime = now
		s.countInitialized = true
		s.mu.Unlock()
		// Build initial knownIssues snapshot (best-effort, non-blocking)
		s.loadKnownIssues(client)
		return
	}

	changeDetected := false
	deletionDetected := false

	// Check if total count changed (issue created or deleted externally)
	if totalCount != s.lastKnownCount {
		changeDetected = true
		if totalCount < s.lastKnownCount {
			deletionDetected = true
		}
	}

	lastPollTime := s.lastPollTime
	s.mu.Unlock()

	// Check for in-place updates (count same but issues modified)
	if !changeDetected {
		updatedAfter := lastPollTime.UTC().Format(time.RFC3339)
		updatedResp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter})
		if err == nil && updatedResp.Success {
			var updatedResult struct {
				Count int64 `json:"count"`
			}
			if err := json.Unmarshal(updatedResp.Data, &updatedResult); err == nil && updatedResult.Count > 0 {
				changeDetected = true
			}
		}
	}

	if !changeDetected {
		s.mu.Lock()
		s.lastPollTime = now
		s.mu.Unlock()
		return
	}

	// Deletion detected — fall back to MutationRefresh (tracking all IDs for
	// granular delete detection adds memory/complexity not worth the trade-off).
	if deletionDetected {
		s.broadcastRefresh(now, totalCount)
		// Rebuild knownIssues snapshot after refresh
		s.loadKnownIssues(client)
		return
	}

	// Try granular mutations: fetch changed issues via List RPC
	changed := s.fetchChangedIssues(client, lastPollTime)
	if changed == nil {
		// List RPC failed — fall back to MutationRefresh
		s.broadcastRefresh(now, totalCount)
		return
	}

	if len(changed) > granularMutationThreshold {
		// Too many changes — fall back to MutationRefresh to avoid SSE congestion
		s.broadcastRefresh(now, totalCount)
		s.loadKnownIssues(client)
		return
	}

	// Emit granular mutations and update tracking state
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
