package prreadiness

import (
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"
)

// openGitHubPR is a mergeable, clean open PR with one passing required check.
func openGitHubPR() GitHubPR {
	return GitHubPR{
		Number:           7,
		State:            "OPEN",
		HeadRefName:      "feat",
		HeadRefOid:       "head1",
		BaseRefName:      "main",
		BaseRefOid:       "base1",
		Mergeable:        "MERGEABLE",
		MergeStateStatus: "CLEAN",
		ReviewDecision:   "APPROVED",
		Checks: []GitHubCheck{
			{Name: "ci", Kind: "check_run", Status: "COMPLETED", Conclusion: "SUCCESS", IsRequired: true},
		},
	}
}

func TestFactsFromGitHubScalarFacts(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*GitHubPR)
		get  func(Facts) Fact
		want Fact
	}{
		{"open", func(*GitHubPR) {}, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleOpen)},
		{"open lowercase", func(p *GitHubPR) { p.State = "open" }, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleOpen)},
		{"draft", func(p *GitHubPR) { p.IsDraft = true }, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleDraft)},
		{"closed", func(p *GitHubPR) { p.State = "CLOSED" }, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleClosed)},
		{"merged state", func(p *GitHubPR) { p.State = "MERGED" }, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleMerged)},
		{"merged flag wins over closed", func(p *GitHubPR) { p.State = "CLOSED"; p.Merged = true }, func(f Facts) Fact { return f.Lifecycle }, Known(LifecycleMerged)},
		{"unrecognized state", func(p *GitHubPR) { p.State = "LIMBO" }, func(f Facts) Fact { return f.Lifecycle },
			Fact{Status: FactUnknown, Value: "limbo", Error: ErrUnrecognizedValue}},

		{"mergeable", func(*GitHubPR) {}, func(f Facts) Fact { return f.Conflicts }, Known(ConflictsNone)},
		{"conflicting", func(p *GitHubPR) { p.Mergeable = "CONFLICTING" }, func(f Facts) Fact { return f.Conflicts }, Known(ConflictsConflicting)},
		{"mergeable UNKNOWN is computing", func(p *GitHubPR) { p.Mergeable = "UNKNOWN" }, func(f Facts) Fact { return f.Conflicts }, Computing()},
		{"mergeable empty is computing", func(p *GitHubPR) { p.Mergeable = "" }, func(f Facts) Fact { return f.Conflicts }, Computing()},
		{"mergeable unrecognized", func(p *GitHubPR) { p.Mergeable = "MAYBE" }, func(f Facts) Fact { return f.Conflicts },
			Fact{Status: FactUnknown, Value: "maybe", Error: ErrUnrecognizedValue}},

		{"clean", func(*GitHubPR) {}, func(f Facts) Fact { return f.MergeState }, Known(MergeStateClean)},
		{"has_hooks", func(p *GitHubPR) { p.MergeStateStatus = "HAS_HOOKS" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateHasHooks)},
		{"unstable", func(p *GitHubPR) { p.MergeStateStatus = "UNSTABLE" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateUnstable)},
		{"blocked", func(p *GitHubPR) { p.MergeStateStatus = "BLOCKED" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateBlocked)},
		{"behind", func(p *GitHubPR) { p.MergeStateStatus = "BEHIND" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateBehind)},
		{"dirty", func(p *GitHubPR) { p.MergeStateStatus = "DIRTY" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateDirty)},
		{"draft merge state", func(p *GitHubPR) { p.MergeStateStatus = "DRAFT" }, func(f Facts) Fact { return f.MergeState }, Known(MergeStateDraft)},
		{"merge state UNKNOWN is computing", func(p *GitHubPR) { p.MergeStateStatus = "UNKNOWN" }, func(f Facts) Fact { return f.MergeState }, Computing()},
		{"merge state empty is computing", func(p *GitHubPR) { p.MergeStateStatus = "" }, func(f Facts) Fact { return f.MergeState }, Computing()},
		{"merge state unrecognized", func(p *GitHubPR) { p.MergeStateStatus = "SIDEWAYS" }, func(f Facts) Fact { return f.MergeState },
			Fact{Status: FactUnknown, Value: "sideways", Error: ErrUnrecognizedValue}},

		{"approved", func(*GitHubPR) {}, func(f Facts) Fact { return f.Review }, Known(ReviewApproved)},
		{"changes requested", func(p *GitHubPR) { p.ReviewDecision = "CHANGES_REQUESTED" }, func(f Facts) Fact { return f.Review }, Known(ReviewChangesRequested)},
		{"review required", func(p *GitHubPR) { p.ReviewDecision = "REVIEW_REQUIRED" }, func(f Facts) Fact { return f.Review }, Known(ReviewRequired)},
		{"review empty is not_reported", func(p *GitHubPR) { p.ReviewDecision = "" }, func(f Facts) Fact { return f.Review }, Known(ReviewNotReported)},
		{"review unrecognized", func(p *GitHubPR) { p.ReviewDecision = "SHRUG" }, func(f Facts) Fact { return f.Review },
			Fact{Status: FactUnknown, Value: "shrug", Error: ErrUnrecognizedValue}},

		{"not queued", func(*GitHubPR) {}, func(f Facts) Fact { return f.Queue }, Known(QueueNotQueued)},
		{"in queue without state", func(p *GitHubPR) { p.IsInMergeQueue = true }, func(f Facts) Fact { return f.Queue }, Known(QueueQueued)},
		{"queue QUEUED", func(p *GitHubPR) { p.MergeQueueState = "QUEUED" }, func(f Facts) Fact { return f.Queue }, Known(QueueQueued)},
		{"queue AWAITING_CHECKS", func(p *GitHubPR) { p.MergeQueueState = "AWAITING_CHECKS" }, func(f Facts) Fact { return f.Queue }, Known(QueueAwaitingChecks)},
		{"queue LOCKED", func(p *GitHubPR) { p.MergeQueueState = "LOCKED" }, func(f Facts) Fact { return f.Queue }, Known(QueueLocked)},
		{"queue MERGEABLE", func(p *GitHubPR) { p.MergeQueueState = "MERGEABLE" }, func(f Facts) Fact { return f.Queue }, Known(QueueMergeable)},
		{"queue UNMERGEABLE", func(p *GitHubPR) { p.MergeQueueState = "UNMERGEABLE" }, func(f Facts) Fact { return f.Queue }, Known(QueueUnmergeable)},
		{"queue unrecognized", func(p *GitHubPR) { p.MergeQueueState = "FLOATING" }, func(f Facts) Fact { return f.Queue },
			Fact{Status: FactUnknown, Value: "floating", Error: ErrUnrecognizedValue}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := openGitHubPR()
			tt.mut(&pr)
			if got := tt.get(FactsFromGitHub(pr)); got != tt.want {
				t.Errorf("fact = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFactsFromGitHubChecks(t *testing.T) {
	run := func(name, status, conclusion string, required bool) GitHubCheck {
		return GitHubCheck{Name: name, Kind: "check_run", Status: status, Conclusion: conclusion, IsRequired: required}
	}
	ctx := func(name, state string, required bool) GitHubCheck {
		return GitHubCheck{Name: name, Kind: "status_context", Conclusion: state, IsRequired: required}
	}
	tests := []struct {
		name         string
		checks       []GitHubCheck
		truncated    bool
		wantRequired Fact
		wantReqSum   CheckSummary
		wantOptional Fact
		wantOptSum   CheckSummary
	}{
		{
			name:         "no checks",
			wantRequired: Known(ChecksNoneRequired),
			wantOptional: Known(ChecksNone),
		},
		{
			name: "required vs optional split via isRequired",
			checks: []GitHubCheck{
				run("build", "COMPLETED", "SUCCESS", true),
				run("lint", "COMPLETED", "FAILURE", false),
			},
			wantRequired: Known(ChecksPassing),
			wantReqSum:   CheckSummary{Passed: 1, Total: 1},
			wantOptional: Known(ChecksFailing),
			wantOptSum:   CheckSummary{Failed: 1, Total: 1, FailingNames: []string{"lint"}},
		},
		{
			name: "SUCCESS NEUTRAL SKIPPED all pass",
			checks: []GitHubCheck{
				run("a", "COMPLETED", "SUCCESS", true),
				run("b", "COMPLETED", "NEUTRAL", true),
				run("c", "COMPLETED", "SKIPPED", true),
				ctx("d", "SUCCESS", true),
			},
			wantRequired: Known(ChecksPassing),
			wantReqSum:   CheckSummary{Passed: 4, Total: 4},
			wantOptional: Known(ChecksNone),
		},
		{
			name: "check_run not COMPLETED is pending even with a conclusion",
			checks: []GitHubCheck{
				run("a", "IN_PROGRESS", "SUCCESS", true),
				run("b", "QUEUED", "", true),
			},
			wantRequired: Known(ChecksPending),
			wantReqSum:   CheckSummary{Pending: 2, Total: 2, PendingNames: []string{"a", "b"}},
			wantOptional: Known(ChecksNone),
		},
		{
			name: "status context EXPECTED and PENDING are pending",
			checks: []GitHubCheck{
				ctx("exp", "EXPECTED", true),
				ctx("pend", "PENDING", true),
			},
			wantRequired: Known(ChecksPending),
			wantReqSum:   CheckSummary{Pending: 2, Total: 2, PendingNames: []string{"exp", "pend"}},
			wantOptional: Known(ChecksNone),
		},
		{
			name: "status context ERROR and check_run TIMED_OUT fail",
			checks: []GitHubCheck{
				ctx("s", "ERROR", true),
				run("t", "COMPLETED", "TIMED_OUT", true),
				run("u", "COMPLETED", "STARTUP_FAILURE", true),
				run("v", "COMPLETED", "ACTION_REQUIRED", true),
			},
			wantRequired: Known(ChecksFailing),
			wantReqSum:   CheckSummary{Failed: 4, Total: 4, FailingNames: []string{"s", "t", "u", "v"}},
			wantOptional: Known(ChecksNone),
		},
		{
			name: "failing beats pending",
			checks: []GitHubCheck{
				run("a", "IN_PROGRESS", "", true),
				run("b", "COMPLETED", "FAILURE", true),
				run("c", "COMPLETED", "SUCCESS", true),
			},
			wantRequired: Known(ChecksFailing),
			wantReqSum:   CheckSummary{Passed: 1, Pending: 1, Failed: 1, Total: 3, FailingNames: []string{"b"}, PendingNames: []string{"a"}},
			wantOptional: Known(ChecksNone),
		},
		{
			name:         "truncated is an error even when every seen check passes",
			checks:       []GitHubCheck{run("a", "COMPLETED", "SUCCESS", true), run("o", "COMPLETED", "SUCCESS", false)},
			truncated:    true,
			wantRequired: Errored(ErrChecksTruncated, 0),
			wantReqSum:   CheckSummary{Passed: 1, Total: 1},
			wantOptional: Errored(ErrChecksTruncated, 0),
			wantOptSum:   CheckSummary{Passed: 1, Total: 1},
		},
		{
			name:         "truncated with no checks seen",
			truncated:    true,
			wantRequired: Errored(ErrChecksTruncated, 0),
			wantOptional: Errored(ErrChecksTruncated, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := openGitHubPR()
			pr.Checks = tt.checks
			pr.ChecksTruncated = tt.truncated
			f := FactsFromGitHub(pr)
			if f.RequiredChecks != tt.wantRequired {
				t.Errorf("RequiredChecks = %+v, want %+v", f.RequiredChecks, tt.wantRequired)
			}
			if !reflect.DeepEqual(f.RequiredCounts, tt.wantReqSum) {
				t.Errorf("RequiredCounts = %+v, want %+v", f.RequiredCounts, tt.wantReqSum)
			}
			if f.OptionalChecks != tt.wantOptional {
				t.Errorf("OptionalChecks = %+v, want %+v", f.OptionalChecks, tt.wantOptional)
			}
			if !reflect.DeepEqual(f.OptionalCounts, tt.wantOptSum) {
				t.Errorf("OptionalCounts = %+v, want %+v", f.OptionalCounts, tt.wantOptSum)
			}
		})
	}
}

func TestFactsFromGitHubCapsCheckNames(t *testing.T) {
	pr := openGitHubPR()
	pr.Checks = nil
	for i := range 15 {
		pr.Checks = append(pr.Checks,
			GitHubCheck{Name: "fail-" + strconv.Itoa(i), Kind: "check_run", Status: "COMPLETED", Conclusion: "FAILURE", IsRequired: true},
			GitHubCheck{Name: "pend-" + strconv.Itoa(i), Kind: "check_run", Status: "QUEUED", IsRequired: true},
		)
	}
	f := FactsFromGitHub(pr)
	sum := f.RequiredCounts
	if sum.Failed != 15 || sum.Pending != 15 || sum.Total != 30 {
		t.Fatalf("counts = %+v, want 15 failed, 15 pending, 30 total", sum)
	}
	if len(sum.FailingNames) != maxCheckNames || len(sum.PendingNames) != maxCheckNames {
		t.Fatalf("names = %d failing, %d pending; want both capped at %d", len(sum.FailingNames), len(sum.PendingNames), maxCheckNames)
	}
	if sum.FailingNames[0] != "fail-0" || sum.FailingNames[9] != "fail-9" {
		t.Fatalf("FailingNames = %v, want first ten in order", sum.FailingNames)
	}
}

func TestSnapshotFromGitHubPinsHeadAndBase(t *testing.T) {
	observed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := SnapshotFromGitHub("github:o/r#7", openGitHubPR(), observed)
	if s.PRKey != "github:o/r#7" || s.HeadSHA != "head1" || s.HeadRef != "feat" || s.BaseRef != "main" || s.BaseSHA != "base1" {
		t.Fatalf("snapshot identity = %+v", s)
	}
	if !s.ObservedAt.Equal(observed) || s.Verdict != VerdictReady {
		t.Fatalf("snapshot = %+v, want ready observed at %v", s, observed)
	}
}

// TestHeadMovementChangesFingerprint: the same PR observed on its old head
// with passing checks and on a new head with pending checks yields different
// fingerprints and verdicts.
func TestHeadMovementChangesFingerprint(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	old := openGitHubPR()
	moved := openGitHubPR()
	moved.HeadRefOid = "head2"
	moved.MergeStateStatus = "BLOCKED"
	moved.Checks = []GitHubCheck{{Name: "ci", Kind: "check_run", Status: "IN_PROGRESS", IsRequired: true}}

	a := SnapshotFromGitHub("github:o/r#7", old, t0)
	b := SnapshotFromGitHub("github:o/r#7", moved, t0.Add(time.Second))
	if a.Fingerprint == b.Fingerprint {
		t.Fatal("fingerprint unchanged after head movement")
	}
	if a.Verdict != VerdictReady {
		t.Fatalf("old head verdict = %q, want ready", a.Verdict)
	}
	if b.Verdict != VerdictWaiting || !slices.Equal(b.Reasons, []string{ReasonRequiredChecksPending}) {
		t.Fatalf("new head = %q %v, want waiting [required_checks_pending]", b.Verdict, b.Reasons)
	}

	// Same head re-read later: stable fingerprint.
	c := SnapshotFromGitHub("github:o/r#7", old, t0.Add(time.Hour))
	if c.Fingerprint != a.Fingerprint {
		t.Fatal("fingerprint changed on unchanged re-read")
	}

	// Same facts but only the head SHA moved: still a different fingerprint.
	onlyHead := old
	onlyHead.HeadRefOid = "head3"
	if SnapshotFromGitHub("github:o/r#7", onlyHead, t0).Fingerprint == a.Fingerprint {
		t.Fatal("fingerprint unchanged when only head SHA moved")
	}
}
