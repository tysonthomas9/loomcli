package stackpublish

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
)

// MergedPullRequest is the stack-scoped PR selected for a merge operation.
type MergedPullRequest struct {
	TaskID       string `json:"taskId"`
	OutputBranch string `json:"outputBranch"`
	Number       int    `json:"number"`
	URL          string `json:"url,omitempty"`
}

// MergeReport summarizes a safe stack merge plus the reconcile publish run that
// follows it.
type MergeReport struct {
	StackID     sl.StackID        `json:"stackId"`
	MergedPR    MergedPullRequest `json:"mergedPr"`
	Publish     *Report           `json:"publish,omitempty"`
	NextToMerge []StatusRow       `json:"nextToMerge,omitempty"`
}

type mergeTarget struct {
	Node sl.Node
	PR   PR
}

// MergeNext resolves target against the stack's current next-to-merge unit,
// merges that PR, and immediately runs Publish so descendants are reparented to
// the live base. An explicit target must identify a current next-to-merge unit.
func (r *Reconciler) MergeNext(ctx context.Context, ws string, id sl.StackID, repoPath, target string, opts MergeOptions) (*MergeReport, error) {
	stack, err := r.Store.GetStack(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	owner, repo, err := repoSlug(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	mt, err := r.resolveMergeTarget(ctx, ws, id, repoPath, strings.TrimSpace(target))
	if err != nil {
		return nil, err
	}
	if err := r.Forge.MergePR(ctx, repoPath, owner, repo, mt.PR.Number, opts); err != nil {
		return nil, err
	}
	publish, err := r.Publish(ctx, ws, id, repoPath, Options{})
	if err != nil {
		return nil, fmt.Errorf("post-merge publish: %w", err)
	}
	status, err := r.StackStatus(ctx, ws, id, repoPath)
	if err != nil {
		return nil, fmt.Errorf("post-merge status: %w", err)
	}
	next := make([]StatusRow, 0, len(status.Rows))
	for _, row := range status.Rows {
		if row.NextToMerge {
			next = append(next, row)
		}
	}
	return &MergeReport{
		StackID: stack.ID,
		MergedPR: MergedPullRequest{
			TaskID: mt.Node.TaskID, OutputBranch: mt.Node.OutputBranch,
			Number: mt.PR.Number, URL: mt.PR.URL,
		},
		Publish:     publish,
		NextToMerge: next,
	}, nil
}

func (r *Reconciler) resolveMergeTarget(ctx context.Context, ws string, id sl.StackID, repoPath, target string) (mergeTarget, error) {
	nodes, err := r.Store.ListNodes(ctx, ws, id)
	if err != nil {
		return mergeTarget{}, err
	}
	ordered, err := sl.Ordered(nodes)
	if err != nil {
		return mergeTarget{}, fmt.Errorf("invalid lineage: %w", err)
	}
	nextSet := sl.NextToMergeUnits(ordered)
	if len(nextSet) == 0 {
		return mergeTarget{}, fmt.Errorf("stack %s has no nextToMerge unit", id)
	}
	owner, repo, err := repoSlug(ctx, repoPath)
	if err != nil {
		return mergeTarget{}, err
	}
	prs, err := r.Forge.ListStackPRs(ctx, owner, repo, sl.StackBranchPrefix(id))
	if err != nil {
		return mergeTarget{}, err
	}
	prsByHead := prsByHeadPreferOpen(prs)

	if target == "" {
		if len(nextSet) > 1 {
			return mergeTarget{}, fmt.Errorf("stack %s has multiple nextToMerge units (%s); pass an explicit task id, branch, PR number, or URL", id, formatNextTargets(ordered, nextSet, prsByHead))
		}
		for _, n := range ordered {
			if nextSet[n.TaskID] {
				return mergeTargetForNode(id, n, prsByHead)
			}
		}
	}

	n, pr, ok := matchMergeTarget(target, ordered, prsByHead)
	if !ok {
		return mergeTarget{}, fmt.Errorf("target %q does not identify a PR or task in stack %s", target, id)
	}
	if !nextSet[n.TaskID] {
		return mergeTarget{}, fmt.Errorf("target %q resolves to %s, which is not nextToMerge; current nextToMerge: %s", target, n.TaskID, formatNextTargets(ordered, nextSet, prsByHead))
	}
	if pr.Number == 0 || pr.State != "open" || pr.Merged {
		return mergeTarget{}, fmt.Errorf("nextToMerge unit %s has no open PR; run `loom stack publish %s` first", n.TaskID, id)
	}
	return mergeTarget{Node: n, PR: pr}, nil
}

func prsByHeadPreferOpen(prs []PR) map[string]PR {
	out := make(map[string]PR, len(prs))
	for _, p := range prs {
		if existing, ok := out[p.Head]; ok && existing.State == "open" && p.State != "open" {
			continue
		}
		out[p.Head] = p
	}
	return out
}

func mergeTargetForNode(id sl.StackID, n sl.Node, prsByHead map[string]PR) (mergeTarget, error) {
	pr := prsByHead[n.OutputBranch]
	if pr.Number == 0 || pr.State != "open" || pr.Merged {
		return mergeTarget{}, fmt.Errorf("nextToMerge unit %s has no open PR; run `loom stack publish %s` first", n.TaskID, id)
	}
	return mergeTarget{Node: n, PR: pr}, nil
}

var pullURLNumberRe = regexp.MustCompile(`(?i)(?:^|/)pull/([0-9]+)(?:[/?#].*)?$`)

func parsePullNumber(target string) (int, bool) {
	t := strings.TrimSpace(strings.TrimPrefix(target, "#"))
	if m := pullURLNumberRe.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	n, err := strconv.Atoi(t)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func matchMergeTarget(target string, ordered []sl.Node, prsByHead map[string]PR) (sl.Node, PR, bool) {
	if number, ok := parsePullNumber(target); ok {
		for _, n := range ordered {
			pr := prsByHead[n.OutputBranch]
			if pr.Number == number {
				return n, pr, true
			}
		}
		return sl.Node{}, PR{}, false
	}
	for _, n := range ordered {
		pr := prsByHead[n.OutputBranch]
		if target == n.TaskID || target == n.OutputBranch {
			return n, pr, true
		}
	}
	return sl.Node{}, PR{}, false
}

func formatNextTargets(ordered []sl.Node, nextSet map[string]bool, prsByHead map[string]PR) string {
	var parts []string
	for _, n := range ordered {
		if !nextSet[n.TaskID] {
			continue
		}
		pr := prsByHead[n.OutputBranch]
		if pr.Number > 0 {
			parts = append(parts, fmt.Sprintf("%s (#%d %s)", n.TaskID, pr.Number, n.OutputBranch))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s)", n.TaskID, n.OutputBranch))
		}
	}
	return strings.Join(parts, ", ")
}

func mergePRArgs(owner, repo string, number int, opts MergeOptions) []string {
	args := []string{"pr", "merge", strconv.Itoa(number), "--repo", owner + "/" + repo}
	switch opts.Method {
	case MergeMethodMerge:
		args = append(args, "--merge")
	case MergeMethodSquash:
		args = append(args, "--squash")
	case MergeMethodRebase:
		args = append(args, "--rebase")
	}
	if opts.Auto {
		args = append(args, "--auto")
	}
	if opts.DisableAuto {
		args = append(args, "--disable-auto")
	}
	if opts.Admin {
		args = append(args, "--admin")
	}
	if opts.MatchHeadCommit != "" {
		args = append(args, "--match-head-commit", opts.MatchHeadCommit)
	}
	if opts.AuthorEmail != "" {
		args = append(args, "--author-email", opts.AuthorEmail)
	}
	if opts.SubjectSet {
		args = append(args, "--subject", opts.Subject)
	}
	if opts.BodySet {
		args = append(args, "--body", opts.Body)
	}
	if opts.DeleteBranch {
		args = append(args, "--delete-branch")
	}
	return args
}

// MergePR delegates to gh so `loom stack merge` preserves gh pr merge behavior
// for merge queues, auto-merge, branch deletion, and strategy flags after Loom
// has constrained the target to the stack's next-to-merge PR.
func (g *GitHubForge) MergePR(ctx context.Context, repoPath, owner, repo string, number int, opts MergeOptions) error {
	args := mergePRArgs(owner, repo, number, opts)
	cmd := exec.CommandContext(ctx, "gh", args...) //nolint:gosec // fixed executable; args are constructed from typed options
	cmd.Dir = repoPath
	env := envWith()
	if strings.TrimSpace(g.token) != "" {
		env = append(env, "GH_TOKEN="+g.token)
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(scrubSecrets(string(out), g.token)))
	}
	return nil
}
