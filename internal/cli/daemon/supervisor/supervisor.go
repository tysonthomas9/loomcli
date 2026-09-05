package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"

	"go.opentelemetry.io/otel/attribute"
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

	// ControlStore is the fleet-db-backed control plane used for node,
	// session, lease, terminal, artifact, and command records.
	ControlStore store.Store
	NodeID       string
	NodeTTL      time.Duration
	NodeInterval time.Duration

	// backendRecheckInterval is the fixed delay computeBackoff returns for a
	// BackendUnavailable block (agent's backend CLI missing from PATH). Zero
	// means use the package default (backendUnavailableRecheckInterval). Tests set a
	// small value to avoid the 30s wait.
	backendRecheckInterval time.Duration

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

// Stop gracefully shuts down all agents. Safe to call multiple times.
func (s *Supervisor) Stop() {
	// Signal all goroutines to stop (protected from double-close)
	s.ShutdownOnce.Do(func() {
		close(s.Shutdown)
	})

	// Unblock any agents waiting for concurrency slots
	s.Concurrency.Close()

	// Yield and stop all agent processes in parallel
	s.AgentsMu.RLock()
	snapshot := make([]*AgentProcess, len(s.Agents))
	copy(snapshot, s.Agents)
	s.AgentsMu.RUnlock()

	s.drainAllWithGrace(snapshot)

	// Wait for all superviseAgent goroutines to exit
	s.Wg.Wait()
}

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

		if s.acquireAgentOwnership(ap) != ownershipAcquired {
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

		if !s.Concurrency.Acquire(ap.Entry.Role) {
			releaseOwnership()
			slog.Info("concurrency tracker closed, exiting", "worktree", ap.Entry.Worktree)
			s.setShutdownStopReason(ap)
			return
		}

		if !s.preFlightSetup(ap) {
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
	if err := s.gateBackendAvailable(ap); err != nil {
		return false
	}
	if err := s.gateSafetyKnobsEnforceable(ap); err != nil {
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
		// fully destructive form (incomplete=false).
		if err := s.recoverAgent(ap, 0, false); err != nil {
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

// createAgentSession creates a session for liveness tracking.
func (s *Supervisor) createAgentSession(ap *AgentProcess, epicID string) {
	runtimeDir := cli.GetWorkspaceRuntimeDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		slog.Warn("session store unavailable, watchdog will use log file", "worktree", ap.Entry.Worktree, "err", err)
		return
	}

	phase := "implementation"
	if ap.Entry.Role == "plan" {
		phase = "planning"
	}
	ap.Mu.Lock()
	restartCount := ap.RestartCount
	ap.Mu.Unlock()

	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: ap.Entry.Worktree, Backend: s.GetEffectiveBackend(ap),
		EpicID: epicID, Phase: phase, AttemptNum: restartCount,
	})
	if err != nil {
		slog.Warn("session creation failed, watchdog will use log file", "worktree", ap.Entry.Worktree, "err", err)
		return
	}
	txPath := sessStore.NativeTranscriptPath(sess.SessionID())
	bRef := automode.CaptureHEADRef(ap.WorktreePath)
	ap.Mu.Lock()
	ap.Session = sess
	ap.AgentSessionID = sess.SessionID()
	ap.TranscriptPath = txPath
	ap.BeforeRef = bRef
	ap.Mu.Unlock()

	s.createControlPlaneAgentSession(ap, sess.SessionID(), epicID, phase, restartCount)
}

func (s *Supervisor) createControlPlaneAgentSession(ap *AgentProcess, sessionID, epicID, phase string, attempt int) {
	if s.ControlStore == nil || s.WorkspaceID == "" || sessionID == "" {
		return
	}
	taskID := s.taskIDForLifecycle(ap, nil)
	metadata := s.agentSessionMetadata(ap, epicID)
	createCtx, createCancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	if _, err := s.ControlStore.AgentSessions().Create(createCtx, store.AgentSessionCreate{
		WorkspaceKey:    s.WorkspaceID,
		SessionID:       sessionID,
		AgentID:         ap.Entry.Worktree,
		NodeID:          s.NodeID,
		Kind:            domain.AgentSessionKindTask,
		TaskID:          taskID,
		ParentSessionID: ap.ParentSessionID,
		Status:          domain.AgentSessionStarting,
		Phase:           phase,
		Attempt:         attempt,
		Metadata:        metadata,
	}); err != nil {
		createCancel()
		slog.Warn("control-plane agent session creation failed", "worktree", ap.Entry.Worktree, "session_id", sessionID, "err", err)
		return
	}
	createCancel()

	leaseCtx, leaseCancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer leaseCancel()
	lease, err := s.ControlStore.AgentLeases().Create(leaseCtx, store.AgentLeaseCreate{
		WorkspaceKey: s.WorkspaceID,
		SessionID:    sessionID,
		LeaseID:      sessionID + "-lease",
		AgentID:      ap.Entry.Worktree,
		NodeID:       s.NodeID,
		TTL:          defaultLeaseTTL,
	})
	if err != nil {
		slog.Warn("control-plane agent lease creation failed", "worktree", ap.Entry.Worktree, "session_id", sessionID, "err", err)
		return
	}
	ap.Mu.Lock()
	if ap.AgentSessionID == sessionID {
		ap.AgentLeaseID = lease.LeaseID
		ap.AgentLeaseToken = lease.Token
	}
	ap.Mu.Unlock()
}

// markControlPlaneAgentState persists the given agent state onto the
// fleet-db Agent record so UIs and `workspace ops diagnose` reflect
// supervisor lifecycle transitions (currently used by the
// backend-availability gate to flip between AgentStateBackendUnavailable
// and AgentStateActive). Best-effort: failures are logged but do not
// block the supervisor.
func (s *Supervisor) markControlPlaneAgentState(ap *AgentProcess, state domain.AgentState) {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Agents().Update(ctx, s.WorkspaceID, ap.Entry.Worktree, store.AgentUpdate{
		State: &state,
	}); err != nil {
		slog.Warn("control-plane agent state update failed",
			"worktree", ap.Entry.Worktree, "state", state, "err", err)
	}
}

func (s *Supervisor) markControlPlaneAgentSessionRunning(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	backend := s.GetEffectiveBackend(ap)
	ap.Mu.Lock()
	sessionID := ap.AgentSessionID
	metadata := s.agentSessionMetadataLocked(ap, backend)
	ap.Mu.Unlock()
	if sessionID == "" {
		return
	}
	now := time.Now().UTC()
	status := domain.AgentSessionRunning
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.AgentSessions().Update(ctx, s.WorkspaceID, sessionID, store.AgentSessionUpdate{
		Status:        &status,
		LastHeartbeat: &now,
		Metadata:      &metadata,
	}); err != nil {
		slog.Warn("control-plane agent session running update failed", "worktree", ap.Entry.Worktree, "session_id", sessionID, "err", err)
	}
}

func (s *Supervisor) agentSessionMetadata(ap *AgentProcess, epicID string) map[string]string {
	backend := s.GetEffectiveBackend(ap)
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if epicID != "" {
		ap.AssignedEpicID = epicID
	}
	return s.agentSessionMetadataLocked(ap, backend)
}

// agentSessionMetadataLocked requires ap.Mu to be held.
func (s *Supervisor) agentSessionMetadataLocked(ap *AgentProcess, backend string) map[string]string {
	metadata := map[string]string{}
	if backend != "" {
		metadata["backend"] = backend
	}
	if ap.AssignedEpicID != "" {
		metadata["epic_id"] = ap.AssignedEpicID
	}
	if ap.AssignedTaskID != "" {
		metadata["task_id"] = ap.AssignedTaskID
	}
	if ap.Entry.Mode == domain.AgentModeEphemeral {
		metadata["attempt_kind"] = "ephemeral_task_attempt"
		metadata["cleanup_state"] = "retained"
	}
	if ap.Entry.Repo != "" {
		metadata["repo"] = ap.Entry.Repo
	}
	if ap.TranscriptPath != "" {
		metadata["transcript_path"] = ap.TranscriptPath
	}
	if ap.LogFilePath != "" {
		metadata["log_path"] = ap.LogFilePath
	}
	return metadata
}

type agentSessionCompletionInput struct {
	sessionID  string
	leaseID    string
	leaseToken string
	exitCode   int
	errClass   string
	taskID     string
	diffResult sessionfinalize.WithWorktreeResult
	// transcriptData is the leaf's on-disk transcript (read once in
	// finalizeAgentSession). When present it is uploaded as a control-plane artifact
	// and referenced via metadata["transcript_ref"], so a non-owning serve node can
	// surface it (controlPlaneSessionTranscript). Empty on the backend-unavailable path.
	transcriptData []byte
}

//nolint:funlen // Completion writes status, metadata, transcript artifact, and retry-safe control-plane updates together.
func (s *Supervisor) completeControlPlaneAgentSession(ap *AgentProcess, input agentSessionCompletionInput) {
	if s.ControlStore == nil || s.WorkspaceID == "" || input.sessionID == "" {
		return
	}
	status := domain.AgentSessionCompleted
	if input.exitCode != 0 {
		status = domain.AgentSessionFailed
	}
	finishedAt := time.Now().UTC()
	finishedAtPtr := &finishedAt
	exitCodePtr := &input.exitCode
	var taskIDPtr *string
	if input.taskID != "" {
		taskIDPtr = &input.taskID
	}

	backend := s.GetEffectiveBackend(ap)
	ap.Mu.Lock()
	if input.taskID != "" {
		ap.AssignedTaskID = input.taskID
	}
	metadata := s.agentSessionMetadataLocked(ap, backend)
	ap.Mu.Unlock()
	sessions.EncodeDiffStatsMetadata(metadata, input.diffResult.DiffStats, input.diffResult.FilesTouched, input.diffResult.HasDiffPatch)

	// Upload the leaf transcript as a control-plane artifact and reference it via
	// metadata["transcript_ref"] (the same convention the driver host-bridge uses),
	// so a serve node that does NOT own this session locally can still surface it via
	// controlPlaneSessionTranscript. Best-effort: a failed upload must not block the
	// session completion. Own context so it can't eat the Update's timeout budget.
	if len(input.transcriptData) > 0 {
		upCtx, upCancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
		if ref := s.uploadTranscriptArtifact(upCtx, input.sessionID, input.taskID, backend, input.transcriptData); ref != "" {
			metadata["transcript_ref"] = ref
		}
		upCancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.AgentSessions().Update(ctx, s.WorkspaceID, input.sessionID, store.AgentSessionUpdate{
		Status:     &status,
		TaskID:     taskIDPtr,
		FinishedAt: &finishedAtPtr,
		ErrorClass: &input.errClass,
		ExitCode:   &exitCodePtr,
		Metadata:   &metadata,
	}); err != nil {
		slog.Warn("control-plane agent session completion failed", "worktree", ap.Entry.Worktree, "session_id", input.sessionID, "err", err)
	}
	if input.leaseID != "" && input.leaseToken != "" {
		if _, err := s.ControlStore.AgentLeases().Release(ctx, s.WorkspaceID, input.leaseID, input.leaseToken); err != nil {
			slog.Warn("control-plane agent lease release failed", "worktree", ap.Entry.Worktree, "session_id", input.sessionID, "lease_id", input.leaseID, "err", err)
		}
	}
	s.releaseAssignedTaskClaim(ap, input.taskID)
	s.deregisterWorker(ap)
}

// uploadTranscriptArtifact uploads the daemon leaf's transcript as a content
// artifact and returns its artifact:// ref (or "" on failure). The artifact id is
// stable per session so a retried finalize reuses it (UploadContentArtifact is
// idempotent). Owner is the agent session — the daemon leaf has no task_run, which
// is the driver's owner type.
func (s *Supervisor) uploadTranscriptArtifact(ctx context.Context, sessionID, taskID, backend string, data []byte) string {
	if s.ControlStore == nil {
		return ""
	}
	finalized, err := store.UploadContentArtifact(ctx, s.ControlStore.Artifacts(), store.ArtifactCreate{
		WorkspaceKey:  s.WorkspaceID,
		ArtifactID:    "transcript-" + sessionID,
		SessionID:     sessionID,
		TaskID:        taskID,
		OwnerType:     "session", // fleet-db's valid owner type for a session-owned artifact (OwnerID=sessionID)
		OwnerID:       sessionID,
		Type:          "transcript",
		Summary:       "agent session transcript",
		MIMEType:      "application/x-ndjson",
		DurableStatus: "declared",
		Metadata:      map[string]string{"runtime": "daemon-leaf", "backend": backend},
	}, data)
	if err != nil {
		slog.Warn("daemon transcript artifact upload failed", "session_id", sessionID, "err", err)
		return ""
	}
	return "artifact://" + finalized.ArtifactID
}

// deregisterWorker removes the agent's fleet-db worker registration on exit,
// keyed by the claim actor (ap.Entry.Worktree). This is the graceful fast path:
// it collapses the common stop/drain case to instant board cleanup and releases
// any issue lock the worker still holds. Best-effort and idempotent — the
// server-side worker TTL + sweeper are the backstop for non-graceful death.
func (s *Supervisor) deregisterWorker(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap.Entry.Worktree == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if err := s.ControlStore.Workers().Deregister(ctx, s.WorkspaceID, ap.Entry.Worktree); err != nil {
		slog.Debug("supervisor worker deregister failed",
			"workspace", s.WorkspaceID, "worker_id", ap.Entry.Worktree, "err", err)
	}
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
		if orphanSess != nil {
			_ = orphanSess.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure"})
		}
		s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
			sessionID:  orphanSessionID,
			leaseID:    orphanLeaseID,
			leaseToken: orphanLeaseToken,
			exitCode:   -1,
			errClass:   "spawn_failure",
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

// postMortemRecovery runs recovery after agent exit, skipping for yield exits.
func (s *Supervisor) postMortemRecovery(ap *AgentProcess, exitCode int) {
	if IsYieldRequested(ap.WorktreePath) {
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
	ap.BackoffUntil = time.Now().Add(backoff)
	ap.Mu.Unlock()
	slog.Info("waiting before restart", "worktree", ap.Entry.Worktree, "backoff", backoff, "attempt", count)

	_, span := startSpan(cmdstore.RootContext(),
		"daemon.supervisor.restart",
		attribute.String("loom.agent", ap.Entry.Worktree),
		attribute.String("loom.role", ap.Entry.Role),
		attribute.String("loom.workspace", s.WorkspaceID),
		attribute.Int("loom.restart_count", count),
		attribute.String("loom.error_type", errType),
	)
	defer span.End()

	if evt, err := events.NewEvent(events.AgentRestarted, ap.Entry.Worktree, ap.Entry.Role, "", events.AgentRestartedData{PID: 0, RestartCount: count}); err == nil {
		s.EmitEvent(evt)
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
		ap.Mu.Unlock()
		// Resolve backend name outside the lock (GetEffectiveBackend acquires ap.Mu)
		result[i].CurrentBackend = s.GetEffectiveBackend(ap)
		// Resolve remote branch (reads immutable config, no mutex needed)
		result[i].RemoteBranch = ap.ResolveRemoteBranch()
	}
	return result
}

// resolveRoleConfig looks up a role by name, supporting both built-in and custom roles.
func (s *Supervisor) resolveRoleConfig(roleName string, agentIndex int) (config.RoleConfig, error) {
	cfg := s.ConfigSnapshot()
	rc, err := ResolveRoleConfigStatic(roleName, cfg, s.ProjectDir)
	if err != nil {
		return config.RoleConfig{}, fmt.Errorf("agent[%d]: %w", agentIndex, err)
	}
	return rc, nil
}

// ResolveDaemonPath resolves a path relative to projectDir, or returns as-is if absolute.
func ResolveDaemonPath(projectDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(projectDir, path)
}
