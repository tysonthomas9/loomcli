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

// DrainWithGrace implements a four-phase graceful shutdown sequence:
// yield file -> wait for voluntary exit -> SIGTERM -> SIGKILL.
// The returned DrainOutcome records which phase the agent left in and how long
// it took, so a shutdown that overruns its budget can name the worktree
// responsible instead of reporting an anonymous timeout (PUPPET-39).
func (s *Supervisor) DrainWithGrace(ap *AgentProcess, reason string, yieldTimeout, sigtermTimeout time.Duration) DrainOutcome {
	slog.Info("requesting yield", "worktree", ap.Entry.Worktree, "reason", reason, "timeout", yieldTimeout)

	start := time.Now()
	outcome := func(phase DrainPhase) DrainOutcome {
		return DrainOutcome{
			Worktree: ap.Entry.Worktree,
			Phase:    phase,
			Elapsed:  time.Since(start),
		}
	}

	ap.Mu.Lock()
	pid := ap.Pid
	ap.Mu.Unlock()
	if pid == 0 || !lockfile.IsProcessRunning(pid) {
		if err := ClearYieldFile(ap.WorktreePath); err != nil {
			slog.Warn("failed to clear stale yield file", "worktree", ap.Entry.Worktree, "err", err)
		}
		slog.Info("agent already stopped before yield", "worktree", ap.Entry.Worktree)
		return outcome(DrainPhaseAlreadyStopped)
	}

	// Phase 1: Write yield file
	if err := s.RequestYield(ap, reason); err != nil {
		slog.Warn("yield file write failed, falling back to SIGTERM", "worktree", ap.Entry.Worktree, "err", err)
		s.StopAgent(ap, sigtermTimeout)
		return outcome(DrainPhaseYieldWriteFail)
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

		if pid == 0 || !lockfile.IsProcessRunning(pid) {
			slog.Info("agent yielded gracefully", "worktree", ap.Entry.Worktree, "elapsed", time.Since(start).Truncate(time.Millisecond))
			return outcome(DrainPhaseYielded)
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Phase 3: Escalate to SIGTERM -> SIGKILL
	slog.Info("yield timeout expired, escalating to SIGTERM", "worktree", ap.Entry.Worktree, "timeout", yieldTimeout)
	s.StopAgent(ap, sigtermTimeout)
	return outcome(DrainPhaseSigterm)
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
// Both yield and SIGTERM timeouts are capped (see cappedDrainTimeouts) to keep
// daemon shutdown prompt.
//
// It returns one DrainOutcome per agent and whether every drain finished before
// deadline. A drain that overruns leaves its slot at DrainPhase "" — the
// goroutine is still running, so the slot must not be read as authoritative
// beyond "this worktree did not finish".
func (s *Supervisor) drainAllWithGrace(agents []*AgentProcess, deadline time.Time) ([]DrainOutcome, bool) {
	yieldTimeout, sigtermTimeout := s.logCappedDrainTimeouts()

	// Pre-sized and indexed by position; slots are seeded with the worktree name
	// so a drain that never finishes is still attributable. The mutex is
	// required rather than merely tidy: when the deadline expires we read these
	// slots while the straggler goroutines are still writing them.
	var outcomesMu sync.Mutex
	outcomes := make([]DrainOutcome, len(agents))
	var stopWg sync.WaitGroup
	for i, ap := range agents {
		outcomes[i].Worktree = ap.Entry.Worktree
		stopWg.Add(1)
		go func(idx int, agent *AgentProcess) {
			defer stopWg.Done()
			result := s.DrainWithGrace(agent, "shutdown", yieldTimeout, sigtermTimeout)
			outcomesMu.Lock()
			outcomes[idx] = result
			outcomesMu.Unlock()
		}(i, ap)
	}

	done := make(chan struct{})
	go func() {
		stopWg.Wait()
		close(done)
	}()
	completed := waitUntil(done, deadline)

	outcomesMu.Lock()
	snapshot := make([]DrainOutcome, len(outcomes))
	copy(snapshot, outcomes)
	outcomesMu.Unlock()

	if !completed {
		slog.Error("drain did not complete within the shutdown budget", "agents", len(agents))
		return snapshot, false
	}

	logDrainSummary(snapshot)
	return snapshot, true
}

// logCappedDrainTimeouts returns the shutdown drain timeouts, logging each one
// that the caller's configuration asked to exceed.
func (s *Supervisor) logCappedDrainTimeouts() (yield, sigterm time.Duration) {
	yield, sigterm = s.cappedDrainTimeouts()
	if configured := s.GetYieldTimeout(); configured > yield {
		slog.Info("capping yield timeout for daemon shutdown", "configured", configured, "capped", yield)
	}
	if configured := s.GetSigtermTimeout(); configured > sigterm {
		slog.Info("capping SIGTERM timeout for daemon shutdown", "configured", configured, "capped", sigterm)
	}
	return yield, sigterm
}

// logDrainSummary records per-role yield behavior for a completed drain, so a
// successful shutdown also says which agents yielded and which needed SIGTERM.
func logDrainSummary(outcomes []DrainOutcome) {
	yielded, stragglers := 0, []string{}
	for _, o := range outcomes {
		if o.Yielded() {
			yielded++
		} else {
			stragglers = append(stragglers, o.Worktree)
		}
	}
	slog.Info("drain complete",
		"agents", len(outcomes),
		"yielded", yielded,
		"sigterm", len(stragglers),
		"stragglers", stragglers)
}

// waitUntil blocks until done closes or deadline passes, reporting whether done
// closed in time. A deadline already in the past yields a single non-blocking
// check, never a negative timer duration.
func waitUntil(done <-chan struct{}, deadline time.Time) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
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

	// Set stop reason before signaling
	target.Mu.Lock()
	target.StopReason = reason
	target.Mu.Unlock()

	// Signal the agent to stop (safe against double-close)
	target.StopOnce.Do(func() {
		close(target.StopCh)
	})

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
	if entry.Mode == domain.AgentModeEphemeral && taskID == "" {
		return fmt.Errorf("ephemeral agent %q requires a task_id", entry.Worktree)
	}
	parentSessionID := ""
	if len(parentSessionIDs) > 0 {
		parentSessionID = strings.TrimSpace(parentSessionIDs[0])
	}

	if err := s.checkDuplicateAgent(entry.Worktree); err != nil {
		return err
	}

	// Resolve worktree path (outside lock — may do I/O)
	target, err := workspace.ResolveAgentTarget(entry.Worktree, entry.Repo)
	if err != nil {
		return fmt.Errorf("agent %q worktree: %w", entry.Worktree, err)
	}

	s.AgentsMu.RLock()
	agentCount := len(s.Agents)
	s.AgentsMu.RUnlock()
	roleConfig, err := s.resolveRoleConfig(entry.Role, agentCount)
	if err != nil {
		return err
	}

	ap := s.newRuntimeAgentProcess(entry, roleConfig, target.WorkDir, taskID, parentSessionID)

	// Authoritative duplicate check + slice append + WaitGroup increment under
	// a single write lock so Wg.Add can't race with Stop()'s Wg.Wait.
	s.AgentsMu.Lock()
	for _, existing := range s.Agents {
		if existing.Entry.Worktree == entry.Worktree {
			s.AgentsMu.Unlock()
			return fmt.Errorf("agent %q already exists", entry.Worktree)
		}
	}
	s.Agents = append(s.Agents, ap)
	s.Wg.Add(1)
	s.AgentsMu.Unlock()

	name := GoroutineAgentPrefix + ap.Entry.Worktree
	s.RegisterTick(name)
	go s.supervisedAgentBody(name, ap)

	slog.Info("agent added and started", "worktree", entry.Worktree, "role", entry.Role)
	return nil
}

func (s *Supervisor) newRuntimeAgentProcess(entry config.AgentEntry, roleConfig config.RoleConfig, workDir, taskID, parentSessionID string) *AgentProcess {
	return &AgentProcess{
		Entry:           entry,
		RoleConfig:      roleConfig,
		WorktreePath:    workDir,
		RepoConfig:      s.FindRepoConfig(entry.Repo),
		RequestedTaskID: taskID,
		ParentSessionID: parentSessionID,
		StopCh:          make(chan struct{}),
		Done:            make(chan struct{}),
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

// ---------------------------------------------------------------------------
// Shutdown budget and reporting
//
// These live here rather than in their own files because the supervisor package
// sits at its grandfathered file-count ceiling (scripts/package-size-allow.txt),
// and they are the drain's own vocabulary: the budget is derived from the drain
// timeouts, and the report is the drain's per-agent result.
// ---------------------------------------------------------------------------

// DrainPhase records how far DrainWithGrace got with one agent.
type DrainPhase string

const (
	// DrainPhaseAlreadyStopped means the agent had no live pid on entry.
	DrainPhaseAlreadyStopped DrainPhase = "already_stopped"
	// DrainPhaseYielded means the agent exited voluntarily within the yield timeout.
	DrainPhaseYielded DrainPhase = "yielded"
	// DrainPhaseSigterm means the yield timeout expired and StopAgent ran.
	DrainPhaseSigterm DrainPhase = "sigterm"
	// DrainPhaseYieldWriteFail means the yield file could not be written, so the
	// agent never got the chance to checkpoint before StopAgent ran.
	DrainPhaseYieldWriteFail DrainPhase = "yield_write_failed"
	// DrainPhaseUnfinished means the drain had not returned when the shutdown
	// budget expired, so how far it got is unknown.
	DrainPhaseUnfinished DrainPhase = "unfinished"
)

// DrainOutcome is the per-agent result of a shutdown drain.
type DrainOutcome struct {
	Worktree string
	Phase    DrainPhase
	Elapsed  time.Duration
}

// Yielded reports whether the agent exited without needing SIGTERM. An agent
// that was already stopped counts as yielded: there was nothing to interrupt.
func (o DrainOutcome) Yielded() bool {
	return o.Phase == DrainPhaseYielded || o.Phase == DrainPhaseAlreadyStopped
}

// StopReport is the structured result of StopWithBudget.
type StopReport struct {
	Budget         time.Duration  // the budget the caller granted
	Elapsed        time.Duration  // wall-clock time Stop actually took
	DrainOutcomes  []DrainOutcome // one per agent present at Stop entry
	DrainCompleted bool           // false if the budget expired during drainAllWithGrace
	WaitCompleted  bool           // false if Wg.Wait did not finish within the budget
}

// TimedOut reports whether any phase of Stop exceeded its budget.
func (r StopReport) TimedOut() bool { return !r.DrainCompleted || !r.WaitCompleted }

// StragglerWorktrees returns the worktrees that did not exit from yield alone —
// the agents to name in a shutdown-timeout log line.
func (r StopReport) StragglerWorktrees() []string {
	var out []string
	for _, o := range r.DrainOutcomes {
		if !o.Yielded() {
			out = append(out, o.Worktree)
		}
	}
	return out
}

// LogAttrs renders the report as slog key/value pairs.
func (r StopReport) LogAttrs() []any {
	return []any{
		"budget", r.Budget,
		"elapsed", r.Elapsed,
		"drain_completed", r.DrainCompleted,
		"wait_completed", r.WaitCompleted,
		"stragglers", r.StragglerWorktrees(),
	}
}

// shutdownDrainCap bounds each drain phase during daemon shutdown, regardless
// of how generous the configured restart-policy timeouts are.
const shutdownDrainCap = 30 * time.Second

// drainSlack is headroom over the sum of the yield and SIGTERM caps, covering
// the non-drain work in Stop (listener closes, mutation-buffer drain, Wg.Wait
// bookkeeping) so a healthy shutdown never trips the deadline.
const drainSlack = 15 * time.Second

// ShutdownBudget returns the wall-clock budget StopWithBudget needs for a
// healthy shutdown: the capped yield timeout plus the capped SIGTERM timeout
// (which DrainWithGrace runs sequentially per agent) plus slack. Callers use
// this to arm their own watchdog, so the watchdog and the work it guards can
// never disagree — the bug in PUPPET-39, where a 30s watchdog guarded work
// whose own legitimate worst case was 60s.
func (s *Supervisor) ShutdownBudget() time.Duration {
	yield, sigterm := s.cappedDrainTimeouts()
	budget := yield + sigterm + drainSlack
	if budget < drainSlack {
		// Defensive floor: the getters already fall back to their defaults on
		// non-positive config, so this is unreachable today.
		return drainSlack
	}
	return budget
}

// cappedDrainTimeouts returns the yield and SIGTERM timeouts drainAllWithGrace
// will actually use, applying the shutdown caps. Extracted so ShutdownBudget
// and drainAllWithGrace cannot drift apart.
func (s *Supervisor) cappedDrainTimeouts() (yield, sigterm time.Duration) {
	yield = s.GetYieldTimeout()
	if yield > shutdownDrainCap {
		yield = shutdownDrainCap
	}
	sigterm = s.GetSigtermTimeout()
	if sigterm > shutdownDrainCap {
		sigterm = shutdownDrainCap
	}
	return yield, sigterm
}

// Stop gracefully shuts down all agents. Safe to call multiple times.
func (s *Supervisor) Stop() {
	_ = s.StopWithBudget(s.ShutdownBudget())
}

// StopWithBudget is Stop with an explicit wall-clock budget, returning a
// structured report of what happened. Safe to call multiple times.
//
// Every wait inside is bounded by budget. In particular the wait for the
// superviseAgent goroutines is bounded, where it used to be a bare
// s.Wg.Wait() that could block forever: those goroutines sit in cmd.Wait(),
// which cannot return until every process holding the child's inherited stdout
// pipe closes it, and StopAgent's descendant-pgroup SIGKILL is a snapshot that
// can miss late or reparented forks. A goroutine wedged there cannot be
// cancelled from Go, so the only correct response is to stop waiting, report
// the straggler, and let the caller force-exit — which reclaims the fds via the
// kernel. Leaking here is deliberate and is only safe because the caller
// force-exits on TimedOut().
//
// The root cause of that wedge is the io.MultiWriter stdio wiring in
// setupAgentLogFile: handing the child an *os.File instead would let os/exec
// dup the fd directly and cmd.Wait() would not wait on a copy goroutine. That
// change touches the web UI Logs tab and the liveness watchdog's dependency on
// the daemon log's mtime, so it is tracked as a follow-up: PUPPET-41.
func (s *Supervisor) StopWithBudget(budget time.Duration) StopReport {
	start := time.Now()
	deadline := start.Add(budget)

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

	outcomes, drainCompleted := s.drainAllWithGrace(snapshot, deadline)

	// Wait for all superviseAgent goroutines to exit, bounded by the remaining
	// budget.
	wgDone := make(chan struct{})
	go func() {
		s.Wg.Wait()
		close(wgDone)
	}()
	waitCompleted := waitUntil(wgDone, deadline)

	return StopReport{
		Budget:         budget,
		Elapsed:        time.Since(start),
		DrainOutcomes:  outcomes,
		DrainCompleted: drainCompleted,
		WaitCompleted:  waitCompleted,
	}
}
