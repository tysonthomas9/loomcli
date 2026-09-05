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

// releaseClaimOnComplete releases the fleet-db claim on the task referenced by
// the worktree's .agent.lock file, best-effort. When the task is still
// in_progress this puts it back to open/unassigned — not merely dropping the
// operational lock — so the next agent can claim it within one poll interval
// instead of waiting out fleet-db's claim reaper (~5 min, PUPPET-467).
//
// The actor passed is info.AgentName, the agent's own name. That is usually NOT
// the issue's assignee: with an API key configured, fleet-db strips the X-Actor
// override header and attributes every claim to the process's configured actor.
// ReleaseClaim accepts either identity as proof of ownership, which is what
// makes this path effective; resolving the two belongs there, not here.
//
// Safe to call when:
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
	releaser, ok := ib.(backend.ClaimReleaser)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseClaimTimeout)
	defer cancel()
	if err := releaser.ReleaseClaim(ctx, info.TaskID, info.AgentName); err != nil {
		fmt.Fprintf(os.Stderr, "complete: release claim on %s failed (continuing): %v\n",
			info.TaskID, err)
	}
}

// ClaimStillHeld reports whether taskID is STILL claimed by actor — the
// observable inverse of releaseClaimOnComplete having run.
//
// It is the discriminator between "the agent finished" and "the agent's turn
// ended": both exit 0, and both leave a task id on the worktree lock, so exit
// status alone cannot tell them apart. `loom complete` can: every terminal path
// in the worker prompts runs it — closed, review, blocked, needs-revision, and
// the give-up paths alike — and it calls ReleaseClaim, which drops a still
// in_progress claim back to open/unassigned. A task that is in_progress under
// this actor's name after the process is gone is therefore one whose agent
// never signaled completion.
//
// Claim state is not on the worktree lock and not on the AgentProcess, so this
// costs one GET. There is no cheaper sound signal: the completion marker file
// `loom complete` writes is keyed by worktree rather than by run, is never
// consumed on the daemon path, and would read a previous cycle's completion as
// this one's.
//
// POSITIVE EVIDENCE ONLY. A nil backend, a failed GET, an unreadable status, a
// claim held by somebody else (a sibling already re-claimed it) — all return
// false. Callers use a true answer to take a LESS destructive path, so an
// ambiguous answer must keep the established behavior rather than manufacture
// an incomplete run out of a backend blip.
func ClaimStillHeld(ctx context.Context, ib backend.IssueBackend, taskID, actor string) bool {
	if ib == nil || taskID == "" || actor == "" {
		return false
	}
	detail, err := ib.Get(ctx, taskID)
	if err != nil || detail == nil {
		return false
	}
	return detail.Status == "in_progress" && detail.Assignee == actor
}

// claimStillHeldForLock answers ClaimStillHeld for a worktree lock, resolving
// the actor the same way releaseClaimOnComplete does (the lock's AgentName is
// the identity the claim was taken under). Keeping this separate lets callers
// that already read the lock use the same completion discriminator without a
// second, potentially different lock read.
func claimStillHeldForLock(info *cli.LockInfo) bool {
	if info == nil || info.TaskID == "" || info.AgentName == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseClaimTimeout)
	defer cancel()
	return ClaimStillHeld(ctx, cli.DefaultIssueBackend(), info.TaskID, info.AgentName)
}

// claimStillHeldForWorktree answers ClaimStillHeld for the task recorded on the
// worktree's own lock. Used by the in-process worker, which — unlike the
// supervisor — has no handle on the issue backend beyond the process default.
func claimStillHeldForWorktree(worktreePath string) bool {
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil {
		return false
	}
	return claimStillHeldForLock(info)
}
