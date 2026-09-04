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
	ArchiveLogFile         *os.File          // canonical agent archive (~/.loom/logs/<ws>/agents/<worktree>.log) the web UI Logs tab reads; nil if unavailable
	TranscriptPath         string            // path to session transcript.jsonl for watchdog liveness (set by superviseAgent)
	Session                *sessions.Session // daemon-created session handle (nil when no session active)
	AgentSessionID         string            // fleet-db control-plane session id (empty when no session active)
	ParentSessionID        string            // lead/orchestration session that requested this run (empty when unattached)
	AgentLeaseID           string            // fleet-db control-plane lease id (empty when no lease active)
	AgentLeaseToken        string            // fleet-db control-plane lease token (empty when no lease active)
	OwnershipLeaseID       string            // fleet-db logical-agent ownership lease id (empty when not owner)
	OwnershipLeaseToken    string            // fleet-db logical-agent ownership lease token (empty when not owner)
	OwnershipFencingToken  int64             // fencing token for logical-agent ownership
	OwnershipLastHeartbeat time.Time         // last successful ownership heartbeat (server-derived; display/telemetry only)
	OwnershipRenewedAt     time.Time         // local-clock anchor captured just before the last confirmed acquire/renew was sent; drives the bounded fail-open validity window — never server-derived
	BeforeRef              string            // git HEAD ref before spawn (for diff stats at finalization)
	AssignedTaskID         string            // task claimed by supervisor preflight for this run
	RequestedTaskID        string            // task requested by a lifecycle command before normal queue selection
	ResumeTaskID           string            // interrupted task to re-claim this cycle (detected from a surviving crash-remnant lock); drives claimResumeTask for BOTH resume and checkpoint recovery. Per-cycle: cleared in clearAgentSessionState, re-detected in preFlightSetup
	ResumeFailures         int               // consecutive failed RECOVERY attempts — resume AND checkpoint fallback (PERSISTS across cycles); escalation: resume×maxResumeFailures → checkpoint×1 → cold-start
	RecoveryMode           recoveryMode      // this cycle's recovery classification (resume|checkpoint|cold); per-cycle, set in preFlightSetup, read by recordResumeOutcome to decide whether the run's outcome advances ResumeFailures
	LastActivity           time.Time         // most recent PTY output observed by the agent's wrapper (driven by agent IPC heartbeats); zero between spawn and first observation
	InputWaitPending       int               // interactive harness prompts currently awaiting an answer; a count (not a flag) so overlapping prompts nest — see input_wait.go
	InputWaitSince         time.Time         // when InputWaitPending last rose from zero; anchors the bound that stops a suspension from outliving its cause

	RestartCount   int       // consecutive restart attempts
	LastStart      time.Time // when subprocess was last spawned
	LastExit       time.Time // when subprocess last exited
	LastExitCode   int       // exit code from last run
	AssignedEpicID string    // epic this agent is currently assigned to (empty = non-epic mode)

	SoftKnobWarning string // last soft-enforcement warning logged by gateSafetyKnobsEnforceable; deduplicates a per-poll-cycle line down to one per change

	LastError      *agenterr.AgentError // classified error from most recent exit (nil on clean exit)
	RateRetryCount int                  // consecutive rate-limit retries (separate from RestartCount)
	LastNoWork     bool                 // true if last exit was due to no claimable tasks
	NoWorkCount    int                  // consecutive NoWork exits (reset on non-NoWork exit)
	IdleSince      time.Time            // when the current NoWork streak began (zero when not idle); set on the 0→1 transition and cleared with NoWorkCount by resetNoWork
	BlockCount     int                  // block cycles since the last successful run (drives BlockBudget escalation; display-only in the state file, never hydrated across daemon restarts)

	CurrentBackendIdx int       // 0=primary, 1+=fallback index into Entry.FallbackBackends
	BackoffUntil      time.Time // when current backoff sleep ends (zero if not in backoff)

	StopCh   chan struct{} // closed to signal this specific agent to stop (created in Start/addAgent)
	Done     chan struct{} // closed when superviseAgent goroutine exits
	StopOnce sync.Once     // prevents double-close of StopCh

	StopReason StopReason // why the agent was stopped (set at decision site, empty while running)

	Mu sync.Mutex // protects Cmd, Pid, LogFile, SoftKnobWarning, restart tracking, IdleSince, AssignedEpicID, AssignedTaskID, RequestedTaskID, ResumeTaskID, ResumeFailures, RecoveryMode, LastError, CurrentBackendIdx, Session, AgentSessionID, ParentSessionID, AgentLeaseID, AgentLeaseToken, ownership fields, TranscriptPath, BeforeRef, StopReason, LastActivity, InputWaitPending, InputWaitSince
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
	// StopReasonRunDurationExceeded marks a run the supervisor killed for
	// outliving its wall-clock cap (see run_duration.go).
	//
	// Deliberately NOT folded into StopReasonWatchdog. That reason means "silent
	// too long"; this one means "running too long", and the two describe
	// opposite failures: an agent hitting this cap may have been chattering
	// happily the whole time, or parked on a prompt the silence watchdog was
	// explicitly told to excuse. classifyAgentExit keys on the distinction —
	// a watchdog stop with no task is read as idle NoWork, which is the one
	// verdict a four-hour run must never get.
	StopReasonRunDurationExceeded StopReason = "run_duration_exceeded"
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
