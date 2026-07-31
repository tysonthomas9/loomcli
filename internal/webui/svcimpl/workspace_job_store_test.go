package svcimpl

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

func TestWorkspaceJobStore_StartPreparedSuppressesDuplicateRunningRunner(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	var calls atomic.Int32
	started := make(chan struct{})
	proceed := make(chan struct{})
	createFn := func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-proceed
		return service.WorkspaceCreateResult{WorkspaceID: "CLONE-WS"}, nil
	}
	req := service.WorkspaceCreateRequest{Name: "clone_ws", Type: "clone"}

	if id := store.StartPrepared("admission-create-1", req, createFn); id != "admission-create-1" {
		t.Fatalf("first job ID = %q, want exact prepared ID", id)
	}
	<-started
	if id := store.StartPrepared("admission-create-1", req, createFn); id != "admission-create-1" {
		t.Fatalf("duplicate job ID = %q, want exact prepared ID", id)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("runner calls = %d, want one while prepared job is running", got)
	}
	close(proceed)
	waitForWorkspaceJobStatus(t, store, "admission-create-1", service.JobStatusDone)
}

func TestWorkspaceJobStore_StartPreparedAllowsRetryAfterTerminalResult(t *testing.T) {
	store := NewWorkspaceJobStore()
	defer store.Stop()

	var calls atomic.Int32
	req := service.WorkspaceCreateRequest{Name: "clone_ws", Type: "clone"}
	first := func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		calls.Add(1)
		return service.WorkspaceCreateResult{}, fmt.Errorf("first attempt failed")
	}
	store.StartPrepared("admission-create-retry", req, first)
	waitForWorkspaceJobStatus(t, store, "admission-create-retry", service.JobStatusFailed)

	second := func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
		calls.Add(1)
		return service.WorkspaceCreateResult{WorkspaceID: "CLONE-WS"}, nil
	}
	if id := store.StartPrepared("admission-create-retry", req, second); id != "admission-create-retry" {
		t.Fatalf("retry job ID = %q, want exact prepared ID", id)
	}
	job := waitForWorkspaceJobStatus(t, store, "admission-create-retry", service.JobStatusDone)
	if got := calls.Load(); got != 2 {
		t.Fatalf("runner calls = %d, want explicit terminal retry to run", got)
	}
	if job.WorkspaceID != "CLONE-WS" {
		t.Fatalf("workspace ID = %q, want CLONE-WS", job.WorkspaceID)
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

func TestWorkspaceJobStore_ConcurrentStopCancelsActiveJobAndWaitsForUnwind(t *testing.T) {
	store := NewWorkspaceJobStore()

	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	unwound := make(chan struct{})
	var releaseOnce sync.Once
	releaseRun := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	defer releaseRun()

	id := store.StartPrepared(
		"shutdown-active-job",
		service.WorkspaceCreateRequest{Name: "shutdown", Type: "clone"},
		func(ctx context.Context, _ service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			close(unwound)
			// A callback that reports success after cancellation must not
			// overwrite the store's deterministic shutdown failure.
			return service.WorkspaceCreateResult{WorkspaceID: "TOO-LATE"}, nil
		},
	)
	waitForWorkspaceJobSignal(t, started, "workspace job start")

	const stopCallers = 16
	var stops sync.WaitGroup
	stops.Add(stopCallers)
	stopReturned := make(chan struct{}, stopCallers)
	for range stopCallers {
		go func() {
			defer stops.Done()
			store.Stop()
			stopReturned <- struct{}{}
		}()
	}

	waitForWorkspaceJobSignal(t, cancelled, "active workspace job cancellation")
	select {
	case <-stopReturned:
		t.Fatal("Stop returned before the active workspace job unwound")
	default:
	}

	job := store.Get(id)
	if job == nil {
		t.Fatal("shutdown job snapshot is nil")
	}
	if job.Status != service.JobStatusFailed {
		t.Fatalf("shutdown job status = %q, want failed", job.Status)
	}
	if job.Error != jobShutdownError {
		t.Fatalf("shutdown job error = %q, want %q", job.Error, jobShutdownError)
	}
	if job.CompletedAt.IsZero() {
		t.Fatal("shutdown job must have a terminal completion time")
	}

	releaseRun()
	waitForWorkspaceJobSignal(t, unwound, "active workspace job unwind")
	stopsDone := make(chan struct{})
	go func() {
		stops.Wait()
		close(stopsDone)
	}()
	waitForWorkspaceJobSignal(t, stopsDone, "concurrent Stop callers")

	job = store.Get(id)
	if job == nil || job.Status != service.JobStatusFailed || job.Error != jobShutdownError {
		t.Fatalf("late callback replaced shutdown snapshot: %+v", job)
	}
	if job.WorkspaceID != "" {
		t.Fatalf("shutdown workspace ID = %q, want empty", job.WorkspaceID)
	}
}

func TestWorkspaceJobStore_StopWaitIsBoundedWhenJobIgnoresCancellation(t *testing.T) {
	store := NewWorkspaceJobStore()
	store.shutdownTimeout = 20 * time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var releaseOnce sync.Once
	releaseRun := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	defer releaseRun()

	id := store.StartPrepared(
		"shutdown-bounded-job",
		service.WorkspaceCreateRequest{Name: "shutdown", Type: "clone"},
		func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
			close(started)
			<-release
			close(finished)
			return service.WorkspaceCreateResult{WorkspaceID: "TOO-LATE"}, nil
		},
	)
	waitForWorkspaceJobSignal(t, started, "workspace job start")

	stopStarted := time.Now()
	store.Stop()
	if elapsed := time.Since(stopStarted); elapsed > time.Second {
		t.Fatalf("Stop elapsed = %v, want bounded wait under one second", elapsed)
	}

	job := store.Get(id)
	if job == nil || job.Status != service.JobStatusFailed || job.Error != jobShutdownError {
		t.Fatalf("bounded Stop snapshot = %+v, want shutdown failure", job)
	}

	releaseRun()
	waitForWorkspaceJobSignal(t, finished, "ignored-cancellation job completion")
	waitForWorkspaceJobInactive(t, store, id)
	job = waitForWorkspaceJobStatus(t, store, id, service.JobStatusFailed)
	if job.Error != jobShutdownError || job.WorkspaceID != "" {
		t.Fatalf("late success replaced bounded Stop snapshot: %+v", job)
	}
}

func TestWorkspaceJobStore_StartAfterStopDoesNotLaunch(t *testing.T) {
	store := NewWorkspaceJobStore()
	store.Stop()

	var calls atomic.Int32
	id := store.StartPrepared(
		"post-stop-job",
		service.WorkspaceCreateRequest{Name: "shutdown", Type: "clone"},
		func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
			calls.Add(1)
			return service.WorkspaceCreateResult{WorkspaceID: "MUST-NOT-RUN"}, nil
		},
	)
	if got := calls.Load(); got != 0 {
		t.Fatalf("post-stop runner calls = %d, want zero", got)
	}
	job := store.Get(id)
	if job == nil || job.Status != service.JobStatusFailed || job.Error != jobShutdownError {
		t.Fatalf("post-stop snapshot = %+v, want shutdown failure", job)
	}
	if job.CompletedAt.IsZero() {
		t.Fatal("post-stop rejection must be terminal")
	}

	completed := &service.WorkspaceJob{
		ID:          "completed-before-stop",
		Status:      service.JobStatusDone,
		WorkspaceID: "EXISTING",
		CompletedAt: time.Now(),
	}
	store.jobs.Store(completed.ID, completed)
	store.StartPrepared(
		completed.ID,
		service.WorkspaceCreateRequest{Name: "retry", Type: "clone"},
		func(context.Context, service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
			calls.Add(1)
			return service.WorkspaceCreateResult{WorkspaceID: "MUST-NOT-RETRY"}, nil
		},
	)
	if got := calls.Load(); got != 0 {
		t.Fatalf("post-stop retry runner calls = %d, want zero", got)
	}
	if got := store.Get(completed.ID); got != completed {
		t.Fatalf("post-stop retry replaced existing terminal snapshot: %+v", got)
	}
}

func TestWorkspaceJobStore_StartStopRaceIsSafe(t *testing.T) {
	for iteration := range 64 {
		store := NewWorkspaceJobStore()
		gate := make(chan struct{})
		id := fmt.Sprintf("start-stop-race-%d", iteration)
		var calls atomic.Int32
		var racers sync.WaitGroup
		racers.Add(2)

		go func() {
			defer racers.Done()
			<-gate
			store.StartPrepared(
				id,
				service.WorkspaceCreateRequest{Name: "race", Type: "clone"},
				func(ctx context.Context, _ service.WorkspaceCreateRequest) (service.WorkspaceCreateResult, error) {
					calls.Add(1)
					<-ctx.Done()
					return service.WorkspaceCreateResult{}, ctx.Err()
				},
			)
		}()
		go func() {
			defer racers.Done()
			<-gate
			store.Stop()
		}()

		close(gate)
		racers.Wait()

		if got := calls.Load(); got > 1 {
			t.Fatalf("iteration %d runner calls = %d, want at most one", iteration, got)
		}
		job := store.Get(id)
		if job == nil || job.Status != service.JobStatusFailed || job.Error != jobShutdownError {
			t.Fatalf("iteration %d final snapshot = %+v, want shutdown failure", iteration, job)
		}
		if job.CompletedAt.IsZero() {
			t.Fatalf("iteration %d final snapshot is not terminal", iteration)
		}
	}
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

func waitForWorkspaceJobInactive(t *testing.T, store *WorkspaceJobStore, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		store.lifecycleMu.Lock()
		_, active := store.active[id]
		store.lifecycleMu.Unlock()
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %q remained active after its callback returned", id)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForWorkspaceJobSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForWorkspaceJobStatus(
	t *testing.T,
	store *WorkspaceJobStore,
	id string,
	status service.WorkspaceJobStatus,
) *service.WorkspaceJob {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		job := store.Get(id)
		if job != nil && job.Status == status {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %q = %+v, want status %q", id, job, status)
		}
		time.Sleep(time.Millisecond)
	}
}
