package uniondebt

// The pr-pending APPLY pass: the half that decides when a marker goes ON.
//
// The clear pass (prpending_sweep.go) removes the marker when a ticket's pull
// request can actually land. This pass is its exact complement:
//
//	apply(id) <=> unionMerged(id) && !landed(id) && !hasMarker(id) && !Check(id).Passed()
//	clear(id) <=> hasMarker(id) && Check(id).Passed()
//
// Because Check is ONE predicate, read once per ticket per run, the two passes
// are provably disjoint: no sweep can clear a marker and re-apply it to the
// same ticket. That is the whole reason the apply half lives in this command
// rather than in a `derive` command of its own — two commands would ask the
// same question of two different PR snapshots, which is precisely how the
// halves come to disagree.
//
// The governing asymmetry is that this pass WRITES. Every ambiguity resolves
// toward not writing: an unreadable union branch, an unresolvable trunk and a
// possibly-truncated PR listing are all errors for the whole repo, never
// "nothing found, so everything is debt".

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Census is the derived pr-pending census for one repo: the ticket's six
// buckets, counted over the union branch the sweep actually read.
//
// The SHA matters as much as the counts. The union branch is rebuilt
// periodically — the 2026-09-22 rebuild took loomcli from 184 union-only tasks
// to 19 — so a count without the tip it was derived from is not evidence of
// anything.
type Census struct {
	Repo     string `json:"repo"`
	Union    string `json:"union_branch"`
	UnionSHA string `json:"union_sha,omitempty"`
	Trunk    string `json:"trunk"`
	// Merges is the number of distinct tasks the union carries that the trunk
	// does not — the population every bucket below is drawn from.
	Merges int `json:"union_merges"`
	// A: union-merged, unlabeled, not landed, no PR that can land. The only
	// bucket this pass writes.
	A int `json:"a_applied"`
	// B: labeled, union-merged, no open PR. The clear pass owns it.
	B int `json:"b_labeled_no_pr"`
	// C: union-merged, unlabeled, and its PR CAN land. Reported only — this
	// is the population the clear pass clears, and labeling it would start a
	// fight between the two passes.
	C int `json:"c_reported_mergeable"`
	// D: labeled but not in the union at all.
	D int `json:"d_labeled_not_in_union"`
	// E: labeled with an open PR. The clear pass owns it.
	E int `json:"e_labeled_open_pr"`
	// F: union-merged and already on the trunk. Reported; a marker on one of
	// these is stale, and removing it is the clear pass's job.
	F int `json:"f_landed"`
	// NoBranch: union-merged, but no ref answers to the task any more.
	NoBranch int `json:"no_branch"`
	// Excluded: union-merged but carrying a label that says a marker would be
	// a lie (superseded, abandoned, unreachable).
	Excluded int `json:"excluded"`
	// Applied and Skipped split bucket A by what actually happened: Skipped is
	// --pr-limit, and in a dry run Applied counts what WOULD be written.
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	Errors  int `json:"errors"`
}

// sweepPRDerive runs the apply pass over every swept repo and returns its
// rows. judged carries the tickets the clear pass already ruled on; every
// ticket this pass rules on is added to it, so the drift scan that follows
// cannot print a second verdict for the same ticket.
func (s *Sweeper) sweepPRDerive(ctx context.Context, rep *Report, judged prLedger) []Item {
	applied := 0
	var out []Item
	for _, repo := range s.opts.Contract.Repos() {
		if !s.wantRepo(repo) {
			continue
		}
		items, census := s.derivePRRepo(ctx, repo, judged, &applied)
		out = append(out, items...)
		if census != nil {
			rep.Census = append(rep.Census, *census)
		}
	}
	return out
}

// deriveInputs is everything one repo's pass reads before it judges anything.
// It is gathered up front, and a failure to gather ANY of it stops the repo
// with a single error row: a half-read repo would look like a repo that owes
// nothing.
type deriveInputs struct {
	li     LocalIntegration
	trunk  string
	merges []UnionMerge
	tip    string
	// openByID and allByID index pull requests by the task their head branch
	// names. The open listing decides buckets; the all-states listing only
	// names the closed or merged PR behind a "no open PR" verdict, so a row
	// says "#612 is CLOSED" instead of the much weaker "no PR".
	openByID map[string]PR
	allByID  map[string]PR
	// markered is the subset of the clear pass's ledger belonging to this
	// repo, snapshotted BEFORE this pass adds its own IDs.
	markered map[string]bool
}

func (s *Sweeper) derivePRRepo(ctx context.Context, repo string, judged prLedger, applied *int) ([]Item, *Census) {
	in, errItem, ok := s.deriveInputs(repo, judged)
	if !ok {
		return []Item{errItem}, nil
	}

	census := &Census{
		Repo: repo, Union: in.li.Branch, UnionSHA: in.tip, Trunk: in.trunk,
		Merges: len(in.merges),
	}
	inUnion := make(map[string]bool, len(in.merges))

	var out []Item
	for _, merge := range in.merges {
		inUnion[merge.TaskID] = true
		if in.markered[merge.TaskID] {
			// The clear pass already has a row for it. Count the bucket, add
			// no second row.
			s.countMarkered(in, merge.TaskID, census)
			continue
		}
		if _, elsewhere := judged[merge.TaskID]; elsewhere {
			continue
		}
		item, ok := s.deriveOne(ctx, repo, in, merge, applied, census)
		if !ok {
			continue
		}
		judged[merge.TaskID] = repo
		out = append(out, item)
	}

	for id := range in.markered {
		if !inUnion[id] {
			census.D++
		}
	}
	return out, census
}

// deriveInputs gathers one repo's inputs. The second result is the error row
// to report when the third is false.
func (s *Sweeper) deriveInputs(repo string, judged prLedger) (deriveInputs, Item, bool) {
	item := s.prItem("", repo)
	li, ok := s.opts.Contract.Lookup(repo)
	if !ok {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("no local_integration clone for source repo %q in the contract", repo)
		return deriveInputs{}, item, false
	}
	item.Clone, item.Union = li.Clone, li.Branch

	trunk, ok := s.opts.Contract.Trunk(repo)
	if !ok {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("no target_branch for source repo %q in the contract: "+
			"without a trunk nothing can be judged landed, and \"not landed\" is the direction that writes", repo)
		return deriveInputs{}, item, false
	}

	merges, err := s.probe.UnionMerges(li.Clone, li.Branch, trunk)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("enumerate %s merges in %s: %v", li.Branch, li.Clone, err)
		return deriveInputs{}, item, false
	}
	tip, err := s.probe.UnionTip(li.Clone, li.Branch)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("read %s tip in %s: %v", li.Branch, li.Clone, err)
		return deriveInputs{}, item, false
	}

	openByID, allByID, err := s.prIndexes(repo, li.Clone)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return deriveInputs{}, item, false
	}

	markered := map[string]bool{}
	for id, owner := range judged {
		if owner == repo {
			markered[id] = true
		}
	}
	return deriveInputs{
		li: li, trunk: trunk, merges: merges, tip: tip,
		openByID: openByID, allByID: allByID,
		markered: markered,
	}, Item{}, true
}

// prIndexes reads both PR listings BEFORE any classification. A listing that
// fails — or comes back at its cap and may therefore be truncated — must stop
// the whole repo, because a missing PR reads as "nothing can land", which here
// means WRITING a marker.
func (s *Sweeper) prIndexes(repo, clone string) (open, all map[string]PR, err error) {
	openPRs, err := s.checker().OpenPRs(clone)
	if err != nil {
		return nil, nil, fmt.Errorf("list open PRs for %s: %w", repo, err)
	}
	allPRs, err := s.checker().AllPRs(clone)
	if err != nil {
		return nil, nil, fmt.Errorf("list all PRs for %s: %w", repo, err)
	}
	return prsByTask(openPRs), prsByTask(allPRs), nil
}

// countMarkered places a ticket the clear pass owns into bucket B, E or F. It
// deliberately does NOT call Check: the clear pass has already read that
// predicate for this ticket, and reading it twice is how two passes come to
// hold two opinions.
func (s *Sweeper) countMarkered(in deriveInputs, id string, census *Census) {
	if landed, _, err := s.probe.Landed(in.li.Clone, in.trunk, id); err == nil && landed {
		census.F++
		return
	}
	if _, open := in.openByID[id]; open {
		census.E++
		return
	}
	census.B++
}

// deriveOne classifies one unlabeled, union-merged task and writes the marker
// when — and only when — the task's work is neither on the trunk nor able to
// get there today. The second result is false when there is nothing to report
// at all.
func (s *Sweeper) deriveOne(ctx context.Context, repo string, in deriveInputs, merge UnionMerge, applied *int, census *Census) (Item, bool) {
	detail, err := s.issues.Get(ctx, merge.TaskID)
	if err != nil || detail == nil {
		// The union may carry a branch naming a ticket this workspace does not
		// have (another workspace's ID, a hand-made branch). Nothing to report.
		return Item{}, false
	}
	if detail.SourceRepo != repo {
		// The same branch name can exist in two repos; only the repo that owns
		// the ticket may judge it.
		return Item{}, false
	}

	item := s.prItem(merge.TaskID, repo)
	item.Clone, item.Union, item.MergeSHA = in.li.Clone, in.li.Branch, merge.MergeSHA

	if label, skip := s.excludedBy(detail.Labels); skip {
		census.Excluded++
		item.Action = ActionSkipped
		item.Reason = "excluded-label"
		item.Detail = fmt.Sprintf("carries %s: a %s marker would assert this work is waiting to land, which it is not",
			label, s.labels().PRPending)
		return item, true
	}

	landed, why, err := s.probe.Landed(in.li.Clone, in.trunk, merge.TaskID)
	if row, done := s.landedRow(item, in, merge, landed, why, err, census); done {
		return row, true
	}
	return s.unlandedRow(ctx, repo, in, merge, item, why, applied, census), true
}

// landedRow answers the local-git half. The second result is true when that
// half is the whole answer: the branch is gone, the read failed, or the work
// is already on the trunk. Only a task that is genuinely absent from the trunk
// goes on to the PR test.
func (s *Sweeper) landedRow(item Item, in deriveInputs, merge UnionMerge, landed bool, why string, err error, census *Census) (Item, bool) {
	switch {
	case errors.Is(err, errNoTaskRef):
		census.NoBranch++
		item.Class, item.Action = ClassPRNoBranch, ActionReported
		item.Reason = "no-branch"
		item.Detail = fmt.Sprintf("%s is in %s but no ref answers to it in %s: %s covers that, not %s",
			merge.TaskID, in.li.Branch, in.li.Clone, s.labels().Unreachable, s.labels().PRPending)
		return item, true
	case err != nil:
		census.Errors++
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item, true
	case landed:
		census.F++
		item.Class, item.Action, item.Landed = ClassPRLanded, ActionReported, true
		item.Detail = why
		return item, true
	}
	return item, false
}

// unlandedRow applies the shared PR predicate to work the trunk does not have.
// Passing it means the pull request CAN land, which is the clear pass's
// condition — so that ticket is reported and never labeled.
func (s *Sweeper) unlandedRow(ctx context.Context, repo string, in deriveInputs, merge UnionMerge, item Item, why string, applied *int, census *Census) Item {
	req, errItem, ok := s.prRequest(&item, repo, merge.TaskID, false)
	if !ok {
		census.Errors++
		return errItem
	}
	check, err := s.checker().Check(req)
	if err != nil {
		census.Errors++
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item
	}
	recordCheck(&item, check)

	if check.Passed() {
		census.C++
		item.Class, item.Action = ClassPRUnionUnlanded, ActionReported
		item.Detail = fmt.Sprintf("not on %s yet, but %s — reported, not labeled, because the clear pass would remove the marker next run",
			in.trunk, check.Detail)
		return item
	}
	return s.applyMarker(ctx, in, merge, item, why, applied, census)
}

// applyMarker performs the one write this pass makes.
func (s *Sweeper) applyMarker(ctx context.Context, in deriveInputs, merge UnionMerge, item Item, why string, applied *int, census *Census) Item {
	census.A++
	item.Class = ClassPRUnionDebt
	item.Detail = fmt.Sprintf("%s, and %s", why, item.Detail)
	if pr, ok := in.allByID[merge.TaskID]; ok && item.PR == 0 {
		// Name the closed or merged PR behind the "no open PR" verdict: it is
		// the difference between "no PR" and "#612 was closed unmerged".
		item.PR = pr.Number
		item.Detail += fmt.Sprintf(" (#%d is %s)", pr.Number, pr.State)
	}

	if s.opts.PRLimit > 0 && *applied >= s.opts.PRLimit {
		census.Skipped++
		item.Action = ActionSkipped
		item.Reason = "pr-limit"
		item.Detail = fmt.Sprintf("--pr-limit %d reached; %s", s.opts.PRLimit, item.Detail)
		return item
	}

	item.Action = ActionApplied
	census.Applied++
	*applied++
	s.apply(ctx, &item, func() error { return s.applyPRPending(ctx, merge, item) })
	return item
}

// excludedBy reports the label that makes a pr-pending marker a false
// statement about this ticket. Each one says the work is NOT simply waiting to
// land: it was rebuilt, deliberately abandoned, or has no branch at all.
func (s *Sweeper) excludedBy(labels []string) (string, bool) {
	lbl := s.labels()
	excluded := map[string]bool{
		lbl.Superseded:  true,
		lbl.Abandoned:   true,
		lbl.Unreachable: true,
	}
	for _, label := range labels {
		if excluded[label] {
			return label, true
		}
	}
	return "", false
}

// applyPRPending comments on the ticket and then adds the marker. The comment
// goes FIRST, exactly as clearPRPending and retire order it: a label with no
// comment erases the only record of why, while a comment with no label is a
// harmless duplicate next run.
func (s *Sweeper) applyPRPending(ctx context.Context, merge UnionMerge, item Item) error {
	if _, err := s.issues.AddComment(ctx, backend.CommentAddParams{
		IssueID: item.OriginID,
		Text:    prAppliedComment(merge, item, s.labels()),
	}); err != nil {
		return fmt.Errorf("comment on %s: %w", item.OriginID, err)
	}
	marker := s.labels().PRPending
	if err := s.issues.AddLabel(ctx, item.OriginID, marker); err != nil {
		return fmt.Errorf("add %s to %s: %w", marker, item.OriginID, err)
	}
	return nil
}

func prAppliedComment(merge UnionMerge, item Item, lbl LabelSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "union-debt sweep: %s marker applied — this code is in the local build and not upstream.\n\n", lbl.PRPending)
	fmt.Fprintf(&b, "Repo:      %s\n", item.Repo)
	fmt.Fprintf(&b, "Clone:     %s\n", item.Clone)
	fmt.Fprintf(&b, "Union:     %s\n", item.Union)
	fmt.Fprintf(&b, "Merge:     %s\n", merge.MergeSHA)
	if merge.ParentSHA != "" {
		fmt.Fprintf(&b, "Merged:    %s (the side that carried this task in)\n", merge.ParentSHA)
	}
	fmt.Fprintf(&b, "Subject:   %s\n", merge.Subject)
	if item.PR != 0 {
		fmt.Fprintf(&b, "PR:        #%d\n", item.PR)
	}
	if item.Mergeable != "" {
		fmt.Fprintf(&b, "Mergeable: %s\n", item.Mergeable)
	}
	if item.Chain != "" {
		fmt.Fprintf(&b, "Chain:     %s\n", item.Chain)
	}
	fmt.Fprintf(&b, "Reason:    %s\n", item.Detail)
	fmt.Fprintf(&b, "Checked:   %s (no fetch — refs as they stood in the clone)\n", item.ProbedAt)
	fmt.Fprintf(&b, "\nThe marker is derived, not hand-applied: it comes off automatically once a\n")
	fmt.Fprintf(&b, "pull request for this branch is %s and its base chain reaches the trunk.\n", MergeableYes)
	return b.String()
}

// prsByTask indexes a PR listing by the task its head branch names, keeping
// the highest-numbered PR when a task has several (the newest attempt). A head
// that is not a task branch is ignored.
func prsByTask(prs []PR) map[string]PR {
	byID := map[string]PR{}
	for _, pr := range prs {
		m := headTaskPattern.FindStringSubmatch(pr.HeadRefName)
		if m == nil {
			continue
		}
		if prev, ok := byID[m[1]]; ok && prev.Number > pr.Number {
			continue
		}
		byID[m[1]] = pr
	}
	return byID
}
