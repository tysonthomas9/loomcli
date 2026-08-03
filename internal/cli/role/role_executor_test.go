package role

import (
	"strings"
	"testing"
)

// The executor is a closed vocabulary; a typo must fail at the CLI with the
// accepted values, not as a server 400 after the round trip.
func TestBuildRolePatch_ExecutorVocabulary(t *testing.T) {
	for _, valid := range []string{"turn", "conversation", ""} {
		patch, err := buildRolePatch("executor", valid, false)
		if err != nil {
			t.Fatalf("executor %q must be accepted: %v", valid, err)
		}
		if patch.Executor == nil || *patch.Executor != valid {
			t.Fatalf("patch.Executor = %v, want %q", patch.Executor, valid)
		}
	}

	if _, err := buildRolePatch("executor", "chatty", false); err == nil ||
		!strings.Contains(err.Error(), "conversation") {
		t.Fatalf("an unknown executor must be rejected naming the vocabulary, got %v", err)
	}
}
