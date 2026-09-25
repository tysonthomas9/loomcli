package prreadiness

import (
	"slices"
	"testing"
	"time"
)

var testT0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func TestClassifyFreshnessBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		observedAt  time.Time
		age         time.Duration
		invalidated bool
		want        Freshness
	}{
		{"age zero", testT0, 0, false, FreshnessFresh},
		{"exactly 60s is fresh", testT0, 60 * time.Second, false, FreshnessFresh},
		{"60s+1ns is aging", testT0, 60*time.Second + time.Nanosecond, false, FreshnessAging},
		{"exactly 10m is aging", testT0, 10 * time.Minute, false, FreshnessAging},
		{"10m+1ns is stale", testT0, 10*time.Minute + time.Nanosecond, false, FreshnessStale},
		{"clock skew (future observation) is fresh", testT0, -5 * time.Second, false, FreshnessFresh},
		{"invalidated at age zero is stale", testT0, 0, true, FreshnessStale},
		{"zero observedAt is unknown", time.Time{}, 0, false, FreshnessUnknown},
		{"zero observedAt beats invalidation", time.Time{}, 0, true, FreshnessUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := testT0.Add(tt.age)
			if got := ClassifyFreshness(tt.observedAt, now, tt.invalidated); got != tt.want {
				t.Errorf("ClassifyFreshness = %q, want %q", got, tt.want)
			}
		})
	}
}

func snapWithVerdict(t *testing.T, key string, facts Facts, observedAt time.Time) *Snapshot {
	t.Helper()
	s := NewSnapshot(key, "head", "feat", "main", "base", observedAt, facts)
	return &s
}

func TestNewView(t *testing.T) {
	ready := cleanFacts()
	blocked := withFacts(func(f *Facts) { f.Review = Known(ReviewRequired) })
	merged := withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleMerged) })
	closed := withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleClosed) })
	tests := []struct {
		name          string
		facts         *Facts // nil means no snapshot
		age           time.Duration
		invalidated   string
		lastErr       *ReadError
		wantFresh     Freshness
		wantVerdict   Verdict
		wantReasons   []string
		wantLastError bool
		wantAge       int
	}{
		{
			name: "fresh ready is ready", facts: &ready, age: 30 * time.Second,
			wantFresh: FreshnessFresh, wantVerdict: VerdictReady, wantReasons: []string{}, wantAge: 30,
		},
		{
			name: "ready at exactly 60s is still ready", facts: &ready, age: FreshFor,
			wantFresh: FreshnessFresh, wantVerdict: VerdictReady, wantReasons: []string{}, wantAge: 60,
		},
		{
			name: "aging ready is unknown readiness_aging", facts: &ready, age: 61 * time.Second,
			wantFresh: FreshnessAging, wantVerdict: VerdictUnknown, wantReasons: []string{ReasonAging}, wantAge: 61,
		},
		{
			name: "aging blocked stays blocked", facts: &blocked, age: 5 * time.Minute,
			wantFresh: FreshnessAging, wantVerdict: VerdictBlocked, wantReasons: []string{ReasonReview}, wantAge: 300,
		},
		{
			name: "stale ready is unknown stale", facts: &ready, age: 11 * time.Minute,
			wantFresh: FreshnessStale, wantVerdict: VerdictUnknown, wantReasons: []string{ReasonStale}, wantAge: 660,
		},
		{
			name: "stale blocked is unknown stale", facts: &blocked, age: 11 * time.Minute,
			wantFresh: FreshnessStale, wantVerdict: VerdictUnknown, wantReasons: []string{ReasonStale}, wantAge: 660,
		},
		{
			name: "invalidated fresh ready is unknown stale", facts: &ready, age: time.Second, invalidated: InvalidatedHeadMoved,
			wantFresh: FreshnessStale, wantVerdict: VerdictUnknown, wantReasons: []string{ReasonStale}, wantAge: 1,
		},
		{
			name: "merged sticky when stale", facts: &merged, age: 24 * time.Hour,
			wantFresh: FreshnessStale, wantVerdict: VerdictMerged, wantReasons: []string{}, wantAge: 86400,
		},
		{
			name: "merged sticky when invalidated", facts: &merged, age: time.Second, invalidated: InvalidatedBaseChanged,
			wantFresh: FreshnessStale, wantVerdict: VerdictMerged, wantReasons: []string{}, wantAge: 1,
		},
		{
			name: "closed sticky when stale", facts: &closed, age: time.Hour,
			wantFresh: FreshnessStale, wantVerdict: VerdictClosed, wantReasons: []string{}, wantAge: 3600,
		},
		{
			name: "merged ignores newer read error", facts: &merged, age: time.Second,
			lastErr:   &ReadError{Code: ErrTimeout, At: testT0.Add(time.Second)},
			wantFresh: FreshnessFresh, wantVerdict: VerdictMerged, wantReasons: []string{}, wantLastError: true, wantAge: 1,
		},
		{
			name:      "nil snapshot is unknown not_observed",
			wantFresh: FreshnessUnknown, wantVerdict: VerdictUnknown, wantReasons: []string{ReasonNotObserved},
		},
		{
			name:      "nil snapshot with read error is unknown repo_error",
			lastErr:   &ReadError{Code: ErrForbidden, At: testT0},
			wantFresh: FreshnessUnknown, wantVerdict: VerdictUnknown, wantReasons: []string{"repo_error:forbidden"}, wantLastError: true,
		},
		{
			name: "newer failed read demotes fresh ready", facts: &ready, age: 10 * time.Second,
			lastErr:   &ReadError{Code: ErrTimeout, At: testT0.Add(5 * time.Second)},
			wantFresh: FreshnessFresh, wantVerdict: VerdictUnknown, wantReasons: []string{"repo_error:timeout"}, wantLastError: true, wantAge: 10,
		},
		{
			name: "newer failed read appends to blocked", facts: &blocked, age: 10 * time.Second,
			lastErr:   &ReadError{Code: ErrRateLimited, RetryAfterSeconds: 30, At: testT0.Add(5 * time.Second)},
			wantFresh: FreshnessFresh, wantVerdict: VerdictBlocked, wantReasons: []string{ReasonReview, "repo_error:rate_limited"}, wantLastError: true, wantAge: 10,
		},
		{
			name: "older failed read is dropped", facts: &ready, age: 10 * time.Second,
			lastErr:   &ReadError{Code: ErrTimeout, At: testT0.Add(-time.Second)},
			wantFresh: FreshnessFresh, wantVerdict: VerdictReady, wantReasons: []string{}, wantAge: 10,
		},
		{
			name: "failed read at the same instant is dropped", facts: &ready, age: 10 * time.Second,
			lastErr:   &ReadError{Code: ErrTimeout, At: testT0},
			wantFresh: FreshnessFresh, wantVerdict: VerdictReady, wantReasons: []string{}, wantAge: 10,
		},
		{
			name: "future snapshot has zero age", facts: &ready, age: -3 * time.Second,
			wantFresh: FreshnessFresh, wantVerdict: VerdictReady, wantReasons: []string{}, wantAge: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var snap *Snapshot
			if tt.facts != nil {
				snap = snapWithVerdict(t, "github:o/r#1", *tt.facts, testT0)
			}
			v := NewView("github:o/r#1", snap, testT0.Add(tt.age), tt.invalidated, tt.lastErr)
			if v.PRKey != "github:o/r#1" {
				t.Errorf("PRKey = %q", v.PRKey)
			}
			if v.Freshness != tt.wantFresh {
				t.Errorf("Freshness = %q, want %q", v.Freshness, tt.wantFresh)
			}
			if v.CurrentVerdict != tt.wantVerdict {
				t.Errorf("CurrentVerdict = %q, want %q", v.CurrentVerdict, tt.wantVerdict)
			}
			if !slices.Equal(v.CurrentReasons, tt.wantReasons) || v.CurrentReasons == nil {
				t.Errorf("CurrentReasons = %#v, want %#v", v.CurrentReasons, tt.wantReasons)
			}
			if (v.LastError != nil) != tt.wantLastError {
				t.Errorf("LastError = %+v, want present=%v", v.LastError, tt.wantLastError)
			}
			if v.AgeSeconds != tt.wantAge {
				t.Errorf("AgeSeconds = %d, want %d", v.AgeSeconds, tt.wantAge)
			}
			if v.Invalidated != tt.invalidated {
				t.Errorf("Invalidated = %q, want %q", v.Invalidated, tt.invalidated)
			}
			if snap != nil && v.Snapshot != snap {
				t.Errorf("Snapshot not retained as history")
			}
			if v.CurrentVerdict == VerdictReady && v.Freshness != FreshnessFresh {
				t.Errorf("invariant: current ready while freshness %q", v.Freshness)
			}
		})
	}
}

// TestNewViewStaleKeepsSnapshotHistory: a stale ready snapshot still reports
// its original verdict in snapshot (history) while the current verdict is
// unknown.
func TestNewViewStaleKeepsSnapshotHistory(t *testing.T) {
	snap := snapWithVerdict(t, "github:o/r#1", cleanFacts(), testT0)
	v := NewView("github:o/r#1", snap, testT0.Add(time.Hour), "", nil)
	if v.Snapshot == nil || v.Snapshot.Verdict != VerdictReady {
		t.Fatalf("snapshot = %+v, want ready history", v.Snapshot)
	}
	if v.CurrentVerdict != VerdictUnknown {
		t.Fatalf("current = %q, want unknown", v.CurrentVerdict)
	}
}

// TestNewViewDoesNotAliasSnapshotReasons: mutating the view's current
// reasons must not rewrite the snapshot's reasons.
func TestNewViewDoesNotAliasSnapshotReasons(t *testing.T) {
	facts := withFacts(func(f *Facts) { f.Review = Known(ReviewRequired) })
	snap := snapWithVerdict(t, "github:o/r#1", facts, testT0)
	v := NewView("github:o/r#1", snap, testT0.Add(time.Second), "", &ReadError{Code: ErrTimeout, At: testT0.Add(time.Second)})
	if !slices.Equal(snap.Reasons, []string{ReasonReview}) {
		t.Fatalf("snapshot reasons mutated to %v", snap.Reasons)
	}
	v.CurrentReasons[0] = "mutated"
	if snap.Reasons[0] != ReasonReview {
		t.Fatalf("view reasons alias snapshot reasons")
	}
}
