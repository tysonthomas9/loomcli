package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
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
	// StackID names the Loom Git stack the PR joined (per repository for a
	// cross-repo lead), for loom merge.
	StackID string `json:"stack_id,omitempty"`
}

// ResetResult contains the structured result of a reset operation.
type ResetResult struct {
	Success        bool               `json:"success"`
	Message        string             `json:"message"`
	PreviousBranch string             `json:"previous_branch,omitempty"`
	Pushed         bool               `json:"pushed"`
	CaptureRef     string             `json:"capture_ref,omitempty"`
	Ignored        []ResetIgnoredFile `json:"ignored,omitempty"`
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

// PullRepoWorktreeResult pulls a source branch into the worktree and returns a structured result.
// Unlike pullRepoWorktree, it does NOT launch an AI agent for conflicts.
func PullRepoWorktreeResult(repoPath, currentBranch, sourceBranch, remote string) (*PullResult, error) {
	_ = currentBranch
	result, err := pull.PullLocal(context.Background(), repoPath, remote, sourceBranch, uuid.NewString())
	if err != nil {
		if len(result.Paths) > 0 {
			return &PullResult{Message: err.Error(), ConflictedFiles: result.Paths}, nil
		}
		return nil, err
	}
	return &PullResult{Success: true, Message: "Restacked working area onto recorded trunk",
		AlreadyUpToDate: result.AlreadyUpToDate}, nil
}

// CreatePRResult publishes an approved change through the host publisher.
func CreatePRResult(ctx context.Context, workspace, lead, change string) (*PRResult, error) {
	mode, err := publish.DeliveryModeLocal(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if mode == "stack" {
		result, err := publish.PublishLeadChangeLocal(ctx, workspace, lead, change)
		if err != nil {
			return nil, err
		}
		return &PRResult{URL: result.PRURL, Created: !result.AlreadyExists, AlreadyExists: result.AlreadyExists,
			StackID: result.StackID}, nil
	}
	result, err := publish.PublishLocal(ctx, workspace, lead, change)
	if err != nil {
		return nil, err
	}
	return &PRResult{URL: result.PRURL, Created: !result.AlreadyExists, AlreadyExists: result.AlreadyExists}, nil
}

// ResetWorktreeResult hard-resets a worktree to a target branch and returns structured result.
// Unlike resetWorktree, it does NOT prompt for confirmation — callers must handle that.
// It checks protection and captures before any destructive step.
// push is rejected for callers still using the old API.
func ResetWorktreeResult(worktreePath, worktreeName, targetBranch string, force, push bool) (*ResetResult, error) {
	if push {
		return nil, fmt.Errorf("reset cannot push")
	}
	prepared, err := prepareReset(worktreePath, targetBranch, force)
	if err != nil {
		return nil, err
	}
	if err := finishReset(worktreePath, targetBranch); err != nil {
		return nil, err
	}

	return &ResetResult{
		Success:        true,
		Message:        fmt.Sprintf("Reset complete: %s is now at origin/%s", worktreeName, targetBranch),
		PreviousBranch: prepared.branch,
		Pushed:         false,
		CaptureRef:     prepared.capture.Ref,
		Ignored:        ignoredResetEntries(prepared.capture),
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
	branch, err := cli.GetCurrentBranch(worktreePath)
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
	result := cli.GetDeps(nil).Exec.Run(".", "gh", "--version")
	if result.Err != nil {
		return fmt.Errorf("gh CLI unavailable: %w", result.Err)
	}
	return nil
}

// getChangedFiles returns a list of changed files using git status --porcelain.
func getChangedFiles(dir string) ([]string, error) {
	output, err := cli.RunGitCommand(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	trimmed := strings.Trim(output, "\r\n")
	if trimmed == "" {
		return nil, nil
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
	return files, nil
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

// getAheadBehind returns the ahead/behind counts relative to remote tracking branch.
func getAheadBehind(dir, localBranch, remoteBranch string) (ahead, behind int) {
	if localBranch == "" || localBranch == "(detached)" {
		return 0, 0
	}
	upstream := "origin/" + remoteBranch
	output, err := cli.RunGitCommand(dir, "rev-list", "--left-right", "--count", localBranch+"..."+upstream)
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
