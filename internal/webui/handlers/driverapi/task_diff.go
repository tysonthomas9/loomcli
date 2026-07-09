package driverapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const (
	taskDiffMaxBytes      = 512 << 10
	taskDiffGitTimeout    = 15 * time.Second
	taskDiffSmallOutLimit = 64 << 10
)

// codedOpError lets narrowly-scoped driver ops expose precise machine-readable
// error classes without broadening the global domain sentinel set. It still
// unwraps its cause so tests and callers can inspect the original failure.
type codedOpError struct {
	status    int
	code      string
	message   string
	retryable bool
	details   map[string]any
	cause     error
}

func (e *codedOpError) Error() string {
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.code
}

func (e *codedOpError) Unwrap() error {
	return e.cause
}

func taskDiffError(status int, code, message string, retryable bool, details map[string]any, cause error) error {
	if status == 0 {
		status = http.StatusBadRequest
	}
	if cause == nil {
		cause = domain.ErrInvalid
	}
	return &codedOpError{
		status:    status,
		code:      code,
		message:   message,
		retryable: retryable,
		details:   details,
		cause:     cause,
	}
}

type taskDiffParams struct {
	TaskID string `json:"taskId"`
}

type taskDiffResponse struct {
	TaskID          string `json:"taskId"`
	ExternalRef     string `json:"externalRef"`
	RepoName        string `json:"repoName"`
	SourceRepo      string `json:"sourceRepo,omitempty"`
	Branch          string `json:"branch"`
	HeadSha         string `json:"headSha"`
	ResolvedHead    string `json:"resolvedHead"`
	BaseRef         string `json:"baseRef"`
	BaseSha         string `json:"baseSha"`
	Diff            string `json:"diff"`
	SizeBytes       int    `json:"sizeBytes"`
	LimitBytes      int    `json:"limitBytes"`
	EgressMechanism string `json:"egressMechanism"`
}

// taskDiff is the local-review diff source for local-branch deliveries. We do
// NOT use the patch artifact route here: local-task-runner's PR/stack/local-
// branch delivery paths return no top-level patch because the published ref is
// the delivery, and HostBridgeTaskExecutor only creates patch_artifact_id when a
// patch is returned. The robust local input is therefore the review card's
// external_ref stamp plus the workspace repo's filesystem origin.
//
// Auth is the same run-scoped verifyParent model as role-get: a trusted
// workflow run may read only cards/repos inside its workspace. The operation is
// read-only and fail-closed: malformed stamps, missing refs, non-filesystem
// origins, and over-large diffs all return precise structured error codes.
func (m *Module) taskDiff(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[taskDiffParams](body)
	if err != nil {
		return nil, err
	}
	taskID := strings.TrimSpace(params.TaskID)
	if taskID == "" {
		return nil, taskDiffError(http.StatusBadRequest, "task_diff_task_id_required", "taskId required", false, nil, domain.ErrInvalid)
	}
	issueBackend, _, err := m.issueBackendForRun(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	card, err := issueBackend.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("get issue for task diff: %w", err)
	}
	externalRef := strings.TrimSpace(card.ExternalRef)
	branch, stampedSHA, err := parseLocalBranchExternalRef(externalRef)
	if err != nil {
		return nil, err
	}

	repos, err := m.store.Repos().List(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("list repos for task diff: %w", err)
	}
	repo, err := selectTaskDiffRepo(repos, card.SourceRepo)
	if err != nil {
		return nil, err
	}
	originPath, err := filesystemOriginPath(ctx, repo.RemoteURL)
	if err != nil {
		return nil, err
	}
	if err := validateLocalBranchRef(ctx, originPath, branch); err != nil {
		return nil, err
	}
	headCommit, err := gitRevParseCommit(ctx, originPath, "refs/heads/"+branch, "task_diff_branch_missing", "local branch "+branch+" not found in filesystem origin")
	if err != nil {
		return nil, err
	}
	if !shaMatches(stampedSHA, headCommit) {
		return nil, taskDiffError(http.StatusConflict, "task_diff_sha_mismatch",
			"local branch "+branch+" points at "+shortSHA(headCommit)+" but external_ref stamped "+stampedSHA,
			false, map[string]any{"branch": branch, "head": headCommit, "stamped": stampedSHA}, domain.ErrConflict)
	}
	defaultBranch, err := taskDiffDefaultBranch(ctx, originPath, repo.DefaultBranch)
	if err != nil {
		return nil, err
	}
	baseCommit, err := gitRevParseCommit(ctx, originPath, "refs/heads/"+defaultBranch, "task_diff_base_missing", "default branch "+defaultBranch+" not found in filesystem origin")
	if err != nil {
		return nil, err
	}
	diff, err := gitDiff(ctx, originPath, baseCommit, headCommit)
	if err != nil {
		return nil, err
	}
	return taskDiffResponse{
		TaskID:          taskID,
		ExternalRef:     externalRef,
		RepoName:        repo.Name,
		SourceRepo:      card.SourceRepo,
		Branch:          branch,
		HeadSha:         stampedSHA,
		ResolvedHead:    headCommit,
		BaseRef:         defaultBranch,
		BaseSha:         baseCommit,
		Diff:            diff,
		SizeBytes:       len([]byte(diff)),
		LimitBytes:      taskDiffMaxBytes,
		EgressMechanism: "filesystem-origin",
	}, nil
}

func parseLocalBranchExternalRef(externalRef string) (string, string, error) {
	externalRef = strings.TrimSpace(externalRef)
	if externalRef == "" {
		return "", "", taskDiffError(http.StatusBadRequest, "task_diff_external_ref_missing", "task has no external_ref", false, nil, domain.ErrInvalid)
	}
	if !strings.HasPrefix(externalRef, "local-branch:") {
		return "", "", taskDiffError(http.StatusBadRequest, "task_diff_external_ref_unsupported", "external_ref is not a local-branch ref", false, nil, domain.ErrInvalid)
	}
	body := strings.TrimPrefix(externalRef, "local-branch:")
	at := strings.LastIndex(body, "@")
	if at <= 0 || at == len(body)-1 {
		return "", "", taskDiffError(http.StatusBadRequest, "task_diff_external_ref_invalid", "local-branch external_ref must be local-branch:<branch>@<sha>", false, nil, domain.ErrInvalid)
	}
	branch := strings.TrimSpace(body[:at])
	sha := strings.TrimSpace(body[at+1:])
	if branch == "" || sha == "" {
		return "", "", taskDiffError(http.StatusBadRequest, "task_diff_external_ref_invalid", "local-branch external_ref must include branch and sha", false, nil, domain.ErrInvalid)
	}
	if !isHexSHA(sha) {
		return "", "", taskDiffError(http.StatusBadRequest, "task_diff_sha_invalid", "local-branch external_ref sha must be a 7-64 character hex commit prefix", false, nil, domain.ErrInvalid)
	}
	return branch, strings.ToLower(sha), nil
}

func selectTaskDiffRepo(repos []*domain.Repo, sourceRepo string) (*domain.Repo, error) {
	available := make([]*domain.Repo, 0, len(repos))
	for _, repo := range repos {
		if repo != nil {
			available = append(available, repo)
		}
	}
	if len(available) == 0 {
		return nil, taskDiffError(http.StatusNotFound, "task_diff_repo_missing", "workspace has no repos", false, nil, domain.ErrNotFound)
	}
	sourceRepo = strings.TrimSpace(sourceRepo)
	if sourceRepo == "" {
		if len(available) == 1 {
			return available[0], nil
		}
		return nil, taskDiffError(http.StatusBadRequest, "task_diff_repo_ambiguous", "task has no source_repo and workspace has multiple repos", false, nil, domain.ErrInvalid)
	}
	want := normalizedTaskDiffRepoToken(sourceRepo)
	wantBase := normalizedTaskDiffRepoToken(repoBaseName(sourceRepo))
	for _, repo := range available {
		for _, candidate := range []string{repo.Name, firstNonEmpty(repo.SourceRepoID, repo.Name), repo.RemoteURL, repoBaseName(repo.RemoteURL)} {
			got := normalizedTaskDiffRepoToken(candidate)
			if got != "" && (got == want || got == wantBase) {
				return repo, nil
			}
		}
	}
	return nil, taskDiffError(http.StatusNotFound, "task_diff_repo_missing", "no workspace repo matches task source_repo "+sourceRepo, false, nil, domain.ErrNotFound)
}

func filesystemOriginPath(ctx context.Context, remoteURL string) (string, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_missing", "selected repo has no remote URL", false, nil, domain.ErrInvalid)
	}
	path := ""
	if strings.HasPrefix(remoteURL, "file://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil || parsed.Scheme != "file" {
			return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_invalid", "selected repo origin file URL is invalid", false, nil, domain.ErrInvalid)
		}
		if parsed.Host != "" && parsed.Host != "localhost" {
			return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_not_filesystem", "selected repo origin file URL must be local", false, nil, domain.ErrInvalid)
		}
		path = parsed.Path
	} else if filepath.IsAbs(remoteURL) {
		path = remoteURL
	} else {
		return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_not_filesystem", "selected repo origin is not a local filesystem path", false, nil, domain.ErrInvalid)
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", taskDiffError(http.StatusNotFound, "task_diff_origin_missing", "selected repo filesystem origin is missing", false, nil, err)
	}
	if !info.IsDir() {
		return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_not_git", "selected repo filesystem origin is not a directory", false, nil, domain.ErrInvalid)
	}
	if _, _, err := runTaskDiffGit(ctx, path, taskDiffSmallOutLimit, "rev-parse", "--git-dir"); err != nil {
		if isCodedTaskDiffError(err) {
			return "", err
		}
		return "", taskDiffError(http.StatusBadRequest, "task_diff_origin_not_git", "selected repo filesystem origin is not a git repository", false, nil, err)
	}
	return path, nil
}

func validateLocalBranchRef(ctx context.Context, originPath, branch string) error {
	if branch == "" || strings.HasPrefix(branch, "-") {
		return taskDiffError(http.StatusBadRequest, "task_diff_branch_invalid", "local branch name is invalid", false, nil, domain.ErrInvalid)
	}
	_, stderr, err := runTaskDiffGit(ctx, originPath, taskDiffSmallOutLimit, "check-ref-format", "--branch", branch)
	if err != nil {
		if isCodedTaskDiffError(err) {
			return err
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = "local branch name is invalid"
		}
		return taskDiffError(http.StatusBadRequest, "task_diff_branch_invalid", msg, false, nil, err)
	}
	return nil
}

func taskDiffDefaultBranch(ctx context.Context, originPath, configured string) (string, error) {
	if branch := normalizeTaskDiffBranch(configured); branch != "" {
		return branch, nil
	}
	stdout, _, err := runTaskDiffGit(ctx, originPath, taskDiffSmallOutLimit, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		if isCodedTaskDiffError(err) {
			return "", err
		}
		return "", taskDiffError(http.StatusBadRequest, "task_diff_default_branch_missing", "repo default branch is not configured and origin HEAD is not symbolic", false, nil, err)
	}
	branch := normalizeTaskDiffBranch(stdout)
	if branch == "" {
		return "", taskDiffError(http.StatusBadRequest, "task_diff_default_branch_missing", "repo default branch is empty", false, nil, domain.ErrInvalid)
	}
	return branch, nil
}

func gitRevParseCommit(ctx context.Context, originPath, ref, code, message string) (string, error) {
	stdout, _, err := runTaskDiffGit(ctx, originPath, taskDiffSmallOutLimit, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		if isCodedTaskDiffError(err) {
			return "", err
		}
		return "", taskDiffError(http.StatusNotFound, code, message, false, nil, err)
	}
	sha := strings.TrimSpace(stdout)
	if !isHexSHA(sha) {
		return "", taskDiffError(http.StatusBadRequest, "task_diff_sha_invalid", "git resolved a non-hex commit for "+ref, false, nil, domain.ErrInvalid)
	}
	return strings.ToLower(sha), nil
}

func gitDiff(ctx context.Context, originPath, baseCommit, headCommit string) (string, error) {
	stdout, stderr, err := runTaskDiffGit(ctx, originPath, taskDiffMaxBytes+1, "diff", "--binary", baseCommit+"..."+headCommit)
	if len([]byte(stdout)) > taskDiffMaxBytes {
		return "", taskDiffError(http.StatusRequestEntityTooLarge, "task_diff_too_large", fmt.Sprintf("task diff exceeds %d byte limit", taskDiffMaxBytes), false, nil, domain.ErrInvalid)
	}
	if err != nil {
		if isCodedTaskDiffError(err) {
			return "", err
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = "git diff failed"
		}
		return "", taskDiffError(http.StatusBadRequest, "task_diff_git_failed", msg, false, nil, err)
	}
	return stdout, nil
}

func runTaskDiffGit(ctx context.Context, dir string, stdoutLimit int, args ...string) (string, string, error) {
	if stdoutLimit <= 0 {
		stdoutLimit = taskDiffSmallOutLimit
	}
	gitCtx, cancel := context.WithTimeout(ctx, taskDiffGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", append([]string{"-C", dir}, args...)...) //nolint:gosec // args are fixed or validated git refs; no shell.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout limitedBuffer
	stdout.limit = stdoutLimit
	var stderr limitedBuffer
	stderr.limit = 8 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if gitCtx.Err() != nil {
		return stdout.String(), stderr.String(), taskDiffError(http.StatusGatewayTimeout, "task_diff_git_timeout", "git "+firstNonEmpty(args...)+" timed out", true, nil, gitCtx.Err())
	}
	return stdout.String(), stderr.String(), err
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

func normalizeTaskDiffBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "origin/")
	return branch
}

func normalizedTaskDiffRepoToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.Trim(value, "/")
	return value
}

func repoBaseName(value string) string {
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

func isHexSHA(value string) bool {
	if len(value) < 7 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') {
			continue
		}
		return false
	}
	return true
}

func shaMatches(stamped, resolved string) bool {
	stamped = strings.ToLower(strings.TrimSpace(stamped))
	resolved = strings.ToLower(strings.TrimSpace(resolved))
	return stamped != "" && resolved != "" && strings.HasPrefix(resolved, stamped)
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

func isCodedTaskDiffError(err error) bool {
	var coded *codedOpError
	return errors.As(err, &coded)
}
