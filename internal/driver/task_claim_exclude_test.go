package driver

import (
	"context"
	"testing"

	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
)

// An epic drain claims label-blind: any open, unblocked child is fair game.
// That silently dispatches work that is mid-flight in a label-routed pipeline
// to a generic implementer. ExcludeLabels is the opt-in guard.
func TestClaimReadyTask_SkipsExcludedLabels(t *testing.T) {
	ready := []backend.IssueData{
		{ID: "T-1", Status: "open", Labels: []string{"criticized", "review-cycle=1"}},
		{ID: "T-2", Status: "open", Labels: []string{"ready-to-implement"}},
	}
	var attempted []string
	m := clitest.NewMockIssueBackend()
	m.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) { return ready, nil }
	m.ClaimIssueFn = func(_ context.Context, id string, _ time.Duration) error {
		attempted = append(attempted, id)
		return nil
	}

	got, err := ClaimReadyTask(context.Background(), m, TaskClaimOptions{
		EpicID:        "E-1",
		ExcludeLabels: []string{"criticized"},
	})
	if err != nil {
		t.Fatalf("ClaimReadyTask: %v", err)
	}
	if got == nil || got.ID != "T-2" {
		t.Fatalf("claimed = %+v, want T-2 (T-1 carries an excluded label)", got)
	}
	for _, id := range attempted {
		if id == "T-1" {
			t.Fatal("must not even attempt to claim an excluded task")
		}
	}
}

func TestClaimReadyTask_ExcludeIsCaseInsensitiveAndOptional(t *testing.T) {
	ready := []backend.IssueData{{ID: "T-1", Status: "open", Labels: []string{"Criticized"}}}
	m := clitest.NewMockIssueBackend()
	m.ReadyFn = func(_ context.Context, _ backend.ReadyOpts) ([]backend.IssueData, error) { return ready, nil }
	m.ClaimIssueFn = func(_ context.Context, _ string, _ time.Duration) error { return nil }

	got, err := ClaimReadyTask(context.Background(), m, TaskClaimOptions{ExcludeLabels: []string{"criticized"}})
	if err != nil || got != nil {
		t.Fatalf("case-insensitive exclude should skip everything; got %+v err %v", got, err)
	}

	// No excludes configured ⇒ unchanged behavior.
	got, err = ClaimReadyTask(context.Background(), m, TaskClaimOptions{})
	if err != nil || got == nil || got.ID != "T-1" {
		t.Fatalf("without excludes the task must still be claimed; got %+v err %v", got, err)
	}
}

func TestNormalizeLabelSetAndHasAnyLabel(t *testing.T) {
	set := normalizeLabelSet([]string{" Criticized ", "", "  "})
	if len(set) != 1 {
		t.Fatalf("normalizeLabelSet dropped blanks incorrectly: %v", set)
	}
	if !hasAnyLabel([]string{"other", "CRITICIZED"}, set) {
		t.Fatal("hasAnyLabel must match case-insensitively")
	}
	if hasAnyLabel([]string{"other"}, set) {
		t.Fatal("hasAnyLabel false positive")
	}
	if hasAnyLabel([]string{"anything"}, nil) {
		t.Fatal("an empty set must never match")
	}
}
