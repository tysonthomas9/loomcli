// Package localworkspace contains machine-local workspace filesystem helpers.
package localworkspace

import (
	"bytes"
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
	"github.com/tysonthomas9/loomcli/internal/gitauth"
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
	return CloneRepoToWithCredentials(ctx, cloneURL, targetPath, nil)
}

// CloneRepoToWithCredentials clones cloneURL into targetPath, resolving an
// ephemeral credential only when source recognizes the remote. The remote URL
// written by git remains the original token-free URL.
func CloneRepoToWithCredentials(ctx context.Context, cloneURL, targetPath string, source gitauth.Source) error {
	if err := rejectRemoteURLSecrets(cloneURL); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create clone parent directory: %w", err)
	}
	_, err := runGitForRemote(ctx, filepath.Dir(targetPath), source, cloneURL, "clone", "--", cloneURL, targetPath)
	if err != nil {
		return fmt.Errorf("git clone failed for %s: %w", sanitizedRemoteURL(cloneURL), err)
	}
	return nil
}

// EnsureGitWorktree creates a git worktree at targetPath from repoPath.
func EnsureGitWorktree(repoPath, targetPath, branchName string) error {
	return EnsureGitWorktreeFromBranch(repoPath, targetPath, branchName, "", "")
}

// EnsureDetachedGitWorktreeFromBranch creates a detached git worktree at
// targetPath from the latest available remote/defaultBranch ref. Existing
// worktrees are left untouched.
func EnsureDetachedGitWorktreeFromBranch(repoPath, targetPath, remoteName, defaultBranch string) error {
	return EnsureDetachedGitWorktreeFromBranchWithCredentials(
		context.Background(), repoPath, targetPath, remoteName, defaultBranch, nil,
	)
}

// EnsureDetachedGitWorktreeFromBranchWithCredentials is the credential-aware
// form used by serve-owned task runners for private admitted repositories.
func EnsureDetachedGitWorktreeFromBranchWithCredentials(
	ctx context.Context,
	repoPath, targetPath, remoteName, defaultBranch string,
	source gitauth.Source,
) error {
	if _, err := os.Stat(filepath.Join(targetPath, ".git")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("creating worktree parent: %w", err)
	}

	baseRef, err := resolveFreshBaseRef(ctx, repoPath, remoteName, defaultBranch, source)
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
	return EnsureDetachedGitWorktreeAtPRHeadWithCredentials(
		ctx, repoPath, targetPath, remoteName, prNumber, headSHA, nil,
	)
}

// EnsureDetachedGitWorktreeAtPRHeadWithCredentials is the credential-aware
// form used by the UI PR reviewer for private admitted repositories.
func EnsureDetachedGitWorktreeAtPRHeadWithCredentials(
	ctx context.Context,
	repoPath, targetPath, remoteName string,
	prNumber int,
	headSHA string,
	source gitauth.Source,
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
	if _, err = runGitRemote(ctx, repoPath, remoteName, source, "fetch", remoteName, fetchRef); err != nil {
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
	return EnsureGitWorktreeFromBranchWithCredentials(
		context.Background(), repoPath, targetPath, branchName, remoteName, defaultBranch, nil,
	)
}

// EnsureGitWorktreeFromBranchWithCredentials is the credential-aware form for
// branch worktrees whose base must be refreshed from a private remote.
func EnsureGitWorktreeFromBranchWithCredentials(
	ctx context.Context,
	repoPath, targetPath, branchName, remoteName, defaultBranch string,
	source gitauth.Source,
) error {
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

	baseRef, err := resolveFreshBaseRef(ctx, repoPath, remoteName, defaultBranch, source)
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
	source gitauth.Source,
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
		if _, err := runGitRemote(ctx, repoPath, remoteName, source, "fetch", remoteName, defaultBranch); err != nil {
			if _, localErr := runGit(context.Background(), repoPath, "rev-parse", "--verify", defaultBranch); localErr == nil {
				return defaultBranch, nil
			}
			return "", fmt.Errorf("fetch base branch %q from %q: %w", defaultBranch, remoteName, err)
		}
		return remoteName + "/" + defaultBranch, nil
	}

	if _, err := runGit(ctx, repoPath, "fetch", remoteName, defaultBranch); err == nil {
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
	return RecordPRReviewContextWithCredentials(
		ctx, worktreePath, remoteName, baseRef, meta, nil,
	)
}

// RecordPRReviewContextWithCredentials is the credential-aware form used by
// the UI reviewer after checking out a private pull request.
func RecordPRReviewContextWithCredentials(
	ctx context.Context,
	worktreePath, remoteName, baseRef string,
	meta map[string]string,
	source gitauth.Source,
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
	if out, err := runGitRemote(ctx, worktreePath, remoteName, source, "fetch", remoteName, "--", baseRef); err != nil {
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

// The helper command is supplied through ephemeral `git -c` arguments. The
// credential itself is available only in the child git environment, never in
// argv, a remote URL, or repository/global config.
//
//nolint:gosec // G101: env variable name and helper template, not credential material.
const (
	gitHTTPPasswordEnv = "LOOM_PR_GIT_PASSWORD"
	gitHTTPHelper      = `!f() { test "$1" = get || exit 0; protocol= host=; while IFS='=' read -r key value; do case "$key" in protocol) protocol=$value ;; host) host=$value ;; esac; done; case "$protocol" in [hH][tT][tT][pP][sS]) ;; *) exit 0 ;; esac; case "$host" in [gG][iI][tT][hH][uU][bB].[cC][oO][mM]|[gG][iI][tT][hH][uU][bB].[cC][oO][mM]:443) ;; *) exit 0 ;; esac; printf '%s\n' username=x-access-token "password=$LOOM_PR_GIT_PASSWORD"; }; f`
)

func runGitRemote(
	ctx context.Context,
	repoPath, remoteName string,
	source gitauth.Source,
	args ...string,
) (string, error) {
	if source == nil {
		return runGit(ctx, repoPath, args...)
	}
	remoteURL, err := runGit(ctx, repoPath, "remote", "get-url", remoteName)
	if err != nil {
		return runGit(ctx, repoPath, args...)
	}
	return runGitForRemote(ctx, repoPath, source, strings.TrimSpace(remoteURL), args...)
}

func runGitForRemote(
	ctx context.Context,
	dir string,
	source gitauth.Source,
	remoteURL string,
	args ...string,
) (string, error) {
	if err := rejectRemoteURLSecrets(remoteURL); err != nil {
		return "", err
	}
	if source == nil {
		return runGit(ctx, dir, args...)
	}
	if err := validateCredentialedGitOperation(args); err != nil {
		return "", err
	}

	// Try the network operation without any credential helper first. Public
	// repositories must remain usable even when Settings contains an expired
	// or otherwise invalid GitHub token, and a public read does not needlessly
	// disclose that token. The operations routed here are read-only
	// clone/fetch operations; a private remote's authentication failure is
	// therefore safe to retry with the just-in-time credential.
	anonymousOut, anonymousErr := runGitAnonymous(ctx, dir, args...)
	if anonymousErr == nil {
		return anonymousOut, nil
	}
	if ctx.Err() != nil {
		return anonymousOut, anonymousErr
	}

	credential, err := source.Resolve(ctx, remoteURL)
	if err != nil {
		return "", err
	}
	if credential == nil {
		// Preserve the Source contract for installations that intentionally
		// rely on an existing machine-local credential helper. The first pass
		// above was strictly anonymous; this fallback is reached only after it
		// failed and Settings supplied no Loom-managed credential.
		return runGit(ctx, dir, args...)
	}
	defer credential.Close()
	return runGitWithCredential(ctx, dir, credential, args...)
}

func validateCredentialedGitOperation(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("credentialed git operation is empty")
	}
	switch args[0] {
	case "clone", "fetch", "ls-remote":
		return nil
	default:
		return fmt.Errorf("credentialed git operation %q is not a read-only clone/fetch", args[0])
	}
}

func runGitAnonymous(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := []string{
		"-c", "credential.helper=",
		"-c", "core.askPass=",
		"-c", "http.extraHeader=",
	}
	gitArgs = append(gitArgs, args...)
	return runGitNetworkCommand(ctx, dir, nil, gitArgs...)
}

func runGitWithCredential(
	ctx context.Context,
	dir string,
	credential *gitauth.Credential,
	args ...string,
) (string, error) {
	gitArgs := []string{
		"-c", "credential.helper=",
		"-c", "credential.helper=" + gitHTTPHelper,
		"-c", "core.askPass=",
		"-c", "http.extraHeader=",
	}
	gitArgs = append(gitArgs, args...)
	return runGitNetworkCommand(ctx, dir, credential, gitArgs...)
}

func runGitNetworkCommand(
	ctx context.Context,
	dir string,
	credential *gitauth.Credential,
	gitArgs ...string,
) (string, error) {
	if credential != nil {
		if err := requireGitCredentialProcessIsolation(); err != nil {
			return "", err
		}
	}
	cmd := exec.CommandContext(ctx, "git", gitArgs...) //nolint:gosec // fixed git executable; token is never present in args.
	cmd.Dir = dir
	cmd.Env = gitNetworkEnv(credential)
	cmd.WaitDelay = 2 * time.Second
	configureGitNetworkCancellation(cmd)

	rawOut, err := cmd.CombinedOutput()
	out := redactCredential(rawOut, credential)
	zeroBytes(rawOut)
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

func sanitizedGitArgs(args []string) string {
	safe := make([]string, len(args))
	for i, arg := range args {
		safe[i] = sanitizedRemoteURL(arg)
	}
	return strings.Join(safe, " ")
}

// gitNetworkEnv deliberately starts from a narrow allowlist rather than the
// agent subprocess allowlist. A git clone/fetch needs process discovery,
// locale, temporary-directory, proxy, and certificate settings; it does not
// need AI-provider keys, control-plane authority, or GITHUB_TOKEN.
func gitNetworkEnv(credential *gitauth.Credential) []string {
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
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	if credential != nil {
		// exec.Cmd requires environment entries as immutable Go strings, so
		// this conversion cannot be reliably overwritten in place. Keep the
		// string scoped to one Cmd, clear Cmd.Env immediately after Wait, and
		// let Credential.Close overwrite the mutable source bytes.
		env = append(env, gitHTTPPasswordEnv+"="+string(credential.Password))
	}
	return env
}

func redactCredential(output []byte, credential *gitauth.Credential) []byte {
	if credential == nil || len(credential.Password) == 0 {
		return bytes.Clone(output)
	}
	return bytes.ReplaceAll(bytes.Clone(output), credential.Password, []byte("***"))
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func clearCommandEnv(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// Drop every Cmd-held reference promptly. The strings themselves are
	// immutable (documented in gitNetworkEnv), but keeping them reachable
	// through a completed Cmd would unnecessarily extend their lifetime.
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
