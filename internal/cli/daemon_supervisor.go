package cli

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// resetWorktreeBranches moves all worktrees back to their default
// (worktree-named) branches. This prevents cross-checkout deadlocks
// when epic assignments differ from a prior daemon run — git refuses
// to checkout a branch that is already checked out in another worktree.
func (d *Daemon) resetWorktreeBranches() {
	d.agentsMu.RLock()
	snapshot := make([]*AgentProcess, len(d.agents))
	copy(snapshot, d.agents)
	d.agentsMu.RUnlock()

	for _, ap := range snapshot {
		current, err := GetCurrentBranch(ap.worktreePath)
		if err != nil {
			slog.Warn("failed to get branch", "worktree", ap.entry.Worktree, "err", err)
			continue
		}
		defaultBranch := ap.entry.Worktree
		if current == defaultBranch {
			continue
		}
		slog.Info("resetting worktree branch", "worktree", ap.entry.Worktree, "from", current, "to", defaultBranch)
		// Discard dirty state before switching
		clean, _ := IsCleanWorkingTree(ap.worktreePath)
		if !clean {
			if err := discardDirtyState(ap.worktreePath); err != nil {
				slog.Warn("discard dirty state failed", "worktree", ap.entry.Worktree, "err", err)
			}
		}
		if err := GitCheckout(ap.worktreePath, defaultBranch); err != nil {
			slog.Warn("failed to reset worktree", "worktree", ap.entry.Worktree, "branch", defaultBranch, "err", err)
		}
	}
}

// Start launches supervisor goroutines for all configured agents.
func (d *Daemon) Start() error {
	d.shutdown = make(chan struct{})

	// Reset all worktrees to their default branches to prevent
	// cross-checkout conflicts from prior daemon runs.
	d.resetWorktreeBranches()

	// Compute initial config hash for reconciler no-op detection.
	// Take reconcileMu to stay consistent with reloadAndReconcile's write pattern.
	d.reconcileMu.Lock()
	d.configHash = computeConfigHash(d.config)
	d.reconcileMu.Unlock()

	// Start healthChecker goroutine
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.healthChecker()
	}()

	// Start configReconciler goroutine
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.configReconciler()
	}()

	// Initialize stop/done channels and start superviseAgent goroutine for each agent
	d.agentsMu.RLock()
	snapshot := make([]*AgentProcess, len(d.agents))
	copy(snapshot, d.agents)
	d.agentsMu.RUnlock()

	for _, ap := range snapshot {
		ap.stopCh = make(chan struct{})
		ap.done = make(chan struct{})
		d.wg.Add(1)
		go func(agent *AgentProcess) {
			defer d.wg.Done()
			defer close(agent.done)
			d.superviseAgent(agent)
		}(ap)
	}

	return nil
}

// Stop gracefully shuts down all agents. Safe to call multiple times.
func (d *Daemon) Stop() {
	// Signal all goroutines to stop (protected from double-close)
	d.shutdownOnce.Do(func() {
		close(d.shutdown)
	})

	// Close the control socket listener (if running)
	if d.controlListener != nil {
		_ = d.controlListener.Close()
	}

	// Unblock any agents waiting for concurrency slots
	d.concurrency.Close()

	// Stop all agent processes
	d.agentsMu.RLock()
	snapshot := make([]*AgentProcess, len(d.agents))
	copy(snapshot, d.agents)
	d.agentsMu.RUnlock()

	for _, ap := range snapshot {
		d.stopAgent(ap)
	}

	// Wait for all superviseAgent goroutines to exit
	d.wg.Wait()
}

// superviseAgent is the main loop for a single agent (runs in goroutine).
func (d *Daemon) superviseAgent(ap *AgentProcess) {
	slog.Info("starting agent supervisor", "worktree", ap.entry.Worktree, "role", ap.entry.Role)

	for {
		// Check shutdown or per-agent stop before each cycle
		select {
		case <-d.shutdown:
			slog.Info("shutdown signal received", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			ap.stopReason = StopReasonShutdown
			ap.mu.Unlock()
			return
		case <-ap.stopCh:
			slog.Info("stop signal received", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			if ap.stopReason == "" {
				ap.stopReason = StopReasonConfigRemoved
			}
			ap.mu.Unlock()
			return
		default:
		}

		// Clear session state from previous iteration
		ap.mu.Lock()
		ap.session = nil
		ap.transcriptPath = ""
		ap.beforeRef = ""
		ap.mu.Unlock()

		// Acquire concurrency slot for this role (blocks if at limit)
		if !d.concurrency.Acquire(ap.entry.Role) {
			slog.Info("concurrency tracker closed, exiting", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			ap.stopReason = StopReasonShutdown
			ap.mu.Unlock()
			return
		}

		// 1. Pre-flight recovery
		if err := d.recoverAgent(ap, 0); err != nil {
			slog.Warn("pre-flight recovery failed", "worktree", ap.entry.Worktree, "err", err)
			// Continue with caution - spawn may still work
		}

		// 1.5. Assign epic to worktree (only if parent is configured)
		var epicID string
		if ap.entry.Parent != "" {
			epicID = ap.entry.Parent
			slog.Info("using configured epic", "worktree", ap.entry.Worktree, "epic", epicID)
		}
		ap.mu.Lock()
		ap.assignedEpicID = epicID
		ap.mu.Unlock()

		// Emit epic_assigned event if an epic was assigned
		if epicID != "" {
			if evt, err := events.NewEvent(events.EpicAssigned, ap.entry.Worktree, ap.entry.Role, epicID, events.EpicAssignedData{EpicID: epicID}); err == nil {
				d.emitEvent(evt)
			}
		}

		// 2. Ensure correct branch for epic assignment
		targetBranch := ap.entry.Worktree // default: agent-name branch
		if epicID != "" {
			targetBranch = epicBranchName(epicID)
		}
		slog.Info("ensuring branch", "worktree", ap.entry.Worktree, "branch", targetBranch)
		if err := EnsureWorktreeBranch(ap.worktreePath, targetBranch, ap.resolveRemote(), ap.resolveRemoteBranch()); err != nil {
			slog.Warn("branch setup failed", "worktree", ap.entry.Worktree, "err", err)
			d.concurrency.Release(ap.entry.Role)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 2.5. Create session for liveness tracking + capture git state for finalization
		beadsDir := GetBeadsDir()
		if sessStore, err := sessions.NewStore(beadsDir); err == nil {
			phase := "implementation"
			if ap.entry.Role == "plan" {
				phase = "planning"
			}
			ap.mu.Lock()
			restartCount := ap.restartCount
			ap.mu.Unlock()

			sess, err := sessStore.CreateSession(sessions.CreateOptions{
				AgentName:  ap.entry.Worktree,
				Backend:    d.getEffectiveBackend(ap),
				EpicID:     epicID,
				Phase:      phase,
				AttemptNum: restartCount,
			})
			if err == nil {
				txPath := filepath.Join(beadsDir, "sessions", sess.SessionID(), "transcript.jsonl")
				bRef := captureHEADRef(ap.worktreePath)
				ap.mu.Lock()
				ap.session = sess
				ap.transcriptPath = txPath
				ap.beforeRef = bRef
				ap.mu.Unlock()
			} else {
				slog.Warn("session creation failed, watchdog will use log file", "worktree", ap.entry.Worktree, "err", err)
			}
		} else {
			slog.Warn("session store unavailable, watchdog will use log file", "worktree", ap.entry.Worktree, "err", err)
		}

		// 3. Spawn subprocess
		if err := d.spawnAgent(ap); err != nil {
			slog.Warn("spawn failed", "worktree", ap.entry.Worktree, "err", err)
			// Finalize orphaned session from step 2.5 (would stay in "running" forever)
			ap.mu.Lock()
			orphanSess := ap.session
			ap.session = nil
			ap.mu.Unlock()
			if orphanSess != nil {
				_ = orphanSess.Finalize(sessions.FinalizeOptions{ExitCode: -1, ErrorClass: "spawn_failure"})
			}
			d.concurrency.Release(ap.entry.Role)
			if !d.handleRestartAfterError(ap) {
				return
			}
			continue
		}

		// 4. Wait for exit
		exitCode := d.waitForAgent(ap)

		// 4.5. Classify error and detect NoWork (before recovery clears lock file)
		d.classifyAgentExit(ap, exitCode)

		// 4.6. Finalize daemon-created session
		ap.mu.Lock()
		sess := ap.session
		bRef := ap.beforeRef
		ap.session = nil // prevent double finalization on shutdown
		ap.mu.Unlock()
		if sess != nil {
			taskID := ""
			if info, lockErr := ReadLockFile(ap.worktreePath); lockErr == nil {
				taskID = info.TaskID
			}
			diffStats := ComputeDiffStats(ap.worktreePath, bRef)

			ap.mu.Lock()
			errClass := ""
			if ap.lastError != nil {
				errClass = ap.lastError.Class.String()
			}
			ap.mu.Unlock()

			if err := sess.Finalize(sessions.FinalizeOptions{
				TaskID:     taskID,
				ExitCode:   exitCode,
				ErrorClass: errClass,
				DiffStats: sessions.DiffStats{
					FilesChanged: diffStats.FilesChanged,
					LinesAdded:   diffStats.LinesAdded,
					LinesRemoved: diffStats.LinesRemoved,
				},
			}); err != nil {
				slog.Warn("session finalization failed", "worktree", ap.entry.Worktree, "err", err)
			}
		}

		// 4.7. Checkpoint management (save on error, clear on success)
		d.handleAgentCheckpoint(ap, exitCode)

		// 5. Post-mortem recovery (exit-code-aware)
		if err := d.recoverAgent(ap, exitCode); err != nil {
			slog.Warn("post-mortem recovery failed", "worktree", ap.entry.Worktree, "err", err)
			// Non-fatal, continue with restart logic
		}

		// 5.5. Ensure PR exists for epic branch (non-fatal)
		ap.mu.Lock()
		currentEpicID := ap.assignedEpicID
		ap.mu.Unlock()
		if currentEpicID != "" {
			if err := EnsureEpicPR(ap.worktreePath, currentEpicID, d.eventBus); err != nil {
				slog.Warn("PR creation failed", "worktree", ap.entry.Worktree, "err", err)
				// Non-fatal — don't block restart
			}
		}

		// 5.6. Release concurrency slot so waiting agents can proceed
		d.concurrency.Release(ap.entry.Role)

		// 6. Epic exhaustion check
		d.handleEpicTransition(ap)

		// 7. Check shutdown or per-agent stop after subprocess exit
		select {
		case <-d.shutdown:
			slog.Info("shutdown signal received after exit", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			ap.stopReason = StopReasonShutdown
			ap.mu.Unlock()
			return
		case <-ap.stopCh:
			slog.Info("stop signal received after exit", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			if ap.stopReason == "" {
				ap.stopReason = StopReasonConfigRemoved
			}
			ap.mu.Unlock()
			return
		default:
		}

		// 7.5. Check for backend failover (before restart decision)
		if d.tryFallbackBackend(ap) {
			slog.Info("backend failover triggered", "worktree", ap.entry.Worktree, "backend", d.getEffectiveBackend(ap))
			continue
		}

		// 8. Restart decision
		if !d.shouldRestart(ap) {
			slog.Warn("max restarts exceeded, stopping supervisor", "worktree", ap.entry.Worktree)
			return
		}

		// 9. Backoff sleep (interruptible)
		backoff := d.computeBackoff(ap)
		ap.mu.Lock()
		count := ap.restartCount
		ap.backoffUntil = time.Now().Add(backoff)
		ap.mu.Unlock()
		slog.Info("waiting before restart", "worktree", ap.entry.Worktree, "backoff", backoff, "attempt", count)

		// Emit agent_restarted event
		if evt, err := events.NewEvent(events.AgentRestarted, ap.entry.Worktree, ap.entry.Role, "", events.AgentRestartedData{PID: 0, RestartCount: count}); err == nil {
			d.emitEvent(evt)
		}

		var backoffAction string
		select {
		case <-time.After(backoff):
			backoffAction = "continue"
		case <-d.shutdown:
			backoffAction = "shutdown"
		case <-ap.stopCh:
			backoffAction = "stop"
		}

		ap.mu.Lock()
		ap.backoffUntil = time.Time{}
		ap.mu.Unlock()

		if backoffAction == "shutdown" {
			slog.Info("shutdown during backoff", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			ap.stopReason = StopReasonShutdown
			ap.mu.Unlock()
			return
		}
		if backoffAction == "stop" {
			slog.Info("stop signal during backoff", "worktree", ap.entry.Worktree)
			ap.mu.Lock()
			if ap.stopReason == "" {
				ap.stopReason = StopReasonConfigRemoved
			}
			ap.mu.Unlock()
			return
		}
	}
}

// AgentCount returns the number of configured agents.
func (d *Daemon) AgentCount() int {
	d.agentsMu.RLock()
	n := len(d.agents)
	d.agentsMu.RUnlock()
	return n
}

// Agents returns a snapshot of all agent statuses for inspection.
// The returned SupervisedAgentStatus structs are safe to use without synchronization.
func (d *Daemon) Agents() []SupervisedAgentStatus {
	d.agentsMu.RLock()
	snapshot := make([]*AgentProcess, len(d.agents))
	copy(snapshot, d.agents)
	d.agentsMu.RUnlock()

	result := make([]SupervisedAgentStatus, len(snapshot))
	for i, ap := range snapshot {
		ap.mu.Lock()
		result[i] = SupervisedAgentStatus{
			Worktree:       ap.entry.Worktree,
			Role:           ap.entry.Role,
			Repo:           ap.entry.Repo,
			WorktreePath:   ap.worktreePath,
			PID:            ap.pid,
			RestartCount:   ap.restartCount,
			LastStart:      ap.lastStart,
			LastExit:       ap.lastExit,
			LastExitCode:   ap.lastExitCode,
			AssignedEpicID: ap.assignedEpicID,
			StopReason:     ap.stopReason,
			NoWorkCount:    ap.noWorkCount,
			BackoffUntil:   ap.backoffUntil,
		}
		if ap.lastError != nil {
			result[i].LastErrorClass = ap.lastError.Class.String()
		}
		ap.mu.Unlock()
		// Resolve backend name outside the lock (getEffectiveBackend acquires ap.mu)
		result[i].CurrentBackend = d.getEffectiveBackend(ap)
		// Resolve remote branch (reads immutable config, no mutex needed)
		result[i].RemoteBranch = ap.resolveRemoteBranch()
	}
	return result
}
