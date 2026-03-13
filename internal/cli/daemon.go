package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// AgentProcess tracks a single supervised agent subprocess.
type AgentProcess struct {
	entry        AgentEntry  // config from loom.yaml
	roleConfig   RoleConfig  // resolved role configuration
	worktreePath string      // resolved worktree path
	repoConfig   *RepoConfig // per-repo config (nil in non-workspace mode)

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

// resolveRemote returns the git remote name for this agent.
// Uses repoConfig.Remote if available, otherwise defaults to "origin".
func (ap *AgentProcess) resolveRemote() string {
	if ap.repoConfig != nil && ap.repoConfig.Remote != "" {
		return ap.repoConfig.Remote
	}
	return "origin"
}

// resolveRemoteBranch returns the full remote/branch ref for this agent
// (e.g. "origin/main"). Uses repoConfig if available, otherwise defaults
// to "origin/main".
func (ap *AgentProcess) resolveRemoteBranch() string {
	if ap.repoConfig != nil {
		remote := ap.repoConfig.Remote
		if remote == "" {
			remote = "origin"
		}
		branch := ap.repoConfig.DefaultBranch
		if branch == "" {
			branch = "main"
		}
		return remote + "/" + branch
	}
	return "origin/main"
}

// SupervisedAgentStatus is a snapshot of a supervised agent's state for external inspection.
// This type is safe to copy and does not contain a mutex.
type SupervisedAgentStatus struct {
	Worktree       string
	Role           string
	Repo           string
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
		// Resolve worktree path (handles per-repo routing when entry.Repo is set)
		target, err := ResolveAgentTarget(entry.Worktree, entry.Repo)
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

		// Acquire concurrency slot for this role (blocks if at limit)
		if !d.concurrency.Acquire(ap.entry.Role) {
			log.Printf("[daemon] Agent %s: concurrency tracker closed, exiting", ap.entry.Worktree)
			return
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
		if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, ap.resolveRemote(), ap.resolveRemoteBranch()); err != nil {
			log.Printf("[daemon] Agent %s: branch setup failed: %v", ap.entry.Worktree, err)
			d.concurrency.Release(ap.entry.Role)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 3. Spawn subprocess
		if err := d.spawnAgent(ap); err != nil {
			log.Printf("[daemon] Agent %s: spawn failed: %v", ap.entry.Worktree, err)
			d.concurrency.Release(ap.entry.Role)
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

		// 5.7. Release concurrency slot so waiting agents can proceed
		d.concurrency.Release(ap.entry.Role)

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
			Repo:           ap.entry.Repo,
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
