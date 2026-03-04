package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
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

	epicAssigner *EpicAssigner       // manages epic-to-worktree assignments
	concurrency  *ConcurrencyTracker // enforces per-role concurrency limits
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
		concurrency:  NewConcurrencyTracker(config.Roles),
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
