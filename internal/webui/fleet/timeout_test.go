package fleet

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// RecordClaimTime / ClearClaimTime
// ---------------------------------------------------------------------------

func TestRecordClaimTime(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	err := store.RecordClaimTime(ctx, "task-1")
	if err != nil {
		t.Fatalf("RecordClaimTime failed: %v", err)
	}

	// Verify the task was added to the sorted set.
	score, err := store.client.ZScore(ctx, claimTimesKey, "task-1").Result()
	if err != nil {
		t.Fatalf("ZScore failed: %v", err)
	}

	now := float64(time.Now().Unix())
	// Score should be within a few seconds of now.
	if score < now-5 || score > now+5 {
		t.Errorf("expected score ~%v, got %v", now, score)
	}
}

func TestClearClaimTime(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Add a claim time first.
	err := store.RecordClaimTime(ctx, "task-1")
	if err != nil {
		t.Fatalf("RecordClaimTime failed: %v", err)
	}

	// Verify it exists.
	count, err := store.client.ZCard(ctx, claimTimesKey).Result()
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 member in sorted set, got %d", count)
	}

	// Clear it.
	err = store.ClearClaimTime(ctx, "task-1")
	if err != nil {
		t.Fatalf("ClearClaimTime failed: %v", err)
	}

	// Verify it was removed.
	count, err = store.client.ZCard(ctx, claimTimesKey).Result()
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 members after clear, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// FindTimedOutTasks
// ---------------------------------------------------------------------------

func TestFindTimedOutTasks_NoTasks(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	tasks, err := store.FindTimedOutTasks(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("FindTimedOutTasks failed: %v", err)
	}
	if tasks != nil {
		t.Errorf("expected nil, got %v", tasks)
	}
}

func TestFindTimedOutTasks_UnderTimeout(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Claim a task (adds to sorted set with current timestamp via TryClaim).
	ok, err := store.TryClaim(ctx, "task-1", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Ask for tasks that have been claimed for more than 10 minutes.
	// Since we just claimed it, nothing should be returned.
	tasks, err := store.FindTimedOutTasks(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("FindTimedOutTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected no timed-out tasks, got %d", len(tasks))
	}
}

func TestFindTimedOutTasks_OverTimeout(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Claim a task so the claim key is set.
	ok, err := store.TryClaim(ctx, "task-1", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Overwrite the sorted set score with a timestamp far in the past so
	// FindTimedOutTasks (which compares against time.Now()) sees it as
	// timed out. miniredis.FastForward only advances Redis TTLs, not Go's
	// wall clock.
	oldTimestamp := float64(time.Now().Add(-15 * time.Minute).Unix())
	store.client.ZAdd(ctx, claimTimesKey, redis.Z{
		Score:  oldTimestamp,
		Member: "task-1",
	})

	tasks, err := store.FindTimedOutTasks(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("FindTimedOutTasks failed: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 timed-out task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.TaskID != "task-1" {
		t.Errorf("expected TaskID=task-1, got %s", task.TaskID)
	}
	if task.WorkerID != "worker-1" {
		t.Errorf("expected WorkerID=worker-1, got %s", task.WorkerID)
	}
	if task.ClaimedAt.IsZero() {
		t.Error("expected non-zero ClaimedAt")
	}
	// ClaimedAt should match the score we set.
	expectedClaimedAt := time.Unix(int64(oldTimestamp), 0)
	if !task.ClaimedAt.Equal(expectedClaimedAt) {
		t.Errorf("expected ClaimedAt=%v, got %v", expectedClaimedAt, task.ClaimedAt)
	}
}

func TestFindTimedOutTasks_CleansUpOrphanedEntries(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Manually add an entry to the sorted set with a very old timestamp
	// but do NOT set a corresponding claim key. This simulates an orphaned
	// entry where the claim key expired but the sorted set entry remains.
	oldTimestamp := float64(time.Now().Add(-1 * time.Hour).Unix())
	store.client.ZAdd(ctx, claimTimesKey, redis.Z{
		Score:  oldTimestamp,
		Member: "orphaned-task",
	})

	// FindTimedOutTasks should clean up the orphaned entry and return nothing.
	tasks, err := store.FindTimedOutTasks(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("FindTimedOutTasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected no tasks (orphan should be cleaned up), got %d", len(tasks))
	}

	// Verify the orphaned entry was removed from the sorted set.
	count, err := store.client.ZCard(ctx, claimTimesKey).Result()
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected orphaned entry to be removed, got %d members", count)
	}
}

func TestFindTimedOutTasks_MultipleTasks(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Claim three tasks.
	for i := 1; i <= 3; i++ {
		taskID := "task-" + strconv.Itoa(i)
		workerID := "worker-" + strconv.Itoa(i)
		ok, err := store.TryClaim(ctx, taskID, workerID)
		if err != nil {
			t.Fatalf("TryClaim(%s) failed: %v", taskID, err)
		}
		if !ok {
			t.Fatalf("expected claim for %s to succeed", taskID)
		}
	}

	// Overwrite the sorted set scores with old timestamps so they appear
	// timed out. miniredis.FastForward does not affect Go's time.Now().
	oldTimestamp := float64(time.Now().Add(-15 * time.Minute).Unix())
	for i := 1; i <= 3; i++ {
		taskID := "task-" + strconv.Itoa(i)
		store.client.ZAdd(ctx, claimTimesKey, redis.Z{
			Score:  oldTimestamp,
			Member: taskID,
		})
	}

	tasks, err := store.FindTimedOutTasks(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("FindTimedOutTasks failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 timed-out tasks, got %d", len(tasks))
	}

	// Build a set of returned task IDs for easy lookup.
	seen := make(map[string]bool)
	for _, task := range tasks {
		seen[task.TaskID] = true
	}
	for i := 1; i <= 3; i++ {
		taskID := "task-" + strconv.Itoa(i)
		if !seen[taskID] {
			t.Errorf("expected %s in results", taskID)
		}
	}
}

// ---------------------------------------------------------------------------
// TimeoutEnforcer: Start / Stop
// ---------------------------------------------------------------------------

func TestTimeoutEnforcer_Start_Stop(t *testing.T) {
	store, _ := setupTest(t)

	enforcer := NewTimeoutEnforcer(store, TimeoutConfig{
		TaskTimeout:   100 * time.Millisecond,
		CheckInterval: 50 * time.Millisecond,
	}, nil)

	enforcer.Start()

	// Give it a moment to prove it's running without panic.
	time.Sleep(80 * time.Millisecond)

	// Stop should not hang.
	done := make(chan struct{})
	go func() {
		enforcer.Stop()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() hung for more than 2 seconds")
	}
}

// ---------------------------------------------------------------------------
// TimeoutEnforcer: DetectsTimeout
// ---------------------------------------------------------------------------

func TestTimeoutEnforcer_DetectsTimeout(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Claim a task.
	ok, err := store.TryClaim(ctx, "task-timeout", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Extend TTL so the claim key outlives the fast-forward.
	if err := store.ExtendClaimTTL(ctx, "task-timeout", 60); err != nil {
		t.Fatalf("ExtendClaimTTL failed: %v", err)
	}

	// Fast-forward past the task timeout so it appears timed out.
	mr.FastForward(200 * time.Millisecond)

	enforcer := NewTimeoutEnforcer(store, TimeoutConfig{
		TaskTimeout:   100 * time.Millisecond,
		CheckInterval: 50 * time.Millisecond,
	}, nil)

	enforcer.Start()
	defer enforcer.Stop()

	// Wait for the enforcer to run at least one cycle.
	time.Sleep(200 * time.Millisecond)

	// The enforcer should have released the claim.
	// Check that the claim key no longer exists.
	_, err = store.client.Get(ctx, claimedTaskKey("task-timeout")).Result()
	if err != redis.Nil {
		t.Errorf("expected claim to be released (redis.Nil), got err=%v", err)
	}

	// Counter should have incremented.
	count := enforcer.GetTimeoutCount()
	if count < 1 {
		t.Errorf("expected timeout count >= 1, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// TimeoutEnforcer: CallsOnTimeout
// ---------------------------------------------------------------------------

func TestTimeoutEnforcer_CallsOnTimeout(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Claim a task.
	ok, err := store.TryClaim(ctx, "task-cb", "worker-cb")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Extend TTL to survive fast-forward.
	if err := store.ExtendClaimTTL(ctx, "task-cb", 60); err != nil {
		t.Fatalf("ExtendClaimTTL failed: %v", err)
	}

	// Fast-forward past timeout.
	mr.FastForward(200 * time.Millisecond)

	var mu sync.Mutex
	var callbackWorkerID, callbackTaskID string
	var callbackDuration time.Duration

	enforcer := NewTimeoutEnforcer(store, TimeoutConfig{
		TaskTimeout:   100 * time.Millisecond,
		CheckInterval: 50 * time.Millisecond,
	}, nil)
	enforcer.SetOnTimeout(func(workerID, taskID string, duration time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		callbackWorkerID = workerID
		callbackTaskID = taskID
		callbackDuration = duration
		return nil
	})

	enforcer.Start()
	defer enforcer.Stop()

	// Wait for the enforcer to detect the timeout.
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if callbackTaskID != "task-cb" {
		t.Errorf("expected callback taskID=task-cb, got %q", callbackTaskID)
	}
	if callbackWorkerID != "worker-cb" {
		t.Errorf("expected callback workerID=worker-cb, got %q", callbackWorkerID)
	}
	if callbackDuration <= 0 {
		t.Errorf("expected positive callback duration, got %v", callbackDuration)
	}
}

// ---------------------------------------------------------------------------
// TimeoutEnforcer: IgnoresNonTimedOut
// ---------------------------------------------------------------------------

func TestTimeoutEnforcer_IgnoresNonTimedOut(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Claim a task.
	ok, err := store.TryClaim(ctx, "task-fresh", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Do NOT fast-forward. The task was just claimed so it should not be
	// considered timed out with a long timeout.
	enforcer := NewTimeoutEnforcer(store, TimeoutConfig{
		TaskTimeout:   10 * time.Minute, // very long, well beyond test duration
		CheckInterval: 50 * time.Millisecond,
	}, nil)

	callbackCalled := false
	enforcer.SetOnTimeout(func(workerID, taskID string, duration time.Duration) error {
		callbackCalled = true
		return nil
	})

	enforcer.Start()
	defer enforcer.Stop()

	// Let the enforcer run a few cycles.
	time.Sleep(200 * time.Millisecond)

	// Claim should still exist.
	owner, err := store.client.Get(ctx, claimedTaskKey("task-fresh")).Result()
	if err != nil {
		t.Fatalf("expected claim key to still exist, got error: %v", err)
	}
	if owner != "worker-1" {
		t.Errorf("expected owner worker-1, got %s", owner)
	}

	if callbackCalled {
		t.Error("callback should not have been called for a non-timed-out task")
	}

	if enforcer.GetTimeoutCount() != 0 {
		t.Errorf("expected timeout count 0, got %d", enforcer.GetTimeoutCount())
	}
}

// ---------------------------------------------------------------------------
// TimeoutEnforcer: CounterIncrements
// ---------------------------------------------------------------------------

func TestTimeoutEnforcer_CounterIncrements(t *testing.T) {
	store, mr := setupTest(t)
	ctx := context.Background()

	// Claim two tasks.
	for _, id := range []string{"task-a", "task-b"} {
		ok, err := store.TryClaim(ctx, id, "worker-"+id)
		if err != nil {
			t.Fatalf("TryClaim(%s) failed: %v", id, err)
		}
		if !ok {
			t.Fatalf("expected claim for %s to succeed", id)
		}
		// Extend TTL to survive fast-forward.
		if err := store.ExtendClaimTTL(ctx, id, 60); err != nil {
			t.Fatalf("ExtendClaimTTL(%s) failed: %v", id, err)
		}
	}

	// Fast-forward past the task timeout.
	mr.FastForward(200 * time.Millisecond)

	enforcer := NewTimeoutEnforcer(store, TimeoutConfig{
		TaskTimeout:   100 * time.Millisecond,
		CheckInterval: 50 * time.Millisecond,
	}, nil)

	enforcer.Start()
	defer enforcer.Stop()

	// Wait for the enforcer to process both tasks.
	time.Sleep(300 * time.Millisecond)

	count := enforcer.GetTimeoutCount()
	if count != 2 {
		t.Errorf("expected timeout count 2, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Integration: TryClaim records claim time in sorted set
// ---------------------------------------------------------------------------

func TestTryClaim_RecordsClaimTime(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	ok, err := store.TryClaim(ctx, "task-ct", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Verify the task was added to the claim_times sorted set.
	score, err := store.client.ZScore(ctx, claimTimesKey, "task-ct").Result()
	if err != nil {
		t.Fatalf("expected task in claim_times sorted set, got error: %v", err)
	}

	now := float64(time.Now().Unix())
	if score < now-5 || score > now+5 {
		t.Errorf("expected score ~%v, got %v", now, score)
	}
}

// ---------------------------------------------------------------------------
// Integration: ReleaseClaim clears claim time from sorted set
// ---------------------------------------------------------------------------

func TestReleaseClaim_ClearsClaimTime(t *testing.T) {
	store, _ := setupTest(t)
	ctx := context.Background()

	// Claim a task.
	ok, err := store.TryClaim(ctx, "task-rc", "worker-1")
	if err != nil {
		t.Fatalf("TryClaim failed: %v", err)
	}
	if !ok {
		t.Fatal("expected claim to succeed")
	}

	// Verify the claim time was recorded.
	_, err = store.client.ZScore(ctx, claimTimesKey, "task-rc").Result()
	if err != nil {
		t.Fatalf("expected task in claim_times sorted set after TryClaim, got error: %v", err)
	}

	// Release the claim.
	err = store.ReleaseClaim(ctx, "task-rc")
	if err != nil {
		t.Fatalf("ReleaseClaim failed: %v", err)
	}

	// Verify the claim time entry was removed from the sorted set.
	_, err = store.client.ZScore(ctx, claimTimesKey, "task-rc").Result()
	if err != redis.Nil {
		t.Errorf("expected task to be removed from claim_times sorted set (redis.Nil), got err=%v", err)
	}
}
