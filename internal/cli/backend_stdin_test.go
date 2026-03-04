package cli

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestBackendStdinNonInteractivePipePattern verifies the os.Pipe pattern used
// by non-interactive invokers: the prompt is written to a pipe which becomes
// the process's stdin, and the write end is closed so the process sees EOF.
func TestBackendStdinNonInteractivePipePattern(t *testing.T) {
	prompt := "non-interactive secret prompt"

	// Create a pipe exactly as the non-interactive invokers do.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}

	// Write prompt then close write end (mirrors the production code pattern).
	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		t.Fatalf("failed to write prompt: %v", err)
	}
	w.Close()

	// The helper prints its args, then reads all of stdin.
	cmd := exec.Command("/bin/sh", "-c", `echo "ARGS:$@"; cat`, "--") //nolint:norawexec
	cmd.Stdin = r

	out, err := cmd.Output()
	r.Close()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	output := string(out)
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// No arguments should be present.
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line of output")
	}
	argsLine := lines[0]
	if argsLine != "ARGS:" {
		t.Errorf("expected no args (\"ARGS:\"), got %q", argsLine)
	}

	// The prompt must appear in stdin output.
	if !strings.Contains(output, prompt) {
		t.Errorf("expected prompt %q in stdin output, got %q", prompt, output)
	}
}

// TestBackendStdinNonInteractivePipeEOF verifies that closing the write end
// of the pipe causes the child process to receive EOF on stdin.
func TestBackendStdinNonInteractivePipeEOF(t *testing.T) {
	prompt := "pipe-eof-test"

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}

	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		t.Fatalf("failed to write prompt: %v", err)
	}
	w.Close()

	// Read all from the pipe's read end to simulate what the child does.
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("failed to read from pipe: %v", err)
	}

	got := string(data)
	if got != prompt {
		t.Errorf("expected pipe to contain exactly %q, got %q", prompt, got)
	}
}

// TestBackendStdinSpecialCharacters verifies that prompts with shell-special
// characters are safely delivered via stdin without shell interpretation.
func TestBackendStdinSpecialCharacters(t *testing.T) {
	// Characters that would cause problems if passed as CLI args and
	// interpreted by a shell: quotes, backticks, dollar signs, newlines.
	prompt := `He said "hello $USER" and ran $(rm -rf /) with ` + "`backticks`" + "\nand newlines"

	cmd := exec.Command("/bin/sh", "-c", "cat") //nolint:norawexec
	cmd.Stdin = strings.NewReader(prompt)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	if string(out) != prompt {
		t.Errorf("expected prompt to pass through unchanged via stdin\nwant: %q\ngot:  %q", prompt, string(out))
	}
}
