package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// AgentProcess tracks a single supervised agent subprocess.
type AgentProcess struct {
	entry        AgentEntry // config from loom.yaml
	roleConfig   RoleConfig // resolved role configuration
	worktreePath string     // resolved worktree path

	cmd     *exec.Cmd // current subprocess (nil when not running)
	pid     int       // PID of current subprocess (0 when not running)
	logFile *os.File  // log file handle for subprocess output (nil if not logging)

	restartCount    int       // consecutive restart attempts
	lastStart       time.Time // when subprocess was last spawned
	lastExit        time.Time // when subprocess last exited
	lastExitCode    int       // exit code from last run
	assignedEpicID  string    // epic this agent is currently assigned to (empty = non-epic mode)

	mu sync.Mutex // protects cmd, pid, logFile, restart tracking, assignedEpicID
}

// SupervisedAgentStatus is a snapshot of a supervised agent's state for external inspection.
// This type is safe to copy and does not contain a mutex.
type SupervisedAgentStatus struct {
	Worktree       string
	Role           string
	WorktreePath   string
	PID            int
	RestartCount   int
	LastStart      time.Time
	LastExit       time.Time
	LastExitCode   int
	AssignedEpicID string
}

// Daemon coordinates multiple supervised agents.
type Daemon struct {
	config     *DaemonConfig
	projectDir string // directory containing loom.yaml

	// agents is populated during NewDaemon and is immutable afterward.
	// Safe to read without holding mu after Start() is called.
	agents       []*AgentProcess
	shutdown     chan struct{}  // closed to signal shutdown
	shutdownOnce sync.Once      // protects shutdown channel from double-close
	wg           sync.WaitGroup // tracks superviseAgent goroutines

	epicAssigner *EpicAssigner // manages epic-to-worktree assignments
}

// builtInRoles defines the built-in role names that use loom <role> command.
var builtInRoles = map[string]bool{
	"plan": true,
	"task": true,
}

// NewDaemon creates a daemon from the loaded config.
func NewDaemon(config *DaemonConfig, projectDir string) (*Daemon, error) {
	if config == nil {
		return nil, fmt.Errorf("daemon config is nil")
	}
	if len(config.Agents) == 0 {
		return nil, fmt.Errorf("no agents configured in loom.yaml")
	}

	d := &Daemon{
		config:       config,
		projectDir:   projectDir,
		agents:       make([]*AgentProcess, 0, len(config.Agents)),
		epicAssigner: NewEpicAssigner(),
	}

	for i, entry := range config.Agents {
		// Resolve worktree path
		target, err := ResolveAgentTarget(entry.Worktree)
		if err != nil {
			return nil, fmt.Errorf("agent[%d] worktree %q: %w", i, entry.Worktree, err)
		}

		// Resolve role config
		roleConfig, err := d.resolveRoleConfig(entry.Role, i)
		if err != nil {
			return nil, err
		}

		ap := &AgentProcess{
			entry:        entry,
			roleConfig:   roleConfig,
			worktreePath: target.WorkDir,
		}
		d.agents = append(d.agents, ap)
	}

	return d, nil
}

// resolveRoleConfig looks up a role by name, supporting both built-in and custom roles.
func (d *Daemon) resolveRoleConfig(roleName string, agentIndex int) (RoleConfig, error) {
	// Check for built-in roles first
	if builtInRoles[roleName] {
		return RoleConfig{Description: fmt.Sprintf("Built-in %s agent", roleName)}, nil
	}

	// Look up custom role in config
	rc, ok := d.config.ResolveRole(roleName)
	if !ok {
		return RoleConfig{}, fmt.Errorf("agent[%d]: role %q not found (not a built-in role and not defined in config.Roles)", agentIndex, roleName)
	}

	// Custom roles require a prompt file
	if rc.PromptFile == "" {
		return RoleConfig{}, fmt.Errorf("agent[%d]: custom role %q missing prompt_file", agentIndex, roleName)
	}

	// Resolve prompt file path relative to project dir
	promptPath := rc.PromptFile
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(d.projectDir, promptPath)
	}
	if _, err := os.Stat(promptPath); err != nil {
		return RoleConfig{}, fmt.Errorf("agent[%d]: prompt file %q not found: %w", agentIndex, promptPath, err)
	}
	rc.PromptFile = promptPath

	return rc, nil
}

// Start launches supervisor goroutines for all configured agents.
func (d *Daemon) Start() error {
	d.shutdown = make(chan struct{})

	// Start healthChecker goroutine
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.healthChecker()
	}()

	// Start superviseAgent goroutine for each agent
	for _, ap := range d.agents {
		d.wg.Add(1)
		go func(agent *AgentProcess) {
			defer d.wg.Done()
			d.superviseAgent(agent)
		}(ap)
	}

	return nil
}

// Stop gracefully shuts down all agents. Safe to call multiple times.
func (d *Daemon) Stop() {
	// Signal all goroutines to stop (protected from double-close)
	d.shutdownOnce.Do(func() {
		close(d.shutdown)
	})

	// Stop all agent processes
	for _, ap := range d.agents {
		d.stopAgent(ap)
	}

	// Wait for all superviseAgent goroutines to exit
	d.wg.Wait()
}

// superviseAgent is the main loop for a single agent (runs in goroutine).
func (d *Daemon) superviseAgent(ap *AgentProcess) {
	defer d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)
	log.Printf("[daemon] Starting supervisor for agent %s (role: %s)", ap.entry.Worktree, ap.entry.Role)

	for {
		// Check shutdown before each cycle
		select {
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown signal received", ap.entry.Worktree)
			return
		default:
		}

		// 1. Pre-flight recovery
		if err := d.recoverAgent(ap); err != nil {
			log.Printf("[daemon] Agent %s: pre-flight recovery failed: %v", ap.entry.Worktree, err)
			// Continue with caution - spawn may still work
		}

		// 1.5. Assign epic to worktree (if available)
		epicID, err := d.epicAssigner.AssignWorktree(ap.entry.Worktree)
		if err != nil {
			log.Printf("[daemon] Agent %s: epic assignment failed (falling back to non-epic mode): %v", ap.entry.Worktree, err)
			epicID = ""
		}
		ap.mu.Lock()
		ap.assignedEpicID = epicID
		ap.mu.Unlock()

		// 2. Spawn subprocess
		if err := d.spawnAgent(ap); err != nil {
			log.Printf("[daemon] Agent %s: spawn failed: %v", ap.entry.Worktree, err)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 3. Wait for exit
		exitCode := d.waitForAgent(ap)
		log.Printf("[daemon] Agent %s: exited with code %d", ap.entry.Worktree, exitCode)

		// 4. Post-mortem recovery
		if err := d.recoverAgent(ap); err != nil {
			log.Printf("[daemon] Agent %s: post-mortem recovery failed: %v", ap.entry.Worktree, err)
			// Non-fatal, continue with restart logic
		}

		// 5. Check shutdown after subprocess exit
		select {
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown signal received after exit", ap.entry.Worktree)
			return
		default:
		}

		// 6. Restart decision
		if !d.shouldRestart(ap) {
			log.Printf("[daemon] Agent %s: max restarts exceeded, stopping supervisor", ap.entry.Worktree)
			return
		}

		// 7. Backoff sleep (interruptible)
		backoff := d.computeBackoff(ap)
		log.Printf("[daemon] Agent %s: waiting %v before restart (attempt %d)", ap.entry.Worktree, backoff, ap.restartCount)

		select {
		case <-time.After(backoff):
			// Backoff complete, continue to next iteration
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown during backoff", ap.entry.Worktree)
			return
		}
	}
}

// handleRestartAfterError handles restart logic after spawn failure.
// Returns true if the supervisor should continue, false if it should exit.
func (d *Daemon) handleRestartAfterError(ap *AgentProcess) bool {
	ap.mu.Lock()
	ap.restartCount++
	count := ap.restartCount
	ap.mu.Unlock()

	maxRetries := d.getMaxRetries()
	if count > maxRetries {
		log.Printf("[daemon] Agent %s: max retries exceeded after spawn error", ap.entry.Worktree)
		return false
	}

	backoff := d.computeBackoff(ap)
	log.Printf("[daemon] Agent %s: spawn failed, waiting %v before retry (attempt %d/%d)",
		ap.entry.Worktree, backoff, count, maxRetries)

	select {
	case <-time.After(backoff):
		return true
	case <-d.shutdown:
		return false
	}
}

// spawnAgent starts the subprocess for an agent.
func (d *Daemon) spawnAgent(ap *AgentProcess) error {
	ap.mu.Lock()
	defer ap.mu.Unlock()

	var cmd *exec.Cmd

	// Resolve backend for this agent: per-agent > project > global > default
	agentBackend := ap.entry.Backend
	if agentBackend == "" {
		agentBackend = d.config.Backend
	}

	if builtInRoles[ap.entry.Role] {
		// Built-in role: loom <role> <worktree> --auto --daemon-mode
		args := []string{ap.entry.Role, ap.worktreePath, "--auto", "--daemon-mode"}
		if agentBackend != "" {
			args = append(args, "--backend", agentBackend)
		}
		if ap.assignedEpicID != "" {
			args = append(args, "--parent", ap.assignedEpicID)
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
		if ap.assignedEpicID != "" {
			args = append(args, "--parent", ap.assignedEpicID)
		}
		cmd = exec.Command("loom", args...)
	}

	// Set working directory to worktree
	cmd.Dir = ap.worktreePath

	// Set environment
	cmd.Env = append(FilteredEnv(),
		fmt.Sprintf("BD_ACTOR=%s", ap.entry.Worktree),
		fmt.Sprintf("LOOM_WORKTREE_PATH=%s", ap.worktreePath),
	)

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
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	ap.cmd = cmd
	ap.pid = cmd.Process.Pid
	ap.lastStart = time.Now()

	log.Printf("[daemon] Agent %s: spawned subprocess PID %d", ap.entry.Worktree, ap.pid)
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

	return exitCode
}

// recoverAgent calls RecoverWorktree for cleanup.
func (d *Daemon) recoverAgent(ap *AgentProcess) error {
	return RecoverWorktree(ap.worktreePath, ap.entry.Worktree)
}

// shouldRestart determines if agent should restart based on backoff policy.
func (d *Daemon) shouldRestart(ap *AgentProcess) bool {
	maxRetries := d.getMaxRetries()

	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Successful run (exit 0, ran for >1 minute) resets counter
	if ap.lastExitCode == 0 && time.Since(ap.lastStart) > time.Minute {
		ap.restartCount = 0
		return true
	}

	ap.restartCount++
	return ap.restartCount <= maxRetries
}

// computeBackoff returns the sleep duration before next restart.
func (d *Daemon) computeBackoff(ap *AgentProcess) time.Duration {
	initial := d.getBackoffInitial()
	maxBackoff := d.getBackoffMax()

	ap.mu.Lock()
	count := ap.restartCount
	ap.mu.Unlock()

	// Cap count to prevent integer overflow in bit shift (max safe shift is 30 for 32-bit result)
	if count > 30 {
		count = 30
	}

	// Exponential: initial * 2^restartCount
	backoffSec := initial * (1 << count)
	if backoffSec > maxBackoff || backoffSec < 0 { // check for overflow (negative)
		backoffSec = maxBackoff
	}

	return time.Duration(backoffSec) * time.Second
}

// Helper functions to safely access RestartPolicy fields with defaults.
func (d *Daemon) getMaxRetries() int {
	if d.config.Daemon.RestartPolicy.MaxRetries != nil {
		return *d.config.Daemon.RestartPolicy.MaxRetries
	}
	return 3 // default
}

func (d *Daemon) getBackoffInitial() int {
	if d.config.Daemon.RestartPolicy.BackoffInitial != nil {
		return *d.config.Daemon.RestartPolicy.BackoffInitial
	}
	return 2 // default seconds
}

func (d *Daemon) getBackoffMax() int {
	if d.config.Daemon.RestartPolicy.BackoffMax != nil {
		return *d.config.Daemon.RestartPolicy.BackoffMax
	}
	return 300 // default seconds
}

// stopAgent sends SIGTERM then SIGKILL to a single agent.
// This function is safe to call concurrently with waitForAgent.
// It uses polling instead of cmd.Wait() to avoid double-wait issues.
func (d *Daemon) stopAgent(ap *AgentProcess) {
	ap.mu.Lock()
	proc := ap.cmd
	pid := ap.pid
	ap.mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	log.Printf("[daemon] Agent %s: sending SIGTERM to PID %d", ap.entry.Worktree, pid)

	// Try graceful shutdown
	if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may have already exited
		log.Printf("[daemon] Agent %s: SIGTERM failed (process may have exited): %v", ap.entry.Worktree, err)
		return
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
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !IsProcessRunning(pid) {
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill if still running
	ap.mu.Lock()
	stillRunning := ap.pid != 0
	ap.mu.Unlock()

	if stillRunning {
		log.Printf("[daemon] Agent %s: sending SIGKILL to PID %d", ap.entry.Worktree, pid)
		_ = proc.Process.Signal(syscall.SIGKILL)
	}
}

// healthChecker runs periodic health checks in a goroutine.
func (d *Daemon) healthChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.shutdown:
			return
		case <-ticker.C:
			d.checkAgentHealth()
		}
	}
}

// checkAgentHealth performs health checks on all agents.
func (d *Daemon) checkAgentHealth() {
	for _, ap := range d.agents {
		ap.mu.Lock()
		pid := ap.pid
		worktreePath := ap.worktreePath
		worktreeName := ap.entry.Worktree
		ap.mu.Unlock()

		if pid == 0 {
			continue // Not running
		}

		// Check if PID is alive
		if !IsProcessRunning(pid) {
			// Process died unexpectedly - superviseAgent will detect via cmd.Wait()
			log.Printf("[daemon] Agent %s (PID %d) is not running", worktreeName, pid)
		}

		// Check lock file for stale state
		lockInfo, isRunning, err := CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			log.Printf("[daemon] Stale lock detected for agent %s", worktreeName)
		}
	}
}

// AgentCount returns the number of configured agents.
func (d *Daemon) AgentCount() int {
	return len(d.agents)
}

// Agents returns a snapshot of all agent statuses for inspection.
// The returned SupervisedAgentStatus structs are safe to use without synchronization.
func (d *Daemon) Agents() []SupervisedAgentStatus {
	result := make([]SupervisedAgentStatus, len(d.agents))
	for i, ap := range d.agents {
		ap.mu.Lock()
		result[i] = SupervisedAgentStatus{
			Worktree:       ap.entry.Worktree,
			Role:           ap.entry.Role,
			WorktreePath:   ap.worktreePath,
			PID:            ap.pid,
			RestartCount:   ap.restartCount,
			LastStart:      ap.lastStart,
			LastExit:       ap.lastExit,
			LastExitCode:   ap.lastExitCode,
			AssignedEpicID: ap.assignedEpicID,
		}
		ap.mu.Unlock()
	}
	return result
}
