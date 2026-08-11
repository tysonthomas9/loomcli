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
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
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
		return workspaceDirPlan{}, workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("cannot resolve workspace path %q", wsDir), err)
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
			return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("workspace path is not a directory: %s", wsDir), nil)
		}
		if _, err := os.Stat(filepath.Join(wsDir, ".git")); err == nil {
			return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path must not be an existing git repository: %s", wsDir), nil)
		}
		empty, err := dirIsEmpty(wsDir)
		if err != nil {
			return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("cannot inspect workspace path: %s", wsDir), err)
		}
		if !empty {
			return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path must be empty or not exist: %s", wsDir), nil)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("cannot inspect workspace path: %s", wsDir), err)
	}

	parent := filepath.Dir(wsDir)
	if PathContains(defaultWorkspaceBase(), wsDir) {
		return nil
	}
	info, err := os.Stat(parent)
	if err != nil {
		return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("workspace parent directory does not exist: %s", parent), err)
	}
	if !info.IsDir() {
		return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("workspace parent path is not a directory: %s", parent), nil)
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
			return nil, workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("cannot resolve path %q", rp), err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("repo path does not exist: %s", absPath), err)
		}
		if !info.IsDir() {
			return nil, workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("repo path is not a directory: %s", absPath), nil)
		}

		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			return nil, workspacemodule.NewCreateError(workspacemodule.NotGitRepo, fmt.Sprintf("not a git repository: %s", absPath), err)
		}

		baseName := filepath.Base(absPath)
		if prev, exists := seenNames[baseName]; exists {
			return nil, workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("duplicate repo name %q from paths %s and %s", baseName, prev, absPath), nil)
		}
		seenNames[baseName] = absPath
		resolved = append(resolved, resolvedRepo{path: absPath, name: baseName})
	}

	if len(resolved) == 0 {
		return nil, workspacemodule.NewCreateError(workspacemodule.PathNotFound, "no valid repos specified", nil)
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
	return addWorktreesWithRepoDefault(ctx, resolved, wsDir, branch, branch)
}

// addWorktreesWithRepoDefault keeps the workspace checkout branch separate
// from the repository's integration branch. A blank override auto-detects the
// source repository branch; this is the UI Add Repo path, where the workspace
// branch remains an isolation branch but task diffing must use the source base.
func addWorktreesWithRepoDefault(
	ctx context.Context,
	resolved []resolvedRepo,
	wsDir, worktreeBranch, defaultBranchOverride string,
) ([]createdWorktree, []config.RepoConfig, error) {
	var created []createdWorktree
	var repos []config.RepoConfig

	for _, repo := range resolved {
		if ctx.Err() != nil {
			return created, nil, ctx.Err()
		}
		worktreePath := filepath.Join(wsDir, repo.name)
		repoConfig, err := worktreeRepoConfig(repo, worktreePath, defaultBranchOverride)
		if err != nil {
			return created, nil, workspacemodule.NewCreateError(
				workspacemodule.GitFailed,
				fmt.Sprintf("detect default branch for local repo %q", repo.name),
				err,
			)
		}
		worktree, err := createWorkspaceWorktree(repo, worktreePath, worktreeBranch)
		if err != nil {
			if errors.Is(err, ErrRepositoryNotUsable) {
				return created, nil, workspacemodule.NewCreateError(workspacemodule.GitFailed, fmt.Sprintf("source repo is not usable for %s", repo.name), err)
			}
			warnSkippedWorktree(ctx, repo.name, worktreePath, err)
			continue
		}
		created = append(created, worktree)
		repos = append(repos, repoConfig)
	}
	return created, repos, nil
}

func worktreeRepoConfig(repo resolvedRepo, worktreePath, defaultBranchOverride string) (config.RepoConfig, error) {
	defaultBranch := strings.TrimSpace(defaultBranchOverride)
	if defaultBranch == "" {
		var err error
		defaultBranch, err = detectRepoDefaultBranch(repo.path)
		if err != nil {
			return config.RepoConfig{}, err
		}
	}
	return config.RepoConfig{
		Name:          repo.name,
		Path:          worktreePath,
		Remote:        "origin",
		DefaultBranch: defaultBranch,
		SourceRepoID:  repo.name,
	}, nil
}

func createWorkspaceWorktree(repo resolvedRepo, worktreePath, branch string) (createdWorktree, error) {
	return createWorkspaceWorktreeContext(
		context.Background(),
		repo,
		worktreePath,
		branch,
	)
}

func createWorkspaceWorktreeContext(
	ctx context.Context,
	repo resolvedRepo,
	worktreePath,
	branch string,
) (createdWorktree, error) {
	created, err := CreateWorktreeContext(
		ctx,
		repo.path,
		worktreePath,
		branch,
	)
	if err != nil {
		return createdWorktree{}, err
	}
	return createdWorktree{
		origRepoPath: created.OriginalRepositoryPath,
		worktreePath: created.WorktreePath,
		branch:       created.Branch,
	}, nil
}

func workspaceWorktreeRecoveryBase(repoPath, targetBranch string) string {
	return RecoveryBase(repoPath, targetBranch)
}

func workspaceWorktreeRecoveryBaseContext(
	ctx context.Context,
	repoPath,
	targetBranch string,
) string {
	return RecoveryBaseContext(ctx, repoPath, targetBranch)
}

func addWorkspaceWorktree(repoPath, worktreePath, branch, baseRef string, createBranch bool) error {
	return AddWorktree(
		repoPath,
		worktreePath,
		branch,
		baseRef,
		createBranch,
	)
}

func addWorkspaceWorktreeContext(
	ctx context.Context,
	repoPath,
	worktreePath,
	branch,
	baseRef string,
	createBranch bool,
) error {
	return AddWorktreeContext(
		ctx,
		repoPath,
		worktreePath,
		branch,
		baseRef,
		createBranch,
	)
}

func runWorkspaceGitContext(
	ctx context.Context,
	dir string,
	args ...string,
) (string, error) {
	return RunGitContext(ctx, dir, args...)
}

func warnSkippedWorktree(ctx context.Context, repoName, worktreePath string, err error) {
	msg := fmt.Sprintf("Skipped checkout for repo %q at %s: %v", repoName, worktreePath, err)
	slog.Warn("workspace bootstrap skipped checkout", "repo", repoName, "path", worktreePath, "err", err)
	workspacecoord.AddCreateWarning(ctx, msg)
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
		return workspacemodule.NewCreateError(workspacemodule.PathNotFound, fmt.Sprintf("cannot resolve workspace path %q", wsDir), err)
	}
	volumeRoot := filepath.VolumeName(absDir) + string(filepath.Separator)
	if absDir == volumeRoot {
		return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && absDir == filepath.Clean(home) {
		return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	configDir := filepath.Clean(config.GetConfigDir())
	if absConfigDir, err := filepath.Abs(configDir); err == nil && absDir == absConfigDir {
		return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path is too broad: %s", absDir), nil)
	}
	if absDir == defaultWorkspaceBase() {
		return workspacemodule.NewCreateError(workspacemodule.SecurityViolation, fmt.Sprintf("workspace path must be a workspace-specific folder under %s", defaultWorkspaceBase()), nil)
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

// detectRepoDefaultBranch resolves the repository's integration branch.
// Prefer the remote's symbolic HEAD because it is the repository contract.
// Older local-mode fixtures may omit that symbolic ref while still carrying a
// conventional fetched main/master branch, so accept those deterministically.
// A configured remote with no advertised or conventional base fails closed;
// only a repo with no remote may use its checkout's symbolic HEAD.
func detectRepoDefaultBranch(repoPath string) (string, error) {
	const remote = "origin"
	remotePrefix := "refs/remotes/" + remote + "/"
	remoteHead := remotePrefix + "HEAD"
	if out, err := cli.RunGitCommand(repoPath, "symbolic-ref", "--quiet", remoteHead); err == nil {
		target := strings.TrimSpace(out)
		branch := strings.TrimPrefix(target, remotePrefix)
		if target != branch && branch != "" && branch != "HEAD" && gitRefResolvesToCommit(repoPath, target) {
			return branch, nil
		}
	}
	for _, branch := range []string{"main", "master"} {
		ref := remotePrefix + branch
		if gitRefIsDirect(repoPath, ref) && gitRefResolvesToCommit(repoPath, ref) {
			return branch, nil
		}
	}
	if out, err := cli.RunGitCommand(repoPath, "remote", "get-url", remote); err == nil && strings.TrimSpace(out) != "" {
		return "", fmt.Errorf("remote %q does not advertise a resolvable committed default branch; specify one explicitly", remote)
	}
	const localPrefix = "refs/heads/"
	if out, err := cli.RunGitCommand(repoPath, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		target := strings.TrimSpace(out)
		branch := strings.TrimPrefix(target, localPrefix)
		if target != branch && branch != "" && gitRefResolvesToCommit(repoPath, target) {
			return branch, nil
		}
	}
	return "", fmt.Errorf("remote %q and repository HEAD do not resolve to a committed branch", remote)
}

func gitRefIsDirect(repoPath, ref string) bool {
	out, err := cli.RunGitCommand(repoPath, "for-each-ref", "--format=%(symref)", "--count=1", ref)
	return err == nil && strings.TrimSpace(out) == ""
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
