package prreadiness

import (
	"slices"
	"testing"
	"time"
)

// pv builds a view for key with the given refs and facts, observed at
// observedAt and viewed at now.
func pv(key, headRef, baseRef string, facts Facts, observedAt, now time.Time) View {
	s := NewSnapshot(key, "sha-"+key, headRef, baseRef, "base-sha", observedAt, facts)
	return NewView(key, &s, now, "", nil)
}

func readyView(key, headRef, baseRef string) View {
	return pv(key, headRef, baseRef, cleanFacts(), testT0, testT0.Add(time.Second))
}

func positions(p Preview) []string {
	out := make([]string, 0, len(p.Members))
	for _, m := range p.Members {
		out = append(out, m.Position)
	}
	return out
}

func TestBuildPreviewEdgeRules(t *testing.T) {
	merged := withFacts(func(f *Facts) { f.Lifecycle = Known(LifecycleMerged) })
	blocked := withFacts(func(f *Facts) { f.Review = Known(ReviewRequired) })
	tests := []struct {
		name          string
		views         []View
		wantPositions []string
		wantReady     int
		wantStop      *PreviewStop
	}{
		{
			name:          "empty",
			wantPositions: []string{},
		},
		{
			name: "leading merged skipped",
			views: []View{
				pv("github:o/r#1", "a", "main", merged, testT0, testT0.Add(time.Hour)),
				readyView("github:o/r#2", "b", "a"),
			},
			wantPositions: []string{PositionMerged, PositionInPrefix},
			wantReady:     1,
		},
		{
			name: "cross-repo both included",
			views: []View{
				readyView("github:o/r#1", "feat", "main"),
				readyView("github:o/other#1", "feat", "main"),
			},
			wantPositions: []string{PositionInPrefix, PositionInPrefix},
			wantReady:     2,
		},
		{
			name: "same-repo lineage stops at successor",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/r#2", "feat-2", "feat-1"),
			},
			wantPositions: []string{PositionInPrefix, PositionStop},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/r#2", Verdict: VerdictWaiting, Reasons: []string{ReasonPredecessorRetarget}},
		},
		{
			name: "reversed lineage is an order conflict",
			views: []View{
				readyView("github:o/r#2", "feat-2", "feat-1"),
				readyView("github:o/r#1", "feat-1", "main"),
			},
			wantPositions: []string{PositionInPrefix, PositionStop},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/r#1", Verdict: VerdictBlocked, Reasons: []string{ReasonOrderConflict}},
		},
		{
			name: "same base waits base_will_move",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/r#2", "feat-2", "main"),
			},
			wantPositions: []string{PositionInPrefix, PositionStop},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/r#2", Verdict: VerdictWaiting, Reasons: []string{ReasonBaseWillMove}},
		},
		{
			name: "unrelated bases included",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/r#2", "feat-2", "release"),
			},
			wantPositions: []string{PositionInPrefix, PositionInPrefix},
			wantReady:     2,
		},
		{
			name: "missing refs fail closed",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/r#2", "", "release"),
			},
			wantPositions: []string{PositionInPrefix, PositionStop},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/r#2", Verdict: VerdictUnknown, Reasons: []string{"refs_unknown"}},
		},
		{
			name: "non-ready member stops and later ones are after_stop",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				pv("github:o/x#2", "feat-2", "main", blocked, testT0, testT0.Add(time.Second)),
				readyView("github:o/y#3", "feat-3", "main"),
				pv("github:o/z#4", "feat-4", "main", merged, testT0, testT0.Add(time.Second)),
			},
			wantPositions: []string{PositionInPrefix, PositionStop, PositionAfter, PositionAfter},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/x#2", Verdict: VerdictBlocked, Reasons: []string{ReasonReview}},
		},
		{
			name: "aging ready first member stops the prefix",
			views: []View{
				pv("github:o/r#1", "feat-1", "main", cleanFacts(), testT0, testT0.Add(2*time.Minute)),
			},
			wantPositions: []string{PositionStop},
			wantStop:      &PreviewStop{PRKey: "github:o/r#1", Verdict: VerdictUnknown, Reasons: []string{ReasonAging}},
		},
		{
			name: "never observed member stops",
			views: []View{
				NewView("github:o/r#1", nil, testT0, "", nil),
			},
			wantPositions: []string{PositionStop},
			wantStop:      &PreviewStop{PRKey: "github:o/r#1", Verdict: VerdictUnknown, Reasons: []string{ReasonNotObserved}},
		},
		{
			name: "merged between ready members does not become the edge predecessor",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				pv("github:o/r#2", "feat-2", "release", merged, testT0, testT0.Add(time.Second)),
				readyView("github:o/r#3", "feat-3", "main"),
			},
			wantPositions: []string{PositionInPrefix, PositionMerged, PositionStop},
			wantReady:     1,
			wantStop:      &PreviewStop{PRKey: "github:o/r#3", Verdict: VerdictWaiting, Reasons: []string{ReasonBaseWillMove}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := BuildPreview(tt.views)
			if got := positions(p); !slices.Equal(got, tt.wantPositions) {
				t.Fatalf("positions = %v, want %v", got, tt.wantPositions)
			}
			if p.ReadyCount != tt.wantReady {
				t.Errorf("ReadyCount = %d, want %d", p.ReadyCount, tt.wantReady)
			}
			inPrefix := 0
			for i, m := range p.Members {
				if m.Index != i {
					t.Errorf("member %d Index = %d", i, m.Index)
				}
				if m.Reasons == nil {
					t.Errorf("member %d Reasons nil, want []", i)
				}
				if m.Position == PositionInPrefix {
					inPrefix++
				}
				if m.Position == PositionAfter && !slices.Equal(m.Reasons, []string{ReasonAfterStop}) {
					t.Errorf("after_stop member %d reasons = %v", i, m.Reasons)
				}
			}
			if inPrefix != p.ReadyCount {
				t.Errorf("ReadyCount %d != in_prefix members %d", p.ReadyCount, inPrefix)
			}
			switch {
			case tt.wantStop == nil && p.StoppedBy != nil:
				t.Errorf("StoppedBy = %+v, want nil", p.StoppedBy)
			case tt.wantStop != nil && p.StoppedBy == nil:
				t.Errorf("StoppedBy = nil, want %+v", tt.wantStop)
			case tt.wantStop != nil:
				if p.StoppedBy.PRKey != tt.wantStop.PRKey || p.StoppedBy.Verdict != tt.wantStop.Verdict ||
					!slices.Equal(p.StoppedBy.Reasons, tt.wantStop.Reasons) {
					t.Errorf("StoppedBy = %+v, want %+v", p.StoppedBy, tt.wantStop)
				}
			}
			if p.Fingerprint == "" {
				t.Error("Fingerprint empty")
			}
		})
	}
}

func TestBuildPreviewFingerprint(t *testing.T) {
	a := readyView("github:o/r#1", "feat-1", "main")
	b := readyView("github:o/other#2", "feat-2", "main")
	base := BuildPreview([]View{a, b})

	if again := BuildPreview([]View{a, b}); again.Fingerprint != base.Fingerprint {
		t.Fatal("fingerprint not deterministic")
	}
	if swapped := BuildPreview([]View{b, a}); swapped.Fingerprint == base.Fingerprint {
		t.Fatal("fingerprint ignores member order")
	}

	// Re-read b later with unchanged facts: stable (observed_at is excluded).
	bLater := pv("github:o/other#2", "feat-2", "main", cleanFacts(), testT0.Add(10*time.Second), testT0.Add(11*time.Second))
	if p := BuildPreview([]View{a, bLater}); p.Fingerprint != base.Fingerprint {
		t.Fatal("fingerprint changed when only observed_at changed")
	}

	// A prefix member's snapshot fingerprint changes (optional check now failing).
	changed := withFacts(func(f *Facts) { f.MergeState = Known(MergeStateUnstable) })
	bChanged := pv("github:o/other#2", "feat-2", "main", changed, testT0, testT0.Add(time.Second))
	if bChanged.CurrentVerdict != VerdictReady {
		t.Fatalf("setup: changed member verdict = %q, want ready", bChanged.CurrentVerdict)
	}
	if p := BuildPreview([]View{a, bChanged}); p.Fingerprint == base.Fingerprint || p.ReadyCount != 2 {
		t.Fatalf("fingerprint unchanged (%v) or ready_count %d after prefix member's snapshot changed", p.Fingerprint == base.Fingerprint, p.ReadyCount)
	}
}

func TestBuildPreviewExpiresAt(t *testing.T) {
	now := testT0.Add(50 * time.Second)
	older := pv("github:o/r#1", "feat-1", "main", cleanFacts(), testT0, now)
	newer := pv("github:o/other#2", "feat-2", "main", cleanFacts(), testT0.Add(30*time.Second), now)
	p := BuildPreview([]View{newer, older})
	if p.ExpiresAt == nil {
		t.Fatal("ExpiresAt nil, want oldest prefix observed_at + 60s")
	}
	if want := testT0.Add(FreshFor); !p.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", *p.ExpiresAt, want)
	}

	// A stopped member's (older) snapshot does not count toward expiry.
	blockedOld := pv("github:o/x#3", "feat-3", "main",
		withFacts(func(f *Facts) { f.Review = Known(ReviewRequired) }), testT0.Add(-40*time.Second), now)
	p = BuildPreview([]View{newer, blockedOld})
	if want := testT0.Add(30 * time.Second).Add(FreshFor); p.ExpiresAt == nil || !p.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v (prefix only)", p.ExpiresAt, want)
	}

	// Empty prefix: nil.
	p = BuildPreview([]View{blockedOld})
	if p.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil for empty prefix", *p.ExpiresAt)
	}
	p = BuildPreview(nil)
	if p.ExpiresAt != nil || p.Members == nil {
		t.Fatalf("empty preview = %+v, want nil ExpiresAt and non-nil Members", p)
	}
}

// TestBuildPreviewChecksEveryEarlierSameRepoMember: the edge rule must hold
// against every earlier unmerged prefix member of the same repository, not
// only the immediately preceding member. Interleaving another repository's
// PR (or an unrelated-base PR) between a stacked pair must not let the
// successor into the ready prefix.
func TestBuildPreviewChecksEveryEarlierSameRepoMember(t *testing.T) {
	tests := []struct {
		name  string
		views []View
		want  []string
	}{
		{
			name: "cross-repo PR between a stacked pair",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/other#9", "x", "main"),
				readyView("github:o/r#2", "feat-2", "feat-1"),
			},
			want: []string{ReasonPredecessorRetarget},
		},
		{
			name: "unrelated-base same-repo PR between two PRs on main",
			views: []View{
				readyView("github:o/r#1", "feat-1", "main"),
				readyView("github:o/r#2", "feat-2", "release"),
				readyView("github:o/r#3", "feat-3", "main"),
			},
			want: []string{ReasonBaseWillMove},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := BuildPreview(tt.views)
			if p.StoppedBy == nil || !slices.Equal(p.StoppedBy.Reasons, tt.want) {
				t.Fatalf("positions %v stopped_by %+v, want stop with %v", positions(p), p.StoppedBy, tt.want)
			}
		})
	}
}
