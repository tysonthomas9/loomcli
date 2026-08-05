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
)

// WorkspaceJobStore manages async workspace mutation jobs. Jobs are keyed by
// UUID in a sync.Map. Terminal jobs (done or failed) expire after
// jobExpiryDuration. A background goroutine cleans up expired entries.
//
// Follows the terminalAuth done/stopOnce pattern for lifecycle management.
type WorkspaceJobStore struct {
	jobs     sync.Map // map[string]*service.WorkspaceJob
	done     chan struct{}
	stopOnce sync.Once
}

// NewWorkspaceJobStore creates a new job store and starts its cleanup goroutine.
func NewWorkspaceJobStore() *WorkspaceJobStore {
	s := &WorkspaceJobStore{
		done: make(chan struct{}),
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
	return s.start(id, req.WorkspaceID, "repository attachment failed", func(ctx context.Context) (service.WorkspaceCreateResult, error) {
		return addReposFn(ctx, req)
	})
}

type workspaceJobRun func(context.Context) (service.WorkspaceCreateResult, error)

// start stores an initial running snapshot, executes run under the job timeout,
// and replaces the snapshot exactly once with a terminal result.
func (s *WorkspaceJobStore) start(
	id, target, fallbackError string,
	run workspaceJobRun,
) string {
	s.jobs.Store(id, &service.WorkspaceJob{
		ID:       id,
		Status:   service.JobStatusRunning,
		Progress: "cloning repository...",
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobCreateTimeout)
		defer cancel()

		result, err := run(ctx)
		if err != nil {
			errMsg := sanitizeJobError(err, fallbackError)
			s.jobs.Store(id, &service.WorkspaceJob{
				ID:          id,
				Status:      service.JobStatusFailed,
				Error:       errMsg,
				CompletedAt: time.Now(),
			})
			logger.Warn("async workspace mutation failed",
				"job_id", id, "target", target, "err", err)
			return
		}

		s.jobs.Store(id, &service.WorkspaceJob{
			ID:          id,
			Status:      service.JobStatusDone,
			WorkspaceID: result.WorkspaceID,
			CompletedAt: time.Now(),
		})
		logger.Info("async workspace mutation completed",
			"job_id", id, "target", target, "workspace_id", result.WorkspaceID)
	}()

	return id
}

// Get returns the current state of a job, or nil if not found / expired.
func (s *WorkspaceJobStore) Get(id string) *service.WorkspaceJob {
	v, ok := s.jobs.Load(id)
	if !ok {
		return nil
	}
	return v.(*service.WorkspaceJob)
}

// Stop stops the cleanup goroutine. Safe to call multiple times.
func (s *WorkspaceJobStore) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
	})
}

// cleanupLoop periodically evicts terminal jobs that have expired.
func (s *WorkspaceJobStore) cleanupLoop() {
	ticker := time.NewTicker(jobCleanupInterval)
	defer ticker.Stop()

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
	cutoff := time.Now().Add(-jobExpiryDuration)
	s.jobs.Range(func(key, value any) bool {
		job := value.(*service.WorkspaceJob)
		if !job.CompletedAt.IsZero() && job.CompletedAt.Before(cutoff) {
			s.jobs.Delete(key)
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
