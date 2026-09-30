// Package localworkspace contains machine-local workspace filesystem helpers.
package localworkspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// Repo is the local filesystem view of a workspace repository.
type Repo struct {
	Name          string
	Path          string
	Remote        string
	DefaultBranch string
	Groups        []string
}

// RepoPath returns the best-known local path for a repo in a workspace.
func RepoPath(local bootstrap.WorkspaceLocalState, repoName string) string {
	if local.Repos != nil && local.Repos[repoName] != "" {
		return local.Repos[repoName]
	}
	if local.Path != "" {
		return filepath.Join(local.Path, repoName)
	}
	return ""
}

// AgentWorktreePath returns the canonical local worktree path for an agent.
func AgentWorktreePath(workspacePath, repoName, agentName string) string {
	return filepath.Join(workspacePath, "worktrees", repoName, agentName)
}

// TaskRunWorktreePath returns the canonical isolated worktree path for a task
// run. The path is deliberately separate from agent worktrees so concurrent
// local-task-runner executions never share a mutable checkout.
func TaskRunWorktreePath(workspacePath, repoName, taskRunID string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if strings.TrimSpace(repoName) == "" {
		return "", fmt.Errorf("repo name is empty")
	}
	if strings.TrimSpace(taskRunID) == "" {
		return "", fmt.Errorf("task run id is empty")
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, ".loom", "task-worktrees", safePathSegment(repoName), safePathSegment(taskRunID)))
	if err != nil {
		return "", err
	}
	if !PathContains(root, target) || root == target {
		return "", fmt.Errorf("task worktree path escapes workspace: %s", target)
	}
	return target, nil
}

// TaskCopyPath names a distinct checkout for every attempt of a task run.
func TaskCopyPath(workspacePath, repoName, attemptID string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" || strings.TrimSpace(repoName) == "" ||
		strings.TrimSpace(attemptID) == "" || safePathSegment(attemptID) != attemptID {
		return "", fmt.Errorf("invalid task copy path")
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, ".loom", "task-copies", safePathSegment(repoName), attemptID))
	if err != nil {
		return "", err
	}
	if !PathContains(root, target) || root == target {
		return "", fmt.Errorf("task copy path escapes workspace: %s", target)
	}
	return target, nil
}

// ResolveTaskBase refreshes the selected branch and returns its exact commit.
func ResolveTaskBase(repoPath, remote, branch string) (string, error) {
	ref, err := resolveFreshBaseRef(repoPath, remote, branch)
	if err != nil {
		return "", err
	}
	if ref == "" {
		ref = "HEAD"
	}
	sha, err := runGit(context.Background(), repoPath, "rev-parse", "--verify", ref+"^{commit}")
	return strings.TrimSpace(sha), err
}

// PRReviewWorktreePath returns a fresh review checkout path under the PR's
// directory. reviewID must be unique for each review attempt.
func PRReviewWorktreePath(workspacePath, repoName string, prNumber int, reviewID string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if strings.TrimSpace(repoName) == "" {
		return "", fmt.Errorf("repo name is empty")
	}
	if prNumber <= 0 {
		return "", fmt.Errorf("pr number must be positive")
	}
	if strings.TrimSpace(reviewID) == "" || safePathSegment(reviewID) != reviewID {
		return "", fmt.Errorf("invalid review id")
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, ".loom", "pr-worktrees", safePathSegment(repoName), fmt.Sprintf("pr-%d", prNumber), reviewID))
	if err != nil {
		return "", err
	}
	if !PathContains(root, target) || root == target {
		return "", fmt.Errorf("pr review worktree path escapes workspace: %s", target)
	}
	return target, nil
}

// RepoCheckoutPath returns a safe direct child path under workspacePath.
func RepoCheckoutPath(workspacePath, name string) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("repo name is empty")
	}
	if filepath.IsAbs(name) || strings.Contains(name, string(filepath.Separator)) {
		return "", fmt.Errorf("repo name must not be a path: %s", name)
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	if !PathContains(root, target) || root == target {
		return "", fmt.Errorf("repo checkout path escapes workspace: %s", target)
	}
	return target, nil
}

// PathContains reports whether path is equal to or inside root.
func PathContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// CloneRepoTo clones cloneURL into targetPath.
func CloneRepoTo(ctx context.Context, cloneURL, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create clone parent directory: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, targetPath) //nolint:gosec // URL is validated upstream and passed as argv.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone failed for %s: %s", cloneURL, strings.TrimSpace(string(output)))
	}
	return nil
}

// EnsureGitWorktree creates a git worktree at targetPath from repoPath.
func EnsureGitWorktree(repoPath, targetPath, branchName string) error {
	return EnsureGitWorktreeFromBranch(repoPath, targetPath, branchName, "", "")
}

// PrepareWorkspaceBase checks Loom's branch namespace and resolves the trunk
// to a commit before a workspace creates any checkout or record.
func PrepareWorkspaceBase(repoPath, workspace, remote, trunk string) (string, error) {
	if err := loomgit.CheckWorkspaceNamespace(repoPath, workspace); err != nil {
		return "", err
	}
	branch, err := loomgit.InteractiveBranch(workspace, "lead")
	if err != nil {
		return "", err
	}
	if _, err := runGit(context.Background(), repoPath, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		return "", loomgit.NewError(loomgit.RefNamespaceConflict, "branch already exists: "+branch, nil)
	}
	base, err := resolveFreshBaseRef(repoPath, remote, trunk)
	if err != nil {
		return "", loomgit.NewError(loomgit.BaseRefUnresolvable, fmt.Sprintf("resolve trunk %q", trunk), err)
	}
	if base == "" {
		return "", loomgit.NewError(loomgit.BaseRefUnresolvable, "trunk is required", nil)
	}
	out, err := runGit(context.Background(), repoPath, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return "", loomgit.NewError(loomgit.BaseRefUnresolvable, fmt.Sprintf("resolve trunk %q", trunk), err)
	}
	return strings.TrimSpace(out), nil
}

// EnsureDetachedGitWorktreeFromBranch creates a detached git worktree at
// targetPath from the latest available remote/defaultBranch ref. Existing
// worktrees are left untouched.
func EnsureDetachedGitWorktreeFromBranch(repoPath, targetPath, remoteName, defaultBranch string) error {
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	baseRef, err := resolveFreshBaseRef(repoPath, remoteName, defaultBranch)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add", "--detach", targetPath}
	if baseRef != "" {
		args = append(args, baseRef)
	}
	_, err = runGit(context.Background(), repoPath, args...)
	return err
}

// PRHeadChangedError reports that the fetched PR tip no longer matches the
// expected head. The target worktree is left untouched when this is returned.
type PRHeadChangedError struct {
	ExpectedSHA string
	TipSHA      string
}

func (e *PRHeadChangedError) Error() string {
	return fmt.Sprintf("fetched PR tip %s does not match expected head %s", e.TipSHA, e.ExpectedSHA)
}

// validatePRWorktreeInputs checks the required inputs and returns the
// effective remote name (default origin).
func validatePRWorktreeInputs(repoPath, targetPath, remoteName string, prNumber int) (string, error) {
	if strings.TrimSpace(repoPath) == "" {
		return "", fmt.Errorf("repo path is empty")
	}
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("target path is empty")
	}
	if prNumber <= 0 {
		return "", fmt.Errorf("pr number must be positive")
	}
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		remoteName = "origin"
	}
	return remoteName, nil
}

// EnsureDetachedGitWorktreeAtPRHead creates a detached git worktree at a new
// targetPath when the fetched PR tip matches the expected head. A file lock
// serializes fetch and checkout across serve and daemon processes.
func EnsureDetachedGitWorktreeAtPRHead(
	ctx context.Context,
	repoPath, targetPath, remoteName string,
	prNumber int,
	headSHA string,
) (string, error) {
	remoteName, err := validatePRWorktreeInputs(repoPath, targetPath, remoteName, prNumber)
	if err != nil {
		return "", err
	}

	lock, err := lockPRReviewWorktree(targetPath)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	defer func() { _ = lockfile.FlockUnlock(lock) }()

	checkoutRef := fmt.Sprintf("refs/loom/pr/%d/head", prNumber)
	fetchRef := fmt.Sprintf("+refs/pull/%d/head:%s", prNumber, checkoutRef)
	// Authentication remains the responsibility of the configured git remote
	// and credential helper; direct connector-token injection is deferred.
	if _, err = runGit(ctx, repoPath, "fetch", remoteName, fetchRef); err != nil {
		return "", fmt.Errorf("fetch PR #%d head from %q: %w", prNumber, remoteName, err)
	}

	tipOut, err := runGit(ctx, repoPath, "rev-parse", "--verify", checkoutRef+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve PR #%d fetched tip %q: %w", prNumber, checkoutRef, err)
	}
	tipSHA := strings.TrimSpace(tipOut)
	expectedSHA := strings.TrimSpace(headSHA)
	if !strings.EqualFold(tipSHA, expectedSHA) {
		return tipSHA, &PRHeadChangedError{ExpectedSHA: expectedSHA, TipSHA: tipSHA}
	}

	if _, err := os.Lstat(targetPath); err == nil {
		return "", fmt.Errorf("PR review worktree path already exists: %s", targetPath)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect PR review worktree path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return "", fmt.Errorf("create PR review worktree parent: %w", err)
	}
	if _, err := runGit(ctx, repoPath, "worktree", "add", "--detach", targetPath, tipSHA); err != nil {
		// The target was absent before add, so only this attempt can own it.
		_ = os.RemoveAll(targetPath)
		return "", fmt.Errorf("add PR review worktree at %s: %w", targetPath, err)
	}
	return tipSHA, nil
}

func lockPRReviewWorktree(targetPath string) (*os.File, error) {
	lockPath := filepath.Dir(targetPath) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create PR review lock parent: %w", err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: opens a lock, not file content; path belongs to this review checkout.
	if err != nil {
		return nil, fmt.Errorf("open PR review lock: %w", err)
	}
	if err := lockfile.FlockExclusiveBlocking(lock); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock PR review: %w", err)
	}
	return lock, nil
}

// EnsureGitWorktreeFromBranch creates a git worktree at targetPath. When
// defaultBranch is provided, the new branch is created from the latest fetched
// remote/defaultBranch ref when available, falling back to the local branch.
// Existing worktrees are left untouched.
func EnsureGitWorktreeFromBranch(repoPath, targetPath, branchName, remoteName, defaultBranch string) error {
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	branch, err := gitbranch.Inspect(repoPath, branchName)
	if err != nil {
		return err
	}
	if branch.State == gitbranch.StateBroken {
		recovery, err := gitbranch.Recover(repoPath, branchName, defaultBranch, branch)
		if err != nil {
			return err
		}
		return addBranchWorktree(repoPath, targetPath, branchName, recovery.BaseSHA)
	}

	baseRef, err := resolveFreshBaseRef(repoPath, remoteName, defaultBranch)
	if err != nil {
		return err
	}
	return addBranchWorktree(repoPath, targetPath, branchName, baseRef)
}

func addBranchWorktree(repoPath, targetPath, branchName, baseRef string) error {
	args := []string{"worktree", "add", targetPath, "-b", branchName}
	if baseRef != "" {
		args = append(args, baseRef)
	}
	if out, err := runGit(context.Background(), repoPath, args...); err == nil {
		return nil
	} else if !branchAlreadyExists(out, err) {
		return err
	}
	if _, err := runGit(context.Background(), repoPath, "worktree", "add", targetPath, branchName); err != nil {
		return err
	}
	return nil
}

func resolveFreshBaseRef(repoPath, remoteName, defaultBranch string) (string, error) {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		return "", nil
	}
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		remoteName = "origin"
	}

	if _, err := runGit(context.Background(), repoPath, "remote", "get-url", remoteName); err == nil {
		if _, err := runGit(context.Background(), repoPath, "fetch", remoteName, defaultBranch); err != nil {
			if _, localErr := runGit(context.Background(), repoPath, "rev-parse", "--verify", defaultBranch); localErr == nil {
				return defaultBranch, nil
			}
			return "", fmt.Errorf("fetch base branch %q from %q: %w", defaultBranch, remoteName, err)
		}
		return remoteName + "/" + defaultBranch, nil
	}

	if _, err := runGit(context.Background(), repoPath, "fetch", remoteName, defaultBranch); err == nil {
		return remoteName + "/" + defaultBranch, nil
	}
	if _, err := runGit(context.Background(), repoPath, "rev-parse", "--verify", defaultBranch); err != nil {
		return "", fmt.Errorf("resolve base branch %q: %w", defaultBranch, err)
	}
	return defaultBranch, nil
}

// RecordPRReviewContext makes a PR-head review worktree self-describing: it
// fetches the PR's base branch into the checkout and records the base commit
// (plus optional metadata) in PER-WORKTREE git config, so a generic reviewer
// prompt can diff `git diff "$(git config loom.reviewBase)"...HEAD` without any
// PR-specific data being injected into the prompt. Per-worktree config keeps
// concurrent reviews of different PRs in the same repo from colliding. Returns
// the resolved base commit sha.
func RecordPRReviewContext(
	ctx context.Context,
	worktreePath, remoteName, baseRef string,
	meta map[string]string,
) (string, error) {
	if strings.TrimSpace(worktreePath) == "" {
		return "", fmt.Errorf("worktree path is empty")
	}
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		remoteName = "origin"
	}
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		return "", fmt.Errorf("base ref is empty")
	}
	// extensions.worktreeConfig must be enabled before --worktree config writes
	// land in this worktree's private config (idempotent, main-repo scoped).
	if out, err := runGit(ctx, worktreePath, "config", "extensions.worktreeConfig", "true"); err != nil {
		return "", fmt.Errorf("enable worktree config: %w: %s", err, out)
	}
	// `--` terminates option parsing so a base ref can never be read as a git
	// flag (baseRef comes from the connector's PR metadata).
	if out, err := runGit(ctx, worktreePath, "fetch", remoteName, "--", baseRef); err != nil {
		return "", fmt.Errorf("fetch review base %q from %q: %w: %s", baseRef, remoteName, err, out)
	}
	out, err := runGit(ctx, worktreePath, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve review base sha: %w", err)
	}
	baseSHA := strings.TrimSpace(out)
	if _, err := runGit(ctx, worktreePath, "config", "--worktree", "loom.reviewBase", baseSHA); err != nil {
		return "", fmt.Errorf("record review base: %w", err)
	}
	for key, value := range meta {
		value = strings.TrimSpace(value)
		if value == "" || strings.TrimSpace(key) == "" {
			continue
		}
		// Best-effort niceties (PR number/title/url) for the reviewer's summary.
		_, _ = runGit(ctx, worktreePath, "config", "--worktree", "loom.review"+key, value)
	}
	return baseSHA, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // G204: fixed git executable; args are controlled by internal worktree callers.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// GitRemoteURL returns the configured URL of the named remote (default "origin")
// for the git checkout at dir. It returns an error when dir is not a git work
// tree or the remote is unset — callers treat that as the "not a usable
// checkout" signal (e.g. workspace local-path self-heal verification).
func GitRemoteURL(dir, remote string) (string, error) {
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	out, err := runGit(context.Background(), dir, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func branchAlreadyExists(out string, err error) bool {
	msg := out
	if err != nil {
		msg += "\n" + err.Error()
	}
	return strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "already registered") ||
		strings.Contains(msg, "already a worktree") ||
		strings.Contains(msg, "already checked out")
}

func safePathSegment(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return "unnamed"
	}
	return out
}

// SelectAgentRepos applies an agent's repo affinity to local repos.
func SelectAgentRepos(repos []Repo, agent domain.Agent) ([]Repo, error) {
	if len(repos) == 0 {
		return nil, nil
	}
	if agent.CrossRepo {
		return repos, nil
	}
	allowed := make(map[string]struct{})
	for _, name := range agent.Repos {
		allowed[name] = struct{}{}
	}
	for _, group := range agent.RepoGroups {
		for _, repo := range repos {
			for _, repoGroup := range repo.Groups {
				if repoGroup == group {
					allowed[repo.Name] = struct{}{}
					break
				}
			}
		}
	}
	if len(allowed) == 0 {
		return []Repo{repos[0]}, nil
	}
	selected := make([]Repo, 0, len(allowed))
	for _, repo := range repos {
		if _, ok := allowed[repo.Name]; ok {
			selected = append(selected, repo)
		}
	}
	if len(selected) == 0 {
		names := make([]string, 0, len(repos))
		for _, repo := range repos {
			names = append(names, repo.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("agent repo affinity does not match any workspace repo; available repos: %s", strings.Join(names, ", "))
	}
	return selected, nil
}

// FirstWorktreePath returns a deterministic path from a repo-name keyed map.
func FirstWorktreePath(paths map[string]string) string {
	if len(paths) == 0 {
		return ""
	}
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	return paths[names[0]]
}

// RememberAgentWorktree stores an agent's local worktree path.
func RememberAgentWorktree(wsKey, agentName, worktreePath string) error {
	return bootstrap.MutateWorkspaceLocalState(wsKey, func(local *bootstrap.WorkspaceLocalState) error {
		if local.Agents == nil {
			local.Agents = make(map[string]bootstrap.AgentLocalState)
		}
		local.Agents[agentName] = bootstrap.AgentLocalState{Worktree: worktreePath}
		return nil
	})
}

// RememberedAgentWorktree returns the agent's remembered worktree path,
// validated to still be a directory containing a .git entry. Returns ("",
// false) when no usable worktree is recorded. This is the single source of
// an agent's launch cwd: the terminal launch path and any reader that must
// mirror the agent's working directory (e.g. harness transcript lookup,
// which indexes by cwd) both resolve through it so they can never disagree.
func RememberedAgentWorktree(wsKey, agentName string) (string, bool) {
	cache, err := bootstrap.LoadStateCache()
	if err != nil || cache == nil {
		return "", false
	}
	local := cache.Workspaces[wsKey]
	worktree := strings.TrimSpace(local.Agents[agentName].Worktree)
	if worktree == "" {
		return "", false
	}
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		return "", false
	}
	return worktree, true
}

// RememberRepoPath stores a repo's local checkout path.
func RememberRepoPath(wsKey, repoName, repoPath string) error {
	return bootstrap.MutateWorkspaceLocalState(wsKey, func(local *bootstrap.WorkspaceLocalState) error {
		if local.Repos == nil {
			local.Repos = make(map[string]string)
		}
		local.Repos[repoName] = repoPath
		return nil
	})
}
