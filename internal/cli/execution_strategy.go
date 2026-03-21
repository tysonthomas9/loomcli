package cli

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"log/slog"
	"time"
)

// ExecutionStrategy abstracts how an agent subprocess is launched and terminated.
// The supervisor loop (superviseAgent) is unchanged — it delegates spawn/kill
// through this interface.
type ExecutionStrategy interface {
	// Spawn starts the agent and returns the exec.Cmd. The caller assigns
	// ap.cmd and ap.pid while holding ap.mu. Implementations must NOT
	// acquire ap.mu.
	Spawn(ap *AgentProcess, loomArgs []string, env []string, logFile *os.File) (*exec.Cmd, error)

	// Kill terminates the agent. For local: SIGTERM to process group.
	// For sandbox: SIGTERM to openshell process + delete sandbox.
	Kill(ap *AgentProcess)

	// Cleanup runs after waitForAgent returns. For sandbox: fetch pushed
	// changes back to host, delete sandbox. For local: no-op.
	Cleanup(ap *AgentProcess) error

	// Name returns a human-readable identifier for this strategy.
	Name() string
}

// DirectStrategy executes agent subprocesses directly on the host via exec.Command.
// This is the default strategy and preserves the original daemon behavior.
type DirectStrategy struct{}

// Name returns the strategy identifier.
func (s *DirectStrategy) Name() string {
	return "direct"
}

// Spawn starts the agent subprocess locally using exec.Command.
// It returns the started exec.Cmd; the caller assigns ap.cmd and ap.pid.
func (s *DirectStrategy) Spawn(ap *AgentProcess, loomArgs []string, env []string, logFile *os.File) (*exec.Cmd, error) {
	cmd := exec.Command("loom", loomArgs...)
	cmd.Dir = ap.worktreePath
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return cmd, nil
}

// Kill sends SIGTERM to the entire process group, then SIGKILL if the process
// does not exit within 5 seconds. This matches the original stopAgent behavior.
func (s *DirectStrategy) Kill(ap *AgentProcess) {
	ap.mu.Lock()
	proc := ap.cmd
	pid := ap.pid
	ap.mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	slog.Info("sending signal to process group", "worktree", ap.entry.Worktree, "signal", "SIGTERM", "pid", pid)

	// Send SIGTERM to the entire process group (negative PID)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process group may have already exited; try the process directly
		slog.Warn("SIGTERM to process group failed, trying process directly", "worktree", ap.entry.Worktree, "err", err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			slog.Warn("SIGTERM failed, process may have exited", "worktree", ap.entry.Worktree, "err", err)
			return
		}
	}

	// Poll for process exit up to 5 seconds instead of calling Wait()
	// (Wait() is called by waitForAgent in the supervise loop)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ap.mu.Lock()
		currentPID := ap.pid
		ap.mu.Unlock()

		if currentPID == 0 {
			// Process has exited (waitForAgent cleared the pid)
			slog.Info("process exited gracefully", "worktree", ap.entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !lockfile.IsProcessRunning(pid) {
			slog.Info("process exited gracefully", "worktree", ap.entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill the entire process group if still running
	ap.mu.Lock()
	stillRunning := ap.pid != 0
	ap.mu.Unlock()

	if stillRunning {
		slog.Warn("sending SIGKILL to process group", "worktree", ap.entry.Worktree, "pid", pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// Cleanup is a no-op for direct local execution.
func (s *DirectStrategy) Cleanup(ap *AgentProcess) error {
	return nil
}
