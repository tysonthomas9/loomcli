//go:build ignore

package cli

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// TestHelperProcess is the subprocess entry point for PTY integration tests.
// When invoked as a subprocess (GO_WANT_HELPER_PROCESS=1), it checks whether
// stdin is a TTY using unix.IoctlGetTermios and exits with code 0 (TTY) or 1 (no TTY).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return // Not running as subprocess; nothing to do in normal test execution.
	}
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), ioctlReadTermios)
	if err != nil {
		os.Exit(1) // Not a TTY
	}
	os.Exit(0) // Is a TTY
}

func skipUnlessIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("LOOM_INTEGRATION_TESTS") != "1" {
		t.Skip("skipping integration test (set LOOM_INTEGRATION_TESTS=1 to run)")
	}
}

func TestClaudeInteractiveWithPTY(t *testing.T) {
	skipUnlessIntegration(t)

	// Verify the builder sets Stdin to os.Stdin (integration-level check)
	builderCmd := buildClaudeInteractiveCmd("/tmp/work", "test", "agent")
	if builderCmd.Stdin != os.Stdin {
		t.Fatal("buildClaudeInteractiveCmd did not set Stdin to os.Stdin")
	}

	// Run test helper subprocess with a real PTY to prove TTY detection works
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start failed: %v", err)
	}
	t.Cleanup(func() {
		ptmx.Close()
	})

	// Drain PTY output to prevent subprocess from blocking on write
	go io.Copy(io.Discard, ptmx)

	if err := cmd.Wait(); err != nil {
		t.Errorf("expected exit code 0 (TTY detected), got error: %v", err)
	}
}

func TestClaudeInteractiveWithoutPTY(t *testing.T) {
	skipUnlessIntegration(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	// Use a pipe for stdin (no TTY)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	t.Cleanup(func() {
		r.Close()
	})
	w.Close()
	cmd.Stdin = r
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start failed: %v", err)
	}

	err = cmd.Wait()
	if err == nil {
		t.Fatal("expected exit code 1 (no TTY), but got exit code 0")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
	}
}

func TestCodexInteractiveWithPTY(t *testing.T) {
	skipUnlessIntegration(t)

	// Verify the builder sets Stdin to os.Stdin (integration-level check)
	builderCmd := buildCodexInteractiveCmd("/tmp/work", "test", "agent")
	if builderCmd.Stdin != os.Stdin {
		t.Fatal("buildCodexInteractiveCmd did not set Stdin to os.Stdin")
	}

	// Run test helper subprocess with a real PTY to prove TTY detection works
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start failed: %v", err)
	}
	t.Cleanup(func() {
		ptmx.Close()
	})

	go io.Copy(io.Discard, ptmx)

	if err := cmd.Wait(); err != nil {
		t.Errorf("expected exit code 0 (TTY detected), got error: %v", err)
	}
}

func TestOpenCodeInteractiveWithPTY(t *testing.T) {
	skipUnlessIntegration(t)

	// Verify the builder sets Stdin to os.Stdin (integration-level check)
	builderCmd := buildOpenCodeInteractiveCmd("/tmp/work", "test", "agent")
	if builderCmd.Stdin != os.Stdin {
		t.Fatal("buildOpenCodeInteractiveCmd did not set Stdin to os.Stdin")
	}

	// Run test helper subprocess with a real PTY to prove TTY detection works
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start failed: %v", err)
	}
	t.Cleanup(func() {
		ptmx.Close()
	})

	go io.Copy(io.Discard, ptmx)

	if err := cmd.Wait(); err != nil {
		t.Errorf("expected exit code 0 (TTY detected), got error: %v", err)
	}
}
