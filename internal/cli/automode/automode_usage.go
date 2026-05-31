package automode

import (
	"errors"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// buildSessionUsage constructs a usage record after an agent invocation
// finishes. When bridge is non-nil, reads the lock via the bridge; otherwise
// uses the local filesystem.
func buildSessionUsage(collector *usage.Collector, worktreePath, agentName, parentID string, startedAt, endedAt time.Time, invokeErr error, bridge cli.LockBridge) (usage.SessionUsage, bool) {
	if collector == nil {
		return usage.SessionUsage{}, false
	}

	// Derive exit code from error
	exitCode := 0
	if invokeErr != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(invokeErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	// Read task/epic context from lock file
	var taskID, epicID string
	if bridge != nil {
		if info, err := bridge.ReadLock(agentName); err == nil && info != nil {
			taskID = info.TaskID
		}
	} else {
		if info, err := cli.ReadLockFile(worktreePath); err == nil && info != nil {
			taskID = info.TaskID
		}
	}
	epicID = parentID

	record := collector.Finalize(taskID, epicID, startedAt, endedAt, exitCode)
	return record, true
}

// appendSessionUsage persists a usage record. Failures are logged but do not
// interrupt the auto mode loop.
func appendSessionUsage(store *usage.Store, record usage.SessionUsage) {
	if store == nil {
		return
	}
	if err := store.Append(record); err != nil {
		log.Printf("[auto] Warning: failed to record usage: %v", err)
	}
}

// captureHEADRef returns the current HEAD ref for the worktree (empty on error).
func CaptureHEADRef(worktreePath string) string {
	out, err := cli.RunGitCommand(worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
