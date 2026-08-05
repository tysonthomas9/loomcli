package svcimpl

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

const (
	jobCleanupInterval = 1 * time.Minute
	jobExpiryDuration  = 5 * time.Minute
	jobCreateTimeout   = 5 * time.Minute
	jobShutdownTimeout = 5 * time.Second
	jobShutdownError   = "workspace operation interrupted by server shutdown"
)

// WorkspaceJobStore manages async workspace mutation jobs. Jobs are keyed by
// UUID in a sync.Map. Terminal jobs (done or failed) expire after
// jobExpiryDuration. A background goroutine cleans up expired entries.
//
// The lifecycle mutex serializes starts, terminal publication, and shutdown.
// This lets Stop prevent new work, publish deterministic shutdown snapshots,
// cancel every accepted run, and wait for its completion without racing a
// terminal retry.
type WorkspaceJobStore struct {
	jobs            sync.Map // map[string]*service.WorkspaceJob
	lifecycleMu     sync.Mutex
	active          map[string]*workspaceJobExecution
	stopped         bool
	done            chan struct{}
	cleanupDone     chan struct{}
	shutdownTimeout time.Duration
	stopOnce        sync.Once
}

type workspaceJobExecution struct {
	id      string
	running *service.WorkspaceJob
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewWorkspaceJobStore creates a new job store and starts its cleanup goroutine.
func NewWorkspaceJobStore() *WorkspaceJobStore {
	s := &WorkspaceJobStore{
		active:          make(map[string]*workspaceJobExecution),
		done:            make(chan struct{}),
		cleanupDone:     make(chan struct{}),
		shutdownTimeout: jobShutdownTimeout,
	}
	go s.cleanupLoop()
	return s
}

// Start launches an async workspace creation job.
func (s *WorkspaceJobStore) Start(req service.WorkspaceCreateRequest, createFn service.WorkspaceCreateFn) string {
	id := uuid.New().String()
	if req.Type == "clone" && req.Name != "" {
		id = service.WorkspaceKeyFromName(req.Name)
	}
	return s.StartPrepared(id, req, createFn)
}

// StartPrepared launches an async workspace creation job under an exact,
// durable admission ID.
func (s *WorkspaceJobStore) StartPrepared(
	id string,
	req service.WorkspaceCreateRequest,
	createFn service.WorkspaceCreateFn,
) string {
	return s.start(id, req.Name, "workspace creation failed", func(ctx context.Context) (service.WorkspaceCreateResult, error) {
		return createFn(ctx, req)
	})
}

// StartAddRepos launches an async remote-repository attachment job. Add-repo
// jobs use opaque UUIDs so repeated attachments to the same workspace do not
// overwrite one another or collide with durable workspace-creation job IDs.
func (s *WorkspaceJobStore) StartAddRepos(
	req service.WorkspaceAddReposRequest,
	addReposFn service.WorkspaceAddReposFn,
) string {
	id := uuid.New().String()
	return s.StartPreparedAddRepos(id, req, addReposFn)
}

// StartPreparedAddRepos launches an async repository-attachment job under an
// exact, durable admission ID.
func (s *WorkspaceJobStore) StartPreparedAddRepos(
	id string,
	req service.WorkspaceAddReposRequest,
	addReposFn service.WorkspaceAddReposFn,
) string {
	return s.start(id, req.WorkspaceID, "repository attachment failed", func(ctx context.Context) (service.WorkspaceCreateResult, error) {
		return addReposFn(ctx, req)
	})
}

type workspaceJobRun func(context.Context) (service.WorkspaceCreateResult, error)

// start atomically installs an initial running snapshot, executes run under the
// job timeout, and replaces that exact snapshot with a terminal result. A
// duplicate start while the same ID is running is suppressed. A later explicit
// start may replace a terminal snapshot to retry the durable admission.
func (s *WorkspaceJobStore) start(
	id, target, fallbackError string,
	run workspaceJobRun,
) string {
	running := &service.WorkspaceJob{
		ID:       id,
		Status:   service.JobStatusRunning,
		Progress: "cloning repository...",
	}

	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	if s.stopped {
		s.storeShutdownSnapshot(id, time.Now())
		return id
	}

	for {
		current, loaded := s.jobs.LoadOrStore(id, running)
		if !loaded {
			break
		}
		job := current.(*service.WorkspaceJob)
		if job.Status == service.JobStatusRunning {
			return id
		}
		if s.jobs.CompareAndSwap(id, current, running) {
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), jobCreateTimeout)
	execution := &workspaceJobExecution{
		id:      id,
		running: running,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	s.active[id] = execution
	go func() {
		s.execute(ctx, target, fallbackError, run, execution)
	}()

	return id
}

func (s *WorkspaceJobStore) execute(
	ctx context.Context,
	target, fallbackError string,
	run workspaceJobRun,
	execution *workspaceJobExecution,
) {
	result, err := run(ctx)
	completedAt := time.Now()

	var terminal *service.WorkspaceJob
	if err != nil {
		terminal = &service.WorkspaceJob{
			ID:          execution.id,
			Status:      service.JobStatusFailed,
			Error:       sanitizeJobError(err, fallbackError),
			CompletedAt: completedAt,
		}
	} else {
		terminal = &service.WorkspaceJob{
			ID:          execution.id,
			Status:      service.JobStatusDone,
			WorkspaceID: result.WorkspaceID,
			CompletedAt: completedAt,
		}
	}

	s.lifecycleMu.Lock()
	published := s.jobs.CompareAndSwap(execution.id, execution.running, terminal)
	execution.cancel()
	close(execution.done)
	delete(s.active, execution.id)
	s.lifecycleMu.Unlock()

	if !published {
		return
	}
	if err != nil {
		logger.Warn("async workspace mutation failed",
			"job_id", execution.id, "target", target, "err", err)
		return
	}
	logger.Info("async workspace mutation completed",
		"job_id", execution.id, "target", target, "workspace_id", result.WorkspaceID)
}

// Get returns the current state of a job, or nil if not found / expired.
func (s *WorkspaceJobStore) Get(id string) *service.WorkspaceJob {
	v, ok := s.jobs.Load(id)
	if !ok {
		return nil
	}
	return v.(*service.WorkspaceJob)
}

// Stop prevents new jobs, stops cleanup, cancels active runs, and waits up to
// jobShutdownTimeout for accepted callbacks to unwind. It is safe to call
// concurrently or multiple times.
func (s *WorkspaceJobStore) Stop() {
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopped = true
		close(s.done)

		completions := make([]<-chan struct{}, 0, len(s.active)+1)
		completions = append(completions, s.cleanupDone)
		stoppedAt := time.Now()
		for _, execution := range s.active {
			s.jobs.CompareAndSwap(
				execution.id,
				execution.running,
				newWorkspaceJobShutdownSnapshot(execution.id, stoppedAt),
			)
			execution.cancel()
			completions = append(completions, execution.done)
		}
		s.lifecycleMu.Unlock()

		waitForWorkspaceJobShutdown(completions, s.shutdownTimeout)
	})
}

func (s *WorkspaceJobStore) storeShutdownSnapshot(id string, completedAt time.Time) {
	shutdown := newWorkspaceJobShutdownSnapshot(id, completedAt)
	for {
		current, loaded := s.jobs.LoadOrStore(id, shutdown)
		if !loaded {
			return
		}
		job := current.(*service.WorkspaceJob)
		if job.Status != service.JobStatusRunning {
			return
		}
		if s.jobs.CompareAndSwap(id, current, shutdown) {
			return
		}
	}
}

func newWorkspaceJobShutdownSnapshot(id string, completedAt time.Time) *service.WorkspaceJob {
	return &service.WorkspaceJob{
		ID:          id,
		Status:      service.JobStatusFailed,
		Error:       jobShutdownError,
		CompletedAt: completedAt,
	}
}

func waitForWorkspaceJobShutdown(completions []<-chan struct{}, timeout time.Duration) {
	if len(completions) == 0 || timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, completion := range completions {
		select {
		case <-completion:
		case <-timer.C:
			return
		}
	}
}

// cleanupLoop periodically evicts terminal jobs that have expired.
func (s *WorkspaceJobStore) cleanupLoop() {
	ticker := time.NewTicker(jobCleanupInterval)
	defer ticker.Stop()
	defer close(s.cleanupDone)

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.evictExpired()
		}
	}
}

// evictExpired removes jobs that completed more than jobExpiryDuration ago.
func (s *WorkspaceJobStore) evictExpired() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()

	cutoff := time.Now().Add(-jobExpiryDuration)
	s.jobs.Range(func(key, value any) bool {
		job := value.(*service.WorkspaceJob)
		if !job.CompletedAt.IsZero() && job.CompletedAt.Before(cutoff) {
			s.jobs.CompareAndDelete(key, value)
		}
		return true
	})
}

// sanitizeJobError extracts a user-facing message from a creation error.
// If the error implements the createErrorer interface (matching workspaceerrors.CreateError),
// use its Message field. Otherwise return a generic message to avoid leaking internals.
func sanitizeJobError(err error, fallback string) string {
	type createErrorer interface {
		error
		CreateMessage() string
	}
	// Walk the error chain manually to find a createErrorer.
	for e := err; e != nil; {
		if ce, ok := e.(createErrorer); ok {
			return ce.CreateMessage()
		}
		// Try unwrapping.
		if u, ok := e.(interface{ Unwrap() error }); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return fallback
}
