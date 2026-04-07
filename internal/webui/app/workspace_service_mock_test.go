package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// mockWorkspaceService is a test double for service.WorkspaceService.
type mockWorkspaceService struct {
	getActiveWorkspaceFn    func(ctx context.Context) (*ops.WorkspaceData, error)
	listWorkspacesFn        func(ctx context.Context) ([]service.WorkspaceListItem, error)
	getWorkspaceFn          func(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	createWorkspaceFn       func(ctx context.Context, req service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error)
	startAsyncCreateFn      func(ctx context.Context, req service.WorkspaceCreateRequest) (string, error)
	getWorkspaceJobFn       func(ctx context.Context, jobID string) (*service.WorkspaceJob, error)
	deleteWorkspaceFn       func(ctx context.Context, wsID string) (*ops.WorkspaceData, error)
	renameWorkspaceFn       func(ctx context.Context, wsID string, newName string) (*ops.WorkspaceData, error)
	reorderWorkspacesFn     func(ctx context.Context, order []string) (*ops.WorkspaceData, error)
	setDefaultWorkspaceFn   func(ctx context.Context, name string) (*ops.WorkspaceData, error)
	clearDefaultWorkspaceFn func(ctx context.Context) (*ops.WorkspaceData, error)
	patchWorkspaceBackendFn func(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error)
}

func (m *mockWorkspaceService) GetActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if m.getActiveWorkspaceFn != nil {
		return m.getActiveWorkspaceFn(ctx)
	}
	return &ops.WorkspaceData{}, nil
}

func (m *mockWorkspaceService) ListWorkspaces(ctx context.Context) ([]service.WorkspaceListItem, error) {
	if m.listWorkspacesFn != nil {
		return m.listWorkspacesFn(ctx)
	}
	return nil, nil
}

func (m *mockWorkspaceService) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if m.getWorkspaceFn != nil {
		return m.getWorkspaceFn(ctx, wsID)
	}
	return nil, service.ErrNotFound("not found")
}

func (m *mockWorkspaceService) CreateWorkspace(ctx context.Context, req service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	if m.createWorkspaceFn != nil {
		return m.createWorkspaceFn(ctx, req)
	}
	return nil, nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) StartAsyncCreate(ctx context.Context, req service.WorkspaceCreateRequest) (string, error) {
	if m.startAsyncCreateFn != nil {
		return m.startAsyncCreateFn(ctx, req)
	}
	return "", service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) GetWorkspaceJob(ctx context.Context, jobID string) (*service.WorkspaceJob, error) {
	if m.getWorkspaceJobFn != nil {
		return m.getWorkspaceJobFn(ctx, jobID)
	}
	return nil, service.ErrNotFound("not found")
}

func (m *mockWorkspaceService) DeleteWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if m.deleteWorkspaceFn != nil {
		return m.deleteWorkspaceFn(ctx, wsID)
	}
	return nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) RenameWorkspace(ctx context.Context, wsID string, newName string) (*ops.WorkspaceData, error) {
	if m.renameWorkspaceFn != nil {
		return m.renameWorkspaceFn(ctx, wsID, newName)
	}
	return nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) ReorderWorkspaces(ctx context.Context, order []string) (*ops.WorkspaceData, error) {
	if m.reorderWorkspacesFn != nil {
		return m.reorderWorkspacesFn(ctx, order)
	}
	return nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) SetDefaultWorkspace(ctx context.Context, name string) (*ops.WorkspaceData, error) {
	if m.setDefaultWorkspaceFn != nil {
		return m.setDefaultWorkspaceFn(ctx, name)
	}
	return nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) ClearDefaultWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if m.clearDefaultWorkspaceFn != nil {
		return m.clearDefaultWorkspaceFn(ctx)
	}
	return nil, service.ErrUnavailable("not available")
}

func (m *mockWorkspaceService) PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error) {
	if m.patchWorkspaceBackendFn != nil {
		return m.patchWorkspaceBackendFn(ctx, wsID, backend)
	}
	return nil, service.ErrUnavailable("not available")
}
