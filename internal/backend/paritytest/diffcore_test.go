//go:build parity

package paritytest

import (
	"reflect"
	"testing"
)

// TestDiffMaps_Basic confirms the shared routine behaves the same as the
// pre-extraction diffMaps: nil input → nil, equal inputs → no rows, a
// differing field emits one row, ignored keys are skipped.
func TestDiffMaps_Basic(t *testing.T) {
	// Nil in, nil out.
	if got := DiffMaps(DiffOpts{}, "fx", "s1", "m1", nil, nil); got != nil {
		t.Errorf("nil/nil: got %v, want nil", got)
	}

	// Identical maps → no diffs.
	m := map[string]any{"title": "hi", "priority": 2}
	if got := DiffMaps(DiffOpts{}, "fx", "s1", "m1", m, m); got != nil {
		t.Errorf("identical maps: got %v, want nil", got)
	}

	// One differing field → one row with the correct shape.
	left := map[string]any{"title": "hi", "priority": 2}
	right := map[string]any{"title": "bye", "priority": 2}
	diffs := DiffMaps(DiffOpts{}, "fx", "s1", "m1", left, right)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %v", len(diffs), diffs)
	}
	if diffs[0].Field != "title" {
		t.Errorf("field: got %q want title", diffs[0].Field)
	}
	if diffs[0].FleetDB != "hi" || diffs[0].Beads != "bye" {
		t.Errorf("columns: got %v/%v, want hi/bye", diffs[0].FleetDB, diffs[0].Beads)
	}

	// Ignored fields suppressed.
	ignored := DiffOpts{Ignored: map[string]bool{"title": true}}
	if got := DiffMaps(ignored, "fx", "s1", "m1", left, right); got != nil {
		t.Errorf("ignored field should produce no diffs, got %v", got)
	}
}

// TestDiffMaps_Aliases confirms alias-collapsing: two maps that use
// different names for the same field (issue_type vs type) should show as
// equal after DiffOpts.Aliases is applied.
func TestDiffMaps_Aliases(t *testing.T) {
	aliases := map[string]string{"issue_type": "type"}
	left := map[string]any{"type": "task"}
	right := map[string]any{"issue_type": "task"}
	diffs := DiffMaps(DiffOpts{Aliases: aliases}, "fx", "s1", "m1", left, right)
	if len(diffs) != 0 {
		t.Errorf("aliased equal values: got %d diffs, want 0: %v", len(diffs), diffs)
	}
}

// TestDiffMaps_CustomEqual confirms the Equal hook overrides the default
// equality check. Used by CLI-side diffMapsCLI to handle priority
// normalization ("P2" vs 2).
func TestDiffMaps_CustomEqual(t *testing.T) {
	called := false
	opts := DiffOpts{
		Equal: func(_ string, a, b any) bool {
			called = true
			// Treat everything as equal in this test.
			return true
		},
	}
	left := map[string]any{"x": 1}
	right := map[string]any{"x": "one"}
	diffs := DiffMaps(opts, "fx", "s1", "m1", left, right)
	if !called {
		t.Error("custom Equal was never invoked")
	}
	if len(diffs) != 0 {
		t.Errorf("custom Equal returned true; expected 0 diffs, got %d", len(diffs))
	}
}

// TestDiffMaps_NormalizeMap confirms the NormalizeMap hook runs before
// aliasing and diffing. Used to hoist fleet-db's nested {issue, blockers}
// wrapper up to a flat shape before comparison.
func TestDiffMaps_NormalizeMap(t *testing.T) {
	opts := DiffOpts{
		NormalizeMap: func(m map[string]any) map[string]any {
			// Flatten a nested "inner" wrapper into top-level keys.
			if inner, ok := m["inner"].(map[string]any); ok {
				out := map[string]any{}
				for k, v := range inner {
					out[k] = v
				}
				return out
			}
			return m
		},
	}
	left := map[string]any{"inner": map[string]any{"x": 1}}
	right := map[string]any{"x": 1}
	diffs := DiffMaps(opts, "fx", "s1", "m1", left, right)
	if len(diffs) != 0 {
		t.Errorf("normalized equal maps: got %d diffs, want 0: %v", len(diffs), diffs)
	}
}

// TestDiffMaps_DeterministicOrder confirms DiffMaps emits rows in sorted
// field order so diff reports reproduce identically across runs — a
// regression here would make report diffs noisy between CI runs even
// when the underlying data is identical.
func TestDiffMaps_DeterministicOrder(t *testing.T) {
	left := map[string]any{"a": 1, "b": 1, "c": 1}
	right := map[string]any{"a": 2, "b": 2, "c": 2}
	diffs := DiffMaps(DiffOpts{}, "fx", "s1", "m1", left, right)
	got := []string{diffs[0].Field, diffs[1].Field, diffs[2].Field}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order: got %v want %v", got, want)
	}
}

// TestDiffMaps_NilEmptyString confirms the default Equal treats nil and
// the empty string as equivalent — loom's IssueBackend implementations
// are inconsistent here and we don't want that to be the noise source.
func TestDiffMaps_NilEmptyString(t *testing.T) {
	left := map[string]any{"description": ""}
	right := map[string]any{"description": nil}
	if got := DiffMaps(DiffOpts{}, "fx", "s1", "m1", left, right); got != nil {
		t.Errorf("nil/empty-string: got %v, want nil", got)
	}
}
