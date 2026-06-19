package stackpublish

import (
	"context"
	"fmt"
	"time"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

// Reconciler publishes a stack's lineage as stacked PRs.
type Reconciler struct {
	Store stackstore.Store
	Forge Forge
}

// Options tunes a publish run.
type Options struct {
	DryRun bool
}

// Report summarizes what a publish run did (or would do, for DryRun).
type Report struct {
	DryRun     bool              `json:"dryRun"`
	Created    []string          `json:"created,omitempty"`    // task IDs
	Reparented []string          `json:"reparented,omitempty"` // task IDs
	Skipped    []string          `json:"skipped,omitempty"`    // task IDs (already correct)
	Closed     []string          `json:"closed,omitempty"`     // branches (orphan PRs)
	Merged     []string          `json:"merged,omitempty"`     // task IDs (terminal)
	Empty      []string          `json:"empty,omitempty"`      // task IDs (no PR)
	PRURLs     map[string]string `json:"prUrls,omitempty"`     // task ID → PR URL
}

type actionKind string

const (
	actCreate   actionKind = "create"
	actReparent actionKind = "reparent"
	actSkip     actionKind = "skip"
	actClose    actionKind = "close"
	actMerged   actionKind = "merged"
	actEmpty    actionKind = "empty"
)

type action struct {
	Kind        actionKind
	TaskID      string
	Branch      string
	DesiredBase string
	PR          *PR
}

// computePlan is the pure core: given the desired ordered lineage, the current
// PRs (indexed by head branch), and the set of branches that add nothing over
// their base, it produces the reconcile actions. No I/O — unit-tested directly.
func computePlan(stack sl.Stack, ordered []sl.Node, prsByHead map[string]PR, empty map[string]bool) []action {
	byTask := sl.ByTask(ordered)
	desired := make(map[string]bool, len(ordered))
	for _, n := range ordered {
		desired[n.OutputBranch] = true
	}

	// effectiveBase slides past merged/empty predecessors to the first real base,
	// or RootBase. A merged unit's content is in RootBase; an empty unit adds nothing.
	var effectiveBase func(n sl.Node) string
	effectiveBase = func(n sl.Node) string {
		if n.BaseTaskID == "" {
			return stack.RootBase
		}
		pred, ok := byTask[n.BaseTaskID]
		if !ok {
			return stack.RootBase
		}
		predPR, has := prsByHead[pred.OutputBranch]
		if (has && predPR.Merged) || empty[pred.OutputBranch] {
			return effectiveBase(pred)
		}
		return pred.OutputBranch
	}

	var actions []action
	for _, n := range ordered {
		pr, has := prsByHead[n.OutputBranch]
		switch {
		case has && pr.Merged:
			actions = append(actions, action{Kind: actMerged, TaskID: n.TaskID, Branch: n.OutputBranch, PR: prPtr(pr)})
		case empty[n.OutputBranch]:
			actions = append(actions, action{Kind: actEmpty, TaskID: n.TaskID, Branch: n.OutputBranch})
		default:
			base := effectiveBase(n)
			switch {
			case !has || pr.State == "closed":
				actions = append(actions, action{Kind: actCreate, TaskID: n.TaskID, Branch: n.OutputBranch, DesiredBase: base})
			case pr.Base == base:
				actions = append(actions, action{Kind: actSkip, TaskID: n.TaskID, Branch: n.OutputBranch, DesiredBase: base, PR: prPtr(pr)})
			default:
				actions = append(actions, action{Kind: actReparent, TaskID: n.TaskID, Branch: n.OutputBranch, DesiredBase: base, PR: prPtr(pr)})
			}
		}
	}
	// Orphans: open PRs under our namespace whose head is no longer a desired unit.
	for head, pr := range prsByHead {
		if pr.State == "open" && !pr.Merged && !desired[head] {
			actions = append(actions, action{Kind: actClose, Branch: head, PR: prPtr(pr)})
		}
	}
	return actions
}

func prPtr(p PR) *PR { return &p }

// reparentPRNumbers returns the PR numbers of reparent actions (whose base would change).
func reparentPRNumbers(plan []action) []int {
	var out []int
	for _, a := range plan {
		if a.Kind == actReparent && a.PR != nil {
			out = append(out, a.PR.Number)
		}
	}
	return out
}

// queuedConflicts returns the subset of targets that are in the queued set.
func queuedConflicts(targets []int, queued map[int]bool) []int {
	var out []int
	for _, n := range targets {
		if queued[n] {
			out = append(out, n)
		}
	}
	return out
}

// Publish reconciles the stack's PRs to match its desired lineage. repoPath is a
// Loom-owned local checkout where each unit's output branch already exists at its
// desired commit; the reconciler pushes them in the safe order. Cursor-less and
// idempotent: it re-derives everything from forge truth each run.
func (r *Reconciler) Publish(ctx context.Context, ws string, id sl.StackID, repoPath string, opts Options) (*Report, error) {
	stack, err := r.Store.GetStack(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	nodes, err := r.Store.ListNodes(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	ordered, err := sl.Ordered(nodes)
	if err != nil {
		return nil, fmt.Errorf("invalid lineage: %w", err)
	}
	owner, repo, err := repoSlug(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	byTask := sl.ByTask(ordered)

	// Emptiness: a unit whose branch adds no commits over its lineage base. Only
	// checked for branches present locally; missing ones surface at push time.
	empty := map[string]bool{}
	for _, n := range ordered {
		base, berr := sl.BaseBranch(*stack, n, byTask)
		if berr != nil {
			return nil, fmt.Errorf("base for %s: %w", n.TaskID, berr)
		}
		cnt, cerr := commitsBetween(ctx, repoPath, base, n.OutputBranch)
		if cerr != nil {
			continue // branch not materialized locally; let push report it
		}
		if cnt == 0 {
			empty[n.OutputBranch] = true
		}
	}

	prefix := sl.StackBranchPrefix(id)
	prs, err := r.Forge.ListStackPRs(ctx, owner, repo, prefix)
	if err != nil {
		return nil, err
	}
	prsByHead := make(map[string]PR, len(prs))
	for _, p := range prs {
		// Prefer the open PR if duplicates exist for a head.
		if existing, ok := prsByHead[p.Head]; ok && existing.State == "open" && p.State != "open" {
			continue
		}
		prsByHead[p.Head] = p
	}

	plan := computePlan(*stack, ordered, prsByHead, empty)

	// Merge-queue pre-flight: a PR in GitHub's merge queue has an immutable base,
	// so a reparent would 422. Detect it BEFORE any mutation and fail closed with
	// an actionable message (no half-reparented stack). Skipped for dry-run, which
	// mutates nothing.
	if !opts.DryRun {
		if targets := reparentPRNumbers(plan); len(targets) > 0 {
			queued, qerr := r.Forge.QueuedPRNumbers(ctx, owner, repo)
			if qerr != nil {
				return nil, fmt.Errorf("merge-queue preflight: %w", qerr)
			}
			if blocked := queuedConflicts(targets, queued); len(blocked) > 0 {
				return nil, fmt.Errorf("cannot reorder: PR(s) %v are in the merge queue and their base is immutable; wait for them to merge or dequeue, then re-run publish", blocked)
			}
		}
	}

	report := &Report{DryRun: opts.DryRun, PRURLs: map[string]string{}}

	if opts.DryRun {
		for _, a := range plan {
			classify(report, a)
		}
		return report, nil
	}

	// Phase 1 — reparent changed-base PRs to a safe base (RootBase) before any push.
	for _, a := range plan {
		if a.Kind == actReparent && a.PR != nil && a.PR.Base != stack.RootBase {
			if err := r.Forge.UpdatePRBase(ctx, owner, repo, a.PR.Number, stack.RootBase); err != nil {
				return report, fmt.Errorf("phase1 reparent #%d: %w", a.PR.Number, err)
			}
		}
	}

	// Phase 2 — push all live (non-merged, non-empty) unit branches, atomically.
	var toPush []string
	for _, a := range plan {
		if a.Kind == actCreate || a.Kind == actReparent || a.Kind == actSkip {
			toPush = append(toPush, a.Branch)
		}
	}
	if err := r.Forge.PushBranches(ctx, repoPath, toPush); err != nil {
		return report, fmt.Errorf("phase2 push: %w", err)
	}

	// Phase 3 — set final bases for reparented PRs (branches now final).
	for _, a := range plan {
		if a.Kind == actReparent && a.PR != nil {
			if err := r.Forge.UpdatePRBase(ctx, owner, repo, a.PR.Number, a.DesiredBase); err != nil {
				return report, fmt.Errorf("phase3 set base #%d: %w", a.PR.Number, err)
			}
		}
	}

	// Phase 4 — create new PRs, close orphans, and persist node states.
	liveByTask := map[string]PR{} // task → PR for units with a live PR (for the body listing)
	for _, a := range plan {
		switch a.Kind {
		case actCreate:
			title := fmt.Sprintf("%s: %s", id, a.TaskID)
			body := fmt.Sprintf("Stacked PR for task %s in stack %s (managed by Loom).", a.TaskID, id)
			pr, cerr := r.Forge.CreatePR(ctx, owner, repo, a.Branch, a.DesiredBase, title, body)
			if cerr != nil {
				return report, fmt.Errorf("phase4 create %s: %w", a.Branch, cerr)
			}
			r.markPublished(ctx, ws, id, a, repoPath, pr)
			liveByTask[a.TaskID] = pr
			report.Created = append(report.Created, a.TaskID)
			report.PRURLs[a.TaskID] = pr.URL
		case actReparent:
			r.markPublished(ctx, ws, id, a, repoPath, *a.PR)
			liveByTask[a.TaskID] = *a.PR
			report.Reparented = append(report.Reparented, a.TaskID)
			report.PRURLs[a.TaskID] = a.PR.URL
		case actSkip:
			r.markPublished(ctx, ws, id, a, repoPath, *a.PR)
			liveByTask[a.TaskID] = *a.PR
			report.Skipped = append(report.Skipped, a.TaskID)
			if a.PR != nil {
				report.PRURLs[a.TaskID] = a.PR.URL
			}
		case actMerged:
			_ = r.Store.UpdateNode(ctx, ws, id, a.TaskID, func(n *sl.Node) error {
				n.State = sl.NodeStateMerged
				return nil
			})
			report.Merged = append(report.Merged, a.TaskID)
		case actEmpty:
			_ = r.Store.UpdateNode(ctx, ws, id, a.TaskID, func(n *sl.Node) error {
				n.State = sl.NodeStateEmpty
				return nil
			})
			report.Empty = append(report.Empty, a.TaskID)
		case actClose:
			if err := r.Forge.ClosePR(ctx, owner, repo, a.PR.Number, "Closing: this unit was removed from the Loom stack."); err != nil {
				return report, fmt.Errorf("phase4 close #%d: %w", a.PR.Number, err)
			}
			report.Closed = append(report.Closed, a.Branch)
		}
	}

	// Phase 5 — write the stack listing into each live PR's body (idempotent:
	// only PATCH when the rendered section actually changes).
	for _, n := range ordered {
		pr, ok := liveByTask[n.TaskID]
		if !ok {
			continue
		}
		desired := withStackSection(pr.Body, renderStackListing(ordered, liveByTask, n.TaskID, id))
		if desired != pr.Body {
			if err := r.Forge.UpdatePRBody(ctx, owner, repo, pr.Number, desired); err != nil {
				return report, fmt.Errorf("phase5 body #%d: %w", pr.Number, err)
			}
		}
	}
	return report, nil
}

func (r *Reconciler) markPublished(ctx context.Context, ws string, id sl.StackID, a action, repoPath string, pr PR) {
	sha, _ := headSHA(ctx, repoPath, a.Branch)
	now := time.Now().UTC()
	_ = r.Store.UpdateNode(ctx, ws, id, a.TaskID, func(n *sl.Node) error {
		n.State = sl.NodeStatePublished
		n.PRNumber = pr.Number
		n.PRURL = pr.URL
		if sha != "" {
			n.OutputSHA = sha
		}
		n.LastPublishedAt = &now
		return nil
	})
}

func classify(r *Report, a action) {
	switch a.Kind {
	case actCreate:
		r.Created = append(r.Created, a.TaskID)
	case actReparent:
		r.Reparented = append(r.Reparented, a.TaskID)
	case actSkip:
		r.Skipped = append(r.Skipped, a.TaskID)
	case actClose:
		r.Closed = append(r.Closed, a.Branch)
	case actMerged:
		r.Merged = append(r.Merged, a.TaskID)
	case actEmpty:
		r.Empty = append(r.Empty, a.TaskID)
	}
}
