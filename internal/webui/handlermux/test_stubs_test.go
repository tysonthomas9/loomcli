package handlermux

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
func (m *mockWorkspaceService) RemoveWorkspaceRepo(_ context.Context, _ service.WorkspaceRemoveRepoRequest) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
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
func (m *mockWorkspaceService) GetWorkspaceBackend(_ context.Context, _ string) (*service.BackendConfigData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) PatchWorkspaceBackend(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}
func (m *mockWorkspaceService) PatchWorkspaceDesignFormat(_ context.Context, _ string, _ string) (*ops.WorkspaceData, error) {
	return nil, service.ErrUnavailable("not available")
}

// stubErrorPool implements daemon.Pool, returning errors from Get.
type stubErrorPool struct{}

func (s *stubErrorPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, context.DeadlineExceeded
}
func (s *stubErrorPool) Put(_ *rpc.Client)           {}
func (s *stubErrorPool) PutAfterError(_ *rpc.Client) {}
func (s *stubErrorPool) Discard(_ *rpc.Client)       {}
func (s *stubErrorPool) Stats() daemon.PoolStats     { return daemon.PoolStats{} }
func (s *stubErrorPool) Close() error                { return nil }
