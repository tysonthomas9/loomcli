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

// buildLoomArgs constructs the loom CLI arguments for spawning an agent subprocess.
// The returned slice is suitable for passing to exec.Command("loom", args...) or
// to an ExecutionStrategy's Spawn method.
func (d *Daemon) buildLoomArgs(ap *AgentProcess) []string {
	ap.mu.Lock()
	epicID := ap.assignedEpicID // snapshot before getEffectiveBackend (also acquires ap.mu)
	ap.mu.Unlock()

	// Resolve backend using failover-aware resolution
	agentBackend := d.getEffectiveBackend(ap)

	var args []string
	if builtInRoles[ap.entry.Role] {
		// Built-in role: loom <role> <worktree> --auto --daemon-mode
		args = []string{ap.entry.Role, ap.worktreePath, "--auto", "--daemon-mode"}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if epicID != "" {
			args = append(args, "--parent", epicID)
		}
	} else {
		// Custom role: loom agent <worktree> --prompt <path> --task-filter <filter> --auto --daemon-mode
		args = []string{"agent", ap.worktreePath, "--prompt", ap.roleConfig.PromptFile, "--auto", "--daemon-mode"}
		if ap.roleConfig.TaskFilter != "" {
			args = append(args, "--task-filter", ap.roleConfig.TaskFilter)
		}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if epicID != "" {
			args = append(args, "--parent", epicID)
		}
	}

	return args
}

// buildEnv constructs the environment variable slice for an agent subprocess.
func (d *Daemon) buildEnv(ap *AgentProcess) []string {
	cfg := d.configSnapshot() // snapshot config for consistent reads

	env := append(FilteredEnv(),
		fmt.Sprintf("BD_ACTOR=%s", ap.entry.Worktree),
		fmt.Sprintf("LOOM_WORKTREE_PATH=%s", ap.worktreePath),
		fmt.Sprintf("LOOM_EVENTS_DIR=%s", resolveDaemonPath(d.projectDir, cfg.Daemon.EventsDir)),
	)

	// Propagate repo context for subprocess diagnostics and prompts
	if ap.entry.Repo != "" {
		env = append(env, fmt.Sprintf("LOOM_AGENT_REPO=%s", ap.entry.Repo))
	}

	// Propagate role constraints as env vars for the subprocess
	if len(ap.roleConfig.AllowedTools) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ALLOWED_TOOLS=%s", strings.Join(ap.roleConfig.AllowedTools, ",")))
	}
	if len(ap.roleConfig.DeniedTools) > 0 {
		env = append(env, fmt.Sprintf("LOOM_DENIED_TOOLS=%s", strings.Join(ap.roleConfig.DeniedTools, ",")))
	}
	if ap.roleConfig.ReadOnly {
		env = append(env, "LOOM_READ_ONLY=1")
	}

	// Propagate routing constraints as env vars for subprocess task routing
	if len(ap.roleConfig.Skills) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ROLE_SKILLS=%s", strings.Join(ap.roleConfig.Skills, ",")))
	}
	if len(ap.roleConfig.PathPatterns) > 0 {
		env = append(env, fmt.Sprintf("LOOM_ROLE_PATH_PATTERNS=%s", strings.Join(ap.roleConfig.PathPatterns, ",")))
	}
	if ap.roleConfig.MaxPriority != nil {
		env = append(env, fmt.Sprintf("LOOM_ROLE_MAX_PRIORITY=%d", *ap.roleConfig.MaxPriority))
	}
	if ap.roleConfig.TaskFilter != "" {
		env = append(env, fmt.Sprintf("LOOM_ROLE_TASK_FILTER=%s", ap.roleConfig.TaskFilter))
	}
	env = append(env, fmt.Sprintf("LOOM_ROLE=%s", ap.entry.Role))
	if len(ap.entry.PathPatterns) > 0 {
		env = append(env, fmt.Sprintf("LOOM_AGENT_PATH_PATTERNS=%s", strings.Join(ap.entry.PathPatterns, ",")))
	}

	// Propagate resolved source repos for repo affinity scoring
	if sourceRepos := resolveAgentRepos(ap.entry, d.repos); len(sourceRepos) > 0 {
		env = append(env, fmt.Sprintf("LOOM_SOURCE_REPOS=%s", strings.Join(sourceRepos, ",")))
	}

	return env
}

// buildCommand constructs the exec.Cmd for spawning an agent subprocess (does not start it).
// It combines buildLoomArgs and buildEnv into a fully configured exec.Cmd.
// This method is retained for backward compatibility with existing tests that
// inspect the constructed command.
func (d *Daemon) buildCommand(ap *AgentProcess) *exec.Cmd {
	loomArgs := d.buildLoomArgs(ap)
	env := d.buildEnv(ap)

	cmd := exec.Command("loom", loomArgs...)
	cmd.Dir = ap.worktreePath
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = env

	return cmd
}

// spawnAgent starts the subprocess for an agent using its execution strategy.
func (d *Daemon) spawnAgent(ap *AgentProcess) error {
	loomArgs := d.buildLoomArgs(ap)
	env := d.buildEnv(ap)

	// Read config BEFORE acquiring ap.mu to avoid lock-order inversion
	// (configSnapshot acquires d.reconcileMu.RLock).
	cfg := d.configSnapshot()

	// For sandbox agents, push the worktree branch before creating the sandbox.
	// The sandbox will clone this branch, so it must be available on the remote.
	if _, isSandbox := ap.strategy.(*SandboxStrategy); isSandbox {
		if err := d.pushWorktreeBranch(ap); err != nil {
			return fmt.Errorf("pre-spawn push failed (sandbox requires remote branch): %w", err)
		}
	}

	ap.mu.Lock()

	// Set up log files if log directory is configured
	var logFile *os.File
	if cfg.Daemon.LogDir != "" {
		logDir := cfg.Daemon.LogDir
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
				logFile = f
				ap.logFile = f // Track file handle for cleanup in waitForAgent
			}
		}
	}

	// Resolve strategy (fallback to DirectStrategy if nil for backward compatibility with tests)
	strategy := ap.strategy
	if strategy == nil {
		strategy = &DirectStrategy{}
	}

	// Start the subprocess via execution strategy.
	// Spawn returns the *exec.Cmd; we assign ap.cmd and ap.pid here while
	// holding ap.mu so that Spawn implementations never need ap.mu.
	cmd, err := strategy.Spawn(ap, loomArgs, env, logFile)
	if err != nil {
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

	// Resolve strategy for cleanup (fallback for backward compatibility with tests)
	strategy := ap.strategy
	ap.mu.Unlock()

	// Run strategy-specific cleanup (e.g., fetch changes from sandbox)
	if strategy != nil {
		if err := strategy.Cleanup(ap); err != nil {
			log.Printf("[daemon] Agent %s: strategy cleanup failed: %v", worktree, err)
		}
	}

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

// pushWorktreeBranch pushes the agent's worktree branch to the remote.
// This is called before sandbox creation so the sandbox can clone the branch.
// Uses --force-with-lease for safety (fails if remote has unexpected changes).
func (d *Daemon) pushWorktreeBranch(ap *AgentProcess) error {
	branch := ap.entry.Worktree
	cmd := exec.Command("git", "push", "origin",
		fmt.Sprintf("%s:%s", branch, branch), "--force-with-lease")
	cmd.Dir = d.projectDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s: %s: %w", branch, string(out), err)
	}
	return nil
}
