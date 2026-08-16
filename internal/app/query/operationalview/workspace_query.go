package operationalview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

// WorkspaceRecords is the read-only Workspace owner seam used by the
// cross-owner topology projection.
type WorkspaceRecords interface {
	Get(context.Context, string) (*workspaceowner.Workspace, error)
	GetByName(context.Context, string) (*workspaceowner.Workspace, error)
	List(context.Context) ([]*workspaceowner.Workspace, error)
}

// RepositoryRecords is the read-only Workspace repository seam used by the
// topology projection.
type RepositoryRecords interface {
	List(context.Context, string) ([]*workspaceowner.Repository, error)
}

// WorkspacePlacement supplies machine-local paths and backend selection. It
// cannot mutate owner state or mint product authority.
type WorkspacePlacement interface {
	WorkspacePath(string) string
	RepositoryPath(string, string) string
	Backend(string) string
}

// ActiveWorkspace resolves the process-local active Workspace selection. An
// empty key means that the projection should select the first catalog entry.
type ActiveWorkspace func(context.Context) (string, error)

// WorkspaceTopologyQuery is the immutable query consumed by delivery and
// machine-local Source Control mechanics.
type WorkspaceTopologyQuery interface {
	Active(context.Context) (*Workspace, error)
	ByKey(context.Context, string) (*Workspace, error)
	ResolveKey(context.Context, string) (string, error)
	Paths(context.Context) (map[string]string, error)
}

// NewWorkspaceTopologyQuery composes Workspace and repository records with
// machine-local placement. The returned query exposes no persistence ports.
func NewWorkspaceTopologyQuery(
	workspaces WorkspaceRecords,
	repositories RepositoryRecords,
	placement WorkspacePlacement,
	active ActiveWorkspace,
) WorkspaceTopologyQuery {
	return workspaceTopologyQuery{
		workspaces: workspaces, repositories: repositories,
		placement: placement, active: active,
	}
}

type workspaceTopologyQuery struct {
	workspaces   WorkspaceRecords
	repositories RepositoryRecords
	placement    WorkspacePlacement
	active       ActiveWorkspace
}

func (query workspaceTopologyQuery) Active(ctx context.Context) (*Workspace, error) {
	if query.workspaces == nil {
		return nil, nil
	}
	key := ""
	if query.active != nil {
		var err error
		key, err = query.active(ctx)
		if err != nil {
			return nil, err
		}
	}
	if key == "" {
		var err error
		key, err = query.firstWorkspaceKey(ctx)
		if err != nil || key == "" {
			return nil, err
		}
	}
	return query.ByKey(ctx, key)
}

func (query workspaceTopologyQuery) ByKey(ctx context.Context, key string) (*Workspace, error) {
	if query.workspaces == nil || query.repositories == nil {
		return nil, errors.New("workspace topology query is unavailable")
	}
	workspace, err := query.workspaces.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	repositories, groups, err := query.repositoryViews(ctx, workspace.Key)
	if err != nil {
		return nil, err
	}
	summaries, err := query.summaries(ctx, workspace.Key)
	if err != nil {
		return nil, err
	}
	return &Workspace{
		ID: workspace.Key, Name: workspace.Name, Path: query.workspacePath(workspace.Key),
		Repos: repositories, Groups: groups, Agents: []Agent{}, Workspaces: summaries,
		DesignFormat: workspace.DesignFormat,
	}, nil
}

func (query workspaceTopologyQuery) ResolveKey(ctx context.Context, identity string) (string, error) {
	if query.workspaces == nil {
		return "", errors.New("workspace topology query is unavailable")
	}
	if workspace, err := query.workspaces.Get(ctx, identity); err == nil && workspace != nil {
		return workspace.Key, nil
	}
	workspace, err := query.workspaces.GetByName(ctx, identity)
	if err != nil {
		return "", err
	}
	return workspace.Key, nil
}

func (query workspaceTopologyQuery) Paths(ctx context.Context) (map[string]string, error) {
	if query.workspaces == nil {
		return nil, nil
	}
	workspaces, err := query.workspaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace topology: list workspaces: %w", err)
	}
	if len(workspaces) == 0 {
		return nil, nil
	}
	paths := make(map[string]string, len(workspaces))
	for _, workspace := range workspaces {
		paths[workspace.Key] = query.workspacePath(workspace.Key)
	}
	return paths, nil
}

func (query workspaceTopologyQuery) repositoryViews(ctx context.Context, workspaceKey string) ([]Repository, []string, error) {
	records, err := query.repositories.List(ctx, workspaceKey)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace topology: list repositories: %w", err)
	}
	groupSet := make(map[string]struct{})
	views := make([]Repository, 0, len(records))
	workspacePath := query.workspacePath(workspaceKey)
	for _, record := range records {
		defaultBranch := record.DefaultBranch
		if defaultBranch == "" {
			defaultBranch = "main"
		}
		remote := record.Remote
		if remote == "" {
			remote = "origin"
		}
		repositoryPath := query.repositoryPath(workspaceKey, record.Name)
		if repositoryPath == "" && workspacePath != "" {
			repositoryPath = filepath.Join(workspacePath, record.Name)
		}
		groups := append([]string(nil), record.Groups...)
		views = append(views, Repository{
			Name: record.Name, Path: repositoryPath, DefaultBranch: defaultBranch,
			Remote: remote, RemoteURL: record.RemoteURL, SourceRepoID: record.SourceRepoID,
			Groups: groups,
		})
		for _, group := range groups {
			groupSet[group] = struct{}{}
		}
	}
	groups := make([]string, 0, len(groupSet))
	for group := range groupSet {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return views, groups, nil
}

func (query workspaceTopologyQuery) summaries(ctx context.Context, activeKey string) ([]Summary, error) {
	workspaces, err := query.workspaces.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace topology: list workspaces: %w", err)
	}
	views := make([]Summary, 0, len(workspaces))
	for _, workspace := range workspaces {
		repositoryCount := 0
		if repositories, listErr := query.repositories.List(ctx, workspace.Key); listErr == nil {
			repositoryCount = len(repositories)
		}
		views = append(views, Summary{
			ID: workspace.Key, Name: workspace.Name, Path: query.workspacePath(workspace.Key),
			Active: workspace.Key == activeKey, RepoCount: repositoryCount,
			Backend: query.backend(workspace.Key), State: string(workspace.State),
			ErrorMessage: workspace.ErrorMessage,
		})
	}
	sort.Slice(views, func(left, right int) bool { return views[left].Name < views[right].Name })
	return views, nil
}

func (query workspaceTopologyQuery) firstWorkspaceKey(ctx context.Context) (string, error) {
	workspaces, err := query.workspaces.List(ctx)
	if err != nil {
		return "", fmt.Errorf("workspace topology: list workspaces: %w", err)
	}
	if len(workspaces) == 0 {
		return "", nil
	}
	sort.Slice(workspaces, func(left, right int) bool { return workspaces[left].Name < workspaces[right].Name })
	return workspaces[0].Key, nil
}

func (query workspaceTopologyQuery) workspacePath(key string) string {
	if query.placement == nil {
		return ""
	}
	return query.placement.WorkspacePath(key)
}

func (query workspaceTopologyQuery) repositoryPath(workspaceKey, repository string) string {
	if query.placement == nil {
		return ""
	}
	return query.placement.RepositoryPath(workspaceKey, repository)
}

func (query workspaceTopologyQuery) backend(key string) string {
	if query.placement == nil {
		return ""
	}
	return query.placement.Backend(key)
}
