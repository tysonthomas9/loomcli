package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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

// PushBranchInRepoResult performs a push (merge-into-target) and returns a structured result.
// This is the API-friendly equivalent of pushBranchInRepo. It does NOT launch an AI agent
// for conflicts — it returns conflict info for the caller to handle.
//
// Note: "push" in loom terminology means merge the source branch INTO the target branch,
// not a simple git push.
func PushBranchInRepoResult(repoPath, sourceBranch, targetBranch, remote string) (*PushResult, error) {
	r := resolveRemote(remote)

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

	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		return &PushResult{
			Success:         true,
			Message:         fmt.Sprintf("Already up to date (no new commits in %s)", sourceBranch),
			AlreadyUpToDate: true,
		}, nil
	}

	conflicts, mergeErr := mergeSource(repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			return &PushResult{
				Success:         false,
				Message:         "merge conflicts detected",
				ConflictedFiles: conflicts,
			}, nil
		}
		return nil, mergeErr
	}

	if err := GitPushRemote(repoPath, remote, targetBranch); err != nil {
		return nil, fmt.Errorf("pushing: %v", err)
	}

	return &PushResult{
		Success: true,
		Message: fmt.Sprintf("Pushed to %s/%s", r, targetBranch),
	}, nil
}

func pushBranchInRepoDetachedResult(repoPath, sourceBranch, targetBranch, remote string) (*PushResult, error) {
	r := resolveRemote(remote)
	tempBranch := fmt.Sprintf("loom-push-temp-%d", time.Now().UnixNano())

	if err := GitCheckoutDetached(repoPath, r+"/"+targetBranch); err != nil {
		return nil, fmt.Errorf("checking out %s/%s detached: %v", r, targetBranch, err)
	}
	defer func() {
		_ = GitCheckout(repoPath, sourceBranch)
	}()

	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		return &PushResult{
			Success:         true,
			Message:         fmt.Sprintf("Already up to date (no new commits in %s)", sourceBranch),
			AlreadyUpToDate: true,
		}, nil
	}

	if err := GitCreateBranchFromHead(repoPath, tempBranch); err != nil {
		return nil, fmt.Errorf("creating temp branch: %v", err)
	}
	defer func() {
		_ = GitDeleteBranch(repoPath, tempBranch, true)
	}()

	conflicts, mergeErr := mergeSource(repoPath, sourceBranch, targetBranch)
	if mergeErr != nil {
		if len(conflicts) > 0 {
			return &PushResult{
				Success:         false,
				Message:         "merge conflicts detected",
				ConflictedFiles: conflicts,
			}, nil
		}
		return nil, mergeErr
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

	title, body := generatePRInfo(repoPath, r, targetBranch, sourceBranch)

	result := execCommand(repoPath, "gh", "pr", "create",
		"--base", targetBranch,
		"--head", sourceBranch,
		"--title", title,
		"--body", body)

	if result.Err != nil {
		errMsg := result.Stderr + result.Stdout
		if strings.Contains(errMsg, "already exists") {
			url, urlErr := getExistingPRURL(repoPath, sourceBranch)
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
func ResetWorktreeResult(worktreePath, worktreeName, targetBranch string, force bool) (*ResetResult, error) {
	// Check for active agent lock
	lockInfo, running, checkErr := CheckLock(worktreePath)
	if checkErr == nil && running && !force {
		duration := time.Since(lockInfo.StartedAt).Round(time.Second)
		return nil, &LockedError{
			AgentName: lockInfo.AgentName,
			PID:       lockInfo.PID,
			Duration:  duration,
			TaskID:    lockInfo.TaskID,
		}
	}

	currentBranch, err := GetCurrentBranch(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("getting current branch: %v", err)
	}

	// Check protected branch BEFORE any destructive operations
	if isProtectedBranch(currentBranch) && !force {
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

	if err := GitPushForce(worktreePath, currentBranch); err != nil {
		return nil, fmt.Errorf("force pushing: %v", err)
	}

	return &ResetResult{
		Success:        true,
		Message:        fmt.Sprintf("Reset complete: %s is now at origin/%s", worktreeName, targetBranch),
		PreviousBranch: currentBranch,
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
	branch, err := GetCurrentBranch(worktreePath)
	if err != nil {
		branch = "(detached)"
	}

	clean, err := IsCleanWorkingTree(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("checking working tree: %v", err)
	}

	conflicted, _ := GetConflictedFiles(worktreePath)
	if conflicted == nil {
		conflicted = []string{}
	}

	changed, _ := getChangedFiles(worktreePath)
	if changed == nil {
		changed = []string{}
	}

	ahead, behind := getAheadBehind(worktreePath, branch, targetBranch)

	stashCount, _ := getStashCount(worktreePath)

	return &GitStatusSummary{
		Branch:          branch,
		TargetBranch:    targetBranch,
		IsClean:         clean,
		Ahead:           ahead,
		Behind:          behind,
		ChangedFiles:    changed,
		ConflictedFiles: conflicted,
		HasConflicts:    len(conflicted) > 0,
		StashCount:      stashCount,
	}, nil
}

// CheckGhInstalled checks if the gh CLI is available.
func CheckGhInstalled() error {
	return checkGhInstalled()
}

// getChangedFiles returns a list of changed files using git status --porcelain.
func getChangedFiles(dir string) ([]string, error) {
	output, err := RunGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) > 3 {
			files = append(files, line[3:])
		} else if len(line) > 0 {
			files = append(files, strings.TrimSpace(line))
		}
	}
	return files, nil
}

// getAheadBehind returns the ahead/behind counts relative to remote tracking branch.
func getAheadBehind(dir, localBranch, remoteBranch string) (ahead, behind int) {
	if localBranch == "" || localBranch == "(detached)" {
		return 0, 0
	}
	upstream := "origin/" + remoteBranch
	output, err := RunGitCommand(dir, "rev-list", "--left-right", "--count", localBranch+"..."+upstream)
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(strings.TrimSpace(output))
	if len(parts) == 2 {
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		return a, b
	}
	return 0, 0
}
