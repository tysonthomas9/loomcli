// Package localworkspace contains machine-local workspace filesystem helpers.
package localworkspace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/gitbranch"
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

// PRReviewWorktreePath returns the canonical isolated worktree path for a PR
// review checkout.
func PRReviewWorktreePath(workspacePath, repoName string, prNumber int) (string, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return "", fmt.Errorf("workspace path is empty")
	}
	if strings.TrimSpace(repoName) == "" {
		return "", fmt.Errorf("repo name is empty")
	}
	if prNumber <= 0 {
		return "", fmt.Errorf("pr number must be positive")
	}
	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, ".loom", "pr-worktrees", safePathSegment(repoName), fmt.Sprintf("pr-%d", prNumber)))
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
	return CloneRepoToAnonymous(ctx, cloneURL, "origin", targetPath)
}

// CloneRepoToAnonymous performs one token-free clone with ambient credential
// helpers disabled. Credential policy and retry selection remain with
// Connectors/localgit.
func CloneRepoToAnonymous(ctx context.Context, cloneURL, remoteName, targetPath string) error {
	if err := prepareBoundedClone(cloneURL, targetPath); err != nil {
		return err
	}
	args, err := boundedCloneArgs(cloneURL, remoteName, targetPath)
	if err != nil {
		return err
	}
	if _, err := runGitAnonymous(
		ctx,
		filepath.Dir(targetPath),
		args...,
	); err != nil {
		return fmt.Errorf("anonymous git clone failed for %s: %w", sanitizedRemoteURL(cloneURL), err)
	}
	return nil
}

func prepareBoundedClone(cloneURL, targetPath string) error {
	if err := rejectRemoteURLSecrets(cloneURL); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create clone parent directory: %w", err)
	}
	return nil
}

func boundedCloneArgs(cloneURL, remoteName, targetPath string) ([]string, error) {
	if strings.TrimSpace(remoteName) == "" ||
		strings.HasPrefix(remoteName, "-") ||
		strings.ContainsAny(remoteName, " \t\r\n:/\\") {
		return nil, fmt.Errorf("git remote name is invalid")
	}
	return []string{"clone", "--origin", remoteName, "--", cloneURL, targetPath}, nil
}

// EnsureGitWorktree creates a git worktree at targetPath from repoPath.
func EnsureGitWorktree(repoPath, targetPath, branchName string) error {
	return EnsureGitWorktreeFromBranch(repoPath, targetPath, branchName, "", "")
}

// EnsureDetachedGitWorktreeFromBranch creates a detached git worktree at
// targetPath from the latest available remote/defaultBranch ref. Existing
// worktrees are left untouched.
func EnsureDetachedGitWorktreeFromBranch(repoPath, targetPath, remoteName, defaultBranch string) error {
	ctx := context.Background()
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	baseRef, err := resolveFreshBaseRef(ctx, repoPath, remoteName, defaultBranch)
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

// EnsureDetachedGitWorktreeAtRef creates a detached worktree from an already
// materialized local ref. It never performs a network operation.
func EnsureDetachedGitWorktreeAtRef(
	ctx context.Context,
	repoPath, targetPath, baseRef, baseCommit string,
) error {
	baseRef = strings.TrimSpace(baseRef)
	baseCommit = strings.TrimSpace(baseCommit)
	if !strings.HasPrefix(baseRef, "refs/loom/") || baseCommit == "" {
		return fmt.Errorf("Source-Control-owned task base ref and commit are required")
	}
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}
	tipOut, err := runGit(ctx, repoPath, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve fetched task base %q: %w", baseRef, err)
	}
	tipCommit := strings.TrimSpace(tipOut)
	if !strings.EqualFold(tipCommit, baseCommit) {
		return fmt.Errorf("fetched task base changed from the Source Control receipt")
	}
	args := []string{"worktree", "add", "--detach", targetPath, tipCommit}
	_, err = runGit(ctx, repoPath, args...)
	return err
}

// FetchGitRefAnonymous performs one exact read-only ref fetch with ambient Git
// credential helpers disabled. Source Control validates the refs before this
// infrastructure helper is reached.
func FetchGitRefAnonymous(
	ctx context.Context,
	repoPath, remoteName, sourceRef, destinationRef string,
) error {
	args, err := exactFetchRefArgs(remoteName, sourceRef, destinationRef)
	if err != nil {
		return err
	}
	if out, err := runGitAnonymous(ctx, repoPath, args...); err != nil {
		return fmt.Errorf("anonymous git fetch failed: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func exactFetchRefArgs(remoteName, sourceRef, destinationRef string) ([]string, error) {
	if strings.TrimSpace(remoteName) == "" || strings.HasPrefix(remoteName, "-") ||
		strings.ContainsAny(remoteName, " \t\r\n:/\\") {
		return nil, fmt.Errorf("git remote name is invalid")
	}
	for _, value := range []string{sourceRef, destinationRef} {
		if !strings.HasPrefix(value, "refs/") || strings.ContainsAny(value, " \t\r\n:") ||
			strings.HasPrefix(value, "-") {
			return nil, fmt.Errorf("git ref is invalid")
		}
	}
	return []string{
		"fetch", "--no-tags", "--force", "--",
		remoteName, sourceRef + ":" + destinationRef,
	}, nil
}

// prWorktreeLocks serializes EnsureDetachedGitWorktreeAtPRHead per target path
// within a process, so a second concurrent call for the same PR can't tear down
// (worktree remove --force) a checkout the first call is actively serving.
var prWorktreeLocks sync.Map

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

// EnsureDetachedGitWorktreeAtPRHead creates or updates a detached git worktree
// at targetPath when the fetched PR tip matches the expected head.
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

	lockAny, _ := prWorktreeLocks.LoadOrStore(targetPath, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	checkoutRef := fmt.Sprintf("refs/loom/pr/%d/head", prNumber)
	fetchRef := fmt.Sprintf("+refs/pull/%d/head:%s", prNumber, checkoutRef)
	if _, err = runGitAnonymous(ctx, repoPath, "fetch", remoteName, fetchRef); err != nil {
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

	return syncPRWorktree(ctx, repoPath, targetPath, checkoutRef, tipSHA)
}

// EnsureDetachedGitWorktreeAtFetchedPRHead materializes a PR worktree from a
// Source-Control-owned ref that has already been fetched and commit-verified.
// It performs no network operation.
func EnsureDetachedGitWorktreeAtFetchedPRHead(
	ctx context.Context,
	repoPath, targetPath, headRef, headSHA string,
) (string, error) {
	headRef = strings.TrimSpace(headRef)
	headSHA = strings.TrimSpace(headSHA)
	if !strings.HasPrefix(headRef, "refs/loom/pr-reviews/") ||
		headSHA == "" {
		return "", fmt.Errorf("Source-Control-owned PR head ref and commit are required")
	}
	lockAny, _ := prWorktreeLocks.LoadOrStore(targetPath, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	tipOut, err := runGit(ctx, repoPath, "rev-parse", "--verify", headRef+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve fetched PR head %q: %w", headRef, err)
	}
	tipSHA := strings.TrimSpace(tipOut)
	if !strings.EqualFold(tipSHA, headSHA) {
		return tipSHA, &PRHeadChangedError{ExpectedSHA: headSHA, TipSHA: tipSHA}
	}
	return syncPRWorktree(ctx, repoPath, targetPath, tipSHA, tipSHA)
}

// syncPRWorktree materializes checkout at targetPath: create when absent,
// cache-hit when already there and pristine, else scrub back to the exact
// sha (reset+clean), recreating the worktree when even that fails.
func syncPRWorktree(ctx context.Context, repoPath, targetPath, checkout, expectHEAD string) (string, error) {
	addWorktree := func() error {
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating PR worktree parent: %w", err)
		}
		out, err := runGit(ctx, repoPath, "worktree", "add", "--detach", targetPath, checkout)
		if err == nil {
			return nil
		}
		if !branchAlreadyExists(out, err) {
			return fmt.Errorf("add PR worktree at %s: %w", targetPath, err)
		}
		_, _ = runGit(ctx, repoPath, "worktree", "remove", "--force", targetPath)
		if _, err := runGit(ctx, repoPath, "worktree", "add", "--detach", targetPath, checkout); err != nil {
			return fmt.Errorf("add PR worktree at %s after removing stale registration: %w", targetPath, err)
		}
		return nil
	}

	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat PR worktree git dir: %w", err)
		}
		if err := addWorktree(); err != nil {
			return "", err
		}
		return expectHEAD, nil
	}

	// Cache hit only when the worktree is at the target sha AND pristine — a
	// review checkout must faithfully match the PR head, so drift left by a
	// prior session (untracked/modified files, interrupted clean) is scrubbed
	// via the reset+clean path rather than handed back dirty.
	head, _ := runGit(ctx, targetPath, "rev-parse", "HEAD")
	if strings.TrimSpace(head) == expectHEAD {
		if status, err := runGit(ctx, targetPath, "status", "--porcelain"); err == nil && strings.TrimSpace(status) == "" {
			return expectHEAD, nil
		}
	}

	if _, err := runGit(ctx, targetPath, "reset", "--hard", checkout); err != nil {
		_, _ = runGit(ctx, repoPath, "worktree", "remove", "--force", targetPath)
		if err := addWorktree(); err != nil {
			return "", fmt.Errorf("recreate PR worktree at %s after reset failure: %w", targetPath, err)
		}
		return expectHEAD, nil
	}
	if _, err := runGit(ctx, targetPath, "clean", "-fdx"); err != nil {
		return "", fmt.Errorf("clean PR worktree at %s: %w", targetPath, err)
	}
	return expectHEAD, nil
}

// EnsureGitWorktreeFromBranch creates a git worktree at targetPath. When
// defaultBranch is provided, the new branch is created from the latest fetched
// remote/defaultBranch ref when available, falling back to the local branch.
// Existing worktrees are left untouched.
func EnsureGitWorktreeFromBranch(repoPath, targetPath, branchName, remoteName, defaultBranch string) error {
	ctx := context.Background()
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

	baseRef, err := resolveFreshBaseRef(ctx, repoPath, remoteName, defaultBranch)
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

func resolveFreshBaseRef(
	ctx context.Context,
	repoPath, remoteName, defaultBranch string,
) (string, error) {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		return "", nil
	}
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		remoteName = "origin"
	}

	if _, err := runGit(ctx, repoPath, "remote", "get-url", remoteName); err == nil {
		if _, err := runGitAnonymous(ctx, repoPath, "fetch", remoteName, defaultBranch); err != nil {
			if _, localErr := runGit(context.Background(), repoPath, "rev-parse", "--verify", defaultBranch); localErr == nil {
				return defaultBranch, nil
			}
			return "", fmt.Errorf("fetch base branch %q from %q: %w", defaultBranch, remoteName, err)
		}
		return remoteName + "/" + defaultBranch, nil
	}

	if _, err := runGitAnonymous(ctx, repoPath, "fetch", remoteName, defaultBranch); err == nil {
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
	if out, err := runGitAnonymous(ctx, worktreePath, "fetch", remoteName, "--", baseRef); err != nil {
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

// RecordPRReviewContextFromFetchedBase records an already fetched and verified
// base commit in per-worktree config. It performs no network operation.
func RecordPRReviewContextFromFetchedBase(
	ctx context.Context,
	worktreePath, baseCommit string,
	meta map[string]string,
) (string, error) {
	if strings.TrimSpace(worktreePath) == "" {
		return "", fmt.Errorf("worktree path is empty")
	}
	baseCommit = strings.TrimSpace(baseCommit)
	if baseCommit == "" {
		return "", fmt.Errorf("base commit is empty")
	}
	if out, err := runGit(ctx, worktreePath, "rev-parse", "--verify", baseCommit+"^{commit}"); err != nil {
		return "", fmt.Errorf("verify review base commit: %w: %s", err, out)
	}
	if out, err := runGit(ctx, worktreePath, "config", "extensions.worktreeConfig", "true"); err != nil {
		return "", fmt.Errorf("enable worktree config: %w: %s", err, out)
	}
	if _, err := runGit(ctx, worktreePath, "config", "--worktree", "loom.reviewBase", baseCommit); err != nil {
		return "", fmt.Errorf("record review base: %w", err)
	}
	for key, value := range meta {
		value = strings.TrimSpace(value)
		if value == "" || strings.TrimSpace(key) == "" {
			continue
		}
		_, _ = runGit(ctx, worktreePath, "config", "--worktree", "loom.review"+key, value)
	}
	return baseCommit, nil
}

func runGitAnonymous(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := []string{
		"-c", "credential.helper=",
		"-c", "core.askPass=",
		"-c", "http.extraHeader=",
	}
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...) //nolint:gosec // fixed git executable; token is never present in args.
	cmd.Dir = dir
	cmd.Env = anonymousGitNetworkEnv()
	cmd.WaitDelay = 2 * time.Second
	configureGitNetworkCancellation(cmd)

	out, err := cmd.CombinedOutput()
	clearCommandEnv(cmd)
	if err != nil {
		return string(out), fmt.Errorf(
			"git %s: %w: %s",
			sanitizedGitArgs(gitArgs),
			err,
			strings.TrimSpace(string(out)),
		)
	}
	return string(out), nil
}

// ConfigureGitProcessCancellation makes a context-owned Git command terminate
// its complete process group on supported platforms. Callers that execute Git
// operations capable of spawning hooks or transport children must invoke this
// before Start or Run.
func ConfigureGitProcessCancellation(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	configureGitNetworkCancellation(cmd)
}

func sanitizedGitArgs(args []string) string {
	safe := make([]string, len(args))
	for i, arg := range args {
		safe[i] = sanitizedRemoteURL(arg)
	}
	return strings.Join(safe, " ")
}

// anonymousGitNetworkEnv starts from a narrow allowlist. An anonymous Git read
// needs process discovery, locale, proxy, and certificate settings, but no
// application, control-plane, or provider authority.
func anonymousGitNetworkEnv() []string {
	allowed := map[string]struct{}{
		"PATH": {}, "TMPDIR": {}, "TMP": {}, "TEMP": {},
		"SystemRoot": {}, "WINDIR": {}, "COMSPEC": {}, "PATHEXT": {},
		"LANG": {}, "LC_ALL": {}, "LC_CTYPE": {}, "LC_MESSAGES": {},
		"HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"http_proxy": {}, "https_proxy": {}, "no_proxy": {},
		"ALL_PROXY": {}, "all_proxy": {},
		"SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}, "CURL_CA_BUNDLE": {},
		"GIT_SSL_CAINFO": {}, "GIT_SSL_CAPATH": {},
	}
	env := make([]string, 0, len(allowed)+3)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, keep := allowed[name]; keep {
			env = append(env, entry)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
}

func clearCommandEnv(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	for i := range cmd.Env {
		cmd.Env[i] = ""
	}
	cmd.Env = nil
}

func sanitizedRemoteURL(remoteURL string) string {
	trimmed := strings.TrimSpace(remoteURL)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return trimmed
	}
	// Query strings and fragments are never valid at Loom's remote boundary,
	// but strip them here as defense in depth so a rejected manual caller
	// cannot reflect a token through an API-visible git error.
	if index := strings.IndexAny(trimmed, "?#"); index >= 0 {
		trimmed = trimmed[:index]
	}
	// Avoid reflecting URL userinfo in an API-visible clone error. Repo
	// admission itself supplies a token-free URL, but this also keeps manual
	// callers from accidentally surfacing embedded credentials.
	parts := strings.SplitN(trimmed, "://", 2)
	if len(parts) != 2 {
		return trimmed
	}
	if at := strings.LastIndex(parts[1], "@"); at >= 0 {
		return parts[0] + "://***@" + parts[1][at+1:]
	}
	return trimmed
}

func rejectRemoteURLSecrets(remoteURL string) error {
	trimmed := strings.TrimSpace(remoteURL)
	parsed, err := url.Parse(trimmed)
	lower := strings.ToLower(trimmed)
	lexicallyHTTP := strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://")
	if err != nil {
		if lexicallyHTTP {
			return fmt.Errorf("git remote HTTP(S) URL is malformed")
		}
		return nil
	}
	if !strings.EqualFold(parsed.Scheme, "http") &&
		!strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	if parsed.User != nil {
		return fmt.Errorf("git remote URL userinfo is forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("git remote URL query strings and fragments are forbidden")
	}
	return nil
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
