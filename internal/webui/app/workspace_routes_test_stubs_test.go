package app

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
)

// workspaceRoutesMockWorkspaceService is a minimal test double for workspacecoord.WorkspaceService.
type workspaceRoutesMockWorkspaceService struct{}

func (m *workspaceRoutesMockWorkspaceService) GetActiveWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	return &ops.WorkspaceData{}, nil
}
func (m *workspaceRoutesMockWorkspaceService) GetWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrNotFound("not found")
}
func (m *workspaceRoutesMockWorkspaceService) CreateWorkspace(_ context.Context, _ workspacecoord.WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	return nil, nil, apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) AddWorkspaceRepos(_ context.Context, _ workspacecoord.WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) StartAsyncAddRepos(_ context.Context, _ workspacecoord.WorkspaceAddReposRequest) (string, error) {
	return "", apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) StartAsyncCreate(_ context.Context, _ workspacecoord.WorkspaceCreateRequest) (string, error) {
	return "", apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) GetWorkspaceJob(_ context.Context, _ string) (*workspacecoord.WorkspaceJob, error) {
	return nil, apperrors.ErrNotFound("not found")
}
func (m *workspaceRoutesMockWorkspaceService) DeleteWorkspace(_ context.Context, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) GetWorkspaceBackend(_ context.Context, _ string) (*workspacecoord.BackendConfigData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
func (m *workspaceRoutesMockWorkspaceService) PatchWorkspaceBackend(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, apperrors.ErrUnavailable("not available")
}
