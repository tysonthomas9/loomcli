package cli

import (
	"fmt"
	"log/slog"
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

	stopCh   chan struct{} // closed to signal this specific agent to stop (created in Start/addAgent)
	done     chan struct{} // closed when superviseAgent goroutine exits
	stopOnce sync.Once     // prevents double-close of stopCh

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

	// agents is the list of supervised agents. Protected by agentsMu for
	// concurrent access — readers acquire RLock, writers (addAgent/drainAgent) acquire Lock.
	agents   []*AgentProcess
	agentsMu sync.RWMutex // protects the agents slice for concurrent read/write access

	shutdown     chan struct{}  // closed to signal shutdown
	shutdownOnce sync.Once      // protects shutdown channel from double-close
	wg           sync.WaitGroup // tracks superviseAgent goroutines

	concurrency *ConcurrencyTracker // enforces per-role concurrency limits
	eventBus    events.Emitter      // event emission for observability (nil-safe via NopBus default)
	repos       []RepoConfig        // workspace repos for resolveAgentRepos; nil outside workspace mode
	workspaceID string              // stable workspace UUID for log namespacing; empty outside workspace mode

	configHash  string       // SHA-256 hash of current running config for no-op detection
	reconcileMu sync.RWMutex // serializes config writes; readers hold RLock when accessing d.config
}

// configSnapshot returns a snapshot of the current config pointer under RLock.
// Safe for concurrent use with reloadAndReconcile which swaps d.config under Lock.
func (d *Daemon) configSnapshot() *DaemonConfig {
	d.reconcileMu.RLock()
	cfg := d.config
	d.reconcileMu.RUnlock()
	return cfg
}

// findRepoConfig looks up a RepoConfig by name in d.repos.
// Returns nil if repoName is empty or not found (non-workspace mode).
func (d *Daemon) findRepoConfig(repoName string) *RepoConfig {
	if repoName == "" {
		return nil
	}
	for i := range d.repos {
		if d.repos[i].Name == repoName {
			return &d.repos[i]
		}
	}
	return nil
}

// emitEvent is a convenience helper that emits an event via the daemon's event bus.
// If the bus is nil (e.g., in tests that construct Daemon directly), it silently returns.
func (d *Daemon) emitEvent(evt events.Event) {
	if d.eventBus == nil {
		return
	}
	if err := d.eventBus.Emit(evt); err != nil {
		slog.Warn("failed to emit event", "event_type", evt.Type, "err", err)
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
		config:      config,
		projectDir:  projectDir,
		agents:      make([]*AgentProcess, 0, len(config.Agents)),
		concurrency: NewConcurrencyTracker(config.Roles),
		eventBus:    eventBus,
	}

	// Load workspace repos and ID for source repo resolution and log namespacing (best-effort)
	if ws, err := ResolveActiveWorkspace(); err == nil && ws != nil {
		d.repos = ws.Repos
		d.workspaceID = ws.ID
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
			repoConfig:   d.findRepoConfig(entry.Repo),
		}
		d.agents = append(d.agents, ap)
	}

	return d, nil
}
