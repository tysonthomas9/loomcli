package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const ErrorClassLocalWorktreeUnprovisioned = "local_worktree_unprovisioned"

type TaskWorktree struct {
	Path         string
	RepoName     string
	SourceRepoID string
}

type TaskWorktreeResolver interface {
	ResolveTaskWorktree(ctx context.Context, req TaskExecRequest, fallbackPath string) (TaskWorktree, error)
}

type LocalTaskWorktreeResolver struct {
	Store store.Store
}

func (r LocalTaskWorktreeResolver) ResolveTaskWorktree(ctx context.Context, req TaskExecRequest, _ string) (TaskWorktree, error) {
	if r.Store == nil {
		return TaskWorktree{}, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" {
		return TaskWorktree{}, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	taskRunID := strings.TrimSpace(req.TaskRunID)
	if taskRunID == "" {
		return TaskWorktree{}, fmt.Errorf("task run id required: %w", domain.ErrInvalid)
	}
	local, err := loadWorkspaceLocalState(workspaceKey)
	if err != nil {
		return TaskWorktree{}, err
	}
	if strings.TrimSpace(local.Path) == "" {
		return TaskWorktree{}, fmt.Errorf("workspace %q has no local path in loom state", workspaceKey)
	}

	repos, err := r.Store.Repos().List(ctx, workspaceKey)
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("list workspace repos: %w", err)
	}
	if len(repos) == 0 {
		return TaskWorktree{}, fmt.Errorf("workspace %q has no repos", workspaceKey)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

	selected, err := r.selectRepo(ctx, workspaceKey, repos, req)
	if err != nil {
		return TaskWorktree{}, err
	}
	repoPath, err := r.ensureRepoCheckout(ctx, workspaceKey, local, selected)
	if err != nil {
		return TaskWorktree{}, err
	}
	target, err := localworkspace.TaskRunWorktreePath(local.Path, selected.Name, taskRunID)
	if err != nil {
		return TaskWorktree{}, err
	}
	if err := localworkspace.EnsureDetachedGitWorktreeFromBranch(repoPath, target, repoRemote(selected), repoDefaultBranch(selected)); err != nil {
		return TaskWorktree{}, fmt.Errorf("ensure task run worktree for repo %q: %w", selected.Name, err)
	}
	return TaskWorktree{
		Path:         target,
		RepoName:     selected.Name,
		SourceRepoID: firstNonEmpty(selected.SourceRepoID, selected.Name),
	}, nil
}

func loadWorkspaceLocalState(workspaceKey string) (bootstrap.WorkspaceLocalState, error) {
	sc, err := bootstrap.LoadStateCache()
	if err != nil {
		return bootstrap.WorkspaceLocalState{}, fmt.Errorf("load local workspace state: %w", err)
	}
	if sc == nil || sc.Workspaces == nil {
		return bootstrap.WorkspaceLocalState{}, fmt.Errorf("workspace %q has no local state", workspaceKey)
	}
	local, ok := sc.Workspaces[workspaceKey]
	if !ok {
		return bootstrap.WorkspaceLocalState{}, fmt.Errorf("workspace %q has no local state", workspaceKey)
	}
	return local, nil
}

func (r LocalTaskWorktreeResolver) selectRepo(ctx context.Context, workspaceKey string, repos []*domain.Repo, req TaskExecRequest) (*domain.Repo, error) {
	selectors := taskWorktreeRepoSelectors(req)
	if req.WorkerProfileID != "" {
		if profile, err := r.Store.WorkerProfiles().Get(ctx, workspaceKey, req.WorkerProfileID); err == nil && profile != nil {
			selectors = append(selectors, profile.Repos...)
		}
	}
	for _, selector := range selectors {
		if repo := findRepoBySelector(repos, selector); repo != nil {
			return repo, nil
		}
	}
	if len(repos) == 1 {
		return repos[0], nil
	}
	if len(selectors) > 0 {
		return nil, fmt.Errorf("no workspace repo matches task repo selector %q", strings.Join(selectors, ", "))
	}
	return repos[0], nil
}

func taskWorktreeRepoSelectors(req TaskExecRequest) []string {
	var out []string
	out = append(out, req.RunnerPlacement.RepoRef, req.SandboxPlacement.RepoRef)
	out = append(out, taskInputRepoSelectors(req.Input)...)
	return normalizeStringList(out)
}

func taskInputRepoSelectors(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var out []string
	collectRepoSelectors(value, 0, &out)
	return out
}

func collectRepoSelectors(value any, depth int, out *[]string) {
	if depth > 3 {
		return
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, value := range obj {
		if repoSelectorKey(key) {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				*out = append(*out, s)
			}
			continue
		}
		collectRepoSelectors(value, depth+1, out)
	}
}

func repoSelectorKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "sourcerepo", "reporef", "reponame", "repo", "repository", "githubrepo":
		return true
	default:
		return false
	}
}

func findRepoBySelector(repos []*domain.Repo, selector string) *domain.Repo {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	want := normalizedRepoToken(selector)
	wantBase := normalizedRepoToken(repoBasename(selector))
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		candidates := []string{
			repo.Name,
			firstNonEmpty(repo.SourceRepoID, repo.Name),
			repo.RemoteURL,
			repoBasename(repo.RemoteURL),
		}
		for _, candidate := range candidates {
			got := normalizedRepoToken(candidate)
			if got != "" && (got == want || got == wantBase) {
				return repo
			}
		}
	}
	return nil
}

func normalizedRepoToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.Trim(value, "/")
	return value
}

func repoBasename(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, "/:"); idx >= 0 && idx+1 < len(value) {
		return value[idx+1:]
	}
	return value
}

func (r LocalTaskWorktreeResolver) ensureRepoCheckout(ctx context.Context, workspaceKey string, local bootstrap.WorkspaceLocalState, repo *domain.Repo) (string, error) {
	if repo == nil {
		return "", fmt.Errorf("repo required: %w", domain.ErrInvalid)
	}
	repoPath := strings.TrimSpace(localworkspace.RepoPath(local, repo.Name))
	if repoPath == "" {
		var err error
		repoPath, err = localworkspace.RepoCheckoutPath(local.Path, repo.Name)
		if err != nil {
			return "", err
		}
	}
	if isGitCheckout(repoPath) {
		return repoPath, nil
	}
	if _, err := os.Stat(repoPath); err == nil {
		return "", fmt.Errorf("repo %q path %s is not a git checkout", repo.Name, repoPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat repo %q path %s: %w", repo.Name, repoPath, err)
	}
	if strings.TrimSpace(repo.RemoteURL) == "" {
		return "", fmt.Errorf("repo %q has no local checkout at %s and no remote URL to clone", repo.Name, repoPath)
	}
	if err := localworkspace.CloneRepoTo(ctx, repo.RemoteURL, repoPath); err != nil {
		return "", err
	}
	if err := localworkspace.RememberRepoPath(workspaceKey, repo.Name, repoPath); err != nil {
		return "", fmt.Errorf("remember repo path: %w", err)
	}
	return repoPath, nil
}

func isGitCheckout(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	return false
}

func repoRemote(repo *domain.Repo) string {
	if repo == nil || strings.TrimSpace(repo.Remote) == "" {
		return "origin"
	}
	return strings.TrimSpace(repo.Remote)
}

func repoDefaultBranch(repo *domain.Repo) string {
	if repo == nil {
		return ""
	}
	return strings.TrimSpace(repo.DefaultBranch)
}
