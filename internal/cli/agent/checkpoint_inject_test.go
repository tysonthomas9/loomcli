package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestInjectCheckpointContext(t *testing.T) {
	prompt := `## WORKFLOW: Implementation Task

Some preamble text.

### Step 1: Select ONE Task
Do stuff here.
`
	cp := &config.Checkpoint{
		TaskID:     "loom-123",
		ExitCode:   1,
		ErrorClass: "RateLimited",
		GitDiff:    "+added line",
		Timestamp:  time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}

	result := injectCheckpointContext(prompt, cp)

	// Checkpoint context should appear before Step 1
	cpIdx := strings.Index(result, "PREVIOUS ATTEMPT CONTEXT")
	stepIdx := strings.Index(result, "### Step 1:")
	if cpIdx < 0 {
		t.Fatal("Checkpoint context not found in result")
	}
	if stepIdx < 0 {
		t.Fatal("Step 1 not found in result")
	}
	if cpIdx > stepIdx {
		t.Error("Checkpoint context should appear before Step 1")
	}

	if !strings.Contains(result, "loom-123") {
		t.Error("Result should contain task ID")
	}
	if !strings.Contains(result, "RateLimited") {
		t.Error("Result should contain error class")
	}
	if !strings.Contains(result, "+added line") {
		t.Error("Result should contain git diff")
	}
}

func TestInjectCheckpointContextNoDiff(t *testing.T) {
	prompt := `### Step 1: Do something`
	cp := &config.Checkpoint{
		TaskID:    "loom-456",
		ExitCode:  137,
		Timestamp: time.Now(),
	}

	result := injectCheckpointContext(prompt, cp)
	if !strings.Contains(result, "no uncommitted changes") {
		t.Error("Result should mention no uncommitted changes when diff is empty")
	}
}

// An empty diff plus a scan list is diagnosable: the next agent can see WHERE
// the previous attempt was looked for, rather than only that nothing was found.
func TestInjectCheckpointContextNoDiffListsScannedPaths(t *testing.T) {
	prompt := `### Step 1: Do something`
	cp := &config.Checkpoint{
		TaskID:       "loom-457",
		ExitCode:     137,
		Timestamp:    time.Now(),
		ScannedPaths: []string{"/ws/worktrees/loomcli/worker", "/ws/loomcli"},
	}

	result := injectCheckpointContext(prompt, cp)
	if !strings.Contains(result, "no uncommitted changes (scanned: ") {
		t.Errorf("Result should name the scanned paths, got:\n%s", result)
	}
	for _, want := range []string{"/ws/worktrees/loomcli/worker", "/ws/loomcli"} {
		if !strings.Contains(result, want) {
			t.Errorf("Result should contain scanned path %q, got:\n%s", want, result)
		}
	}
}

func TestInjectCheckpointContextNoStep1(t *testing.T) {
	// Fallback: append to end when "### Step 1:" is not found
	prompt := `Some prompt without steps`
	cp := &config.Checkpoint{
		TaskID:    "loom-789",
		ExitCode:  1,
		GitDiff:   "some diff",
		Timestamp: time.Now(),
	}

	result := injectCheckpointContext(prompt, cp)
	if !strings.Contains(result, "PREVIOUS ATTEMPT CONTEXT") {
		t.Error("Checkpoint context should be appended")
	}
	if !strings.HasPrefix(result, "Some prompt without steps") {
		t.Error("Original prompt should be preserved at start")
	}
}

func TestInjectCheckpointContext_Yield(t *testing.T) {
	prompt := `## WORKFLOW: Implementation Task

Some preamble text.

### Step 1: Select ONE Task
Do stuff here.
`
	cp := &config.Checkpoint{
		TaskID:      "loom-yield-2",
		ExitCode:    0,
		ErrorClass:  "Yielded",
		YieldReason: "manual_stop",
		GitDiff:     "+yield change",
		Timestamp:   time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
	}

	result := injectCheckpointContext(prompt, cp)

	// Should contain yield-specific messaging
	if !strings.Contains(result, "preempted") {
		t.Error("Yield checkpoint should contain 'preempted'")
	}
	if !strings.Contains(result, "manual_stop") {
		t.Error("Yield checkpoint should contain the yield reason 'manual_stop'")
	}
	if !strings.Contains(result, "Continue from where it left off") {
		t.Error("Yield checkpoint should contain 'Continue from where it left off'")
	}

	// Should NOT contain crash messaging
	if strings.Contains(result, "exited with code") {
		t.Error("Yield checkpoint should NOT contain 'exited with code'")
	}
	if strings.Contains(result, "start fresh") {
		t.Error("Yield checkpoint should NOT contain 'start fresh'")
	}

	// Should be injected before Step 1
	cpIdx := strings.Index(result, "PREVIOUS ATTEMPT CONTEXT")
	stepIdx := strings.Index(result, "### Step 1:")
	if cpIdx < 0 {
		t.Fatal("Checkpoint context not found in result")
	}
	if stepIdx < 0 {
		t.Fatal("Step 1 not found in result")
	}
	if cpIdx > stepIdx {
		t.Error("Checkpoint context should appear before Step 1")
	}
}

func TestInjectCheckpointContext_CrashUnchanged(t *testing.T) {
	prompt := `## WORKFLOW: Implementation Task

### Step 1: Select ONE Task
Do stuff here.
`
	cp := &config.Checkpoint{
		TaskID:     "loom-crash-1",
		ExitCode:   1,
		ErrorClass: "RateLimited",
		GitDiff:    "+crash change",
		Timestamp:  time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
	}

	result := injectCheckpointContext(prompt, cp)

	// Should contain crash-specific messaging
	if !strings.Contains(result, "exited with code") {
		t.Error("Crash checkpoint should contain 'exited with code'")
	}
	if !strings.Contains(result, "start fresh") {
		t.Error("Crash checkpoint should contain 'start fresh'")
	}

	// Should NOT contain yield messaging
	if strings.Contains(result, "preempted") {
		t.Error("Crash checkpoint should NOT contain 'preempted'")
	}
	if strings.Contains(result, "Continue from where it left off") {
		t.Error("Crash checkpoint should NOT contain yield-specific 'Continue from where it left off'")
	}
}
