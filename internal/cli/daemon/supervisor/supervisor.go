package supervisor

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/git"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
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
}

// NewAgent creates an AgentProcess from an agent entry, resolving the worktree path
// and role config. The idx is used for error messages only.
func (s *Supervisor) NewAgent(entry config.AgentEntry, idx int) (*AgentProcess, error) {
	target, err := workspace.ResolveAgentTarget(entry.Worktree, entry.Repo)
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
		RepoConfig:   s.FindRepoConfig(entry.Repo),
	}
	return ap, nil
}

// Start launches supervisor goroutines for all configured agents.
func (s *Supervisor) Start() error {
	s.Shutdown = make(chan struct{})

	// Sweep orphaned sessions from prior daemon runs before launching agents.
	if sessStore, err := sessions.NewStore(cli.GetBeadsDir()); err != nil {
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
func (s *Supervisor) superviseAgent(ap *AgentProcess) {
	slog.Info("starting agent supervisor", "worktree", ap.Entry.Worktree, "role", ap.Entry.Role)

	for {
		if s.checkAgentStopSignals(ap) {
			return
		}

		s.clearAgentSessionState(ap)

		if !s.Concurrency.Acquire(ap.Entry.Role) {
			slog.Info("concurrency tracker closed, exiting", "worktree", ap.Entry.Worktree)
			s.setStopReason(ap, StopReasonShutdown)
			return
		}

		s.preFlightSetup(ap)

		if !s.spawnAndWait(ap) {
			continue
		}

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
	ap.TranscriptPath = ""
	ap.BeforeRef = ""
	ap.Mu.Unlock()
}

// preFlightSetup runs recovery, assigns epic, creates session, and clears yield file.
func (s *Supervisor) preFlightSetup(ap *AgentProcess) {
	if err := s.recoverAgent(ap, 0); err != nil {
		slog.Warn("pre-flight recovery failed", "worktree", ap.Entry.Worktree, "err", err)
	}

	epicID := s.assignEpic(ap)
	s.createAgentSession(ap, epicID)

	if err := ClearYieldFile(ap.WorktreePath); err != nil {
		slog.Warn("failed to clear stale yield file", "worktree", ap.Entry.Worktree, "err", err)
	}
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
	beadsDir := cli.GetBeadsDir()
	sessStore, err := sessions.NewStore(beadsDir)
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
	txPath := filepath.Join(beadsDir, "sessions", sess.SessionID(), "transcript.jsonl")
	bRef := automode.CaptureHEADRef(ap.WorktreePath)
	ap.Mu.Lock()
	ap.Session = sess
	ap.TranscriptPath = txPath
	ap.BeforeRef = bRef
	ap.Mu.Unlock()
}

// spawnAndWait spawns the agent and waits for it to exit. Returns false if spawn fails (continue loop).
func (s *Supervisor) spawnAndWait(ap *AgentProcess) bool {
	if err := s.spawnAgent(ap); err != nil {
		slog.Warn("spawn failed", "worktree", ap.Entry.Worktree, "err", err)
		ap.Mu.Lock()
		orphanSess := ap.Session
		ap.Session = nil
		ap.Mu.Unlock()
		if orphanSess != nil {
			_ = orphanSess.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure"})
		}
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

// finalizeAgentSession finalizes the daemon-created session after agent exit.
func (s *Supervisor) finalizeAgentSession(ap *AgentProcess, exitCode int) {
	ap.Mu.Lock()
	sess := ap.Session
	bRef := ap.BeforeRef
	ap.Session = nil
	ap.Mu.Unlock()

	if sess == nil {
		return
	}

	taskID := ""
	if info, lockErr := cli.ReadLockFile(ap.WorktreePath); lockErr == nil {
		taskID = info.TaskID
	}
	diffStats := git.ComputeDiffStats(ap.WorktreePath, bRef)

	ap.Mu.Lock()
	errClass := ""
	if ap.LastError != nil {
		errClass = ap.LastError.Class.String()
	}
	ap.Mu.Unlock()

	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID: taskID, ExitCode: exitCode, ErrorClass: errClass,
		FilesTouched: diffStats.FilesTouched,
		DiffStats: sessions.DiffStats{
			FilesChanged: diffStats.FilesChanged, LinesAdded: diffStats.LinesAdded, LinesRemoved: diffStats.LinesRemoved,
		},
	}); err != nil {
		slog.Warn("session finalization failed", "worktree", ap.Entry.Worktree, "err", err)
	}
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
			Worktree:       ap.Entry.Worktree,
			Role:           ap.Entry.Role,
			Repo:           ap.Entry.Repo,
			WorktreePath:   ap.WorktreePath,
			PID:            ap.Pid,
			RestartCount:   ap.RestartCount,
			LastStart:      ap.LastStart,
			LastExit:       ap.LastExit,
			LastExitCode:   ap.LastExitCode,
			AssignedEpicID: ap.AssignedEpicID,
			StopReason:     ap.StopReason,
			NoWorkCount:    ap.NoWorkCount,
			BackoffUntil:   ap.BackoffUntil,
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
