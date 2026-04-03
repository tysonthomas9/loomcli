package webui

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

const (
	jobCleanupInterval = 1 * time.Minute
	jobExpiryDuration  = 5 * time.Minute
	jobCreateTimeout   = 5 * time.Minute
)

// WorkspaceJobStore manages async workspace creation jobs. Jobs are keyed by
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

// Start launches an async workspace creation job. It stores an initial
// "running" entry, spawns a goroutine that calls createFn, and returns the
// job ID immediately.
func (s *WorkspaceJobStore) Start(req service.WorkspaceCreateRequest, createFn service.WorkspaceCreateFn) string {
	id := uuid.New().String()

	// Store initial running state.
	s.jobs.Store(id, &service.WorkspaceJob{
		ID:       id,
		Status:   service.JobStatusRunning,
		Progress: "cloning repository...",
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobCreateTimeout)
		defer cancel()

		result, err := createFn(ctx, req)
		if err != nil {
			errMsg := "workspace creation failed"
			var ce *workspaceerrors.CreateError
			if errors.As(err, &ce) {
				errMsg = ce.Message
			}
			s.jobs.Store(id, &service.WorkspaceJob{
				ID:          id,
				Status:      service.JobStatusFailed,
				Error:       errMsg,
				CompletedAt: time.Now(),
			})
			slog.Warn("async workspace creation failed",
				"job_id", id, "name", req.Name, "err", err)
			return
		}

		s.jobs.Store(id, &service.WorkspaceJob{
			ID:          id,
			Status:      service.JobStatusDone,
			WorkspaceID: result.WorkspaceID,
			CompletedAt: time.Now(),
		})
		slog.Info("async workspace creation completed",
			"job_id", id, "name", req.Name, "workspace_id", result.WorkspaceID)
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
