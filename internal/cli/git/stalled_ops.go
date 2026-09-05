package git

import (
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
	"github.com/tysonthomas9/loomcli/internal/cli/integration"
)

// StalledOpThreshold is the age past which an in-progress git operation stops
// being "someone is merging right now" and starts being a stalled one. It
// matches the doctor check's default deliberately: two surfaces reporting the
// same condition at two different ages would be worse than either alone.
const StalledOpThreshold = 10 * time.Minute

// StalledWorktree is one shared integration worktree stuck mid-operation.
//
// The fields are flat rather than an embedded gitstate.State so that callers
// can report one without taking on gitstate as a dependency of their own.
type StalledWorktree struct {
	Repo     string
	Path     string
	Branch   string
	Op       string
	Head     string
	Unmerged int
	Age      time.Duration
	AgeKnown bool
	Summary  string // one-line human rendering
}

// StalledSharedWorktrees returns the shared `local/union` worktrees declared in
// the workspace's integration.yaml that are sitting in an unfinished merge,
// rebase, cherry-pick, revert or bisect past StalledOpThreshold.
//
// It never repairs one. A live sibling integrator may own that merge, and
// ownership is unprovable from a worktree alone — the sanctioned repair is an
// operator running `loom doctor --fix`.
//
// Returns nil for every uninteresting case (no contract, nothing declared,
// nothing stuck, git unavailable), so callers can treat a nil result as
// "nothing to say" without an error path.
func StalledSharedWorktrees() []StalledWorktree {
	shared, err := integration.SharedWorktrees()
	if err != nil || len(shared) == 0 {
		return nil
	}
	var out []StalledWorktree
	for _, sw := range shared {
		st, inspectErr := gitstate.Inspect(sw.Path)
		if inspectErr != nil || st.Op == gitstate.OpNone {
			continue
		}
		// An age that cannot be determined is unknown, not young: report it.
		if st.AgeKnown() && st.Age() < StalledOpThreshold {
			continue
		}
		out = append(out, StalledWorktree{
			Repo:     sw.Repo,
			Path:     sw.Path,
			Branch:   sw.Branch,
			Op:       string(st.Op),
			Head:     st.Head,
			Unmerged: st.Unmerged,
			Age:      st.Age().Truncate(time.Second),
			AgeKnown: st.AgeKnown(),
			Summary:  st.String(),
		})
	}
	return out
}

// AbortInProgressOp aborts an unfinished merge/rebase/cherry-pick left behind
// in an AGENT worktree and returns a one-line description of what it did ("" if
// there was nothing to abort).
//
// Only ever call this for a worktree loom owns. Recovery already destroys
// uncommitted work in an agent worktree via `git clean`, so aborting there is
// strictly less destructive than what already happens — that reasoning does not
// extend to a shared worktree, which is why StalledSharedWorktrees only reports.
func AbortInProgressOp(worktreePath string) (string, error) {
	st, err := gitstate.Inspect(worktreePath)
	if err != nil || st.Op == gitstate.OpNone {
		return "", nil
	}
	if abortErr := gitstate.Abort(worktreePath, st.Op); abortErr != nil {
		return "", fmt.Errorf("abort %s in %s: %w", st.Op, worktreePath, abortErr)
	}
	return fmt.Sprintf("aborted in-progress %s in %s (head=%s, %d unmerged)",
		st.Op, worktreePath, st.Head, st.Unmerged), nil
}
