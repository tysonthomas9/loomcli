package uniondebt

// The pr-pending clear test.
//
// This is the one part of the sweep that talks to GitHub. The no-fetch rule in
// the package doc still holds — nothing here runs `git fetch`, and the only git
// it runs is the same local ref read the union probe does — but mergeability is
// a fact only GitHub can compute, so it is read through `gh`. Every call goes
// through githubClient so unit tests use a fake and never touch the network.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Mergeable values as GitHub reports them on a pull request.
const (
	// MergeableYes is the ONLY value that clears a marker.
	MergeableYes = "MERGEABLE"
	// MergeableNo means the PR conflicts with its base.
	MergeableNo = "CONFLICTING"
	// MergeableUnknown is what GitHub answers while it computes mergeability
	// lazily — 142 PRs answered it on PUPPET-626's first pass. It is NOT
	// mergeable, and a sweep that read it as one cleared markers on work that
	// could not land.
	MergeableUnknown = "UNKNOWN"
)

// PR is the slice of a pull request the pr-pending test reads. The field tags
// are the `gh pr list --json` names, so the struct decodes gh's output as-is.
type PR struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Mergeable   string `json:"mergeable"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
}

// githubClient is the GitHub layer, faked in tests.
type githubClient interface {
	// OpenPRs lists the OPEN pull requests of the repo that clone points at.
	// Only open PRs: a base chain that reaches a closed or merged-away branch
	// must read as "no PR for that base" and keep the marker.
	OpenPRs(clone string) ([]PR, error)
	// Mergeable re-reads one PR's mergeable value, for the UNKNOWN re-poll.
	Mergeable(clone string, number int) (string, error)
}

// prListLimit caps one repo's PR listing. gh defaults to 30, which silently
// truncates a repo with a long-lived queue of agent branches.
const prListLimit = "500"

type execGHClient struct{}

func (execGHClient) run(clone string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...) //nolint:gosec,norawexec // G204 — args are literals plus a PR number
	cmd.Dir = clone
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gh %s in %s: %w: %s", strings.Join(args, " "), clone, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh %s in %s: %w", strings.Join(args, " "), clone, err)
	}
	return out, nil
}

func (g execGHClient) OpenPRs(clone string) ([]PR, error) {
	out, err := g.run(clone, "pr", "list", "--state", "open", "--limit", prListLimit,
		"--json", "number,state,mergeable,baseRefName,headRefName")
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse gh pr list output in %s: %w", clone, err)
	}
	return prs, nil
}

func (g execGHClient) Mergeable(clone string, number int) (string, error) {
	out, err := g.run(clone, "pr", "view", fmt.Sprint(number), "--json", "mergeable")
	if err != nil {
		return "", err
	}
	var view struct {
		Mergeable string `json:"mergeable"`
	}
	if err := json.Unmarshal(out, &view); err != nil {
		return "", fmt.Errorf("parse gh pr view %d output in %s: %w", number, clone, err)
	}
	return view.Mergeable, nil
}

// PRCheck is the outcome of the pr-pending test for one ticket.
type PRCheck struct {
	Class Class
	// Reason is the machine-readable skip reason (e.g. "mergeability-unknown").
	Reason string
	// Detail is the human sentence that goes into the report and the comment.
	Detail string
	// Number and Mergeable describe the ticket's own PR, when one was found.
	Number    int
	Mergeable string
	// Chain is the base chain as walked, e.g. "#612 MERGEABLE -> #598 MERGEABLE -> v5".
	Chain string
}

// Passed reports whether the marker may be cleared. Exactly one class does.
func (c PRCheck) Passed() bool { return c.Class == ClassPRMergeable }

const (
	// ClassPRMergeable is the ONE class that clears the marker: a PR exists,
	// GitHub reports MERGEABLE, and the base chain terminates at the trunk
	// through open, mergeable PRs.
	ClassPRMergeable Class = "pr-mergeable"
	// ClassPRUnknown means GitHub still answered UNKNOWN after the re-poll.
	ClassPRUnknown Class = "pr-unknown"
	// ClassPRConflicting means some link in the chain is not mergeable.
	ClassPRConflicting Class = "pr-conflicting"
	// ClassPRNone means no open PR exists for the ticket's branch.
	ClassPRNone Class = "pr-none"
	// ClassPRBaseUnreachable means the base chain does not reach the trunk:
	// some base has no open PR of its own.
	ClassPRBaseUnreachable Class = "pr-base-unreachable"
)

// chainDepthCap bounds the base walk at the size of the graph it walks. A
// cycle-free chain cannot visit more PRs than exist, so this can only fire on
// a graph that the cycle guard somehow let through — it is a termination
// proof, not a policy.
//
// It is NOT a fixed small number. Measured against this workspace on
// 2026-09-20: 354 open loomcli PRs, 341 of them reaching v5, with the deepest
// chain 136 links long. A cap of 10 (the first cut) answered "chain-too-deep"
// for 123 of 153 items and cleared nothing at all — a test that never clears
// is not a conservative test, it is a broken one.
func chainDepthCap(graph int) int { return graph + 1 }

// Poll defaults for the UNKNOWN re-poll: four looks spread over ~30s.
const (
	defaultPollAttempts = 4
	defaultPollBackoff  = 5 * time.Second
)

// PRCheckRequest is one pr-pending test.
type PRCheckRequest struct {
	// Clone is the directory gh runs in; it identifies the repo.
	Clone string
	// Trunk is the repo's target_branch, read from the contract. Never "main"
	// by assumption: loomcli's is v5.
	Trunk  string
	TaskID string
	// NoPoll skips the UNKNOWN re-poll. The drift scan sets it: drift is
	// read-only reporting and must not sleep once per candidate PR.
	NoPoll bool
}

// PRChecker answers the pr-pending clear test.
type PRChecker struct {
	gh       githubClient
	git      gitRunner
	attempts int
	backoff  time.Duration
	sleep    func(time.Duration)
	// prs caches one listing per clone for the life of the checker. Every
	// ticket in a repo walks the same PR graph, and re-listing it per ticket
	// cost 39 `gh pr list` calls over 354 PRs on this workspace's first live
	// run. The cache is a snapshot on purpose: one sweep judges one graph, so
	// two tickets cannot disagree about what the stack looked like.
	prs map[string][]PR
}

// NewPRChecker returns a PRChecker backed by the real gh and git binaries.
func NewPRChecker() *PRChecker {
	return &PRChecker{
		gh:       execGHClient{},
		git:      execGitRunner{},
		attempts: defaultPollAttempts,
		backoff:  defaultPollBackoff,
		sleep:    time.Sleep,
	}
}

// OpenPRs exposes the PR listing, cached per clone, so the sweep can scan for
// inverse drift over the same snapshot the clear pass judged.
func (c *PRChecker) OpenPRs(clone string) ([]PR, error) {
	if prs, ok := c.prs[clone]; ok {
		return prs, nil
	}
	prs, err := c.gh.OpenPRs(clone)
	if err != nil {
		return nil, err
	}
	if c.prs == nil {
		c.prs = map[string][]PR{}
	}
	c.prs[clone] = prs
	return prs, nil
}

// Check runs the three-part test for one ticket:
//
//  1. an open PR exists for the ticket's branch (revision-aware: loom/<ID>-rN
//     outranks loom/<ID>, exactly as the union probe resolves refs);
//  2. GitHub reports MERGEABLE for it, with UNKNOWN re-polled and never
//     treated as mergeable;
//  3. the base chain terminates at the repo's trunk through open, mergeable
//     PRs.
//
// It returns an error only for a malformed graph (a base cycle) or a GitHub
// failure. Every other outcome is a class, and every class but
// ClassPRMergeable keeps the marker.
func (c *PRChecker) Check(req PRCheckRequest) (PRCheck, error) {
	if err := validateRef(req.TaskID); err != nil {
		return PRCheck{}, err
	}
	if req.Trunk == "" {
		return PRCheck{}, fmt.Errorf("no target_branch in the contract for the repo at %s: "+
			"without a trunk no base chain can be judged to terminate", req.Clone)
	}

	prs, err := c.OpenPRs(req.Clone)
	if err != nil {
		return PRCheck{}, err
	}
	byHead := indexByHead(prs)

	head, err := c.headBranch(req.Clone, req.TaskID, byHead)
	if err != nil {
		return PRCheck{}, err
	}
	pr, ok := byHead[head]
	if !ok {
		return PRCheck{
			Class:  ClassPRNone,
			Reason: "no-pr",
			Detail: fmt.Sprintf("no open PR has head loom/%s (or a loom/%s-rN revision)", req.TaskID, req.TaskID),
		}, nil
	}
	return c.walk(req, pr, byHead)
}

// indexByHead keys open PRs by head branch. A duplicate head is impossible on
// GitHub for open PRs against one repo, so last-write-wins is safe.
func indexByHead(prs []PR) map[string]PR {
	byHead := make(map[string]PR, len(prs))
	for _, pr := range prs {
		byHead[pr.HeadRefName] = pr
	}
	return byHead
}

// headBranch picks the branch name that answers to taskID today. It prefers
// the highest-numbered republished revision, the same order the union probe
// uses, and falls back to the unsuffixed name. The revision is read from the
// clone's refs; when the clone has none but GitHub does, the PR index is the
// second source, so a revision published after this clone last fetched is
// still found.
func (c *PRChecker) headBranch(clone, taskID string, byHead map[string]PR) (string, error) {
	rev, err := highestRevisionRef(c.git, clone, taskID)
	if err != nil {
		return "", err
	}
	if rev != "" {
		return strings.TrimPrefix(rev, "origin/"), nil
	}
	if best := highestRevisionHead(taskID, byHead); best != "" {
		return best, nil
	}
	return "loom/" + taskID, nil
}

// highestRevisionHead finds the highest loom/<ID>-rN among open PR heads.
func highestRevisionHead(taskID string, byHead map[string]PR) string {
	re := revisionPattern(taskID)
	best, bestN := "", -1
	for head := range byHead {
		m := re.FindStringSubmatch(head)
		if m == nil {
			continue
		}
		n := atoiSafe(m[1])
		if n > bestN {
			bestN, best = n, head
		}
	}
	return best
}

// walk applies the mergeability test to the ticket's PR and then to every link
// of its base chain, stopping at the trunk.
func (c *PRChecker) walk(req PRCheckRequest, pr PR, byHead map[string]PR) (PRCheck, error) {
	out := PRCheck{Number: pr.Number}
	var links []string
	seen := map[string]bool{}
	depthCap := chainDepthCap(len(byHead))

	for depth := 0; ; depth++ {
		if seen[pr.HeadRefName] {
			return PRCheck{}, fmt.Errorf("base chain from #%d cycles back to %s: %s",
				out.Number, pr.HeadRefName, chainOf(append(links, pr.HeadRefName)))
		}
		seen[pr.HeadRefName] = true
		if depth >= depthCap {
			out.Class, out.Reason = ClassPRBaseUnreachable, "chain-too-deep"
			out.Chain = chainOf(links)
			out.Detail = fmt.Sprintf("the base chain is deeper than %d links and was not followed to %s: %s",
				depthCap, req.Trunk, out.Chain)
			return out, nil
		}

		mergeable, err := c.mergeableOf(req, pr)
		if err != nil {
			return PRCheck{}, err
		}
		links = append(links, fmt.Sprintf("#%d %s", pr.Number, mergeable))
		if depth == 0 {
			out.Mergeable = mergeable
		}
		if stop, res := classifyMergeable(mergeable, pr, links); stop {
			res.Number, res.Mergeable = out.Number, out.Mergeable
			return res, nil
		}

		if pr.BaseRefName == req.Trunk {
			out.Class = ClassPRMergeable
			out.Chain = chainOf(append(links, req.Trunk))
			out.Detail = fmt.Sprintf("#%d is MERGEABLE and its base chain reaches %s: %s",
				out.Number, req.Trunk, out.Chain)
			return out, nil
		}

		next, ok := byHead[pr.BaseRefName]
		if !ok {
			out.Class, out.Reason = ClassPRBaseUnreachable, "base-no-pr"
			out.Chain = chainOf(append(links, pr.BaseRefName))
			out.Detail = fmt.Sprintf("the base chain stops at %s, which is not %s and has no open PR: %s",
				pr.BaseRefName, req.Trunk, out.Chain)
			return out, nil
		}
		pr = next
	}
}

// classifyMergeable turns one link's mergeable value into a terminal result.
// The second result is meaningful only when the first is true; MERGEABLE is
// the only value that lets the walk continue.
func classifyMergeable(mergeable string, pr PR, links []string) (bool, PRCheck) {
	switch mergeable {
	case MergeableYes:
		return false, PRCheck{}
	case MergeableUnknown:
		return true, PRCheck{
			Class:  ClassPRUnknown,
			Reason: "mergeability-unknown",
			Chain:  chainOf(links),
			Detail: fmt.Sprintf("GitHub still reports UNKNOWN for #%d after the re-poll; UNKNOWN is not mergeable", pr.Number),
		}
	default:
		return true, PRCheck{
			Class:  ClassPRConflicting,
			Reason: "not-mergeable",
			Chain:  chainOf(links),
			Detail: fmt.Sprintf("#%d is %s, not %s", pr.Number, mergeable, MergeableYes),
		}
	}
}

// mergeableOf reads one PR's mergeable value, re-polling while GitHub answers
// UNKNOWN. The backoff is linear and bounded: GitHub computes mergeability
// lazily and usually has an answer within seconds, but a sweep must never
// block indefinitely on one PR.
func (c *PRChecker) mergeableOf(req PRCheckRequest, pr PR) (string, error) {
	mergeable := pr.Mergeable
	if req.NoPoll {
		return mergeable, nil
	}
	for attempt := 1; mergeable == MergeableUnknown && attempt < c.attempts; attempt++ {
		c.sleep(time.Duration(attempt) * c.backoff)
		got, err := c.gh.Mergeable(req.Clone, pr.Number)
		if err != nil {
			return "", err
		}
		mergeable = got
	}
	return mergeable, nil
}

// chainHead and chainTail bound how much of a long chain is rendered.
const (
	chainHead = 4
	chainTail = 4
)

// chainOf renders the base chain, eliding the middle of a long one. This
// workspace's stack runs past a hundred links, and a hundred-link string in a
// report line and a ticket comment buries the two facts a reader needs: where
// the chain starts and that it ends at the trunk. The elision names how many
// links it dropped, and every dropped link is MERGEABLE by construction — a
// link that is not terminates the walk, and those chains are short.
func chainOf(links []string) string {
	if len(links) <= chainHead+chainTail+1 {
		return strings.Join(links, " -> ")
	}
	elided := len(links) - chainHead - chainTail
	parts := append([]string{}, links[:chainHead]...)
	parts = append(parts, fmt.Sprintf("... %d more links ...", elided))
	parts = append(parts, links[len(links)-chainTail:]...)
	return strings.Join(parts, " -> ")
}
