package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultExecCommand_Success(t *testing.T) {
	// Use a simple command that always succeeds
	result := defaultExecCommand("", "echo", "hello")
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Errorf("expected stdout 'hello', got %q", result.Stdout)
	}
	if result.Stderr != "" {
		t.Errorf("expected empty stderr, got %q", result.Stderr)
	}
}

func TestDefaultExecCommand_Failure(t *testing.T) {
	// Use a command that will fail
	result := defaultExecCommand("", "ls", "/nonexistent/path/that/does/not/exist")
	if result.Err == nil {
		t.Error("expected error for nonexistent path, got nil")
	}
	// stderr should contain error message
	if result.Stderr == "" && !strings.Contains(result.Stdout, "No such file") {
		// Some systems put error in stdout for ls
		t.Log("Note: error message location varies by system")
	}
}

func TestDefaultExecCommand_Dir(t *testing.T) {
	// Create a temp directory
	tmpDir := t.TempDir()

	// Create a test file in the temp directory
	testFile := filepath.Join(tmpDir, "testfile.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run ls in that directory
	result := defaultExecCommand(tmpDir, "ls")
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if !strings.Contains(result.Stdout, "testfile.txt") {
		t.Errorf("expected 'testfile.txt' in output, got %q", result.Stdout)
	}
}

func TestDefaultExecCommand_OutputCapture(t *testing.T) {
	// Test that both stdout and stderr are captured separately
	// Use sh -c to write to both streams
	result := defaultExecCommand("", "sh", "-c", "echo stdout_msg; echo stderr_msg >&2")
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if !strings.Contains(result.Stdout, "stdout_msg") {
		t.Errorf("expected 'stdout_msg' in stdout, got %q", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "stderr_msg") {
		t.Errorf("expected 'stderr_msg' in stderr, got %q", result.Stderr)
	}
}

func TestDefaultExecCommand_NoOutput(t *testing.T) {
	// Commands that produce no output
	result := defaultExecCommand("", "true")
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if result.Stdout != "" {
		t.Errorf("expected empty stdout, got %q", result.Stdout)
	}
	if result.Stderr != "" {
		t.Errorf("expected empty stderr, got %q", result.Stderr)
	}
}

func TestDefaultExecCommand_ExitCode(t *testing.T) {
	// Test that exit codes are captured via error
	result := defaultExecCommand("", "false") // 'false' command exits with 1
	if result.Err == nil {
		t.Error("expected error from 'false' command, got nil")
	}
}
