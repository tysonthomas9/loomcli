package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// LockFileName is the name of the lock file in each worktree
const LockFileName = ".agent.lock"

// LockInfo holds information about a running agent
type LockInfo struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command"`
	StartedAt time.Time `json:"started_at"`
	AgentName string    `json:"agent_name"`
	TaskID    string    `json:"task_id,omitempty"`
	TaskTitle string    `json:"task_title,omitempty"`
}

// AcquireLock attempts to acquire an agent lock for the worktree
// Returns an error if an agent is already running
func AcquireLock(worktreePath, command, agentName string) error {
	lockPath := filepath.Join(worktreePath, LockFileName)

	// Check for existing lock
	if info, running, err := CheckLock(worktreePath); err == nil && running {
		duration := time.Since(info.StartedAt).Round(time.Second)
		return fmt.Errorf("agent already running: %s (PID %d, started %s ago)",
			info.Command, info.PID, duration)
	}

	// Create new lock
	info := LockInfo{
		PID:       os.Getpid(),
		Command:   command,
		StartedAt: time.Now(),
		AgentName: agentName,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write lock file: %w", err)
	}

	return nil
}

// ReleaseLock releases the agent lock for the worktree
func ReleaseLock(worktreePath string) error {
	lockPath := filepath.Join(worktreePath, LockFileName)

	// Only remove if the lock belongs to this process
	info, _, err := CheckLock(worktreePath)
	if err != nil {
		// Lock doesn't exist or can't be read, nothing to release
		return nil
	}

	if info == nil {
		// No lock exists, nothing to release
		return nil
	}

	if info.PID != os.Getpid() {
		// Lock belongs to another process, don't remove it
		return nil
	}

	return os.Remove(lockPath)
}

// CheckLock checks if a lock exists and if the process is still running
// Returns the lock info, whether the process is running, and any error
func CheckLock(worktreePath string) (*LockInfo, bool, error) {
	lockPath := filepath.Join(worktreePath, LockFileName)

	data, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to read lock file: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		// Invalid lock file, treat as no lock
		return nil, false, nil
	}

	// Check if the process is still running
	running := IsProcessRunning(info.PID)

	return &info, running, nil
}

// UpdateLockTask updates the lock file with task information
// This is called by Claude after picking a task to work on
func UpdateLockTask(worktreePath, taskID, taskTitle string) error {
	lockPath := filepath.Join(worktreePath, LockFileName)

	// Read existing lock
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("no active lock to update: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}

	// Update task info
	info.TaskID = taskID
	info.TaskTitle = taskTitle

	// Write back
	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// IsProcessRunning checks if a process with the given PID is still running
func IsProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds, so we need to send signal 0
	// to check if the process actually exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// GetLockStatus returns a human-readable status for a worktree's lock
// Uses explicit state words: planning, working, done, review
func GetLockStatus(worktreePath string) string {
	info, running, err := CheckLock(worktreePath)
	if err != nil || !running {
		return ""
	}

	duration := time.Since(info.StartedAt).Round(time.Second)

	if info.TaskID != "" {
		// Check actual task status
		taskStatus := getTaskStatus(info.TaskID)
		switch taskStatus {
		case "closed":
			return fmt.Sprintf("done: %s (%s)", info.TaskID, duration)
		case "needs_review":
			// Only show "review" for planning agents that completed their work
			if info.Command == "plan" {
				return fmt.Sprintf("review: %s (%s)", info.TaskID, duration)
			}
			// Implementation agents show "working" even on [Need Review] tasks
			return fmt.Sprintf("working: %s (%s)", info.TaskID, duration)
		default:
			// Use "planning" or "working" based on command
			if info.Command == "plan" {
				return fmt.Sprintf("planning: %s (%s)", info.TaskID, duration)
			}
			return fmt.Sprintf("working: %s (%s)", info.TaskID, duration)
		}
	}
	// No TaskID yet - show state with ellipsis
	if info.Command == "plan" {
		return fmt.Sprintf("planning: ... (%s)", duration)
	}
	return fmt.Sprintf("working: ... (%s)", duration)
}

// getTaskStatus returns the status of a beads task
// Returns "needs_review", "closed", "in_progress", "open", or ""
func getTaskStatus(taskID string) string {
	output, err := exec.Command("bd", "show", taskID, "--json").Output()
	if err != nil {
		return ""
	}
	var issues []struct {
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if json.Unmarshal(output, &issues) != nil || len(issues) == 0 {
		return ""
	}
	if strings.Contains(issues[0].Title, "[Need Review]") {
		return "needs_review"
	}
	return issues[0].Status
}
