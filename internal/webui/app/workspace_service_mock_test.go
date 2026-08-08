package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// mockWorkspaceService is a test double for workspacecoord.WorkspaceService.
type mockWorkspaceService struct {
	getActiveWorkspaceFn    func(ctx context.Context) (*ops.WorkspaceData, error)
	getWorkspaceFn          func(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	createWorkspaceFn       func(ctx context.Context, req workspacecoord.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error)
	addWorkspaceReposFn     func(ctx context.Context, req workspacecoord.WorkspaceAddReposRequest) (*ops.WorkspaceData, error)
	startAsyncAddReposFn    func(ctx context.Context, req workspacecoord.WorkspaceAddReposRequest) (string, error)
	startAsyncCreateFn      func(ctx context.Context, req workspacecoord.WorkspaceCreateRequest) (string, error)
	getWorkspaceJobFn       func(ctx context.Context, jobID string) (*workspacecoord.WorkspaceJob, error)
	deleteWorkspaceFn       func(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	getWorkspaceBackendFn   func(ctx context.Context, wsID string) (*workspacecoord.BackendConfigData, error)
	patchWorkspaceBackendFn func(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error)
}

func (m *mockWorkspaceService) GetActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if m.getActiveWorkspaceFn != nil {
		return m.getActiveWorkspaceFn(ctx)
	}
	return &ops.WorkspaceData{}, nil
}

func (m *mockWorkspaceService) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if m.getWorkspaceFn != nil {
		return m.getWorkspaceFn(ctx, wsID)
	}
	return nil, apperrors.ErrNotFound("not found")
}

func (m *mockWorkspaceService) CreateWorkspace(ctx context.Context, req workspacecoord.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	if m.createWorkspaceFn != nil {
		return m.createWorkspaceFn(ctx, req)
	}
	return nil, nil, apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) AddWorkspaceRepos(ctx context.Context, req workspacecoord.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
	if m.addWorkspaceReposFn != nil {
		return m.addWorkspaceReposFn(ctx, req)
	}
	return nil, apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) StartAsyncAddRepos(ctx context.Context, req workspacecoord.WorkspaceAddReposRequest) (string, error) {
	if m.startAsyncAddReposFn != nil {
		return m.startAsyncAddReposFn(ctx, req)
	}
	return "", apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) StartAsyncCreate(ctx context.Context, req workspacecoord.WorkspaceCreateRequest) (string, error) {
	if m.startAsyncCreateFn != nil {
		return m.startAsyncCreateFn(ctx, req)
	}
	return "", apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) GetWorkspaceJob(ctx context.Context, jobID string) (*workspacecoord.WorkspaceJob, error) {
	if m.getWorkspaceJobFn != nil {
		return m.getWorkspaceJobFn(ctx, jobID)
	}
	return nil, apperrors.ErrNotFound("not found")
}

func (m *mockWorkspaceService) DeleteWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if m.deleteWorkspaceFn != nil {
		return m.deleteWorkspaceFn(ctx, wsID)
	}
	return nil, apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) GetWorkspaceBackend(ctx context.Context, wsID string) (*workspacecoord.BackendConfigData, error) {
	if m.getWorkspaceBackendFn != nil {
		return m.getWorkspaceBackendFn(ctx, wsID)
	}
	return nil, apperrors.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error) {
	if m.patchWorkspaceBackendFn != nil {
		return m.patchWorkspaceBackendFn(ctx, wsID, backend)
	}
	return nil, apperrors.ErrUnavailable("not available")
}
