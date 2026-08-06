package handlermux

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// mockWorkspaceService is a minimal test double for workspacecoord.WorkspaceService.
type mockWorkspaceService struct{}

func (m *mockWorkspaceService) GetActiveWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	return &ops.WorkspaceData{}, nil
}
func (m *mockWorkspaceService) GetWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrNotFound("not found")
}
func (m *mockWorkspaceService) CreateWorkspace(_ context.Context, _ workspacecoord.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	return nil, nil, apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) AddWorkspaceRepos(_ context.Context, _ workspacecoord.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) StartAsyncAddRepos(_ context.Context, _ workspacecoord.WorkspaceAddReposRequest) (string, error) {
	return "", apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) StartAsyncCreate(_ context.Context, _ workspacecoord.WorkspaceCreateRequest) (string, error) {
	return "", apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) GetWorkspaceJob(_ context.Context, _ string) (*workspacecoord.WorkspaceJob, error) {
	return nil, apperrors.ErrNotFound("not found")
}
func (m *mockWorkspaceService) DeleteWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) GetWorkspaceBackend(_ context.Context, _ string) (*workspacecoord.BackendConfigData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) PatchWorkspaceBackend(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
