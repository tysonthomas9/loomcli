package cli

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestBackendStdinInteractivePattern verifies the io.MultiReader pattern used
// by interactive invokers: the prompt is delivered via stdin (not CLI args),
// followed by any remaining stdin content.
func TestBackendStdinInteractivePattern(t *testing.T) {
	prompt := "secret prompt that should not appear in args"

	// Use a helper shell command that prints its args, then reads stdin.
	// This mirrors how the interactive invokers work: prompt is piped via
	// io.MultiReader, not passed as a CLI argument.
	cmd := exec.Command("/bin/sh", "-c", `echo "ARGS:$@"; cat`, "--")
	cmd.Stdin = io.MultiReader(strings.NewReader(prompt+"\n"), strings.NewReader("extra-input\n"))

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	output := string(out)
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// The args line should have no arguments (just "ARGS:")
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line of output")
	}
	argsLine := lines[0]
	if argsLine != "ARGS:" {
		t.Errorf("expected no args (\"ARGS:\"), got %q", argsLine)
	}

	// The prompt should appear on stdin (printed by cat)
	if !strings.Contains(output, prompt) {
		t.Errorf("expected prompt %q in stdin output, got %q", prompt, output)
	}

	// The extra input after the prompt should also be readable
	if !strings.Contains(output, "extra-input") {
		t.Errorf("expected trailing stdin content in output, got %q", output)
	}
}

// TestBackendStdinInteractivePromptNotInArgs verifies that when using the
// io.MultiReader pattern, the prompt does NOT appear in the process argument list.
func TestBackendStdinInteractivePromptNotInArgs(t *testing.T) {
	prompt := "super-secret-prompt-value"

	// The shell script prints $0 and $@ so we can inspect all args.
	// The prompt must NOT appear anywhere in the argument list.
	cmd := exec.Command("/bin/sh", "-c", `echo "$0"; echo "$@"; cat >/dev/null`, "testprog")
	cmd.Stdin = io.MultiReader(strings.NewReader(prompt+"\n"), strings.NewReader(""))

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	output := string(out)
	if strings.Contains(output, prompt) {
		t.Errorf("prompt %q must NOT appear in process args, but found in output: %q", prompt, output)
	}
}

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
	cmd := exec.Command("/bin/sh", "-c", `echo "ARGS:$@"; cat`, "--")
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

// TestBackendStdinMultiReaderOrdering verifies that io.MultiReader delivers
// the prompt string first, followed by the secondary reader content.
func TestBackendStdinMultiReaderOrdering(t *testing.T) {
	prompt := "first-part"
	trailing := "second-part"

	mr := io.MultiReader(
		strings.NewReader(prompt+"\n"),
		strings.NewReader(trailing+"\n"),
	)

	data, err := io.ReadAll(mr)
	if err != nil {
		t.Fatalf("failed to read from MultiReader: %v", err)
	}

	got := string(data)
	promptIdx := strings.Index(got, prompt)
	trailingIdx := strings.Index(got, trailing)

	if promptIdx == -1 {
		t.Fatalf("expected prompt %q in output, got %q", prompt, got)
	}
	if trailingIdx == -1 {
		t.Fatalf("expected trailing %q in output, got %q", trailing, got)
	}
	if promptIdx >= trailingIdx {
		t.Errorf("expected prompt before trailing content; prompt at %d, trailing at %d in %q",
			promptIdx, trailingIdx, got)
	}
}

// TestBackendStdinSpecialCharacters verifies that prompts with shell-special
// characters are safely delivered via stdin without shell interpretation.
func TestBackendStdinSpecialCharacters(t *testing.T) {
	// Characters that would cause problems if passed as CLI args and
	// interpreted by a shell: quotes, backticks, dollar signs, newlines.
	prompt := `He said "hello $USER" and ran $(rm -rf /) with ` + "`backticks`" + "\nand newlines"

	cmd := exec.Command("/bin/sh", "-c", "cat")
	cmd.Stdin = strings.NewReader(prompt)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command failed: %v", err)
	}

	if string(out) != prompt {
		t.Errorf("expected prompt to pass through unchanged via stdin\nwant: %q\ngot:  %q", prompt, string(out))
	}
}
