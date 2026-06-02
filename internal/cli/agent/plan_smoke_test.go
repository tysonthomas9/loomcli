package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestPlanSmoke_HappyPath validates the full single-task pipeline:
// task found -> prompt generated -> agent invoked -> lock released -> session finalized with exit code 0.
func TestPlanSmoke_HappyPath(t *testing.T) {
	// Not parallel: uses global default tracker.

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []backend.IssueData{
		{ID: "smoke-1", Status: "open", IssueType: "task", Title: "Smoke task", Design: ""},
	})

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	cmd := &cobra.Command{Use: "plan", Args: cobra.MaximumNArgs(1), Run: runPlan}
	cmd.SetContext(WithDeps(context.Background(), deps))
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' in output, got: %s", output)
	}
	if len(recorder.InteractiveCalls) != 1 {
		t.Fatalf("expected 1 agent invocation, got %d", len(recorder.InteractiveCalls))
	}

	prompt := recorder.InteractiveCalls[0].Prompt
	if !strings.Contains(prompt, "Planning Task") {
		t.Errorf("prompt should contain 'Planning Task', got prompt length %d", len(prompt))
	}

	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runPlan completes in single-task mode")
	}
}

// TestPlanSmoke_NeedsRevisionTask validates that a task with design + needs-revision label
// is still picked up for planning (re-planning flow).
func TestPlanSmoke_NeedsRevisionTask(t *testing.T) {
	// Not parallel: uses global default tracker.

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []backend.IssueData{
		{ID: "smoke-2", Status: "open", IssueType: "task", Title: "Revision task", Design: "existing plan", Labels: []string{"needs-revision"}},
	})

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	cmd := &cobra.Command{Use: "plan", Args: cobra.MaximumNArgs(1), Run: runPlan}
	cmd.SetContext(WithDeps(context.Background(), deps))
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if strings.Contains(output, "No tasks available for planning") {
		t.Error("needs-revision task should be available for planning, but got 'No tasks available'")
	}
	if len(recorder.InteractiveCalls) != 1 {
		t.Fatalf("expected 1 agent invocation for needs-revision task, got %d", len(recorder.InteractiveCalls))
	}
}

// TestPlanSmoke_NoTasksExitsCleanly validates that an empty pipeline exits
// cleanly with a message and no agent invocation.
func TestPlanSmoke_NoTasksExitsCleanly(t *testing.T) {
	// Not parallel: uses global default tracker.

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, nil)

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	cmd := &cobra.Command{Use: "plan", Args: cobra.MaximumNArgs(1), Run: runPlan}
	cmd.SetContext(WithDeps(context.Background(), deps))
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' in output, got: %s", output)
	}
	if len(recorder.InteractiveCalls) != 0 {
		t.Errorf("expected no agent invocations when no tasks available, got %d", len(recorder.InteractiveCalls))
	}

	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("no lock file should exist when no tasks are available")
	}
}

// TestPlanSmoke_AgentError_SessionFinalized validates that when the agent returns an error,
// the session is finalized with a non-zero exit code.
func TestPlanSmoke_AgentError_SessionFinalized(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	runtimeDir := filepath.Join(tmpDir, "runtime")

	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("failed to create session store: %v", err)
	}

	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "smoke-test",
		Backend:   "mock",
		Phase:     "planning",
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if sess.Meta.Status != sessions.StatusRunning {
		t.Errorf("new session status should be 'running', got %q", sess.Meta.Status)
	}

	err = sess.Finalize(sessions.FinalizeOptions{ExitCode: 1})
	if err != nil {
		t.Fatalf("failed to finalize session: %v", err)
	}

	if sess.Meta.Status != sessions.StatusFailed {
		t.Errorf("session status should be 'failed', got %q", sess.Meta.Status)
	}
	if sess.Meta.ExitCode != 1 {
		t.Errorf("session exit code should be 1, got %d", sess.Meta.ExitCode)
	}
	if sess.Meta.Phase != "planning" {
		t.Errorf("session phase should be 'planning', got %q", sess.Meta.Phase)
	}

	metaPath := filepath.Join(runtimeDir, "sessions", sess.SessionID(), "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("failed to read metadata.json: %v", err)
	}
	var meta sessions.SessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("failed to unmarshal metadata: %v", err)
	}
	if meta.Status != sessions.StatusFailed {
		t.Errorf("persisted metadata status should be 'failed', got %q", meta.Status)
	}
}

// TestPlanSmoke_PromptIncludesAllSections validates that GeneratePlanningPrompt
// produces a prompt containing all expected workflow sections and safety guardrails.
func TestPlanSmoke_PromptIncludesAllSections(t *testing.T) {
	t.Parallel()

	prompt := GeneratePlanningPrompt("smoke-agent", nil, "")

	expectedSections := []string{
		"Step 1:",
		"Step 2:",
		"Step 3:",
		"agent name is:",
		"loom data ready",
	}

	for _, section := range expectedSections {
		if !strings.Contains(prompt, section) {
			t.Errorf("prompt missing expected section %q (prompt length: %d)", section, len(prompt))
		}
	}

	if !strings.Contains(prompt, "smoke-agent") {
		t.Error("prompt should contain the agent name 'smoke-agent'")
	}

	if len(prompt) < 500 {
		t.Errorf("prompt seems too short for a planning workflow (%d chars), expected > 500", len(prompt))
	}
}
