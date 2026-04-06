package daemon

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// classifyAgentExit reads the lock file (before recovery clears it) and classifies
// the agent's exit into an error class. Sets ap.lastError and ap.lastNoWork.
func (d *Daemon) classifyAgentExit(ap *AgentProcess, exitCode int) {
	// Read lock info before recovery clears it (for logging and NoWork detection)
	lockInfo, _, _ := cli.CheckLock(ap.worktreePath)
	if lockInfo != nil && lockInfo.TaskID != "" {
		log.Printf("[daemon] Agent %s: exited with code %d (task %s: %s)",
			ap.entry.Worktree, exitCode, lockInfo.TaskID, lockInfo.TaskTitle)
	} else {
		log.Printf("[daemon] Agent %s: exited with code %d", ap.entry.Worktree, exitCode)
	}

	// Resolve backend for classification
	ap.mu.Lock()
	backend := ap.entry.Backend
	logPath := ap.logFilePath
	ap.mu.Unlock()
	if backend == "" {
		backend = d.configSnapshot().Backend
	}

	if exitCode == 0 && (lockInfo == nil || lockInfo.TaskID == "") {
		// No work available — exit 0 with no task claimed
		ap.mu.Lock()
		ap.lastError = &agenterr.AgentError{
			Class:   agenterr.NoWork,
			Message: "no claimable tasks",
			Backend: backend,
		}
		ap.lastNoWork = true
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: no work available (idle)", ap.entry.Worktree)
	} else if exitCode != 0 {
		ae := agenterr.ClassifyFromLog(logPath, exitCode, backend)
		ap.mu.Lock()
		ap.lastError = ae
		ap.lastNoWork = false
		ap.mu.Unlock()
		log.Printf("[daemon] Agent %s: classified error: %v", ap.entry.Worktree, ae)
	} else {
		ap.mu.Lock()
		ap.lastError = nil
		ap.lastNoWork = false
		ap.mu.Unlock()
	}
}

// handleAgentCheckpoint saves a checkpoint on non-zero exit (before recovery clears the
// worktree) or clears the checkpoint on successful exit. For yield exits (exit 0 with
// yield file present), a yield checkpoint is saved instead of clearing.
func (d *Daemon) handleAgentCheckpoint(ap *AgentProcess, exitCode int) {
	if exitCode == 0 {
		// Check if this was a yield exit — save checkpoint instead of clearing
		if IsYieldRequested(ap.worktreePath) {
			d.saveYieldCheckpoint(ap)
			return
		}
		lockDir := cli.ResolveLockDir(ap.worktreePath)
		if err := config.ClearCheckpoint(lockDir); err != nil {
			log.Printf("[daemon] Agent %s: failed to clear checkpoint: %v", ap.entry.Worktree, err)
		}
		return
	}
	d.saveAgentCheckpoint(ap, exitCode)
}

// saveAgentCheckpoint captures the current worktree diff and agent state into a
// checkpoint file. Called when an agent exits non-zero before recovery clears the worktree.
func (d *Daemon) saveAgentCheckpoint(ap *AgentProcess, exitCode int) {
	lockInfo, _, _ := cli.CheckLock(ap.worktreePath)
	if lockInfo == nil || lockInfo.TaskID == "" {
		return
	}

	diff := captureGitDiff(ap.worktreePath, config.MaxDiffBytes)
	errClass := ""
	ap.mu.Lock()
	if ap.lastError != nil {
		errClass = ap.lastError.Class.String()
	}
	epicID := ap.assignedEpicID
	ap.mu.Unlock()

	cp := &config.Checkpoint{
		AgentName:  lockInfo.AgentName,
		TaskID:     lockInfo.TaskID,
		EpicID:     epicID,
		GitDiff:    diff,
		ExitCode:   exitCode,
		ErrorClass: errClass,
		Timestamp:  time.Now(),
	}
	lockDir := cli.ResolveLockDir(ap.worktreePath)
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		log.Printf("[daemon] Agent %s: failed to save checkpoint: %v", ap.entry.Worktree, err)
	} else {
		log.Printf("[daemon] Agent %s: saved checkpoint for task %s", ap.entry.Worktree, lockInfo.TaskID)
	}
}

// saveYieldCheckpoint captures the worktree state when an agent is preempted
// via yield. Unlike saveAgentCheckpoint (crash path), this sets ErrorClass to
// "Yielded" and records the yield reason from the yield file.
func (d *Daemon) saveYieldCheckpoint(ap *AgentProcess) {
	lockInfo, _, _ := cli.CheckLock(ap.worktreePath)
	if lockInfo == nil || lockInfo.TaskID == "" {
		return
	}

	diff := captureGitDiff(ap.worktreePath, config.MaxDiffBytes)

	yieldReason := "unknown"
	if req, err := ReadYieldFile(ap.worktreePath); err == nil && req != nil && req.Reason != "" {
		yieldReason = req.Reason
	}

	ap.mu.Lock()
	epicID := ap.assignedEpicID
	ap.mu.Unlock()

	cp := &config.Checkpoint{
		AgentName:   lockInfo.AgentName,
		TaskID:      lockInfo.TaskID,
		EpicID:      epicID,
		GitDiff:     diff,
		ExitCode:    0,
		ErrorClass:  "Yielded",
		YieldReason: yieldReason,
		Timestamp:   time.Now(),
	}
	lockDir := cli.ResolveLockDir(ap.worktreePath)
	if err := config.SaveCheckpoint(lockDir, cp); err != nil {
		log.Printf("[daemon] Agent %s: failed to save yield checkpoint: %v", ap.entry.Worktree, err)
	} else {
		log.Printf("[daemon] Agent %s: saved yield checkpoint for task %s (reason: %s)",
			ap.entry.Worktree, lockInfo.TaskID, yieldReason)
	}
}
