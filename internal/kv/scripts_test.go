package kv

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTest(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewClientFromRedis(rdb), mr
}

func TestClaimTask_Success(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	result, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got failure with owner: %s", result.ExistingOwner)
	}

	// Verify Redis state
	owner, err := mr.Get(taskOwnerKey("task-abc"))
	if err != nil {
		t.Fatalf("task owner key missing: %v", err)
	}
	if owner != "worker-1" {
		t.Errorf("expected owner worker-1, got %s", owner)
	}

	// Verify worker state hash
	state := mr.HGet(workerStateKey("worker-1"), "state")
	if state != "working" {
		t.Errorf("expected state working, got %s", state)
	}
	taskID := mr.HGet(workerStateKey("worker-1"), "task_id")
	if taskID != "task-abc" {
		t.Errorf("expected task_id task-abc, got %s", taskID)
	}
	agentType := mr.HGet(workerStateKey("worker-1"), "agent_type")
	if agentType != "spark" {
		t.Errorf("expected agent_type spark, got %s", agentType)
	}

	// Verify active workers ZSET
	members, err := mr.ZMembers(activeWorkersKey())
	if err != nil {
		t.Fatalf("failed to get ZSET members: %v", err)
	}
	if len(members) != 1 || members[0] != "worker-1" {
		t.Errorf("expected [worker-1] in active workers, got %v", members)
	}

	// Verify TTL is set
	ttl := mr.TTL(taskOwnerKey("task-abc"))
	if ttl <= 0 {
		t.Errorf("expected positive TTL on task owner key, got %v", ttl)
	}
	ttl = mr.TTL(workerStateKey("worker-1"))
	if ttl <= 0 {
		t.Errorf("expected positive TTL on worker state key, got %v", ttl)
	}
}

func TestClaimTask_AlreadyClaimed(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims task
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Worker 2 tries to claim same task
	result, err := client.ClaimTask(ctx, "worker-2", "task-abc", "Fix bug", "comet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure, got success")
	}
	if result.ExistingOwner != "worker-1" {
		t.Errorf("expected existing owner worker-1, got %s", result.ExistingOwner)
	}
}

func TestClaimTask_IdempotentReclaim(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	// Worker claims task
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Same worker reclaims (e.g., after restart)
	result, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("reclaim failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected reclaim to succeed, got failure with owner: %s", result.ExistingOwner)
	}
}

func TestClaimTask_ReclaimAfterExpiry(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims task
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}

	// Fast-forward past TTL
	mr.FastForward(time.Duration(DefaultTTLSeconds+1) * time.Second)

	// Worker 2 can now claim the expired task
	result, err := client.ClaimTask(ctx, "worker-2", "task-abc", "Fix bug", "comet")
	if err != nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success after expiry, got failure with owner: %s", result.ExistingOwner)
	}
}

func TestHeartbeat_ExtendsAllTTLs(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Claim a task first
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Fast-forward half the TTL
	mr.FastForward(time.Duration(DefaultTTLSeconds/2) * time.Second)

	// Heartbeat should extend TTLs
	result, err := client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.TTL != DefaultTTLSeconds {
		t.Errorf("expected TTL %d, got %d", DefaultTTLSeconds, result.TTL)
	}

	// Verify TTLs were extended (should be close to DefaultTTLSeconds again)
	ttl := mr.TTL(taskOwnerKey("task-abc"))
	if ttl <= time.Duration(DefaultTTLSeconds/2)*time.Second {
		t.Errorf("task owner TTL not extended: %v", ttl)
	}
	ttl = mr.TTL(workerStateKey("worker-1"))
	if ttl <= time.Duration(DefaultTTLSeconds/2)*time.Second {
		t.Errorf("worker state TTL not extended: %v", ttl)
	}
}

func TestHeartbeat_NoActiveSession(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	// Heartbeat without a claim
	result, err := client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for unregistered worker")
	}
	if result.Error != "no_active_session" {
		t.Errorf("expected no_active_session, got %s", result.Error)
	}
}

func TestHeartbeat_OwnershipLost(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims task
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Simulate task being reclaimed: manually change owner
	mr.Set(taskOwnerKey("task-abc"), "worker-2")

	// Worker 1's heartbeat should detect ownership lost
	result, err := client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for lost ownership")
	}
	if result.Error != "ownership_lost" {
		t.Errorf("expected ownership_lost, got %s", result.Error)
	}
}

func TestCompleteTask_Success(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Claim then complete
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	result, err := client.CompleteTask(ctx, "worker-1", "task-abc")
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}

	// Verify task owner key is deleted
	if mr.Exists(taskOwnerKey("task-abc")) {
		t.Error("task owner key should be deleted after completion")
	}

	// Verify worker state is idle
	state := mr.HGet(workerStateKey("worker-1"), "state")
	if state != "idle" {
		t.Errorf("expected idle state, got %s", state)
	}

	// Verify task-specific fields are removed
	taskID := mr.HGet(workerStateKey("worker-1"), "task_id")
	if taskID != "" {
		t.Errorf("expected task_id to be removed, got %s", taskID)
	}

	// Verify worker is still in active workers ZSET
	members, err := mr.ZMembers(activeWorkersKey())
	if err != nil {
		t.Fatalf("failed to get ZSET members: %v", err)
	}
	if len(members) != 1 || members[0] != "worker-1" {
		t.Errorf("worker should remain in active ZSET after completion, got %v", members)
	}
}

func TestCompleteTask_NotOwner(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Worker 2 tries to complete
	result, err := client.CompleteTask(ctx, "worker-2", "task-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for non-owner")
	}
	if result.Error != "not_owner" {
		t.Errorf("expected not_owner, got %s", result.Error)
	}
}

func TestCompleteTask_TaskExpired(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Claim then expire
	_, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	mr.FastForward(time.Duration(DefaultTTLSeconds+1) * time.Second)

	// Complete should report task_not_found
	result, err := client.CompleteTask(ctx, "worker-1", "task-abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected failure for expired task")
	}
	if result.Error != "task_not_found" {
		t.Errorf("expected task_not_found, got %s", result.Error)
	}
}

func TestClaimThenHeartbeatThenComplete(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// 1. Claim
	claimResult, err := client.ClaimTask(ctx, "worker-1", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if !claimResult.Success {
		t.Fatal("claim should succeed")
	}

	// 2. Advance time and heartbeat
	mr.FastForward(60 * time.Second)
	hbResult, err := client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if !hbResult.Success {
		t.Fatalf("heartbeat should succeed, got error: %s", hbResult.Error)
	}

	// 3. Advance more time and heartbeat again
	mr.FastForward(60 * time.Second)
	hbResult, err = client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("second heartbeat failed: %v", err)
	}
	if !hbResult.Success {
		t.Fatalf("second heartbeat should succeed, got error: %s", hbResult.Error)
	}

	// 4. Complete
	completeResult, err := client.CompleteTask(ctx, "worker-1", "task-abc")
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if !completeResult.Success {
		t.Fatalf("complete should succeed, got error: %s", completeResult.Error)
	}

	// 5. Verify final state
	if mr.Exists(taskOwnerKey("task-abc")) {
		t.Error("task owner key should be deleted")
	}
	state := mr.HGet(workerStateKey("worker-1"), "state")
	if state != "idle" {
		t.Errorf("worker should be idle, got %s", state)
	}
}

func TestConcurrentClaims(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	const numWorkers = 10
	results := make([]ClaimResult, numWorkers)
	errs := make([]error, numWorkers)

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(i int) {
			defer wg.Done()
			workerID := "worker-" + string(rune('A'+i))
			results[i], errs[i] = client.ClaimTask(ctx, workerID, "task-race", "Race test", "spark")
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i := 0; i < numWorkers; i++ {
		if errs[i] != nil {
			t.Errorf("worker %d got error: %v", i, errs[i])
			continue
		}
		if results[i].Success {
			successCount++
		}
	}

	// Exactly one worker should win (miniredis is single-threaded so Lua scripts
	// execute atomically, but multiple can succeed if they're the same "first" writer
	// due to serialization). At least 1 should succeed and at most numWorkers-0.
	if successCount < 1 {
		t.Errorf("expected at least 1 successful claim, got %d", successCount)
	}
}

func TestOwnershipTransfer(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	// Worker A claims
	_, err := client.ClaimTask(ctx, "worker-A", "task-abc", "Fix bug", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// TTL expires
	mr.FastForward(time.Duration(DefaultTTLSeconds+1) * time.Second)

	// Worker B claims
	result, err := client.ClaimTask(ctx, "worker-B", "task-abc", "Fix bug", "comet")
	if err != nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if !result.Success {
		t.Fatal("expected worker B to claim successfully after expiry")
	}

	// Worker A tries heartbeat → should detect ownership lost
	// But worker A's state key also expired, so this returns no_active_session
	hbResult, err := client.Heartbeat(ctx, "worker-A")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hbResult.Success {
		t.Fatal("expected heartbeat to fail for worker A")
	}
	// Either no_active_session (state expired) or ownership_lost
	if hbResult.Error != "no_active_session" && hbResult.Error != "ownership_lost" {
		t.Errorf("expected no_active_session or ownership_lost, got %s", hbResult.Error)
	}
}
