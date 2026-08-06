package workspacecoord

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// JobRegistry tracks process-local async workspace mutations.
type JobRegistry interface {
	Start(req WorkspaceCreateRequest, createFn WorkspaceCreateFn) string
	StartPrepared(id string, req WorkspaceCreateRequest, createFn WorkspaceCreateFn) string
	StartAddRepos(req WorkspaceAddReposRequest, addReposFn WorkspaceAddReposFn) string
	StartPreparedAddRepos(id string, req WorkspaceAddReposRequest, addReposFn WorkspaceAddReposFn) string
	Get(id string) *WorkspaceJob
}

// WorkspaceAdmissionCoordinator durably records an asynchronous workspace
// mutation before request handling schedules its process-local runner. Job IDs
// are opaque durable admission handles; callers must pass them through without
// deriving a replacement.
type WorkspaceAdmissionCoordinator interface {
	PrepareCreate(ctx context.Context, req WorkspaceCreateRequest) (jobID string, err error)
	PrepareAddRepos(ctx context.Context, req WorkspaceAddReposRequest) (jobID string, err error)
	LookupJob(ctx context.Context, jobID string) (job *WorkspaceJob, found bool, err error)
}

// WorkspaceService is the remaining machine-local workspace coordinator.
// Durable catalog lifecycle is owned by modules/workspace.
type WorkspaceService interface {
	// GetActiveWorkspace returns the active workspace topology.
	// Returns empty ops.WorkspaceData (non-nil, empty slices) if config unavailable.
	GetActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error)

	// GetWorkspace returns full workspace data for a specific workspace ID.
	// Returns ServiceError{Kind: NotFound} if workspace does not exist.
	GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error)

	// CreateWorkspace creates a new workspace synchronously.
	// Returns refreshed workspace data and any non-fatal warnings.
	CreateWorkspace(ctx context.Context, req WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error)

	// AddWorkspaceRepos attaches one or more local git repos to an existing workspace.
	AddWorkspaceRepos(ctx context.Context, req WorkspaceAddReposRequest) (*ops.WorkspaceData, error)

	// StartAsyncAddRepos starts an async repo-attachment job when cloning a
	// remote repository. Returns the job ID after validating the request.
	// Returns ServiceError{Kind: Unavailable} if job storage is unavailable.
	StartAsyncAddRepos(ctx context.Context, req WorkspaceAddReposRequest) (string, error)

	// StartAsyncCreate starts an async workspace creation job for clone workspaces.
	// Returns the job ID. Validates the request before starting.
	// Returns ServiceError{Kind: Unavailable} if job store not available.
	StartAsyncCreate(ctx context.Context, req WorkspaceCreateRequest) (string, error)

	// GetWorkspaceJob returns the status of an async workspace creation job.
	// Returns ServiceError{Kind: NotFound} if job not found or expired.
	GetWorkspaceJob(ctx context.Context, jobID string) (*WorkspaceJob, error)

	// DeleteWorkspace deletes a workspace by UUID.
	// Returns refreshed workspace data.
	DeleteWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error)

	// GetWorkspaceBackend returns a workspace's AI backend config setting.
	GetWorkspaceBackend(ctx context.Context, wsID string) (*BackendConfigData, error)

	// PatchWorkspaceBackend updates a workspace's AI backend config setting.
	// Caller must pre-validate the backend name (isValidBackend).
	// Returns refreshed workspace data.
	PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error)
}
