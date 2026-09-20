package uniondebt

// The pr-pending pass of the sweep.
//
// It is a sibling of the union-pending pass, not a second command: the same
// ledger enumeration, the same `apply` write path, the same report. What
// differs is the question. The union pass asks "is this branch in the local
// union branch?" and answers it from local refs. This one asks "can this
// ticket's pull request actually land?" — a fact only GitHub can compute, read
// through githubClient (see prpending.go) so unit tests never reach the
// network.
//
// The pass only ever CLEARS. A ticket that fails the test keeps its marker,
// and a ticket that fails the test while carrying NO marker is reported as
// drift and left alone: deriving a marker is a different question, answered by
// PUPPET-653.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// prChecker is the pr-pending test, stubbed in tests.
type prChecker interface {
	Check(req PRCheckRequest) (PRCheck, error)
	OpenPRs(clone string) ([]PR, error)
}

// checker returns the pr-pending test, creating the real (gh-backed) one on
// first use. Nothing calls this unless there is an item to test, which is what
// keeps a sweep with an empty pr-pending ledger free of GitHub calls.
func (s *Sweeper) checker() prChecker {
	if s.pr == nil {
		s.pr = NewPRChecker()
	}
	return s.pr
}

// sweepPRPending runs the clear pass over the pr-pending ledger and, when
// asked, the read-only drift scan. A single item's failure is an error item,
// never an aborted run; only a ledger read that fails outright returns an
// error, because a partial ledger would silently under-report.
func (s *Sweeper) sweepPRPending(ctx context.Context, rep *Report) error {
	marker := s.labels().PRPending
	ledger, err := s.issuesLabeled(ctx, marker)
	if err != nil {
		return err
	}

	markered := make(map[string]bool, len(ledger))
	for _, iss := range ledger {
		markered[iss.ID] = true
		rep.add(s.handlePR(ctx, iss))
	}

	if !s.opts.PRDrift {
		return nil
	}
	for _, item := range s.driftScan(ctx, markered) {
		rep.add(item)
	}
	return nil
}

// handlePR runs the three-part test for one marked ticket and clears the
// marker when — and only when — it passes.
func (s *Sweeper) handlePR(ctx context.Context, iss backend.IssueData) Item {
	item := s.prItem(iss.ID, iss.SourceRepo)

	req, errItem, ok := s.prRequest(&item, iss.SourceRepo, iss.ID, false)
	if !ok {
		return errItem
	}

	check, err := s.checker().Check(req)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item
	}
	recordCheck(&item, check)

	if !check.Passed() {
		item.Action = ActionSkipped
		return item
	}
	item.Action = ActionCleared
	s.apply(ctx, &item, func() error { return s.clearPRPending(ctx, iss.ID, item) })
	return item
}

// prItem seeds the report row shared by both the clear pass and the drift
// scan.
func (s *Sweeper) prItem(id, repo string) Item {
	return Item{
		OriginID: id,
		Repo:     repo,
		ProbedAt: s.opts.Now().UTC().Format(time.RFC3339),
		DryRun:   s.opts.DryRun,
	}
}

// prRequest resolves the clone and the trunk one ticket's test needs. The
// trunk is read from the contract and never guessed: loomcli's is `v5`, and a
// hardcoded "main" would judge every base chain to terminate at a branch that
// does not exist, clearing markers on work that cannot land.
func (s *Sweeper) prRequest(item *Item, repo, id string, noPoll bool) (PRCheckRequest, Item, bool) {
	li, ok := s.opts.Contract.Lookup(repo)
	if !ok {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("no local_integration clone for source repo %q in the contract", repo)
		return PRCheckRequest{}, *item, false
	}
	item.Clone = li.Clone

	trunk, ok := s.opts.Contract.Trunk(repo)
	if !ok {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("no target_branch for source repo %q in the contract: "+
			"without a trunk no base chain can be judged to terminate", repo)
		return PRCheckRequest{}, *item, false
	}
	return PRCheckRequest{Clone: li.Clone, Trunk: trunk, TaskID: id, NoPoll: noPoll}, Item{}, true
}

// recordCheck copies a test outcome onto the report row.
func recordCheck(item *Item, check PRCheck) {
	item.Class = check.Class
	item.PR = check.Number
	item.Mergeable = check.Mergeable
	item.Chain = check.Chain
	item.Reason = check.Reason
	item.Detail = check.Detail
}

// clearPRPending comments on the ticket and then removes the marker. The
// comment goes FIRST, exactly as retire() orders it: a marker removed with no
// comment would erase the only record of why a sweep decided this PR can land.
func (s *Sweeper) clearPRPending(ctx context.Context, id string, item Item) error {
	if _, err := s.issues.AddComment(ctx, backend.CommentAddParams{
		IssueID: id,
		Text:    prClearedComment(item, s.labels()),
	}); err != nil {
		return fmt.Errorf("comment on %s: %w", id, err)
	}
	marker := s.labels().PRPending
	if err := s.issues.RemoveLabel(ctx, id, marker); err != nil {
		return fmt.Errorf("remove %s from %s: %w", marker, id, err)
	}
	return nil
}

func prClearedComment(item Item, lbl LabelSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "union-debt sweep: %s marker cleared — the pull request can land.\n\n", lbl.PRPending)
	fmt.Fprintf(&b, "Repo:      %s\n", item.Repo)
	fmt.Fprintf(&b, "Clone:     %s\n", item.Clone)
	fmt.Fprintf(&b, "PR:        #%d\n", item.PR)
	fmt.Fprintf(&b, "Mergeable: %s\n", item.Mergeable)
	fmt.Fprintf(&b, "Chain:     %s\n", item.Chain)
	fmt.Fprintf(&b, "Checked:   %s\n", item.ProbedAt)
	fmt.Fprintf(&b, "\nEvery link in that chain is an open PR GitHub reports as %s, and the chain\n", MergeableYes)
	fmt.Fprintf(&b, "ends at the repo's trunk. %s is not mergeable and never clears this marker.\n", MergeableUnknown)
	return b.String()
}

// --- inverse drift (read-only) ---

// driftScan lists every open PR whose head is a task branch and reports the
// tickets that FAIL the test while carrying no marker. It writes nothing: the
// marker this sweep can clear is not a marker it may apply.
//
// The re-poll is switched off here. Drift is a reporting pass over every open
// PR in the workspace, and sleeping tens of seconds per UNKNOWN would make it
// cost minutes; an UNKNOWN read as "not mergeable" is exactly the direction
// this report may safely err in, because nothing is written either way.
func (s *Sweeper) driftScan(ctx context.Context, markered map[string]bool) []Item {
	var out []Item
	for _, repo := range s.opts.Contract.Repos() {
		if !s.wantRepo(repo) {
			continue
		}
		out = append(out, s.driftScanRepo(ctx, repo, markered)...)
	}
	return out
}

func (s *Sweeper) driftScanRepo(ctx context.Context, repo string, markered map[string]bool) []Item {
	item := s.prItem("", repo)
	li, ok := s.opts.Contract.Lookup(repo)
	if !ok {
		return nil
	}
	item.Clone = li.Clone

	prs, err := s.checker().OpenPRs(li.Clone)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("list open PRs for %s: %v", repo, err)
		return []Item{item}
	}

	var out []Item
	for _, id := range taskIDsOf(prs) {
		if markered[id] {
			// Already judged by the clear pass above.
			continue
		}
		if drift, ok := s.driftOf(ctx, repo, id); ok {
			out = append(out, drift)
		}
	}
	return out
}

// driftOf tests one unmarked ticket. The second result is false whenever there
// is nothing to report — the ticket is absent from this workspace, it already
// carries the marker, or its PR passes the test.
func (s *Sweeper) driftOf(ctx context.Context, repo, id string) (Item, bool) {
	detail, err := s.issues.Get(ctx, id)
	if err != nil || detail == nil {
		// A branch may name a ticket this workspace does not have (another
		// workspace's ID, or a hand-made branch). Nothing to report.
		return Item{}, false
	}
	if detail.SourceRepo != repo {
		// The same branch name can exist in two repos; the ticket belongs to
		// exactly one of them, and only that repo's PRs judge it.
		return Item{}, false
	}
	for _, label := range detail.Labels {
		if label == s.labels().PRPending {
			return Item{}, false
		}
	}

	item := s.prItem(id, repo)
	req, errItem, ok := s.prRequest(&item, repo, id, true)
	if !ok {
		return errItem, true
	}
	check, err := s.checker().Check(req)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item, true
	}
	if check.Passed() {
		return Item{}, false
	}
	recordCheck(&item, check)
	item.Action = ActionDrift
	item.Detail = fmt.Sprintf("carries no %s and %s", s.labels().PRPending, item.Detail)
	return item, true
}

// headTaskPattern pulls a task ID out of a branch name the coder prompt
// produced: loom/<ID>, or a republished revision loom/<ID>-rN. The ID itself
// must look like a ticket — a letter-led key, a dash, digits — so a hand-made
// branch such as loom/wip cannot become a backend lookup.
var headTaskPattern = regexp.MustCompile(`^loom/([A-Za-z][A-Za-z0-9]*-[0-9]+)(?:-r[0-9]+)?$`)

// taskIDsOf reduces a PR listing to the distinct task IDs it mentions, sorted
// so the report is stable. Revisions collapse onto their ticket: loom/X and
// loom/X-r2 are one ticket, tested once.
func taskIDsOf(prs []PR) []string {
	seen := map[string]bool{}
	var out []string
	for _, pr := range prs {
		m := headTaskPattern.FindStringSubmatch(pr.HeadRefName)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}
