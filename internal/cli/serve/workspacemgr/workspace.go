package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
	if localworkspace.PathContains(defaultWorkspaceBase(), wsDir) {
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
	// branch is non-empty only when this operation created the branch. Existing
	// healthy branches are checked out detached and must never be deleted by a
	// later rollback.
	branch string
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
		repoConfig := worktreeRepoConfig(repo, worktreePath, branch)
		worktree, err := createWorkspaceWorktree(repo, worktreePath, branch)
		if err != nil {
			if errors.Is(err, gitbranch.ErrRepositoryNotUsable) {
				return created, nil, workspaceerrors.New(workspaceerrors.GitFailed, fmt.Sprintf("source repo is not usable for %s", repo.name), err)
			}
			warnSkippedWorktree(ctx, repo.name, worktreePath, err)
			continue
		}
		created = append(created, worktree)
		repos = append(repos, repoConfig)
	}
	return created, repos, nil
}

func worktreeRepoConfig(repo resolvedRepo, worktreePath, branch string) config.RepoConfig {
	return config.RepoConfig{
		Name:          repo.name,
		Path:          worktreePath,
		Remote:        "origin",
		DefaultBranch: branch,
		SourceRepoID:  repo.name,
	}
}

func createWorkspaceWorktree(repo resolvedRepo, worktreePath, branch string) (createdWorktree, error) {
	info, err := gitbranch.Inspect(repo.path, branch)
	if err != nil {
		return createdWorktree{}, err
	}
	baseRef := ""
	createBranch := info.State != gitbranch.StateHealthy
	if info.State == gitbranch.StateBroken {
		recoveryBase := workspaceWorktreeRecoveryBase(repo.path, branch)
		recovery, err := gitbranch.Recover(repo.path, branch, recoveryBase, info)
		if err != nil {
			return createdWorktree{}, err
		}
		baseRef = recovery.BaseSHA
	} else if info.State == gitbranch.StateHealthy {
		// A local repo is commonly attached while its default branch is checked
		// out in the source checkout. Git correctly refuses to check the same
		// branch out in two worktrees. The workspace checkout is only a safe,
		// machine-local base for isolated task worktrees, so detach it at the
		// exact healthy branch tip instead of weakening Git's branch lock.
		baseRef = info.BaseSHA
	}
	createdBranch := ""
	if createBranch {
		args := []string{"branch", branch}
		if baseRef != "" {
			args = append(args, baseRef)
		}
		if _, err := cli.RunGitCommand(repo.path, args...); err != nil {
			return createdWorktree{}, err
		}
		createdBranch = branch
	}
	if err := addWorkspaceWorktree(repo.path, worktreePath, branch, baseRef, createBranch); err != nil {
		// Delete only a branch whose creation just succeeded in this operation.
		// If another process checked it out in the meantime, Git refuses the
		// deletion, preserving the concurrent owner's branch.
		if createdBranch != "" {
			_, _ = cli.RunGitCommand(repo.path, "branch", "-D", createdBranch)
		}
		return createdWorktree{}, err
	}
	return createdWorktree{origRepoPath: repo.path, worktreePath: worktreePath, branch: createdBranch}, nil
}

func workspaceWorktreeRecoveryBase(repoPath, targetBranch string) string {
	out, err := cli.RunGitCommand(repoPath, "branch", "--show-current")
	if err != nil {
		return ""
	}
	base := strings.TrimSpace(out)
	if base == "" || base == targetBranch {
		return ""
	}
	return base
}

func addWorkspaceWorktree(repoPath, worktreePath, branch, baseRef string, createBranch bool) error {
	args := []string{"worktree", "add"}
	if createBranch {
		args = append(args, worktreePath, branch)
	} else {
		args = append(args, "--detach", worktreePath)
		if baseRef != "" {
			args = append(args, baseRef)
		}
	}
	_, err := cli.RunGitCommand(repoPath, args...)
	return err
}

func warnSkippedWorktree(ctx context.Context, repoName, worktreePath string, err error) {
	msg := fmt.Sprintf("Skipped checkout for repo %q at %s: %v", repoName, worktreePath, err)
	slog.Warn("workspace bootstrap skipped checkout", "repo", repoName, "path", worktreePath, "err", err)
	service.AddCreateWarning(ctx, msg)
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
		if err := localworkspace.CloneRepoTo(ctx, cloneURL, clonePath); err != nil {
			cleanupClonedRepos(repos)
			return nil, workspaceerrors.New(workspaceerrors.GitFailed, err.Error(), err)
		}
		defaultBranch, err := detectClonedRepoDefaultBranch(clonePath, "origin")
		if err != nil {
			_ = os.RemoveAll(clonePath)
			cleanupClonedRepos(repos)
			return nil, workspaceerrors.New(
				workspaceerrors.GitFailed,
				fmt.Sprintf("detect default branch for cloned repo %q", repoName),
				err,
			)
		}

		repos = append(repos, config.RepoConfig{
			Name:          repoName,
			Path:          clonePath,
			Remote:        "origin",
			DefaultBranch: defaultBranch,
			SourceRepoID:  repoName,
		})
	}
	return repos, nil
}

// detectClonedRepoDefaultBranch resolves the branch selected by git clone.
// Prefer the remote's symbolic HEAD because it is the repository contract;
// fall back to the clone's symbolic HEAD for local/file remotes that do not
// advertise refs/remotes/<remote>/HEAD. A clone with no resolvable branch is
// not runnable and must not be registered with guessed metadata.
func detectClonedRepoDefaultBranch(repoPath, remote string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		remote = "origin"
	}
	remoteHead := "refs/remotes/" + remote + "/HEAD"
	if out, err := cli.RunGitCommand(repoPath, "symbolic-ref", "--quiet", "--short", remoteHead); err == nil {
		shortRef := strings.TrimSpace(out)
		branch := strings.TrimPrefix(shortRef, remote+"/")
		if branch != "" && gitRefResolvesToCommit(repoPath, shortRef) {
			return branch, nil
		}
	}
	if out, err := cli.RunGitCommand(repoPath, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		if branch := strings.TrimSpace(out); branch != "" && gitRefResolvesToCommit(repoPath, "HEAD") {
			return branch, nil
		}
	}
	return "", fmt.Errorf("remote %q and clone HEAD do not resolve to a committed branch", remote)
}

func gitRefResolvesToCommit(repoPath, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false
	}
	_, err := cli.RunGitCommand(repoPath, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// applyRequestedCloneBranch validates an explicit branch against every cloned
// repository. With no explicit branch, each clone keeps its independently
// detected remote HEAD. This prevents one shared workspace default from
// corrupting mixed-repository metadata.
func applyRequestedCloneBranch(repos []config.RepoConfig, requested string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	for i := range repos {
		repo := &repos[i]
		if _, err := cli.RunGitCommand(repo.Path, "check-ref-format", "--branch", requested); err != nil {
			return fmt.Errorf("invalid default branch %q for repo %q: %w", requested, repo.Name, err)
		}
		remote := strings.TrimSpace(repo.Remote)
		if remote == "" {
			remote = "origin"
		}
		remoteRef := "refs/remotes/" + remote + "/" + requested + "^{commit}"
		localRef := "refs/heads/" + requested + "^{commit}"
		if _, err := cli.RunGitCommand(repo.Path, "rev-parse", "--verify", "--quiet", remoteRef); err != nil {
			if _, localErr := cli.RunGitCommand(repo.Path, "rev-parse", "--verify", "--quiet", localRef); localErr != nil {
				return fmt.Errorf("default branch %q does not exist in cloned repo %q", requested, repo.Name)
			}
		}
		repo.DefaultBranch = requested
	}
	return nil
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
