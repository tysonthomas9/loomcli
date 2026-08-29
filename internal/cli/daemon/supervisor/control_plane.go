package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/cli/sessionfinalize"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
)

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
		Labels:          s.daemonRuntimeLabels(),
		Capabilities:    []string{"local-supervisor", "agent-process"},
		Capacity:        len(s.Agents),
		DrainState:      domain.NodeDrainActive,
		TTL:             ttl,
	}); err != nil {
		return fmt.Errorf("register supervisor node %q: %w", nodeID, err)
	}
	s.NodeID = nodeID

	s.RegisterTick(GoroutineNodeHeartbeat)
	s.RunCritical(GoroutineNodeHeartbeat, func() {
		s.runNodeHeartbeat(nodeID, ttl, interval)
	})
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
			s.RecordTick(GoroutineNodeHeartbeat)
		}
	}
}

// daemonRuntimeLabels composes the loom.daemon.* Node labels that
// daemonregistry.Detect parses to report cwd-independent daemon
// liveness. The labels are the LOOM-3 fix: they let
// `loom workspace ops diagnose` recognize a supervisor regardless of
// the cwd it was launched from.
func (s *Supervisor) daemonRuntimeLabels() []string {
	labels := []string{
		daemonregistry.LabelPID + strconv.Itoa(os.Getpid()),
	}
	if s.ProjectDir != "" {
		labels = append(labels, daemonregistry.LabelCwd+s.ProjectDir)
	}
	if s.IpcSocketPath != "" {
		labels = append(labels, daemonregistry.LabelSocket+s.IpcSocketPath)
	}
	// One label per active degradation. Labels are replaced wholesale by
	// NodeUpdate, so a recovered daemon drops its degraded labels on the next
	// refresh without any explicit removal step.
	labels = append(labels, s.DegradedLabels()...)
	return labels
}

// PublishDegradation announces the current state of one degradation kind on the
// two handles that do NOT share a failure mode with the thing that degraded:
// the events bus and the fleet-db Node labels. The daemon state file is
// deliberately not one of them — a state_write degradation is precisely the
// case where writing the report there fails too.
//
// Best-effort by construction. It is called from the state updater's 5s loop,
// and a fleet-db that is slow or unreachable must not stall that loop or turn a
// reporting problem into a second outage: RefreshNodeLabels already logs at
// Warn and swallows its error, and a nil ControlStore or EmitEvent (tests, and
// daemons running without a control plane) is a no-op rather than a panic.
func (s *Supervisor) PublishDegradation(kind DegradationKind) {
	d, active := s.Degradation(kind)
	data := events.DaemonDegradedData{
		Kind:   string(kind),
		Active: active,
	}
	if active {
		data.Since = d.Since
		data.Count = d.Count
		data.LastErr = d.LastErr
	}

	if s.EmitEvent != nil {
		if evt, err := events.NewEvent(events.DaemonDegraded, "", "", "", data); err == nil {
			s.EmitEvent(evt)
		} else {
			slog.Warn("building daemon degradation event failed", "kind", string(kind), "err", err)
		}
	}

	s.RefreshNodeLabels()
}

// RefreshNodeLabels re-publishes the supervisor's Node labels using
// the current Supervisor state. Call from startDaemonSockets after
// the IPC socket has actually bound — without the refresh, callers
// that mutate IpcSocketPath after Start (e.g., flipping it to "" on
// bind failure in a future code path) would not be reflected in the
// fleet-db Node row.
//
// Errors are logged at Warn level and not returned: the Socket label
// is informational, not load-bearing for the LOOM-3 liveness rule.
func (s *Supervisor) RefreshNodeLabels() {
	if s.ControlStore == nil || s.WorkspaceID == "" || s.NodeID == "" {
		return
	}
	labels := s.daemonRuntimeLabels()
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Nodes().Update(ctx, s.WorkspaceID, s.NodeID, store.NodeUpdate{
		Labels: &labels,
	}); err != nil {
		slog.Warn("supervisor node label refresh failed", "workspace", s.WorkspaceID, "node_id", s.NodeID, "err", err)
	}
}

func (s *Supervisor) markAgentActive(ap *AgentProcess) {
	s.updateAgentRuntimeState(ap, domain.AgentStateActive, nil)
}

func (s *Supervisor) markAgentStoppedOnExit(ap *AgentProcess) {
	var desired *domain.AgentDesiredState
	ap.Mu.Lock()
	stopReason := ap.StopReason
	ap.Mu.Unlock()
	if ap.Entry.Mode == domain.AgentModeEphemeral && stopReason == StopReasonEphemeralDone {
		stopped := domain.AgentDesiredStopped
		desired = &stopped
	}
	s.updateAgentRuntimeState(ap, domain.AgentStateStopped, desired)
}

func (s *Supervisor) updateAgentRuntimeState(ap *AgentProcess, state domain.AgentState, desired *domain.AgentDesiredState) {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	patch := store.AgentUpdate{State: &state}
	if desired != nil {
		patch.DesiredState = desired
	}
	if _, err := s.ControlStore.Agents().Update(ctx, s.WorkspaceID, ap.Entry.Worktree, patch); err != nil {
		slog.Warn("agent runtime state update failed", "worktree", ap.Entry.Worktree, "state", state, "err", err)
	}
}

// ResolveNodeID exposes this supervisor's node identity to the daemon package.
//
// Callers that run before Start() must use this rather than reading the NodeID
// field: NodeID is assigned inside startControlPlaneNode, which runs from
// Start(), so it is still empty while NewDaemon is building the agent list.
// This method falls back to the same PID-derived identity that registration
// would have used, which is what makes a drain from a previous supervisor
// resolve as superseded rather than as an unattributable one.
func (s *Supervisor) ResolveNodeID() string { return s.resolveNodeID() }

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

// ---------------------------------------------------------------------------
// Agent session lifecycle (control plane)
// ---------------------------------------------------------------------------

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
		AgentName: ap.Entry.Worktree, Role: ap.Entry.Role, Backend: s.GetEffectiveBackend(ap),
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
func (s *Supervisor) markControlPlaneAgentState(ctx context.Context, ap *AgentProcess, state domain.AgentState) {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	// WithoutCancel keeps the trace parent while detaching cancellation: this
	// write is best-effort and must still land if the spawn context is done.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.Agents().Update(ctx, s.WorkspaceID, ap.Entry.Worktree, store.AgentUpdate{
		State: &state,
	}); err != nil {
		slog.Warn("control-plane agent state update failed",
			"worktree", ap.Entry.Worktree, "state", state, "err", err)
	}
}

func (s *Supervisor) markControlPlaneAgentSessionRunning(ctx context.Context, ap *AgentProcess) {
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlPlaneOperationTimeout)
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
	errMessage string
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
		Summary:    agentSessionSummary(status, input.errMessage),
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
