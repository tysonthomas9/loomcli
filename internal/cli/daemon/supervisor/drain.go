package supervisor

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// DefaultYieldTimeout is the default number of seconds to wait for an agent
// to exit after a yield file is written, before escalating to SIGTERM.
const DefaultYieldTimeout = 60 // seconds

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

// setStopReasonDefault sets the agent's stop reason only if not already set.
func (s *Supervisor) setStopReasonDefault(ap *AgentProcess, reason StopReason) {
	ap.Mu.Lock()
	if ap.StopReason == "" {
		ap.StopReason = reason
	}
	ap.Mu.Unlock()
}

// DrainWithGrace implements a four-phase graceful shutdown sequence:
// yield file -> wait for voluntary exit -> SIGTERM -> SIGKILL.
// Returns true if the agent exited from yield alone (SIGTERM was not needed).
func (s *Supervisor) DrainWithGrace(ap *AgentProcess, reason string, yieldTimeout, sigtermTimeout time.Duration) bool {
	slog.Info("requesting yield", "worktree", ap.Entry.Worktree, "reason", reason, "timeout", yieldTimeout)

	ap.Mu.Lock()
	pid := ap.Pid
	ap.Mu.Unlock()
	if pid == 0 || !lockfile.IsProcessRunning(pid) {
		if err := ClearYieldFile(ap.WorktreePath); err != nil {
			slog.Warn("failed to clear stale yield file", "worktree", ap.Entry.Worktree, "err", err)
		}
		slog.Info("agent already stopped before yield", "worktree", ap.Entry.Worktree)
		return true
	}

	// Phase 1: Write yield file
	if err := s.RequestYield(ap, reason); err != nil {
		slog.Warn("yield file write failed, falling back to SIGTERM", "worktree", ap.Entry.Worktree, "err", err)
		s.StopAgent(ap, sigtermTimeout)
		return false
	}
	defer func() {
		if err := ClearYieldFile(ap.WorktreePath); err != nil {
			slog.Warn("failed to clear yield file after drain", "worktree", ap.Entry.Worktree, "err", err)
		}
	}()

	// Phase 2: Poll for voluntary exit
	deadline := time.Now().Add(yieldTimeout)
	for time.Now().Before(deadline) {
		ap.Mu.Lock()
		pid := ap.Pid
		ap.Mu.Unlock()

		if pid == 0 {
			slog.Info("agent yielded gracefully", "worktree", ap.Entry.Worktree, "elapsed", time.Since(deadline.Add(-yieldTimeout)).Truncate(time.Millisecond))
			return true
		}

		if !lockfile.IsProcessRunning(pid) {
			slog.Info("agent yielded gracefully", "worktree", ap.Entry.Worktree, "elapsed", time.Since(deadline.Add(-yieldTimeout)).Truncate(time.Millisecond))
			return true
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Phase 3: Escalate to SIGTERM -> SIGKILL
	slog.Info("yield timeout expired, escalating to SIGTERM", "worktree", ap.Entry.Worktree, "timeout", yieldTimeout)
	s.StopAgent(ap, sigtermTimeout)
	return false
}

// GetYieldTimeout returns the configured yield timeout duration.
// Falls back to DefaultYieldTimeout if not set or <= 0.
func (s *Supervisor) GetYieldTimeout() time.Duration {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.YieldTimeout != nil && *cfg.Daemon.RestartPolicy.YieldTimeout > 0 {
		return time.Duration(*cfg.Daemon.RestartPolicy.YieldTimeout) * time.Second
	}
	return DefaultYieldTimeout * time.Second
}

// drainAllWithGrace yields all agents in parallel, used by Supervisor.Stop().
// Both yield and SIGTERM timeouts are capped at 30s to keep daemon shutdown prompt.
func (s *Supervisor) drainAllWithGrace(agents []*AgentProcess) {
	yieldTimeout := s.GetYieldTimeout()
	if yieldTimeout > 30*time.Second {
		slog.Info("capping yield timeout for daemon shutdown", "configured", yieldTimeout, "capped", 30*time.Second)
		yieldTimeout = 30 * time.Second
	}
	sigtermTimeout := s.GetSigtermTimeout()
	if sigtermTimeout > 30*time.Second {
		slog.Info("capping SIGTERM timeout for daemon shutdown", "configured", sigtermTimeout, "capped", 30*time.Second)
		sigtermTimeout = 30 * time.Second
	}

	var stopWg sync.WaitGroup
	for _, ap := range agents {
		stopWg.Add(1)
		go func(agent *AgentProcess) {
			defer stopWg.Done()
			// Shutdown is already closed, so no later transition can begin.
			// Wait for an in-flight claim-to-spawn transition to publish Cmd/Pid
			// before DrainWithGrace inspects the process.
			agent.StartStopMu.Lock()
			agent.Mu.Lock()
			publishedPID := agent.Pid
			agent.Mu.Unlock()
			agent.StartStopMu.Unlock()
			slog.Debug("agent start transition settled before shutdown drain",
				"worktree", agent.Entry.Worktree, "pid", publishedPID)
			s.DrainWithGrace(agent, "shutdown", yieldTimeout, sigtermTimeout)
		}(ap)
	}
	stopWg.Wait()
}

// DrainAgent gracefully stops a single agent by name and removes it from the agents slice.
// It signals the agent's superviseAgent goroutine to exit via StopCh, stops the subprocess
// via SIGTERM/SIGKILL, waits for the goroutine to finish, then removes the agent.
func (s *Supervisor) DrainAgent(name string) error {
	// Find the agent under lock
	s.AgentsMu.Lock()
	var target *AgentProcess
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		s.AgentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	s.AgentsMu.Unlock()

	// Fleet mode: no superviseAgent goroutine was launched, StopCh/Done are nil.
	if target.StopCh == nil {
		return nil
	}

	// Linearize Stop with the supervisor's claim-to-spawn transition. If the
	// transition already won, this waits until Cmd/Pid are published; if Stop
	// wins, the under-lock StopCh check prevents a new claim.
	target.StartStopMu.Lock()

	// Set stop reason before signaling (superviseAgent reads it after seeing StopCh closed)
	target.Mu.Lock()
	target.StopReason = StopReasonConfigRemoved
	target.Mu.Unlock()

	// Signal the agent to stop (safe against double-close).
	// ORDERING: StopCh must close BEFORE DrainWithGrace — prevents superviseAgent
	// from respawning after the subprocess exits via yield.
	target.StopOnce.Do(func() {
		close(target.StopCh)
	})
	target.StartStopMu.Unlock()

	// Yield -> wait -> SIGTERM -> SIGKILL
	s.DrainWithGrace(target, "config_removed", s.GetYieldTimeout(), s.GetSigtermTimeout())

	// Wait for the superviseAgent goroutine to exit
	<-target.Done

	// Remove from the agents slice under write lock
	s.AgentsMu.Lock()
	for i, ap := range s.Agents {
		if ap == target {
			s.Agents = append(s.Agents[:i], s.Agents[i+1:]...)
			break
		}
	}
	s.AgentsMu.Unlock()

	slog.Info("agent drained and removed", "worktree", name)
	return nil
}

// DrainAgentWithReason is like DrainAgent but sets a specific stop reason.
func (s *Supervisor) DrainAgentWithReason(name string, reason StopReason) error {
	// Find the agent under lock
	s.AgentsMu.Lock()
	var target *AgentProcess
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		s.AgentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	s.AgentsMu.Unlock()

	// Fleet mode: no superviseAgent goroutine was launched, StopCh/Done are nil.
	if target.StopCh == nil {
		return nil
	}

	target.StartStopMu.Lock()

	// Set stop reason before signaling
	target.Mu.Lock()
	target.StopReason = reason
	target.Mu.Unlock()

	// Signal the agent to stop (safe against double-close).
	// ORDERING: StopCh must close BEFORE DrainWithGrace — prevents superviseAgent
	// from respawning after the subprocess exits via yield.
	target.StopOnce.Do(func() {
		close(target.StopCh)
	})
	target.StartStopMu.Unlock()

	// Yield -> wait -> SIGTERM -> SIGKILL
	s.DrainWithGrace(target, string(reason), s.GetYieldTimeout(), s.GetSigtermTimeout())

	// Wait for the superviseAgent goroutine to exit
	<-target.Done

	// Remove from the agents slice under write lock
	s.AgentsMu.Lock()
	for i, ap := range s.Agents {
		if ap == target {
			s.Agents = append(s.Agents[:i], s.Agents[i+1:]...)
			break
		}
	}
	s.AgentsMu.Unlock()

	slog.Info("agent drained", "worktree", name, "reason", reason)
	return nil
}

// DrainAgentForceful is like DrainAgentWithReason but skips DrainWithGrace,
// going directly to SIGTERM/SIGKILL. Used by the CLI force-stop path where
// the control socket timeout is a concern.
func (s *Supervisor) DrainAgentForceful(name string, reason StopReason) error {
	// Find the agent under lock
	s.AgentsMu.Lock()
	var target *AgentProcess
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == name {
			target = ap
			break
		}
	}
	if target == nil {
		s.AgentsMu.Unlock()
		return fmt.Errorf("agent %q not found", name)
	}
	s.AgentsMu.Unlock()

	// Fleet mode: no superviseAgent goroutine was launched, StopCh/Done are nil.
	if target.StopCh == nil {
		return nil
	}

	target.StartStopMu.Lock()

	// Set stop reason before signaling
	target.Mu.Lock()
	target.StopReason = reason
	target.Mu.Unlock()

	// Signal the agent to stop (safe against double-close)
	target.StopOnce.Do(func() {
		close(target.StopCh)
	})
	target.StartStopMu.Unlock()

	// Stop the subprocess directly: SIGTERM -> SIGKILL (no yield)
	s.StopAgent(target, s.GetSigtermTimeout())

	// Wait for the superviseAgent goroutine to exit
	<-target.Done

	// Remove from the agents slice under write lock
	s.AgentsMu.Lock()
	for i, ap := range s.Agents {
		if ap == target {
			s.Agents = append(s.Agents[:i], s.Agents[i+1:]...)
			break
		}
	}
	s.AgentsMu.Unlock()

	slog.Info("agent force-drained", "worktree", name, "reason", reason)
	return nil
}

// AddAgent creates and starts a new agent at runtime.
// The agent begins its superviseAgent loop immediately.
func (s *Supervisor) AddAgent(entry config.AgentEntry) error {
	return s.AddAgentForTask(entry, "")
}

// AddAgentForTask creates and starts a new agent with an optional first task
// requested by the control plane.
func (s *Supervisor) AddAgentForTask(entry config.AgentEntry, taskID string, parentSessionIDs ...string) error {
	_, err := s.addAgentForTask(entry, taskID, firstParentSessionID(parentSessionIDs), false)
	return err
}

// AddAgentWithOwnershipReservation starts a fresh lifecycle generation whose
// first ownership lease cannot be released until the caller durably completes
// the Restart command. The returned process exposes an explicit
// OwnershipAcquired signal; callers must release the reservation on every
// terminal path.
func (s *Supervisor) AddAgentWithOwnershipReservation(entry config.AgentEntry) (*AgentProcess, error) {
	return s.addAgentForTask(entry, "", "", true)
}

// AddAgentForTaskWithOwnershipReservation is the task-aware Start-command
// counterpart to AddAgentWithOwnershipReservation.
func (s *Supervisor) AddAgentForTaskWithOwnershipReservation(
	entry config.AgentEntry,
	taskID,
	parentSessionID string,
) (*AgentProcess, error) {
	return s.addAgentForTask(entry, taskID, parentSessionID, true)
}

func (s *Supervisor) addAgentForTask(
	entry config.AgentEntry,
	taskID string,
	parentSessionID string,
	reserveOwnership bool,
) (*AgentProcess, error) {
	if channelClosed(s.Shutdown) {
		return nil, fmt.Errorf("cannot add agent %q: supervisor is shutting down", entry.Worktree)
	}
	if entry.Mode == domain.AgentModeEphemeral && taskID == "" {
		return nil, fmt.Errorf("ephemeral agent %q requires a task_id", entry.Worktree)
	}

	if err := s.checkDuplicateAgent(entry.Worktree); err != nil {
		return nil, err
	}

	// Resolve worktree path (outside lock — may do I/O)
	target, err := workspace.ResolveAgentTarget(entry.Worktree, entry.Repo)
	if err != nil {
		return nil, fmt.Errorf("agent %q worktree: %w", entry.Worktree, err)
	}

	s.AgentsMu.RLock()
	agentCount := len(s.Agents)
	s.AgentsMu.RUnlock()
	roleConfig, err := s.resolveRoleConfig(entry.Role, agentCount)
	if err != nil {
		return nil, err
	}

	ap := s.newRuntimeAgentProcess(entry, roleConfig, target.WorkDir, taskID, parentSessionID)
	ap.ownershipCommandReserved = reserveOwnership

	if err := s.registerRuntimeAgent(ap); err != nil {
		return nil, err
	}

	name := GoroutineAgentPrefix + ap.Entry.Worktree
	s.RegisterTick(name)
	go s.supervisedAgentBody(name, ap)

	slog.Info("agent added and started", "worktree", entry.Worktree, "role", entry.Role)
	return ap, nil
}

func (s *Supervisor) registerRuntimeAgent(ap *AgentProcess) error {
	// Authoritative duplicate check + slice append + WaitGroup increment under
	// a single write lock so Wg.Add can't race with Stop()'s snapshot and Wait.
	// Shutdown closes before Stop takes the same lock for its snapshot: an add
	// that wins this lock is included in that snapshot, while one that loses is
	// rejected and cannot outlive Stop.
	s.AgentsMu.Lock()
	defer s.AgentsMu.Unlock()
	if channelClosed(s.Shutdown) {
		return fmt.Errorf("cannot add agent %q: supervisor is shutting down", ap.Entry.Worktree)
	}
	for _, existing := range s.Agents {
		if existing.Entry.Worktree == ap.Entry.Worktree {
			return fmt.Errorf("agent %q already exists", ap.Entry.Worktree)
		}
	}
	s.Agents = append(s.Agents, ap)
	s.Wg.Add(1)
	return nil
}

func firstParentSessionID(parentSessionIDs []string) string {
	if len(parentSessionIDs) == 0 {
		return ""
	}
	return strings.TrimSpace(parentSessionIDs[0])
}

func (s *Supervisor) newRuntimeAgentProcess(entry config.AgentEntry, roleConfig config.RoleConfig, workDir, taskID, parentSessionID string) *AgentProcess {
	return &AgentProcess{
		Entry:                 entry,
		RoleConfig:            roleConfig,
		WorktreePath:          workDir,
		RepoConfig:            s.FindRepoConfig(entry.Repo),
		LifecycleGenerationAt: time.Now().UTC(),
		OwnershipAcquired:     make(chan struct{}),
		RequestedTaskID:       taskID,
		ParentSessionID:       parentSessionID,
		StopCh:                make(chan struct{}),
		Done:                  make(chan struct{}),
	}
}

// checkDuplicateAgent does a lock-free probe for an existing agent with the
// same worktree. Cheap fast-fail before the I/O in AddAgentForTask; the
// authoritative check happens under AgentsMu.Lock after the I/O.
func (s *Supervisor) checkDuplicateAgent(worktree string) error {
	s.AgentsMu.RLock()
	defer s.AgentsMu.RUnlock()
	for _, ap := range s.Agents {
		if ap.Entry.Worktree == worktree {
			return fmt.Errorf("agent %q already exists", worktree)
		}
	}
	return nil
}
