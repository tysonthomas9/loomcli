package kv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tysonthomas9/loomcli/internal/circuitbreaker"
)

func TestNewClient(t *testing.T) {
	client := NewClient("localhost:6379", "", 0)
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	// Don't actually connect — just verify construction
	_ = client.Close()
}

func TestKeyBuilders(t *testing.T) {
	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{"taskOwnerKey", func() string { return taskOwnerKey("task-123") }, "loom:task:task-123:owner"},
		{"workerStateKey", func() string { return workerStateKey("worker-1") }, "loom:worker:worker-1:state"},
		{"activeWorkersKey", func() string { return activeWorkersKey() }, "loom:workers:active"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestToInt64(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    int64
		wantErr bool
	}{
		{"int64", int64(42), 42, false},
		{"string", "42", 42, false},
		{"float", 42.0, 0, true},
		{"nil", nil, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toInt64(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toInt64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{"string", "hello", "hello", false},
		{"int64", int64(42), "42", false},
		{"nil", nil, "", false},
		{"float", 42.0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toString(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toString(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	if KeyPrefix != "loom:" {
		t.Errorf("expected KeyPrefix loom:, got %s", KeyPrefix)
	}
	if DefaultTTLSeconds != 300 {
		t.Errorf("expected DefaultTTLSeconds 300, got %d", DefaultTTLSeconds)
	}
}

func TestValidateID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid", "worker-1", false},
		{"valid with dots", "loomcli-kv6.1", false},
		{"empty", "", true},
		{"contains colon", "worker:1", true},
		{"contains newline", "worker\n1", true},
		{"contains tab", "worker\t1", true},
		{"contains space", "worker 1", true},
		{"contains carriage return", "worker\r1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateID(tt.id, "testID")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

func TestClaimTask_InvalidInputs(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	_, err := client.ClaimTask(ctx, "", "task-1", "title", "spark")
	if err == nil {
		t.Error("expected error for empty workerID")
	}

	_, err = client.ClaimTask(ctx, "worker-1", "", "title", "spark")
	if err == nil {
		t.Error("expected error for empty taskID")
	}

	_, err = client.ClaimTask(ctx, "worker:bad", "task-1", "title", "spark")
	if err == nil {
		t.Error("expected error for workerID with colon")
	}
}

func TestRedisShouldTrip(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"context.Canceled", context.Canceled, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
		{"net.Error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		{"connection refused string", errors.New("connection refused"), true},
		{"EOF", errors.New("EOF"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"broken pipe", errors.New("broken pipe"), true},
		{"generic error", errors.New("some random error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedisShouldTrip(tt.err)
			if got != tt.want {
				t.Errorf("RedisShouldTrip(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// newTestBreaker creates a circuit breaker suitable for tests.
// High failure threshold so operations pass through without tripping.
func newTestBreaker() *circuitbreaker.Breaker {
	return circuitbreaker.NewBreaker("test", circuitbreaker.Config{
		FailureThreshold: 100,
		OpenTimeout:      30 * time.Second,
		ShouldTrip:       RedisShouldTrip,
	})
}

func TestSetCircuitBreaker(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	b := newTestBreaker()
	client.SetCircuitBreaker(b)

	// Verify breaker is set by running an operation through the breaker path
	result, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected claim to succeed through breaker")
	}
}

func TestClaimTask_WithCircuitBreaker(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	result, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test task", "spark")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got failure with owner: %s", result.ExistingOwner)
	}
}

func TestHeartbeat_WithCircuitBreaker(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	// Claim first (through breaker)
	_, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Heartbeat through breaker
	result, err := client.Heartbeat(ctx, "worker-1")
	if err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected heartbeat success, got error: %s", result.Error)
	}
}

func TestHeartbeat_InvalidInput(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	_, err := client.Heartbeat(ctx, "")
	if err == nil {
		t.Error("expected error for empty workerID")
	}

	_, err = client.Heartbeat(ctx, "worker:bad")
	if err == nil {
		t.Error("expected error for workerID with colon")
	}
}

func TestCompleteTask_WithCircuitBreaker(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	// Claim then complete through breaker
	_, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	result, err := client.CompleteTask(ctx, "worker-1", "task-1")
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
}

func TestCompleteTask_InvalidInputs(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	_, err := client.CompleteTask(ctx, "", "task-1")
	if err == nil {
		t.Error("expected error for empty workerID")
	}

	_, err = client.CompleteTask(ctx, "worker-1", "")
	if err == nil {
		t.Error("expected error for empty taskID")
	}

	_, err = client.CompleteTask(ctx, "worker:bad", "task-1")
	if err == nil {
		t.Error("expected error for workerID with invalid chars")
	}
}

func TestGetStaleWorkers_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	// Seed stale worker
	oldTime := float64(time.Now().Add(-10 * time.Minute).UnixMilli())
	mr.ZAdd(activeWorkersKey(), oldTime, "old-worker")

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

func TestGetWorkerState_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.HSet(workerStateKey("worker-1"), "task_id", "task-1", "state", "working")

	state, err := client.GetWorkerState(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state["task_id"] != "task-1" {
		t.Errorf("expected task_id=task-1, got %s", state["task_id"])
	}
}

func TestSetLeaderKey_WithCircuitBreaker(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

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

func TestDeleteTaskOwner_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.Set(taskOwnerKey("task-1"), "worker-1")

	err := client.DeleteTaskOwner(ctx, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mr.Exists(taskOwnerKey("task-1")) {
		t.Error("key should be deleted")
	}
}

func TestDeleteWorkerState_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.HSet(workerStateKey("worker-1"), "state", "working")

	err := client.DeleteWorkerState(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mr.Exists(workerStateKey("worker-1")) {
		t.Error("key should be deleted")
	}
}

func TestRemoveActiveWorker_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.ZAdd(activeWorkersKey(), 1000, "worker-1")

	err := client.RemoveActiveWorker(ctx, "worker-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	members, _ := mr.ZMembers(activeWorkersKey())
	for _, m := range members {
		if m == "worker-1" {
			t.Error("worker-1 should be removed")
		}
	}
}

func TestRenewLeaderKey_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.Set("test:leader", "server-1")

	err := client.RenewLeaderKey(ctx, "test:leader", 30*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify TTL was set
	ttl := mr.TTL("test:leader")
	if ttl <= 0 {
		t.Errorf("expected positive TTL, got %v", ttl)
	}
}

func TestDeleteLeaderKey_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.Set("test:leader", "server-1")

	err := client.DeleteLeaderKey(ctx, "test:leader")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mr.Exists("test:leader") {
		t.Error("leader key should be deleted")
	}
}

func TestGetTaskOwner_WithCircuitBreaker(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	client.SetCircuitBreaker(newTestBreaker())

	mr.Set(taskOwnerKey("task-1"), "worker-1")

	owner, err := client.GetTaskOwner(ctx, "task-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "worker-1" {
		t.Errorf("expected worker-1, got %s", owner)
	}

	// Non-existent task through breaker
	owner, err = client.GetTaskOwner(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "" {
		t.Errorf("expected empty string, got %s", owner)
	}
}

// --- Edge case tests ---

func TestToInt64_Overflow(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"overflow positive", "9999999999999999999999"},
		{"overflow negative", "-9999999999999999999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := toInt64(tt.input)
			if err == nil {
				t.Errorf("toInt64(%q) expected overflow error, got nil", tt.input)
			}
		})
	}
}

func TestToInt64_EdgeValues(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    int64
		wantErr bool
	}{
		{"max int64 string", fmt.Sprintf("%d", math.MaxInt64), math.MaxInt64, false},
		{"min int64 string", fmt.Sprintf("%d", math.MinInt64), math.MinInt64, false},
		{"zero string", "0", 0, false},
		{"zero int64", int64(0), 0, false},
		{"empty string", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt64(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toInt64(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toInt64(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestToString_EdgeValues(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{"zero int64", int64(0), "0", false},
		{"negative int64", int64(-1), "-1", false},
		{"max int64", int64(math.MaxInt64), fmt.Sprintf("%d", math.MaxInt64), false},
		{"byte slice", []byte("test"), "", true},
		{"bool", true, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("toString(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("toString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateID_UnicodeAndEmoji(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"null char", "worker-\x00", false},
		{"emoji", "worker-emoji-🚀", false},
		{"accented chars", "worker-café", false},
		{"CJK characters", "worker-日本語", false},
		{"mixed unicode", "worker-αβγ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateID(tt.id, "testID")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

// customNetError implements net.Error for testing.
type customNetError struct {
	timeout   bool
	temporary bool
	msg       string
}

func (e *customNetError) Error() string   { return e.msg }
func (e *customNetError) Timeout() bool   { return e.timeout }
func (e *customNetError) Temporary() bool { return e.temporary }

func TestRedisShouldTrip_CustomNetError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"timeout and temporary",
			&customNetError{timeout: true, temporary: true, msg: "custom timeout"},
			true,
		},
		{
			"not timeout not temporary",
			&customNetError{timeout: false, temporary: false, msg: "custom error"},
			true,
		},
		{
			"wrapped net.Error",
			fmt.Errorf("wrapped: %w", &customNetError{msg: "inner net error"}),
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedisShouldTrip(tt.err)
			if got != tt.want {
				t.Errorf("RedisShouldTrip(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRedisShouldTrip_WrappedErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"wrapped context.Canceled not caught by ==",
			fmt.Errorf("wrapper: %w", context.Canceled),
			false,
		},
		{
			"connection refused string match through wrapping",
			fmt.Errorf("connection refused: %w", errors.New("inner")),
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedisShouldTrip(tt.err)
			if got != tt.want {
				t.Errorf("RedisShouldTrip(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// newOpenBreaker creates a circuit breaker that is already in OPEN state.
func newOpenBreaker() *circuitbreaker.Breaker {
	b := circuitbreaker.NewBreaker("test-open", circuitbreaker.Config{
		FailureThreshold: 1,
		OpenTimeout:      1 * time.Hour, // long timeout so it stays open
		ShouldTrip:       func(err error) bool { return true },
	})
	// Trip the breaker by executing a failing function
	_ = b.Execute(func() error { return errors.New("trip") })
	return b
}

func TestCircuitBreaker_OpenState_ClaimTask(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_Heartbeat(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.Heartbeat(ctx, "worker-1")
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_CompleteTask(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.CompleteTask(ctx, "worker-1", "task-1")
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_GetStaleWorkers(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.GetStaleWorkers(ctx, 5*time.Minute)
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_GetWorkerState(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.GetWorkerState(ctx, "worker-1")
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_GetTaskOwner(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.GetTaskOwner(ctx, "task-1")
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_SetLeaderKey(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	_, err := client.SetLeaderKey(ctx, "test:leader", "server-1", 30*time.Second)
	if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreaker_OpenState_DeleteOps(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()
	client.SetCircuitBreaker(newOpenBreaker())

	tests := []struct {
		name string
		fn   func() error
	}{
		{"DeleteTaskOwner", func() error { return client.DeleteTaskOwner(ctx, "task-1") }},
		{"DeleteWorkerState", func() error { return client.DeleteWorkerState(ctx, "worker-1") }},
		{"RemoveActiveWorker", func() error { return client.RemoveActiveWorker(ctx, "worker-1") }},
		{"RenewLeaderKey", func() error { return client.RenewLeaderKey(ctx, "test:leader", 30*time.Second) }},
		{"DeleteLeaderKey", func() error { return client.DeleteLeaderKey(ctx, "test:leader") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if !errors.Is(err, circuitbreaker.ErrCircuitOpen) {
				t.Errorf("expected ErrCircuitOpen, got %v", err)
			}
		})
	}
}

func TestClaimTask_ScriptError(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	mr.Close()

	_, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if err == nil {
		t.Fatal("expected error when Redis is closed")
	}
	if !strings.Contains(err.Error(), "claim script failed") {
		t.Errorf("expected error to contain 'claim script failed', got: %v", err)
	}
}

func TestHeartbeat_ScriptError(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	mr.Close()

	_, err := client.Heartbeat(ctx, "worker-1")
	if err == nil {
		t.Fatal("expected error when Redis is closed")
	}
	if !strings.Contains(err.Error(), "heartbeat script failed") {
		t.Errorf("expected error to contain 'heartbeat script failed', got: %v", err)
	}
}

func TestCompleteTask_ScriptError(t *testing.T) {
	client, mr := setupTest(t)
	ctx := context.Background()

	mr.Close()

	_, err := client.CompleteTask(ctx, "worker-1", "task-1")
	if err == nil {
		t.Fatal("expected error when Redis is closed")
	}
	if !strings.Contains(err.Error(), "complete script failed") {
		t.Errorf("expected error to contain 'complete script failed', got: %v", err)
	}
}

func TestClaimTask_LuaCompilationError(t *testing.T) {
	client, _ := setupTest(t)
	ctx := context.Background()

	// Save original and replace with invalid Lua that triggers a compilation error
	original := claimScript
	claimScript = redis.NewScript("this is !!! invalid lua syntax")
	t.Cleanup(func() { claimScript = original })

	_, err := client.ClaimTask(ctx, "worker-1", "task-1", "Test", "spark")
	if err == nil {
		t.Fatal("expected error for Lua compilation failure")
	}
	if !strings.Contains(err.Error(), "claim script failed") {
		t.Errorf("expected error to contain 'claim script failed', got: %v", err)
	}
}
