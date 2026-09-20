package uniondebt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- fakes ---

// fakeGitHub answers the two GitHub reads the pr-pending test makes. No
// process is ever started: the whole point of githubClient is that these tests
// never reach the network.
type fakeGitHub struct {
	prs []PR
	// views is the queue of mergeable values a re-poll of one PR sees, in
	// order. The last value repeats once the queue is drained, so a PR that is
	// permanently UNKNOWN needs a single entry.
	views     map[int][]string
	listErr   error
	listCalls int
	viewCalls []int
}

func (f *fakeGitHub) OpenPRs(string) ([]PR, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]PR(nil), f.prs...), nil
}

func (f *fakeGitHub) Mergeable(_ string, number int) (string, error) {
	f.viewCalls = append(f.viewCalls, number)
	q := f.views[number]
	if len(q) == 0 {
		return MergeableUnknown, nil
	}
	if len(q) > 1 {
		f.views[number] = q[1:]
	}
	return q[0], nil
}

// stubGit answers the one git read the pr-pending test makes: the revision
// for-each-ref. Output is keyed by the glob, so a test that declares nothing
// gets "no revisions" and the unsuffixed branch name.
type stubGit struct {
	refs map[string]string
}

func (s stubGit) Run(_ string, args ...string) (string, int, error) {
	if len(args) == 3 && args[0] == "for-each-ref" {
		return s.refs[args[2]], 0, nil
	}
	return "", 0, nil
}

// --- helpers ---

func prIssue(id, repo string, labels ...string) backend.IssueData {
	if len(labels) == 0 {
		labels = []string{defaultLabels.PRPending}
	}
	return backend.IssueData{ID: id, Status: "in_progress", SourceRepo: repo, Labels: labels}
}

// runPR sweeps with the real PRChecker wired to fakes, so the test exercises
// the ref resolution, the re-poll and the chain walk exactly as production
// runs them — only the two process boundaries are replaced.
func runPR(t *testing.T, f *fakeBackend, gh githubClient, git gitRunner, opts Options) *Report {
	t.Helper()
	if opts.Contract == nil {
		opts.Contract = testContract(t)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) }
	}
	if git == nil {
		git = stubGit{}
	}
	s := NewSweeper(f, &stubProber{}, opts)
	s.pr = &PRChecker{
		gh:       gh,
		git:      git,
		attempts: 3,
		backoff:  time.Millisecond,
		// No real sleeping: the backoff is a production concern, and a unit
		// test that waits tens of seconds for it stops being run.
		sleep: func(time.Duration) {},
	}
	rep, err := s.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// --- the clear test ---

// TestSweep_PRPendingClearTest is the table the ticket's acceptance criteria
// name. Exactly one row clears the marker; every other outcome keeps it,
// because a marker wrongly cleared says work can land when it cannot.
func TestSweep_PRPendingClearTest(t *testing.T) {
	cases := []struct {
		name string
		prs  []PR
		// views drives the UNKNOWN re-poll, keyed by PR number.
		views      map[int][]string
		wantAction Action
		wantClass  Class
		wantReason string
		wantChain  string
	}{
		{
			name:       "mergeable straight onto the trunk clears",
			prs:        []PR{{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"}},
			wantAction: ActionCleared,
			wantClass:  ClassPRMergeable,
			wantChain:  "#612 MERGEABLE -> v5",
		},
		{
			name:       "UNKNOWN that resolves to MERGEABLE on re-poll clears",
			prs:        []PR{{Number: 612, State: "OPEN", Mergeable: MergeableUnknown, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"}},
			views:      map[int][]string{612: {MergeableYes}},
			wantAction: ActionCleared,
			wantClass:  ClassPRMergeable,
			wantChain:  "#612 MERGEABLE -> v5",
		},
		{
			name:       "UNKNOWN throughout keeps the marker",
			prs:        []PR{{Number: 612, State: "OPEN", Mergeable: MergeableUnknown, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"}},
			views:      map[int][]string{612: {MergeableUnknown}},
			wantAction: ActionSkipped,
			wantClass:  ClassPRUnknown,
			wantReason: "mergeability-unknown",
		},
		{
			name:       "CONFLICTING keeps the marker",
			prs:        []PR{{Number: 612, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"}},
			wantAction: ActionSkipped,
			wantClass:  ClassPRConflicting,
			wantReason: "not-mergeable",
		},
		{
			name:       "no PR keeps the marker",
			prs:        []PR{{Number: 700, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-999", BaseRefName: "v5"}},
			wantAction: ActionSkipped,
			wantClass:  ClassPRNone,
			wantReason: "no-pr",
		},
		{
			name: "a stacked chain that is mergeable all the way to the trunk clears",
			prs: []PR{
				{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "loom/PUPPET-0"},
				{Number: 598, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-0", BaseRefName: "v5"},
			},
			wantAction: ActionCleared,
			wantClass:  ClassPRMergeable,
			wantChain:  "#612 MERGEABLE -> #598 MERGEABLE -> v5",
		},
		{
			name: "one CONFLICTING link in the chain keeps the marker",
			prs: []PR{
				{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "loom/PUPPET-0"},
				{Number: 598, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-0", BaseRefName: "v5"},
			},
			wantAction: ActionSkipped,
			wantClass:  ClassPRConflicting,
			wantReason: "not-mergeable",
		},
		{
			name: "a chain ending on a base with no open PR keeps the marker",
			prs: []PR{
				{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "loom/PUPPET-0"},
			},
			wantAction: ActionSkipped,
			wantClass:  ClassPRBaseUnreachable,
			wantReason: "base-no-pr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeBackend()
			f.add(prIssue("PUPPET-1", "loomcli"))
			gh := &fakeGitHub{prs: tc.prs, views: tc.views}

			item := onlyItem(t, runPR(t, f, gh, nil, Options{}))

			if item.Action != tc.wantAction || item.Class != tc.wantClass {
				t.Fatalf("item = %+v, want action %s class %s", item, tc.wantAction, tc.wantClass)
			}
			if item.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", item.Reason, tc.wantReason)
			}
			if tc.wantChain != "" && item.Chain != tc.wantChain {
				t.Errorf("Chain = %q, want %q", item.Chain, tc.wantChain)
			}

			cleared := tc.wantAction == ActionCleared
			removed := len(f.removed) > 0
			if removed != cleared {
				t.Fatalf("label removals = %v, want cleared=%v", f.removed, cleared)
			}
			if !cleared {
				if len(f.comments["PUPPET-1"]) != 0 {
					t.Errorf("a kept marker must not be commented on: %v", f.comments["PUPPET-1"])
				}
				return
			}
			if f.removed[0] != "PUPPET-1:"+defaultLabels.PRPending {
				t.Errorf("removed %v, want the pr-pending marker off PUPPET-1", f.removed)
			}
			if len(f.added) != 0 {
				t.Errorf("clearing must stamp nothing, got %v", f.added)
			}
		})
	}
}

// TestSweep_PRPendingUnknownIsRepolledThenGivesUp pins the poll itself: UNKNOWN
// is re-read, bounded, and never read as mergeable.
func TestSweep_PRPendingUnknownIsRepolledThenGivesUp(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{
		prs:   []PR{{Number: 612, State: "OPEN", Mergeable: MergeableUnknown, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"}},
		views: map[int][]string{612: {MergeableUnknown}},
	}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{}))

	if item.Action != ActionSkipped || item.Reason != "mergeability-unknown" {
		t.Fatalf("item = %+v, want skipped/mergeability-unknown", item)
	}
	// attempts=3 means the first read plus two re-reads.
	if len(gh.viewCalls) != 2 {
		t.Errorf("re-polled %d time(s), want 2 bounded re-reads", len(gh.viewCalls))
	}
	if !strings.Contains(item.Detail, MergeableUnknown) {
		t.Errorf("detail should name the value that kept the marker: %q", item.Detail)
	}
}

// TestSweep_PRPendingCycleIsAnError: a base chain that loops is a malformed
// graph, not a verdict. It must be reported as an error and keep the marker —
// never followed until the depth cap quietly calls it unreachable.
func TestSweep_PRPendingCycleIsAnError(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "stack/a"},
		{Number: 598, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "stack/a", BaseRefName: "loom/PUPPET-1"},
	}}

	rep := runPR(t, f, gh, nil, Options{})
	item := onlyItem(t, rep)

	if item.Action != ActionError {
		t.Fatalf("item = %+v, want an error item", item)
	}
	if rep.Errors != 1 {
		t.Errorf("Errors = %d, want 1", rep.Errors)
	}
	if !strings.Contains(item.ErrMessage, "cycle") {
		t.Errorf("error should name the cycle, got %q", item.ErrMessage)
	}
	if len(f.removed) != 0 {
		t.Errorf("a cycle must keep the marker, got removals %v", f.removed)
	}
}

// TestSweep_PRPendingRevisionAwareHead: a republished branch (loom/<ID>-rN) is
// the branch that answers to the ticket today, and the unsuffixed name may
// still carry a stale PR. The revision wins, exactly as the union probe
// resolves refs.
func TestSweep_PRPendingRevisionAwareHead(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 500, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"},
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1-r2", BaseRefName: "v5"},
	}}
	git := stubGit{refs: map[string]string{
		"refs/remotes/origin/loom/PUPPET-1-r*": "origin/loom/PUPPET-1-r2\n",
	}}

	item := onlyItem(t, runPR(t, f, gh, git, Options{}))

	if item.Action != ActionCleared || item.PR != 612 {
		t.Fatalf("item = %+v, want the -r2 revision's PR #612 to clear", item)
	}
}

// TestSweep_PRPendingRevisionFromPRsWhenCloneHasNone: a revision published
// after this clone last fetched has no local ref (the package never fetches),
// so the PR listing is the second source of truth for the head branch.
func TestSweep_PRPendingRevisionFromPRsWhenCloneHasNone(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1-r3", BaseRefName: "v5"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{}))

	if item.Action != ActionCleared || item.PR != 612 {
		t.Fatalf("item = %+v, want the -r3 revision found through the PR listing", item)
	}
}

// TestSweep_PRPendingDryRunWritesNothing: --dry-run must report the would-clear
// set and leave the board untouched.
func TestSweep_PRPendingDryRunWritesNothing(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{DryRun: true}))

	if item.Action != ActionCleared || !item.DryRun {
		t.Fatalf("item = %+v, want a dry-run cleared item", item)
	}
	if len(f.removed) != 0 || len(f.comments) != 0 {
		t.Errorf("dry run wrote: removals %v comments %v", f.removed, f.comments)
	}
}

// TestSweep_PRPendingCommentNamesTheEvidence: every clear leaves the PR, the
// value GitHub reported and the chain walked, so the decision can be audited
// without re-running the sweep.
func TestSweep_PRPendingCommentNamesTheEvidence(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "loom/PUPPET-0"},
		{Number: 598, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-0", BaseRefName: "v5"},
	}}

	runPR(t, f, gh, nil, Options{})

	got := f.comments["PUPPET-1"]
	if len(got) != 1 {
		t.Fatalf("comments = %v, want exactly one", got)
	}
	for _, want := range []string{"pr-pending", "#612", MergeableYes, "#612 MERGEABLE -> #598 MERGEABLE -> v5", "loomcli"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("comment missing %q:\n%s", want, got[0])
		}
	}
}

// TestSweep_PRPendingUsesTheContractLabel: the marker is vocabulary, not a
// string literal. A workspace that renames it must have the renamed label
// read, tested and removed.
func TestSweep_PRPendingUsesTheContractLabel(t *testing.T) {
	c, err := LoadContract(writeContract(t, withLabels("  labels:\n    pr_pending: awaiting-merge\n")))
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli", "awaiting-merge"))
	// A ticket carrying the DEFAULT name must be invisible to this sweep.
	f.add(prIssue("PUPPET-2", "loomcli", defaultLabels.PRPending))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{Contract: c}))

	if item.OriginID != "PUPPET-1" || item.Action != ActionCleared {
		t.Fatalf("item = %+v, want PUPPET-1 cleared", item)
	}
	if len(f.removed) != 1 || f.removed[0] != "PUPPET-1:awaiting-merge" {
		t.Errorf("removed %v, want the contract's awaiting-merge label", f.removed)
	}
}

// TestSweep_PRPendingTrunkComesFromTheContract: loomcli's trunk is v5. A chain
// that terminates at `main` reaches nothing here, and must keep the marker
// rather than clear it on an assumed trunk name.
func TestSweep_PRPendingTrunkComesFromTheContract(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "main"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{}))

	if item.Action != ActionSkipped || item.Class != ClassPRBaseUnreachable {
		t.Fatalf("item = %+v, want the marker kept: main is not loomcli's trunk", item)
	}
}

// TestSweep_PRPendingGitHubFailureIsAnErrorItem: a gh failure is never a
// verdict. It is reported, counted, and the marker stays.
func TestSweep_PRPendingGitHubFailureIsAnErrorItem(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))
	gh := &fakeGitHub{listErr: errors.New("gh: HTTP 502")}

	rep := runPR(t, f, gh, nil, Options{})
	item := onlyItem(t, rep)

	if item.Action != ActionError || rep.Errors != 1 {
		t.Fatalf("item = %+v errors=%d, want one error item", item, rep.Errors)
	}
	if len(f.removed) != 0 {
		t.Errorf("a failed read must keep the marker, got %v", f.removed)
	}
}

// TestSweep_EmptyPRPendingLedgerNeverCallsGitHub is the guard that keeps the
// union pass unchanged: no marked tickets, no drift scan, no gh.
func TestSweep_EmptyPRPendingLedgerNeverCallsGitHub(t *testing.T) {
	f := newFakeBackend()
	gh := &fakeGitHub{}

	rep := runPR(t, f, gh, nil, Options{})

	if len(rep.Items) != 0 {
		t.Fatalf("items = %+v, want none", rep.Items)
	}
	if gh.listCalls != 0 {
		t.Errorf("GitHub was called %d time(s) for an empty ledger", gh.listCalls)
	}
}

// --- inverse drift ---

// TestSweep_PRDriftReportsButNeverWrites: a ticket that fails the test while
// carrying no marker is drift in the other direction. It is listed and left
// alone — applying the marker is PUPPET-653's question, not this sweep's.
func TestSweep_PRDriftReportsButNeverWrites(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-7", "loomcli", "delivered"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 700, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-7", BaseRefName: "v5"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{PRDrift: true}))

	if item.Action != ActionDrift || item.OriginID != "PUPPET-7" || item.PR != 700 {
		t.Fatalf("item = %+v, want PUPPET-7 reported as drift on #700", item)
	}
	if len(f.added) != 0 || len(f.removed) != 0 || len(f.comments) != 0 {
		t.Errorf("drift is read-only; wrote added=%v removed=%v comments=%v", f.added, f.removed, f.comments)
	}
}

// TestSweep_PRDriftIgnoresHealthyAndMarkedTickets: only a failing, unmarked
// ticket is drift. A mergeable one is healthy, and a marked one was already
// judged by the clear pass — reporting either would make the report noise.
func TestSweep_PRDriftIgnoresHealthyAndMarkedTickets(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-1", "loomcli"))             // marked: the clear pass owns it
	f.add(prIssue("PUPPET-8", "loomcli", "approved")) // unmarked and mergeable: healthy
	gh := &fakeGitHub{prs: []PR{
		{Number: 612, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-1", BaseRefName: "v5"},
		{Number: 800, State: "OPEN", Mergeable: MergeableYes, HeadRefName: "loom/PUPPET-8", BaseRefName: "v5"},
		// A branch naming a ticket this workspace does not have.
		{Number: 900, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/OTHER-3", BaseRefName: "v5"},
		// A hand-made branch that is not a task branch at all.
		{Number: 901, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/wip", BaseRefName: "v5"},
	}}

	item := onlyItem(t, runPR(t, f, gh, nil, Options{PRDrift: true}))

	if item.OriginID != "PUPPET-1" || item.Action != ActionCleared {
		t.Fatalf("item = %+v, want only the cleared PUPPET-1", item)
	}
}

// TestSweep_PRDriftIsOffByDefault: the scan costs a full PR listing per repo
// and reaches GitHub even with an empty ledger, so a caller opts in.
func TestSweep_PRDriftIsOffByDefault(t *testing.T) {
	f := newFakeBackend()
	f.add(prIssue("PUPPET-7", "loomcli", "delivered"))
	gh := &fakeGitHub{prs: []PR{
		{Number: 700, State: "OPEN", Mergeable: MergeableNo, HeadRefName: "loom/PUPPET-7", BaseRefName: "v5"},
	}}

	rep := runPR(t, f, gh, nil, Options{})

	if len(rep.Items) != 0 || gh.listCalls != 0 {
		t.Fatalf("items = %+v, gh calls = %d, want neither without --pr-drift", rep.Items, gh.listCalls)
	}
}

// TestTaskIDsOf pins the head-branch parser: revisions collapse onto their
// ticket, and anything that is not a task branch is not looked up.
func TestTaskIDsOf(t *testing.T) {
	got := taskIDsOf([]PR{
		{HeadRefName: "loom/PUPPET-655"},
		{HeadRefName: "loom/PUPPET-655-r2"},
		{HeadRefName: "loom/WEB-3"},
		{HeadRefName: "loom/wip"},
		{HeadRefName: "fix/v5-frontend-ci"},
		{HeadRefName: "loom/PUPPET-655-rx"},
	})
	want := []string{"PUPPET-655", "WEB-3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("taskIDsOf = %v, want %v", got, want)
	}
}
