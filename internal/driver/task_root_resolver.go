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
}

type TaskRootResolver interface {
	ResolveTaskRoot(ctx context.Context, req TaskExecRequest) (TaskRoot, error)
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
	local, err := loadWorkspaceLocalState(workspaceKey)
	if err != nil {
		return TaskRoot{}, err
	}
	if strings.TrimSpace(local.Path) == "" {
		return TaskRoot{}, fmt.Errorf("workspace %q has no local path in loom state", workspaceKey)
	}

	names := append([]string(nil), req.RepositorySet...)
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
		baseSHA, err := resolveRepositoryBase(ctx, repoPath, repository)
		if err != nil {
			return TaskRoot{}, fmt.Errorf("resolve TaskRun repository %q base: %w", name, err)
		}
		specs = append(specs, taskroot.RepositorySpec{
			Name:       name,
			SourcePath: repoPath,
			BranchName: stableTaskBranch(req.TaskID, name),
			BaseSHA:    baseSHA,
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
		return TaskRoot{}, err
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
		})
	}
	return resolved, nil
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
