package kv

import (
	"context"
	"testing"
	"time"
)

func TestNewReconciler_DefaultPath(t *testing.T) {
	r := NewReconciler("")
	if r.bdPath != "bd" {
		t.Errorf("expected default bdPath 'bd', got %s", r.bdPath)
	}
}

func TestNewReconciler_CustomPath(t *testing.T) {
	r := NewReconciler("/usr/local/bin/bd")
	if r.bdPath != "/usr/local/bin/bd" {
		t.Errorf("expected bdPath '/usr/local/bin/bd', got %s", r.bdPath)
	}
}

func TestReconciler_ResetOrphanedTask_BinaryNotFound(t *testing.T) {
	// Use a non-existent binary to test error handling
	r := NewReconciler("/nonexistent/bd-binary")
	ctx := context.Background()

	tasks := []OrphanedTask{
		{TaskID: "task-1", TaskTitle: "Test task", WorkerID: "worker-1", StaleSince: time.Now()},
	}

	results := r.ResetOrphanedTasks(ctx, tasks)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Success {
		t.Error("expected failure when binary not found")
	}
	if results[0].Error == nil {
		t.Error("expected non-nil error")
	}
	if results[0].TaskID != "task-1" {
		t.Errorf("expected task-1, got %s", results[0].TaskID)
	}
}

func TestReconciler_MultipleOrphanedTasks(t *testing.T) {
	// Use a non-existent binary — all tasks should fail
	r := NewReconciler("/nonexistent/bd-binary")
	ctx := context.Background()

	tasks := []OrphanedTask{
		{TaskID: "task-1", TaskTitle: "First", WorkerID: "w1"},
		{TaskID: "task-2", TaskTitle: "Second", WorkerID: "w2"},
		{TaskID: "task-3", TaskTitle: "Third", WorkerID: "w3"},
	}

	results := r.ResetOrphanedTasks(ctx, tasks)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Success {
			t.Errorf("result[%d] should have failed", i)
		}
		if r.TaskID != tasks[i].TaskID {
			t.Errorf("result[%d] taskID = %s, want %s", i, r.TaskID, tasks[i].TaskID)
		}
	}
}

func TestReconciler_EmptyTasks(t *testing.T) {
	r := NewReconciler("")
	ctx := context.Background()

	results := r.ResetOrphanedTasks(ctx, nil)
	if len(results) != 0 {
		t.Errorf("expected 0 results for nil tasks, got %d", len(results))
	}

	results = r.ResetOrphanedTasks(ctx, []OrphanedTask{})
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty tasks, got %d", len(results))
	}
}
