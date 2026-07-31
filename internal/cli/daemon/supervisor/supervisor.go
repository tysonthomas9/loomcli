package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
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
	// has been observed stale. The watchdog only signals fatal once a tick has
	// been stale for livenessStaleScansBeforeFatal scans in a row, so a single
	// transient stall (a slow control-plane cycle, brief mutex contention) does
	// not crash the daemon. lastLivenessScan records when scanTicks last ran so
	// it can detect a process-wide suspension (sleep/swap/SIGSTOP) — after which
	// every tick looks ancient — and skip the fatal for that scan. Both fields
	// are owned exclusively by the single livenessWatchdog goroutine (tests call
	// scanTicks serially), so they need no synchronization.
	livenessStreak   map[string]int
	lastLivenessScan time.Time

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

	// ControlStore is the fleet-db-backed control plane used for node, worker,
	// agent ownership, and transitional command records.
	ControlStore store.Store
	NodeID       string
	NodeTTL      time.Duration
	NodeInterval time.Duration

	// ownershipOwnerID is the durable identity of this local supervisor
	// installation. Unlike NodeID (which intentionally identifies one daemon
	// process), it survives daemon and container restarts so a replacement
	// process can atomically re-acquire its own agent-ownership leases.
	ownershipOwnerMu sync.Mutex
	ownershipOwnerID string
	// ownershipOwnerDurable distinguishes the persisted workspace identity
	// from the process-scoped fail-closed fallback used by ownership leases.
	// Lifecycle command claiming requires this bit in production.
	ownershipOwnerDurable bool

	// backendRecheckInterval is the fixed delay computeBackoff returns for a
	// BackendUnavailable block (agent's backend CLI missing from PATH). Zero
	// means use the package default (backendUnavailableRecheckInterval). Tests set a
	// small value to avoid the 30s wait.
	backendRecheckInterval time.Duration

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
		Entry:                 entry,
		RoleConfig:            roleConfig,
		WorktreePath:          target.WorkDir,
		RepoConfig:            s.FindRepoConfig(repoName),
		LifecycleGenerationAt: time.Now().UTC(),
		OwnershipAcquired:     make(chan struct{}),
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

	// Sweep orphaned daemon-local transcript sessions from prior runs before
	// launching agents.
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
	defaultNodeTTL        = 2 * time.Minute
	defaultNodeInterval   = 30 * time.Second
	agentIPCAuthTokenSize = 32
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
	tickName := agentTickName(ap)

	for {
		// Refreshed at the top of every iteration; while we block in
		// waitForAgent → cmd.Wait(), startAgentWaitHeartbeat keeps it fresh.
		s.RecordTick(tickName)
		if s.checkAgentStopSignals(ap) {
			return
		}

		s.clearAgentSessionState(ap)

		// A role slot is local to this daemon, while ownership is the
		// cross-daemon spawn fence. Take the potentially blocking local slot
		// first so the ownership lease we acquire below is current immediately
		// before preflight/spawn. Acquiring ownership before this wait lets its
		// heartbeat fail and stop silently while no process exists, after which
		// the queued agent could still launch alongside the new owner.
		if !s.Concurrency.AcquireUntil(ap.Entry.Role, ap.StopCh) {
			slog.Info("concurrency wait interrupted, exiting", "worktree", ap.Entry.Worktree)
			if !s.checkAgentStopSignals(ap) {
				s.setShutdownStopReason(ap)
			}
			return
		}

		if s.acquireAgentOwnership(ap) != ownershipAcquired {
			s.Concurrency.Release(ap.Entry.Role)
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

		var startDecision agentStartDecision
		stopOwnershipHeartbeat, startDecision = s.prepareAgentStartTransition(ap, stopOwnershipHeartbeat)
		switch startDecision {
		case agentStartStopped:
			s.Concurrency.Release(ap.Entry.Role)
			releaseOwnership()
			return
		case agentStartPreflightRetry:
			if !s.retryAfterPreflightFailure(ap, releaseOwnership) {
				return
			}
			continue
		case agentStartOwnershipRetry:
			if !s.sleepBeforeOwnershipRetry(ap) {
				return
			}
			continue
		case agentStartReady:
			// StartStopMu remains held until spawnAgent publishes Cmd/Pid.
		}

		spawnErr := s.spawnAgent(ap)
		s.endAgentStartTransition(ap)
		s.finishSpawnAndWait(ap, spawnErr)
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

// RecordAgentActivity advances ap.LastActivity for the named agent toward the
// observed PTY-output timestamp. It is a no-op if the agent isn't currently
// supervised. Out-of-order heartbeats never regress the stored value — callers
// can safely retry without ever rewinding the timestamp.
func (s *Supervisor) RecordAgentActivity(agentName string, at time.Time) {
	if agentName == "" || at.IsZero() {
		return
	}
	s.AgentsMu.RLock()
	var target *AgentProcess
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == agentName {
			target = ap
			break
		}
	}
	s.AgentsMu.RUnlock()
	if target == nil {
		return
	}
	target.Mu.Lock()
	if at.After(target.LastActivity) {
		target.LastActivity = at
	}
	target.Mu.Unlock()
}

// preFlightSetup verifies the backend is spawnable, then runs recovery,
// assigns epic, creates session, and clears yield file.
//
// Resume-first / checkpoint-fallback: when the worktree carries a genuine crash
// remnant (a dead agent PID with a task within TTL), re-claim the interrupted
// task while preserving the lock, in-progress worktree files, and fleet claim.
// A carried Claude session resumes directly; backends without one (including
// Codex) cold-start the same task through the checkpoint path. This avoids the
// destructive recoverAgent path, which deletes the lock and would otherwise
// leave an interrupted in_progress task absent from the ready queue. Repeated
// recovery failures eventually cold-start a fresh task. See detectRecovery.
func (s *Supervisor) preFlightSetup(ap *AgentProcess) bool {
	if err := s.gateBackendAvailable(ap); err != nil {
		return false
	}

	taskID, mode := s.detectRecovery(ap)
	switch mode {
	case recoverResume:
		s.prepareResume(ap, taskID)
	case recoverCheckpoint:
		s.prepareCheckpointRetry(ap, taskID)
	default: // recoverCold
		// A cold decision can still carry the task from a stale, live-orphaned,
		// or recovery-exhausted lock. Treat that task as failed so destructive
		// recovery resets it before Ready selects fresh work. With exit code 0,
		// RecoverWorktree trusts the old in_progress status and then excludes the
		// same lock task from its orphan scan, stranding it permanently.
		recoveryExitCode := 0
		if taskID != "" {
			recoveryExitCode = -1
		}
		if err := s.recoverAgent(ap, recoveryExitCode); err != nil {
			slog.Warn("pre-flight recovery failed", "worktree", ap.Entry.Worktree, "err", err)
			// Keep this lock on the destructive cold path across retries. In
			// particular, a release conflict means another actor now owns its task;
			// falling back to resume/checkpoint on the next loop would target that
			// other actor's work.
			ap.Mu.Lock()
			ap.ResumeFailures = maxResumeFailures + 1
			ap.Mu.Unlock()
			return false
		}
		ap.Mu.Lock()
		ap.ResumeFailures = 0 // successful cold cleanup lets a future interruption recover again
		ap.Mu.Unlock()
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

// createAgentSession creates the daemon-local transcript/watchdog session and
// its process-local IPC credential. It does not create an Interaction-owned
// AgentSession or AgentLease.
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
	ap.AgentIPCAuthToken = newAgentIPCAuthToken()
	ap.TranscriptPath = txPath
	ap.BeforeRef = bRef
	ap.Mu.Unlock()
}

func newAgentIPCAuthToken() string {
	var random [agentIPCAuthTokenSize]byte
	if _, err := rand.Read(random[:]); err != nil {
		// A missing token fails closed in daemon IPC validation. Keep local
		// transcript/watchdog tracking available on this exceptional path.
		slog.Error("daemon IPC credential generation failed", "err", err)
		return ""
	}
	return hex.EncodeToString(random[:])
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
	s.finishSpawnAndWait(ap, s.spawnAgent(ap))
}

// finishSpawnAndWait owns the lifecycle after a spawn attempt. superviseAgent
// calls spawnAgent while holding AgentProcess.StartStopMu, releases the
// transition barrier once Cmd/Pid are published, then enters this function so
// Stop never waits behind the subprocess's unbounded lifetime.
func (s *Supervisor) finishSpawnAndWait(ap *AgentProcess, spawnErr error) {
	if spawnErr != nil {
		err := spawnErr
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
		orphan := takeAgentSessionForFinalize(ap)
		if orphan.session != nil {
			_ = orphan.session.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure"})
		}
		taskID := s.taskIDForLifecycle(ap, nil)
		s.releaseAssignedTaskClaim(ap, taskID)
		s.deregisterWorker(ap)
		s.Concurrency.Release(ap.Entry.Role)
		s.markSpawnFailure(ap, err)
		return
	}

	exitCode := s.waitForAgent(ap)
	s.classifyAgentExit(ap, exitCode)
	// Ledger hook: LastError is set, the lock is still present, and
	// The local AgentSessionID has not been cleared by finalization yet.
	s.recordTaskExitForQuarantine(ap, exitCode)
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
	if err := s.recoverAgent(ap, exitCode); err != nil {
		slog.Warn("post-mortem recovery failed", "worktree", ap.Entry.Worktree, "err", err)
		// RecoverWorktree deliberately preserves the local lock when a destructive
		// backend release/reset cannot be proven safe. Keep the next loop on cold
		// cleanup rather than interpreting that lock as resumable work.
		ap.Mu.Lock()
		ap.ResumeFailures = maxResumeFailures + 1
		ap.Mu.Unlock()
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
		}
		ap.Mu.Unlock()
		// Resolve backend name outside the lock (GetEffectiveBackend acquires ap.Mu)
		result[i].CurrentBackend = s.GetEffectiveBackend(ap)
		// Resolve remote branch (reads immutable config, no mutex needed)
		result[i].RemoteBranch = ap.ResolveRemoteBranch()
	}
	return result
}
