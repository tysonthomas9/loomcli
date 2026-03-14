package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
)

// buildCommand constructs the exec.Cmd for spawning an agent subprocess (does not start it).
func (d *Daemon) buildCommand(ap *AgentProcess) *exec.Cmd {
	ap.mu.Lock()
	epicID := ap.assignedEpicID // snapshot before getEffectiveBackend (also acquires ap.mu)
	ap.mu.Unlock()
	var cmd *exec.Cmd

	// Resolve backend using failover-aware resolution
	agentBackend := d.getEffectiveBackend(ap)

	if builtInRoles[ap.entry.Role] {
		// Built-in role: loom <role> <worktree> --auto --daemon-mode
		args := []string{ap.entry.Role, ap.worktreePath, "--auto", "--daemon-mode"}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if epicID != "" {
			args = append(args, "--parent", epicID)
		}
		cmd = exec.Command("loom", args...)
	} else {
		// Custom role: loom agent <worktree> --prompt <path> --task-filter <filter> --auto --daemon-mode
		args := []string{"agent", ap.worktreePath, "--prompt", ap.roleConfig.PromptFile, "--auto", "--daemon-mode"}
		if ap.roleConfig.TaskFilter != "" {
			args = append(args, "--task-filter", ap.roleConfig.TaskFilter)
		}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if epicID != "" {
			args = append(args, "--parent", epicID)
		}
		cmd = exec.Command("loom", args...)
	}

	// Set working directory to worktree
	cmd.Dir = ap.worktreePath

	// Create a new process group so we can kill the entire tree on stop
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set environment
	cmd.Env = append(FilteredEnv(),
		fmt.Sprintf("BD_ACTOR=%s", ap.entry.Worktree),
		fmt.Sprintf("LOOM_WORKTREE_PATH=%s", ap.worktreePath),
		fmt.Sprintf("LOOM_EVENTS_DIR=%s", resolveDaemonPath(d.projectDir, d.config.Daemon.EventsDir)),
	)

	// Propagate role constraints as env vars for the subprocess
	if len(ap.roleConfig.AllowedTools) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ALLOWED_TOOLS=%s", strings.Join(ap.roleConfig.AllowedTools, ",")))
	}
	if len(ap.roleConfig.DeniedTools) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_DENIED_TOOLS=%s", strings.Join(ap.roleConfig.DeniedTools, ",")))
	}
	if ap.roleConfig.ReadOnly {
		cmd.Env = append(cmd.Env, "LOOM_READ_ONLY=1")
	}

	// Propagate routing constraints as env vars for subprocess task routing
	if len(ap.roleConfig.Skills) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ROLE_SKILLS=%s", strings.Join(ap.roleConfig.Skills, ",")))
	}
	if len(ap.roleConfig.PathPatterns) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ROLE_PATH_PATTERNS=%s", strings.Join(ap.roleConfig.PathPatterns, ",")))
	}
	if ap.roleConfig.MaxPriority != nil {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ROLE_MAX_PRIORITY=%d", *ap.roleConfig.MaxPriority))
	}
	if ap.roleConfig.TaskFilter != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ROLE_TASK_FILTER=%s", ap.roleConfig.TaskFilter))
	}
	cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_ROLE=%s", ap.entry.Role))
	if len(ap.entry.PathPatterns) > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("LOOM_AGENT_PATH_PATTERNS=%s", strings.Join(ap.entry.PathPatterns, ",")))
	}

	return cmd
}

// spawnAgent starts the subprocess for an agent.
func (d *Daemon) spawnAgent(ap *AgentProcess) error {
	cmd := d.buildCommand(ap)

	ap.mu.Lock()

	// Set up log files if log directory is configured
	if d.config.Daemon.LogDir != "" {
		logDir := d.config.Daemon.LogDir
		if !filepath.IsAbs(logDir) {
			logDir = filepath.Join(d.projectDir, logDir)
		}
		if err := os.MkdirAll(logDir, 0700); err != nil {
			log.Printf("[daemon] Agent %s: failed to create log directory: %v", ap.entry.Worktree, err)
		} else {
			// Sanitize worktree name to prevent path traversal in log filename
			safeWorktree := filepath.Base(ap.entry.Worktree)
			logFilePath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", ap.entry.Role, safeWorktree))
			ap.logFilePath = logFilePath // store for watchdog stat checks
			f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				log.Printf("[daemon] Agent %s: failed to open log file: %v", ap.entry.Worktree, err)
			} else {
				cmd.Stdout = f
				cmd.Stderr = f
				ap.logFile = f // Track file handle for cleanup in waitForAgent
			}
		}
	}

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		if ap.logFile != nil {
			_ = ap.logFile.Close()
			ap.logFile = nil
		}
		ap.mu.Unlock()
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	ap.cmd = cmd
	ap.pid = cmd.Process.Pid
	ap.lastStart = time.Now()

	// Snapshot fields before releasing lock for event emission
	pid := ap.pid
	worktree := ap.entry.Worktree
	role := ap.entry.Role
	epicID := ap.assignedEpicID
	ap.mu.Unlock()

	log.Printf("[daemon] Agent %s: spawned subprocess PID %d", worktree, pid)

	// Emit agent_started event outside the lock (best-effort, matching waitForAgent pattern)
	if evt, err := events.NewEvent(events.AgentStarted, worktree, role, epicID, events.AgentStartedData{PID: pid}); err == nil {
		d.emitEvent(evt)
	}

	return nil
}

// waitForAgent blocks until subprocess exits, returns exit code.
func (d *Daemon) waitForAgent(ap *AgentProcess) int {
	ap.mu.Lock()
	cmd := ap.cmd
	ap.mu.Unlock()

	if cmd == nil {
		return -1
	}

	err := cmd.Wait()

	ap.mu.Lock()
	ap.lastExit = time.Now()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			ap.lastExitCode = exitErr.ExitCode()
		} else {
			ap.lastExitCode = -1
		}
	} else {
		ap.lastExitCode = 0
	}
	exitCode := ap.lastExitCode
	pid := ap.pid // capture before clearing
	worktree := ap.entry.Worktree
	role := ap.entry.Role
	epicID := ap.assignedEpicID
	ap.cmd = nil
	ap.pid = 0

	// Close log file handle to prevent leaks
	if ap.logFile != nil {
		if err := ap.logFile.Close(); err != nil {
			log.Printf("[daemon] Agent %s: failed to close log file: %v", ap.entry.Worktree, err)
		}
		ap.logFile = nil
	}
	ap.mu.Unlock()

	// Emit agent_stopped event outside the lock (best-effort)
	if evt, err := events.NewEvent(events.AgentStopped, worktree, role, epicID, events.AgentStoppedData{PID: pid, ExitCode: exitCode}); err == nil {
		d.emitEvent(evt)
	}

	return exitCode
}

// recoverAgent calls RecoverWorktree for cleanup.
// exitCode is passed so recovery can make smarter decisions (e.g. skip task
// reset on clean exit when the task status is already terminal).
func (d *Daemon) recoverAgent(ap *AgentProcess, exitCode int) error {
	return RecoverWorktree(ap.worktreePath, ap.entry.Worktree, exitCode)
}
