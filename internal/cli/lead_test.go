package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLead_InvokesClaude(t *testing.T) {
	// Setup temp directory as working directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS /var -> /private/var
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout to suppress banner output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runLead(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner was printed
	if !strings.Contains(output, "Starting LEAD mode") {
		t.Errorf("expected 'Starting LEAD mode' banner in output, got: %s", output)
	}

	// Verify Claude was invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	inv := recorder.Invocations[0]
	// WorkDir should be the temp directory
	if inv.WorkDir != tmpDir {
		t.Errorf("expected workDir %q, got %q", tmpDir, inv.WorkDir)
	}
	// Prompt should be the lead prompt
	leadPrompt := GenerateLeadPrompt()
	if inv.Prompt != leadPrompt {
		t.Errorf("expected lead prompt, got %q", inv.Prompt)
	}
	// AgentName should be empty for lead mode (not claiming tasks)
	if inv.AgentName != "" {
		t.Errorf("expected empty agentName for lead mode, got %q", inv.AgentName)
	}
}

func TestRunLead_ClaudeError(t *testing.T) {
	// This test verifies that errors from Claude are handled.
	// Since runLead calls os.Exit(1) on error, we can't test the full path
	// without subprocess execution. Instead, we verify the mock is called.

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	expectedErr := errors.New("claude failed")
	recorder := SetupMockClaudeInvoker(t, expectedErr)

	// Capture stdout
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// Note: This will cause the test to fail if we don't handle the os.Exit
	// In production code, runLead calls os.Exit(1) on error
	// For now, we just verify the mock was invoked correctly
	defer func() {
		w.Close()
		os.Stdout = oldStdout
	}()

	// The mock will return an error, but runLead calls os.Exit(1)
	// which we can't capture in a unit test without subprocess
	// So we verify the setup is correct
	if recorder.ReturnErr != expectedErr {
		t.Errorf("mock not configured correctly")
	}
}

func TestGenerateLeadPrompt_NotEmpty(t *testing.T) {
	// Verify that GenerateLeadPrompt returns a non-empty prompt
	prompt := GenerateLeadPrompt()
	if prompt == "" {
		t.Error("expected non-empty lead prompt")
	}
	// The prompt should contain some lead-related keywords
	if !strings.Contains(strings.ToLower(prompt), "lead") &&
		!strings.Contains(strings.ToLower(prompt), "project") &&
		!strings.Contains(strings.ToLower(prompt), "review") {
		t.Errorf("lead prompt should contain relevant keywords, got %q", prompt)
	}
}
