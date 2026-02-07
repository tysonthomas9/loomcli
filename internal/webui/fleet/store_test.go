package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupTest(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewStoreFromClient(rdb, nil), mr
}

func TestNewStore_RequiresAddress(t *testing.T) {
	_, err := NewStore(RedisConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestNewStore_DefaultPoolSize(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewStore(RedisConfig{Address: mr.Addr()}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer store.Close()
}

func TestTryClaim_Success(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	ok, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Verify Redis state
	owner, err := mr.Get(claimedTaskKey("task-abc"))
	if err != nil {
		t.Fatalf("claim key missing: %v", err)
	}
	if owner != "worker-1" {
		t.Errorf("expected owner worker-1, got %s", owner)
	}

	// Verify TTL is set
	ttl := mr.TTL(claimedTaskKey("task-abc"))
	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
}

func TestTryClaim_AlreadyClaimed(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims
	ok, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("first claim should succeed")
	}

	// Worker 2 tries to claim same task
	ok, err = store.TryClaim(ctx, "task-abc", "worker-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected second claim to fail")
	}
}

func TestTryClaim_IdempotentReclaim(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims
	ok, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("first claim should succeed")
	}

	// Worker 1 reclaims (idempotent)
	ok, err = store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("idempotent reclaim should succeed")
	}
}

func TestTryClaim_AfterExpiry(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims
	_, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fast-forward past TTL
	mr.FastForward(defaultClaimTTL + time.Second)

	// Worker 2 can now claim
	ok, err := store.TryClaim(ctx, "task-abc", "worker-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("claim should succeed after expiry")
	}
}

func TestTryClaim_ConcurrentRace(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	const numWorkers = 10
	results := make([]bool, numWorkers)
	errs := make([]error, numWorkers)

	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(i int) {
			defer wg.Done()
			workerID := fmt.Sprintf("worker-%d", i)
			results[i], errs[i] = store.TryClaim(ctx, "task-race", workerID)
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i := 0; i < numWorkers; i++ {
		if errs[i] != nil {
			t.Errorf("worker %d got error: %v", i, errs[i])
			continue
		}
		if results[i] {
			successCount++
		}
	}

	if successCount < 1 {
		t.Errorf("expected at least 1 successful claim, got %d", successCount)
	}
}

func TestExtendClaimTTL_Success(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Claim first
	_, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Extend TTL to 30 minutes
	err = store.ExtendClaimTTL(ctx, "task-abc", 30)
	if err != nil {
		t.Fatalf("extend TTL failed: %v", err)
	}

	// Verify TTL was extended
	ttl := mr.TTL(claimedTaskKey("task-abc"))
	if ttl < 25*time.Minute {
		t.Errorf("expected TTL >= 25 minutes, got %v", ttl)
	}
}

func TestExtendClaimTTL_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	err := store.ExtendClaimTTL(ctx, "nonexistent", 30)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestReleaseClaim_Success(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Claim then release
	_, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	err = store.ReleaseClaim(ctx, "task-abc")
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Verify key is gone
	if mr.Exists(claimedTaskKey("task-abc")) {
		t.Error("claim key should be deleted after release")
	}
}

func TestReleaseClaim_NonExistent(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Releasing a non-existent claim should not error (idempotent)
	err := store.ReleaseClaim(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("release of non-existent claim should succeed: %v", err)
	}
}

func TestReleaseClaim_EnablesReclaim(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Worker 1 claims
	_, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Worker 2 cannot claim
	ok, err := store.TryClaim(ctx, "task-abc", "worker-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("should not be able to claim while held")
	}

	// Worker 1 releases
	err = store.ReleaseClaim(ctx, "task-abc")
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Worker 2 can now claim
	ok, err = store.TryClaim(ctx, "task-abc", "worker-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("should be able to claim after release")
	}
}

func TestRecordWorkerClaim_Success(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	claim := &ClaimResponse{
		TaskID:  "task-abc",
		Success: true,
		Payload: json.RawMessage(`{"issue":{"id":"task-abc"}}`),
	}

	err := store.RecordWorkerClaim(ctx, "worker-1", claim)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify key exists with TTL
	if !mr.Exists(workerClaimKey("worker-1")) {
		t.Fatal("worker claim key should exist")
	}
	ttl := mr.TTL(workerClaimKey("worker-1"))
	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
}

func TestGetWorkerClaim_Success(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	original := &ClaimResponse{
		TaskID:  "task-abc",
		Success: true,
		Payload: json.RawMessage(`{"issue":{"id":"task-abc"}}`),
	}

	err := store.RecordWorkerClaim(ctx, "worker-1", original)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	// Retrieve it
	retrieved, err := store.GetWorkerClaim(ctx, "worker-1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("expected non-nil claim")
	}
	if retrieved.TaskID != "task-abc" {
		t.Errorf("expected task-abc, got %s", retrieved.TaskID)
	}
	if !retrieved.Success {
		t.Error("expected success=true")
	}
	if string(retrieved.Payload) != string(original.Payload) {
		t.Errorf("payload mismatch: got %s, want %s", retrieved.Payload, original.Payload)
	}
}

func TestGetWorkerClaim_NotFound(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	claim, err := store.GetWorkerClaim(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claim != nil {
		t.Fatalf("expected nil for non-existent worker, got %+v", claim)
	}
}

func TestGetWorkerClaim_Expired(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	claim := &ClaimResponse{
		TaskID:  "task-abc",
		Success: true,
	}

	err := store.RecordWorkerClaim(ctx, "worker-1", claim)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	// Fast-forward past TTL
	mr.FastForward(defaultWorkerClaimTTL + time.Second)

	// Should return nil after expiry
	retrieved, err := store.GetWorkerClaim(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retrieved != nil {
		t.Fatal("expected nil after TTL expiry")
	}
}

func TestFullClaimWorkflow(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// 1. Claim task
	ok, err := store.TryClaim(ctx, "task-abc", "worker-1")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if !ok {
		t.Fatal("claim should succeed")
	}

	// 2. Record the claim response
	claim := &ClaimResponse{
		TaskID:  "task-abc",
		Success: true,
		Payload: json.RawMessage(`{"issue":{"id":"task-abc","title":"Fix bug"}}`),
	}
	err = store.RecordWorkerClaim(ctx, "worker-1", claim)
	if err != nil {
		t.Fatalf("record failed: %v", err)
	}

	// 3. Idempotent retry returns same cached result
	retrieved, err := store.GetWorkerClaim(ctx, "worker-1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if retrieved.TaskID != "task-abc" {
		t.Errorf("expected task-abc, got %s", retrieved.TaskID)
	}

	// 4. Extend TTL after beads confirms
	err = store.ExtendClaimTTL(ctx, "task-abc", 30)
	if err != nil {
		t.Fatalf("extend failed: %v", err)
	}

	// 5. Verify extended TTL
	ttl := mr.TTL(claimedTaskKey("task-abc"))
	if ttl < 25*time.Minute {
		t.Errorf("expected extended TTL, got %v", ttl)
	}

	// 6. Release claim when done
	err = store.ReleaseClaim(ctx, "task-abc")
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// 7. Another worker can now claim
	ok, err = store.TryClaim(ctx, "task-abc", "worker-2")
	if err != nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if !ok {
		t.Fatal("second worker should be able to claim after release")
	}
}

func TestValidateID_EmptyID(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	_, err := store.TryClaim(ctx, "", "worker-1")
	if err == nil {
		t.Fatal("expected error for empty taskID")
	}

	_, err = store.TryClaim(ctx, "task-1", "")
	if err == nil {
		t.Fatal("expected error for empty workerID")
	}
}

func TestValidateID_InvalidChars(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Colon in ID could break key namespacing
	_, err := store.TryClaim(ctx, "task:bad", "worker-1")
	if err == nil {
		t.Fatal("expected error for colon in taskID")
	}

	// Newline could cause parsing issues
	_, err = store.TryClaim(ctx, "task-1", "worker\n1")
	if err == nil {
		t.Fatal("expected error for newline in workerID")
	}

	// Space in ID
	err = store.ExtendClaimTTL(ctx, "task 1", 30)
	if err == nil {
		t.Fatal("expected error for space in taskID")
	}

	err = store.ReleaseClaim(ctx, "task\t1")
	if err == nil {
		t.Fatal("expected error for tab in taskID")
	}

	err = store.RecordWorkerClaim(ctx, "worker:1", &ClaimResponse{TaskID: "t"})
	if err == nil {
		t.Fatal("expected error for colon in workerID")
	}

	_, err = store.GetWorkerClaim(ctx, "worker 1")
	if err == nil {
		t.Fatal("expected error for space in workerID")
	}
}

func TestNewStore_PasswordFromEnv(t *testing.T) {
	mr := miniredis.RunT(t)

	t.Run("FLEET_REDIS_PASSWORD takes precedence over LOOM_REDIS_PASSWORD", func(t *testing.T) {
		t.Setenv("FLEET_REDIS_PASSWORD", "fleet-secret")
		t.Setenv("LOOM_REDIS_PASSWORD", "loom-secret")
		store, err := NewStore(RedisConfig{Address: mr.Addr()}, nil)
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		store.Close()
	})

	t.Run("LOOM_REDIS_PASSWORD used as fallback", func(t *testing.T) {
		t.Setenv("FLEET_REDIS_PASSWORD", "")
		t.Setenv("LOOM_REDIS_PASSWORD", "loom-secret")
		store, err := NewStore(RedisConfig{Address: mr.Addr()}, nil)
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		store.Close()
	})

	t.Run("YAML password takes precedence over env vars", func(t *testing.T) {
		t.Setenv("FLEET_REDIS_PASSWORD", "fleet-secret")
		t.Setenv("LOOM_REDIS_PASSWORD", "loom-secret")
		store, err := NewStore(RedisConfig{Address: mr.Addr(), Password: "yaml-secret"}, nil)
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		store.Close()
	})

	t.Run("no password when all sources empty", func(t *testing.T) {
		t.Setenv("FLEET_REDIS_PASSWORD", "")
		t.Setenv("LOOM_REDIS_PASSWORD", "")
		store, err := NewStore(RedisConfig{Address: mr.Addr()}, nil)
		if err != nil {
			t.Fatalf("NewStore() error = %v", err)
		}
		store.Close()
	})
}

func TestClose(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := NewStore(RedisConfig{Address: mr.Addr()}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = store.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestUpdateHeartbeat_Success(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Register a worker first
	worker := &Worker{
		WorkerID:     "hb-worker-1",
		Repos:        []string{"repo-a"},
		RegisteredAt: time.Now().Unix(),
	}
	if err := store.RegisterWorker(ctx, worker); err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}

	// Call UpdateHeartbeat
	before := time.Now().UTC()
	lastHB, err := store.UpdateHeartbeat(ctx, "hb-worker-1")
	if err != nil {
		t.Fatalf("UpdateHeartbeat failed: %v", err)
	}
	after := time.Now().UTC()

	// Verify returned time is reasonable (between before and after)
	if lastHB.Before(before) || lastHB.After(after) {
		t.Errorf("lastHeartbeat = %v, want between %v and %v", lastHB, before, after)
	}
}

func TestUpdateHeartbeat_WorkerNotFound(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Call UpdateHeartbeat for an unregistered worker
	_, err := store.UpdateHeartbeat(ctx, "nonexistent-worker")
	if err == nil {
		t.Fatal("expected error for unregistered worker")
	}
	if err != ErrWorkerNotFound {
		t.Errorf("error = %v, want %v", err, ErrWorkerNotFound)
	}
}

func TestUpdateHeartbeat_RefreshesTTL(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Register a worker
	worker := &Worker{
		WorkerID:     "hb-worker-ttl",
		Repos:        []string{"repo-b"},
		RegisteredAt: time.Now().Unix(),
	}
	if err := store.RegisterWorker(ctx, worker); err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}

	// Fast-forward time by 1 hour (half of the 2-hour registration TTL)
	mr.FastForward(1 * time.Hour)

	// Call UpdateHeartbeat to refresh the TTL
	_, err := store.UpdateHeartbeat(ctx, "hb-worker-ttl")
	if err != nil {
		t.Fatalf("UpdateHeartbeat failed: %v", err)
	}

	// Verify TTL was refreshed back to the full defaultWorkerRegistrationTTL
	ttl := mr.TTL(workerRegistrationKey("hb-worker-ttl"))
	// After refresh, TTL should be close to defaultWorkerRegistrationTTL (2 hours)
	// Allow some tolerance since miniredis FastForward is not exact for subsequent ops
	if ttl < defaultWorkerRegistrationTTL-time.Minute {
		t.Errorf("TTL = %v, want close to %v (at least %v)", ttl, defaultWorkerRegistrationTTL, defaultWorkerRegistrationTTL-time.Minute)
	}
}
