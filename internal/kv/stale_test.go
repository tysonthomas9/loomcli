package kv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupStaleTest(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewClientFromRedis(rdb), mr
}

func TestStaleDetector_LeaderElection_SingleInstance(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd := NewStaleDetector(client, cfg, "server-1", nil)
	acquired, err := sd.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("expected to acquire leadership")
	}

	// Same server should still see itself as leader
	acquired, err = sd.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("expected same server to remain leader")
	}
}

func TestStaleDetector_LeaderElection_MultipleInstances(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd1 := NewStaleDetector(client, cfg, "server-1", nil)
	sd2 := NewStaleDetector(client, cfg, "server-2", nil)

	// Server 1 acquires
	acquired, err := sd1.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("server-1 should acquire leadership")
	}

	// Server 2 cannot acquire
	acquired, err = sd2.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acquired {
		t.Fatal("server-2 should NOT acquire leadership while server-1 holds it")
	}
}

func TestStaleDetector_LeaderExpiry(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd1 := NewStaleDetector(client, cfg, "server-1", nil)
	sd2 := NewStaleDetector(client, cfg, "server-2", nil)

	// Server 1 acquires
	_, err := sd1.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fast-forward past TTL
	mr.FastForward(31 * time.Second)

	// Server 2 should now be able to acquire
	acquired, err := sd2.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("server-2 should acquire leadership after TTL expiry")
	}
}

func TestStaleDetector_DetectStaleWorkers(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Seed a stale worker (heartbeat 10 minutes ago)
	staleTime := float64(time.Now().Add(-10 * time.Minute).UnixMilli())
	mr.ZAdd(activeWorkersKey(), staleTime, "stale-worker-1")
	mr.HSet(workerStateKey("stale-worker-1"), "task_id", "task-abc", "task_title", "Fix bug", "state", "working")
	mr.Set(taskOwnerKey("task-abc"), "stale-worker-1")

	// Seed a fresh worker (heartbeat just now)
	freshTime := float64(time.Now().UnixMilli())
	mr.ZAdd(activeWorkersKey(), freshTime, "fresh-worker-1")
	mr.HSet(workerStateKey("fresh-worker-1"), "task_id", "task-xyz", "task_title", "New feature", "state", "working")

	staleWorkers, err := sd.detectStaleWorkers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(staleWorkers) != 1 {
		t.Fatalf("expected 1 stale worker, got %d", len(staleWorkers))
	}
	if staleWorkers[0].WorkerID != "stale-worker-1" {
		t.Errorf("expected stale-worker-1, got %s", staleWorkers[0].WorkerID)
	}
	if staleWorkers[0].TaskID != "task-abc" {
		t.Errorf("expected task-abc, got %s", staleWorkers[0].TaskID)
	}
}

func TestStaleDetector_CleanupWorker(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Set up worker state
	mr.ZAdd(activeWorkersKey(), float64(time.Now().UnixMilli()), "worker-1")
	mr.HSet(workerStateKey("worker-1"), "task_id", "task-abc", "state", "working")
	mr.Set(taskOwnerKey("task-abc"), "worker-1")

	sw := StaleWorker{
		WorkerID: "worker-1",
		TaskID:   "task-abc",
	}

	err := sd.cleanupWorker(ctx, sw)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Verify task owner key is deleted
	if mr.Exists(taskOwnerKey("task-abc")) {
		t.Error("task owner key should be deleted")
	}

	// Verify worker state is deleted
	if mr.Exists(workerStateKey("worker-1")) {
		t.Error("worker state key should be deleted")
	}

	// Verify worker is removed from ZSET
	members, _ := mr.ZMembers(activeWorkersKey())
	for _, m := range members {
		if m == "worker-1" {
			t.Error("worker-1 should be removed from active workers ZSET")
		}
	}
}

func TestStaleDetector_NoStaleWorkers(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		StaleThreshold: 5 * time.Minute,
		LeaderKey:      DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Only fresh workers
	freshTime := float64(time.Now().UnixMilli())
	mr.ZAdd(activeWorkersKey(), freshTime, "worker-1")
	mr.ZAdd(activeWorkersKey(), freshTime, "worker-2")

	staleWorkers, err := sd.detectStaleWorkers(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(staleWorkers) != 0 {
		t.Errorf("expected 0 stale workers, got %d", len(staleWorkers))
	}
}

func TestStaleDetector_CleanupWorker_AlreadyExpired(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Worker with no Redis state (already expired via TTL)
	sw := StaleWorker{
		WorkerID: "ghost-worker",
		TaskID:   "task-ghost",
	}

	// Cleanup should succeed silently (idempotent)
	err := sd.cleanupWorker(ctx, sw)
	if err != nil {
		t.Fatalf("cleanup of already-expired worker should succeed, got: %v", err)
	}
}

func TestStaleDetector_CleanupWorker_TaskOwnedByDifferentWorker(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Set up: worker-1 is stale, but task was already reclaimed by worker-2
	mr.ZAdd(activeWorkersKey(), float64(time.Now().UnixMilli()), "worker-1")
	mr.HSet(workerStateKey("worker-1"), "task_id", "task-abc", "state", "working")
	mr.Set(taskOwnerKey("task-abc"), "worker-2") // different owner!

	sw := StaleWorker{
		WorkerID: "worker-1",
		TaskID:   "task-abc",
	}

	err := sd.cleanupWorker(ctx, sw)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Task owner should NOT be deleted (owned by different worker)
	owner, err := mr.Get(taskOwnerKey("task-abc"))
	if err != nil {
		t.Fatalf("task owner key missing: %v", err)
	}
	if owner != "worker-2" {
		t.Errorf("expected owner worker-2, got %s", owner)
	}

	// Worker state should still be deleted
	if mr.Exists(workerStateKey("worker-1")) {
		t.Error("worker-1 state should be deleted")
	}
}

func TestStaleDetector_GracefulShutdown(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx, cancel := context.WithCancel(context.Background())

	cfg := StaleDetectorConfig{
		CheckInterval:  50 * time.Millisecond,
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Ensure the key doesn't get expired by miniredis during our test
	_ = mr

	done := make(chan error, 1)
	go func() {
		done <- sd.Run(ctx)
	}()

	// Let it run a couple cycles
	time.Sleep(150 * time.Millisecond)

	// Cancel and verify it shuts down
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale detector did not shut down in time")
	}
}

func TestStaleDetector_ReleaseLeadership(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Acquire leadership
	acquired, _ := sd.tryAcquireLeadership(ctx)
	if !acquired {
		t.Fatal("expected to acquire leadership")
	}

	// Release it
	sd.releaseLeadership(ctx)

	// Another server should now be able to acquire
	sd2 := NewStaleDetector(client, cfg, "server-2", nil)
	acquired, _ = sd2.tryAcquireLeadership(ctx)
	if !acquired {
		t.Fatal("server-2 should acquire after server-1 released")
	}
}

func TestStaleDetector_RenewLeadership(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Acquire leadership
	_, err := sd.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fast-forward 20 seconds (2/3 of TTL)
	mr.FastForward(20 * time.Second)

	// Renew
	err = sd.renewLeadership(ctx)
	if err != nil {
		t.Fatalf("renewal failed: %v", err)
	}

	// Fast-forward another 20 seconds (total 40s, but renewed at 20s so should still hold)
	mr.FastForward(20 * time.Second)

	// Should still be leader
	acquired, err := sd.tryAcquireLeadership(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !acquired {
		t.Fatal("should still be leader after renewal")
	}
}

func TestStaleDetector_Status(t *testing.T) {
	client, _ := setupStaleTest(t)

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	status := sd.Status()
	if !status.Enabled {
		t.Error("expected Enabled=true")
	}
	if status.IsLeader {
		t.Error("expected IsLeader=false before any cycle")
	}
}

func TestStaleDetector_FullCycle(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		CheckInterval:  50 * time.Millisecond,
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}

	// No reconciler since we can't exec bd in tests
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Seed a stale worker manually (heartbeat 10 minutes ago)
	staleTime := float64(time.Now().Add(-10 * time.Minute).UnixMilli())
	mr.ZAdd(activeWorkersKey(), staleTime, "worker-1")
	mr.HSet(workerStateKey("worker-1"), "task_id", "task-abc", "task_title", "Fix bug", "state", "working")
	mr.Set(taskOwnerKey("task-abc"), "worker-1")

	// Verify state is in place
	owner, err := mr.Get(taskOwnerKey("task-abc"))
	if err != nil || owner != "worker-1" {
		t.Fatalf("expected owner worker-1, got %s (err=%v)", owner, err)
	}

	// Run one detection cycle manually
	sd.runCycle(ctx)

	// Verify cleanup happened
	if mr.Exists(taskOwnerKey("task-abc")) {
		t.Error("task owner key should be deleted after stale detection")
	}
	if mr.Exists(workerStateKey("worker-1")) {
		t.Error("worker state should be deleted after stale detection")
	}
	members, _ := mr.ZMembers(activeWorkersKey())
	for _, m := range members {
		if m == "worker-1" {
			t.Error("worker-1 should be removed from active workers ZSET")
		}
	}

	// Verify status reflects the detection
	status := sd.Status()
	if !status.IsLeader {
		t.Error("expected IsLeader=true after running cycle")
	}
	if status.StaleWorkersFound != 1 {
		t.Errorf("expected StaleWorkersFound=1, got %d", status.StaleWorkersFound)
	}
}

func TestGetStaleWorkers(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	// Add workers with different timestamps
	now := time.Now()
	oldTime := float64(now.Add(-10 * time.Minute).UnixMilli())
	recentTime := float64(now.UnixMilli())

	mr.ZAdd(activeWorkersKey(), oldTime, "old-worker")
	mr.ZAdd(activeWorkersKey(), recentTime, "new-worker")

	entries, err := client.GetStaleWorkers(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 stale entry, got %d", len(entries))
	}
	if entries[0].WorkerID != "old-worker" {
		t.Errorf("expected old-worker, got %s", entries[0].WorkerID)
	}
}

func TestGetWorkerState(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.HSet(workerStateKey("worker-1"), "task_id", "task-1", "state", "working")

	state, err := client.GetWorkerState(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state["task_id"] != "task-1" {
		t.Errorf("expected task_id=task-1, got %s", state["task_id"])
	}
	if state["state"] != "working" {
		t.Errorf("expected state=working, got %s", state["state"])
	}
}

func TestGetTaskOwner(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.Set(taskOwnerKey("task-1"), "worker-1")

	owner, err := client.GetTaskOwner(ctx, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "worker-1" {
		t.Errorf("expected worker-1, got %s", owner)
	}

	// Non-existent task
	owner, err = client.GetTaskOwner(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "" {
		t.Errorf("expected empty string for non-existent task, got %s", owner)
	}
}

func TestSetLeaderKey(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	// First set should succeed
	ok, err := client.SetLeaderKey(ctx, "test:leader", "server-1", 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected SETNX to succeed")
	}

	// Second set should fail
	ok, err = client.SetLeaderKey(ctx, "test:leader", "server-2", 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected SETNX to fail when key exists")
	}
}

func TestDeleteTaskOwner(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.Set(taskOwnerKey("task-1"), "worker-1")

	err := client.DeleteTaskOwner(ctx, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mr.Exists(taskOwnerKey("task-1")) {
		t.Error("key should be deleted")
	}

	// Deleting non-existent key should be a no-op
	err = client.DeleteTaskOwner(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("deleting non-existent key should not error: %v", err)
	}
}

func TestRemoveActiveWorker(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.ZAdd(activeWorkersKey(), 1000, "worker-1")
	mr.ZAdd(activeWorkersKey(), 2000, "worker-2")

	err := client.RemoveActiveWorker(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	members, _ := mr.ZMembers(activeWorkersKey())
	if len(members) != 1 || members[0] != "worker-2" {
		t.Errorf("expected [worker-2], got %v", members)
	}
}

func TestGenerateServerID(t *testing.T) {
	id := GenerateServerID()
	if id == "" {
		t.Fatal("server ID should not be empty")
	}
	// Should contain at least two colons (hostname:pid:timestamp)
	colons := 0
	for _, c := range id {
		if c == ':' {
			colons++
		}
	}
	if colons < 2 {
		t.Errorf("expected at least 2 colons in server ID %q", id)
	}
}

func TestDefaultStaleDetectorConfig_Defaults(t *testing.T) {
	cfg := DefaultStaleDetectorConfig()

	if cfg.CheckInterval != 15*time.Second {
		t.Errorf("expected CheckInterval=15s, got %v", cfg.CheckInterval)
	}
	if cfg.StaleThreshold != 5*time.Minute {
		t.Errorf("expected StaleThreshold=5m, got %v", cfg.StaleThreshold)
	}
	if cfg.LeaderTTL != 30*time.Second {
		t.Errorf("expected LeaderTTL=30s, got %v", cfg.LeaderTTL)
	}
	if cfg.LeaderKey != DefaultLeaderKey {
		t.Errorf("expected LeaderKey=%s, got %s", DefaultLeaderKey, cfg.LeaderKey)
	}
}

func TestDefaultStaleDetectorConfig_EnvOverrides(t *testing.T) {
	t.Setenv("LOOM_STALE_CHECK_INTERVAL", "30s")
	t.Setenv("LOOM_STALE_THRESHOLD", "10m")
	t.Setenv("LOOM_STALE_LEADER_TTL", "1m")

	cfg := DefaultStaleDetectorConfig()

	if cfg.CheckInterval != 30*time.Second {
		t.Errorf("expected CheckInterval=30s, got %v", cfg.CheckInterval)
	}
	if cfg.StaleThreshold != 10*time.Minute {
		t.Errorf("expected StaleThreshold=10m, got %v", cfg.StaleThreshold)
	}
	if cfg.LeaderTTL != 1*time.Minute {
		t.Errorf("expected LeaderTTL=1m, got %v", cfg.LeaderTTL)
	}
}

func TestDefaultStaleDetectorConfig_InvalidEnv(t *testing.T) {
	t.Setenv("LOOM_STALE_CHECK_INTERVAL", "not-a-duration")
	t.Setenv("LOOM_STALE_THRESHOLD", "invalid")
	t.Setenv("LOOM_STALE_LEADER_TTL", "garbage")

	cfg := DefaultStaleDetectorConfig()

	// Should fall back to defaults when env vars are invalid
	if cfg.CheckInterval != 15*time.Second {
		t.Errorf("expected default CheckInterval=15s, got %v", cfg.CheckInterval)
	}
	if cfg.StaleThreshold != 5*time.Minute {
		t.Errorf("expected default StaleThreshold=5m, got %v", cfg.StaleThreshold)
	}
	if cfg.LeaderTTL != 30*time.Second {
		t.Errorf("expected default LeaderTTL=30s, got %v", cfg.LeaderTTL)
	}
}

func TestStaleDetector_RunCycle_NoStaleWorkers(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		CheckInterval:  50 * time.Millisecond,
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Only fresh workers
	freshTime := float64(time.Now().UnixMilli())
	mr.ZAdd(activeWorkersKey(), freshTime, "worker-1")

	// Run a cycle - should acquire leadership, find 0 stale, renew
	sd.runCycle(ctx)

	status := sd.Status()
	if !status.IsLeader {
		t.Error("expected IsLeader=true after running cycle")
	}
	if status.StaleWorkersFound != 0 {
		t.Errorf("expected StaleWorkersFound=0, got %d", status.StaleWorkersFound)
	}
}

func TestStaleDetector_RunCycle_WithReconciler(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		CheckInterval:  50 * time.Millisecond,
		StaleThreshold: 5 * time.Minute,
		LeaderTTL:      30 * time.Second,
		LeaderKey:      DefaultLeaderKey,
	}

	// Use /usr/bin/true as a stub binary that exits successfully
	reconciler := NewReconciler("/usr/bin/true")
	sd := NewStaleDetector(client, cfg, "server-1", reconciler)

	// Seed a stale worker with a task
	staleTime := float64(time.Now().Add(-10 * time.Minute).UnixMilli())
	mr.ZAdd(activeWorkersKey(), staleTime, "stale-worker")
	mr.HSet(workerStateKey("stale-worker"), "task_id", "task-orphan", "task_title", "Orphaned task", "state", "working")
	mr.Set(taskOwnerKey("task-orphan"), "stale-worker")

	sd.runCycle(ctx)

	status := sd.Status()
	if status.StaleWorkersFound != 1 {
		t.Errorf("expected StaleWorkersFound=1, got %d", status.StaleWorkersFound)
	}
	// The reconciler runs /usr/bin/true which succeeds
	if status.TasksReconciled != 1 {
		t.Errorf("expected TasksReconciled=1, got %d", status.TasksReconciled)
	}
}

func TestStaleDetector_CleanupWorker_NoTask(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Idle worker that went stale (no task)
	mr.ZAdd(activeWorkersKey(), float64(time.Now().UnixMilli()), "idle-worker")
	mr.HSet(workerStateKey("idle-worker"), "state", "idle")

	sw := StaleWorker{
		WorkerID: "idle-worker",
		TaskID:   "", // no task
	}

	err := sd.cleanupWorker(ctx, sw)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Worker state should be deleted
	if mr.Exists(workerStateKey("idle-worker")) {
		t.Error("idle worker state should be deleted")
	}

	// Worker should be removed from ZSET
	members, _ := mr.ZMembers(activeWorkersKey())
	for _, m := range members {
		if m == "idle-worker" {
			t.Error("idle-worker should be removed from active workers ZSET")
		}
	}
}

func TestStaleDetector_ReleaseLeadership_NotLeader(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Don't acquire leadership, just call release - should be a no-op
	sd.releaseLeadership(ctx)

	// Key should not exist
	if mr.Exists(DefaultLeaderKey) {
		t.Error("leader key should not exist")
	}
}

func TestStaleDetector_ReleaseLeadership_DifferentOwner(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderTTL: 30 * time.Second,
		LeaderKey: DefaultLeaderKey,
	}

	// server-1 acquires leadership
	sd1 := NewStaleDetector(client, cfg, "server-1", nil)
	acquired, _ := sd1.tryAcquireLeadership(ctx)
	if !acquired {
		t.Fatal("expected server-1 to acquire leadership")
	}

	// server-2 tries to release — should be a no-op since it doesn't own the key
	sd2 := NewStaleDetector(client, cfg, "server-2", nil)
	sd2.releaseLeadership(ctx)

	// Key should still exist and be owned by server-1
	val, err := mr.Get(DefaultLeaderKey)
	if err != nil {
		t.Fatalf("leader key should still exist: %v", err)
	}
	if val != "server-1" {
		t.Errorf("expected leader to be server-1, got %s", val)
	}
}

// --- GetStaleWorkers edge case tests ---

func TestGetStaleWorkers_EmptyZSET(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	// No workers seeded — ZSET doesn't exist
	entries, err := client.GetStaleWorkers(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty ZSET, got %d", len(entries))
	}
}

func TestGetStaleWorkers_RedisError(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.Close()

	_, err := client.GetStaleWorkers(ctx, 5*time.Minute)
	if err == nil {
		t.Fatal("expected error when Redis is closed")
	}
	if !strings.Contains(err.Error(), "ZRANGEBYSCORE failed") {
		t.Errorf("expected error to contain 'ZRANGEBYSCORE failed', got: %v", err)
	}
}

func TestGetStaleWorkers_AllWorkersStale(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	// Seed 3 workers all with timestamps 10 minutes ago
	staleTime := float64(time.Now().Add(-10 * time.Minute).UnixMilli())
	mr.ZAdd(activeWorkersKey(), staleTime, "worker-1")
	mr.ZAdd(activeWorkersKey(), staleTime-1000, "worker-2")
	mr.ZAdd(activeWorkersKey(), staleTime-2000, "worker-3")

	entries, err := client.GetStaleWorkers(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 stale entries, got %d", len(entries))
	}
}

func TestGetStaleWorkers_ZeroThreshold(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	// Seed workers with current timestamps
	now := float64(time.Now().UnixMilli())
	mr.ZAdd(activeWorkersKey(), now, "worker-1")
	mr.ZAdd(activeWorkersKey(), now, "worker-2")

	// Zero threshold means cutoff = now, so all workers at or before now are stale
	entries, err := client.GetStaleWorkers(ctx, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 stale entries with zero threshold, got %d", len(entries))
	}
}

// --- DeleteTaskOwnerIfMatch (TOCTOU fix) tests ---

func TestDeleteTaskOwnerIfMatch_MatchingOwner(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.Set(taskOwnerKey("task-abc"), "worker-1")

	deleted, err := client.DeleteTaskOwnerIfMatch(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected true when owner matches")
	}
	if mr.Exists(taskOwnerKey("task-abc")) {
		t.Error("task owner key should be deleted when owner matches")
	}
}

func TestDeleteTaskOwnerIfMatch_DifferentOwner(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	mr.Set(taskOwnerKey("task-abc"), "worker-2")

	deleted, err := client.DeleteTaskOwnerIfMatch(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted {
		t.Fatal("expected false when owner does not match")
	}

	// Key should still exist with the original owner
	owner, err := mr.Get(taskOwnerKey("task-abc"))
	if err != nil {
		t.Fatalf("task owner key should still exist: %v", err)
	}
	if owner != "worker-2" {
		t.Errorf("expected owner worker-2, got %s", owner)
	}
}

func TestDeleteTaskOwnerIfMatch_NoKey(t *testing.T) {
	client, _ := setupStaleTest(t)
	ctx := context.Background()

	// No key set — nonexistent key should return true (nothing to delete is success)
	deleted, err := client.DeleteTaskOwnerIfMatch(ctx, "nonexistent-task", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected true when key does not exist")
	}
}

func TestCleanupWorker_AtomicOwnershipCheck(t *testing.T) {
	client, mr := setupStaleTest(t)
	ctx := context.Background()

	cfg := StaleDetectorConfig{
		LeaderKey: DefaultLeaderKey,
	}
	sd := NewStaleDetector(client, cfg, "server-1", nil)

	// Set up worker-1 as stale with task-abc
	mr.ZAdd(activeWorkersKey(), float64(time.Now().UnixMilli()), "worker-1")
	mr.HSet(workerStateKey("worker-1"), "task_id", "task-abc", "state", "working")
	mr.Set(taskOwnerKey("task-abc"), "worker-1")

	// Simulate worker-2 claiming task-abc before cleanup runs (the race condition)
	mr.Set(taskOwnerKey("task-abc"), "worker-2")

	sw := StaleWorker{
		WorkerID: "worker-1",
		TaskID:   "task-abc",
	}

	err := sd.cleanupWorker(ctx, sw)
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	// Task-abc should still be owned by worker-2 (atomic check prevented deletion)
	owner, err := mr.Get(taskOwnerKey("task-abc"))
	if err != nil {
		t.Fatalf("task owner key should still exist: %v", err)
	}
	if owner != "worker-2" {
		t.Errorf("expected task owner to remain worker-2, got %s", owner)
	}

	// Worker-1's state should be cleaned up
	if mr.Exists(workerStateKey("worker-1")) {
		t.Error("worker-1 state should be deleted")
	}

	// Worker-1 should be removed from active workers ZSET
	members, _ := mr.ZMembers(activeWorkersKey())
	for _, m := range members {
		if m == "worker-1" {
			t.Error("worker-1 should be removed from active workers ZSET")
		}
	}
}
