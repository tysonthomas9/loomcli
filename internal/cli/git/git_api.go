package git

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// PushResult contains the structured result of a push operation.
type PushResult struct {
	Success         bool     `json:"success"`
	Message         string   `json:"message"`
	AlreadyUpToDate bool     `json:"already_up_to_date"`
	ConflictedFiles []string `json:"conflicted_files,omitempty"`
}

// PullResult contains the structured result of a pull operation.
type PullResult struct {
	Success         bool     `json:"success"`
	Message         string   `json:"message"`
	AlreadyUpToDate bool     `json:"already_up_to_date"`
	ConflictedFiles []string `json:"conflicted_files,omitempty"`
}

// PRResult contains the structured result of a PR creation.
type PRResult struct {
	URL           string `json:"url,omitempty"`
	Created       bool   `json:"created"`
	AlreadyExists bool   `json:"already_exists"`
	NoCommits     bool   `json:"no_commits"`
}

// ResetResult contains the structured result of a reset operation.
type ResetResult struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	PreviousBranch string `json:"previous_branch,omitempty"`
	Pushed         bool   `json:"pushed"`
}

// GitStatusSummary contains a comprehensive git status for a worktree.
type GitStatusSummary struct {
	Branch          string   `json:"branch"`
	TargetBranch    string   `json:"target_branch"`
	IsClean         bool     `json:"is_clean"`
	Ahead           int      `json:"ahead"`
	Behind          int      `json:"behind"`
	ChangedFiles    []string `json:"changed_files"`
	ConflictedFiles []string `json:"conflicted_files"`
	HasConflicts    bool     `json:"has_conflicts"`
	StashCount      int      `json:"stash_count"`
}

// PorcelainStatus maps a root-relative file path to the raw two-character
// git status --porcelain XY code for that path.
type PorcelainStatus map[string]string

// PushBranchInRepoResult performs a push (merge-into-target) and returns a structured result.
// This is the API-friendly equivalent of pushBranchInRepo. It does NOT launch an AI agent
// for conflicts — it returns conflict info for the caller to handle.
//
// Note: "push" in loom terminology means merge the source branch INTO the target branch,
// not a simple git push.
func PushBranchInRepoResult(repoPath, sourceBranch, targetBranch, remote string) (*PushResult, error) {
	if err := GitFetchRemote(repoPath, remote); err != nil {
		return nil, fmt.Errorf("fetching: %v", err)
	}

	stashCleanup, err := stashIfDirty(repoPath)
	if err != nil {
		return nil, fmt.Errorf("stashing changes: %v", err)
	}
	defer stashCleanup()

	restoreBranch, err := checkoutTarget(repoPath, targetBranch)
	defer restoreBranch()
	if err != nil {
		if isWorktreeConflictErr(err) {
			return pushBranchInRepoDetachedResult(repoPath, sourceBranch, targetBranch, remote)
		}
		return nil, fmt.Errorf("checking out %s: %v", targetBranch, err)
	}

	if err := GitPullRemote(repoPath, remote, targetBranch); err != nil {
		return nil, fmt.Errorf("pulling %s: %v", targetBranch, err)
	}

	return pushMergeAndPush(repoPath, sourceBranch, targetBranch, remote)
}

// pushMergeAndPush checks for new commits, merges, and pushes to remote.
func pushMergeAndPush(repoPath, sourceBranch, targetBranch, remote string) (*PushResult, error) {
	r := resolveRemote(remote)

	if upToDate, res := checkAlreadyUpToDate(repoPath, remote, targetBranch, sourceBranch); upToDate {
		return res, nil
	}

	conflicts, mergeErr := mergeSource(repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		return mergeResultToConflicts(conflicts, mergeErr)
	}

	if err := GitPushRemote(repoPath, remote, targetBranch); err != nil {
		return nil, fmt.Errorf("pushing: %v", err)
	}

	return &PushResult{
		Success: true,
		Message: fmt.Sprintf("Pushed to %s/%s", r, targetBranch),
	}, nil
}

// checkAlreadyUpToDate returns true with a success result if there are no new commits.
func checkAlreadyUpToDate(repoPath, remote, targetBranch, sourceBranch string) (bool, *PushResult) {
	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		return true, &PushResult{
			Success:         true,
			Message:         fmt.Sprintf("Already up to date (no new commits in %s)", sourceBranch),
			AlreadyUpToDate: true,
		}
	}
	return false, nil
}

// mergeResultToConflicts converts a merge error with optional conflicts into a PushResult.
func mergeResultToConflicts(conflicts []string, mergeErr error) (*PushResult, error) {
	if len(conflicts) > 0 {
		return &PushResult{
			Success:         false,
			Message:         "merge conflicts detected",
			ConflictedFiles: conflicts,
		}, nil
	}
	return nil, mergeErr
}

func pushBranchInRepoDetachedResult(repoPath, sourceBranch, targetBranch, remote string) (*PushResult, error) {
	r := resolveRemote(remote)
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	if err := GitCheckoutDetached(repoPath, r+"/"+targetBranch); err != nil {
		return nil, fmt.Errorf("checking out %s/%s detached: %v", r, targetBranch, err)
	}
	defer func() { _ = GitCheckout(repoPath, sourceBranch) }()

	if upToDate, res := checkAlreadyUpToDate(repoPath, remote, targetBranch, sourceBranch); upToDate {
		return res, nil
	}

	if err := GitCreateBranchFromHead(repoPath, tempBranch); err != nil {
		return nil, fmt.Errorf("creating temp branch: %v", err)
	}
	defer func() { _ = GitDeleteBranch(repoPath, tempBranch, true) }()

	conflicts, mergeErr := mergeSource(repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		return mergeResultToConflicts(conflicts, mergeErr)
	}

	if err := GitPushRefspec(repoPath, remote, tempBranch, targetBranch); err != nil {
		return nil, fmt.Errorf("pushing: %v", err)
	}

	return &PushResult{
		Success: true,
		Message: fmt.Sprintf("Pushed to %s/%s", r, targetBranch),
	}, nil
}

// PullRepoWorktreeResult pulls a source branch into the worktree and returns a structured result.
// Unlike pullRepoWorktree, it does NOT launch an AI agent for conflicts.
func PullRepoWorktreeResult(repoPath, currentBranch, sourceBranch, remote string) (*PullResult, error) {
	r := resolveRemote(remote)

	if err := GitFetchRemote(repoPath, remote); err != nil {
		return nil, fmt.Errorf("fetching: %v", err)
	}

	mergeMsg := fmt.Sprintf("Pull from %s\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>", sourceBranch)
	if err := GitMergeRemote(repoPath, remote, sourceBranch, mergeMsg); err != nil {
		conflicts, conflictErr := GetConflictedFiles(repoPath)
		if conflictErr != nil || len(conflicts) == 0 {
			// No conflict markers found — abort merge to restore clean state
			_ = GitMergeAbort(repoPath)
			return nil, fmt.Errorf("merge failed: %v", err)
		}
		// Abort the merge to leave the worktree in a clean state.
		// The API returns conflict info for the caller to handle.
		_ = GitMergeAbort(repoPath)
		return &PullResult{
			Success:         false,
			Message:         "merge conflicts detected",
			ConflictedFiles: conflicts,
		}, nil
	}

	if err := GitPushRemote(repoPath, remote, currentBranch); err != nil {
		return nil, fmt.Errorf("pushing: %v", err)
	}

	return &PullResult{
		Success: true,
		Message: fmt.Sprintf("Pulled from %s and pushed to %s/%s", sourceBranch, r, currentBranch),
	}, nil
}

// CreatePRResult creates a GitHub PR and returns structured result.
func CreatePRResult(repoPath, sourceBranch, targetBranch, remote string) (*PRResult, error) {
	r := resolveRemote(remote)

	if err := validateGitRef(sourceBranch); err != nil {
		return nil, err
	}
	if err := validateGitRef(targetBranch); err != nil {
		return nil, err
	}
	if err := validateGitRef(r); err != nil {
		return nil, err
	}

	if err := GitFetchRemote(repoPath, remote); err != nil {
		return nil, fmt.Errorf("fetching: %v", err)
	}

	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		return &PRResult{NoCommits: true}, nil
	}

	if err := GitPushRemote(repoPath, remote, sourceBranch); err != nil {
		return nil, fmt.Errorf("pushing branch: %v", err)
	}

	title, body := generatePRInfo(cli.GetDeps(nil), repoPath, r, targetBranch, sourceBranch)

	result := cli.GetDeps(nil).Exec.Run(repoPath, "gh", "pr", "create",
		"--base", targetBranch,
		"--head", sourceBranch,
		"--title", title,
		"--body", body)

	if result.Err != nil {
		errMsg := result.Stderr + result.Stdout
		if strings.Contains(errMsg, "already exists") {
			url, urlErr := getExistingPRURL(cli.GetDeps(nil), repoPath, sourceBranch)
			if urlErr != nil {
				return nil, urlErr
			}
			return &PRResult{URL: url, AlreadyExists: true}, nil
		}
		return nil, fmt.Errorf("creating PR: %s", strings.TrimSpace(errMsg))
	}

	prURL := strings.TrimSpace(result.Stdout)
	return &PRResult{URL: prURL, Created: true}, nil
}

// ResetWorktreeResult hard-resets a worktree to a target branch and returns structured result.
// Unlike resetWorktree, it does NOT prompt for confirmation — callers must handle that.
// It DOES check the agent lock and returns an error if locked (unless force=true).
// If push=true, force-pushes the branch to origin after resetting.
func ResetWorktreeResult(worktreePath, worktreeName, targetBranch string, force, push bool) (*ResetResult, error) {
	// Check for active agent lock
	lockInfo, running, checkErr := cli.CheckLock(worktreePath)
	if checkErr == nil && running && !force {
		duration := time.Since(lockInfo.StartedAt).Round(time.Second)
		return nil, &LockedError{
			AgentName: lockInfo.AgentName,
			PID:       lockInfo.PID,
			Duration:  duration,
			TaskID:    lockInfo.TaskID,
		}
	}

	currentBranch, err := cli.GetCurrentBranch(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %v", err)
	}

	// Check protected branch BEFORE any destructive operations (only relevant when pushing)
	if push && isProtectedBranch(currentBranch) && !force {
		return nil, fmt.Errorf("refusing to force-push to protected branch '%s'; set force=true to override", currentBranch)
	}

	if err := GitFetch(worktreePath); err != nil {
		return nil, fmt.Errorf("fetching: %v", err)
	}

	if err := GitReset(worktreePath, "HEAD"); err != nil {
		return nil, fmt.Errorf("resetting: %v", err)
	}
	if err := GitClean(worktreePath); err != nil {
		return nil, fmt.Errorf("cleaning: %v", err)
	}

	if err := GitReset(worktreePath, "origin/"+targetBranch); err != nil {
		return nil, fmt.Errorf("resetting to %s: %v", targetBranch, err)
	}

	if push {
		if err := GitPushForce(worktreePath, currentBranch); err != nil {
			return nil, fmt.Errorf("force pushing: %v", err)
		}
	}

	return &ResetResult{
		Success:        true,
		Message:        fmt.Sprintf("Reset complete: %s is now at origin/%s", worktreeName, targetBranch),
		PreviousBranch: currentBranch,
		Pushed:         push,
	}, nil
}

// LockedError indicates a worktree is locked by an active agent.
type LockedError struct {
	AgentName string
	PID       int
	Duration  time.Duration
	TaskID    string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("agent '%s' (PID %d) is actively working in worktree (running %s)", e.AgentName, e.PID, e.Duration)
}

// GetGitStatusSummary returns comprehensive git status for a worktree.
func GetGitStatusSummary(worktreePath, targetBranch string) (*GitStatusSummary, error) {
	return readGitStatusSummary(worktreePath, targetBranch, cli.RunGitCommand)
}

type statusReadRunner func(string, ...string) (string, error)

func readGitStatusSummary(dir, target string, run statusReadRunner) (*GitStatusSummary, error) {
	if target == "" {
		return nil, fmt.Errorf("git status comparison target is required")
	}
	branch, err := run(dir, "branch", "--show-current")
	if err != nil {
		return nil, fmt.Errorf("reading branch: %w", err)
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "(detached)"
	}
	porcelain, err := run(dir, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("reading changed files: %w", err)
	}
	conflicts, err := run(dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, fmt.Errorf("reading conflicted files: %w", err)
	}
	counts, err := run(dir, "rev-list", "--left-right", "--count", "HEAD...refs/remotes/origin/"+target)
	if err != nil {
		return nil, fmt.Errorf("reading ahead/behind comparison: %w", err)
	}
	ahead, behind, err := parseAheadBehind(counts)
	if err != nil {
		return nil, err
	}
	stashes, err := run(dir, "stash", "list")
	if err != nil {
		return nil, fmt.Errorf("reading stash count: %w", err)
	}
	conflicted := statusLines(conflicts)
	return &GitStatusSummary{
		Branch: branch, TargetBranch: target,
		IsClean: strings.TrimSpace(porcelain) == "",
		Ahead:   ahead, Behind: behind,
		ChangedFiles:    changedFilesFromPorcelain(porcelain),
		ConflictedFiles: conflicted, HasConflicts: len(conflicted) > 0,
		StashCount: len(statusLines(stashes)),
	}, nil
}

func statusLines(output string) []string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "\n")
}

// CheckGhInstalled checks if the gh CLI is available.
func CheckGhInstalled() error {
	return checkGhInstalled(cli.GetDeps(nil))
}

// getChangedFiles returns a list of changed files using git status --porcelain.
func getChangedFiles(dir string) ([]string, error) {
	output, err := cli.RunGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	return changedFilesFromPorcelain(output), nil
}

func changedFilesFromPorcelain(output string) []string {
	trimmed := strings.Trim(output, "\r\n")
	if trimmed == "" {
		return []string{}
	}
	lines := strings.Split(trimmed, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if len(line) >= 4 && line[2] == ' ' {
			files = append(files, line[3:])
		} else {
			files = append(files, strings.TrimSpace(line))
		}
	}
	return files
}

// GetPorcelainStatus returns root-relative changed file paths with their raw
// two-character porcelain XY status code preserved.
func GetPorcelainStatus(dir string) (PorcelainStatus, error) {
	output, err := cli.RunGitCommand(dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return ParsePorcelainStatus(output), nil
}

// ParsePorcelainStatus parses git status --porcelain output while preserving
// the two-character XY code. Renames/copies are keyed by their destination path.
func ParsePorcelainStatus(output string) PorcelainStatus {
	trimmed := strings.Trim(output, "\r\n")
	if trimmed == "" {
		return PorcelainStatus{}
	}
	lines := strings.Split(trimmed, "\n")
	status := make(PorcelainStatus, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if strings.Contains(path, " -> ") {
			parts := strings.Split(path, " -> ")
			path = parts[len(parts)-1]
		}
		status[path] = xy
	}
	return status
}

// parseAheadBehind never represents an unavailable comparison as zero counts.
func parseAheadBehind(output string) (int, int, error) {
	parts := strings.Fields(output)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid ahead/behind response %q", output)
	}
	ahead, err := strconv.Atoi(parts[0])
	if err != nil || ahead < 0 {
		return 0, 0, fmt.Errorf("invalid ahead count %q", parts[0])
	}
	behind, err := strconv.Atoi(parts[1])
	if err != nil || behind < 0 {
		return 0, 0, fmt.Errorf("invalid behind count %q", parts[1])
	}
	return ahead, behind, nil
}
