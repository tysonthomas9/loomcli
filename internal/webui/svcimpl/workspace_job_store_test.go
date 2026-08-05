package svcimpl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestWorkspaceJobStore_StartReturnsWorkspaceKeyForClone(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	createFn := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		return service.WorkspaceCreateResult{WorkspaceID: "ws-123"}, nil
	}

	id := store.Start(service.WorkspaceCreateRequest{Name: "clone_ws", Type: "clone"}, createFn)
	if id != "CLONE-WS" {
		t.Fatalf("job id = %q, want CLONE-WS", id)
	}
}

func TestWorkspaceJobStore_GetRunningJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	started := make(chan struct{})
	proceed := make(chan struct{})

	createFn := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		close(started)
		<-proceed
		return service.WorkspaceCreateResult{WorkspaceID: "ws-123"}, nil
	}

	id := store.Start(service.WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	// Wait for goroutine to start
	<-started

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != service.JobStatusRunning {
		t.Errorf("expected status %q, got %q", service.JobStatusRunning, job.Status)
	}
	if job.Progress == "" {
		t.Error("expected non-empty progress for running job")
	}

	close(proceed)
}

func TestWorkspaceJobStore_StartAddReposRunsOutsideRequestAndUsesOpaqueJobID(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	started := make(chan struct{})
	proceed := make(chan struct{})
	addReposFn := func(ctx context.Context, req service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
		if req.WorkspaceID != "ALPHA" {
			t.Errorf("workspace ID = %q, want ALPHA", req.WorkspaceID)
		}
		close(started)
		<-proceed
		return service.WorkspaceCreateResult{WorkspaceID: "ALPHA"}, nil
	}

	id := store.StartAddRepos(service.WorkspaceAddReposRequest{
		WorkspaceID: "ALPHA",
		CloneURLs:   []string{"https://github.com/acme/slow.git"},
	}, addReposFn)
	if id == "" || id == "ALPHA" {
		t.Fatalf("job ID = %q, want non-workspace opaque ID", id)
	}

	<-started
	job := store.Get(id)
	if job == nil || job.Status != service.JobStatusRunning {
		t.Fatalf("job = %+v, want running while clone is blocked", job)
	}

	close(proceed)
	deadline := time.Now().Add(time.Second)
	for {
		job = store.Get(id)
		if job != nil && job.Status == service.JobStatusDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job = %+v, want done", job)
		}
		time.Sleep(time.Millisecond)
	}
	if job.WorkspaceID != "ALPHA" {
		t.Fatalf("workspace ID = %q, want ALPHA", job.WorkspaceID)
	}
}

func TestWorkspaceJobStore_StartAddReposUsesAttachmentFailureMessage(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})
	id := store.StartAddRepos(
		service.WorkspaceAddReposRequest{WorkspaceID: "ALPHA"},
		func(context.Context, service.WorkspaceAddReposRequest) (service.WorkspaceCreateResult, error) {
			defer close(done)
			return service.WorkspaceCreateResult{}, fmt.Errorf("unclassified clone failure")
		},
	)
	<-done

	deadline := time.Now().Add(time.Second)
	for {
		job := store.Get(id)
		if job != nil && job.Status == service.JobStatusFailed {
			if job.Error != "repository attachment failed" {
				t.Fatalf("error = %q, want sanitized attachment failure", job.Error)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %q did not fail", id)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceJobStore_GetCompletedJob(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	done := make(chan struct{})

	createFn := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		defer close(done)
		return service.WorkspaceCreateResult{WorkspaceID: "ws-456"}, nil
	}

	id := store.Start(service.WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	// Wait for completion
	<-done
	// Small delay to allow sync.Map.Store to propagate
	time.Sleep(10 * time.Millisecond)

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != service.JobStatusDone {
		t.Errorf("expected status %q, got %q", service.JobStatusDone, job.Status)
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

	createFn := func(ctx context.Context, req service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		defer close(done)
		return service.WorkspaceCreateResult{}, fmt.Errorf("git clone failed: exit 128")
	}

	id := store.Start(service.WorkspaceCreateRequest{Name: "test", Type: "clone"}, createFn)

	<-done
	time.Sleep(10 * time.Millisecond)

	job := store.Get(id)
	if job == nil {
		t.Fatal("expected job to exist")
	}
	if job.Status != service.JobStatusFailed {
		t.Errorf("expected status %q, got %q", service.JobStatusFailed, job.Status)
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
	store.jobs.Store("old-job", &service.WorkspaceJob{
		ID:          "old-job",
		Status:      service.JobStatusDone,
		WorkspaceID: "ws-old",
		CompletedAt: time.Now().Add(-10 * time.Minute), // expired
	})

	// Insert a running job (should never be evicted).
	store.jobs.Store("running-job", &service.WorkspaceJob{
		ID:     "running-job",
		Status: service.JobStatusRunning,
	})

	// Insert a recently completed job (should NOT be evicted).
	store.jobs.Store("recent-job", &service.WorkspaceJob{
		ID:          "recent-job",
		Status:      service.JobStatusDone,
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
