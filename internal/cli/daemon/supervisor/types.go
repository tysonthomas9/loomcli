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
	// LifecycleGenerationAt identifies when this in-memory agent generation was
	// created. It is immutable and lets durable restart recovery prove that a
	// post-command generation already exists instead of restarting twice.
	LifecycleGenerationAt time.Time

	Cmd                    *exec.Cmd         // current subprocess (nil when not running)
	Pid                    int               // PID of current subprocess (0 when not running)
	LogFile                *os.File          // log file handle for subprocess output (nil if not logging)
	LogFilePath            string            // path to agent log file for watchdog stat checks
	ArchiveLogFile         *os.File          // canonical agent archive (~/.loom/logs/<ws>/agents/<worktree>.log) the web UI Logs tab reads; nil if unavailable
	TranscriptPath         string            // path to session transcript.jsonl for watchdog liveness (set by superviseAgent)
	Session                *sessions.Session // daemon-created session handle (nil when no session active)
	AgentSessionID         string            // fleet-db control-plane session id (empty when no session active)
	ParentSessionID        string            // lead/orchestration session that requested this run (empty when unattached)
	AgentLeaseID           string            // fleet-db control-plane lease id (empty when no lease active)
	AgentLeaseToken        string            // fleet-db control-plane lease token (empty when no lease active)
	OwnershipLeaseID       string            // fleet-db logical-agent ownership lease id (empty when not owner)
	OwnershipOwnerID       string            // stable owner identity bound by the ownership lease
	OwnershipNodeID        string            // daemon process identity bound by the ownership lease
	OwnershipLeaseToken    string            // fleet-db logical-agent ownership lease token (empty when not owner)
	OwnershipFencingToken  int64             // fencing token for logical-agent ownership
	OwnershipLastHeartbeat time.Time         // last successful ownership heartbeat (server-derived; display/telemetry only)
	OwnershipRenewedAt     time.Time         // local-clock anchor captured just before the last confirmed acquire/renew was sent; drives the bounded fail-open validity window — never server-derived
	// OwnershipAcquired is closed after this AgentProcess first acquires its
	// logical-agent ownership lease. Lifecycle Restart waits on this explicit
	// signal before reporting success; it must not infer authority from the
	// Agent projection or mere presence in the supervisor slice.
	OwnershipAcquired chan struct{}
	ownershipReady    sync.Once
	// ownershipCommandReserved keeps a newly restarted process's ownership
	// lease alive until the durable Restart completion is fenced. If the
	// supervised process exits first, release is deferred and performed when
	// the command releases this reservation.
	ownershipCommandReserved bool
	ownershipReleasePending  bool
	BeforeRef                string       // git HEAD ref before spawn (for diff stats at finalization)
	AssignedTaskID           string       // task claimed by supervisor preflight for this run
	RequestedTaskID          string       // task requested by a lifecycle command before normal queue selection
	ResumeTaskID             string       // interrupted task to re-claim this cycle (detected from a surviving crash-remnant lock); drives claimResumeTask for BOTH resume and checkpoint recovery. Per-cycle: cleared in clearAgentSessionState, re-detected in preFlightSetup
	ResumeFailures           int          // consecutive failed RECOVERY attempts — resume AND checkpoint fallback (PERSISTS across cycles); escalation: resume×maxResumeFailures → checkpoint×1 → cold-start
	RecoveryMode             recoveryMode // this cycle's recovery classification (resume|checkpoint|cold); per-cycle, set in preFlightSetup, read by recordResumeOutcome to decide whether the run's outcome advances ResumeFailures
	LastActivity             time.Time    // most recent PTY output observed by the agent's wrapper (driven by agent IPC heartbeats); zero between spawn and first observation

	RestartCount   int       // consecutive restart attempts
	LastStart      time.Time // when subprocess was last spawned
	LastExit       time.Time // when subprocess last exited
	LastExitCode   int       // exit code from last run
	AssignedEpicID string    // epic this agent is currently assigned to (empty = non-epic mode)

	LastError      *agenterr.AgentError // classified error from most recent exit (nil on clean exit)
	RateRetryCount int                  // consecutive rate-limit retries (separate from RestartCount)
	LastNoWork     bool                 // true if last exit was due to no claimable tasks
	NoWorkCount    int                  // consecutive NoWork exits (reset on non-NoWork exit)
	BlockCount     int                  // block cycles since the last successful run (drives BlockBudget escalation; display-only in the state file, never hydrated across daemon restarts)

	CurrentBackendIdx int       // 0=primary, 1+=fallback index into Entry.FallbackBackends
	BackoffUntil      time.Time // when current backoff sleep ends (zero if not in backoff)

	StopCh   chan struct{} // closed to signal this specific agent to stop (created in Start/addAgent)
	Done     chan struct{} // closed when superviseAgent goroutine exits
	StopOnce sync.Once     // prevents double-close of StopCh

	StopReason StopReason // why the agent was stopped (set at decision site, empty while running)

	// StartStopMu linearizes the claim-to-spawn transition with Stop. The
	// supervisor holds it from its final pre-claim stop check until spawnAgent
	// publishes Cmd/Pid. Drain paths take it before closing StopCh, so a stop
	// either prevents the claim or observes the process it must drain.
	//
	// Lock ordering: StartStopMu -> Mu. Never wait for a subprocess or backoff
	// while holding StartStopMu.
	StartStopMu sync.Mutex

	// SessionHeartbeatMu is a lifecycle barrier. Heartbeat RPCs hold a read
	// lock; starting-to-running transitions and terminal retirement hold the
	// write lock. Retirement drains earlier heartbeats and clears the session
	// IDs before releasing, so queued jobs become no-ops without holding the
	// barrier through slow transcript, Git, artifact, or completion work.
	SessionHeartbeatMu sync.RWMutex
	// ownershipOpMu serializes ownership Acquire/Heartbeat/Release from request
	// construction through local state publication. Lock order is
	// ownershipOpMu -> Mu. Fleet rotates the raw token on every Acquire, so a
	// delayed operation from an older AgentProcess is fenced server-side while
	// this mutex prevents out-of-order resurrection within one AgentProcess.
	ownershipOpMu sync.Mutex
	Mu            sync.Mutex // protects Cmd, Pid, LogFile, restart tracking, AssignedEpicID, AssignedTaskID, RequestedTaskID, ResumeTaskID, ResumeFailures, RecoveryMode, LastError, CurrentBackendIdx, Session, AgentSessionID, ParentSessionID, AgentLeaseID, AgentLeaseToken, ownership fields/reservation, TranscriptPath, BeforeRef, StopReason, LastActivity
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
	// StopReasonMaxRetriesBlocked marks an agent that exhausted its restart
	// budget and is now block-and-retrying on a fixed interval (policy
	// Decision Retry with OnExhaustion Block) instead of being abandoned.
	// The supervise goroutine stays alive and the agent self-resumes once a
	// transient root cause clears.
	StopReasonMaxRetriesBlocked StopReason = "max_retries_blocked"
	// StopReasonFastFail marks a deterministic failure the policy refuses to
	// retry or block (Decision FastFail — e.g. ContextOverflow, ModelNotFound
	// with backends exhausted, or a capped block that never made progress).
	// Surfaced as "failed" in daemon-status.
	StopReasonFastFail StopReason = "fast_fail"
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
	BlockCount             int        // block cycles since the last successful run
	BackoffUntil           time.Time  // when backoff sleep ends (zero if not in backoff)
	RemoteBranch           string     // remote tracking ref (e.g. "origin/main")
	OwnershipLeaseID       string
	OwnershipFencingToken  int64
	OwnershipLastHeartbeat time.Time
	AssignedTaskID         string    // task currently claimed by this agent (empty when between tasks)
	LastActivity           time.Time // most recent PTY output observed by the wrapper; zero if no observation yet
}

// BuiltInRoles defines the built-in role names that use loom <role> command.
var BuiltInRoles = map[string]bool{
	"plan": true,
	"task": true,
}
