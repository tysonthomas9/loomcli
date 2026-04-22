package subscription

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

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

	// Agent state file watcher fields (protected by agentStateMu).
	agentStatePath  string       // path to daemon-agents.json; empty = watcher is a no-op
	agentStateMtime time.Time    // last observed mtime of daemon-agents.json
	agentStateMu    sync.RWMutex // protects agentStatePath and agentStateMtime
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
		s.wg.Add(3)
		go s.subscriptionLoop()
		go s.externalChangeLoop()
		go s.agentStateWatchLoop()
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

// subscriptionLoop continuously waits for mutations from the daemon.
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

		s.waitForMutations()
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
//
// Connection-pool discipline (ref: loomcli-67meg): rpcOK is set true only after
// every RPC on `client` has completed at the transport level — i.e. the daemon
// sent a full response frame. A transport-level failure (err != nil && resp == nil
// from any client.X call) may leave unread bytes on the wire, so the connection
// must be Discarded, not Put back. This matches the invariant documented in
// waitForMutations (subscriber.go:178-188).
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

	totalCount, countOK, healthy := s.fetchTotalCount(client)
	if !healthy {
		return // Discard: transport error left bytes on the wire
	}
	if !countOK {
		rpcOK = true // Clean logical failure — connection intact
		return
	}

	now := time.Now()

	// First poll: initialize state without broadcasting
	firstPoll, initHealthy := s.initPollStateIfNeeded(client, totalCount, now)
	if firstPoll {
		rpcOK = initHealthy
		return
	}

	changeDetected, deletionDetected, lastPollTime, detectHealthy := s.detectExternalChanges(client, totalCount)
	if !detectHealthy {
		return // Discard
	}

	if !changeDetected {
		s.mu.Lock()
		s.lastPollTime = now
		s.mu.Unlock()
		rpcOK = true
		return
	}

	rpcOK = s.handleExternalChanges(client, now, totalCount, lastPollTime, deletionDetected)
}

// fetchTotalCount calls the Count RPC and returns the total issue count.
//
// Returns (count, countOK, clientHealthy):
//   - countOK == false when the logical result is unusable (RPC failure,
//     Success=false, or JSON parse error). In these cases count is 0.
//   - clientHealthy == false when the RPC call returned a transport error
//     with no response frame (resp == nil && err != nil). In that state the
//     connection's read buffer may hold partial bytes and the caller must
//     Discard it. Parse errors of a fully-received body keep the connection
//     healthy.
func (s *DaemonSubscriber) fetchTotalCount(client *rpc.Client) (count int64, countOK, clientHealthy bool) {
	resp, err := client.Count(&rpc.CountArgs{})
	if err != nil {
		if resp == nil {
			slog.Error("external poll: count transport error", "err", err)
			return 0, false, false
		}
		// resp != nil — the daemon sent a full "Success: false" response;
		// connection is intact, just a logical failure.
		return 0, false, true
	}
	if !resp.Success {
		return 0, false, true
	}
	var countResult struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &countResult); err != nil {
		// Parse error on a fully-received body — connection is fine.
		slog.Error("external poll: parse count error", "err", err)
		return 0, false, true
	}
	return countResult.Count, true, true
}

// initPollStateIfNeeded initializes poll state on the first invocation.
//
// Returns (firstPoll, clientHealthy):
//   - firstPoll == true on the initial call; the caller should return early.
//   - clientHealthy reflects the health of the inner loadKnownIssues RPC on
//     first poll; on subsequent calls it is always true (no RPC performed).
func (s *DaemonSubscriber) initPollStateIfNeeded(client *rpc.Client, totalCount int64, now time.Time) (firstPoll, clientHealthy bool) {
	s.mu.Lock()
	if s.countInitialized {
		s.mu.Unlock()
		return false, true
	}
	s.lastKnownCount = totalCount
	s.lastPollTime = now
	s.countInitialized = true
	s.mu.Unlock()
	// Build initial knownIssues snapshot (best-effort, non-blocking).
	// loadKnownIssues surfaces its transport-health bit so a first-poll List
	// failure correctly Discards the connection instead of poisoning the pool.
	return true, s.loadKnownIssues(client)
}

// detectExternalChanges compares the current count against the last known count
// and checks for in-place updates via the Count(UpdatedAfter) RPC. Returns
// (changed, deleted, lastPoll, clientHealthy). clientHealthy is false only if
// the inner Count RPC hit a transport error.
func (s *DaemonSubscriber) detectExternalChanges(client *rpc.Client, totalCount int64) (changed, deleted bool, lastPoll time.Time, clientHealthy bool) {
	s.mu.RLock()
	lastKnown := s.lastKnownCount
	lastPoll = s.lastPollTime
	s.mu.RUnlock()

	if totalCount != lastKnown {
		return true, totalCount < lastKnown, lastPoll, true
	}
	// Count unchanged — check for in-place updates
	updatedAfter := lastPoll.UTC().Format(time.RFC3339)
	resp, err := client.Count(&rpc.CountArgs{UpdatedAfter: updatedAfter})
	if err != nil {
		if resp == nil {
			// Transport error — connection unsafe, caller must Discard.
			return false, false, lastPoll, false
		}
		// Success=false — logical failure, connection intact.
		return false, false, lastPoll, true
	}
	if !resp.Success {
		return false, false, lastPoll, true
	}
	var result struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &result); err == nil && result.Count > 0 {
		return true, false, lastPoll, true
	}
	return false, false, lastPoll, true
}

// handleExternalChanges responds to detected external DB changes by emitting
// granular per-issue mutations or falling back to a blanket refresh. Returns
// clientHealthy: false if any inner RPC on `client` hit a transport error,
// requiring the caller to Discard the connection.
func (s *DaemonSubscriber) handleExternalChanges(client *rpc.Client, now time.Time, totalCount int64, lastPollTime time.Time, deletionDetected bool) (clientHealthy bool) {
	// Deletion detected — fall back to global MutationRefresh.
	if deletionDetected {
		s.broadcastRefresh(now, totalCount)
		return s.loadKnownIssues(client)
	}

	changed, fetchHealthy := s.fetchChangedIssues(client, lastPollTime)
	if !fetchHealthy {
		return false
	}

	if changed == nil || len(changed) > granularMutationThreshold {
		// List RPC logically failed or too many changes — fall back to per-repo refresh.
		repoHealthy := s.emitPerRepoRefreshes(client, now, lastPollTime, totalCount)
		s.mu.Lock()
		s.lastKnownCount = totalCount
		s.lastPollTime = now
		s.mu.Unlock()
		if !repoHealthy {
			return false
		}
		if changed != nil {
			// List succeeded but exceeded threshold — rebuild known snapshot.
			if !s.loadKnownIssues(client) {
				return false
			}
		}
		return true
	}

	s.emitGranularMutations(changed, now, totalCount)
	return true
}

// waitWithDone waits for the specified duration or until done is signaled.
func (s *DaemonSubscriber) waitWithDone(d time.Duration) {
	select {
	case <-s.done:
	case <-time.After(d):
	}
}
