package stackstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// Migration sentinel errors.
var (
	// ErrMigrateConflict means at least one local stack disagrees with the
	// destination. Nothing was written; the report lists every conflict.
	ErrMigrateConflict = errors.New("stackstore: migration refused: local stacks conflict with the destination store")
	// ErrMigrateSameStore means source and destination are the same stacks.json.
	ErrMigrateSameStore = errors.New("stackstore: migration source and destination are the same local stacks.json")
)

// MigrateAction is what Migrate does (or would do) with one local stack.
type MigrateAction string

const (
	MigrateCreate    MigrateAction = "create"    // stack absent in destination; import it whole
	MigrateExtend    MigrateAction = "extend"    // stack present and consistent; add missing nodes
	MigrateUnchanged MigrateAction = "unchanged" // already imported; nothing to do
	MigrateConflict  MigrateAction = "conflict"  // refused; see Conflicts
)

// MigrateOptions scopes a migration run.
type MigrateOptions struct {
	// Workspace is the workspace key whose local stacks are imported.
	Workspace string
	// DryRun plans the migration without writing a backup or the destination.
	DryRun bool
	// Now overrides the clock used for the backup file name (tests).
	Now func() time.Time
}

// MigrateStackPlan is the per-stack outcome.
type MigrateStackPlan struct {
	StackID   sl.StackID    `json:"stackId"`
	RepoName  string        `json:"repoName"`
	Action    MigrateAction `json:"action"`
	AddNodes  []string      `json:"addNodes,omitempty"`  // task ids to import, in lineage order
	Conflicts []string      `json:"conflicts,omitempty"` // human-readable reasons for MigrateConflict
}

// MigrateReport is the operator-visible result of Migrate.
type MigrateReport struct {
	Source          string             `json:"source"`
	SourceMissing   bool               `json:"sourceMissing,omitempty"`
	Workspace       string             `json:"workspace"`
	DryRun          bool               `json:"dryRun"`
	Stacks          []MigrateStackPlan `json:"stacks"`
	OtherWorkspaces []string           `json:"otherWorkspaces,omitempty"` // present locally, not migrated by this run
	Backup          string             `json:"backup,omitempty"`
	Applied         bool               `json:"applied"`
}

// Conflicted reports whether any stack was refused.
func (r *MigrateReport) Conflicted() bool {
	for _, p := range r.Stacks {
		if p.Action == MigrateConflict {
			return true
		}
	}
	return false
}

// Path returns the stacks.json path this store reads and writes.
func (s *LocalStore) Path() string { return s.path() }

type migrateItem struct {
	plan   MigrateStackPlan
	header sl.Stack
	nodes  map[string]sl.Node // local nodes by task id
}

// Migrate imports the local stacks.json lineage for opts.Workspace into dst.
//
// It plans every stack before writing anything and refuses the whole run
// (ErrMigrateConflict) if any stack disagrees with dst: a differing stack header,
// a node whose lineage or publish state differs, a task already registered in a
// different destination stack of the same repo, a merged lineage that is not
// linear, or an output branch dst would assign differently. Stacks that already
// match are skipped, so re-running after success — or after a partial failure —
// is safe. The local file is never modified; before the first write Migrate
// copies it to a timestamped backup beside it.
func Migrate(ctx context.Context, src *LocalStore, dst Store, opts MigrateOptions) (*MigrateReport, error) {
	if strings.TrimSpace(opts.Workspace) == "" {
		return nil, errors.New("stackstore: migration workspace is required")
	}
	if src == nil || src.dir == "" {
		return nil, ErrLoomDirMissing
	}
	report := &MigrateReport{Source: src.path(), Workspace: opts.Workspace, DryRun: opts.DryRun, Stacks: []MigrateStackPlan{}}
	if ls, ok := dst.(*LocalStore); ok && sameFile(ls.path(), src.path()) {
		return report, ErrMigrateSameStore
	}

	raw, f, err := readSource(src, report)
	if err != nil || f == nil {
		return report, err
	}

	items, err := planMigration(ctx, f.Workspaces[opts.Workspace], dst, opts.Workspace)
	if err != nil {
		return report, err
	}
	pending := false
	for _, it := range items {
		report.Stacks = append(report.Stacks, it.plan)
		if len(it.plan.AddNodes) > 0 || it.plan.Action == MigrateCreate {
			pending = true
		}
	}
	if report.Conflicted() {
		return report, ErrMigrateConflict
	}
	if opts.DryRun || !pending {
		return report, nil
	}

	return report, backupAndApply(ctx, src, dst, raw, items, opts, report)
}

// readSource reads stacks.json and records the workspaces this run leaves
// alone. A missing file yields (nil, nil, nil) with report.SourceMissing set.
func readSource(src *LocalStore, report *MigrateReport) ([]byte, *stacksFile, error) {
	raw, err := os.ReadFile(src.path()) //nolint:gosec // path from loom dir
	if err != nil {
		if os.IsNotExist(err) {
			report.SourceMissing = true
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("stackstore: read %s: %w", src.path(), err)
	}
	f, err := src.load()
	if err != nil {
		return nil, nil, err
	}
	for name := range f.Workspaces {
		if name != report.Workspace {
			report.OtherWorkspaces = append(report.OtherWorkspaces, name)
		}
	}
	sort.Strings(report.OtherWorkspaces)
	return raw, f, nil
}

func backupAndApply(ctx context.Context, src *LocalStore, dst Store, raw []byte, items []migrateItem, opts MigrateOptions, report *MigrateReport) error {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	backup := src.path() + ".pre-migrate-" + now().UTC().Format("20060102T150405Z") + ".bak"
	if err := atomicfile.WriteFile(backup, raw, 0o600); err != nil {
		return fmt.Errorf("stackstore: back up %s: %w", src.path(), err)
	}
	report.Backup = backup
	for _, it := range items {
		if err := applyMigration(ctx, dst, opts.Workspace, it); err != nil {
			return fmt.Errorf("stackstore: migrate %s: %w (re-run is safe: already-imported nodes are skipped)", it.plan.StackID, err)
		}
	}
	report.Applied = true
	return nil
}

func planMigration(ctx context.Context, local *workspaceStacks, dst Store, ws string) ([]migrateItem, error) {
	if local == nil || len(local.Stacks) == 0 {
		return nil, nil
	}
	dstStacks, err := dst.ListStacks(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("stackstore: list destination stacks: %w", err)
	}
	// owner maps repo+task to the destination stack that already holds it.
	owner := map[string]sl.StackID{}
	dstNodes := map[sl.StackID][]sl.Node{}
	dstHeaders := map[sl.StackID]sl.Stack{}
	for _, s := range dstStacks {
		nodes, err := dst.ListNodes(ctx, ws, s.ID)
		if err != nil {
			return nil, fmt.Errorf("stackstore: list destination nodes of %s: %w", s.ID, err)
		}
		dstNodes[s.ID] = nodes
		dstHeaders[s.ID] = s
		for _, n := range nodes {
			owner[s.RepoName+"\x00"+n.TaskID] = s.ID
		}
	}

	ids := make([]string, 0, len(local.Stacks))
	for id := range local.Stacks {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	items := make([]migrateItem, 0, len(ids))
	for _, id := range ids {
		st := local.Stacks[id]
		it := migrateItem{
			header: st.Stack,
			nodes:  map[string]sl.Node{},
			plan:   MigrateStackPlan{StackID: st.Stack.ID, RepoName: st.Stack.RepoName},
		}
		for tid, n := range st.Nodes {
			it.nodes[tid] = *n
		}
		it.plan.Action, it.plan.AddNodes, it.plan.Conflicts = planStack(it, dstHeaders, dstNodes, owner)
		// Claim planned tasks so a sibling local stack cannot import them too.
		for _, tid := range it.plan.AddNodes {
			owner[it.header.RepoName+"\x00"+tid] = it.header.ID
		}
		items = append(items, it)
	}
	return items, nil
}

func (it migrateItem) localNodes() []sl.Node {
	out := make([]sl.Node, 0, len(it.nodes))
	for _, n := range it.nodes {
		out = append(out, n)
	}
	return out
}

func planStack(it migrateItem, dstHeaders map[sl.StackID]sl.Stack, dstNodes map[sl.StackID][]sl.Node, owner map[string]sl.StackID) (MigrateAction, []string, []string) {
	id := it.header.ID
	var conflicts []string
	ordered, err := sl.Ordered(it.localNodes())
	if err != nil {
		return MigrateConflict, nil, []string{fmt.Sprintf("local lineage is invalid: %v", err)}
	}

	header, exists := dstHeaders[id]
	existing := map[string]sl.Node{}
	if exists {
		conflicts = headerDiffs(it.header, header)
		for _, n := range dstNodes[id] {
			existing[n.TaskID] = n
		}
	}

	taken := make(map[string]struct{}, len(existing))
	merged := make([]sl.Node, 0, len(existing)+len(ordered))
	for _, n := range existing {
		taken[n.OutputBranch] = struct{}{}
		merged = append(merged, n)
	}
	var add []string
	for _, n := range ordered {
		if cur, ok := existing[n.TaskID]; ok {
			if diffs := nodeDiffs(n, cur); len(diffs) > 0 {
				conflicts = append(conflicts, fmt.Sprintf("node %s differs from destination: %s", n.TaskID, strings.Join(diffs, "; ")))
			}
			continue
		}
		if other, ok := owner[it.header.RepoName+"\x00"+n.TaskID]; ok && other != id {
			conflicts = append(conflicts, fmt.Sprintf("task %s is already in destination stack %s", n.TaskID, other))
			continue
		}
		if got := sl.AssignBranch(id, n.TaskID, taken); got != n.OutputBranch {
			conflicts = append(conflicts, fmt.Sprintf("node %s output branch %q would be reassigned as %q", n.TaskID, n.OutputBranch, got))
		}
		taken[n.OutputBranch] = struct{}{}
		merged = append(merged, n)
		add = append(add, n.TaskID)
	}
	if len(conflicts) == 0 && exists && len(add) > 0 {
		if _, err := sl.Ordered(merged); err != nil {
			conflicts = append(conflicts, fmt.Sprintf("merged lineage with destination is invalid: %v", err))
		}
	}
	return classify(exists, add, conflicts)
}

func classify(exists bool, add, conflicts []string) (MigrateAction, []string, []string) {
	switch {
	case len(conflicts) > 0:
		return MigrateConflict, nil, conflicts
	case !exists:
		return MigrateCreate, add, nil
	case len(add) > 0:
		return MigrateExtend, add, nil
	default:
		return MigrateUnchanged, nil, nil
	}
}

func headerDiffs(local, dst sl.Stack) []string {
	var d []string
	if dst.RepoName != local.RepoName {
		d = append(d, fmt.Sprintf("repo differs: local %q, destination %q", local.RepoName, dst.RepoName))
	}
	if dst.RootBase != local.RootBase {
		d = append(d, fmt.Sprintf("root base differs: local %q, destination %q", local.RootBase, dst.RootBase))
	}
	if a, b := local.DefaultCommitMode, dst.DefaultCommitMode; a != "" && b != "" && a != b {
		d = append(d, fmt.Sprintf("default commit mode differs: local %q, destination %q", a, b))
	}
	return d
}

// nodeDiffs lists lineage and publish-state fields that differ. Timestamps
// other than LastPublishedAt are store-assigned and ignored.
func nodeDiffs(local, dst sl.Node) []string {
	var d []string
	add := func(field string, a, b any) { d = append(d, fmt.Sprintf("%s local=%v destination=%v", field, a, b)) }
	if local.BaseTaskID != dst.BaseTaskID {
		add("baseTaskId", quoted(local.BaseTaskID), quoted(dst.BaseTaskID))
	}
	if local.OutputBranch != dst.OutputBranch {
		add("outputBranch", quoted(local.OutputBranch), quoted(dst.OutputBranch))
	}
	if local.CommitMode != "" && dst.CommitMode != "" && local.CommitMode != dst.CommitMode {
		add("commitMode", local.CommitMode, dst.CommitMode)
	}
	if local.State != dst.State {
		add("state", local.State, dst.State)
	}
	if local.PRNumber != dst.PRNumber {
		add("prNumber", local.PRNumber, dst.PRNumber)
	}
	if local.PRURL != dst.PRURL {
		add("prUrl", quoted(local.PRURL), quoted(dst.PRURL))
	}
	if local.OutputSHA != dst.OutputSHA {
		add("outputSha", quoted(local.OutputSHA), quoted(dst.OutputSHA))
	}
	if !sameInstant(local.LastPublishedAt, dst.LastPublishedAt) {
		add("lastPublishedAt", fmtTime(local.LastPublishedAt), fmtTime(dst.LastPublishedAt))
	}
	return d
}

func applyMigration(ctx context.Context, dst Store, ws string, it migrateItem) error {
	switch it.plan.Action {
	case MigrateCreate:
		if err := dst.EnsureStack(ctx, it.header); err != nil {
			return err
		}
	case MigrateExtend:
	default:
		return nil
	}
	for _, tid := range it.plan.AddNodes {
		n := it.nodes[tid]
		created, err := dst.AddNode(ctx, ws, it.header.ID, n.TaskID, n.BaseTaskID, n.CommitMode)
		if err != nil {
			return fmt.Errorf("add node %s: %w", tid, err)
		}
		if err := finishNode(ctx, dst, ws, it.header.ID, n, created); err != nil {
			// Undo the half-imported node so a re-run plans it as new, not as a conflict.
			if rmErr := dst.RemoveNode(ctx, ws, it.header.ID, tid); rmErr != nil {
				return fmt.Errorf("%w (rollback of node %s also failed: %w)", err, tid, rmErr)
			}
			return err
		}
	}
	return nil
}

// finishNode verifies the destination's branch assignment and copies the
// local publish state onto a freshly added node.
func finishNode(ctx context.Context, dst Store, ws string, id sl.StackID, n, created sl.Node) error {
	tid := n.TaskID
	if created.OutputBranch != n.OutputBranch {
		return fmt.Errorf("node %s: destination assigned output branch %q, local has %q", tid, created.OutputBranch, n.OutputBranch)
	}
	if !needsStateCopy(n, created) {
		return nil
	}
	if err := dst.UpdateNode(ctx, ws, id, tid, func(d *sl.Node) error {
		d.State = n.State
		d.PRNumber = n.PRNumber
		d.PRURL = n.PRURL
		d.OutputSHA = n.OutputSHA
		if n.LastPublishedAt != nil {
			t := *n.LastPublishedAt
			d.LastPublishedAt = &t
		}
		if n.CommitMode != "" {
			d.CommitMode = n.CommitMode
		}
		return nil
	}); err != nil {
		return fmt.Errorf("copy publish state of %s: %w", tid, err)
	}
	return nil
}

func needsStateCopy(local, created sl.Node) bool {
	return local.State != created.State || local.PRNumber != created.PRNumber ||
		local.PRURL != created.PRURL || local.OutputSHA != created.OutputSHA ||
		!sameInstant(local.LastPublishedAt, created.LastPublishedAt) ||
		(local.CommitMode != "" && local.CommitMode != created.CommitMode)
}

func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Truncate(time.Second).Equal(b.Truncate(time.Second))
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "<none>"
	}
	return t.UTC().Format(time.RFC3339)
}

func quoted(s string) string { return fmt.Sprintf("%q", s) }

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
