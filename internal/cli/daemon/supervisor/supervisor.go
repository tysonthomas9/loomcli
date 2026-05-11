package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// EventEmitter is the interface for emitting observability events.
type EventEmitter = events.Emitter

type actorClaimBackend interface {
	ClaimIssueAsActor(ctx context.Context, id string, lockTTL time.Duration, actor string) error
}

const (
	claimReadyLimit         = 256
	claimConflictRetryLimit = 16
	claimOperationTimeout   = 10 * time.Second
)

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

	// ControlStore is the fleet-db-backed control plane used for node,
	// session, lease, terminal, artifact, and command records.
	ControlStore store.Store
	NodeID       string
	NodeTTL      time.Duration
	NodeInterval time.Duration
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

	if err := s.startControlPlaneNode(); err != nil {
		return err
	}

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

	// Start healthChecker goroutine
	s.Wg.Add(1)
	go func() {
		defer s.Wg.Done()
		s.healthChecker()
	}()

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
		s.Wg.Add(1)
		go func(agent *AgentProcess) {
			defer s.Wg.Done()
			defer close(agent.Done)
			s.superviseAgent(agent)
		}(ap)
	}

	return nil
}

const (
	defaultNodeTTL      = 2 * time.Minute
	defaultNodeInterval = 30 * time.Second
	defaultLeaseTTL     = 2 * time.Minute
)

var controlPlaneOperationTimeout = 2 * time.Second

func (s *Supervisor) startControlPlaneNode() error {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return nil
	}
	nodeID := s.resolveNodeID()
	ttl := s.NodeTTL
	if ttl <= 0 {
		ttl = defaultNodeTTL
	}
	interval := s.NodeInterval
	if interval <= 0 {
		interval = defaultNodeInterval
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    s.WorkspaceID,
		NodeID:          nodeID,
		OwnerActor:      resolveNodeOwnerActor(),
		RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities:    []string{"local-supervisor", "agent-process"},
		Capacity:        len(s.Agents),
		DrainState:      domain.NodeDrainActive,
		TTL:             ttl,
	}); err != nil {
		return fmt.Errorf("register supervisor node %q: %w", nodeID, err)
	}
	s.NodeID = nodeID

	s.Wg.Add(1)
	go func() {
		defer s.Wg.Done()
		s.runNodeHeartbeat(nodeID, ttl, interval)
	}()
	return nil
}

func (s *Supervisor) runNodeHeartbeat(nodeID string, ttl, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.Shutdown:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
			_, err := s.ControlStore.Nodes().Heartbeat(ctx, s.WorkspaceID, nodeID, ttl)
			cancel()
			if err != nil {
				slog.Warn("supervisor node heartbeat failed", "workspace", s.WorkspaceID, "node_id", nodeID, "err", err)
			}
		}
	}
}

func (s *Supervisor) resolveNodeID() string {
	if s.NodeID != "" {
		return s.NodeID
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("loom-supervisor-%s-%d", host, os.Getpid())
}

func resolveNodeOwnerActor() string {
	for _, key := range []string{"LOOM_FLEET_DB_ACTOR", "LOOM_AGENT_NAME", "USER"} {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return "local"
}

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

	for {
		if s.checkAgentStopSignals(ap) {
			return
		}

		s.clearAgentSessionState(ap)

		if !s.acquireAgentOwnership(ap) {
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
			s.setStopReason(ap, StopReasonShutdown)
			return
		}

		if !s.preFlightSetup(ap) {
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

		if !s.spawnAndWait(ap) {
			releaseOwnership()
			continue
		}

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
			slog.Warn("max restarts exceeded, stopping supervisor", "worktree", ap.Entry.Worktree)
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
		s.setStopReason(ap, StopReasonShutdown)
		return true
	case <-ap.StopCh:
		slog.Info("stop signal received", "worktree", ap.Entry.Worktree)
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return true
	default:
		return false
	}
}

// setStopReason unconditionally sets the agent's stop reason.
func (s *Supervisor) setStopReason(ap *AgentProcess, reason StopReason) {
	ap.Mu.Lock()
	ap.StopReason = reason
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
	ap.Mu.Unlock()
}

// preFlightSetup runs recovery, assigns epic, creates session, and clears yield file.
func (s *Supervisor) preFlightSetup(ap *AgentProcess) bool {
	if err := s.recoverAgent(ap, 0); err != nil {
		slog.Warn("pre-flight recovery failed", "worktree", ap.Entry.Worktree, "err", err)
	}

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

func (s *Supervisor) claimTask(ap *AgentProcess, epicID string) bool {
	if s.IssueBackend == nil || !shouldClaimTaskForRole(ap) {
		return true
	}
	ae := ap.Entry
	if sourceRepos, err := config.ResolveAgentRepos(ap.Entry, s.Repos); err == nil {
		ae.SourceRepos = sourceRepos
	} else {
		slog.Warn("failed to resolve agent repos for task claim", "worktree", ap.Entry.Worktree, "err", err)
	}
	constraints := cli.MergeRoleConstraints(ap.RoleConfig, ae)
	opts := backend.ReadyOpts{Limit: claimReadyLimit, ParentID: epicID}
	if ap.Entry.Repo != "" {
		opts.Labels = []string{"repo:" + ap.Entry.Repo}
	}
	if len(ae.SourceRepos) > 0 {
		opts.SourceRepos = ae.SourceRepos
	}

	ap.Mu.Lock()
	requestedTaskID := ap.RequestedTaskID
	ap.Mu.Unlock()
	if ap.Entry.Mode == domain.AgentModeEphemeral && requestedTaskID == "" {
		s.setPreflightError(ap, agenterr.NoWork, "ephemeral worker requires a requested task")
		return false
	}
	if requestedTaskID != "" {
		return s.claimRequestedTask(ap, opts, requestedTaskID)
	}

	if ap.Entry.Worktree != "" {
		assignedOpts := opts
		assignedOpts.Assignee = ap.Entry.Worktree
		assignedIssues, err := s.readyIssues(assignedOpts)
		if err != nil {
			s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("ready query failed: %v", err))
			return false
		}
		claimed, failed := s.tryClaimBestTask(ap, assignedIssues, constraints)
		if claimed {
			return true
		}
		if failed {
			return false
		}
	}

	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("ready query failed: %v", err))
		return false
	}
	claimed, failed := s.tryClaimBestTask(ap, issues, constraints)
	if claimed {
		return true
	}
	if failed {
		return false
	}
	s.setPreflightError(ap, agenterr.NoWork, "no claimable tasks")
	return false
}

func (s *Supervisor) readyIssues(opts backend.ReadyOpts) ([]backend.IssueData, error) {
	readyCtx, readyCancel := s.operationContext(claimOperationTimeout)
	issues, err := s.IssueBackend.Ready(readyCtx, opts)
	readyCancel()
	return issues, err
}

func (s *Supervisor) claimRequestedTask(ap *AgentProcess, opts backend.ReadyOpts, taskID string) bool {
	issues, err := s.readyIssues(opts)
	if err != nil {
		s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("ready query failed: %v", err))
		return false
	}
	for _, issue := range issues {
		if issue.ID != taskID {
			continue
		}
		if !cli.IsWorkableTask(issue) {
			s.setPreflightError(ap, agenterr.NoWork, fmt.Sprintf("requested task %s is not claimable", taskID))
			return false
		}
		if err := s.claimIssueForAgent(ap, taskID, "requested task"); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				s.setPreflightError(ap, agenterr.NoWork, fmt.Sprintf("requested task %s was already claimed", taskID))
				return false
			}
			s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("claim failed for %s: %v", taskID, err))
			return false
		}
		return true
	}
	s.setPreflightError(ap, agenterr.NoWork, fmt.Sprintf("requested task %s is not ready", taskID))
	return false
}

func (s *Supervisor) tryClaimBestTask(ap *AgentProcess, issues []backend.IssueData, constraints cli.RoleConstraints) (bool, bool) {
	conflicts := 0
	for {
		match := cli.SelectBestTask(issues, constraints)
		if match == nil {
			return false, false
		}
		if err := s.claimIssueForAgent(ap, match.Issue.ID, match.Reason); err != nil {
			if backend.IsKind(err, backend.KindConflict) {
				conflicts++
				if conflicts >= claimConflictRetryLimit {
					s.setPreflightError(ap, agenterr.NoWork, "no claimable tasks after claim conflicts")
					return false, true
				}
				issues = removeIssueByID(issues, match.Issue.ID)
				continue
			}
			s.setPreflightError(ap, agenterr.Unknown, fmt.Sprintf("claim failed for %s: %v", match.Issue.ID, err))
			return false, true
		}
		return true, false
	}
}

func (s *Supervisor) claimIssueForAgent(ap *AgentProcess, taskID, reason string) error {
	claimCtx, claimCancel := s.operationContext(claimOperationTimeout)
	var err error
	if ap.Entry.Worktree != "" {
		if actorBackend, ok := s.IssueBackend.(actorClaimBackend); ok {
			err = actorBackend.ClaimIssueAsActor(claimCtx, taskID, 0, ap.Entry.Worktree)
		} else {
			err = s.IssueBackend.ClaimIssue(claimCtx, taskID, 0)
		}
	} else {
		err = s.IssueBackend.ClaimIssue(claimCtx, taskID, 0)
	}
	claimCancel()
	if err != nil {
		return err
	}
	ap.Mu.Lock()
	ap.AssignedTaskID = taskID
	ap.RequestedTaskID = ""
	ap.Mu.Unlock()
	slog.Info("claimed task for agent", "worktree", ap.Entry.Worktree, "task_id", taskID, "reason", reason)
	return nil
}

func (s *Supervisor) operationContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if s.Shutdown == nil {
		return ctx, cancel
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-s.Shutdown:
			cancel()
		case <-done:
		case <-ctx.Done():
		}
	}()
	return ctx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func shouldClaimTaskForRole(ap *AgentProcess) bool {
	return BuiltInRoles[ap.Entry.Role] || ap.RoleConfig.TaskFilter != ""
}

func (s *Supervisor) setPreflightError(ap *AgentProcess, class agenterr.ErrorClass, message string) {
	ap.Mu.Lock()
	ap.LastExitCode = 0
	ap.LastExit = time.Now()
	ap.LastError = &agenterr.AgentError{Class: class, Message: message}
	ap.LastNoWork = class == agenterr.NoWork
	ap.Mu.Unlock()
}

func removeIssueByID(issues []backend.IssueData, id string) []backend.IssueData {
	out := issues[:0]
	for _, issue := range issues {
		if issue.ID != id {
			out = append(out, issue)
		}
	}
	return out
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
		WorkspaceKey: s.WorkspaceID,
		SessionID:    sessionID,
		AgentID:      ap.Entry.Worktree,
		NodeID:       s.NodeID,
		Kind:         domain.AgentSessionKindTask,
		TaskID:       taskID,
		Status:       domain.AgentSessionStarting,
		Phase:        phase,
		Attempt:      attempt,
		Metadata:     metadata,
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
}

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
}

// spawnAndWait spawns the agent and waits for it to exit. Returns false if spawn fails (continue loop).
func (s *Supervisor) spawnAndWait(ap *AgentProcess) bool {
	if err := s.spawnAgent(ap); err != nil {
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
		return s.handleRestartAfterError(ap)
	}

	exitCode := s.waitForAgent(ap)
	s.classifyAgentExit(ap, exitCode)
	s.finalizeAgentSession(ap, exitCode)
	s.handleAgentCheckpoint(ap, exitCode)
	s.postMortemRecovery(ap, exitCode)
	s.Concurrency.Release(ap.Entry.Role)
	s.handleEpicTransition(ap)
	return true
}

type agentSessionFinalizeState struct {
	session    *sessions.Session
	sessionID  string
	leaseID    string
	leaseToken string
	beforeRef  string
}

// finalizeAgentSession finalizes the daemon-created session after agent exit.
func (s *Supervisor) finalizeAgentSession(ap *AgentProcess, exitCode int) {
	state := takeAgentSessionForFinalize(ap)
	if state.session == nil && state.sessionID == "" {
		return
	}
	taskID := s.taskIDForFinalize(ap)
	errClass := agentErrorClass(ap)
	diffResult := finalizeLocalSession(state.session, ap, state.beforeRef, taskID, exitCode, errClass)
	s.completeControlPlaneAgentSession(ap, agentSessionCompletionInput{
		sessionID:  state.sessionID,
		leaseID:    state.leaseID,
		leaseToken: state.leaseToken,
		exitCode:   exitCode,
		errClass:   errClass,
		taskID:     taskID,
		diffResult: diffResult,
	})
}

func takeAgentSessionForFinalize(ap *AgentProcess) agentSessionFinalizeState {
	ap.Mu.Lock()
	state := agentSessionFinalizeState{
		session:    ap.Session,
		sessionID:  ap.AgentSessionID,
		leaseID:    ap.AgentLeaseID,
		leaseToken: ap.AgentLeaseToken,
		beforeRef:  ap.BeforeRef,
	}
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentLeaseID = ""
	ap.AgentLeaseToken = ""
	ap.Mu.Unlock()
	return state
}

func (s *Supervisor) taskIDForFinalize(ap *AgentProcess) string {
	taskID := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		taskID = info.TaskID
	}
	if taskID == "" {
		taskID = s.taskIDForLifecycle(ap, nil)
	}
	return taskID
}

func agentErrorClass(ap *AgentProcess) string {
	ap.Mu.Lock()
	errClass := ""
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	ap.Mu.Unlock()
	return errClass
}

func finalizeLocalSession(
	sess *sessions.Session,
	ap *AgentProcess,
	beforeRef string,
	taskID string,
	exitCode int,
	errClass string,
) sessionfinalize.WithWorktreeResult {
	result, err := sessionfinalize.WithWorktree(sess, sessionfinalize.WithWorktreeOptions{
		WorktreePath: ap.WorktreePath,
		BeforeRef:    beforeRef,
		TaskID:       taskID,
		ExitCode:     exitCode,
		ErrorClass:   errClass,
	})
	if err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
	return result
}

// postMortemRecovery runs recovery after agent exit, skipping for yield exits.
func (s *Supervisor) postMortemRecovery(ap *AgentProcess, exitCode int) {
	if IsYieldRequested(ap.WorktreePath) {
		slog.Info("skipping post-mortem recovery for yield exit", "worktree", ap.Entry.Worktree)
		return
	}
	if err := s.recoverAgent(ap, exitCode); err != nil {
		slog.Warn("post-mortem recovery failed", "worktree", ap.Entry.Worktree, "err", err)
	}
}

// postExitCleanup runs all cleanup steps after agent exits.
func (s *Supervisor) postExitCleanup(ap *AgentProcess) {
	// This is a placeholder — actual cleanup is done inside spawnAndWait.
	// Keeping as a hook point for future steps.
}

// sleepBeforeRestart performs interruptible backoff sleep. Returns false if interrupted.
func (s *Supervisor) sleepBeforeRestart(ap *AgentProcess) bool {
	backoff := s.computeBackoff(ap)
	ap.Mu.Lock()
	count := ap.RestartCount
	ap.BackoffUntil = time.Now().Add(backoff)
	ap.Mu.Unlock()
	slog.Info("waiting before restart", "worktree", ap.Entry.Worktree, "backoff", backoff, "attempt", count)

	if evt, err := events.NewEvent(events.AgentRestarted, ap.Entry.Worktree, ap.Entry.Role, "", events.AgentRestartedData{PID: 0, RestartCount: count}); err == nil {
		s.EmitEvent(evt)
	}

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
		s.setStopReason(ap, StopReasonShutdown)
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
			BackoffUntil:           ap.BackoffUntil,
			OwnershipLeaseID:       ap.OwnershipLeaseID,
			OwnershipFencingToken:  ap.OwnershipFencingToken,
			OwnershipLastHeartbeat: ap.OwnershipLastHeartbeat,
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
