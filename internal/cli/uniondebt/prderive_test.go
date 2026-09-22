package uniondebt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- helpers ---

// derivedIssue is a ticket the union carries and nobody has labeled.
func derivedIssue(id string, labels ...string) backend.IssueData {
	if labels == nil {
		labels = []string{"delivered"}
	}
	return backend.IssueData{ID: id, Status: "closed", SourceRepo: "loomcli", Labels: labels}
}

// unionOf turns task IDs into a union enumeration, one merge each.
func unionOf(ids ...string) []UnionMerge {
	var out []UnionMerge
	for i, id := range ids {
		out = append(out, UnionMerge{
			TaskID:    id,
			MergeSHA:  "merge" + id,
			ParentSHA: "parent" + id,
			Subject:   "union: PR #" + string(rune('1'+i)) + " (loom/" + id + ")",
		})
	}
	return out
}

// runDerive sweeps with the apply pass on, restricted to loomcli so the
// fixture's second repo does not answer with the same stubbed union.
func runDerive(t *testing.T, f *fakeBackend, gh githubClient, p *stubProber, opts Options) *Report {
	t.Helper()
	opts.PRDerive = true
	if opts.Repos == nil {
		opts.Repos = []string{"loomcli"}
	}
	if opts.Contract == nil {
		opts.Contract = testContract(t)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC) }
	}
	s := NewSweeper(f, p, opts)
	s.pr = &PRChecker{gh: gh, git: stubGit{}, attempts: 3, backoff: time.Millisecond, sleep: func(time.Duration) {}}
	rep, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func itemFor(t *testing.T, rep *Report, id string) Item {
	t.Helper()
	var found []Item
	for _, it := range rep.Items {
		if it.OriginID == id {
			found = append(found, it)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one row for %s, got %d: %+v", id, len(found), rep.Items)
	}
	return found[0]
}

func onlyCensus(t *testing.T, rep *Report) Census {
	t.Helper()
	if len(rep.Census) != 1 {
		t.Fatalf("expected one census block, got %d: %+v", len(rep.Census), rep.Census)
	}
	return rep.Census[0]
}

// --- the apply table ---

// TestDerive_AppliesOnlyWhenNothingCanLand is the complement of
// TestSweep_PRPendingClearTest: every row the clear test would refuse to clear
// is a row this pass applies, and the ONE row it would clear is reported and
// left alone.
func TestDerive_AppliesOnlyWhenNothingCanLand(t *testing.T) {
	cases := []struct {
		name       string
		prs        []PR
		all        []PR
		wantAction Action
		wantClass  Class
		wantDetail string
	}{
		{
			name:       "no PR at all",
			wantAction: ActionApplied,
			wantClass:  ClassPRUnionDebt,
		},
		{
			name:       "only a closed PR",
			all:        []PR{{Number: 612, State: "CLOSED", HeadRefName: "loom/PUPPET-11", BaseRefName: "v5"}},
			wantAction: ActionApplied,
			wantClass:  ClassPRUnionDebt,
			wantDetail: "#612 is CLOSED",
		},
		{
			name:       "merged PR whose work is not on the trunk",
			all:        []PR{{Number: 613, State: "MERGED", HeadRefName: "loom/PUPPET-11", BaseRefName: "v5"}},
			wantAction: ActionApplied,
			wantClass:  ClassPRUnionDebt,
			wantDetail: "#613 is MERGED",
		},
		{
			name:       "open but conflicting",
			prs:        []PR{{Number: 614, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-11", BaseRefName: "v5"}},
			wantAction: ActionApplied,
			wantClass:  ClassPRUnionDebt,
		},
		{
			name:       "open, mergeable, chain reaches the trunk",
			prs:        []PR{{Number: 615, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-11", BaseRefName: "v5"}},
			wantAction: ActionReported,
			wantClass:  ClassPRUnionUnlanded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeBackend()
			f.add(derivedIssue("PUPPET-11"))
			rep := runDerive(t, f, &fakeGitHub{prs: tc.prs, all: tc.all},
				&stubProber{merges: unionOf("PUPPET-11")}, Options{})

			item := itemFor(t, rep, "PUPPET-11")
			if item.Action != tc.wantAction || item.Class != tc.wantClass {
				t.Errorf("action/class = %s/%s, want %s/%s (%s)", item.Action, item.Class, tc.wantAction, tc.wantClass, item.Detail)
			}
			if item.MergeSHA != "mergePUPPET-11" {
				t.Errorf("merge_sha = %q, want the union merge that carries the task", item.MergeSHA)
			}
			if tc.wantDetail != "" && !strings.Contains(item.Detail, tc.wantDetail) {
				t.Errorf("detail = %q, want it to name the PR behind the verdict (%q)", item.Detail, tc.wantDetail)
			}

			wantWrite := tc.wantAction == ActionApplied
			if got := len(f.added) == 1 && f.added[0] == "PUPPET-11:pr-pending"; got != wantWrite {
				t.Errorf("labels added = %v, want written=%v", f.added, wantWrite)
			}
		})
	}
}

func TestDerive_LandedIsReportedNotLabeled(t *testing.T) {
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-97"))
	p := &stubProber{
		merges: unionOf("PUPPET-97"),
		landed: map[string]string{"PUPPET-97": "origin/v5 contains origin/loom/PUPPET-97 (abc1234)"},
	}
	rep := runDerive(t, f, &fakeGitHub{}, p, Options{})

	item := itemFor(t, rep, "PUPPET-97")
	if item.Action != ActionReported || item.Class != ClassPRLanded || !item.Landed {
		t.Errorf("item = %+v, want a reported pr-landed row with landed=true", item)
	}
	if len(f.added) != 0 {
		t.Errorf("labels added = %v, want none: the work already shipped", f.added)
	}
	if c := onlyCensus(t, rep); c.F != 1 {
		t.Errorf("census F = %d, want 1", c.F)
	}
}

// TestDerive_MarkeredLandedTicketIsBucketF covers the stale-marker case: the
// apply pass counts it and writes nothing, and the clear pass does NOT remove
// the marker, because its rule is the narrower one.
func TestDerive_MarkeredLandedTicketIsBucketF(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-97", "loomcli"))
	p := &stubProber{
		merges: unionOf("PUPPET-97"),
		landed: map[string]string{"PUPPET-97": "origin/v5 contains it"},
	}
	rep := runDerive(t, f, &fakeGitHub{}, p, Options{})

	if c := onlyCensus(t, rep); c.F != 1 || c.A != 0 {
		t.Errorf("census = %+v, want F=1 A=0", c)
	}
	if len(f.removed) != 0 || len(f.added) != 0 {
		t.Errorf("writes: removed=%v added=%v, want none", f.removed, f.added)
	}
	// One row, from the clear pass. Never two verdicts about one ticket.
	itemFor(t, rep, "PUPPET-97")
}

func TestDerive_SkipsTicketsTheApplyPassMayNotJudge(t *testing.T) {
	cases := []struct {
		name    string
		labels  []string
		wantRow bool
		wantAct Action
	}{
		{name: "already carries the marker", labels: []string{defaultLabels.PRPending}, wantRow: true, wantAct: ActionSkipped},
		{name: "superseded", labels: []string{defaultLabels.Superseded}, wantRow: true, wantAct: ActionSkipped},
		{name: "abandoned", labels: []string{defaultLabels.Abandoned}, wantRow: true, wantAct: ActionSkipped},
		{name: "unreachable", labels: []string{defaultLabels.Unreachable}, wantRow: true, wantAct: ActionSkipped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeBackend()
			f.add(backend.IssueData{ID: "PUPPET-11", Status: "closed", SourceRepo: "loomcli", Labels: tc.labels})
			rep := runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11")}, Options{})

			if len(f.added) != 0 {
				t.Errorf("labels added = %v, want none", f.added)
			}
			if item := itemFor(t, rep, "PUPPET-11"); item.Action != tc.wantAct {
				t.Errorf("action = %s, want %s", item.Action, tc.wantAct)
			}
		})
	}
}

func TestDerive_IgnoresTicketsItDoesNotOwn(t *testing.T) {
	f := newFakeBackend()
	// Present but belonging to another repo, and absent altogether.
	f.add(backend.IssueData{ID: "PUPPET-11", Status: "closed", SourceRepo: "meta-harness"})
	rep := runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11", "PUPPET-999")}, Options{})

	if len(rep.Items) != 0 {
		t.Errorf("items = %+v, want none: one ticket belongs to another repo, the other does not exist", rep.Items)
	}
	if len(f.added) != 0 {
		t.Errorf("labels added = %v, want none", f.added)
	}
}

func TestDerive_NoBranchIsReported(t *testing.T) {
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-11"))
	p := &stubProber{
		merges:     unionOf("PUPPET-11"),
		landedErrs: map[string]error{"PUPPET-11": errNoTaskRef},
	}
	rep := runDerive(t, f, &fakeGitHub{}, p, Options{})

	item := itemFor(t, rep, "PUPPET-11")
	if item.Action != ActionReported || item.Class != ClassPRNoBranch {
		t.Errorf("item = %+v, want a reported pr-no-branch row", item)
	}
	if len(f.added) != 0 {
		t.Errorf("labels added = %v, want none: there is no branch to land", f.added)
	}
	if c := onlyCensus(t, rep); c.NoBranch != 1 {
		t.Errorf("census NoBranch = %d, want 1", c.NoBranch)
	}
}

func TestDerive_DryRunWritesNothing(t *testing.T) {
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-11"))
	rep := runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11")}, Options{DryRun: true})

	item := itemFor(t, rep, "PUPPET-11")
	if item.Action != ActionApplied || !item.DryRun {
		t.Errorf("item = %+v, want the same applied row, flagged dry_run", item)
	}
	if len(f.added) != 0 || len(f.comments) != 0 {
		t.Errorf("writes in a dry run: added=%v comments=%v", f.added, f.comments)
	}
}

func TestDerive_PRLimitStopsWritingButKeepsReporting(t *testing.T) {
	f := newFakeBackend()
	for _, id := range []string{"PUPPET-11", "PUPPET-12", "PUPPET-13"} {
		f.add(derivedIssue(id))
	}
	rep := runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11", "PUPPET-12", "PUPPET-13")},
		Options{PRLimit: 2})

	if len(f.added) != 2 {
		t.Errorf("labels added = %v, want exactly 2 (--pr-limit)", f.added)
	}
	last := itemFor(t, rep, "PUPPET-13")
	if last.Action != ActionSkipped || !strings.Contains(last.Detail, "--pr-limit 2 reached") {
		t.Errorf("third item = %+v, want skipped naming the limit", last)
	}
	if c := onlyCensus(t, rep); c.A != 3 || c.Applied != 2 || c.Skipped != 1 {
		t.Errorf("census = %+v, want A=3 applied=2 skipped=1: the census counts what the rule found, not what the limit allowed", c)
	}
}

// TestDerive_ListingFailureStopsTheRepo covers the two ways a repo's inputs go
// missing. Both must stop the whole repo with one error row and write nothing:
// a truncated or failed listing turns a real PR into "no PR", which here means
// writing a label.
func TestDerive_ListingFailureStopsTheRepo(t *testing.T) {
	t.Run("all-states listing fails", func(t *testing.T) {
		f := newFakeBackend()
		f.add(derivedIssue("PUPPET-11"))
		gh := &fakeGitHub{allErr: guardTruncation("/clones/loomcli", "all", 2000, allPRListLimit)}
		rep := runDerive(t, f, gh, &stubProber{merges: unionOf("PUPPET-11")}, Options{PRDrift: false})

		if rep.Errors != 1 || len(rep.Items) != 1 {
			t.Fatalf("report = %+v, want exactly one error row for the repo", rep.Items)
		}
		if !strings.Contains(rep.Items[0].ErrMessage, "truncated") {
			t.Errorf("error = %q, want it to name truncation", rep.Items[0].ErrMessage)
		}
		if len(f.added) != 0 {
			t.Errorf("labels added = %v, want none", f.added)
		}
	})

	t.Run("union range unreadable", func(t *testing.T) {
		f := newFakeBackend()
		f.add(derivedIssue("PUPPET-11"))
		p := &stubProber{mergesErr: errUnionRange}
		rep := runDerive(t, f, &fakeGitHub{}, p, Options{})

		if rep.Errors != 1 {
			t.Fatalf("report = %+v, want one error row", rep.Items)
		}
		if len(f.added) != 0 {
			t.Errorf("labels added = %v, want none: an unreadable union is not an empty union", f.added)
		}
		if len(rep.Census) != 0 {
			t.Errorf("census = %+v, want none for a repo that was not enumerated", rep.Census)
		}
	})
}

func TestGuardTruncation(t *testing.T) {
	if err := guardTruncation("/clones/loomcli", "all", 729, allPRListLimit); err != nil {
		t.Errorf("a listing under the cap must pass: %v", err)
	}
	if err := guardTruncation("/clones/loomcli", "open", prListLimit, prListLimit); err == nil {
		t.Error("a listing AT the cap must fail: gh gives no more-results signal")
	}
}

// TestDerive_MarkerNameComesFromTheContract is the requirement that both
// halves read ONE source for the label: a workspace that renames the marker
// must see the renamed one applied, not the default.
func TestDerive_MarkerNameComesFromTheContract(t *testing.T) {
	c, err := LoadContract(writeContract(t, withLabels("  labels:\n    pr_pending: waiting-on-pr\n")))
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-11"))
	runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11")}, Options{Contract: c})

	if len(f.added) != 1 || f.added[0] != "PUPPET-11:waiting-on-pr" {
		t.Errorf("labels added = %v, want the contract's waiting-on-pr", f.added)
	}
}

// TestDerive_CommentPrecedesLabel: a label with no comment erases the only
// record of why the marker appeared, so the comment goes first and a failing
// label still leaves it behind.
func TestDerive_CommentPrecedesLabel(t *testing.T) {
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-11"))
	f.addLabelErr = errors.New("backend down")
	rep := runDerive(t, f, &fakeGitHub{}, &stubProber{merges: unionOf("PUPPET-11")}, Options{})

	want := []string{"comment:PUPPET-11", "add:PUPPET-11:pr-pending"}
	if strings.Join(f.ops, ",") != strings.Join(want, ",") {
		t.Errorf("ops = %v, want %v", f.ops, want)
	}
	if item := itemFor(t, rep, "PUPPET-11"); item.Action != ActionError {
		t.Errorf("action = %s, want error when the label write fails", item.Action)
	}
	body := f.comments["PUPPET-11"][0]
	for _, want := range []string{"mergePUPPET-11", "parentPUPPET-11", "pr-pending marker applied"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment missing %q:\n%s", want, body)
		}
	}
}

// TestRunNeverAppliesAndClearsSameTicket is the ticket's central invariant,
// encoded: one run, all six buckets, both optional passes on. No ticket may be
// both cleared and applied, and no ticket may appear twice with two verdicts.
func TestRunNeverAppliesAndClearsSameTicket(t *testing.T) {
	f := newFakeBackend()
	var ids []string
	var prs []PR
	number := 600

	// Unlabeled, union-merged: half with a mergeable PR (reported), half with
	// nothing that can land (applied).
	for _, id := range []string{"PUPPET-11", "PUPPET-12", "PUPPET-13", "PUPPET-14"} {
		f.add(derivedIssue(id))
		ids = append(ids, id)
	}
	for _, id := range []string{"PUPPET-13", "PUPPET-14"} {
		number++
		prs = append(prs, PR{Number: number, State: "OPEN", Mergeable: MergeableYes,
			HeadRefName: "loom/" + id, BaseRefName: "v5"})
	}
	// Labeled: one with a mergeable PR (the clear pass clears it), one with
	// none (it keeps the marker), one already on the trunk (bucket F).
	for _, id := range []string{"PUPPET-21", "PUPPET-22", "PUPPET-23"} {
		f.add(prIssue(id, "loomcli"))
		ids = append(ids, id)
	}
	number++
	prs = append(prs, PR{Number: number, State: "OPEN", Mergeable: MergeableYes,
		HeadRefName: "loom/PUPPET-21", BaseRefName: "v5"})
	// Labeled but not in the union at all: bucket D.
	f.add(prIssue("PUPPET-31", "loomcli"))

	p := &stubProber{
		merges: unionOf(append(ids, "PUPPET-41")...), // PUPPET-41 is on no board
		landed: map[string]string{"PUPPET-23": "origin/v5 contains it"},
	}
	rep := runDerive(t, f, &fakeGitHub{prs: prs}, p, Options{PRDrift: true})

	seen := map[string]Action{}
	cleared, applied := map[string]bool{}, map[string]bool{}
	for _, it := range rep.Items {
		if it.OriginID == "" {
			continue
		}
		if prev, dup := seen[it.OriginID]; dup {
			t.Errorf("%s has two rows in one report: %s and %s", it.OriginID, prev, it.Action)
		}
		seen[it.OriginID] = it.Action
		switch it.Action {
		case ActionCleared:
			cleared[it.OriginID] = true
		case ActionApplied:
			applied[it.OriginID] = true
		}
	}
	for id := range cleared {
		if applied[id] {
			t.Errorf("%s was both cleared and applied in one run", id)
		}
	}
	if !cleared["PUPPET-21"] {
		t.Errorf("PUPPET-21 should have been cleared; rows: %+v", seen)
	}
	if !applied["PUPPET-11"] || !applied["PUPPET-12"] {
		t.Errorf("PUPPET-11/12 should have been applied; rows: %+v", seen)
	}

	c := onlyCensus(t, rep)
	if c.A != 2 || c.C != 2 || c.D != 1 || c.E != 1 || c.F != 1 || c.B != 1 {
		t.Errorf("census = %+v, want A=2 B=1 C=2 D=1 E=1 F=1", c)
	}
}

func TestDerive_OffByDefault(t *testing.T) {
	f := newFakeBackend()
	f.add(derivedIssue("PUPPET-11"))
	p := &stubProber{merges: unionOf("PUPPET-11")}
	rep := run(t, f, p, Options{Repos: []string{"loomcli"}})

	if len(rep.Items) != 0 || len(rep.Census) != 0 {
		t.Errorf("report = %+v / %+v, want an untouched board: library callers opt in", rep.Items, rep.Census)
	}
	if len(p.landedCalls) != 0 {
		t.Errorf("Landed calls = %v, want none", p.landedCalls)
	}
}
