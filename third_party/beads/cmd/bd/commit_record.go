package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/beads"
)

// commitRecord represents a commit-to-task mapping written to commits.jsonl.
type commitRecord struct {
	TaskID    string    `json:"task_id"`
	SHA       string    `json:"sha"`
	Subject   string    `json:"subject"`
	Author    string    `json:"author"`
	Timestamp time.Time `json:"timestamp"`
	Worktree  string    `json:"worktree,omitempty"`
}

var commitRecordCmd = &cobra.Command{
	Use:     "commit-record <sha> <task-id>",
	Short: "Record a commit association with a task",
	Long: `Record that a git commit is associated with a beads task.
This command is called by the post-commit git hook to track
which commits belong to which tasks.

The record is appended to .beads/commits.jsonl.`,
	Args:   cobra.ExactArgs(2),
	Hidden: true, // Internal command, not for direct user use
	Run: func(cmd *cobra.Command, args []string) {
		sha := args[0]
		taskID := args[1]

		// Find beads directory
		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			// Silently exit - no beads workspace
			return
		}

		// Get commit metadata via git log
		subject, author, timestamp := getCommitMetadata(sha)

		// Detect worktree name
		worktree := detectWorktreeName()

		rec := commitRecord{
			TaskID:    taskID,
			SHA:       sha,
			Subject:   subject,
			Author:    author,
			Timestamp: timestamp,
			Worktree:  worktree,
		}

		// Append to commits.jsonl
		if err := appendCommitRecord(beadsDir, rec); err != nil {
			// Post-commit hook context: log warning but don't fail
			fmt.Fprintf(os.Stderr, "Warning: failed to record commit: %v\n", err)
		}
	},
}

// getCommitMetadata retrieves subject, author, and timestamp from a git commit.
func getCommitMetadata(sha string) (subject, author string, timestamp time.Time) {
	// Format: subject\nauthor\ntimestamp(ISO8601)
	// #nosec G204 - sha is a git commit hash from our own post-commit hook
	out, err := exec.Command("git", "log", "-1", "--format=%s%n%an%n%aI", sha).Output()
	if err != nil {
		return sha, "unknown", time.Now()
	}

	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 3)
	if len(lines) >= 1 {
		subject = lines[0]
	}
	if len(lines) >= 2 {
		author = lines[1]
	}
	if len(lines) >= 3 {
		if t, err := time.Parse(time.RFC3339, lines[2]); err == nil {
			timestamp = t
		} else {
			timestamp = time.Now()
		}
	} else {
		timestamp = time.Now()
	}
	return
}

// detectWorktreeName returns the worktree name if in a git worktree.
func detectWorktreeName() string {
	gitDir, err := exec.Command("git", "rev-parse", "--git-dir").Output()
	if err != nil {
		return ""
	}
	gitCommonDir, err := exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return ""
	}

	gitDirStr := strings.TrimSpace(string(gitDir))
	gitCommonDirStr := strings.TrimSpace(string(gitCommonDir))

	// If git-dir != git-common-dir, we're in a worktree
	if gitDirStr != gitCommonDirStr {
		// Worktree name is the last component of the worktree path
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		return filepath.Base(cwd)
	}
	return ""
}

// appendCommitRecord appends a JSON record to .beads/commits.jsonl.
func appendCommitRecord(beadsDir string, rec commitRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(beadsDir, "commits.jsonl")

	// #nosec G304 - path derived from beadsDir (trusted)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	// Acquire exclusive file lock to prevent corruption from concurrent agent writes
	if err := lockCommitFile(f); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer unlockCommitFile(f)

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(commitRecordCmd)
}
