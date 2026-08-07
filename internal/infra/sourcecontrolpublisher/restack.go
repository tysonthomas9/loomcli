package stackpublish

import (
	"context"
	"fmt"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
)

// ConflictResolver resolves a rebase conflict in place. It is called mid-rebase
// with the repo (conflict markers present) and the conflicted files; it must
// leave the working tree resolved. The engine then stages and runs
// `git rebase --continue`. Implementations live in the CLI/runner layer (an
// interactive agent or a headless backend CLI), keeping this package agent-free.
type ConflictResolver interface {
	ResolveRebaseConflicts(ctx context.Context, repoPath, branch, ontoRef string, conflicts []string) error
}

// RestackReport summarizes a restack run.
type RestackReport struct {
	Rebased  []string `json:"rebased,omitempty"`  // task IDs whose branch was rebased
	Resolved []string `json:"resolved,omitempty"` // task IDs that needed conflict resolution
}

// slideSafe reports whether a descendant is safe to retarget onto RootBase after
// its predecessor merged: either the predecessor's commits are reachable from the
// updated base (a merge-commit merge), or the base is already reachable from the
// descendant (the descendant has been rebased onto it).
func slideSafe(ctx context.Context, repoPath, rootBase, predBranch, nodeBranch string) (bool, error) {
	onBase := "origin/" + rootBase
	if ok, err := isAncestor(ctx, repoPath, predBranch, onBase); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	if ok, err := isAncestor(ctx, repoPath, onBase, nodeBranch); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	return false, nil
}

// Restack rebases descendants of merged predecessors onto the live RootBase,
// resolving conflicts via the resolver, so a squash/rebase merge doesn't leave
// the descendant carrying its predecessor's commits. It is idempotent: chains
// that are already safe (merge-commit, or already rebased) are skipped.
func (r *Reconciler) Restack(ctx context.Context, ws string, id sl.StackID, repoPath string, resolver ConflictResolver) (*RestackReport, error) {
	// Defensive: restack semantics (rebase descendants of MERGED units) are
	// meaningless without pull requests; every current caller is GitHub-gated,
	// but fail closed rather than hitting a PR method's runtime error if a
	// future caller hands us a branches-only forge.
	if !forgeSupportsPullRequests(r.Forge) {
		return nil, fmt.Errorf("stackpublish: restack requires a pull-request-capable forge")
	}
	stackProjection, err := r.Stacks.GetStack(ctx, ws, string(id))
	if err != nil {
		return nil, err
	}
	nodeProjections, err := r.Stacks.ListStackNodes(ctx, ws, string(id))
	if err != nil {
		return nil, err
	}
	stack := legacyStack(*stackProjection)
	nodes := legacyStackNodes(nodeProjections)
	ordered, err := sl.Ordered(nodes)
	if err != nil {
		return nil, fmt.Errorf("invalid lineage: %w", err)
	}
	owner, repo, err := repoSlug(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	prs, err := r.Forge.ListStackPRs(ctx, owner, repo, sl.StackBranchPrefix(id))
	if err != nil {
		return nil, err
	}
	prsByHead := map[string]PR{}
	for _, p := range prs {
		prsByHead[p.Head] = p
	}
	if err := fetchRef(ctx, repoPath, "origin", stack.RootBase); err != nil {
		return nil, fmt.Errorf("restack: fetch %s: %w", stack.RootBase, err)
	}

	report := &RestackReport{}
	for _, chain := range chains(ordered) {
		if err := r.restackChain(ctx, repoPath, stack.RootBase, chain, prsByHead, resolver, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// restackChain cascade-rebases one chain when a merged prefix has left a
// descendant unsafe to slide.
func (r *Reconciler) restackChain(ctx context.Context, repoPath, rootBase string, chain []sl.Node, prsByHead map[string]PR, resolver ConflictResolver, report *RestackReport) error {
	oldTip := make(map[string]string, len(chain))
	for _, u := range chain {
		t, err := headSHA(ctx, repoPath, u.OutputBranch)
		if err != nil {
			return fmt.Errorf("restack: resolve %s: %w", u.OutputBranch, err)
		}
		oldTip[u.TaskID] = t
	}

	onto := ""     // ref the next non-merged unit rebases onto
	upstream := "" // commit after which the next unit's own commits begin
	cascading := false
	for _, u := range chain {
		if pr, ok := prsByHead[u.OutputBranch]; ok && pr.Merged {
			// Next non-merged unit rebases onto the live base, dropping this unit.
			onto = "origin/" + rootBase
			upstream = oldTip[u.TaskID]
			continue
		}
		if onto == "" {
			// No merged predecessor yet — leave this unit; it anchors its successor.
			upstream = oldTip[u.TaskID]
			onto = u.OutputBranch
			continue
		}
		if !cascading {
			// First non-merged unit after the merged prefix: only rebase if it's
			// genuinely unsafe (a squash/rebase merge). Merge-commit chains are
			// already fine and need no rebase.
			safe, err := slideSafe(ctx, repoPath, rootBase, upstream, u.OutputBranch)
			if err != nil {
				return fmt.Errorf("restack: safety check %s: %w", u.TaskID, err)
			}
			if safe {
				return nil // whole chain is safe; nothing to do
			}
			cascading = true
		}
		resolved, err := r.rebaseOnto(ctx, repoPath, u.OutputBranch, onto, upstream, resolver)
		if err != nil {
			return err
		}
		report.Rebased = append(report.Rebased, u.TaskID)
		if resolved {
			report.Resolved = append(report.Resolved, u.TaskID)
		}
		upstream = oldTip[u.TaskID] // successor cuts off this unit's OLD tip
		onto = u.OutputBranch       // ...and rebases onto its NEW (rebased) branch
	}
	return nil
}

// rebaseOnto runs `git rebase --onto <onto> <upstream> <branch>`, driving the
// resolver through any conflicts. Returns whether conflict resolution was needed.
func (r *Reconciler) rebaseOnto(ctx context.Context, repoPath, branch, onto, upstream string, resolver ConflictResolver) (bool, error) {
	env := append(envWith(), "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true")
	if _, err := runGit(ctx, repoPath, env, "rebase", "--onto", onto, upstream, branch); err == nil {
		return false, nil // clean rebase
	}
	conflicts := conflictedFiles(ctx, repoPath)
	if len(conflicts) == 0 {
		_, _ = runGit(ctx, repoPath, env, "rebase", "--abort")
		return false, fmt.Errorf("rebase %s onto %s failed (not a conflict)", branch, onto)
	}
	if resolver == nil {
		_, _ = runGit(ctx, repoPath, env, "rebase", "--abort")
		return false, fmt.Errorf("rebase conflict in %s and no resolver available: %s", branch, strings.Join(conflicts, ", "))
	}
	for {
		if err := resolver.ResolveRebaseConflicts(ctx, repoPath, branch, onto, conflicts); err != nil {
			_, _ = runGit(ctx, repoPath, env, "rebase", "--abort")
			return true, fmt.Errorf("conflict resolution failed for %s: %w", branch, err)
		}
		if _, err := runGit(ctx, repoPath, nil, "add", "-A"); err != nil {
			_, _ = runGit(ctx, repoPath, env, "rebase", "--abort")
			return true, err
		}
		if _, err := runGit(ctx, repoPath, env, "rebase", "--continue"); err == nil {
			return true, nil // rebase complete
		}
		conflicts = conflictedFiles(ctx, repoPath)
		if len(conflicts) == 0 {
			_, _ = runGit(ctx, repoPath, env, "rebase", "--abort")
			return true, fmt.Errorf("rebase --continue failed for %s with no remaining conflicts", branch)
		}
		// More conflicts on the next replayed commit → loop.
	}
}

func conflictedFiles(ctx context.Context, repoPath string) []string {
	out, _ := runGit(ctx, repoPath, nil, "diff", "--name-only", "--diff-filter=U")
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			files = append(files, s)
		}
	}
	return files
}

// chains splits the forest's ordered nodes into per-root chains.
func chains(ordered []sl.Node) [][]sl.Node {
	var out [][]sl.Node
	var cur []sl.Node
	for _, n := range ordered {
		if n.BaseTaskID == "" && len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, n)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
