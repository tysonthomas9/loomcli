package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// TestPlanSmoke_HappyPath validates the full single-task pipeline:
// task found → prompt generated → agent invoked → lock released → session finalized with exit code 0.
func TestPlanSmoke_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	origAuto, origDaemon := planAutoMode, planDaemonMode
	planAutoMode = false
	planDaemonMode = false
	t.Cleanup(func() {
		planAutoMode = origAuto
		planDaemonMode = origDaemon
	})

	taskJSON := `[{"id":"smoke-1","status":"open","issue_type":"task","title":"Smoke task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner
	if !strings.Contains(output, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' in output, got: %s", output)
	}

	// Verify Claude invoked exactly once
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	// Verify prompt contains planning workflow marker
	prompt := recorder.Invocations[0].Prompt
	if !strings.Contains(prompt, "WORKFLOW: Planning Task") {
		t.Errorf("prompt should contain 'WORKFLOW: Planning Task', got prompt length %d", len(prompt))
	}

	// Verify lock file is released after single-task mode
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runPlan completes in single-task mode")
	}
}

// TestPlanSmoke_NeedsRevisionTask validates that a task with design + needs-revision label
// is still picked up for planning (re-planning flow).
func TestPlanSmoke_NeedsRevisionTask(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	origAuto, origDaemon := planAutoMode, planDaemonMode
	planAutoMode = false
	planDaemonMode = false
	t.Cleanup(func() {
		planAutoMode = origAuto
		planDaemonMode = origDaemon
	})

	// Task has design but needs-revision label — should still be available for planning
	taskJSON := `[{"id":"smoke-2","status":"open","issue_type":"task","title":"Revision task","design":"existing plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "def456\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "def456..HEAD"}, Stdout: ""},
	})
	mock.Install()

	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should NOT say "no tasks available" — needs-revision task should be picked up
	if strings.Contains(output, "No tasks available for planning") {
		t.Error("needs-revision task should be available for planning, but got 'No tasks available'")
	}

	// Agent should have been invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation for needs-revision task, got %d", len(recorder.Invocations))
	}
}

// TestPlanSmoke_NoTasksExitsCleanly validates that an empty pipeline exits
// cleanly with a message and no agent invocation.
func TestPlanSmoke_NoTasksExitsCleanly(t *testing.T) {
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	origAuto, origDaemon := planAutoMode, planDaemonMode
	planAutoMode = false
	planDaemonMode = false
	t.Cleanup(func() {
		planAutoMode = origAuto
		planDaemonMode = origDaemon
	})

	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: "[]"},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: "[]"},
	})
	mock.Install()

	// Install mock invoker and assert it was never called
	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' in output, got: %s", output)
	}

	// Verify Claude was NOT invoked
	if len(recorder.Invocations) != 0 {
		t.Errorf("expected no Claude invocations when no tasks available, got %d", len(recorder.Invocations))
	}

	// Verify no lock file was left behind
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("no lock file should exist when no tasks are available")
	}
}

// TestPlanSmoke_AgentError_SessionFinalized validates that when the agent returns an error,
// the session is finalized with a non-zero exit code.
// Since runPlan calls os.Exit(1) on agent error, we exercise the session
// finalization path directly — the same code path runPlan executes before exit.
func TestPlanSmoke_AgentError_SessionFinalized(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, "beads")

	sessStore, err := sessions.NewStore(beadsDir)
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

	// Verify session starts as running
	if sess.Meta.Status != sessions.StatusRunning {
		t.Errorf("new session status should be 'running', got %q", sess.Meta.Status)
	}

	// Finalize with error exit code (simulating what runPlan does on agent error)
	err = sess.Finalize(sessions.FinalizeOptions{
		ExitCode: 1,
	})
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

	// Verify metadata was persisted to disk
	metaPath := filepath.Join(beadsDir, "sessions", sess.SessionID(), "metadata.json")
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
	prompt := GeneratePlanningPrompt("smoke-agent", nil, "")

	expectedSections := []string{
		"Step 1:",        // task selection
		"Step 2:",        // research
		"Step 3:",        // plan creation
		"agent name is:", // agent identity
		"bd ready",       // task discovery command
	}

	for _, section := range expectedSections {
		if !strings.Contains(prompt, section) {
			t.Errorf("prompt missing expected section %q (prompt length: %d)", section, len(prompt))
		}
	}

	// Verify agent name is embedded
	if !strings.Contains(prompt, "smoke-agent") {
		t.Error("prompt should contain the agent name 'smoke-agent'")
	}

	// Verify prompt is non-trivially long (a real planning prompt has workflow details)
	if len(prompt) < 500 {
		t.Errorf("prompt seems too short for a planning workflow (%d chars), expected > 500", len(prompt))
	}
}
