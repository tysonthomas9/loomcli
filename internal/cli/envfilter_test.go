package cli

import (
	"slices"
	"testing"
)

func TestFilterEnv_AllowsExactMatches(t *testing.T) {
	input := []string{"PATH=/usr/bin", "HOME=/root"}
	got := FilterEnv(input)
	if len(got) != 2 {
		t.Fatalf("FilterEnv() returned %d entries, want 2", len(got))
	}
	if got[0] != "PATH=/usr/bin" {
		t.Errorf("got[0] = %q, want %q", got[0], "PATH=/usr/bin")
	}
	if got[1] != "HOME=/root" {
		t.Errorf("got[1] = %q, want %q", got[1], "HOME=/root")
	}
}

func TestFilterEnv_AllowsPrefixMatches(t *testing.T) {
	input := []string{
		"LOOM_WORKTREE_PATH=/foo",
		"BD_ACTOR=agent1",
		"BEADS_DB=/bar",
	}
	got := FilterEnv(input)
	if len(got) != 3 {
		t.Fatalf("FilterEnv() returned %d entries, want 3", len(got))
	}
	for i, want := range input {
		if got[i] != want {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestFilterEnv_BlocksSensitiveVars(t *testing.T) {
	input := []string{
		"AWS_SECRET_ACCESS_KEY=secret",
		"DB_PASSWORD=pass123",
		"MY_SECRET_TOKEN=tok",
	}
	got := FilterEnv(input)
	if len(got) != 0 {
		t.Errorf("FilterEnv() returned %d entries, want 0; got %v", len(got), got)
	}
}

func TestFilterEnv_MixedInput(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"AWS_SECRET_ACCESS_KEY=secret",
		"LOOM_WORKTREE_PATH=/foo",
		"DB_PASSWORD=pass123",
		"HOME=/root",
		"UNKNOWN_VAR=hello",
		"BD_ACTOR=agent1",
	}
	got := FilterEnv(input)
	want := []string{
		"PATH=/usr/bin",
		"LOOM_WORKTREE_PATH=/foo",
		"HOME=/root",
		"BD_ACTOR=agent1",
	}
	if len(got) != len(want) {
		t.Fatalf("FilterEnv() returned %d entries, want %d; got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterEnv_EmptyInput(t *testing.T) {
	// Empty slice should return empty (not nil) slice.
	got := FilterEnv([]string{})
	if got == nil {
		t.Fatal("FilterEnv([]string{}) returned nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("FilterEnv([]string{}) returned %d entries, want 0", len(got))
	}

	// Nil input should also return empty (not nil) slice.
	got = FilterEnv(nil)
	if got == nil {
		t.Fatal("FilterEnv(nil) returned nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("FilterEnv(nil) returned %d entries, want 0", len(got))
	}
}

func TestFilterEnv_PrefixDoesNotMatchExact(t *testing.T) {
	// PATH is in the exact allowlist, but PATHOLOGICAL should not match
	// because exact matches require the full variable name, not a prefix.
	input := []string{"PATHOLOGICAL=1"}
	got := FilterEnv(input)
	if len(got) != 0 {
		t.Errorf("FilterEnv() returned %d entries, want 0; got %v", len(got), got)
	}
}

func TestFilterEnv_MalformedEntries(t *testing.T) {
	input := []string{
		"NO_EQUALS_SIGN",
		"PATH=/usr/bin",
		"ALSO_MALFORMED",
		"HOME=/root",
		"",
	}
	got := FilterEnv(input)
	want := []string{"PATH=/usr/bin", "HOME=/root"}
	if len(got) != len(want) {
		t.Fatalf("FilterEnv() returned %d entries, want %d; got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilteredEnv_ReturnsFilteredOsEnviron(t *testing.T) {
	// PATH is virtually always set in the environment.
	// Set an additional known variable to verify prefix matching.
	t.Setenv("LOOM_TEST_ENVFILTER", "hello")

	got := FilteredEnv()

	// Check that PATH appears in the result.
	foundPATH := slices.ContainsFunc(got, func(s string) bool {
		return len(s) > 5 && s[:5] == "PATH="
	})
	if !foundPATH {
		t.Error("FilteredEnv() does not contain PATH, expected it to be present")
	}

	// Check that our LOOM_ prefixed var appears.
	foundLoom := slices.ContainsFunc(got, func(s string) bool {
		const prefix = "LOOM_TEST_ENVFILTER="
		return len(s) >= len(prefix) && s[:len(prefix)] == prefix
	})
	if !foundLoom {
		t.Error("FilteredEnv() does not contain LOOM_TEST_ENVFILTER, expected it to be present")
	}
}
