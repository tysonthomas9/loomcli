package kv

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

const (
	// DefaultLeaderKey is the Redis key used for leader election.
	DefaultLeaderKey = KeyPrefix + "stale-detector:leader"
)

// StaleDetectorConfig holds configuration for the stale detector.
type StaleDetectorConfig struct {
	// CheckInterval is how often to run a detection cycle.
	CheckInterval time.Duration

	// StaleThreshold is how old a heartbeat must be to consider a worker stale.
	StaleThreshold time.Duration

	// LeaderTTL is the TTL on the leader election key.
	LeaderTTL time.Duration

	// LeaderKey is the Redis key for leader election.
	LeaderKey string
}

// DefaultStaleDetectorConfig returns sensible defaults, overridable via env vars.
func DefaultStaleDetectorConfig() StaleDetectorConfig {
	cfg := StaleDetectorConfig{
		CheckInterval:  15 * time.Second,
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}

	if v := os.Getenv("LOOM_STALE_CHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.CheckInterval = d
		}
	}
	if v := os.Getenv("LOOM_STALE_THRESHOLD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.StaleThreshold = d
		}
	}
	if v := os.Getenv("LOOM_STALE_LEADER_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.LeaderTTL = d
		}
	}

	return cfg
}

// StaleWorker contains information about a worker detected as stale.
type StaleWorker struct {
	WorkerID      string
	TaskID        string
	TaskTitle     string
	LastHeartbeat time.Time
}

// StaleDetectorStatus exposes the current state of the stale detector for API responses.
type StaleDetectorStatus struct {
	Enabled           bool      `json:"enabled"`
	IsLeader          bool      `json:"is_leader"`
	LastCheck         time.Time `json:"last_check,omitempty"`
	StaleWorkersFound int       `json:"stale_workers_found"`
	TasksReconciled   int       `json:"tasks_reconciled"`
}

// StaleDetector periodically scans for stale workers and cleans up their state.
// Only one instance runs detection at a time via Redis leader election.
type StaleDetector struct {
	client     *Client
	config     StaleDetectorConfig
	serverID   string
	reconciler *Reconciler

	mu     sync.RWMutex
	status StaleDetectorStatus
}

// NewStaleDetector creates a new stale detector.
func NewStaleDetector(client *Client, config StaleDetectorConfig, serverID string, reconciler *Reconciler) *StaleDetector {
	return &StaleDetector{
		client:     client,
		config:     config,
		serverID:   serverID,
		reconciler: reconciler,
		status: StaleDetectorStatus{
			Enabled: true,
		},
	}
}

// Status returns the current status of the stale detector.
func (sd *StaleDetector) Status() StaleDetectorStatus {
	sd.mu.RLock()
	defer sd.mu.RUnlock()
	return sd.status
}

// Run starts the main detection loop. It blocks until ctx is canceled.
func (sd *StaleDetector) Run(ctx context.Context) error {
	log.Printf("Stale detector started (server=%s, interval=%s, threshold=%s)",
		sd.serverID, sd.config.CheckInterval, sd.config.StaleThreshold)

	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer releaseCancel()
		sd.releaseLeadership(releaseCtx)
	}()

	ticker := time.NewTicker(sd.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Stale detector shutting down")
			return ctx.Err()
		case <-ticker.C:
			sd.runCycle(ctx)
		}
	}
}

func (sd *StaleDetector) runCycle(ctx context.Context) {
	isLeader, err := sd.tryAcquireLeadership(ctx)
	if err != nil {
		log.Printf("Stale detector: leader election error: %v", err)
		return
	}

	sd.mu.Lock()
	sd.status.IsLeader = isLeader
	sd.mu.Unlock()

	if !isLeader {
		return
	}

	// Run detection
	staleWorkers, err := sd.detectStaleWorkers(ctx)
	if err != nil {
		log.Printf("Stale detector: detection error: %v", err)
		return
	}

	sd.mu.Lock()
	sd.status.LastCheck = time.Now()
	sd.status.StaleWorkersFound = len(staleWorkers)
	sd.mu.Unlock()

	if len(staleWorkers) == 0 {
		// Renew leadership even when no stale workers found
		if err := sd.renewLeadership(ctx); err != nil {
			log.Printf("Stale detector: leadership renewal failed: %v", err)
		}
		return
	}

	log.Printf("Stale detector: found %d stale worker(s)", len(staleWorkers))

	// Clean up each stale worker
	var orphanedTasks []OrphanedTask
	for _, sw := range staleWorkers {
		if err := sd.cleanupWorker(ctx, sw); err != nil {
			log.Printf("Stale detector: cleanup failed for worker %s: %v", sw.WorkerID, err)
			continue
		}
		log.Printf("Stale detector: cleaned up worker %s (task=%s)", sw.WorkerID, sw.TaskID)

		if sw.TaskID != "" {
			orphanedTasks = append(orphanedTasks, OrphanedTask{
				TaskID:     sw.TaskID,
				TaskTitle:  sw.TaskTitle,
				WorkerID:   sw.WorkerID,
				StaleSince: sw.LastHeartbeat,
			})
		}
	}

	// Reconcile with beads
	if sd.reconciler != nil && len(orphanedTasks) > 0 {
		results := sd.reconciler.ResetOrphanedTasks(ctx, orphanedTasks)
		reconciled := 0
		for _, r := range results {
			if r.Success {
				reconciled++
				log.Printf("Stale detector: reconciled task %s", r.TaskID)
			} else {
				log.Printf("Stale detector: reconcile failed for task %s: %v", r.TaskID, r.Error)
			}
		}
		sd.mu.Lock()
		sd.status.TasksReconciled = reconciled
		sd.mu.Unlock()
	}

	// Renew leadership after doing work
	if err := sd.renewLeadership(ctx); err != nil {
		log.Printf("Stale detector: leadership renewal failed: %v", err)
	}
}

func (sd *StaleDetector) tryAcquireLeadership(ctx context.Context) (bool, error) {
	acquired, err := sd.client.SetLeaderKey(ctx, sd.config.LeaderKey, sd.serverID, sd.config.LeaderTTL)
	if err != nil {
		return false, fmt.Errorf("SET NX failed: %w", err)
	}
	if acquired {
		return true, nil
	}

	// Check if we already hold the key (re-election after our own renewal)
	val, err := sd.client.rdb.Get(ctx, sd.config.LeaderKey).Result()
	if err != nil {
		return false, nil // Key may have expired, try again next cycle
	}
	return val == sd.serverID, nil
}

func (sd *StaleDetector) renewLeadership(ctx context.Context) error {
	return sd.client.RenewLeaderKey(ctx, sd.config.LeaderKey, sd.config.LeaderTTL)
}

func (sd *StaleDetector) releaseLeadership(ctx context.Context) {
	// Best-effort: delete only if we own it
	val, err := sd.client.rdb.Get(ctx, sd.config.LeaderKey).Result()
	if err != nil || val != sd.serverID {
		return
	}
	if err := sd.client.DeleteLeaderKey(ctx, sd.config.LeaderKey); err != nil {
		log.Printf("Stale detector: failed to release leadership: %v", err)
	}
}

func (sd *StaleDetector) detectStaleWorkers(ctx context.Context) ([]StaleWorker, error) {
	entries, err := sd.client.GetStaleWorkers(ctx, sd.config.StaleThreshold)
	if err != nil {
		return nil, err
	}

	var staleWorkers []StaleWorker
	for _, entry := range entries {
		state, err := sd.client.GetWorkerState(ctx, entry.WorkerID)
		if err != nil {
			log.Printf("Stale detector: failed to get state for worker %s: %v", entry.WorkerID, err)
			continue
		}

		sw := StaleWorker{
			WorkerID:      entry.WorkerID,
			TaskID:        state["task_id"],
			TaskTitle:     state["task_title"],
			LastHeartbeat: time.UnixMilli(int64(entry.Score)),
		}
		staleWorkers = append(staleWorkers, sw)
	}

	return staleWorkers, nil
}

func (sd *StaleDetector) cleanupWorker(ctx context.Context, worker StaleWorker) error {
	// Release task ownership atomically — only delete if this worker still owns it.
	// Uses a Lua script to prevent a TOCTOU race where a new worker could claim the
	// task between a GET and DEL.
	if worker.TaskID != "" {
		if _, err := sd.client.DeleteTaskOwnerIfMatch(ctx, worker.TaskID, worker.WorkerID); err != nil {
			return fmt.Errorf("cleanup task owner: %w", err)
		}
	}

	// Delete worker state
	if err := sd.client.DeleteWorkerState(ctx, worker.WorkerID); err != nil {
		return fmt.Errorf("delete worker state: %w", err)
	}

	// Remove from active workers ZSET
	if err := sd.client.RemoveActiveWorker(ctx, worker.WorkerID); err != nil {
		return fmt.Errorf("remove active worker: %w", err)
	}

	return nil
}

// GenerateServerID creates a unique server identifier from hostname, PID, and timestamp.
func GenerateServerID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), time.Now().UnixMilli())
}
