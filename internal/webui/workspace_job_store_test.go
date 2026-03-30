package webui

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestWorkspaceJobStore_StartReturnsUUID(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		return WorkspaceCreateResult{WorkspaceID: "ws-123"}, nil
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)
	if id == "" {
		t.Fatal("expected non-empty job ID")
	}
	// UUID format: 8-4-4-4-12
	if len(id) != 36 {
		t.Errorf("expected UUID format (36 chars), got %d chars: %s", len(id), id)
	}
}

func TestWorkspaceJobStore_GetRunningJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	started := make(chan struct{})
	proceed := make(chan struct{})

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		close(started)
		<-proceed
		return WorkspaceCreateResult{WorkspaceID: "ws-123"}, nil
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	// Wait for goroutine to start
	<-started

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != JobStatusRunning {
		t.Errorf("expected status %q, got %q", JobStatusRunning, job.Status)
	}
	if job.Progress == "" {
		t.Error("expected non-empty progress for running job")
	}

	close(proceed)
}

func TestWorkspaceJobStore_GetCompletedJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{WorkspaceID: "ws-456"}, nil
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	// Wait for completion
	<-done
	// Small delay to allow sync.Map.Store to propagate
	time.Sleep(10 * time.Millisecond)

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != JobStatusDone {
		t.Errorf("expected status %q, got %q", JobStatusDone, job.Status)
	}
	if job.WorkspaceID != "ws-456" {
		t.Errorf("expected workspace_id %q, got %q", "ws-456", job.WorkspaceID)
	}
	if job.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set")
	}
}

func TestWorkspaceJobStore_GetFailedJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})

	createFn := func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error) {
		defer close(done)
		return WorkspaceCreateResult{}, fmt.Errorf("git clone failed: exit 128")
	}

	id := store.Start(WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	<-done
	time.Sleep(10 * time.Millisecond)

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != JobStatusFailed {
		t.Errorf("expected status %q, got %q", JobStatusFailed, job.Status)
	}
	// Non-CreateError errors are sanitized to a generic message.
	if job.Error != "workspace creation failed" {
		t.Errorf("expected error %q, got %q", "workspace creation failed", job.Error)
	}
	if job.CompletedAt.IsZero() {
		t.Error("expected CompletedAt to be set")
	}
}

func TestWorkspaceJobStore_GetUnknownID(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	job := store.Get("nonexistent-id")
	if job != nil {
		t.Errorf("expected nil for unknown job, got %+v", job)
	}
}

func TestWorkspaceJobStore_StopIdempotent(t *testing.T) {
	store := NewWorkspaceJobStore()
	// Calling Stop multiple times should not panic.
	store.Stop()
	store.Stop()
	store.Stop()
}

func TestWorkspaceJobStore_EvictExpired(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	// Manually insert a terminal job with an old CompletedAt.
	store.jobs.Store("old-job", &WorkspaceJob{
		ID:          "old-job",
		Status:      JobStatusDone,
		WorkspaceID: "ws-old",
		CompletedAt: time.Now().Add(-10 * time.Minute), // expired
	})

	// Insert a running job (should never be evicted).
	store.jobs.Store("running-job", &WorkspaceJob{
		ID:     "running-job",
		Status: JobStatusRunning,
	})

	// Insert a recently completed job (should NOT be evicted).
	store.jobs.Store("recent-job", &WorkspaceJob{
		ID:          "recent-job",
		Status:      JobStatusDone,
		CompletedAt: time.Now(),
	})

	store.evictExpired()

	if store.Get("old-job") != nil {
		t.Error("expected old-job to be evicted")
	}
	if store.Get("running-job") == nil {
		t.Error("expected running-job to NOT be evicted")
	}
	if store.Get("recent-job") == nil {
		t.Error("expected recent-job to NOT be evicted")
	}
}
