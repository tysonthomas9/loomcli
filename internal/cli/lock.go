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

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
	// RunID is a STABLE LOGICAL run id, established at first acquisition and
	// carried forward across stale-lock replacement (daemon restart). It keys
	// transcript-event dedup so resumed/replayed rows collapse rather than
	// fragmenting under a fresh per-process id.
	RunID string `json:"run_id,omitempty"`
}

// lockSidecarName is a stable advisory-lock file placed next to the lock file.
// flock is inode-based, so we must lock a file that is NEVER renamed — the
// lock file itself is atomically replaced (temp+rename), which would drop a
// flock taken on it. The sidecar is only ever flocked, never renamed.
const lockSidecarName = ".agent.lock.flock"

// lockSidecar acquires an exclusive advisory lock on the stable sidecar file in
// lockDir and returns an unlock func. All lock read-modify-write paths take this
// guard so concurrent mutators cannot lose updates (the previous per-field
// writers each did an unguarded read-modify-write).
func lockSidecar(lockDir string) (unlock func(), err error) {
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return func() {}, fmt.Errorf("lock: creating dir %s: %w", lockDir, err)
	}
	p := filepath.Join(lockDir, lockSidecarName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600) //nolint:gosec // p = lockDir + fixed name
	if err != nil {
		return func() {}, fmt.Errorf("lock: opening sidecar %s: %w", p, err)
	}
	if err := lockfile.FlockExclusiveBlocking(f); err != nil {
		_ = f.Close()
		return func() {}, fmt.Errorf("lock: flock %s: %w", p, err)
	}
	return func() {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}, nil
}

// UpdateLock performs a sidecar-flock-guarded, atomic (temp+rename via
// atomicfile) read-modify-write of the worktree lock. mutate receives the
// current LockInfo and may return an error to abort the write (e.g. an
// ownership check). Read errors (including a missing lock) propagate to the
// caller, which maps them as needed. This is the single path through which all
// lock mutations flow, so no straggler reintroduces the lost-update race.
func UpdateLock(worktreePath string, mutate func(*LockInfo) error) error {
	lockDir := ResolveLockDir(worktreePath)
	lockPath := filepath.Join(lockDir, LockFileName)

	unlock, err := lockSidecar(lockDir)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(lockPath) //nolint:gosec // lockPath = lockDir + fixed name
	if err != nil {
		return err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return fmt.Errorf("invalid lock file: %w", err)
	}
	if err := mutate(&info); err != nil {
		return err
	}
	out, err := json.MarshalIndent(&info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal lock info: %w", err)
	}
	return atomicfile.WriteFile(lockPath, out, 0600)
}

// requireOwner returns an error unless the lock is owned by this process.
// Mutators that must only touch their own lock wrap their change with it.
func requireOwner(info *LockInfo) error {
	if info.PID != os.Getpid() {
		return fmt.Errorf("lock belongs to different process (PID %d)", info.PID)
	}
	return nil
}

// newRunID returns a fresh stable logical run id (hex of 16 random bytes).
func newRunID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to a time-derived id so RunID is never empty.
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ResolveLockDir determines the correct directory for the lock file.
// Each worktree gets its own lock file so multiple agents in the same
// workspace don't collide. Returns the path unchanged.
func ResolveLockDir(path string) string {
	return path
}

// resolveWorkspaceName returns the workspace name for the given path, or empty string if not in a workspace.
func resolveWorkspaceName(path string) string {
	// Cloud invocations have an explicit workspace key. Avoid the global
	// workspace list here: scoped credentials cannot access it, and this value
	// is only advisory lock-event attribution.
	if bootstrap.DetectMode() == bootstrap.ModeCloud {
		return os.Getenv(bootstrap.EnvWorkspace)
	}

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

	// Carry resume continuity forward from a STALE (dead-PID) prior lock before
	// the acquire path removes it (acquireLockRetry does os.Remove on a stale
	// lock, which would otherwise lose the session id needed to --resume on a
	// daemon restart). Read it up front. The same-task/TTL guards are applied
	// later by the resume decision; here we just preserve the data + RunID.
	if prior, perr := ReadLockFile(worktreePath); perr == nil && prior != nil && !lockfile.IsProcessRunning(prior.PID) {
		info.ClaudeSessionID = prior.ClaudeSessionID
		info.RunID = prior.RunID
		info.TaskID = prior.TaskID
		info.TaskTitle = prior.TaskTitle
		info.TaskStartedAt = prior.TaskStartedAt
	}
	if info.RunID == "" {
		info.RunID = newRunID() // stable logical run id; reused across restarts via carry-forward
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
	return UpdateLock(worktreePath, func(info *LockInfo) error {
		info.TaskID = taskID
		info.TaskTitle = taskTitle
		info.TaskStartedAt = time.Now() // Reset timer for this task
		return nil
	})
}

// UpdateLockState updates the lock file with current execution state
// Used by auto mode to distinguish idle (polling) from active (executing Claude)
func UpdateLockState(worktreePath, state string) error {
	return UpdateLock(worktreePath, func(info *LockInfo) error {
		if err := requireOwner(info); err != nil {
			return err
		}
		info.State = state
		return nil
	})
}

// ClearLockTaskID resets the TaskID, TaskTitle, and TaskStartedAt in the lock file.
// Called before each agent session in auto mode so we can detect whether the
// new session claims a task (used for no-progress detection).
func ClearLockTaskID(worktreePath string) error {
	return UpdateLock(worktreePath, func(info *LockInfo) error {
		if err := requireOwner(info); err != nil {
			return err
		}
		info.TaskID = ""
		info.TaskTitle = ""
		info.TaskStartedAt = time.Time{}
		return nil
	})
}

// UpdateLockClaudeSessionID updates the lock file with the Claude CLI session UUID.
// Used by auto mode to persist the session ID for --resume on restart.
func UpdateLockClaudeSessionID(worktreePath, claudeSessionID string) error {
	return UpdateLock(worktreePath, func(info *LockInfo) error {
		if err := requireOwner(info); err != nil {
			return err
		}
		info.ClaudeSessionID = claudeSessionID
		return nil
	})
}

// ClearLockClaudeSessionID clears the Claude session UUID from the lock file.
// Called after a Claude process exits to prevent stale session IDs.
func ClearLockClaudeSessionID(worktreePath string) error {
	err := UpdateLock(worktreePath, func(info *LockInfo) error {
		if err := requireOwner(info); err != nil {
			return err
		}
		info.ClaudeSessionID = ""
		return nil
	})
	if os.IsNotExist(err) {
		return nil // lock already released, nothing to clear
	}
	return err
}

// ClearStaleLockClaudeSessionID clears the carried Claude session UUID from a
// dead-PID lock. This is for daemon recovery paths that intentionally preserve a
// crash-remnant lock owned by the prior agent subprocess but need to force the
// next acquisition to cold-start from checkpoint instead of --resume.
func ClearStaleLockClaudeSessionID(worktreePath string) error {
	err := UpdateLock(worktreePath, func(info *LockInfo) error {
		if lockfile.IsProcessRunning(info.PID) {
			return fmt.Errorf("lock belongs to running process (PID %d)", info.PID)
		}
		info.ClaudeSessionID = ""
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
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
	// No TaskID yet. A planner without a task is still legitimately planning
	// (it bootstraps tasks rather than claiming one), so keep the ellipsis form.
	// A worker without a task is not actually working — surface it as idle so
	// the UI doesn't show "Working" while the detail panel reports no task.
	if info.Command == "plan" {
		return fmt.Sprintf("planning: ... (%s)", duration)
	}
	return fmt.Sprintf("idle (%s)", duration)
}

// getTaskStatus returns the status of a task.
// Returns "needs_review", "closed", "in_progress", "open", or ""
func getTaskStatus(taskID string) string {
	d := *ensureDefaultDeps()
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
