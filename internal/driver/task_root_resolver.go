package driver

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	if r.Store == nil {
		return TaskRoot{}, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" || strings.TrimSpace(req.TaskRunID) == "" || strings.TrimSpace(req.TaskID) == "" {
		return TaskRoot{}, fmt.Errorf("workspace_key, task_run_id, and task_id are required: %w", domain.ErrInvalid)
	}
	if len(req.RepositorySet) == 0 {
		return TaskRoot{}, fmt.Errorf("TaskRun repository_set is required: %w", domain.ErrInvalid)
	}
	if lifecycle, ok := r.Store.(store.TaskRunExecutionContextStore); ok {
		if _, err := lifecycle.UpdateTaskRunExecutionContext(ctx, workspaceKey, req.TaskRunID, store.TaskRunExecutionContextUpdate{
			RootState: domain.TaskRunRootProvisioning, RootNodeID: req.NodeID, RootFencingToken: req.FencingToken, BackendKind: req.ProviderProfile,
		}); err != nil {
			return TaskRoot{}, fmt.Errorf("record TaskRun Root provisioning: %w", err)
		}
	}
	local, err := loadWorkspaceLocalState(workspaceKey)
	if err != nil {
		return TaskRoot{}, err
	}
	if strings.TrimSpace(local.Path) == "" {
		return TaskRoot{}, fmt.Errorf("workspace %q has no local path in loom state", workspaceKey)
	}

	names := append([]string(nil), req.RepositorySet...)
	changeSetHeads := map[string]domain.TaskChangeSetEntry{}
	if req.ExecutionClass == domain.TaskRunExecutionReview {
		handoff, ok := r.Store.(store.TaskChangeHandoffStore)
		if !ok {
			return TaskRoot{}, fmt.Errorf("Task Change Set store required for review: %w", domain.ErrInvalid)
		}
		changeSet, err := handoff.GetTaskChangeSet(ctx, workspaceKey, req.TaskID, req.ChangeSetVersion)
		if err != nil {
			return TaskRoot{}, fmt.Errorf("get Task Change Set version %d: %w", req.ChangeSetVersion, err)
		}
		names = names[:0]
		for _, entry := range changeSet.Entries {
			changeSetHeads[entry.RepoName] = entry
			names = append(names, entry.RepoName)
		}
	}
	sort.Strings(names)
	seen := make(map[string]struct{}, len(names))
	specs := make([]taskroot.RepositorySpec, 0, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			return TaskRoot{}, fmt.Errorf("TaskRun repository_set contains duplicate %q", name)
		}
		seen[name] = struct{}{}
		repository, err := r.Store.Repos().Get(ctx, workspaceKey, name)
		if err != nil {
			return TaskRoot{}, fmt.Errorf("get exact TaskRun repository %q: %w", name, err)
		}
		repoPath, err := (LocalTaskWorktreeResolver{Store: r.Store}).ensureRepoCheckout(ctx, workspaceKey, local, repository)
		if err != nil {
			return TaskRoot{}, err
		}
		baseSHA := ""
		detached := req.ExecutionClass == domain.TaskRunExecutionReview
		branchName := stableTaskBranch(req.TaskID, name)
		if detached {
			entry, ok := changeSetHeads[name]
			if !ok {
				return TaskRoot{}, fmt.Errorf("review repository %q is absent from Task Change Set", name)
			}
			if hasGitRemote(ctx, repoPath, entry.RemoteName) {
				if _, err := driverGit(ctx, repoPath, "fetch", entry.RemoteName, "refs/heads/"+entry.BranchName); err != nil {
					return TaskRoot{}, fmt.Errorf("fetch review repository %q: %w", name, err)
				}
			}
			baseSHA = entry.HeadSHA
			branchName = ""
			if resolved, err := driverGit(ctx, repoPath, "rev-parse", baseSHA+"^{commit}"); err != nil || resolved != baseSHA {
				return TaskRoot{}, fmt.Errorf("review repository %q head %s is unavailable", name, baseSHA)
			}
		} else {
			var err error
			baseSHA, err = resolveRepositoryBase(ctx, repoPath, repository)
			if err != nil {
				return TaskRoot{}, fmt.Errorf("resolve TaskRun repository %q base: %w", name, err)
			}
		}
		specs = append(specs, taskroot.RepositorySpec{
			Name:       name,
			SourcePath: repoPath,
			BranchName: branchName,
			BaseSHA:    baseSHA,
			Detached:   detached,
		})
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
		if lifecycle, ok := r.Store.(store.TaskRunExecutionContextStore); ok {
			_, _ = lifecycle.UpdateTaskRunExecutionContext(ctx, workspaceKey, req.TaskRunID, store.TaskRunExecutionContextUpdate{
				RootState: domain.TaskRunRootFailed, RootNodeID: req.NodeID, RootFencingToken: req.FencingToken, BackendKind: req.ProviderProfile,
			})
		}
		return TaskRoot{}, err
	}
	if lifecycle, ok := r.Store.(store.TaskRunExecutionContextStore); ok {
		if _, err := lifecycle.UpdateTaskRunExecutionContext(ctx, workspaceKey, req.TaskRunID, store.TaskRunExecutionContextUpdate{
			RootState: domain.TaskRunRootReady, RootNodeID: req.NodeID, RootFencingToken: req.FencingToken, BackendKind: req.ProviderProfile,
		}); err != nil {
			return TaskRoot{}, fmt.Errorf("record TaskRun Root ready: %w", err)
		}
	}
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
	return resolved, nil
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
