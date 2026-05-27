package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// releaseClaimTimeout caps the time we wait for the fleet /release call
// during `loom complete`. The agent's exit path must not perceptibly stall
// on a slow / unreachable backend.
const releaseClaimTimeout = 5 * time.Second

// releaseClaimOnComplete drops the fleet-db claim lock on the task referenced
// by the worktree's .agent.lock file, best-effort. Safe to call when:
//   - the lock file is missing or unreadable (no-op)
//   - the lock has no TaskID or AgentName (agent exited before claiming, no-op)
//   - the release call itself returns an error (logged, but not surfaced)
//
// Closes the planner-leaks-claim-lock path documented in LOOM-1: a planner
// that wrote only --design and exited cleanly previously left the claim held
// even though the worktree lock and process were gone, blocking downstream
// workers whose ready-queue filter matched the task.
func releaseClaimOnComplete(worktreePath string) {
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil || info == nil || info.TaskID == "" || info.AgentName == "" {
		return
	}
	ib := cli.DefaultIssueBackend()
	if ib == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseClaimTimeout)
	defer cancel()
	if err := releaseClaimForActor(ctx, ib, info.TaskID, info.AgentName); err != nil {
		fmt.Fprintf(os.Stderr, "complete: release claim on %s failed (continuing): %v\n",
			info.TaskID, err)
	}
}

func releaseClaimForActor(ctx context.Context, ib backend.IssueBackend, taskID, actor string) error {
	if taskID == "" || actor == "" || ib == nil {
		return nil
	}
	if releaser, ok := ib.(backend.ClaimReleaser); ok {
		return releaser.ReleaseClaim(ctx, taskID, actor)
	}
	current, err := ib.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if current == nil || current.Assignee != actor {
		return nil
	}
	if current.Status == "in_progress" {
		open := "open"
		empty := ""
		return ib.Update(ctx, taskID, backend.UpdateParams{Status: &open, Assignee: &empty})
	}
	return ib.ReleaseIssueLock(ctx, taskID, actor)
}
