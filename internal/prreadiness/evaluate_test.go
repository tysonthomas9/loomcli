package prreadiness

import (
	"slices"
	"testing"
	"time"
)

// cleanFacts is a fully known, mergeable, open PR with no review requirement
// reported and passing required checks.
func cleanFacts() Facts {
	return Facts{
		Lifecycle:      Known(LifecycleOpen),
		Conflicts:      Known(ConflictsNone),
		MergeState:     Known(MergeStateClean),
		Review:         Known(ReviewApproved),
		RequiredChecks: Known(ChecksPassing),
		OptionalChecks: Known(ChecksNone),
		Queue:          Known(QueueNotQueued),
	}
}

func withFacts(mut func(*Facts)) Facts {
	f := cleanFacts()
	mut(&f)
	return f
}

func TestEvaluateTruthTable(t *testing.T) {
	unknownFact := Fact{Status: FactUnknown, Error: ErrUnrecognizedValue}
	errFact := Errored(ErrRateLimited, 30*time.Second)
	tests := []struct {
		name        string
		facts       Facts
		wantVerdict Verdict
		wantReasons []string
	}{
		{
			name:        "clean is ready",
			facts:       cleanFacts(),
			wantVerdict: VerdictReady,
			wantReasons: []string{},
		},
		{
			name:        "has_hooks is ready",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateHasHooks) }),
			wantVerdict: VerdictReady,
			wantReasons: []string{},
		},
		{
			name:        "unstable is ready with optional_check_failing",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateUnstable) }),
			wantVerdict: VerdictReady,
			wantReasons: []string{ReasonOptionalCheckFailing},
		},
		{
			name:        "not_reported review is ready with no_review_required",
			facts:       withFacts(func(f *Facts) { f.Review = Known(ReviewNotReported) }),
			wantVerdict: VerdictReady,
			wantReasons: []string{ReasonNoReviewRequired},
		},
		{
			name: "unstable plus not_reported carries both warnings in order",
			facts: withFacts(func(f *Facts) {
				f.MergeState = Known(MergeStateUnstable)
				f.Review = Known(ReviewNotReported)
			}),
			wantVerdict: VerdictReady,
			wantReasons: []string{ReasonOptionalCheckFailing, ReasonNoReviewRequired},
		},
		{
			name:        "none_required checks is ready",
			facts:       withFacts(func(f *Facts) { f.RequiredChecks = Known(ChecksNoneRequired) }),
			wantVerdict: VerdictReady,
			wantReasons: []string{},
		},
		{
			name:        "merged",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleMerged) }),
			wantVerdict: VerdictMerged,
			wantReasons: []string{},
		},
		{
			name: "merged wins over unknown gating facts",
			facts: withFacts(func(f *Facts) {
				f.Lifecycle = Known(LifecycleMerged)
				f.Conflicts = unknownFact
			}),
			wantVerdict: VerdictMerged,
			wantReasons: []string{},
		},
		{
			name:        "closed",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleClosed) }),
			wantVerdict: VerdictClosed,
			wantReasons: []string{},
		},
		{
			name:        "draft lifecycle is blocked",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleDraft) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonDraft},
		},
		{
			name:        "draft merge_state is blocked",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateDraft) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonDraft},
		},
		{
			name:        "lifecycle unknown",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = unknownFact }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"lifecycle_unknown:unrecognized_value"},
		},
		{
			name:        "lifecycle unrecognized known value",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = Known("reopened") }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"lifecycle_unknown:known"},
		},
		{
			name:        "zero-value lifecycle",
			facts:       withFacts(func(f *Facts) { f.Lifecycle = Fact{} }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"lifecycle_unknown:unknown"},
		},
		{
			name:        "conflicts unknown",
			facts:       withFacts(func(f *Facts) { f.Conflicts = unknownFact }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"conflicts_unknown:unrecognized_value"},
		},
		{
			name:        "merge_state error",
			facts:       withFacts(func(f *Facts) { f.MergeState = errFact }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"merge_state_unknown:rate_limited"},
		},
		{
			name:        "required checks truncated",
			facts:       withFacts(func(f *Facts) { f.RequiredChecks = Errored(ErrChecksTruncated, 0) }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"required_checks_unknown:checks_truncated"},
		},
		{
			name:        "queue zero value",
			facts:       withFacts(func(f *Facts) { f.Queue = Fact{} }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"queue_unknown:unknown"},
		},
		{
			name: "every unknown gating fact is listed in order",
			facts: withFacts(func(f *Facts) {
				f.Conflicts = unknownFact
				f.MergeState = errFact
				f.RequiredChecks = Fact{Status: FactUnknown}
				f.Queue = Errored(ErrTimeout, 0)
			}),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{
				"conflicts_unknown:unrecognized_value",
				"merge_state_unknown:rate_limited",
				"required_checks_unknown:unknown",
				"queue_unknown:timeout",
			},
		},
		{
			name: "unknown gating fact beats computing",
			facts: withFacts(func(f *Facts) {
				f.Conflicts = Computing()
				f.RequiredChecks = errFact
			}),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"required_checks_unknown:rate_limited"},
		},
		{
			name:        "conflicts computing is waiting",
			facts:       withFacts(func(f *Facts) { f.Conflicts = Computing() }),
			wantVerdict: VerdictWaiting,
			wantReasons: []string{ReasonGitHubComputing},
		},
		{
			name:        "merge_state computing is waiting",
			facts:       withFacts(func(f *Facts) { f.MergeState = Computing() }),
			wantVerdict: VerdictWaiting,
			wantReasons: []string{ReasonGitHubComputing},
		},
		{
			name: "computing beats conflicting evidence",
			facts: withFacts(func(f *Facts) {
				f.Conflicts = Computing()
				f.MergeState = Known(MergeStateDirty)
			}),
			wantVerdict: VerdictWaiting,
			wantReasons: []string{ReasonGitHubComputing},
		},
		{
			name:        "queued",
			facts:       withFacts(func(f *Facts) { f.Queue = Known(QueueQueued) }),
			wantVerdict: VerdictQueued,
			wantReasons: []string{ReasonInMergeQueue},
		},
		{
			name:        "awaiting_checks queue state is queued",
			facts:       withFacts(func(f *Facts) { f.Queue = Known(QueueAwaitingChecks) }),
			wantVerdict: VerdictQueued,
			wantReasons: []string{ReasonInMergeQueue},
		},
		{
			name: "unmergeable queue state is still queued, not blocked",
			facts: withFacts(func(f *Facts) {
				f.Queue = Known(QueueUnmergeable)
				f.RequiredChecks = Known(ChecksFailing)
			}),
			wantVerdict: VerdictQueued,
			wantReasons: []string{ReasonInMergeQueue},
		},
		{
			name:        "conflicting",
			facts:       withFacts(func(f *Facts) { f.Conflicts = Known(ConflictsConflicting) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonConflicts},
		},
		{
			name:        "dirty merge state",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateDirty) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonConflicts},
		},
		{
			name:        "required check failed",
			facts:       withFacts(func(f *Facts) { f.RequiredChecks = Known(ChecksFailing) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonRequiredCheckFailed},
		},
		{
			name: "approved plus failing required check is blocked, never ready",
			facts: withFacts(func(f *Facts) {
				f.Review = Known(ReviewApproved)
				f.MergeState = Known(MergeStateBlocked)
				f.RequiredChecks = Known(ChecksFailing)
			}),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonRequiredCheckFailed},
		},
		{
			name:        "required checks pending is waiting",
			facts:       withFacts(func(f *Facts) { f.RequiredChecks = Known(ChecksPending) }),
			wantVerdict: VerdictWaiting,
			wantReasons: []string{ReasonRequiredChecksPending},
		},
		{
			name:        "changes requested",
			facts:       withFacts(func(f *Facts) { f.Review = Known(ReviewChangesRequested) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonReview},
		},
		{
			name:        "review required",
			facts:       withFacts(func(f *Facts) { f.Review = Known(ReviewRequired) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonReview},
		},
		{
			name: "pending checks first then review: first verdict wins, reasons accumulate",
			facts: withFacts(func(f *Facts) {
				f.RequiredChecks = Known(ChecksPending)
				f.Review = Known(ReviewRequired)
				f.MergeState = Known(MergeStateBlocked)
			}),
			wantVerdict: VerdictWaiting,
			wantReasons: []string{ReasonRequiredChecksPending, ReasonReview},
		},
		{
			name: "all blockers listed in rule order",
			facts: withFacts(func(f *Facts) {
				f.Conflicts = Known(ConflictsConflicting)
				f.RequiredChecks = Known(ChecksFailing)
				f.Review = Known(ReviewChangesRequested)
				f.MergeState = Known(MergeStateBehind)
			}),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonConflicts, ReasonRequiredCheckFailed, ReasonReview, ReasonBehindBase},
		},
		{
			name:        "behind base",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateBehind) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonBehindBase},
		},
		{
			name:        "blocked with no other reason is rule_unsatisfied",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known(MergeStateBlocked) }),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonRuleUnsatisfied},
		},
		{
			name: "approved-only never ready when merge_state blocked",
			facts: withFacts(func(f *Facts) {
				f.Review = Known(ReviewApproved)
				f.MergeState = Known(MergeStateBlocked)
			}),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonRuleUnsatisfied},
		},
		{
			name: "blocked with a review reason does not add rule_unsatisfied",
			facts: withFacts(func(f *Facts) {
				f.Review = Known(ReviewRequired)
				f.MergeState = Known(MergeStateBlocked)
			}),
			wantVerdict: VerdictBlocked,
			wantReasons: []string{ReasonReview},
		},
		{
			name:        "unrecognized known merge_state fails closed",
			facts:       withFacts(func(f *Facts) { f.MergeState = Known("mystery") }),
			wantVerdict: VerdictUnknown,
			wantReasons: []string{"merge_state_unknown:unrecognized_value"},
		},
		{
			name: "review unknown does not gate readiness (GitHub merge_state owns it)",
			facts: withFacts(func(f *Facts) {
				f.Review = Fact{Status: FactUnknown, Error: ErrUnrecognizedValue}
			}),
			wantVerdict: VerdictReady,
			wantReasons: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, reasons := Evaluate(tt.facts)
			if verdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q (reasons %v)", verdict, tt.wantVerdict, reasons)
			}
			if reasons == nil {
				t.Errorf("reasons = nil, want non-nil slice (JSON [] not null)")
			}
			if !slices.Equal(reasons, tt.wantReasons) {
				t.Errorf("reasons = %v, want %v", reasons, tt.wantReasons)
			}
		})
	}
}

// TestEvaluateNeverReadyUnlessAllGatingFactsKnown sweeps every non-known
// status over each gating fact of an otherwise ready PR.
func TestEvaluateNeverReadyUnlessAllGatingFactsKnown(t *testing.T) {
	bad := []Fact{{}, {Status: FactUnknown}, Computing(), Errored(ErrTimeout, 0)}
	setters := map[string]func(*Facts, Fact){
		"lifecycle":       func(f *Facts, v Fact) { f.Lifecycle = v },
		"conflicts":       func(f *Facts, v Fact) { f.Conflicts = v },
		"merge_state":     func(f *Facts, v Fact) { f.MergeState = v },
		"required_checks": func(f *Facts, v Fact) { f.RequiredChecks = v },
		"queue":           func(f *Facts, v Fact) { f.Queue = v },
	}
	for name, set := range setters {
		for _, b := range bad {
			f := cleanFacts()
			set(&f, b)
			if v, reasons := Evaluate(f); v == VerdictReady {
				t.Errorf("%s=%+v: verdict ready (reasons %v), want not ready", name, b, reasons)
			}
		}
	}
}

func TestNewSnapshotPinsIdentityAndEvaluates(t *testing.T) {
	observed := time.Date(2026, 9, 24, 12, 0, 0, 0, time.FixedZone("X", 3600))
	s := NewSnapshot("github:o/r#1", "head1", "feat", "main", "base1", observed, cleanFacts())
	if s.PRKey != "github:o/r#1" || s.HeadSHA != "head1" || s.HeadRef != "feat" || s.BaseRef != "main" || s.BaseSHA != "base1" {
		t.Fatalf("identity not pinned: %+v", s)
	}
	if !s.ObservedAt.Equal(observed) || s.ObservedAt.Location() != time.UTC {
		t.Fatalf("ObservedAt = %v, want %v in UTC", s.ObservedAt, observed)
	}
	if s.Verdict != VerdictReady || s.Fingerprint != Fingerprint(s) || s.Fingerprint == "" {
		t.Fatalf("snapshot = %+v, want ready with computed fingerprint", s)
	}
}

func TestFingerprintIgnoresObservedAt(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	a := NewSnapshot("github:o/r#1", "h", "feat", "main", "b", t0, cleanFacts())
	b := NewSnapshot("github:o/r#1", "h", "feat", "main", "b", t0.Add(5*time.Minute), cleanFacts())
	if a.Fingerprint != b.Fingerprint {
		t.Fatalf("fingerprint changed across observed_at: %s vs %s", a.Fingerprint, b.Fingerprint)
	}
}

func TestFingerprintChangesWhenAnySingleInputChanges(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	base := NewSnapshot("github:o/r#1", "h", "feat", "main", "b", t0, cleanFacts())
	mutations := map[string]func(*Snapshot){
		"pr_key":           func(s *Snapshot) { s.PRKey = "github:o/r#2" },
		"head_sha":         func(s *Snapshot) { s.HeadSHA = "h2" },
		"head_ref":         func(s *Snapshot) { s.HeadRef = "feat2" },
		"base_ref":         func(s *Snapshot) { s.BaseRef = "dev" },
		"base_sha":         func(s *Snapshot) { s.BaseSHA = "b2" },
		"lifecycle":        func(s *Snapshot) { s.Facts.Lifecycle = Known(LifecycleDraft) },
		"conflicts":        func(s *Snapshot) { s.Facts.Conflicts = Computing() },
		"merge_state":      func(s *Snapshot) { s.Facts.MergeState = Known(MergeStateUnstable) },
		"review":           func(s *Snapshot) { s.Facts.Review = Known(ReviewNotReported) },
		"required_checks":  func(s *Snapshot) { s.Facts.RequiredChecks = Known(ChecksPending) },
		"required_counts":  func(s *Snapshot) { s.Facts.RequiredCounts.Passed = 3 },
		"optional_checks":  func(s *Snapshot) { s.Facts.OptionalChecks = Known(ChecksFailing) },
		"optional_counts":  func(s *Snapshot) { s.Facts.OptionalCounts.FailingNames = []string{"lint"} },
		"queue":            func(s *Snapshot) { s.Facts.Queue = Known(QueueQueued) },
		"fact error code":  func(s *Snapshot) { s.Facts.Queue = Errored(ErrTimeout, 0) },
		"fact retry_after": func(s *Snapshot) { s.Facts.Queue = Errored(ErrRateLimited, 10*time.Second) },
	}
	seen := map[string]string{base.Fingerprint: "base"}
	for name, mut := range mutations {
		s := base
		mut(&s)
		fp := Fingerprint(s)
		if fp == base.Fingerprint {
			t.Errorf("%s: fingerprint unchanged", name)
		}
		if prev, dup := seen[fp]; dup {
			t.Errorf("%s: fingerprint collides with %s", name, prev)
		}
		seen[fp] = name
	}
}

func TestErroredCeilsRetryAfter(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want int
	}{
		{0, 0},
		{-time.Second, 0},
		{time.Nanosecond, 1},
		{time.Second, 1},
		{time.Second + time.Millisecond, 2},
		{90 * time.Second, 90},
	}
	for _, tt := range tests {
		if got := Errored(ErrRateLimited, tt.in).RetryAfterSeconds; got != tt.want {
			t.Errorf("Errored(%v).RetryAfterSeconds = %d, want %d", tt.in, got, tt.want)
		}
	}
}
