package epic

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// epicStackID is the deterministic stack id for an epic. The publisher
// (Stage 4) and `loom stack` use the same "<kind>:<value>" convention, so a
// re-run, a manual `loom stack publish`, and the post-drain reconcile all
// converge on one stack record.
func epicStackID(epicID string) sourcecontrol.StackID {
	return "epic:" + strings.TrimSpace(epicID)
}

// epicTask is the minimal projection input: a task and the in-epic tasks that
// must complete before it (its lineage predecessors).
type epicTask struct {
	ID        string
	BlockedBy []string
}

// projectedNode is one planned slot in the stack forest: a task and the single
// predecessor its branch is based on ("" = chain root, based on RootBase).
type projectedNode struct {
	TaskID     string
	BaseTaskID string
}

// projectionStats records how the DAG was linearized, so callers can surface
// (log) where a chain was deliberately broken rather than silently dropping
// structure.
type projectionStats struct {
	Tasks        int
	Roots        int
	LinearLinks  int
	FanInBreaks  int // task had >1 in-epic predecessor → started a new chain
	FanOutBreaks int // predecessor had >1 successor → successors started new chains
}

// planEpicForest linearizes an epic's `blocks` DAG into a forest of linear
// chains — the only shape stacklineage permits (a base may have at most one
// successor; a node has exactly one base). It is pure and deterministic
// (output ordered by task id).
//
// Linking rule: task T bases on predecessor P iff T has exactly one in-epic
// predecessor P AND P has exactly one in-epic successor (T). Every other task
// becomes a chain root (BaseTaskID ""), rooted on the stack's RootBase. This
// guarantees no node gets two bases (fan-in) and no base gets two children
// (fan-out), so the result always validates as linear chains.
//
//nolint:funlen // The projection is deliberately kept as one pure pass for deterministic tests.
func planEpicForest(tasks []epicTask) ([]projectedNode, projectionStats) {
	inEpic := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		if id := strings.TrimSpace(t.ID); id != "" {
			inEpic[id] = struct{}{}
		}
	}

	// In-epic predecessors per task (deduped), and successor counts per task.
	preds := make(map[string][]string, len(tasks))
	succCount := make(map[string]int, len(tasks))
	for _, t := range tasks {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			continue
		}
		seen := map[string]struct{}{}
		for _, b := range t.BlockedBy {
			b = strings.TrimSpace(b)
			if b == "" || b == id {
				continue
			}
			if _, ok := inEpic[b]; !ok {
				continue // dependency outside this epic — not part of the forest
			}
			if _, dup := seen[b]; dup {
				continue
			}
			seen[b] = struct{}{}
			preds[id] = append(preds[id], b)
			succCount[b]++
		}
	}

	ids := make([]string, 0, len(inEpic))
	for id := range inEpic {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]projectedNode, 0, len(ids))
	stats := projectionStats{Tasks: len(ids)}
	for _, id := range ids {
		p := preds[id]
		switch {
		case len(p) == 1 && succCount[p[0]] == 1:
			out = append(out, projectedNode{TaskID: id, BaseTaskID: p[0]})
			stats.LinearLinks++
		case len(p) == 0:
			out = append(out, projectedNode{TaskID: id})
			stats.Roots++
		default:
			// >1 predecessor (fan-in), or predecessor shared with a sibling
			// (fan-out): cannot stay linear, so root this task on RootBase.
			out = append(out, projectedNode{TaskID: id})
			stats.Roots++
			if len(p) > 1 {
				stats.FanInBreaks++
			} else {
				stats.FanOutBreaks++
			}
		}
	}
	return out, stats
}

// EpicStackProjection is the result of projecting an epic into the stackstore.
type EpicStackProjection struct {
	StackID    sourcecontrol.StackID
	RepoName   string
	RepoURL    string // origin remote of the repo the stack is scoped to (for the reconcile checkout)
	RootBase   string
	Stats      projectionStats
	Created    []string // task ids newly added as nodes
	Reparented []string // existing nodes whose base changed
	Lineage    map[string]driverpkg.TaskLineage
}

// projectEpicStack reads an epic's child-task DAG from Work Items and
// asks Source Control to reconcile it as a forest of linear chains. It is
// idempotent: re-running keeps every existing node's stable OutputBranch
// (never re-AddNode), only repointing a base via SetBase when lineage changed.
// New tasks are added in dependency order so each node's base already exists.
//
// repoName must match the workspace repo the worktree resolver selects (it
// scopes lineage lookups per repo); rootBase is the branch chain roots build on.
//
//nolint:cyclop,funlen,gocognit // Projection combines backend snapshot normalization with stackstore upsert ordering.
func projectEpicStack(ctx context.Context, items driverpkg.EpicWorkItems, stacks sourcecontrol.StackLifecycle, ws, epicID, repoName, rootBase string) (*EpicStackProjection, error) {
	ws = strings.TrimSpace(ws)
	epicID = strings.TrimSpace(epicID)
	repoName = strings.TrimSpace(repoName)
	rootBase = strings.TrimSpace(rootBase)
	if items == nil || stacks == nil {
		return nil, fmt.Errorf("work items projection and source control stack lifecycle required")
	}
	if ws == "" || epicID == "" || repoName == "" || rootBase == "" {
		return nil, fmt.Errorf("workspace, epic id, repo name, and root base are required")
	}

	snapshot, err := driverpkg.LoadEpicSnapshot(ctx, items, driverpkg.EpicSnapshotOptions{EpicID: epicID})
	if err != nil {
		return nil, fmt.Errorf("load epic snapshot: %w", err)
	}

	// OpenChildren is the full open-task universe; Blocked carries the
	// authoritative dependency edges. Overlay edges from every list keyed by id
	// so a task's predecessors are captured regardless of which list it lands in.
	universe := map[string]*epicTask{}
	for _, s := range snapshot.OpenChildren {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		if _, ok := universe[id]; !ok {
			universe[id] = &epicTask{ID: id}
		}
	}
	for _, list := range [][]driverpkg.EpicTaskSummary{snapshot.Blocked, snapshot.OpenChildren, snapshot.Ready} {
		for _, s := range list {
			id := strings.TrimSpace(s.ID)
			t, ok := universe[id]
			if !ok {
				continue
			}
			if len(t.BlockedBy) == 0 && len(s.BlockedBy) > 0 {
				t.BlockedBy = append([]string(nil), s.BlockedBy...)
			}
		}
	}

	tasks := make([]epicTask, 0, len(universe))
	for _, t := range universe {
		tasks = append(tasks, *t)
	}
	plan, stats := planEpicForest(tasks)

	stackID := epicStackID(epicID)
	desired := make([]sourcecontrol.DesiredStackNode, len(plan))
	for index, node := range plan {
		desired[index] = sourcecontrol.DesiredStackNode{TaskID: node.TaskID, BaseTaskID: node.BaseTaskID}
	}
	result, err := stacks.ReconcileStack(ctx, sourcecontrol.ReconcileStackCommand{
		Stack: sourcecontrol.EnsureStackCommand{
			WorkspaceKey: ws, StackID: stackID, Repository: repoName, RootBase: rootBase,
		},
		Nodes: desired,
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile stack %s: %w", stackID, err)
	}
	lineage := make(map[string]driverpkg.TaskLineage, len(result.Lineage))
	for taskID, value := range result.Lineage {
		lineage[taskID] = driverpkg.TaskLineage{
			StackID: value.StackID, BaseRef: value.BaseRef, OutputBranch: value.OutputBranch,
		}
	}
	return &EpicStackProjection{
		StackID: stackID, RepoName: repoName, RootBase: rootBase, Stats: stats,
		Created: result.Created, Reparented: result.Reparented, Lineage: lineage,
	}, nil
}

// projectEpicStackForRun is the `loom epic run` wiring for stacked mode: it
// builds the FleetDB Work Items adapter, resolves the repo + root base the stack is
// scoped to, and projects the epic DAG into the per-user stackstore — the same
// store the worktree resolver reads via DefaultStackLineageLookup, so a stacked
// task's worktree base comes from this projection.
//
// It is intentionally fail-open at the call site: the lineage path is inert
// until populated, and a missing/partial projection only means the resolver
// falls back to the repo default branch (pre-stacking behavior), never a broken
// run. Callers should surface (log) a non-nil error rather than aborting.
func projectEpicStackForRun(ctx context.Context, handle *bootstrap.StoreHandle, ws, epicID, runID, repoURL, baseBranch string) (*EpicStackProjection, error) {
	selected, rootBase, err := resolveEpicStackRepo(ctx, handle, ws, repoURL, baseBranch)
	if err != nil {
		return nil, err
	}
	repoName := selected.Name
	originURL := strings.TrimSpace(selected.RemoteURL)
	store, err := fleet.New(fleet.Config{
		BaseURL:     handle.URL(),
		WorkspaceID: ws,
		APIKey:      handle.FleetDBClientAPIKey(),
		Actor:       driverpkg.DriverRunActor(runID),
	})
	if err != nil {
		return nil, fmt.Errorf("create FleetDB Work Items adapter: %w", err)
	}
	items, err := workitems.New(store)
	if err != nil {
		return nil, fmt.Errorf("compose Work Items: %w", err)
	}
	sstore, err := stackstore.Default()
	if err != nil {
		return nil, fmt.Errorf("open stack store: %w", err)
	}
	stacks, err := sourcecontrol.NewStackLifecycle(sstore, time.Now)
	if err != nil {
		return nil, err
	}
	proj, err := projectEpicStack(ctx, items, stacks, ws, epicID, repoName, rootBase)
	if err != nil {
		return nil, err
	}
	proj.RepoURL = originURL
	return proj, nil
}

// resolveEpicStackRepo picks the single repo the epic's stack is scoped to and
// the branch its chain roots build on. The worktree resolver scopes lineage by
// repo Name, so this must return the same Name. It is deliberately strict: with
// more than one workspace repo and no --repo-url to disambiguate, it errors
// rather than guessing and scoping lineage to the wrong repo.
func resolveEpicStackRepo(ctx context.Context, handle *bootstrap.StoreHandle, ws, repoURL, baseBranch string) (selected *workspacemodule.Repository, rootBase string, err error) {
	repos, err := handle.Store.Repos().List(ctx, ws)
	if err != nil {
		return nil, "", fmt.Errorf("list workspace repos: %w", err)
	}
	if len(repos) == 0 {
		return nil, "", fmt.Errorf("workspace %q has no repos to scope a stack to", ws)
	}
	if want := strings.TrimSpace(repoURL); want != "" {
		wantTok := repoToken(want)
		for _, r := range repos {
			if r == nil {
				continue
			}
			if repoToken(r.Name) == wantTok || repoToken(r.RemoteURL) == wantTok || repoToken(repoBase(r.RemoteURL)) == repoToken(repoBase(want)) {
				selected = r
				break
			}
		}
		if selected == nil {
			return nil, "", fmt.Errorf("no workspace repo matches --repo-url %q", repoURL)
		}
	} else if len(repos) == 1 {
		selected = repos[0]
	} else {
		return nil, "", fmt.Errorf("workspace has %d repos; pass --repo-url to scope the stack", len(repos))
	}

	rootBase = strings.TrimSpace(baseBranch)
	if rootBase == "" {
		rootBase = strings.TrimSpace(selected.DefaultBranch)
	}
	if rootBase == "" {
		rootBase = "main"
	}
	return selected, rootBase, nil
}

func repoToken(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.TrimSuffix(v, ".git")
	return strings.Trim(v, "/")
}

func repoBase(v string) string {
	v = strings.TrimSuffix(strings.TrimSpace(v), ".git")
	v = strings.TrimRight(v, "/")
	if idx := strings.LastIndexAny(v, "/:"); idx >= 0 && idx+1 < len(v) {
		return v[idx+1:]
	}
	return v
}
