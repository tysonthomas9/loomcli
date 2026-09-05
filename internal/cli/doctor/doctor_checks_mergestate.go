package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/gitstate"
	"github.com/tysonthomas9/loomcli/internal/cli/integration"
)

// --- merge_in_progress check ---
//
// A worktree left mid-merge is silently blocking. On 2026-09-05 the shared
// union worktree sat with 38 unresolved conflicts for four hours: every
// integrator run after that failed its first step against it, disabling union
// merges fleet-wide, and no machine-readable surface said so. This check is
// that surface.

// defaultMergeStaleThreshold is the age past which an in-progress operation
// stops being "an integrator is merging right now" and starts being a problem.
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

// worktreeKind separates the two severities. A poisoned shared worktree blocks
// every later union merge; a poisoned agent worktree costs one agent a cycle.
type worktreeKind int

const (
	kindLocal worktreeKind = iota // repo clone or agent worktree
	kindShared
)

// candidate is one worktree to inspect.
type candidate struct {
	label string
	path  string
	kind  worktreeKind
}

// offender is a candidate found mid-operation past the age gate.
type offender struct {
	candidate
	state gitstate.State
	fixed bool
	note  string
}

// Package-level seams, mirroring getSignalDir in doctor_checks_stale.go, so the
// check can be tested without standing up a whole workspace config.
var (
	sharedWorktreeSource = integration.SharedWorktrees
	localWorktreeSource  = defaultLocalWorktrees
	inspectWorktree      = gitstate.Inspect
	snapshotRoot         = defaultSnapshotRoot
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
		out = append(out, candidate{label: name, path: path, kind: kindLocal})
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

func defaultSnapshotRoot() string {
	dir := cli.GetWorktreesDir()
	if dir == "" || dir == "." {
		return filepath.Join(os.TempDir(), "loom-rescue")
	}
	return filepath.Join(dir, "rescue")
}

// samePath reports whether two worktree paths name the same tree. Symlinks are
// resolved, and the comparison is case-insensitive as a fallback because
// EvalSymlinks does not fold case on macOS — /Users/oleh/.loom/workspaces/PUPPET
// and .../puppet resolve to two different strings for the same directory.
func samePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb || strings.EqualFold(ra, rb)
}

// collectCandidates merges shared and local worktrees, preferring the shared
// entry when a path appears in both — it carries the higher severity.
func collectCandidates() ([]candidate, error) {
	shared, err := sharedWorktreeSource()
	if err != nil {
		return nil, err
	}
	var out []candidate
	for _, sw := range shared {
		label := sw.Repo
		if sw.Branch != "" {
			label = fmt.Sprintf("%s (%s)", sw.Repo, sw.Branch)
		}
		out = append(out, candidate{label: label, path: sw.Path, kind: kindShared})
	}
	for _, local := range localWorktreeSource() {
		dup := false
		for _, existing := range out {
			if samePath(existing.path, local.path) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, local)
		}
	}
	return out, nil
}

// findOffenders inspects every candidate and keeps the ones past the age gate.
// An operation younger than the threshold is skipped entirely: a live
// integrator mid-merge is normal. An operation whose age cannot be determined
// is kept — unknown is not young.
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return out[i].kind > out[j].kind // shared first
		}
		return out[i].label < out[j].label
	})
	return out
}

func checkMergeInProgress() CheckResult {
	cands, err := collectCandidates()
	if err != nil {
		return CheckResult{
			Name:    "merge_in_progress",
			Status:  StatusWarn,
			Summary: "could not read the integration contract",
			Detail:  err.Error(),
		}
	}
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

	if doctorFix {
		fixStalledMerges(offenders)
	}
	return mergeStateResult(offenders)
}

// fixStalledMerges aborts what it is allowed to abort, after snapshotting.
//
// Only shared worktrees are ever aborted, and only from an explicit
// `loom doctor --fix`. An agent worktree is left alone: a live agent may be
// mid-run inside it and no lock covers that decision.
func fixStalledMerges(offenders []offender) {
	for i := range offenders {
		o := &offenders[i]
		if o.kind != kindShared {
			o.note = "not fixed: agent/repo worktree, a live agent may own this merge"
			continue
		}
		stamp := strings.ReplaceAll(time.Now().Format(time.RFC3339), ":", "-")
		dest := filepath.Join(snapshotRoot(), fmt.Sprintf("%s-%s-%s", filepath.Base(o.path), o.state.Op, stamp))
		if err := gitstate.Snapshot(o.path, dest); err != nil {
			// Aborting unsnapshotted throws away resolutions with no copy
			// kept, so a snapshot failure downgrades the fix to report-only.
			o.note = fmt.Sprintf("not fixed: snapshot failed (%v)", err)
			continue
		}
		if err := gitstate.Abort(o.path, o.state.Op); err != nil {
			o.note = fmt.Sprintf("not fixed: abort failed (%v); snapshot in %s", err, dest)
			continue
		}
		o.fixed = true
		o.note = fmt.Sprintf("aborted %s; snapshot in %s", o.state.Op, dest)
	}
}

func mergeStateResult(offenders []offender) CheckResult {
	var details []string
	sharedCount, fixedCount := 0, 0
	for _, o := range offenders {
		if o.kind == kindShared {
			sharedCount++
		}
		if o.fixed {
			fixedCount++
		}
		line := fmt.Sprintf("%s %s — %s", kindLabel(o.kind), o.label, o.state.String())
		if o.note != "" {
			line += "\n  " + o.note
		}
		details = append(details, line)
	}
	if !doctorFix {
		details = append(details, "Run: loom doctor --fix (aborts shared worktrees only, after a snapshot)")
	}

	status := StatusWarn
	if sharedCount > 0 && fixedCount < sharedCount {
		status = StatusFail
	}
	summary := fmt.Sprintf("%d worktree(s) stuck mid-operation (%d shared)", len(offenders), sharedCount)
	if fixedCount > 0 {
		summary = fmt.Sprintf("%s, %d aborted", summary, fixedCount)
	}
	return CheckResult{
		Name:    "merge_in_progress",
		Status:  status,
		Summary: summary,
		Detail:  strings.Join(details, "\n"),
	}
}

func kindLabel(k worktreeKind) string {
	if k == kindShared {
		return "[shared]"
	}
	return "[local]"
}
