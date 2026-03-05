package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// AgentProcess tracks a single supervised agent subprocess.
type AgentProcess struct {
	entry        AgentEntry // config from loom.yaml
	roleConfig   RoleConfig // resolved role configuration
	worktreePath string     // resolved worktree path

	cmd         *exec.Cmd // current subprocess (nil when not running)
	pid         int       // PID of current subprocess (0 when not running)
	logFile     *os.File  // log file handle for subprocess output (nil if not logging)
	logFilePath string    // path to agent log file for watchdog stat checks

	restartCount   int       // consecutive restart attempts
	lastStart      time.Time // when subprocess was last spawned
	lastExit       time.Time // when subprocess last exited
	lastExitCode   int       // exit code from last run
	assignedEpicID string    // epic this agent is currently assigned to (empty = non-epic mode)

	lastError      *agenterr.AgentError // classified error from most recent exit (nil on clean exit)
	rateRetryCount int                  // consecutive rate-limit retries (separate from restartCount)
	lastNoWork     bool                 // true if last exit was due to no claimable tasks

	currentBackendIdx int // 0=primary, 1+=fallback index into entry.FallbackBackends

	mu sync.Mutex // protects cmd, pid, logFile, restart tracking, assignedEpicID, lastError, currentBackendIdx
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
	CurrentBackend string // effective backend (includes failover state)
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

	epicAssigner *EpicAssigner       // manages epic-to-worktree assignments
	concurrency  *ConcurrencyTracker // enforces per-role concurrency limits
	eventBus     events.Emitter      // event emission for observability (nil-safe via NopBus default)
}

// emitEvent is a convenience helper that emits an event via the daemon's event bus.
// If the bus is nil (e.g., in tests that construct Daemon directly), it silently returns.
func (d *Daemon) emitEvent(evt events.Event) {
	if d.eventBus == nil {
		return
	}
	if err := d.eventBus.Emit(evt); err != nil {
		log.Printf("[daemon] Failed to emit %s event: %v", evt.Type, err)
	}
}

// builtInRoles defines the built-in role names that use loom <role> command.
var builtInRoles = map[string]bool{
	"plan": true,
	"task": true,
}

// NewDaemon creates a daemon from the loaded config.
// If eventBus is nil, a NopBus is used (events are silently discarded).
func NewDaemon(config *DaemonConfig, projectDir string, eventBus events.Emitter) (*Daemon, error) {
	if config == nil {
		return nil, fmt.Errorf("daemon config is nil")
	}
	if len(config.Agents) == 0 {
		return nil, fmt.Errorf("no agents configured in loom.yaml")
	}

	if eventBus == nil {
		eventBus = events.NopBus{}
	}

	d := &Daemon{
		config:       config,
		projectDir:   projectDir,
		agents:       make([]*AgentProcess, 0, len(config.Agents)),
		epicAssigner: NewEpicAssigner(),
		concurrency:  NewConcurrencyTracker(config.Roles),
		eventBus:     eventBus,
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

// resetWorktreeBranches moves all worktrees back to their default
// (worktree-named) branches. This prevents cross-checkout deadlocks
// when epic assignments differ from a prior daemon run — git refuses
// to checkout a branch that is already checked out in another worktree.
func (d *Daemon) resetWorktreeBranches() {
	for _, ap := range d.agents {
		current, err := GetCurrentBranch(ap.worktreePath)
		if err != nil {
			log.Printf("[daemon] Warning: failed to get branch for %s: %v", ap.entry.Worktree, err)
			continue
		}
		defaultBranch := ap.entry.Worktree
		if current == defaultBranch {
			continue
		}
		log.Printf("[daemon] Resetting worktree %s from %s to %s", ap.entry.Worktree, current, defaultBranch)
		// Create WIP commit if dirty
		clean, _ := IsCleanWorkingTree(ap.worktreePath)
		if !clean {
			msg := fmt.Sprintf("WIP: daemon startup reset from %s", current)
			if err := commitWIP(ap.worktreePath, msg); err != nil {
				log.Printf("[daemon] Warning: WIP commit failed for %s: %v", ap.entry.Worktree, err)
			}
		}
		if err := GitCheckout(ap.worktreePath, defaultBranch); err != nil {
			log.Printf("[daemon] Warning: failed to reset worktree %s to %s: %v", ap.entry.Worktree, defaultBranch, err)
		}
	}
}

// Start launches supervisor goroutines for all configured agents.
func (d *Daemon) Start() error {
	d.shutdown = make(chan struct{})

	// Reset all worktrees to their default branches to prevent
	// cross-checkout conflicts from prior daemon runs.
	d.resetWorktreeBranches()

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

	// Unblock any agents waiting for concurrency slots
	d.concurrency.Close()

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
		if err := d.recoverAgent(ap, 0); err != nil {
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

		// Emit epic_assigned event if an epic was assigned
		if epicID != "" {
			if evt, err := events.NewEvent(events.EpicAssigned, ap.entry.Worktree, ap.entry.Role, epicID, events.EpicAssignedData{EpicID: epicID}); err == nil {
				d.emitEvent(evt)
			}
		}

		// 2. Ensure correct branch for epic assignment
		targetBranch := ap.entry.Worktree // default: agent-name branch
		if epicID != "" {
			targetBranch = epicBranchName(epicID)
		}
		log.Printf("[daemon] Agent %s: ensuring branch %s", ap.entry.Worktree, targetBranch)
		if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, "origin/main"); err != nil {
			log.Printf("[daemon] Agent %s: branch setup failed: %v", ap.entry.Worktree, err)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 3. Spawn subprocess
		if err := d.spawnAgent(ap); err != nil {
			log.Printf("[daemon] Agent %s: spawn failed: %v", ap.entry.Worktree, err)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 4. Wait for exit
		exitCode := d.waitForAgent(ap)

		// 4.5. Classify error and detect NoWork (before recovery clears lock file)
		d.classifyAgentExit(ap, exitCode)

		// 4.7. Checkpoint management (save on error, clear on success)
		d.handleAgentCheckpoint(ap, exitCode)

		// 5. Post-mortem recovery (exit-code-aware)
		if err := d.recoverAgent(ap, exitCode); err != nil {
			log.Printf("[daemon] Agent %s: post-mortem recovery failed: %v", ap.entry.Worktree, err)
			// Non-fatal, continue with restart logic
		}

		// 5.5. Ensure PR exists for epic branch (non-fatal)
		ap.mu.Lock()
		currentEpicID := ap.assignedEpicID
		ap.mu.Unlock()
		if currentEpicID != "" {
			if err := EnsureEpicPR(ap.worktreePath, currentEpicID, d.eventBus); err != nil {
				log.Printf("[daemon] Agent %s: PR creation failed: %v", ap.entry.Worktree, err)
				// Non-fatal — don't block restart
			}
		}

		// 5.6. Release epic assignment so next iteration re-evaluates
		d.epicAssigner.ReleaseWorktree(ap.entry.Worktree)

		// 6. Epic exhaustion check and reassignment
		if err := d.handleEpicTransition(ap); err != nil {
			log.Printf("[daemon] Agent %s: epic transition failed: %v", ap.entry.Worktree, err)
			// Non-fatal: agent will respawn in current mode
		}

		// 7. Check shutdown after subprocess exit
		select {
		case <-d.shutdown:
			log.Printf("[daemon] Agent %s: shutdown signal received after exit", ap.entry.Worktree)
			return
		default:
		}

		// 7.5. Check for backend failover (before restart decision)
		if d.tryFallbackBackend(ap) {
			log.Printf("[daemon] Agent %s: backend failover triggered, retrying with %s",
				ap.entry.Worktree, d.getEffectiveBackend(ap))
			continue
		}

		// 8. Restart decision
		if !d.shouldRestart(ap) {
			log.Printf("[daemon] Agent %s: max restarts exceeded, stopping supervisor", ap.entry.Worktree)
			return
		}

		// 9. Backoff sleep (interruptible)
		backoff := d.computeBackoff(ap)
		ap.mu.Lock()
		count := ap.restartCount
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: waiting %v before restart (attempt %d)", ap.entry.Worktree, backoff, count)

		// Emit agent_restarted event
		if evt, err := events.NewEvent(events.AgentRestarted, ap.entry.Worktree, ap.entry.Role, "", events.AgentRestartedData{PID: 0, RestartCount: count}); err == nil {
			d.emitEvent(evt)
		}

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

// getEffectiveBackend returns the backend name for the agent's current failover position.
// Index 0 = primary (ap.entry.Backend or d.config.Backend), index 1+ = FallbackBackends[idx-1].
func (d *Daemon) getEffectiveBackend(ap *AgentProcess) string {
	ap.mu.Lock()
	idx := ap.currentBackendIdx
	ap.mu.Unlock()

	if idx == 0 {
		b := ap.entry.Backend
		if b == "" {
			b = ap.roleConfig.Backend
		}
		if b == "" && d.config != nil {
			b = d.config.Backend
		}
		return b
	}

	fbIdx := idx - 1
	if fbIdx < len(ap.entry.FallbackBackends) {
		return ap.entry.FallbackBackends[fbIdx]
	}

	// Beyond fallback list — return primary (caller should have prevented this)
	b := ap.entry.Backend
	if b == "" && d.config != nil {
		b = d.config.Backend
	}
	return b
}

// tryFallbackBackend checks if the agent should fail over to the next backend.
// Returns true if failover was triggered (caller should skip normal restart counting).
// Returns false if no failover is needed or all backends are exhausted.
func (d *Daemon) tryFallbackBackend(ap *AgentProcess) bool {
	ap.mu.Lock()
	lastErr := ap.lastError
	rateCount := ap.rateRetryCount
	currentIdx := ap.currentBackendIdx
	numFallbacks := len(ap.entry.FallbackBackends)

	if lastErr == nil || numFallbacks == 0 {
		ap.mu.Unlock()
		return false
	}

	// Determine if failover should trigger
	shouldFailover := false
	switch {
	case lastErr.Class == agenterr.ModelNotFound:
		shouldFailover = true
	case lastErr.Class == agenterr.RateLimited && rateCount > 3:
		shouldFailover = true
	}

	if !shouldFailover {
		ap.mu.Unlock()
		return false
	}

	// Check if there's a next backend to try
	totalBackends := 1 + numFallbacks
	nextIdx := currentIdx + 1
	if nextIdx >= totalBackends {
		worktree := ap.entry.Worktree
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: all backends exhausted (tried %d), no more fallbacks",
			worktree, totalBackends)
		return false
	}

	// Switch to next backend (still holding the lock — no TOCTOU gap)
	ap.currentBackendIdx = nextIdx
	ap.restartCount = 0
	ap.rateRetryCount = 0
	ap.mu.Unlock()

	// Resolve backend name outside the lock for logging (getEffectiveBackend acquires ap.mu)
	nextBackend := d.getEffectiveBackend(ap)
	log.Printf("[daemon] Agent %s: failing over from backend index %d to %d (%s)",
		ap.entry.Worktree, currentIdx, nextIdx, nextBackend)

	return true
}

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

	return cmd
}

// spawnAgent starts the subprocess for an agent.
func (d *Daemon) spawnAgent(ap *AgentProcess) error {
	cmd := d.buildCommand(ap)

	ap.mu.Lock()
	defer ap.mu.Unlock()

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
		return fmt.Errorf("failed to start subprocess: %w", err)
	}

	ap.cmd = cmd
	ap.pid = cmd.Process.Pid
	ap.lastStart = time.Now()

	log.Printf("[daemon] Agent %s: spawned subprocess PID %d", ap.entry.Worktree, ap.pid)

	// Emit agent_started event (best-effort)
	if evt, err := events.NewEvent(events.AgentStarted, ap.entry.Worktree, ap.entry.Role, ap.assignedEpicID, events.AgentStartedData{PID: ap.pid}); err == nil {
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

// classifyAgentExit reads the lock file (before recovery clears it) and classifies
// the agent's exit into an error class. Sets ap.lastError and ap.lastNoWork.
func (d *Daemon) classifyAgentExit(ap *AgentProcess, exitCode int) {
	// Read lock info before recovery clears it (for logging and NoWork detection)
	lockInfo, _, _ := CheckLock(ap.worktreePath)
	if lockInfo != nil && lockInfo.TaskID != "" {
		log.Printf("[daemon] Agent %s: exited with code %d (task %s: %s)",
			ap.entry.Worktree, exitCode, lockInfo.TaskID, lockInfo.TaskTitle)
	} else {
		log.Printf("[daemon] Agent %s: exited with code %d", ap.entry.Worktree, exitCode)
	}

	// Resolve backend for classification
	ap.mu.Lock()
	backend := ap.entry.Backend
	logPath := ap.logFilePath
	ap.mu.Unlock()
	if backend == "" {
		backend = d.config.Backend
	}

	if exitCode == 0 && (lockInfo == nil || lockInfo.TaskID == "") {
		// No work available — exit 0 with no task claimed
		ap.mu.Lock()
		ap.lastError = &agenterr.AgentError{
			Class:   agenterr.NoWork,
			Message: "no claimable tasks",
			Backend: backend,
		}
		ap.lastNoWork = true
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: no work available (idle)", ap.entry.Worktree)
	} else if exitCode != 0 {
		ae := agenterr.ClassifyFromLog(logPath, exitCode, backend)
		ap.mu.Lock()
		ap.lastError = ae
		ap.lastNoWork = false
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: classified error: %v", ap.entry.Worktree, ae)
	} else {
		ap.mu.Lock()
		ap.lastError = nil
		ap.lastNoWork = false
		ap.mu.Unlock()
	}
}

// handleAgentCheckpoint saves a checkpoint on non-zero exit (before recovery clears the
// worktree) or clears the checkpoint on successful exit.
func (d *Daemon) handleAgentCheckpoint(ap *AgentProcess, exitCode int) {
	if exitCode == 0 {
		lockDir := ResolveLockDir(ap.worktreePath)
		if err := ClearCheckpoint(lockDir); err != nil {
			log.Printf("[daemon] Agent %s: failed to clear checkpoint: %v", ap.entry.Worktree, err)
		}
		return
	}
	d.saveAgentCheckpoint(ap, exitCode)
}

// saveAgentCheckpoint captures the current worktree diff and agent state into a
// checkpoint file. Called when an agent exits non-zero before recovery clears the worktree.
func (d *Daemon) saveAgentCheckpoint(ap *AgentProcess, exitCode int) {
	lockInfo, _, _ := CheckLock(ap.worktreePath)
	if lockInfo == nil || lockInfo.TaskID == "" {
		return
	}

	diff := captureGitDiff(ap.worktreePath, maxDiffBytes)
	errClass := ""
	ap.mu.Lock()
	if ap.lastError != nil {
		errClass = ap.lastError.Class.String()
	}
	epicID := ap.assignedEpicID
	ap.mu.Unlock()

	cp := &Checkpoint{
		AgentName:  lockInfo.AgentName,
		TaskID:     lockInfo.TaskID,
		EpicID:     epicID,
		GitDiff:    diff,
		ExitCode:   exitCode,
		ErrorClass: errClass,
		Timestamp:  time.Now(),
	}
	lockDir := ResolveLockDir(ap.worktreePath)
	if err := SaveCheckpoint(lockDir, cp); err != nil {
		log.Printf("[daemon] Agent %s: failed to save checkpoint: %v", ap.entry.Worktree, err)
	} else {
		log.Printf("[daemon] Agent %s: saved checkpoint for task %s", ap.entry.Worktree, lockInfo.TaskID)
	}
}

// shouldRestart determines if agent should restart based on backoff policy
// and the classified error from the most recent exit.
func (d *Daemon) shouldRestart(ap *AgentProcess) bool {
	maxRetries := d.getMaxRetries()

	ap.mu.Lock()
	defer ap.mu.Unlock()

	// Successful run (exit 0, ran for >1 minute) resets all counters
	if ap.lastExitCode == 0 && time.Since(ap.lastStart) > time.Minute {
		ap.restartCount = 0
		ap.rateRetryCount = 0
		ap.currentBackendIdx = 0 // reset to primary backend
		return true
	}

	// NoWork: no claimable tasks — always restart, never count toward max_retries.
	// Preserve currentBackendIdx: NoWork is about task availability, not backend health.
	// If the agent failed over to a fallback backend, it should stay on that backend.
	if ap.lastError != nil && ap.lastError.Class == agenterr.NoWork {
		ap.restartCount = 0
		ap.rateRetryCount = 0
		return true
	}

	// Fatal errors: stop immediately, no retries
	if ap.lastError != nil && ap.lastError.IsFatal() {
		log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
			ap.entry.Worktree, ap.lastError.Class)
		return false
	}

	// Rate-limited: unlimited retries (don't count toward max_retries)
	if ap.lastError != nil && ap.lastError.Class == agenterr.RateLimited && d.getRateLimitNoCount() {
		ap.rateRetryCount++
		log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
			ap.entry.Worktree, ap.rateRetryCount)
		return true
	}

	// All other errors: count toward max_retries
	ap.restartCount++
	ap.rateRetryCount = 0 // reset rate counter on non-rate error
	return ap.restartCount <= maxRetries
}

// computeBackoff returns the sleep duration before next restart.
// Uses error-class-specific initial values and caps.
func (d *Daemon) computeBackoff(ap *AgentProcess) time.Duration {
	maxBackoff := d.getBackoffMax()

	ap.mu.Lock()
	lastErr := ap.lastError
	count := ap.restartCount
	rateCount := ap.rateRetryCount
	ap.mu.Unlock()

	// NoWork: fixed interval, no exponential growth
	if lastErr != nil && lastErr.Class == agenterr.NoWork {
		return time.Duration(d.getNoWorkBackoff()) * time.Second
	}

	// Select initial backoff and retry count based on error class
	var initial int
	var retryN int
	if lastErr != nil && lastErr.Class == agenterr.RateLimited {
		initial = d.getRateLimitBackoff()
		retryN = rateCount
		maxBackoff = d.getRateLimitMaxWait()
	} else if lastErr != nil && lastErr.Class == agenterr.Timeout {
		initial = d.getTimeoutBackoff()
		retryN = count
	} else {
		initial = d.getBackoffInitial()
		retryN = count
	}

	// Cap count to prevent integer overflow in bit shift
	if retryN > 30 {
		retryN = 30
	}

	// Exponential: initial * 2^retryN
	backoffSec := initial * (1 << retryN)
	if backoffSec > maxBackoff || backoffSec < 0 {
		backoffSec = maxBackoff
	}

	backoff := time.Duration(backoffSec) * time.Second

	// For rate limits, respect server Retry-After hint if larger
	if lastErr != nil && lastErr.Class == agenterr.RateLimited && lastErr.RetryAfter > backoff {
		backoff = lastErr.RetryAfter
		if backoff > time.Duration(maxBackoff)*time.Second {
			backoff = time.Duration(maxBackoff) * time.Second
		}
	}

	return backoff
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

func (d *Daemon) getOutputTimeout() int {
	if d.config.Daemon.RestartPolicy.OutputTimeout != nil {
		return *d.config.Daemon.RestartPolicy.OutputTimeout
	}
	return 900 // default: 15 minutes
}

func (d *Daemon) getRateLimitBackoff() int {
	if d.config.Daemon.RestartPolicy.RateLimitBackoff != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitBackoff
	}
	return 30 // default seconds
}

func (d *Daemon) getRateLimitMaxWait() int {
	if d.config.Daemon.RestartPolicy.RateLimitMaxWait != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitMaxWait
	}
	return 300 // default seconds
}

func (d *Daemon) getRateLimitNoCount() bool {
	if d.config.Daemon.RestartPolicy.RateLimitNoCount != nil {
		return *d.config.Daemon.RestartPolicy.RateLimitNoCount
	}
	return true // default: rate-limit retries don't count toward max_retries
}

func (d *Daemon) getTimeoutBackoff() int {
	if d.config.Daemon.RestartPolicy.TimeoutBackoff != nil {
		return *d.config.Daemon.RestartPolicy.TimeoutBackoff
	}
	return 5 // default seconds
}

func (d *Daemon) getNoWorkBackoff() int {
	if d.config.Daemon.RestartPolicy.NoWorkBackoff != nil {
		return *d.config.Daemon.RestartPolicy.NoWorkBackoff
	}
	return 30 // default seconds
}

func (d *Daemon) getIdlePollInterval() int {
	if d.config.Daemon.RestartPolicy.IdlePollInterval != nil {
		return *d.config.Daemon.RestartPolicy.IdlePollInterval
	}
	return 30 // default seconds
}

// stopAgent sends SIGTERM then SIGKILL to a single agent and its entire process group.
// This function is safe to call concurrently with waitForAgent.
// It uses polling instead of cmd.Wait() to avoid double-wait issues.
// The process group kill ensures child processes (e.g. codex) are not orphaned.
func (d *Daemon) stopAgent(ap *AgentProcess) {
	ap.mu.Lock()
	proc := ap.cmd
	pid := ap.pid
	ap.mu.Unlock()

	if proc == nil || proc.Process == nil || pid == 0 {
		return
	}

	log.Printf("[daemon] Agent %s: sending SIGTERM to process group %d", ap.entry.Worktree, pid)

	// Send SIGTERM to the entire process group (negative PID)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process group may have already exited; try the process directly
		log.Printf("[daemon] Agent %s: SIGTERM to process group failed: %v (trying process directly)", ap.entry.Worktree, err)
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("[daemon] Agent %s: SIGTERM failed (process may have exited): %v", ap.entry.Worktree, err)
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
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		// Also check if process is still running via OS
		if !lockfile.IsProcessRunning(pid) {
			log.Printf("[daemon] Agent %s: process exited gracefully", ap.entry.Worktree)
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	// Force kill the entire process group if still running
	ap.mu.Lock()
	stillRunning := ap.pid != 0
	ap.mu.Unlock()

	if stillRunning {
		log.Printf("[daemon] Agent %s: sending SIGKILL to process group %d", ap.entry.Worktree, pid)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
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
	outputTimeout := d.getOutputTimeout()
	var totalAgents, healthyAgents int

	for _, ap := range d.agents {
		ap.mu.Lock()
		pid := ap.pid
		worktreePath := ap.worktreePath
		worktreeName := ap.entry.Worktree
		logPath := ap.logFilePath
		lastStart := ap.lastStart
		ap.mu.Unlock()

		totalAgents++

		if pid == 0 {
			continue // Not running
		}

		// Check if PID is alive
		if !lockfile.IsProcessRunning(pid) {
			// Process died unexpectedly - superviseAgent will detect via cmd.Wait()
			log.Printf("[daemon] Agent %s (PID %d) is not running", worktreeName, pid)
		} else {
			healthyAgents++
		}

		// Check lock file for stale state
		lockInfo, isRunning, err := CheckLock(worktreePath)
		if err == nil && lockInfo != nil && !isRunning {
			log.Printf("[daemon] Stale lock detected for agent %s", worktreeName)
		}

		// Watchdog: kill agent if no log output for outputTimeout seconds
		if outputTimeout > 0 && logPath != "" {
			if info, err := os.Stat(logPath); err == nil {
				lastOutput := info.ModTime()
				// Use lastStart if log hasn't been written yet (agent just spawned)
				if lastOutput.Before(lastStart) {
					lastOutput = lastStart
				}
				silent := time.Since(lastOutput)
				threshold := time.Duration(outputTimeout) * time.Second
				if silent > threshold {
					log.Printf("[daemon] Agent %s: no output for %v (threshold %ds), killing hung process",
						worktreeName, silent.Truncate(time.Second), outputTimeout)
					d.stopAgent(ap)
				}
			}
		}
	}

	// Emit health_check summary event
	if evt, err := events.NewEvent(events.HealthCheck, "", "", "", events.HealthCheckData{AgentCount: totalAgents, HealthyCount: healthyAgents}); err == nil {
		d.emitEvent(evt)
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
		// Resolve backend name outside the lock (getEffectiveBackend acquires ap.mu)
		result[i].CurrentBackend = d.getEffectiveBackend(ap)
	}
	return result
}
