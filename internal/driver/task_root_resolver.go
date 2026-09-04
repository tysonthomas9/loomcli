package driver

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskroot"
)

var taskBranchSegmentPattern = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type TaskRoot struct {
	Path         string
	ManifestPath string
	Repositories []TaskRootRepository
}

type TaskRootRepository struct {
	Name       string
	Path       string
	BranchName string
	BaseSHA    string
	Detached   bool
}

type TaskRootResolver interface {
	ResolveTaskRoot(ctx context.Context, req TaskExecRequest) (TaskRoot, error)
}

type TaskRootReleaser interface {
	ReleaseTaskRoot(ctx context.Context, req TaskExecRequest, policy taskroot.RetentionPolicy) error
}

type LocalTaskRootResolver struct {
	Store store.Store
}

func (r LocalTaskRootResolver) ResolveTaskRoot(ctx context.Context, req TaskExecRequest) (TaskRoot, error) {
	workspaceKey, err := r.validateRequest(req)
	if err != nil {
		return TaskRoot{}, err
	}
	if err := r.recordRootState(ctx, req, domain.TaskRunRootProvisioning); err != nil {
		return TaskRoot{}, fmt.Errorf("record TaskRun Root provisioning: %w", err)
	}
	local, err := loadWorkspaceLocalState(workspaceKey)
	if err != nil {
		return TaskRoot{}, err
	}
	if strings.TrimSpace(local.Path) == "" {
		return TaskRoot{}, fmt.Errorf("workspace %q has no local path in loom state", workspaceKey)
	}

	specs, err := r.repositorySpecs(ctx, req, local)
	if err != nil {
		return TaskRoot{}, err
	}
	generation := req.RootGeneration
	if generation == 0 {
		generation = 1
	}
	manifest, err := taskroot.NewLocalGitManager(local.Path).Prepare(ctx, taskroot.RootSpec{
		TaskRunID:    req.TaskRunID,
		Generation:   generation,
		FencingToken: req.FencingToken,
		Repositories: specs,
	})
	if err != nil {
		_ = r.recordRootState(ctx, req, domain.TaskRunRootFailed)
		return TaskRoot{}, err
	}
	if err := r.recordRootState(ctx, req, domain.TaskRunRootReady); err != nil {
		return TaskRoot{}, fmt.Errorf("record TaskRun Root ready: %w", err)
	}
	return resolvedTaskRoot(manifest), nil
}

func (r LocalTaskRootResolver) validateRequest(req TaskExecRequest) (string, error) {
	if r.Store == nil {
		return "", fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" || strings.TrimSpace(req.TaskRunID) == "" || strings.TrimSpace(req.TaskID) == "" {
		return "", fmt.Errorf("workspace_key, task_run_id, and task_id are required: %w", domain.ErrInvalid)
	}
	if len(req.RepositorySet) == 0 {
		return "", fmt.Errorf("task run repository_set is required: %w", domain.ErrInvalid)
	}
	return workspaceKey, nil
}

func (r LocalTaskRootResolver) recordRootState(ctx context.Context, req TaskExecRequest, state domain.TaskRunRootState) error {
	lifecycle, ok := store.ResolveTaskRunExecutionContextStore(r.Store)
	if !ok {
		return nil
	}
	_, err := lifecycle.UpdateTaskRunExecutionContext(ctx, req.WorkspaceKey, req.TaskRunID, store.TaskRunExecutionContextUpdate{
		RootState: state, RootNodeID: req.NodeID, RootFencingToken: req.FencingToken, BackendKind: req.ProviderProfile,
	})
	return err
}

func (r LocalTaskRootResolver) repositorySpecs(ctx context.Context, req TaskExecRequest, local bootstrap.WorkspaceLocalState) ([]taskroot.RepositorySpec, error) {
	names, heads, err := r.repositoryNames(ctx, req)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	seen := make(map[string]struct{}, len(names))
	specs := make([]taskroot.RepositorySpec, 0, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("TaskRun repository_set contains duplicate %q", name)
		}
		seen[name] = struct{}{}
		spec, err := r.repositorySpec(ctx, req, local, name, heads)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (r LocalTaskRootResolver) repositoryNames(ctx context.Context, req TaskExecRequest) ([]string, map[string]domain.TaskChangeSetEntry, error) {
	names := append([]string(nil), req.RepositorySet...)
	heads := make(map[string]domain.TaskChangeSetEntry)
	if req.ExecutionClass != domain.TaskRunExecutionReview {
		return names, heads, nil
	}
	handoff, ok := store.ResolveTaskChangeHandoffStore(r.Store)
	if !ok {
		return nil, nil, fmt.Errorf("task change set store required for review: %w", domain.ErrInvalid)
	}
	changeSet, err := handoff.GetTaskChangeSet(ctx, req.WorkspaceKey, req.TaskID, req.ChangeSetVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("get Task Change Set version %d: %w", req.ChangeSetVersion, err)
	}
	names = make([]string, 0, len(changeSet.Entries))
	for _, entry := range changeSet.Entries {
		heads[entry.RepoName] = entry
		names = append(names, entry.RepoName)
	}
	return names, heads, nil
}

func (r LocalTaskRootResolver) repositorySpec(ctx context.Context, req TaskExecRequest, local bootstrap.WorkspaceLocalState, name string, heads map[string]domain.TaskChangeSetEntry) (taskroot.RepositorySpec, error) {
	repository, err := r.Store.Repos().Get(ctx, req.WorkspaceKey, name)
	if err != nil {
		return taskroot.RepositorySpec{}, fmt.Errorf("get exact TaskRun repository %q: %w", name, err)
	}
	repoPath, err := (LocalTaskWorktreeResolver{Store: r.Store}).ensureRepoCheckout(ctx, req.WorkspaceKey, local, repository)
	if err != nil {
		return taskroot.RepositorySpec{}, err
	}
	if req.ExecutionClass == domain.TaskRunExecutionReview {
		return reviewRepositorySpec(ctx, name, repoPath, heads)
	}
	baseSHA, err := resolveRepositoryBase(ctx, repoPath, repository)
	if err != nil {
		return taskroot.RepositorySpec{}, fmt.Errorf("resolve TaskRun repository %q base: %w", name, err)
	}
	return taskroot.RepositorySpec{Name: name, SourcePath: repoPath, BranchName: stableTaskBranch(req.TaskID, name), BaseSHA: baseSHA}, nil
}

func reviewRepositorySpec(ctx context.Context, name, repoPath string, heads map[string]domain.TaskChangeSetEntry) (taskroot.RepositorySpec, error) {
	entry, ok := heads[name]
	if !ok {
		return taskroot.RepositorySpec{}, fmt.Errorf("review repository %q is absent from Task Change Set", name)
	}
	if hasGitRemote(ctx, repoPath, entry.RemoteName) {
		if _, err := driverGit(ctx, repoPath, "fetch", entry.RemoteName, "refs/heads/"+entry.BranchName); err != nil {
			return taskroot.RepositorySpec{}, fmt.Errorf("fetch review repository %q: %w", name, err)
		}
	}
	if resolved, err := driverGit(ctx, repoPath, "rev-parse", entry.HeadSHA+"^{commit}"); err != nil || resolved != entry.HeadSHA {
		return taskroot.RepositorySpec{}, fmt.Errorf("review repository %q head %s is unavailable", name, entry.HeadSHA)
	}
	return taskroot.RepositorySpec{Name: name, SourcePath: repoPath, BaseSHA: entry.HeadSHA, Detached: true}, nil
}

func resolvedTaskRoot(manifest taskroot.RootManifest) TaskRoot {
	resolved := TaskRoot{
		Path:         manifest.RootPath,
		ManifestPath: filepath.Join(manifest.RootPath, "manifest.json"),
		Repositories: make([]TaskRootRepository, 0, len(manifest.Repositories)),
	}
	for _, repository := range manifest.Repositories {
		resolved.Repositories = append(resolved.Repositories, TaskRootRepository{
			Name:       repository.Name,
			Path:       repository.Path,
			BranchName: repository.BranchName,
			BaseSHA:    repository.BaseSHA,
			Detached:   repository.Detached,
		})
	}
	return resolved
}

func (r LocalTaskRootResolver) ReleaseTaskRoot(ctx context.Context, req TaskExecRequest, policy taskroot.RetentionPolicy) error {
	local, err := loadWorkspaceLocalState(req.WorkspaceKey)
	if err != nil {
		return err
	}
	return taskroot.NewLocalGitManager(local.Path).Release(ctx, taskroot.RootLease{
		TaskRunID: req.TaskRunID, Generation: req.RootGeneration, FencingToken: req.FencingToken,
	}, policy)
}

func stableTaskBranch(taskID, repositoryName string) string {
	task := strings.Trim(taskBranchSegmentPattern.ReplaceAllString(strings.TrimSpace(taskID), "-"), "-")
	repository := strings.Trim(taskBranchSegmentPattern.ReplaceAllString(strings.TrimSpace(repositoryName), "-"), "-")
	return "loom/task/" + task + "/" + repository
}

func resolveRepositoryBase(ctx context.Context, repoPath string, repository *domain.Repo) (string, error) {
	branch := repoDefaultBranch(repository)
	remote := repoRemote(repository)
	if hasGitRemote(ctx, repoPath, remote) {
		if _, err := driverGit(ctx, repoPath, "fetch", remote, "--", branch); err != nil {
			return "", err
		}
		return driverGit(ctx, repoPath, "rev-parse", "FETCH_HEAD^{commit}")
	}
	return driverGit(ctx, repoPath, "rev-parse", branch+"^{commit}")
}

func hasGitRemote(ctx context.Context, repoPath, remote string) bool {
	_, err := driverGit(ctx, repoPath, "remote", "get-url", remote)
	return err == nil
}

func driverGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed executable and argv-only invocation.
	command.Dir = repoPath
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
