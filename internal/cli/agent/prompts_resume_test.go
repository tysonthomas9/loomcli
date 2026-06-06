package agent

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// TestInjectCheckpointIfNotResuming locks the P4 resume-first / checkpoint-fallback
// behavior: the prior-attempt checkpoint is injected only when NOT resuming; when
// a session resume is armed, it is skipped (the resumed session already carries
// the context, so re-injecting the git-diff block would re-pay for it).
func TestInjectCheckpointIfNotResuming(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("LOOM_WORKTREE_PATH", wt)
	if err := config.SaveCheckpoint(cli.ResolveLockDir(wt), &config.Checkpoint{
		AgentName: "a", TaskID: "t", GitDiff: "diff --git a/x b/x", ExitCode: 1,
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	const marker = "PREVIOUS ATTEMPT CONTEXT"

	// Not resuming → checkpoint injected (the fallback path).
	backends.ClearResumeSessionID()
	if got := injectCheckpointIfNotResuming("BASE"); !strings.Contains(got, marker) {
		t.Errorf("not resuming: expected checkpoint context injected, got:\n%s", got)
	}

	// Resuming → checkpoint SKIPPED, prompt unchanged.
	backends.SetResumeSessionID("resume-uuid")
	defer backends.ClearResumeSessionID()
	got := injectCheckpointIfNotResuming("BASE")
	if strings.Contains(got, marker) {
		t.Errorf("resuming: checkpoint context should be skipped, got:\n%s", got)
	}
	if got != "BASE" {
		t.Errorf("resuming: prompt should be unchanged, got %q", got)
	}
}

// TestInjectCheckpointNoCheckpointNoop confirms that with no checkpoint on disk
// the prompt is returned unchanged (and never errors), resuming or not.
func TestInjectCheckpointNoCheckpointNoop(t *testing.T) {
	t.Setenv("LOOM_WORKTREE_PATH", t.TempDir()) // no checkpoint saved
	backends.ClearResumeSessionID()
	if got := injectCheckpointIfNotResuming("BASE"); got != "BASE" {
		t.Errorf("no checkpoint: prompt should be unchanged, got %q", got)
	}
}

func TestFleetPromptsInjectCheckpointFallback(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("LOOM_WORKTREE_PATH", wt)
	if err := config.SaveCheckpoint(cli.ResolveLockDir(wt), &config.Checkpoint{
		AgentName: "a", TaskID: "t", GitDiff: "diff --git a/x b/x", ExitCode: 1,
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	backends.ClearResumeSessionID()

	for name, prompt := range map[string]string{
		"planning": GenerateFleetPlanningPrompt("planner", "T-1", nil),
		"task":     GenerateFleetTaskPrompt("coder", "T-2", nil, "claude"),
	} {
		if !strings.Contains(prompt, "PREVIOUS ATTEMPT CONTEXT") {
			t.Fatalf("%s prompt did not include checkpoint fallback:\n%s", name, prompt)
		}
	}
}

func TestFleetPromptsSkipCheckpointWhenResuming(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("LOOM_WORKTREE_PATH", wt)
	if err := config.SaveCheckpoint(cli.ResolveLockDir(wt), &config.Checkpoint{
		AgentName: "a", TaskID: "t", GitDiff: "diff --git a/x b/x", ExitCode: 1,
	}); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	backends.SetResumeSessionID("resume-uuid")
	defer backends.ClearResumeSessionID()

	for name, prompt := range map[string]string{
		"planning": GenerateFleetPlanningPrompt("planner", "T-1", nil),
		"task":     GenerateFleetTaskPrompt("coder", "T-2", nil, "claude"),
	} {
		if strings.Contains(prompt, "PREVIOUS ATTEMPT CONTEXT") {
			t.Fatalf("%s prompt included checkpoint while resume was armed:\n%s", name, prompt)
		}
	}
}
