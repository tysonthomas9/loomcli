package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// decomposedListFn builds a ListFn that answers the label query with parents
// and the ParentID query from kids, so a test only has to declare the shape of
// the board it wants.
func decomposedListFn(parents []backend.IssueData, kids map[string][]backend.IssueData) func(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if opts.ParentID != "" {
			return kids[opts.ParentID], nil
		}
		if len(opts.Labels) == 1 && opts.Labels[0] == "decomposed" {
			return parents, nil
		}
		return nil, fmt.Errorf("unexpected list opts: %+v", opts)
	}
}

func TestCheckDecomposedWithoutChildren(t *testing.T) {
	t.Parallel()

	t.Run("skipped when no issue backend", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _ := NewTestDeps(t)
		deps.IssueBackend = nil

		if result := checkDecomposedWithoutChildren(deps); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
		if result := checkDecomposedWithoutChildren(nil); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result for nil deps, got %+v", result)
		}
	})

	t.Run("skipped when list fails", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListErr = errors.New("fleet-db unreachable")

		if result := checkDecomposedWithoutChildren(deps); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	t.Run("skipped when no decomposed issues", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(nil, nil)

		if result := checkDecomposedWithoutChildren(deps); result != (CheckResult{}) {
			t.Errorf("expected empty (skipped) result, got %+v", result)
		}
	})

	t.Run("pass when every decomposed issue has children", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{{ID: "PUPPET-1", Status: "blocked"}},
			map[string][]backend.IssueData{"PUPPET-1": {{ID: "PUPPET-2", Status: "open"}}},
		)

		result := checkDecomposedWithoutChildren(deps)
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Name != "decomposed_without_children" {
			t.Errorf("unexpected name: %s", result.Name)
		}
		if result.Summary != "no decomposed issues without children (1 checked)" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
	})

	t.Run("closed and tombstoned parents are ignored", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{
				{ID: "PUPPET-1", Status: "closed"},
				{ID: "PUPPET-2", Status: "tombstone"},
			},
			nil,
		)

		result := checkDecomposedWithoutChildren(deps)
		if result.Status != StatusPass {
			t.Fatalf("expected pass, got %v: %s", result.Status, result.Summary)
		}
		if result.Summary != "no decomposed issues without children (0 checked)" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
	})

	t.Run("warn lists offenders with remediation", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		mockBackend.ListFn = decomposedListFn(
			[]backend.IssueData{
				{ID: "PUPPET-295", Status: "blocked"},
				{ID: "PUPPET-285", Status: "in_progress"},
				{ID: "PUPPET-300", Status: "blocked"},
				{ID: "PUPPET-9", Status: "closed"},
			},
			map[string][]backend.IssueData{"PUPPET-300": {{ID: "PUPPET-301"}}},
		)

		result := checkDecomposedWithoutChildren(deps)
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if result.Summary != "2 decomposed issue(s) have no children" {
			t.Errorf("unexpected summary: %s", result.Summary)
		}
		for _, want := range []string{
			"issue=PUPPET-285 status=in_progress children=0",
			"issue=PUPPET-295 status=blocked children=0",
			"remediation: the split lost its parent links",
			"`loom data update <child> --parent <parent>`",
		} {
			if !strings.Contains(result.Detail, want) {
				t.Errorf("detail missing %q:\n%s", want, result.Detail)
			}
		}
		if strings.Contains(result.Detail, "PUPPET-300") || strings.Contains(result.Detail, "PUPPET-9") {
			t.Errorf("detail names an issue that is not an offender:\n%s", result.Detail)
		}
	})

	t.Run("truncates rather than fanning out unbounded child queries", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, mockBackend := NewTestDeps(t)
		parents := make([]backend.IssueData, 0, maxDecomposedScan+1)
		for i := 0; i <= maxDecomposedScan; i++ {
			parents = append(parents, backend.IssueData{ID: fmt.Sprintf("PUPPET-%d", i), Status: "blocked"})
		}
		childQueries := 0
		base := decomposedListFn(parents, nil)
		mockBackend.ListFn = func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
			if opts.ParentID != "" {
				childQueries++
			}
			return base(ctx, opts)
		}

		result := checkDecomposedWithoutChildren(deps)
		if result.Status != StatusWarn {
			t.Fatalf("expected warn, got %v: %s", result.Status, result.Summary)
		}
		if childQueries != 0 {
			t.Errorf("expected no child queries when truncating, got %d", childQueries)
		}
		if !strings.Contains(result.Summary, "too many decomposed issues") {
			t.Errorf("summary does not name the cap: %s", result.Summary)
		}
		if !strings.Contains(result.Detail, "truncated:") {
			t.Errorf("detail does not name the truncation:\n%s", result.Detail)
		}
	})
}
