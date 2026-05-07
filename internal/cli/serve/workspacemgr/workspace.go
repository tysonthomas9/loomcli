package workspacemgr

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

type workspaceDirPlan struct {
	path                 string
	removeRootOnRollback bool
}

// resolveWorkspaceDirForCreate resolves and validates the workspace directory
// path for a new workspace. Explicit desktop/UI paths may live outside Loom's
// app data directory, but they must point at a safe workspace root: an empty
// directory, or a not-yet-created leaf under an existing parent.
func resolveWorkspaceDirForCreate(reqPath, wsName string) (workspaceDirPlan, error) {
	wsDir := strings.TrimSpace(reqPath)
	if wsDir == "" {
		wsDir = config.GetWorkspaceDir(wsName)
	}
	wsDir = expandUserPath(wsDir)

	absDir, err := filepath.Abs(filepath.Clean(wsDir))
	if err != nil {
		return workspaceDirPlan{}, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot resolve workspace path %q", wsDir), err)
	}
	if err := validateWorkspaceCreatePath(absDir); err != nil {
		return workspaceDirPlan{}, err
	}

	_, statErr := os.Stat(absDir)
	return workspaceDirPlan{path: absDir, removeRootOnRollback: os.IsNotExist(statErr)}, nil
}

func expandUserPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func validateWorkspaceCreatePath(wsDir string) error {
	if err := validateWorkspacePath(wsDir); err != nil {
		return err
	}

	if info, err := os.Stat(wsDir); err == nil {
		if !info.IsDir() {
			return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("workspace path is not a directory: %s", wsDir), nil)
		}
		if _, err := os.Stat(filepath.Join(wsDir, ".git")); err == nil {
			return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must not be an existing git repository: %s", wsDir), nil)
		}
		empty, err := dirIsEmpty(wsDir)
		if err != nil {
			return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot inspect workspace path: %s", wsDir), err)
		}
		if !empty {
			return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be empty or not exist: %s", wsDir), nil)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot inspect workspace path: %s", wsDir), err)
	}

	parent := filepath.Dir(wsDir)
	if isPathWithin(wsDir, defaultWorkspaceBase()) {
		return nil
	}
	info, err := os.Stat(parent)
	if err != nil {
		return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("workspace parent directory does not exist: %s", parent), err)
	}
	if !info.IsDir() {
		return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("workspace parent path is not a directory: %s", parent), nil)
	}
	return nil
}

func dirIsEmpty(path string) (bool, error) {
	f, err := os.Open(path) //nolint:gosec // path is validated as a user-selected workspace directory.
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

type resolvedRepo struct {
	path string
	name string
}

// resolveRepoPaths validates and resolves a list of repo paths, checking that
// each exists, is a directory, contains a .git directory, and has a unique name.
func resolveRepoPaths(repoPaths []string) ([]resolvedRepo, error) {
	var resolved []resolvedRepo
	seenNames := make(map[string]string)

	for _, rp := range repoPaths {
		rp = strings.TrimSpace(rp)
		if rp == "" {
			continue
		}

		absPath, err := filepath.Abs(rp)
		if err != nil {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot resolve path %q", rp), err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("repo path does not exist: %s", absPath), err)
		}
		if !info.IsDir() {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("repo path is not a directory: %s", absPath), nil)
		}

		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			return nil, workspaceerrors.New(workspaceerrors.NotGitRepo, fmt.Sprintf("not a git repository: %s", absPath), err)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			return nil, workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("duplicate repo name %q from paths %s and %s", baseName, prev, absPath), nil)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolved) == 0 {
		return nil, workspaceerrors.New(workspaceerrors.PathNotFound, "no valid repos specified", nil)
	}
	return resolved, nil
}

// createdWorktree tracks a worktree that was created during workspace setup for cleanup.
type createdWorktree struct {
	origRepoPath string
	worktreePath string
	branch       string
}

// addWorktrees creates git worktrees for each resolved repo in the workspace directory.
func addWorktrees(ctx context.Context, resolved []resolvedRepo, wsDir, branch string) ([]createdWorktree, []config.RepoConfig, error) {
	var created []createdWorktree
	var repos []config.RepoConfig

	for _, repo := range resolved {
		if ctx.Err() != nil {
			return created, nil, ctx.Err()
		}
		worktreePath := filepath.Join(wsDir, repo.name)
		if _, err := cli.RunGitCommand(repo.path, "worktree", "add", worktreePath, "-b", branch); err != nil {
			return created, nil, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git worktree add failed for %s", repo.name), err)
		}
		created = append(created, createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath, branch: branch})
		repos = append(repos, config.RepoConfig{
			Name:          repo.name,
			Path:          worktreePath,
			Remote:        "origin",
			DefaultBranch: branch,
			SourceRepoID:  repo.name,
		})
	}
	return created, repos, nil
}

// cleanupWorktrees removes created worktrees and, only when Loom created it,
// the workspace directory on failure.
func cleanupWorktrees(plan workspaceDirPlan, created []createdWorktree) {
	cleanupAttachedWorktrees(created)
	cleanupWorkspaceRoot(plan)
}

func cleanupAttachedWorktrees(created []createdWorktree) {
	for _, c := range created {
		_, _ = cli.RunGitCommand(c.origRepoPath, "worktree", "remove", c.worktreePath)
		if c.branch != "" {
			_, _ = cli.RunGitCommand(c.origRepoPath, "branch", "-D", c.branch)
		}
	}
}

// validateWorkspacePath ensures a workspace directory points at a safe local
// workspace root. Workspace roots may be custom user-selected directories in
// desktop mode, but they must not be broad container directories that rollback
// or repo operations could affect unexpectedly.
func validateWorkspacePath(wsDir string) error {
	wsDir = filepath.Clean(expandUserPath(wsDir))
	absDir, err := filepath.Abs(wsDir)
	if err != nil {
		return workspaceerrors.New(workspaceerrors.PathNotFound, fmt.Sprintf("cannot resolve workspace path %q", wsDir), err)
	}
	volumeRoot := filepath.VolumeName(absDir) + string(filepath.Separator)
	if absDir == volumeRoot {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && absDir == filepath.Clean(home) {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	configDir := filepath.Clean(config.GetConfigDir())
	if absConfigDir, err := filepath.Abs(configDir); err == nil && absDir == absConfigDir {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	if absDir == defaultWorkspaceBase() {
		return workspaceerrors.New(workspaceerrors.SecurityViolation, fmt.Sprintf("workspace path must be a workspace-specific folder under %s", defaultWorkspaceBase()), nil)
	}
	return nil
}

func defaultWorkspaceBase() string {
	base, err := filepath.Abs(filepath.Join(config.GetConfigDir(), "workspaces"))
	if err != nil {
		return filepath.Clean(filepath.Join(config.GetConfigDir(), "workspaces"))
	}
	return base
}

func isPathWithin(path, base string) bool {
	path = filepath.Clean(path)
	base = filepath.Clean(base)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// repoNameFromURL derives a fleet-db-safe directory/repo name from a git clone URL.
// e.g. "https://github.com/foo/Hello-World.git" -> "hello-world"
func repoNameFromURL(cloneURL string) string {
	// Strip trailing .git
	u := strings.TrimSuffix(cloneURL, ".git")
	// Strip trailing slashes
	u = strings.TrimRight(u, "/")
	// Take the last path segment
	if idx := strings.LastIndex(u, "/"); idx >= 0 {
		u = u[idx+1:]
	}
	// For SSH URLs like git@github.com:foo/bar
	if idx := strings.LastIndex(u, ":"); idx >= 0 {
		u = u[idx+1:]
	}
	return normalizeRepoName(u)
}

func normalizeRepoName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if allowed {
			b.WriteRune(r)
			lastDash = r == '-'
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "repo"
	}
	if len(out) > 100 {
		out = strings.Trim(out[:100], ".-_")
		if out == "" {
			return "repo"
		}
	}
	return out
}

// cloneRepos clones each URL into the workspace directory, deduplicating names.
func cloneRepos(ctx context.Context, cloneURLs []string, wsDir string) ([]config.RepoConfig, error) {
	return cloneReposWithSeen(ctx, cloneURLs, wsDir, make(map[string]bool))
}

func cloneReposWithSeen(ctx context.Context, cloneURLs []string, wsDir string, seenNames map[string]bool) ([]config.RepoConfig, error) {
	var repos []config.RepoConfig
	if seenNames == nil {
		seenNames = make(map[string]bool)
	}

	for _, cloneURL := range cloneURLs {
		if ctx.Err() != nil {
			cleanupClonedRepos(repos)
			return nil, ctx.Err()
		}

		repoName := deduplicateRepoName(repoNameFromURL(cloneURL), seenNames)
		seenNames[repoName] = true

		clonePath := filepath.Join(wsDir, repoName)
		cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, clonePath) //nolint:gosec // URL validated: prefix (https://|git@), no control chars, no dash-prefixed path segments, SSRF hostname blocklist
		if output, err := cmd.CombinedOutput(); err != nil {
			cleanupClonedRepos(repos)
			return nil, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output))), err)
		}

		repos = append(repos, config.RepoConfig{
			Name:         repoName,
			Path:         clonePath,
			Remote:       "origin",
			SourceRepoID: repoName,
		})
	}
	return repos, nil
}

func cleanupClonedRepos(repos []config.RepoConfig) {
	for _, repo := range repos {
		if repo.Path != "" {
			_ = os.RemoveAll(repo.Path)
		}
	}
}

// deduplicateRepoName appends a numeric suffix if the name is already taken.
func deduplicateRepoName(name string, seen map[string]bool) string {
	if !seen[name] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !seen[candidate] {
			return candidate
		}
	}
}

func cleanupWorkspaceRoot(plan workspaceDirPlan) {
	if plan.removeRootOnRollback && plan.path != "" {
		_ = os.RemoveAll(plan.path)
	}
}

func cleanupCloneWorkspace(plan workspaceDirPlan, repos []config.RepoConfig) {
	cleanupClonedRepos(repos)
	cleanupWorkspaceRoot(plan)
}
