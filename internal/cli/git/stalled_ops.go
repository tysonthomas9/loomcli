package git

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
)

// AbortInProgressOp aborts an unfinished merge, rebase, cherry-pick, revert or
// bisect left behind in an AGENT worktree, after writing a snapshot of it
// under snapshotRoot. It returns a one-line description of what it did, or ""
// when there was nothing to abort.
//
// The abort resets the index and every tracked file, which is where the
// agent's conflict resolutions live, and a rebase abort moves the branch back
// past the commits the rebase had already made. The snapshot is what keeps
// that work recoverable, so no abort happens without a complete one: a failed
// snapshot leaves the operation in place and returns an error.
//
// Only ever call this for a worktree loom owns, and only once its agent has
// exited.
func AbortInProgressOp(worktreePath, snapshotRoot string) (string, error) {
	st, err := gitstate.Inspect(worktreePath)
	if err != nil || st.Op == gitstate.OpNone {
		return "", nil
	}
	stamp := strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-")
	dest := filepath.Join(snapshotRoot, fmt.Sprintf("%s-%s-%s", filepath.Base(worktreePath), st.Op, stamp))
	if err := gitstate.Snapshot(worktreePath, dest); err != nil {
		return "", fmt.Errorf("left the in-progress %s in %s untouched: %w", st.Op, worktreePath, err)
	}
	if err := gitstate.Abort(worktreePath, st.Op); err != nil {
		return "", fmt.Errorf("abort %s in %s (snapshot in %s): %w", st.Op, worktreePath, dest, err)
	}
	return fmt.Sprintf("aborted in-progress %s in %s (head=%s, %d unmerged); snapshot in %s",
		st.Op, worktreePath, st.Head, st.Unmerged, dest), nil
}
