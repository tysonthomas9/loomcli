package localworkspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// TaskWorktreeRequest identifies the repository checkout a task owns. Agent
// identity is intentionally absent: every sequential role working the same
// task must resolve to the same branch and worktree.
type TaskWorktreeRequest struct {
	WorkspacePath string
	WorkspaceKey  string
	RepoName      string
	RepoPath      string
	TaskID        string
	Remote        string
	DefaultBranch string

	// DependencyTaskIDs are required code deliveries. The task begins at the
	// newest branch that contains every listed dependency; divergent inputs are
	// rejected rather than combined in an arbitrary order.
	DependencyTaskIDs []string

	// CandidateDependencyTaskIDs come from ordering-only issue dependencies.
	// A candidate contributes code only when this workspace has a published
	// task branch for it; dependencies without code remain scheduling gates.
	CandidateDependencyTaskIDs []string

	// AllowDirtyResume is reserved for resuming the same interrupted attempt.
	// A new agent stage must see only bytes represented by its input SHA/tree.
	AllowDirtyResume bool
}

// TaskWorktree is the stable local Git state owned by one task.
type TaskWorktree struct {
	Path     string
	Branch   string
	InputSHA string
	TreeSHA  string
	Lease    *TaskWorktreeLease
}

// TaskWorktreeLease fences one task checkout across the entire subprocess
// lifecycle. It is OS-backed so separate supervisors cannot write the same
// task worktree concurrently.
type TaskWorktreeLease struct {
	file *os.File
	once sync.Once
}

func (l *TaskWorktreeLease) Release() error {
	if l == nil {
		return nil
	}
	var releaseErr error
	l.once.Do(func() {
		if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
			releaseErr = err
		}
		if err := l.file.Close(); releaseErr == nil {
			releaseErr = err
		}
	})
	return releaseErr
}

// TaskWorktreeRevision is the immutable Git identity published by a completed
// task attempt. A branch remains the local transport; consumers are recorded
// against these exact object IDs rather than trusting its mutable name.
type TaskWorktreeRevision struct {
	HeadSHA string
	TreeSHA string
}

// TaskWorktreePublishRequest binds publication to the exact prepared input and
// task identity. Published refs are the durable local source of truth;
// ordinary task branches are mutable working state only.
type TaskWorktreePublishRequest struct {
	WorkspaceKey string
	RepoPath     string
	TaskID       string
	Path         string
	Branch       string
	InputSHA     string
}

type taskDeliveryCandidate struct {
	taskID string
	sha    string
}

var taskWorktreeLocks sync.Map

// TaskWorktreeManager is the local Adapter used by the daemon supervisor.
type TaskWorktreeManager struct{}

func (TaskWorktreeManager) Prepare(ctx context.Context, req TaskWorktreeRequest) (TaskWorktree, error) {
	if err := validateTaskWorktreeRequest(req); err != nil {
		return TaskWorktree{}, err
	}
	lease, err := acquireTaskWorktreeLease(req)
	if err != nil {
		return TaskWorktree{}, err
	}
	prepared, err := PrepareTaskWorktree(ctx, req)
	if err != nil {
		_ = lease.Release()
		return TaskWorktree{}, err
	}
	prepared.Lease = lease
	return prepared, nil
}

func acquireTaskWorktreeLease(req TaskWorktreeRequest) (*TaskWorktreeLease, error) {
	leasePath := filepath.Join(req.WorkspacePath, ".loom", "task-leases", safePathSegment(req.RepoName), taskPathSegment(req.TaskID)+".lock")
	if !PathContains(req.WorkspacePath, leasePath) {
		return nil, fmt.Errorf("task lease path escapes workspace: %s", leasePath)
	}
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create task lease directory: %w", err)
	}
	// #nosec G304 -- dynamic segments are sanitized and the resolved path is
	// containment-checked against the workspace before it is opened.
	file, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open task worktree lease: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("task %q worktree is owned by another active attempt: %w", req.TaskID, err)
	}
	return &TaskWorktreeLease{file: file}, nil
}

func (TaskWorktreeManager) Publish(ctx context.Context, req TaskWorktreePublishRequest) (TaskWorktreeRevision, error) {
	revision, err := SnapshotTaskWorktree(ctx, req.Path, req.Branch, req.InputSHA)
	if err != nil {
		return TaskWorktreeRevision{}, err
	}
	if err := publishTaskDelivery(ctx, req, revision.HeadSHA); err != nil {
		return TaskWorktreeRevision{}, fmt.Errorf("persist task %q delivery: %w", req.TaskID, err)
	}
	return revision, nil
}

// SnapshotTaskWorktree verifies that a producer committed its entire delivery
// and returns the exact commit and tree identities that downstream stages may
// consume. Dirty output fails closed so a routing label cannot certify bytes
// absent from the published revision.
func SnapshotTaskWorktree(ctx context.Context, path, expectedBranch, inputSHA string) (TaskWorktreeRevision, error) {
	status, err := taskWorktreeStatus(ctx, path)
	if err != nil {
		return TaskWorktreeRevision{}, fmt.Errorf("inspect task worktree: %w", err)
	}
	if dirty := strings.TrimSpace(status); dirty != "" {
		return TaskWorktreeRevision{}, fmt.Errorf("task worktree has uncommitted delivery:\n%s", dirty)
	}
	branch, err := runGit(ctx, path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return TaskWorktreeRevision{}, fmt.Errorf("resolve published task branch: %w", err)
	}
	if expectedBranch != "" && strings.TrimSpace(branch) != expectedBranch {
		return TaskWorktreeRevision{}, fmt.Errorf("task worktree is on branch %q, want %q", strings.TrimSpace(branch), expectedBranch)
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return TaskWorktreeRevision{}, fmt.Errorf("resolve published task commit: %w", err)
	}
	if inputSHA != "" {
		if _, err := runGit(ctx, path, "merge-base", "--is-ancestor", inputSHA, strings.TrimSpace(head)); err != nil {
			return TaskWorktreeRevision{}, fmt.Errorf("published task commit %s does not contain prepared input %s", strings.TrimSpace(head), inputSHA)
		}
	}
	tree, err := runGit(ctx, path, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return TaskWorktreeRevision{}, fmt.Errorf("resolve published task tree: %w", err)
	}
	return TaskWorktreeRevision{HeadSHA: strings.TrimSpace(head), TreeSHA: strings.TrimSpace(tree)}, nil
}

// PrepareTaskWorktree creates or reuses the stable branch and checkout owned by
// a task. Calls for different agents working the same task return identical Git
// state; calls for different tasks remain isolated.
func PrepareTaskWorktree(ctx context.Context, req TaskWorktreeRequest) (TaskWorktree, error) {
	if err := validateTaskWorktreeRequest(req); err != nil {
		return TaskWorktree{}, err
	}
	path := filepath.Join(
		req.WorkspacePath,
		".loom",
		"task-worktrees",
		safePathSegment(req.RepoName),
		taskPathSegment(req.TaskID),
	)
	if !PathContains(req.WorkspacePath, path) {
		return TaskWorktree{}, fmt.Errorf("task worktree path escapes workspace: %s", path)
	}
	branch := TaskBranchName(req.WorkspaceKey, req.TaskID)

	lockAny, _ := taskWorktreeLocks.LoadOrStore(path, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	select {
	case <-ctx.Done():
		return TaskWorktree{}, ctx.Err()
	default:
	}
	baseRef, deliveries, err := resolveTaskDependencyBase(ctx, req)
	if err != nil {
		return TaskWorktree{}, err
	}
	if baseRef == "" {
		err = EnsureGitWorktreeFromBranch(req.RepoPath, path, branch, req.Remote, req.DefaultBranch)
	} else {
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
			return TaskWorktree{}, fmt.Errorf("create task worktree parent: %w", mkdirErr)
		}
		err = addBranchWorktree(req.RepoPath, path, branch, baseRef)
	}
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("prepare task %q worktree: %w", req.TaskID, err)
	}
	return inspectPreparedTaskWorktree(ctx, req, path, branch, baseRef, deliveries)
}

func inspectPreparedTaskWorktree(
	ctx context.Context,
	req TaskWorktreeRequest,
	path, branch, baseRef string,
	deliveries []taskDeliveryCandidate,
) (TaskWorktree, error) {
	if !req.AllowDirtyResume {
		status, statusErr := taskWorktreeStatus(ctx, path)
		if statusErr != nil {
			return TaskWorktree{}, fmt.Errorf("inspect task %q worktree: %w", req.TaskID, statusErr)
		}
		if dirty := strings.TrimSpace(status); dirty != "" {
			return TaskWorktree{}, fmt.Errorf("task %q worktree has bytes outside its published input:\n%s", req.TaskID, dirty)
		}
	}
	head, err := runGit(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("resolve task %q HEAD: %w", req.TaskID, err)
	}
	tree, err := runGit(ctx, path, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("resolve task %q tree: %w", req.TaskID, err)
	}
	if baseRef != "" {
		if _, err := runGit(ctx, path, "merge-base", "--is-ancestor", baseRef, strings.TrimSpace(head)); err != nil {
			return TaskWorktree{}, fmt.Errorf("task %q input is stale: required delivery %s is not an ancestor of %s", req.TaskID, baseRef, strings.TrimSpace(head))
		}
	}
	if err := verifyAndRecordTaskInputs(ctx, req, deliveries, strings.TrimSpace(head)); err != nil {
		return TaskWorktree{}, err
	}
	if err := verifyTaskPublicationState(ctx, req, strings.TrimSpace(head)); err != nil {
		return TaskWorktree{}, err
	}
	return TaskWorktree{
		Path:     path,
		Branch:   branch,
		InputSHA: strings.TrimSpace(head),
		TreeSHA:  strings.TrimSpace(tree),
	}, nil
}

func verifyTaskPublicationState(ctx context.Context, req TaskWorktreeRequest, head string) error {
	startRef := taskStartRef(req.WorkspaceKey, req.TaskID)
	startSHA, startErr := runGit(ctx, req.RepoPath, "rev-parse", "--verify", startRef+"^{commit}")
	if startErr != nil {
		if _, err := runGit(ctx, req.RepoPath, "update-ref", startRef, head); err != nil {
			return fmt.Errorf("record task %q initial revision: %w", req.TaskID, err)
		}
		startSHA = head
	}
	if !req.AllowDirtyResume {
		published, publishedErr := resolveTaskDelivery(ctx, req.RepoPath, req.WorkspaceKey, req.TaskID)
		if publishedErr == nil && published != head {
			return fmt.Errorf("task %q checkout %s does not match its latest published delivery %s", req.TaskID, head, published)
		}
		if publishedErr != nil && strings.TrimSpace(startSHA) != head {
			return fmt.Errorf("task %q has committed working state without a published delivery; recovery is required", req.TaskID)
		}
	}
	return nil
}

// taskWorktreeStatus excludes only Loom's root coordination files. They
// are process state, never delivery bytes, and intentionally survive between
// sequential roles sharing a task checkout. Every other tracked or untracked
// path remains part of the fail-closed cleanliness check.
func taskWorktreeStatus(ctx context.Context, path string) (string, error) {
	return runGit(ctx, path,
		"status", "--porcelain", "--untracked-files=all", "--", ".",
		":(exclude).agent.lock", ":(exclude).agent.lock.flock", ":(exclude).agent.checkpoint.json",
	)
}

func resolveTaskDependencyBase(ctx context.Context, req TaskWorktreeRequest) (string, []taskDeliveryCandidate, error) {
	if len(req.DependencyTaskIDs) == 0 && len(req.CandidateDependencyTaskIDs) == 0 {
		return "", nil, nil
	}
	candidates := make([]taskDeliveryCandidate, 0, len(req.DependencyTaskIDs)+len(req.CandidateDependencyTaskIDs))
	seen := make(map[string]struct{}, cap(candidates))
	for _, taskID := range req.DependencyTaskIDs {
		out, err := resolveTaskDelivery(ctx, req.RepoPath, req.WorkspaceKey, taskID)
		if err != nil {
			return "", nil, fmt.Errorf("required task delivery %q is unavailable: %w", taskID, err)
		}
		candidates = append(candidates, taskDeliveryCandidate{taskID: taskID, sha: out})
		seen[taskID] = struct{}{}
	}
	for _, taskID := range req.CandidateDependencyTaskIDs {
		if _, ok := seen[taskID]; ok {
			continue
		}
		out, err := resolveTaskDelivery(ctx, req.RepoPath, req.WorkspaceKey, taskID)
		if err != nil {
			continue
		}
		candidates = append(candidates, taskDeliveryCandidate{taskID: taskID, sha: out})
		seen[taskID] = struct{}{}
	}
	if len(candidates) == 0 {
		return "", nil, nil
	}
	for _, possibleBase := range candidates {
		containsAll := true
		for _, required := range candidates {
			if required.sha == possibleBase.sha {
				continue
			}
			if _, err := runGit(ctx, req.RepoPath, "merge-base", "--is-ancestor", required.sha, possibleBase.sha); err != nil {
				containsAll = false
				break
			}
		}
		if containsAll {
			return possibleBase.sha, candidates, nil
		}
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.taskID)
	}
	return "", nil, fmt.Errorf("required task deliveries are divergent and need integration: %s", strings.Join(ids, ", "))
}

func verifyAndRecordTaskInputs(ctx context.Context, req TaskWorktreeRequest, deliveries []taskDeliveryCandidate, head string) error {
	expected := make(map[string]taskDeliveryCandidate, len(deliveries))
	for _, delivery := range deliveries {
		expected[taskInputRef(req.WorkspaceKey, req.TaskID, delivery.taskID)] = delivery
	}
	recordedInputs, err := taskInputReceipts(ctx, req.RepoPath, req.WorkspaceKey, req.TaskID)
	if err != nil {
		return err
	}
	for ref := range recordedInputs {
		if _, ok := expected[ref]; !ok {
			return fmt.Errorf("task %q dependency set changed: recorded input %s is no longer required", req.TaskID, ref)
		}
	}
	for _, delivery := range deliveries {
		ref := taskInputRef(req.WorkspaceKey, req.TaskID, delivery.taskID)
		recorded, exists := recordedInputs[ref]
		switch {
		case exists && recorded != delivery.sha:
			return fmt.Errorf("task %q input is stale: dependency %q changed from %s to %s", req.TaskID, delivery.taskID, recorded, delivery.sha)
		case !exists:
			if _, ancestorErr := runGit(ctx, req.RepoPath, "merge-base", "--is-ancestor", delivery.sha, head); ancestorErr != nil {
				return fmt.Errorf("task %q input does not contain published dependency %q at %s", req.TaskID, delivery.taskID, delivery.sha)
			}
			if _, updateErr := runGit(ctx, req.RepoPath, "update-ref", ref, delivery.sha); updateErr != nil {
				return fmt.Errorf("record task %q input dependency %q: %w", req.TaskID, delivery.taskID, updateErr)
			}
		}
	}
	return nil
}

func taskInputReceipts(ctx context.Context, repoPath, workspaceKey, taskID string) (map[string]string, error) {
	prefix := taskInputPrefix(workspaceKey, taskID)
	out, err := runGit(ctx, repoPath, "for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return nil, fmt.Errorf("list task %q input receipts: %w", taskID, err)
	}
	receipts := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], prefix) {
			return nil, fmt.Errorf("invalid task input receipt %q", line)
		}
		receipts[parts[0]] = parts[1]
	}
	return receipts, nil
}

// publishTaskDelivery atomically verifies that every dependency is still at
// the exact revision consumed by this attempt while activating its output.
// A dependency cannot advance in the gap between validation and publication.
func publishTaskDelivery(ctx context.Context, req TaskWorktreePublishRequest, head string) error {
	receipts, err := taskInputReceipts(ctx, req.RepoPath, req.WorkspaceKey, req.TaskID)
	if err != nil {
		return err
	}
	inputPrefix := taskInputPrefix(req.WorkspaceKey, req.TaskID)
	deliveryPrefix := taskDeliveryPrefix(req.WorkspaceKey)
	var transaction strings.Builder
	transaction.WriteString("start\n")
	for inputRef, sha := range receipts {
		dependencySegment := strings.TrimPrefix(inputRef, inputPrefix)
		if dependencySegment == "" || strings.Contains(dependencySegment, "/") {
			return fmt.Errorf("invalid dependency receipt ref %q", inputRef)
		}
		fmt.Fprintf(&transaction, "verify %s %s\n", inputRef, sha)
		fmt.Fprintf(&transaction, "verify %s%s %s\n", deliveryPrefix, dependencySegment, sha)
	}
	fmt.Fprintf(&transaction, "update %s %s\nprepare\ncommit\n", taskDeliveryRef(req.WorkspaceKey, req.TaskID), head)
	cmd := exec.CommandContext(ctx, "git", "update-ref", "--stdin") //nolint:gosec // fixed Git executable and Loom-owned refs.
	cmd.Dir = req.RepoPath
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdin = strings.NewReader(transaction.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("atomically publish verified delivery: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func resolveTaskDelivery(ctx context.Context, repoPath, workspaceKey, taskID string) (string, error) {
	out, err := runGit(ctx, repoPath, "rev-parse", "--verify", taskDeliveryRef(workspaceKey, taskID)+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func taskDeliveryRef(workspaceKey, taskID string) string {
	return "refs/loom/deliveries/" + taskPathSegment(workspaceKey) + "/" + taskPathSegment(taskID)
}

func taskStartRef(workspaceKey, taskID string) string {
	return "refs/loom/starts/" + taskPathSegment(workspaceKey) + "/" + taskPathSegment(taskID)
}

func taskInputRef(workspaceKey, taskID, dependencyTaskID string) string {
	return taskInputPrefix(workspaceKey, taskID) + taskPathSegment(dependencyTaskID)
}

func taskInputPrefix(workspaceKey, taskID string) string {
	return "refs/loom/inputs/" + taskPathSegment(workspaceKey) + "/" + taskPathSegment(taskID) + "/"
}

func taskDeliveryPrefix(workspaceKey string) string {
	return "refs/loom/deliveries/" + taskPathSegment(workspaceKey) + "/"
}

// TaskBranchName returns the stable branch owned by a task. A digest keeps
// identifiers that sanitize to the same text from colliding.
func TaskBranchName(workspaceKey, taskID string) string {
	return "loom/task/" + taskPathSegment(workspaceKey) + "/" + taskPathSegment(taskID)
}

func validateTaskWorktreeRequest(req TaskWorktreeRequest) error {
	for name, value := range map[string]string{
		"workspace path": req.WorkspacePath,
		"workspace key":  req.WorkspaceKey,
		"repo name":      req.RepoName,
		"repo path":      req.RepoPath,
		"task id":        req.TaskID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	return nil
}

func taskPathSegment(value string) string {
	clean := safePathSegment(value)
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", clean, digest[:5])
}
