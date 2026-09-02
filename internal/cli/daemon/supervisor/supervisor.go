package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/metrics/spawnmetrics"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// EventEmitter is the interface for emitting observability events.
type EventEmitter = events.Emitter

// Supervisor manages agent subprocess lifecycle: spawning, health-checking,
// restart logic, and graceful drain. It is created by the daemon and owns
// the agent process list and supervision goroutines.
type Supervisor struct {
	// ConfigSnapshot returns the current daemon config. This callback avoids a
	// circular dependency between supervisor and daemon (daemon swaps config
	// under a lock; supervisor reads it via this function).
	ConfigSnapshot func() *config.DaemonConfig

	ProjectDir  string
	Repos       []config.RepoConfig // workspace repos for resolveAgentRepos; nil outside workspace mode
	WorkspaceID string              // stable workspace UUID for log namespacing
	BootedAt    time.Time           // when this daemon started supervising; zero DISABLES the grace it anchors and never means "the epoch" -- see quarantineBootGrace

	Agents   []*AgentProcess
	AgentsMu sync.RWMutex // protects the agents slice for concurrent read/write access

	// The walls, both scopes, under WallMu. WallUntil/WallClass/WallMessage are
	// the ACCOUNT wall (billing and usage limits park every agent); ProfileWalls
	// holds one per credential owner — a profile root, or "" for the shared
	// operator config — so an auth failure parks only that login's agents.
	WallMu       sync.Mutex
	WallUntil    time.Time
	WallClass    agenterr.Outcome
	WallMessage  string
	ProfileWalls map[string]credentialWall

	// Active self-reported degradations, and when each was last announced;
	// see degraded.go. Guarded by degradedMu and deliberately NOT by AgentsMu:
	// the writer is the 5s state updater, which must never contend with
	// supervision to report that something is wrong.
	degradations       map[DegradationKind]*Degradation
	lastDegradedNotice map[DegradationKind]time.Time
	degradedMu         sync.Mutex

	Shutdown     chan struct{}  // closed to signal shutdown
	ShutdownOnce sync.Once      // protects shutdown channel from double-close
	Wg           sync.WaitGroup // tracks superviseAgent goroutines

	// FatalCh receives the first fatal supervisor error (panic in a critical
	// goroutine, or a critical goroutine returning before shutdown). Buffered
	// size 1; FatalOnce ensures only the first fatal is delivered. The daemon
	// main loop selects on this alongside the shutdown channel so the process
	// can exit non-zero when supervision dies.
	FatalCh   chan error
	FatalOnce sync.Once

	// Ticks holds *atomic.Int64 UnixNano timestamps keyed by goroutine name.
	// Watched goroutines call RecordTick at each loop iteration; the liveness
	// watchdog flags any tick older than the per-name threshold.
	Ticks sync.Map

	// LivenessTimeout overrides the default per-goroutine staleness threshold
	// for the liveness watchdog. Zero means use built-in defaults.
	LivenessTimeout time.Duration

	// livenessStreak counts how many consecutive scans each goroutine's tick
	// has been observed stale, and livenessStreakStart records when each streak
	// began. The watchdog only signals fatal once a tick has been stale for
	// livenessStaleScansBeforeFatal scans in a row AND that streak has spanned
	// livenessMinStaleSpan of real runtime, so neither a single transient stall
	// (a slow control-plane cycle, brief mutex contention) nor several scans
	// crammed into one macOS DarkWake burst crashes the daemon.
	//
	// lastLivenessScan and lastLivenessScanWall both record when scanTicks last
	// ran, in two different clock domains, so the watchdog can tell "the process
	// was suspended" from "a goroutine is wedged". Two clocks are needed because
	// on darwin the monotonic clock (mach_absolute_time) is SUSPENDED while the
	// machine sleeps: after a wake the monotonic scan gap reads a normal ~10s
	// while tick ages — computed from time.Unix values that carry no monotonic
	// reading, so Sub falls back to wall clock — include the entire sleep. A
	// single-clock guard is therefore blind exactly when it is needed, and the
	// daemon kills itself seconds after every wake. lastLivenessScanWall is
	// stored via Round(0) so it is wall-only and its gap measures elapsed wall
	// time regardless of suspension.
	//
	// livenessFatalSignaled is set once the watchdog has signaled fatal; the
	// watchdog then stops scanning, because the daemon is already draining and
	// further Error records plus goroutine dumps are pure noise.
	//
	// All of these fields are owned exclusively by the single livenessWatchdog
	// goroutine (tests call scanTicks serially), so they need no synchronization.
	livenessStreak        map[string]int
	livenessStreakStart   map[string]time.Time
	lastLivenessScan      time.Time
	lastLivenessScanWall  time.Time
	livenessFatalSignaled bool

	// SpawnMetrics counts spawn outcomes per role; nil-safe, so tests need no wiring.
	SpawnMetrics *spawnmetrics.Recorder

	Concurrency *ConcurrencyTracker
	EventBus    EventEmitter
	EmitEvent   func(events.Event)

	IpcSocketPath string // resolved agent IPC socket path; empty if IPC server not started

	// stoppedAgents tracks agents stopped via the control socket.
	StoppedAgents map[string]struct{}

	// FindRepoConfig looks up a config.RepoConfig by name.
	FindRepoConfig func(repoName string) *config.RepoConfig

	// IssueBackendReady checks if an epic has ready tasks. Injected by daemon.
	IssueBackendReady func(epicID string) (bool, error)
	IssueBackend      backend.IssueBackend

	// quarantine is the supervisor-scoped, task-ID-keyed ledger of repeated
	// no-progress kills (see quarantine.go). Lazily initialized via qrec so
	// the cross-package composite-literal construction site stays untouched.
	quarantine     *taskQuarantine
	quarantineOnce sync.Once
	claims         claimLedger // process-local claim mutual exclusion; see claim.go

	// quarantineStatePathCache is the resolved daemon-quarantine.json path
	// (cache + test seam). Resolved from ProjectDir on the first qrec call, so
	// a test that redirects it MUST set it BEFORE any ledger access — setting
	// it afterwards is a silent no-op. Empty disables persistence entirely.
	quarantineStatePathCache string

	// ControlStore is the fleet-db-backed control plane used for node,
	// session, lease, terminal, artifact, and command records.
	ControlStore store.Store

	// LeasesDisabled suppresses skill-materialization lease acquisition for
	// the process lifetime, set at boot when the capability preflight found
	// the fleet-db does not serve the lease routes. Without it every spawn
	// pays a round trip that can only fail before falling through to the
	// same unlocked materialization.
	LeasesDisabled bool

	NodeID       string
	NodeTTL      time.Duration
	NodeInterval time.Duration

	// LogHeartbeatInterval throttles the "daemon heartbeat" line the health
	// checker emits. The line exists so daemon.log's mtime is a real liveness
	// signal: without it an idle fleet writes nothing and a silent log is
	// indistinguishable from a dead daemon. Zero means the package default
	// (defaultLogHeartbeatInterval); negative disables the heartbeat entirely,
	// which is what a configured log_heartbeat_sec of 0 resolves to.
	LogHeartbeatInterval time.Duration

	// lastHeartbeat is owned by the health-checker goroutine and read/written
	// only from it, so it needs no lock.
	lastHeartbeat time.Time

	// backendRecheckInterval is the fixed delay computeBackoff returns for a
	// BackendUnavailable block (agent's backend CLI missing from PATH). Zero
	// means use the package default (backendUnavailableRecheckInterval). Tests set a
	// small value to avoid the 30s wait.
	backendRecheckInterval time.Duration

	// backendStateReassertInterval bounds how often gateBackendAvailable
	// re-asserts an unchanged backend-availability state to the control plane.
	// Zero means use the package default (backendStateReassertInterval). Tests
	// set a short value to exercise the re-assert without waiting 5m.
	backendStateReassert time.Duration

	// claimHold is the workspace-level refusal to START new work (see claim.go).
	// Nil means no hold. Guarded by claimHoldMu; PersistClaimHold is injected by
	// the daemon package so path resolution stays out of the supervisor.
	claimHold                *ClaimHold
	claimHoldMu              sync.RWMutex
	claimHoldExpiryLogged    bool
	claimHoldLastHeldLog     time.Time
	claimHoldRecheckInterval time.Duration          // test override; 0 ⇒ package default
	PersistClaimHold         func(*ClaimHold) error // injected by the daemon package
	claimHoldLastReload      time.Time              // rate-limits ReloadClaimHold; see maybeReloadClaimHold
	// ReloadClaimHold re-reads the hold when the FILE changed under this process. Injected by the daemon.
	ReloadClaimHold func() (*ClaimHold, bool, error) // (hold, changed, err)

	// maxRetriesBlockInterval is the fixed delay computeBackoff returns once an
	// agent has exhausted its restart budget and blocked (StopReasonMaxRetriesBlocked).
	// Zero means use the package default (defaultMaxRetriesBlockInterval). Tests set
	// a small value to avoid the 60s wait.
	maxRetriesBlockInterval time.Duration
}

// NewAgent creates an AgentProcess from an agent entry, resolving the worktree path
// and role config. The idx is used for error messages only.
func (s *Supervisor) NewAgent(entry config.AgentEntry, idx int) (*AgentProcess, error) {
	repoName := entry.Repo
	if repoName == "" && len(entry.Repos) == 1 && !entry.CrossRepo {
		repoName = entry.Repos[0]
	}
	target, err := workspace.ResolveAgentTarget(entry.Worktree, repoName)
	if err != nil {
		return nil, fmt.Errorf("agent[%d] worktree %q: %w", idx, entry.Worktree, err)
	}

	roleConfig, err := s.resolveRoleConfig(entry.Role, idx)
	if err != nil {
		return nil, err
	}

	ap := &AgentProcess{
		Entry:        entry,
		RoleConfig:   roleConfig,
		WorktreePath: target.WorkDir,
		RepoConfig:   s.FindRepoConfig(repoName),
	}
	return ap, nil
}

// Start launches supervisor goroutines for all configured agents.
func (s *Supervisor) Start() error {
	s.Shutdown = make(chan struct{})
	s.FatalCh = make(chan error, 1)

	if err := s.startControlPlaneNode(); err != nil {
		return err
	}

	// Sweep backend processes orphaned by a previous SIGKILL or crash (PPID==1
	// with cwd under a managed worktree). Must happen before we spawn new
	// workers so a brand-new agent doesn't get confused with a leftover one.
	s.sweepOrphanedBackends()

	// Sweep orphaned sessions from prior daemon runs before launching agents.
	if sessStore, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir()); err != nil {
		slog.Warn("session store unavailable, skipping orphan sweep", "err", err)
	} else {
		if healed, err := sessStore.SweepOrphans(); err != nil {
			slog.Warn("session orphan sweep failed", "err", err)
		} else if healed > 0 {
			slog.Info("healed orphaned sessions on startup", "count", healed)
		}
	}

	// Start healthChecker goroutine under the crash-loud harness.
	s.RegisterTick(GoroutineHealthChecker)
	s.RunCritical(GoroutineHealthChecker, s.healthChecker)

	// Start the liveness watchdog — itself wrapped in RunCritical so a panic
	// in the watchdog also exits the daemon. The watchdog reads atomic tick
	// stamps without taking any supervisor mutex, so it stays responsive even
	// if AgentsMu is deadlocked elsewhere.
	s.RegisterTick(GoroutineLivenessWatchdog)
	s.RunCritical(GoroutineLivenessWatchdog, s.livenessWatchdog)

	// In fleet mode, skip agent supervision — agents are managed by the fleet server.
	cfg := s.ConfigSnapshot()
	if cli.IsFleetMode(cfg) {
		slog.Info("agent supervision suppressed (fleet mode — agents managed by fleet server)")
		return nil
	}

	// Initialize stop/done channels and start superviseAgent goroutine for each agent
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.StopCh = make(chan struct{})
		ap.Done = make(chan struct{})
		s.startAgentSupervisor(ap)
	}

	return nil
}

// startAgentSupervisor launches the supervise loop for a single agent under
// the crash-loud harness. Panics inside superviseAgent become fatal so the
// daemon process exits non-zero; normal returns (max retries, config removed)
// are expected and not treated as failures.
//
// This is called from Start() (no mutex held). The AddAgent path in drain.go
// does its own coordinated Wg.Add(1) under AgentsMu and inlines the spawn —
// see drain.go for that variant.
func (s *Supervisor) startAgentSupervisor(ap *AgentProcess) {
	name := GoroutineAgentPrefix + ap.Entry.Worktree
	s.RegisterTick(name)
	s.Wg.Add(1)
	go s.supervisedAgentBody(name, ap)
}

const (
	defaultNodeTTL      = 2 * time.Minute
	defaultNodeInterval = 30 * time.Second
	// defaultLeaseTTL must outlive a typical real-codex turn (often 5+
	// minutes) so the lease is still Active when the worker calls
	// loom data close. There is no periodic heartbeat loop on the agent
	// lease today (only IPC mutations renew it), so a short TTL silently
	// fails task completion after a long codex session.
	defaultLeaseTTL = 30 * time.Minute
)

var controlPlaneOperationTimeout = 2 * time.Second

// superviseAgent is the main loop for a single agent (runs in goroutine).
//
//nolint:funlen // The restart loop keeps lifecycle ordering visible.
func (s *Supervisor) superviseAgent(ap *AgentProcess) {
	slog.Info("starting agent supervisor", "worktree", ap.Entry.Worktree, "role", ap.Entry.Role)
	s.markAgentActive(ap)
	defer s.markAgentStoppedOnExit(ap)
	tickName := agentTickName(ap)

	for {
		// Refreshed at the top of every iteration; while we block in
		// waitForAgent → cmd.Wait(), startAgentWaitHeartbeat keeps it fresh.
		s.RecordTick(tickName)
		if s.checkAgentStopSignals(ap) {
			return
		}

		s.clearAgentSessionState(ap)

		if !s.acquireAgentOwnershipWithReclaim(ap) {
			if !s.sleepBeforeOwnershipRetry(ap) {
				return
			}
			continue
		}
		stopOwnershipHeartbeat := s.startOwnershipHeartbeat(ap)
		releaseOwnership := func() {
			stopOwnershipHeartbeat()
			s.releaseAgentOwnership(ap)
		}

		// Ownership is the proof that no other daemon runs this agent, so any
		// session row it left unfinished belongs to a run that is over. Once
		// per daemon lifetime; see abandoned_run.go.
		s.recordAbandonedRunsForAgent(ap)

		if !s.Concurrency.Acquire(ap.Entry.Role) {
			releaseOwnership()
			slog.Info("concurrency tracker closed, exiting", "worktree", ap.Entry.Worktree)
			s.setShutdownStopReason(ap)
			return
		}

		if !s.preFlightSetup(ap) {
			s.recordRecoveryFailure(ap) // a recovery cycle that never reached spawn still counts
			s.materializeIdleSkills(ap)
			s.Concurrency.Release(ap.Entry.Role)
			releaseOwnership()
			s.postExitCleanup(ap)
			if !s.shouldRestart(ap) {
				return
			}
			if !s.sleepBeforeRestart(ap) {
				return
			}
			continue
		}

		s.spawnAndWait(ap)
		s.recordResumeOutcome(ap) // resume-failure accounting (resume-first / cold-start fallback)

		releaseOwnership()
		s.postExitCleanup(ap)

		if s.checkAgentStopSignals(ap) {
			return
		}

		if s.tryFallbackBackend(ap) {
			slog.Info("backend failover triggered", "worktree", ap.Entry.Worktree, "backend", s.GetEffectiveBackend(ap))
			continue
		}

		if !s.shouldRestart(ap) {
			// Terminal stops only: fatal (auth/billing), fast-fail
			// (deterministic / block-budget escalation), or the
			// max_retries=0 fail-fast opt-out. Budget exhaustion on
			// retryable classes blocks-and-retries instead (returns true).
			slog.Warn("supervisor stopping (terminal)", "worktree", ap.Entry.Worktree)
			return
		}

		if !s.sleepBeforeRestart(ap) {
			return
		}
	}
}

// checkAgentStopSignals checks shutdown and per-agent stop signals.
func (s *Supervisor) checkAgentStopSignals(ap *AgentProcess) bool {
	select {
	case <-s.Shutdown:
		slog.Info("shutdown signal received", "worktree", ap.Entry.Worktree)
		s.setShutdownStopReason(ap)
		return true
	case <-ap.StopCh:
		slog.Info("stop signal received", "worktree", ap.Entry.Worktree)
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return true
	default:
		return false
	}
}

// setShutdownStopReason unconditionally records that this agent stopped
// because of supervisor shutdown. Every caller (drain, signal handler,
// ownership transfer) uses the same reason; if a new code path ever needs
// a different reason, reintroduce the explicit parameter.
func (s *Supervisor) setShutdownStopReason(ap *AgentProcess) {
	ap.Mu.Lock()
	ap.StopReason = StopReasonShutdown
	ap.Mu.Unlock()
}

// SetStopReasonDefault sets the agent's stop reason only if not already set.
func (s *Supervisor) setStopReasonDefault(ap *AgentProcess, reason StopReason) {
	ap.Mu.Lock()
	if ap.StopReason == "" {
		ap.StopReason = reason
	}
	ap.Mu.Unlock()
}

// clearAgentSessionState resets session state between supervision cycles.
func (s *Supervisor) clearAgentSessionState(ap *AgentProcess) {
	ap.Mu.Lock()
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentLeaseID = ""
	ap.AgentLeaseToken = ""
	ap.TranscriptPath = ""
	ap.BeforeRef = ""
	ap.AssignedTaskID = ""
	ap.ResumeTaskID = ""          // per-cycle; re-detected in preFlightSetup (ResumeFailures persists)
	ap.RecoveryMode = recoverCold // per-cycle; re-classified in preFlightSetup
	ap.YieldRequested = false     // per-cycle; re-set by RequestYield
	ap.YieldEscalated = false     // per-cycle; re-set by DrainWithGrace
	ap.LastActivity = time.Time{}
	// A child that died while parked on an interactive prompt never sends its
	// "end", so the in-flight count must not survive into the next cycle: a
	// stale pending count would suspend the output-timeout watchdog for an
	// agent that is no longer waiting on anything.
	ap.InputWaitPending = 0
	ap.InputWaitSince = time.Time{}
	ap.Mu.Unlock()
}

// preFlightSetup verifies the backend is spawnable, then runs recovery,
// assigns epic, creates session, and clears yield file.
//
// Resume-first / checkpoint-fallback: when the worktree carries a genuine crash
// remnant (a dead agent PID with a carried Claude session + task within TTL),
// RESUME the interrupted task — preserve the lock, the in-progress worktree
// files, and the fleet claim so the agent can `--resume` — instead of the
// destructive recoverAgent path, which deletes the lock (discarding the session
// id) and orphans the task. After repeated resume failures it escalates to a
// CHECKPOINT retry of the same task (re-claim, but cold-start with the prior
// attempt's diff injected) before finally cold-starting a fresh task. See
// detectRecovery.
func (s *Supervisor) preFlightSetup(ap *AgentProcess) bool {
	// FIRST gate: a held workspace issues no Ready query, no ClaimIssue, runs
	// no recovery and creates no session.
	if !s.gateClaimsHeld(ap) {
		return false
	}
	// No span is open on this path; pass an explicit background context so the
	// absence of a trace parent is visible here rather than hidden in the gate.
	if err := s.gateBackendAvailable(context.Background(), ap); err != nil {
		return false
	}
	if err := s.gateAccountWall(ap); err != nil {
		return false
	}
	if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
		return false
	}
	// Before claimTask, deliberately: a drifted profile that is only caught at
	// spawn time claims a task and immediately releases it, and the release
	// erases the diagnosis. See gateProfileVerified.
	if err := s.gateProfileVerified(ap); err != nil {
		return false
	}

	taskID, mode := s.detectRecovery(ap)
	switch mode {
	case recoverResume:
		s.prepareResume(ap, taskID)
	case recoverCheckpoint:
		s.prepareCheckpointRetry(ap, taskID)
	default: // recoverCold
		ap.Mu.Lock()
		ap.ResumeFailures = 0 // cold-starting ⇒ let a future interruption recover again
		ap.Mu.Unlock()
		// Cold start: nothing here is being continued, so recovery takes its
		// fully destructive form (incomplete=false). After a daemon restart the
		// in-memory AssignedTaskID is gone and the lock may carry no task, so
		// the checkpoint is the surviving record of the claim to release.
		recTask, recExit := s.taskIDForLifecycle(ap, nil), 0
		if recTask == "" {
			recTask, recExit = checkpointRecoveryTask(ap)
		}
		if err := s.recoverAgentForTask(ap, recTask, recExit, false); err != nil {
			slog.Warn("pre-flight recovery failed", "worktree", ap.Entry.Worktree, "err", err)
		}
	}
	ap.Mu.Lock()
	ap.RecoveryMode = mode // consumed by recordResumeOutcome after the run
	ap.Mu.Unlock()

	if err := ClearYieldFile(ap.WorktreePath); err != nil {
		slog.Warn("failed to clear stale yield file", "worktree", ap.Entry.Worktree, "err", err)
	}

	epicID := s.assignEpic(ap)
	if !s.claimTask(ap, epicID) {
		return false
	}
	// Holding the claim lock proves any unfinished session row for this task is
	// dead. Must run BEFORE this run's own row exists.
	s.recordAbandonedRunsForTask(ap, s.taskIDForLifecycle(ap, nil))
	s.createAgentSession(ap, epicID)
	return true
}

// assignEpic assigns and emits an epic for the agent.
func (s *Supervisor) assignEpic(ap *AgentProcess) string {
	var epicID string
	if ap.Entry.Parent != "" {
		epicID = ap.Entry.Parent
		slog.Info("using configured epic", "worktree", ap.Entry.Worktree, "epic", epicID)
	}
	ap.Mu.Lock()
	ap.AssignedEpicID = epicID
	ap.Mu.Unlock()

	if epicID != "" {
		if evt, err := events.NewEvent(events.EpicAssigned, ap.Entry.Worktree, ap.Entry.Role, epicID, events.EpicAssignedData{EpicID: epicID}); err == nil {
			s.EmitEvent(evt)
		}
	}
	return epicID
}

// spawnAndWait spawns the agent and waits for it to exit. A spawn failure is
// recorded as a synthetic exit (see markSpawnFailure) so the caller's single
// restart decision — shouldRestart + sleepBeforeRestart — owns counting and
// backoff for both real exits and spawn failures.
func (s *Supervisor) spawnAndWait(ap *AgentProcess) {
	if err := s.spawnAgent(ap); err != nil {
		if errors.Is(err, ErrBackendUnavailable) {
			// gateBackendAvailable already set the BackendUnavailable state/error.
			// The normal pre-flight gate runs before task claim, but this spawn-time
			// gate remains as a race guard for a backend disappearing after claim and
			// before exec. Clean up any already-created session/claim/worker before
			// blocking so the task is immediately claimable again.
			s.completeBackendUnavailableCleanup(ap)
			s.Concurrency.Release(ap.Entry.Role)
			return
		}
		slog.Warn("spawn failed", "worktree", ap.Entry.Worktree, "err", err)
		ap.Mu.Lock()
		orphanSess := ap.Session
		ap.Session = nil
		orphanSessionID := ap.AgentSessionID
		ap.AgentSessionID = ""
		orphanLeaseID := ap.AgentLeaseID
		orphanLeaseToken := ap.AgentLeaseToken
		ap.AgentLeaseID = ""
		ap.AgentLeaseToken = ""
		ap.Mu.Unlock()
		spawnMsg := "agent process failed to spawn: " + err.Error()
		if orphanSess != nil {
			_ = orphanSess.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure", ErrorMessage: spawnMsg})
		}
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  orphanSessionID,
			leaseID:    orphanLeaseID,
			leaseToken: orphanLeaseToken,
			exitCode:   -1,
			errClass:   "spawn_failure",
			errMessage: spawnMsg,
			taskID:     s.taskIDForLifecycle(ap, nil),
		})
		s.Concurrency.Release(ap.Entry.Role)
		s.markSpawnFailure(ap, err)
		return
	}

	exitCode := s.waitForAgent(ap)
	s.classifyAgentExit(ap, exitCode)
	// Ledger hook: LastError is set, the lock is still present, and
	// AgentSessionID has not been cleared by finalize yet. It runs with the
	// FACTUAL exit code, so a clean run that later fails a completion hook
	// evicts its ledger entry and the hook failure never counts toward task
	// quarantine — deliberate (hook outcomes are agent-side, not task-side) and
	// bounded instead by the agent's block budget via CompletionHookFailure.
	s.recordTaskExitForQuarantine(ap, exitCode)
	// Completion hooks run while the session id, claim, and transcript still
	// exist, and before finalize/checkpoint/recovery decide the run's fate: a
	// failed hook write demotes exitCode so the owned task is reopened.
	exitCode = s.runCompletionHooks(ap, exitCode)
	s.finalizeAgentSession(ap, exitCode)
	s.handleAgentCheckpoint(ap, exitCode)
	s.postMortemRecovery(ap, exitCode)
	// Sweep AFTER recovery reset the task to open, so the quarantine write
	// transitions open→blocked.
	s.sweepQuarantineDue(ap)
	s.Concurrency.Release(ap.Entry.Role)
	s.handleEpicTransition(ap)
}

// postMortemRecovery runs recovery after agent exit, skipping for GRACEFUL
// yield exits only — an escalated (timed-out, SIGTERMed) yield is a kill and
// must go through recovery so its fleet-db claim is released.
func (s *Supervisor) postMortemRecovery(ap *AgentProcess, exitCode int) {
	if s.isGracefulYieldExit(ap) {
		slog.Info("skipping post-mortem recovery for yield exit", "worktree", ap.Entry.Worktree)
		return
	}
	if err := s.recoverAgent(ap, exitCode, isIncompleteRun(ap)); err != nil {
		slog.Warn("post-mortem recovery failed", "worktree", ap.Entry.Worktree, "err", err)
	}
}

// postExitCleanup runs all cleanup steps after agent exits.
func (s *Supervisor) postExitCleanup(ap *AgentProcess) {
	// This is a placeholder — actual cleanup is done inside spawnAndWait.
	// Keeping as a hook point for future steps.
}

// sleepBeforeRestart performs interruptible backoff sleep. Returns false if interrupted.
//
// One daemon.supervisor.restart span is opened per restart attempt. The span
// covers the backoff window plus the AgentRestarted event emit; the actual
// re-spawn that follows is its own daemon.supervisor.spawn child span (via
// the next iteration of the supervise loop).
// startBackoffHeartbeat keeps the agent's supervise tick fresh during a long
// restart wait (a block, or a long exponential backoff). It returns a no-op
// stopper for short waits that cannot approach the staleness threshold, so
// callers can always defer the returned function.
func (s *Supervisor) startBackoffHeartbeat(ap *AgentProcess, backoff time.Duration) func() {
	if backoff < agentWaitHeartbeatInterval {
		return func() {}
	}
	return s.startAgentWaitHeartbeat(ap)
}

func (s *Supervisor) sleepBeforeRestart(ap *AgentProcess) bool {
	backoff := s.computeBackoff(ap)
	ap.Mu.Lock()
	count := ap.RestartCount
	errType := errorTypeFromAgentErr(ap.LastError)
	lastErr := ap.LastError
	noWorkCount := ap.NoWorkCount
	idleSince := ap.IdleSince
	ap.BackoffUntil = time.Now().Add(backoff)
	ap.Mu.Unlock()

	if logBackoffWait(ap, backoff, count, lastErr, noWorkCount, idleSince) {
		defer s.announceRestartWait(ap, count, errType)()
	}

	// Keep the agent's liveness tick fresh during a long wait (a block, or a
	// long exponential backoff) so the watchdog cannot mistake a healthy,
	// waiting supervise goroutine for a wedged one. The select below is
	// bounded by backoff, so this masks no real deadlock.
	stopHeartbeat := s.startBackoffHeartbeat(ap, backoff)
	defer stopHeartbeat()

	var backoffAction string
	select {
	case <-time.After(backoff):
		backoffAction = "continue"
	case <-s.Shutdown:
		backoffAction = "shutdown"
	case <-ap.StopCh:
		backoffAction = "stop"
	}

	ap.Mu.Lock()
	ap.BackoffUntil = time.Time{}
	ap.Mu.Unlock()

	if backoffAction == "shutdown" {
		slog.Info("shutdown during backoff", "worktree", ap.Entry.Worktree)
		s.setShutdownStopReason(ap)
		return false
	}
	if backoffAction == "stop" {
		slog.Info("stop signal during backoff", "worktree", ap.Entry.Worktree)
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return false
	}
	return true
}

// AgentCount returns the number of configured agents.
func (s *Supervisor) AgentCount() int {
	s.AgentsMu.RLock()
	n := len(s.Agents)
	s.AgentsMu.RUnlock()
	return n
}

// GetAgents returns a snapshot of all agent statuses for inspection.
// The returned SupervisedAgentStatus structs are safe to use without synchronization.
func (s *Supervisor) GetAgents() []SupervisedAgentStatus {
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	result := make([]SupervisedAgentStatus, len(snapshot))
	for i, ap := range snapshot {
		ap.Mu.Lock()
		result[i] = SupervisedAgentStatus{
			Worktree:               ap.Entry.Worktree,
			Role:                   ap.Entry.Role,
			Repo:                   ap.Entry.Repo,
			WorktreePath:           ap.WorktreePath,
			PID:                    ap.Pid,
			RestartCount:           ap.RestartCount,
			LastStart:              ap.LastStart,
			LastExit:               ap.LastExit,
			LastExitCode:           ap.LastExitCode,
			AssignedEpicID:         ap.AssignedEpicID,
			StopReason:             ap.StopReason,
			NoWorkCount:            ap.NoWorkCount,
			BlockCount:             ap.BlockCount,
			BackoffUntil:           ap.BackoffUntil,
			OwnershipLeaseID:       ap.OwnershipLeaseID,
			OwnershipFencingToken:  ap.OwnershipFencingToken,
			OwnershipLastHeartbeat: ap.OwnershipLastHeartbeat,
			AssignedTaskID:         ap.AssignedTaskID,
			LastActivity:           ap.LastActivity,
		}
		if ap.LastError != nil {
			result[i].LastErrorClass = ap.LastError.Class.String()
			// Derived, not stored: the agent's last transition was a claim-hold
			// gate. Clears itself on the next successful pre-flight.
			result[i].ClaimsGated = ap.LastError.Class.Is(agenterr.ClaimsHeldOutcome)
		}
		// Projected as its own field, not folded into LastErrorClass: the
		// message names the drifted version and the profile directory, which
		// is the entire actionable content of the failure.
		if ap.ProfileError != nil {
			result[i].ProfileError = ap.ProfileError.Message
		}
		ap.Mu.Unlock()
		// Resolve backend name outside the lock (GetEffectiveBackend acquires ap.Mu)
		result[i].CurrentBackend = s.GetEffectiveBackend(ap)
		// Resolve remote branch (reads immutable config, no mutex needed)
		result[i].RemoteBranch = ap.ResolveRemoteBranch()
	}
	return result
}
