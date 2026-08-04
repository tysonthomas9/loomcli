package backends

import (
	"slices"
	"testing"
)

func TestAppendCodexEffortArgsMapsMaxToXHigh(t *testing.T) {
	got := appendCodexEffortArgs([]string{"exec"}, "max")
	want := []string{"-c", `model_reasoning_effort="xhigh"`, "exec"}
	if !slices.Equal(got, want) {
		t.Fatalf("appendCodexEffortArgs() = %v, want %v", got, want)
	}
}

func TestAppendCodexEffortArgsSkipsEmpty(t *testing.T) {
	args := []string{"exec"}
	got := appendCodexEffortArgs(args, "")
	if !slices.Equal(got, args) {
		t.Fatalf("appendCodexEffortArgs() = %v, want %v", got, args)
	}
}

func TestBuildClaudeRunTurnArgsAddsEffort(t *testing.T) {
	t.Setenv("LOOM_AGENT_EFFORT", "high")

	got := buildClaudeRunTurnArgs("session-1")

	if !slices.Contains(got, "--effort") {
		t.Fatalf("expected --effort in args, got %v", got)
	}
	for i, arg := range got {
		if arg == "--effort" {
			if i+1 >= len(got) {
				t.Fatalf("--effort missing value; args=%v", got)
			}
			if got[i+1] != "high" {
				t.Fatalf("--effort value = %q, want high; args=%v", got[i+1], got)
			}
			return
		}
	}
}
