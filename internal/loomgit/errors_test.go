package loomgit

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestEveryErrorCodeRoundTrips(t *testing.T) {
	want := []Code{
		"review_required", "conflict", "stale", "capture_incomplete", "unsaved_work",
		"protected", "repo_selection_required", "lineage_unresolved", "task_copy_create_failed",
		"base_ref_unresolvable", "stale_subject", "stack_locked", "stack_not_linear",
		"ref_namespace_conflict", "provider_stack_limit", "diverged", "dependency_abandoned",
		"merge_not_authorized", "merge_blocked", "mode_mismatch", "integrity_missing",
		"attention_required", "apply_pending", "swap_held", "restack_conflict",
		"revision_superseded", "hash_mismatch", "capture_failed", "secret_path_refused",
		"workspace_unsupported",
	}
	if !slices.Equal(ErrorCodes, want) {
		t.Fatalf("error codes differ from naming standards: got %v, want %v", ErrorCodes, want)
	}
	seen := map[Code]bool{}
	for _, code := range ErrorCodes {
		if seen[code] {
			t.Fatalf("duplicate code %s", code)
		}
		seen[code] = true
		cause := errors.New("cause")
		err := fmt.Errorf("outer: %w", NewError(code, "detail", cause))
		if !errors.Is(err, NewError(code, "", nil)) {
			t.Fatalf("Is failed for %s", code)
		}
		var typed *Error
		if !errors.As(err, &typed) || typed.Code() != string(code) || !errors.Is(err, cause) {
			t.Fatalf("As/Code/Unwrap failed for %s: %v", code, err)
		}
	}
}
