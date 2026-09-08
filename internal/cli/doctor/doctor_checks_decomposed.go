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
	"sync"
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

// maxChildScan is the per-parent child page the shared scan asks for. fleet-db
// clamps any list to 200 rows (internal/api/issues.go maxLimit), so a parent
// that returns exactly this many has an unknown tail: asserting "every child is
// closed" over a clamped page is precisely the false positive that would make
// the watcher comment on a healthy parent. Such a parent is excluded from both
// verdicts and reported as truncated instead.
const maxChildScan = 200

const decomposedCheckName = "decomposed_without_children"

const decomposedChildrenCheckName = "decomposed_children_all_closed"

// defaultUnionMarkerLabel is the ledger marker assumed when the workspace names
// none. It only ever adds context to a finding (which finished children are
// still unshipped), so guessing it wrong loses detail, never a verdict.
const defaultUnionMarkerLabel = "union-pending"

// contractLabels is the slice of <workspace>/integration.yaml's
// defaults.labels block these checks read. Only these keys are decoded:
// integration.yaml is large and operator-owned, so a stricter view would turn
// every unrelated addition into a doctor failure.
type contractLabels struct {
	Decomposed string
	Marker     string
}

// readContractLabels loads defaults.labels from the active workspace's
// integration.yaml. Every failure — no workspace, unreadable file, unparseable
// YAML — yields the zero value, and each caller decides what that means.
func readContractLabels() contractLabels {
	wsPath := unionWorkspacePath()
	if wsPath == "" {
		return contractLabels{}
	}
	data, err := os.ReadFile(filepath.Join(wsPath, "integration.yaml")) //nolint:gosec // G304 — path is derived from the resolved workspace
	if err != nil {
		return contractLabels{}
	}
	var parsed struct {
		Defaults struct {
			Labels struct {
				Decomposed string `yaml:"decomposed"`
				Marker     string `yaml:"marker"`
			} `yaml:"labels"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return contractLabels{}
	}
	return contractLabels{
		Decomposed: strings.TrimSpace(parsed.Defaults.Labels.Decomposed),
		Marker:     strings.TrimSpace(parsed.Defaults.Labels.Marker),
	}
}

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
	return readContractLabels().Decomposed
}

// unionMarkerLabel returns the workspace's union-pending ledger marker. Unlike
// decomposedLabel this one falls back to a literal: an absent key must not
// suppress a stranding finding, it may only cost that finding its
// "and the children are still unshipped" clause.
var unionMarkerLabel = func() string {
	if m := readContractLabels().Marker; m != "" {
		return m
	}
	return defaultUnionMarkerLabel
}

// decomposedUnconfiguredResult is what a check reports when the workspace names
// no decomposed label. It warns rather than passing: the check did not run, and
// the one failure mode this whole change exists to remove is a check that
// reports health it never measured.
func decomposedUnconfiguredResult(name string) CheckResult {
	return CheckResult{
		Name:    name,
		Status:  StatusWarn,
		Summary: name + " skipped: no decomposed label configured",
		Detail: "nothing in loom writes the \"this issue was split\" label — a workspace prompt does — " +
			"so this check cannot guess the word.\n" +
			"remediation: set `defaults.labels.decomposed` in <workspace>/integration.yaml to the " +
			"label your decomposer applies, or drop this check from the run list.",
	}
}

func decomposedTruncatedResult(name string, n int) CheckResult {
	return CheckResult{
		Name:    name,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("too many decomposed issues to check (%d open, cap %d)", n, maxDecomposedScan),
		Detail: fmt.Sprintf("truncated: child lookup skipped to avoid %d queries against fleet-db.\n", n) +
			"remediation: narrow the board (close finished decomposed parents) and re-run `loom doctor`.",
	}
}

// scanResult is one reading of the decomposed board: the live parents and,
// unless the scan truncated, every parent's resolved children.
//
// ok is false for every "we could not read the board" case (no backend, a
// failed list, a failed child query, a cancelled context). Both checks report
// that as skipped rather than as a verdict over a half-scanned board.
type scanResult struct {
	ok           bool
	unconfigured bool
	truncated    bool
	live         []backend.IssueData
	kids         map[string][]backend.IssueData
}

// decomposedScan is the one board scan decomposed_without_children and
// decomposed_children_all_closed share. They ask opposite questions — "no
// children at all" versus "children, all finished" — off the same two queries,
// so running them independently would double an N+1 against fleet-db and let
// them disagree about a board a mutation changed between the two reads.
//
// `loom doctor` is a one-shot process, so "once per run" needs no TTL and no
// package-level global: collectDoctorChecks creates one and discards it, which
// keeps t.Parallel() subtests independent.
type decomposedScan struct {
	deps *cli.Deps
	// label, when non-empty, bypasses the workspace read — the seam tests use
	// to exercise the scan without standing up an integration.yaml.
	label string
	once  sync.Once
	res   scanResult
}

func newDecomposedScan(deps *cli.Deps) *decomposedScan {
	return &decomposedScan{deps: deps}
}

func (s *decomposedScan) result() scanResult {
	s.once.Do(func() { s.res = s.run() })
	return s.res
}

func (s *decomposedScan) run() scanResult {
	if s.deps == nil || s.deps.IssueBackend == nil {
		return scanResult{}
	}
	label := s.label
	if label == "" {
		label = decomposedLabel()
	}
	if label == "" {
		return scanResult{unconfigured: true}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	issues, err := s.deps.IssueBackend.List(ctx, backend.ListOpts{Labels: []string{label}})
	if err != nil || len(issues) == 0 {
		return scanResult{}
	}

	live := liveDecomposedIssues(issues)
	if len(live) > maxDecomposedScan {
		return scanResult{ok: true, truncated: true, live: live}
	}

	kids := make(map[string][]backend.IssueData, len(live))
	for _, issue := range live {
		// Limit is maxChildScan, not 1: decomposed_children_all_closed needs
		// each child's status and labels, and the childless question is still
		// answered exactly by len(kids) == 0.
		got, err := s.deps.IssueBackend.List(ctx, backend.ListOpts{ParentID: issue.ID, Limit: maxChildScan})
		if err != nil {
			return scanResult{}
		}
		kids[issue.ID] = got
	}
	return scanResult{ok: true, live: live, kids: kids}
}

// liveDecomposedIssues drops the terminal statuses: a closed or tombstoned
// parent is nobody's problem, however it was split.
func liveDecomposedIssues(issues []backend.IssueData) []backend.IssueData {
	live := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if isTerminalStatus(issue.Status) {
			continue
		}
		live = append(live, issue)
	}
	return live
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
func checkDecomposedWithoutChildren(scan *decomposedScan) CheckResult {
	res := scan.result()
	if res.unconfigured {
		return decomposedUnconfiguredResult(decomposedCheckName)
	}
	if !res.ok {
		return CheckResult{}
	}
	if res.truncated {
		return decomposedTruncatedResult(decomposedCheckName, len(res.live))
	}

	offenders := decomposedOffenders(res)
	if len(offenders) == 0 {
		return CheckResult{
			Name:    decomposedCheckName,
			Status:  StatusPass,
			Summary: fmt.Sprintf("no decomposed issues without children (%d checked)", len(res.live)),
		}
	}

	sort.Strings(offenders)
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d decomposed issue(s) have no children", len(offenders)),
		Detail: strings.Join(offenders, "\n") +
			"\nremediation: the split lost its parent links — re-link each child with " +
			"`loom data update <child> --parent <parent>`, then let the integrator un-park the parent.",
	}
}

// decomposedOffenders returns one line per parent whose child query came back
// empty.
func decomposedOffenders(res scanResult) []string {
	var offenders []string
	for _, issue := range res.live {
		if len(res.kids[issue.ID]) == 0 {
			offenders = append(offenders, fmt.Sprintf("issue=%s status=%s children=0", issue.ID, issue.Status))
		}
	}
	return offenders
}

// strandedParent is one row of the decomposed_children_all_closed payload. The
// sibling PM2 watcher parses this shape; changing a field name is a contract
// change, not a refactor.
type strandedParent struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Children       int    `json:"children"`
	ClosedChildren int    `json:"closed_children"`
	// LastChildClosedAt is omitted when no child carried a closed_at. A
	// timestamp is never synthesized, and a missing one never suppresses the
	// finding.
	LastChildClosedAt string   `json:"last_child_closed_at,omitempty"`
	UnshippedChildren []string `json:"unshipped_children,omitempty"`
	// NotesPresent is always emitted: false must mean "the parent has no
	// blocker note", never "nobody looked".
	NotesPresent bool `json:"notes_present"`
}

// decomposedStrandReport is CheckResult.Data for decomposed_children_all_closed.
//
// Scanned and ChildlessParents are always present for the same reason
// starvationReport.UnreachableComputed exists: a zero that means "measured
// zero" must be distinguishable from an absent key meaning "never asked".
type decomposedStrandReport struct {
	Scanned          int              `json:"scanned"`
	Stranded         []strandedParent `json:"stranded,omitempty"`
	Completable      []strandedParent `json:"completable,omitempty"`
	TruncatedParents []string         `json:"truncated_parents,omitempty"`
	ChildlessParents int              `json:"childless_parents"`
}

// decomposedClass is what one parent's child set makes of it.
type decomposedClass int

const (
	// classPending — at least one child is still live, so the park is honest.
	classPending decomposedClass = iota
	// classStranded — every child is finished and the parent is parked: nobody
	// can claim it and nothing will un-park it.
	classStranded
	// classCompletable — every child is finished and the parent is live. This
	// is the normal end of a split; it is counted, never warned on.
	classCompletable
	// classChildless — decomposed_without_children owns this one.
	classChildless
)

func isTerminalStatus(status string) bool {
	return status == string(types.StatusClosed) || status == string(types.StatusTombstone)
}

// isParkedStatus is the set of statuses no agent will claim. `deferred` is in
// it deliberately: fleet-db's own un-park derivation only handles `blocked`, so
// a deferred parent is exactly the case this backstop exists for.
func isParkedStatus(status string) bool {
	return status == string(types.StatusBlocked) || status == string(types.StatusDeferred)
}

// classifyDecomposedParent decides one parent's verdict from its children, and
// builds the payload row when there is one to build.
func classifyDecomposedParent(parent backend.IssueData, kids []backend.IssueData, marker string) (strandedParent, decomposedClass) {
	if len(kids) == 0 {
		return strandedParent{}, classChildless
	}

	var unshipped []string
	var last *time.Time
	for i := range kids {
		kid := kids[i]
		if !isTerminalStatus(kid.Status) {
			return strandedParent{}, classPending
		}
		if marker != "" && hasLabel(kid.Labels, marker) {
			unshipped = append(unshipped, kid.ID)
		}
		if kid.ClosedAt != nil && (last == nil || kid.ClosedAt.After(*last)) {
			last = kid.ClosedAt
		}
	}
	sort.Strings(unshipped)

	entry := strandedParent{
		ID:                parent.ID,
		Status:            parent.Status,
		Children:          len(kids),
		ClosedChildren:    len(kids),
		UnshippedChildren: unshipped,
		NotesPresent:      strings.TrimSpace(parent.Notes) != "",
	}
	if last != nil {
		entry.LastChildClosedAt = last.UTC().Format(time.RFC3339)
	}
	if isParkedStatus(parent.Status) {
		return entry, classStranded
	}
	return entry, classCompletable
}

// checkDecomposedChildrenAllClosed warns when every child of a decomposed
// parent has finished and the parent is still parked. Nobody can claim such a
// parent and nothing will ever un-park it: fleet-db derives the un-park from a
// child CLOSING, so a parent parked after its last child closed — or parked at
// `deferred`, or closed through a path that bypasses CloseIssue — has no future
// trigger left. This is the backstop for the states that derivation cannot
// structurally reach.
//
// A parent in a live status with every child finished is the NORMAL end of a
// split: it is carried in Data.completable and never warned on, because a
// backstop that cries wolf on healthy boards gets muted, and a muted backstop
// is not a backstop.
//
// Report-only, and skipped (empty CheckResult) whenever the board could not be
// read — see scanResult.ok.
func checkDecomposedChildrenAllClosed(scan *decomposedScan) CheckResult {
	res := scan.result()
	if res.unconfigured {
		return decomposedUnconfiguredResult(decomposedChildrenCheckName)
	}
	if !res.ok {
		return CheckResult{}
	}
	if res.truncated {
		return decomposedTruncatedResult(decomposedChildrenCheckName, len(res.live))
	}

	report := buildStrandReport(res, unionMarkerLabel())
	if len(report.Stranded) == 0 {
		return strandPassResult(report)
	}
	return strandWarnResult(report)
}

func buildStrandReport(res scanResult, marker string) decomposedStrandReport {
	report := decomposedStrandReport{Scanned: len(res.live)}
	for _, parent := range res.live {
		kids := res.kids[parent.ID]
		if len(kids) >= maxChildScan {
			report.TruncatedParents = append(report.TruncatedParents, parent.ID)
			continue
		}
		entry, class := classifyDecomposedParent(parent, kids, marker)
		switch class {
		case classStranded:
			report.Stranded = append(report.Stranded, entry)
		case classCompletable:
			report.Completable = append(report.Completable, entry)
		case classChildless:
			report.ChildlessParents++
		case classPending:
		}
	}
	sortStrandedParents(report.Stranded)
	sortStrandedParents(report.Completable)
	sort.Strings(report.TruncatedParents)
	return report
}

func sortStrandedParents(rows []strandedParent) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
}

func strandPassResult(report decomposedStrandReport) CheckResult {
	result := CheckResult{
		Name:   decomposedChildrenCheckName,
		Status: StatusPass,
		Summary: fmt.Sprintf("no decomposed parent is parked with every child finished (%d checked, %d completable)",
			report.Scanned, len(report.Completable)),
	}
	// Nothing to report means no payload at all: an ops consumer should not
	// have to distinguish an empty report from a green one.
	if len(report.Completable) > 0 || len(report.TruncatedParents) > 0 {
		result.Data = report
		result.Detail = strandDetail(report, nil)
	}
	return result
}

func strandWarnResult(report decomposedStrandReport) CheckResult {
	lines := make([]string, 0, len(report.Stranded))
	for _, p := range report.Stranded {
		line := fmt.Sprintf("issue=%s status=%s children=%d closed=%d", p.ID, p.Status, p.Children, p.ClosedChildren)
		if len(p.UnshippedChildren) > 0 {
			line += " unshipped=" + strings.Join(p.UnshippedChildren, ",")
		}
		lines = append(lines, line)
	}
	remediation := []string{
		"remediation: every child of this parent is finished — the parent is completable; " +
			"un-park it with `loom data update <parent> --status open`, or close it if the split is done.",
	}
	if n := unshippedChildCount(report.Stranded); n > 0 {
		remediation = append(remediation, fmt.Sprintf(
			"%d child(ren) are closed but still carry the union marker — do not close this parent "+
				"until the sweep drains them.", n))
	}
	return CheckResult{
		Name:    decomposedChildrenCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d decomposed parent(s) parked with every child finished", len(report.Stranded)),
		Detail:  strandDetail(report, append(lines, remediation...)),
		Data:    report,
	}
}

func unshippedChildCount(rows []strandedParent) int {
	n := 0
	for _, p := range rows {
		n += len(p.UnshippedChildren)
	}
	return n
}

// strandDetail renders the shared tail of both verdicts: the completable and
// truncated parents, which are information in either direction.
func strandDetail(report decomposedStrandReport, lines []string) string {
	if len(report.Completable) > 0 {
		ids := make([]string, 0, len(report.Completable))
		for _, p := range report.Completable {
			ids = append(ids, p.ID)
		}
		lines = append(lines, fmt.Sprintf("completable (every child finished, parent still live, no action needed): %s",
			strings.Join(ids, ", ")))
	}
	if len(report.TruncatedParents) > 0 {
		lines = append(lines, fmt.Sprintf(
			"not checked — child list hit the %d-row cap, so \"all finished\" could not be established: %s",
			maxChildScan, strings.Join(report.TruncatedParents, ", ")))
	}
	return strings.Join(lines, "\n")
}
