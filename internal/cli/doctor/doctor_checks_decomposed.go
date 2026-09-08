// The decomposed-parent doctor checks: "a split lost its child links"
// (decomposed_without_children) and "a split finished but its parent is still
// parked" (decomposed_children_all_closed), plus the one board scan they share.

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// maxDecomposedScan caps how many `decomposed` issues checkDecomposedWithout-
// Children will fan out child queries for. One List per issue is an N+1 against
// fleet-db; past this many the check reports the truncation instead of issuing
// them.
const maxDecomposedScan = 50

const decomposedCheckName = "decomposed_without_children"

// decomposedLabel returns the workspace's own name for the "this issue was
// split into children" label, or "" when the workspace declares none.
//
// It is read from `defaults.labels.decomposed` in the workspace's
// integration.yaml rather than hardcoded, because nothing in loomcli or
// fleet-db ever WRITES that label — a workspace prompt does. The word is
// therefore workspace vocabulary, not core vocabulary like cli.OperatorLabel,
// and a literal here made the check pass forever for any workspace that spells
// the concept differently: a health check that is silently green is worse than
// no check at all.
//
// A var so tests can supply a label without a workspace on disk, the same seam
// unionWorkspacePath uses.
var decomposedLabel = func() string {
	wsPath := unionWorkspacePath()
	if wsPath == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(wsPath, "integration.yaml")) //nolint:gosec // G304 — path is derived from the resolved workspace
	if err != nil {
		return ""
	}
	// Only the one key is decoded: integration.yaml is large and
	// operator-owned, so a stricter view would turn every unrelated addition
	// into a doctor failure.
	var parsed struct {
		Defaults struct {
			Labels struct {
				Decomposed string `yaml:"decomposed"`
			} `yaml:"labels"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Defaults.Labels.Decomposed)
}

// decomposedUnconfiguredResult is what the check reports when the workspace
// names no decomposed label. It warns rather than passing: the check did not
// run, and the one failure mode this whole change exists to remove is a check
// that reports health it never measured.
func decomposedUnconfiguredResult() CheckResult {
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: "decomposed_without_children skipped: no decomposed label configured",
		Detail: "nothing in loom writes the \"this issue was split\" label — a workspace prompt does — " +
			"so this check cannot guess the word.\n" +
			"remediation: set `defaults.labels.decomposed` in <workspace>/integration.yaml to the " +
			"label your decomposer applies, or drop this check from the run list.",
	}
}

// checkDecomposedWithoutChildren warns when a non-terminal issue carrying the
// workspace's decomposed label (see decomposedLabel) has no children at all. A decomposer that creates children
// without `--parent` leaves such a parent behind: the children run and close
// normally while the parent sits in decomposed + blocked forever, because the
// integrator only un-parks a parent "when every child is closed" and a parent
// with no children never reaches that moment. It looks identical to a parent
// legitimately waiting on work, so nothing else surfaces it.
//
// Report-only. Returns an empty CheckResult (skipped) when no IssueBackend is
// configured, when listing fails, or when no decomposed issues exist, and a
// visible skipped-warning when the workspace configures no label.
func checkDecomposedWithoutChildren(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}

	label := decomposedLabel()
	if label == "" {
		return decomposedUnconfiguredResult()
	}
	return decomposedCheck(deps, label)
}

// decomposedCheck is the check proper, with the label already resolved. It is
// split out so tests can exercise the scan without standing up a workspace.
func decomposedCheck(deps *cli.Deps, label string) CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	issues, err := deps.IssueBackend.List(ctx, backend.ListOpts{Labels: []string{label}})
	if err != nil || len(issues) == 0 {
		return CheckResult{}
	}

	live := liveDecomposedIssues(issues)
	if len(live) > maxDecomposedScan {
		return decomposedTruncatedResult(len(live))
	}

	offenders, ok := decomposedOffenders(ctx, deps.IssueBackend, live)
	if !ok {
		return CheckResult{}
	}
	if len(offenders) == 0 {
		return CheckResult{
			Name:    decomposedCheckName,
			Status:  StatusPass,
			Summary: fmt.Sprintf("no decomposed issues without children (%d checked)", len(live)),
		}
	}

	sort.Strings(offenders)
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d decomposed issue(s) have no children", len(offenders)),
		Detail: strings.Join(offenders, "\n") +
			"\nremediation: the split lost its parent links \u2014 re-link each child with " +
			"`loom data update <child> --parent <parent>`, then let the integrator un-park the parent.",
	}
}

// liveDecomposedIssues drops the terminal statuses: a closed or tombstoned
// parent is nobody's problem, however it was split.
func liveDecomposedIssues(issues []backend.IssueData) []backend.IssueData {
	live := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if issue.Status == string(types.StatusClosed) || issue.Status == string(types.StatusTombstone) {
			continue
		}
		live = append(live, issue)
	}
	return live
}

// decomposedOffenders returns one line per parent whose child query comes back
// empty. The bool is false when a child query failed, which the caller reports
// as skipped rather than as a half-scanned board.
func decomposedOffenders(ctx context.Context, be backend.IssueBackend, live []backend.IssueData) ([]string, bool) {
	var offenders []string
	for _, issue := range live {
		kids, err := be.List(ctx, backend.ListOpts{ParentID: issue.ID, Limit: 1})
		if err != nil {
			return nil, false
		}
		if len(kids) == 0 {
			offenders = append(offenders, fmt.Sprintf("issue=%s status=%s children=0", issue.ID, issue.Status))
		}
	}
	return offenders, true
}

func decomposedTruncatedResult(n int) CheckResult {
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("too many decomposed issues to check (%d open, cap %d)", n, maxDecomposedScan),
		Detail: fmt.Sprintf("truncated: child lookup skipped to avoid %d queries against fleet-db.\n", n) +
			"remediation: narrow the board (close finished decomposed parents) and re-run `loom doctor`.",
	}
}
