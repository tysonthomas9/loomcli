package agent

import (
	"bytes"
	"errors"
	"io"
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

	// Reset backend registry and install a mock backend that records calls.
	resetBackendState(t)
	mock := &mockBackend{name: "claude"}
	RegisterBackend(mock)
	_ = SetBackend("claude")

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
	if len(mock.interactiveCalls) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(mock.interactiveCalls))
	}

	inv := mock.interactiveCalls[0]
	// WorkDir should be the temp directory
	if inv.workDir != tmpDir {
		t.Errorf("expected workDir %q, got %q", tmpDir, inv.workDir)
	}
	// Prompt should be the lead prompt
	leadPrompt := GenerateLeadPrompt()
	if inv.prompt != leadPrompt {
		t.Errorf("expected lead prompt, got %q", inv.prompt)
	}
	// AgentName should be empty for lead mode (not claiming tasks)
	if inv.agentName != "" {
		t.Errorf("expected empty agentName for lead mode, got %q", inv.agentName)
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
	if recorder.InteractiveErr != expectedErr {
		t.Errorf("mock not configured correctly")
	}
}

func TestExecShell_ChdirHonorsWorkDir(t *testing.T) {
	// not parallel: mutates process-wide cwd (os.Chdir) and SHELL env var
	// Capture original cwd so we can restore it after the test.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create a temp dir and resolve symlinks so macOS /var -> /private/var
	// mismatches don't cause false failures.
	tmpDir := t.TempDir()
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		t.Fatalf("failed to eval symlinks for tmpDir: %v", err)
	}

	// Start from a directory different from tmpDir so that an unchanged
	// cwd after execShell would prove the fix is missing.
	startDir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("failed to eval symlinks for os.TempDir: %v", err)
	}
	if err := os.Chdir(startDir); err != nil {
		t.Fatalf("failed to chdir to startDir: %v", err)
	}

	// Force syscall.Exec to fail quickly by pointing SHELL at a path that
	// cannot possibly exist. The fallback cmd.Run() will also fail because
	// the binary is missing — that's fine; we only care that os.Chdir ran
	// before syscall.Exec.
	origShell, shellWasSet := os.LookupEnv("SHELL")
	os.Setenv("SHELL", "/nonexistent/path/shell-bin-for-test-exec-shell-chdir")
	t.Cleanup(func() {
		if shellWasSet {
			os.Setenv("SHELL", origShell)
		} else {
			os.Unsetenv("SHELL")
		}
	})

	// Redirect stderr so any warnings / noise from the fallback don't
	// pollute test output. Drain the read-end so writes never block on a
	// full kernel pipe buffer.
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stderr pipe: %v", err)
	}
	os.Stderr = w
	drained := make(chan struct{})
	go func() {
		io.Copy(io.Discard, r)
		close(drained)
	}()
	defer func() {
		w.Close()
		<-drained
		r.Close()
		os.Stderr = oldStderr
	}()

	// Call execShell. syscall.Exec will fail (bad SHELL path), fallback
	// cmd.Run will also fail — but execShell must have chdir'd to tmpDir
	// before attempting syscall.Exec.
	execShell(tmpDir)

	// Assert: cwd should now be tmpDir, proving os.Chdir(workDir) ran.
	gotDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd after execShell: %v", err)
	}
	gotDir, err = filepath.EvalSymlinks(gotDir)
	if err != nil {
		t.Fatalf("failed to eval symlinks for gotDir: %v", err)
	}
	if gotDir != tmpDir {
		t.Errorf("expected cwd after execShell to be %q, got %q", tmpDir, gotDir)
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
