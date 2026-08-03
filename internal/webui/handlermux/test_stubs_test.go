package handlermux

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// mockWorkspaceService is a minimal test double for service.WorkspaceService.
type mockWorkspaceService struct{}

func (m *mockWorkspaceService) GetActiveWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	return &ops.WorkspaceData{}, nil
}
func (m *mockWorkspaceService) ListWorkspaces(_ context.Context) ([]service.WorkspaceListItem, error) {
	return nil, nil
}
func (m *mockWorkspaceService) GetWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrNotFound("not found")
}
func (m *mockWorkspaceService) CreateWorkspace(_ context.Context, _ service.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	return nil, nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) AddWorkspaceRepos(_ context.Context, _ service.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) StartAsyncAddRepos(_ context.Context, _ service.WorkspaceAddReposRequest) (string, error) {
	return "", service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) StartAsyncCreate(_ context.Context, _ service.WorkspaceCreateRequest) (string, error) {
	return "", service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) GetWorkspaceJob(_ context.Context, _ string) (*service.WorkspaceJob, error) {
	return nil, service.ErrNotFound("not found")
}
func (m *mockWorkspaceService) DeleteWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) RenameWorkspace(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) ReorderWorkspaces(_ context.Context, _ []string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) SetDefaultWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) ClearDefaultWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) GetWorkspaceBackend(_ context.Context, _ string) (*service.BackendConfigData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) PatchWorkspaceBackend(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) PatchWorkspaceDesignFormat(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
