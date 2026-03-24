package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CheckpointFileName is the name of the checkpoint file in each lock directory.
const CheckpointFileName = ".agent.checkpoint.json"

// maxDiffBytes is the maximum size of the git diff stored in a checkpoint.
const maxDiffBytes = 4096

// Checkpoint captures the state of an agent's progress when it exits non-zero.
// This allows the next agent session to continue from where the previous one left off.
type Checkpoint struct {
	AgentName  string    `json:"agent_name"`
	TaskID     string    `json:"task_id"`
	EpicID     string    `json:"epic_id,omitempty"`
	GitDiff    string    `json:"git_diff"`
	ExitCode   int       `json:"exit_code"`
	ErrorClass string    `json:"error_class,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// SaveCheckpoint atomically writes a checkpoint file to the lock directory.
// It writes to a temp file first, then renames to prevent partial reads.
func SaveCheckpoint(lockDir string, cp *Checkpoint) error {
	cpPath := filepath.Join(lockDir, CheckpointFileName)
	tmpPath := cpPath + ".tmp"

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write checkpoint tmp: %w", err)
	}

	if err := os.Rename(tmpPath, cpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename checkpoint: %w", err)
	}

	return nil
}

// LoadCheckpoint reads a checkpoint file from the lock directory.
// Returns (nil, nil) if the file does not exist.
func LoadCheckpoint(lockDir string) (*Checkpoint, error) {
	cpPath := filepath.Join(lockDir, CheckpointFileName)

	data, err := os.ReadFile(cpPath) //nolint:gosec // path is built from trusted ResolveLockDir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("failed to parse checkpoint: %w", err)
	}

	return &cp, nil
}

// ClearCheckpoint removes the checkpoint file. Ignores os.ErrNotExist.
func ClearCheckpoint(lockDir string) error {
	cpPath := filepath.Join(lockDir, CheckpointFileName)
	err := os.Remove(cpPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove checkpoint: %w", err)
	}
	return nil
}

// captureGitDiff runs `git diff HEAD` in the worktree and returns the output
// truncated to maxBytes. In workspace mode, iterates all repos and concatenates
// diffs with repo name headers.
func captureGitDiff(resolver *Resolver, worktreePath string, maxBytes int) string {
	if resolver == nil {
		resolver = getDefaultResolver()
	}
	if resolver.Mode() == ModeWorkspace {
		worktrees, err := resolver.DiscoverWorktrees()
		if err == nil && len(worktrees) > 0 {
			return captureMultiRepoDiff(worktrees, maxBytes)
		}
	}
	return captureSingleRepoDiff(worktreePath, maxBytes)
}

// captureSingleRepoDiff captures git diff HEAD from a single repo.
func captureSingleRepoDiff(repoPath string, maxBytes int) string {
	output, err := RunGitCommand(repoPath, "diff", "HEAD")
	if err != nil {
		return ""
	}
	output = strings.TrimSpace(output)
	return truncateDiff(output, maxBytes)
}

// captureMultiRepoDiff captures diffs across multiple repos with headers.
func captureMultiRepoDiff(worktrees []WorktreeInfo, maxBytes int) string {
	var sb strings.Builder
	for _, wt := range worktrees {
		output, err := RunGitCommand(wt.Path, "diff", "HEAD")
		if err != nil {
			continue
		}
		output = strings.TrimSpace(output)
		if output == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- repo: %s ---\n", wt.Name))
		sb.WriteString(output)
		sb.WriteString("\n")
	}
	return truncateDiff(sb.String(), maxBytes)
}

// truncateDiff truncates a diff string to maxBytes, appending a notice if truncated.
func truncateDiff(diff string, maxBytes int) string {
	if len(diff) <= maxBytes {
		return diff
	}
	notice := fmt.Sprintf("\n... (truncated, full diff was %d bytes)", len(diff))
	return diff[:maxBytes-len(notice)] + notice
}
