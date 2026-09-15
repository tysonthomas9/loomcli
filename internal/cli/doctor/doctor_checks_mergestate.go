package doctor

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
)

// --- merge_in_progress check ---
//
// A worktree left mid-merge, mid-rebase or mid-cherry-pick silently blocks
// whoever works in it next. This check reports the workspace's repo clones
// and agent worktrees that have been in such a state past an age gate. It
// never repairs one, `--fix` included: a live agent may own the operation,
// and no lock covers that decision. After an agent exits, recovery snapshots
// and aborts what it left behind (see agent.RecoverWorktree).

// defaultMergeStaleThreshold is the age past which an in-progress operation
// stops being "someone is resolving it right now" and starts being a problem.
// A presence gate instead of an age gate would fire on every healthy merge and
// get ignored within a day.
const defaultMergeStaleThreshold = 10 * time.Minute

// mergeStaleThreshold reads LOOM_DOCTOR_MERGE_STALE. An unparseable value falls
// back to the default rather than failing the check: a typo in an env var must
// not cost the operator the diagnosis.
func mergeStaleThreshold() time.Duration {
	raw := os.Getenv("LOOM_DOCTOR_MERGE_STALE")
	if raw == "" {
		return defaultMergeStaleThreshold
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return defaultMergeStaleThreshold
	}
	return d
}

// candidate is one worktree to inspect.
type candidate struct {
	label string
	path  string
}

// offender is a candidate found mid-operation past the age gate.
type offender struct {
	candidate
	state gitstate.State
}

// Package-level seams, mirroring getSignalDir in doctor_checks_stale.go, so the
// check can be tested without standing up a whole workspace config.
var (
	localWorktreeSource = defaultLocalWorktrees
	inspectWorktree     = gitstate.Inspect
)

func defaultLocalWorktrees() []candidate {
	var out []candidate
	seen := map[string]struct{}{}
	add := func(name, path string) {
		if path == "" {
			return
		}
		if _, dup := seen[path]; dup {
			return
		}
		seen[path] = struct{}{}
		out = append(out, candidate{label: name, path: path})
	}
	if wts, err := cli.DiscoverWorktrees(); err == nil {
		for _, wt := range wts {
			add(wt.Name, wt.Path)
		}
	}
	if wts, err := cli.DiscoverAgentWorktrees(); err == nil {
		for _, wt := range wts {
			add(wt.Name, wt.Path)
		}
	}
	return out
}

// findOffenders inspects every candidate and keeps the ones past the age gate.
// An operation younger than the threshold is skipped entirely: someone may be
// resolving it right now. An operation whose age cannot be determined is kept:
// unknown is not young.
func findOffenders(cands []candidate, threshold time.Duration) []offender {
	var out []offender
	for _, c := range cands {
		st, err := inspectWorktree(c.path)
		if err != nil || st.Op == gitstate.OpNone {
			continue
		}
		if st.AgeKnown() && st.Age() < threshold {
			continue
		}
		out = append(out, offender{candidate: c, state: st})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

func checkMergeInProgress() CheckResult {
	cands := localWorktreeSource()
	if len(cands) == 0 {
		return CheckResult{} // skip — nothing to inspect
	}

	offenders := findOffenders(cands, mergeStaleThreshold())
	if len(offenders) == 0 {
		return CheckResult{
			Name:    "merge_in_progress",
			Status:  StatusPass,
			Summary: fmt.Sprintf("no stalled merges (%d worktree(s) checked)", len(cands)),
		}
	}
	return mergeStateResult(offenders)
}

func mergeStateResult(offenders []offender) CheckResult {
	details := make([]string, 0, len(offenders)+1)
	for _, o := range offenders {
		details = append(details, fmt.Sprintf("%s — %s", o.label, o.state.String()))
	}
	details = append(details,
		"Finish or abort the operation in that worktree once no agent is working in it. "+
			"Nothing is aborted automatically while its agent may be running.")
	return CheckResult{
		Name:    "merge_in_progress",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d worktree(s) stuck mid-operation", len(offenders)),
		Detail:  strings.Join(details, "\n"),
	}
}
