package main

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/beads/examples/beads-web-ui/daemon"
	"github.com/steveyegge/beads/internal/rpc"
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
)

// DaemonSubscriber manages the subscription to daemon mutations and
// bridges them to the SSE hub.
type DaemonSubscriber struct {
	pool        *daemon.ConnectionPool
	hub         *SSEHub
	done        chan struct{}
	wg          sync.WaitGroup
	lastSince   int64
	mu          sync.RWMutex
	useFallback bool // true if wait_for_mutations is not supported
}

// NewDaemonSubscriber creates a new daemon subscriber.
func NewDaemonSubscriber(pool *daemon.ConnectionPool, hub *SSEHub) *DaemonSubscriber {
	return &DaemonSubscriber{
		pool: pool,
		hub:  hub,
		done: make(chan struct{}),
	}
}

// Start begins the subscription loop in a goroutine.
func (s *DaemonSubscriber) Start() {
	s.wg.Add(1)
	go s.subscriptionLoop()
	log.Printf("Daemon subscription started")
}

// Stop gracefully stops the subscription loop.
func (s *DaemonSubscriber) Stop() {
	close(s.done)
	s.wg.Wait()
	log.Printf("Daemon subscription stopped")
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
		s.hub.Broadcast(payload)
	}

	log.Printf("Broadcast %d mutations to %d SSE clients", len(mutations), s.hub.ClientCount())
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
