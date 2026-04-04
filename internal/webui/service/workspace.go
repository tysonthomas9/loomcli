package service

import (
	"context"
)

// WorkspaceListItem represents a workspace in the list response.
type WorkspaceListItem struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Path   string     `json:"path"`
	Active bool       `json:"active"`
	Pool   *PoolStats `json:"pool,omitempty"`
}

// JobStore is implemented by WorkspaceJobStore for async workspace creation.
type JobStore interface {
	Start(req WorkspaceCreateRequest, createFn WorkspaceCreateFn) string
	Get(id string) *WorkspaceJob
}

// WorkspaceService encapsulates all workspace CRUD, config management,
// validation, and lifecycle logic behind a clean service boundary.
type WorkspaceService interface {
	// GetActiveWorkspace returns the active workspace topology.
	// Returns empty WorkspaceData (non-nil, empty slices) if config unavailable.
	GetActiveWorkspace(ctx context.Context) (*WorkspaceData, error)

	// ListWorkspaces returns all registered workspaces with pool status.
	ListWorkspaces(ctx context.Context) ([]WorkspaceListItem, error)

	// GetWorkspace returns full workspace data for a specific workspace ID.
	// Returns ServiceError{Kind: NotFound} if workspace does not exist.
	GetWorkspace(ctx context.Context, wsID string) (*WorkspaceData, error)

	// CreateWorkspace creates a new workspace synchronously.
	// Returns refreshed workspace data and any non-fatal warnings.
	CreateWorkspace(ctx context.Context, req WorkspaceCreateRequest) (*WorkspaceData, []string, error)

	// StartAsyncCreate starts an async workspace creation job for clone workspaces.
	// Returns the job ID. Validates the request before starting.
	// Returns ServiceError{Kind: Unavailable} if job store not available.
	StartAsyncCreate(ctx context.Context, req WorkspaceCreateRequest) (string, error)

	// GetWorkspaceJob returns the status of an async workspace creation job.
	// Returns ServiceError{Kind: NotFound} if job not found or expired.
	GetWorkspaceJob(ctx context.Context, jobID string) (*WorkspaceJob, error)

	// DeleteWorkspace deletes a workspace by UUID.
	// Returns refreshed workspace data.
	DeleteWorkspace(ctx context.Context, wsID string) (*WorkspaceData, error)

	// RenameWorkspace renames a workspace identified by UUID.
	// Returns refreshed workspace data.
	RenameWorkspace(ctx context.Context, wsID string, newName string) (*WorkspaceData, error)

	// ReorderWorkspaces persists a custom workspace display order.
	// Returns refreshed workspace data.
	ReorderWorkspaces(ctx context.Context, order []string) (*WorkspaceData, error)

	// SetDefaultWorkspace sets the default workspace by name.
	// Returns refreshed workspace data.
	SetDefaultWorkspace(ctx context.Context, name string) (*WorkspaceData, error)

	// ClearDefaultWorkspace clears the default workspace setting.
	// Returns refreshed workspace data.
	ClearDefaultWorkspace(ctx context.Context) (*WorkspaceData, error)

	// PatchWorkspaceBackend updates a workspace's AI backend config setting.
	// Caller must pre-validate the backend name (isValidBackend).
	// Returns refreshed workspace data.
	PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*WorkspaceData, error)
}
