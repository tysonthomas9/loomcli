package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// releaseClaimTimeout bounds how long `loom complete` waits for the
// best-effort claim release. Kept short so a slow/down fleet-db cannot
// block agent exit perceptibly.
const releaseClaimTimeout = 5 * time.Second

// releaseClaimOnComplete drops the fleet-db claim lock for the task
// referenced by the worktree lock file, best-effort. Safe to call when:
//   - the backend doesn't implement backend.ClaimReleaser (api/agentipc/mock)
//   - the .agent.lock file is missing or malformed
//   - the lock entry has an empty TaskID
//   - the claim is already released or held by a different actor
//
// Failures are logged but do not block the completion signal — the auto-mode
// parent still needs the signal file written. See LOOM-1 for the original
// leak path this closes.
func releaseClaimOnComplete(worktreePath string) {
	info, err := cli.ReadLockFile(worktreePath)
	if err != nil || info == nil || info.TaskID == "" {
		return
	}
	releaser, ok := cli.DefaultIssueBackend().(backend.ClaimReleaser)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), releaseClaimTimeout)
	defer cancel()
	if relErr := releaser.ReleaseClaim(ctx, info.TaskID); relErr != nil {
		slog.Warn("release claim on complete failed; continuing", "task_id", info.TaskID, "err", relErr)
	}
}
