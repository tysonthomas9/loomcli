package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// LockFileName is the name of the lock file in each worktree
const LockFileName = ".agent.lock"

// Lock states for auto mode tracking
const (
	StateActive = "active" // Claude is executing (planning/working)
	StateIdle   = "idle"   // Polling for tasks, no work available
)

// LockInfo holds information about a running agent
type LockInfo struct {
	PID             int       `json:"pid"`
	Command         string    `json:"command"`
	StartedAt       time.Time `json:"started_at"`
	AgentName       string    `json:"agent_name"`
	TaskID          string    `json:"task_id,omitempty"`
	TaskTitle       string    `json:"task_title,omitempty"`
	TaskStartedAt   time.Time `json:"task_started_at,omitempty"`   // Per-task timing (reset when new task claimed)
	State           string    `json:"state,omitempty"`             // Execution state (active/idle) for auto mode
	ClaudeSessionID string    `json:"claude_session_id,omitempty"` // Claude CLI session UUID for resume
	Workspace       string    `json:"workspace,omitempty"`         // Workspace name when in workspace mode
}

// ResolveLockDir determines the correct directory for the lock file.
// Each worktree gets its own lock file so multiple agents in the same
// workspace don't collide. Returns the path unchanged.
func ResolveLockDir(path string) string {
	return path
}

// resolveWorkspaceName returns the workspace name for the given path, or empty string if not in a workspace.
func resolveWorkspaceName(path string) string {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return ""
	}

	cleanPath := filepath.Clean(path)

	for name, ws := range cfg.Workspaces {
		if ws.Path == "" {
			continue
		}
		wsPath := filepath.Clean(ws.Path)

		if cleanPath == wsPath || strings.HasPrefix(cleanPath, wsPath+string(filepath.Separator)) {
			return name
		}

		for _, repo := range ws.Repos {
			if repo.Path == "" {
				continue
			}
			repoPath := repo.Path
			if !filepath.IsAbs(repoPath) {
				repoPath = filepath.Join(wsPath, repoPath)
			}
			repoPath = filepath.Clean(repoPath)
			if cleanPath == repoPath || strings.HasPrefix(cleanPath, repoPath+string(filepath.Separator)) {
				return name
			}
		}
	}

	return ""
}

// acquireLockRetry handles the retry loop when the initial O_EXCL lock creation
// fails because a lock file already exists. It re-evaluates the lock on each
// attempt to handle TOCTOU races (e.g., another process removing a stale lock
// and creating a new one between our check and create).
func acquireLockRetry(worktreePath, lockPath string) (*os.File, error) {
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			n, _ := rand.Int(rand.Reader, big.NewInt(40))
			time.Sleep(time.Duration(10+n.Int64()) * time.Millisecond)
		}

		existingInfo, running, checkErr := CheckLock(worktreePath)
		if checkErr != nil {
			return nil, fmt.Errorf("failed to check existing lock: %w", checkErr)
		}

		if existingInfo == nil {
			file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				return file, nil
			}
			if !os.IsExist(err) {
				return nil, fmt.Errorf("failed to create lock file: %w", err)
			}
			continue
		}

		if running {
			duration := time.Since(existingInfo.StartedAt).Round(time.Second)
			return nil, fmt.Errorf("agent already running: %s (PID %d, started %s ago)",
				existingInfo.Command, existingInfo.PID, duration)
		}

		os.Remove(lockPath)
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			return file, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("failed to create lock file: %w", err)
		}
	}
	return nil, fmt.Errorf("failed to acquire lock after %d attempts (concurrent lock contention)", maxRetries)
}

// AcquireLock attempts to acquire an agent lock for the worktree
// Returns an error if an agent is already running
// Uses atomic file creation (O_EXCL) to prevent race conditions
func AcquireLock(worktreePath, command, agentName string) error {
	lockDir := ResolveLockDir(worktreePath)
	lockPath := filepath.Join(lockDir, LockFileName)

	info := LockInfo{
		PID:       os.Getpid(),
		Command:   command,
		AgentName: agentName,
		StartedAt: time.Now(),
		Workspace: resolveWorkspaceName(worktreePath),
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("failed to create lock file: %w", err)
		}
		file, err = acquireLockRetry(worktreePath, lockPath)
		if err != nil {
			return err
		}
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		os.Remove(lockPath)
		return fmt.Errorf("failed to write lock info: %w", err)
	}

	return nil
}

// ReleaseLock releases the agent lock for the worktree
func ReleaseLock(worktreePath string) error {
	lockDir := ResolveLockDir(worktreePath)
	lockPath := filepath.Join(lockDir, LockFileName)

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
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

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
	running := lockfile.IsProcessRunning(info.PID)

	return &info, running, nil
}

// ReadLockFile reads and parses the lock file without modifying it
func ReadLockFile(worktreePath string) (*LockInfo, error) {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return nil, err
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

// UpdateLockTask updates the lock file with task information
// This is called by Claude after picking a task to work on
func UpdateLockTask(worktreePath, taskID, taskTitle string) error {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

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
	info.TaskStartedAt = time.Now() // Reset timer for this task

	// Write back
	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// UpdateLockState updates the lock file with current execution state
// Used by auto mode to distinguish idle (polling) from active (executing Claude)
func UpdateLockState(worktreePath, state string) error {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("no active lock to update: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}

	// Validate PID matches (prevent updating another agent's lock)
	if info.PID != os.Getpid() {
		return fmt.Errorf("lock belongs to different process (PID %d)", info.PID)
	}

	info.State = state

	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// ClearLockTaskID resets the TaskID, TaskTitle, and TaskStartedAt in the lock file.
// Called before each agent session in auto mode so we can detect whether the
// new session claims a task (used for no-progress detection).
func ClearLockTaskID(worktreePath string) error {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("no active lock to update: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}

	// Validate PID matches (prevent clearing another agent's lock)
	if info.PID != os.Getpid() {
		return fmt.Errorf("lock belongs to different process (PID %d)", info.PID)
	}

	info.TaskID = ""
	info.TaskTitle = ""
	info.TaskStartedAt = time.Time{}

	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// UpdateLockClaudeSessionID updates the lock file with the Claude CLI session UUID.
// Used by auto mode to persist the session ID for --resume on restart.
func UpdateLockClaudeSessionID(worktreePath, claudeSessionID string) error {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

	data, err := os.ReadFile(lockPath)
	if err != nil {
		return fmt.Errorf("no active lock to update: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}

	if info.PID != os.Getpid() {
		return fmt.Errorf("lock belongs to different process (PID %d)", info.PID)
	}

	info.ClaudeSessionID = claudeSessionID

	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// ClearLockClaudeSessionID clears the Claude session UUID from the lock file.
// Called after a Claude process exits to prevent stale session IDs.
func ClearLockClaudeSessionID(worktreePath string) error {
	lockPath := filepath.Join(ResolveLockDir(worktreePath), LockFileName)

	data, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // lock already released, nothing to clear
		}
		return fmt.Errorf("no active lock to update: %w", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}

	if info.PID != os.Getpid() {
		return fmt.Errorf("lock belongs to different process (PID %d)", info.PID)
	}

	info.ClaudeSessionID = ""

	data, err = json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}

	return os.WriteFile(lockPath, data, 0600)
}

// GetLockStatus returns a human-readable status for a worktree's lock
// Uses explicit state words: planning, working, done, review, idle
func GetLockStatus(worktreePath string) string {
	info, running, err := CheckLock(worktreePath)
	if err != nil || !running {
		return ""
	}

	// Use per-task timing if available, otherwise session timing
	var duration time.Duration
	if !info.TaskStartedAt.IsZero() {
		duration = time.Since(info.TaskStartedAt).Round(time.Second)
	} else {
		duration = time.Since(info.StartedAt).Round(time.Second)
	}

	// Check for idle state (auto mode waiting for tasks)
	if info.State == StateIdle {
		return fmt.Sprintf("idle (%s)", duration)
	}

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
			// Implementation agents show "working" even on review status tasks
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

// getTaskStatus returns the status of a task.
// Returns "needs_review", "closed", "in_progress", "open", or ""
func getTaskStatus(taskID string) string {
	d := *defaultDeps
	d.IssueBackend = DefaultIssueBackend()
	return GetTaskStatusDeps(&d, taskID)
}

func GetTaskStatusDeps(deps *Deps, taskID string) string {
	detail, err := deps.IssueBackend.Get(cmdstore.RootContext(), taskID)
	if err != nil || detail == nil {
		return ""
	}
	if detail.Status == "review" {
		return "needs_review"
	}
	return detail.Status
}

// WorkspaceHash returns a short hash of the workspace path for signal file naming.
func WorkspaceHash(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:8])
}

// GetSignalFilePath returns the signal file path for a worktree.
func GetSignalFilePath(worktreePath string) string {
	signalDir := filepath.Join(os.TempDir(), fmt.Sprintf("loom-signals-%d", os.Getuid()))
	return filepath.Join(signalDir, WorkspaceHash(worktreePath))
}
