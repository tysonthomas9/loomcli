// Package supervisor manages agent subprocess lifecycle within the daemon.
// It contains the core supervision loop, agent process management, health checking,
// restart logic, and all related types.
package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// AgentProcess tracks a single supervised agent subprocess.
type AgentProcess struct {
	Entry      cfgpkg.AgentEntry // agent configuration from FleetDB
	RoleConfig cfgpkg.RoleConfig // resolved role configuration
	// WorktreePath is the BASE placement: the worktree resolved once at config
	// load from the agent's CONFIGURED repo. It is written by NewAgent and never
	// mutated, so the several goroutines that read it need no synchronization.
	// Runtime readers must NOT use it directly — the cycle's effective worktree
	// follows the claimed task's source_repo and is read via WorkDir(). See
	// placement.go.
	WorktreePath string             // base (configured) worktree path; runtime readers use WorkDir()
	RepoConfig   *cfgpkg.RepoConfig // base per-repo config (nil in non-workspace mode); runtime readers use Placement().RepoConfig

	// placement is the cycle's EFFECTIVE (repo, worktree, repo config) triple,
	// re-resolved after each claim from the claimed task's source_repo. Swapped
	// atomically rather than guarded by Mu: several readers already hold Mu when
	// they need the path, and sync.Mutex is not reentrant. Nil means "never
	// re-pointed" and reads back as the base placement.
	placement atomic.Pointer[AgentPlacement]

	// claimantOnce/claimantIdentity memoize the process-local claim identity
	// used for agents configured without a worktree (see claimantID in
	// claim.go). Self-synchronizing: written only inside claimantOnce.Do and
	// deliberately NOT covered by Mu, because claimantID is called on paths
	// that are about to take Mu.
	claimantOnce     sync.Once
	claimantIdentity string

	Cmd                    *exec.Cmd         // current subprocess (nil when not running)
	Pid                    int               // PID of current subprocess (0 when not running)
	LogFile                *os.File          // log file handle for subprocess output (nil if not logging)
	LogFilePath            string            // path to agent log file for watchdog stat checks
	LogFileStartOffset     int64             // size of the daemon log when this cycle opened it (O_APPEND); the archive mirror starts reading here so earlier cycles' bytes are not re-copied, and exit classification reads from here so a previous run's tail cannot be classified as this run's (PUPPET-49 + PUPPET-184 share this one field)
	ArchiveLogFile         *os.File          // canonical agent archive (~/.loom/logs/<ws>/agents/<worktree>.log) the web UI Logs tab reads; nil if unavailable
	stopLogMirror          func()            // idempotent stop+drain for this cycle's archive mirror goroutine; nil when no mirror runs (see startAgentLogMirror)
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
	AssignedTaskRepo       string            // source_repo of the claimed task ("" when the task carries none, or on a resume); drives applyTaskPlacement
	RequestedTaskID        string            // task requested by a lifecycle command before normal queue selection
	ResumeTaskID           string            // interrupted task to re-claim this cycle (detected from a surviving crash-remnant lock); drives claimResumeTask for BOTH resume and checkpoint recovery. Per-cycle: cleared in clearAgentSessionState, re-detected in preFlightSetup
	ResumeFailures         int               // consecutive failed RECOVERY attempts — resume AND checkpoint fallback (PERSISTS across cycles); escalation: resume×maxResumeFailures → checkpoint×1 → cold-start
	RecoveryMode           recoveryMode      // this cycle's recovery classification (resume|checkpoint|cold); per-cycle, set in preFlightSetup, read by recordResumeOutcome to decide whether the run's outcome advances ResumeFailures
	LastActivity           time.Time         // most recent PTY output observed by the agent's wrapper (driven by agent IPC heartbeats); zero between spawn and first observation
	InputWaitPending       int               // interactive harness prompts currently awaiting an answer; a count (not a flag) so overlapping prompts nest — see input_wait.go
	InputWaitSince         time.Time         // when InputWaitPending last rose from zero; anchors the bound that stops a suspension from outliving its cause
	AbandonedRunsChecked   bool              // true once this process reconciled the agent's leftover unfinished sessions; the check is per daemon lifetime, not per cycle — within one process every run is finished by the exit path

	LastRearm      time.Time // when the fleet-stall sweep last re-armed this agent's supervise goroutine (see checkFleetStall); zero if never
	RestartCount   int       // consecutive restart attempts
	LastStart      time.Time // when subprocess was last spawned
	LastExit       time.Time // when subprocess last exited
	LastExitCode   int       // exit code from last run
	AssignedEpicID string    // epic this agent is currently assigned to (empty = non-epic mode)

	SoftKnobWarning string // last soft-enforcement warning logged by gateSafetyKnobsEnforceable; deduplicates a per-poll-cycle line down to one per change

	// ProfileError holds the harness-profile refusal raised by
	// gateProfileVerified, cleared on the first verify that passes. It is a
	// SEPARATE slot from LastError on purpose: setPreflightError overwrites
	// LastError unconditionally on every cycle (correct for transient
	// outcomes like NoWork), which is exactly how a profile refusal used to
	// vanish from the board within one poll interval while the agent stayed
	// dead. A sticky, operator-actionable fault needs a sticky slot.
	ProfileError *agenterr.AgentError

	BackendStatePatchedAt time.Time // last control-plane agent-state PATCH issued by gateBackendAvailable; edge-triggers the PATCH so a parked agent does not re-assert the same state every recheck (PUPPET-54)

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

	// RunSilentAtStop records whether the run was ALSO silent past its output
	// timeout at the moment the run-duration cap stopped it. Stamped only by
	// applyRunDurationKill, and purely a record: the cap fires regardless (see
	// the asymmetry documented there). The task-quarantine ledger reads it to
	// tell a wedged run (silent AND over the cap — the no-progress signal) from
	// one that was writing right up to the kill, which says nothing about the
	// task. False for every other stop reason.
	RunSilentAtStop bool

	Mu sync.Mutex // protects Cmd, Pid, LogFile, LogFileStartOffset, SoftKnobWarning, ProfileError, restart tracking, IdleSince, AssignedEpicID, AssignedTaskID, AssignedTaskRepo, RequestedTaskID, ResumeTaskID, ResumeFailures, RecoveryMode, LastError, CurrentBackendIdx, Session, AgentSessionID, ParentSessionID, AgentLeaseID, AgentLeaseToken, ownership fields, TranscriptPath, BeforeRef, StopReason, RunSilentAtStop, LastActivity, InputWaitPending, InputWaitSince, AbandonedRunsChecked
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
	// StopReasonIssueBackendUnavailable marks an agent waiting out an ISSUE
	// backend (fleet-db) outage — unreachable, or rejecting the daemon's
	// credentials. Distinct from StopReasonBackendUnavailable, which is about
	// the agent's own CLI binary: the two collide in the logs (fleet-db's
	// error text literally reads "backend [unavailable] ...") and only one of
	// them is about this agent. The supervise goroutine stays alive and
	// rechecks on a fixed interval, so the agent self-resumes on recovery.
	StopReasonIssueBackendUnavailable StopReason = "issue_backend_unavailable"
	StopReasonEphemeralDone           StopReason = "ephemeral_done" // ephemeral-mode agent exited cleanly after one successful task
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
	// StopReasonProfileInvalid marks an agent whose harness profile failed
	// manifest verification in pre-flight. The agent never spawned and never
	// claimed a task, so this is a configuration fault an operator must
	// repair, not a run failure: it renders as "blocked" (like
	// StopReasonMaxRetriesBlocked), the supervise goroutine stays alive, and
	// the agent self-resumes on the first cycle whose profile verifies.
	StopReasonProfileInvalid StopReason = "profile_invalid"
)

// resolveRemote returns the git remote name for this agent.
// Uses RepoConfig.Remote if available, otherwise defaults to "origin".
func (ap *AgentProcess) ResolveRemote() string {
	rc := ap.Placement().RepoConfig
	if rc != nil && rc.Remote != "" {
		return rc.Remote
	}
	return "origin"
}

// ResolveRemoteBranch returns the full remote/branch ref for this agent
// (e.g. "origin/main"). Uses RepoConfig if available, otherwise defaults
// to "origin/main".
func (ap *AgentProcess) ResolveRemoteBranch() string {
	if rc := ap.Placement().RepoConfig; rc != nil {
		remote := rc.Remote
		if remote == "" {
			remote = "origin"
		}
		branch := rc.DefaultBranch
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
	Repo                   string // effective repo for the current cycle (falls back to the configured one)
	WorktreePath           string // effective worktree for the current cycle
	PID                    int
	RestartCount           int
	LastStart              time.Time
	LastExit               time.Time
	LastExitCode           int
	AssignedEpicID         string
	CurrentBackend         string     // effective backend (includes failover state)
	StopReason             StopReason // why the agent stopped (empty while running)
	LastErrorClass         string     // string representation of last error class (e.g. "RateLimited")
	LastErrorEvidence      string     // agenterr.Evidence.Summary() for that error: which step classified it, and on what
	NoWorkCount            int        // consecutive NoWork exits
	BlockCount             int        // block cycles since the last successful run
	BackoffUntil           time.Time  // when backoff sleep ends (zero if not in backoff)
	RemoteBranch           string     // remote tracking ref (e.g. "origin/main")
	OwnershipLeaseID       string
	OwnershipFencingToken  int64
	OwnershipLastHeartbeat time.Time
	AssignedTaskID         string    // task currently claimed by this agent (empty when between tasks)
	LastActivity           time.Time // most recent PTY output observed by the wrapper; zero if no observation yet
	ClaimsGated            bool      // agent is cycling but gated by an active claim hold
	ProfileError           string    // harness-profile refusal message (empty when the profile verifies or none is configured)
}

// BuiltInRoles defines the built-in role names that use loom <role> command.
var BuiltInRoles = map[string]bool{
	"plan": true,
	"task": true,
}

// ResolveDaemonPath resolves a path relative to projectDir, or returns as-is if absolute.
func ResolveDaemonPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectDir, path)
}
