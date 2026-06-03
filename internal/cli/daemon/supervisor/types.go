// Package supervisor manages agent subprocess lifecycle within the daemon.
// It contains the core supervision loop, agent process management, health checking,
// restart logic, and all related types.
package supervisor

import (
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// AgentProcess tracks a single supervised agent subprocess.
type AgentProcess struct {
	Entry        cfgpkg.AgentEntry  // agent configuration from FleetDB
	RoleConfig   cfgpkg.RoleConfig  // resolved role configuration
	WorktreePath string             // resolved worktree path
	RepoConfig   *cfgpkg.RepoConfig // per-repo config (nil in non-workspace mode)

	Cmd                    *exec.Cmd         // current subprocess (nil when not running)
	Pid                    int               // PID of current subprocess (0 when not running)
	LogFile                *os.File          // log file handle for subprocess output (nil if not logging)
	LogFilePath            string            // path to agent log file for watchdog stat checks
	TranscriptPath         string            // path to session transcript.jsonl for watchdog liveness (set by superviseAgent)
	DaytonaSandboxID       string            // provider sandbox id for Daytona-backed agent runs
	DaytonaRuntimePhase    string            // provider runtime phase for Daytona-backed agent runs
	DaytonaCleanupState    string            // provider cleanup state for Daytona-backed agent runs
	Session                *sessions.Session // daemon-created session handle (nil when no session active)
	AgentSessionID         string            // fleet-db control-plane session id (empty when no session active)
	ParentSessionID        string            // lead/orchestration session that requested this run (empty when unattached)
	AgentLeaseID           string            // fleet-db control-plane lease id (empty when no lease active)
	AgentLeaseToken        string            // fleet-db control-plane lease token (empty when no lease active)
	OwnershipLeaseID       string            // fleet-db logical-agent ownership lease id (empty when not owner)
	OwnershipLeaseToken    string            // fleet-db logical-agent ownership lease token (empty when not owner)
	OwnershipFencingToken  int64             // fencing token for logical-agent ownership
	OwnershipLastHeartbeat time.Time         // last successful ownership heartbeat
	BeforeRef              string            // git HEAD ref before spawn (for diff stats at finalization)
	AssignedTaskID         string            // task claimed by supervisor preflight for this run
	RequestedTaskID        string            // task requested by a lifecycle command before normal queue selection
	LastActivity           time.Time         // most recent PTY output observed by the agent's wrapper (driven by agent IPC heartbeats); zero between spawn and first observation

	RestartCount   int       // consecutive restart attempts
	LastStart      time.Time // when subprocess was last spawned
	LastExit       time.Time // when subprocess last exited
	LastExitCode   int       // exit code from last run
	AssignedEpicID string    // epic this agent is currently assigned to (empty = non-epic mode)

	LastError      *agenterr.AgentError // classified error from most recent exit (nil on clean exit)
	RateRetryCount int                  // consecutive rate-limit retries (separate from RestartCount)
	LastNoWork     bool                 // true if last exit was due to no claimable tasks
	NoWorkCount    int                  // consecutive NoWork exits (reset on non-NoWork exit)

	CurrentBackendIdx int       // 0=primary, 1+=fallback index into Entry.FallbackBackends
	BackoffUntil      time.Time // when current backoff sleep ends (zero if not in backoff)

	StopCh   chan struct{} // closed to signal this specific agent to stop (created in Start/addAgent)
	Done     chan struct{} // closed when superviseAgent goroutine exits
	StopOnce sync.Once     // prevents double-close of StopCh

	StopReason StopReason // why the agent was stopped (set at decision site, empty while running)

	Mu sync.Mutex // protects Cmd, Pid, LogFile, restart tracking, AssignedEpicID, AssignedTaskID, RequestedTaskID, LastError, CurrentBackendIdx, Session, AgentSessionID, ParentSessionID, AgentLeaseID, AgentLeaseToken, ownership fields, TranscriptPath, DaytonaSandboxID, DaytonaRuntimePhase, DaytonaCleanupState, BeforeRef, StopReason, LastActivity
}

// StopReason identifies why an agent was stopped.
type StopReason string

const (
	StopReasonNoWork             StopReason = "no_work"
	StopReasonRateLimited        StopReason = "rate_limited"
	StopReasonMaxRetries         StopReason = "max_retries"
	StopReasonFatalError         StopReason = "fatal_error"
	StopReasonManualStop         StopReason = "manual_stop"
	StopReasonConfigRemoved      StopReason = "config_removed"
	StopReasonShutdown           StopReason = "shutdown"
	StopReasonYielded            StopReason = "yielded"
	StopReasonWatchdog           StopReason = "watchdog"
	StopReasonBackendUnavailable StopReason = "backend_unavailable"
	StopReasonEphemeralDone      StopReason = "ephemeral_done" // ephemeral-mode agent exited cleanly after one successful task
)

// resolveRemote returns the git remote name for this agent.
// Uses RepoConfig.Remote if available, otherwise defaults to "origin".
func (ap *AgentProcess) ResolveRemote() string {
	if ap.RepoConfig != nil && ap.RepoConfig.Remote != "" {
		return ap.RepoConfig.Remote
	}
	return "origin"
}

// ResolveRemoteBranch returns the full remote/branch ref for this agent
// (e.g. "origin/main"). Uses RepoConfig if available, otherwise defaults
// to "origin/main".
func (ap *AgentProcess) ResolveRemoteBranch() string {
	if ap.RepoConfig != nil {
		remote := ap.RepoConfig.Remote
		if remote == "" {
			remote = "origin"
		}
		branch := ap.RepoConfig.DefaultBranch
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
	Worktree               string
	Role                   string
	Repo                   string
	WorktreePath           string
	PID                    int
	RestartCount           int
	LastStart              time.Time
	LastExit               time.Time
	LastExitCode           int
	AssignedEpicID         string
	CurrentBackend         string     // effective backend (includes failover state)
	StopReason             StopReason // why the agent stopped (empty while running)
	LastErrorClass         string     // string representation of last error class (e.g. "RateLimited")
	NoWorkCount            int        // consecutive NoWork exits
	BackoffUntil           time.Time  // when backoff sleep ends (zero if not in backoff)
	RemoteBranch           string     // remote tracking ref (e.g. "origin/main")
	OwnershipLeaseID       string
	OwnershipFencingToken  int64
	OwnershipLastHeartbeat time.Time
	AssignedTaskID         string    // task currently claimed by this agent (empty when between tasks)
	LastActivity           time.Time // most recent PTY output observed by the wrapper; zero if no observation yet
	RuntimeProvider        string
	RuntimePhase           string
	RuntimeCleanupState    string
	DaytonaSandboxID       string
}

// BuiltInRoles defines the built-in role names that use loom <role> command.
var BuiltInRoles = map[string]bool{
	"plan": true,
	"task": true,
}
