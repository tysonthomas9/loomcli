package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// CodexBackend implements the Backend interface for the OpenAI Codex CLI.
type CodexBackend struct{}

func (c *CodexBackend) Name() string { return "codex" }

func (c *CodexBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return codexInvoker(workDir, prompt, agentName)
}

func (c *CodexBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	return codexNonInteractiveInvoker(workDir, prompt, agentName, shutdown)
}

// codexInvoker is the function used to invoke Codex interactively (mockable for tests)
var codexInvoker = defaultCodexInvoker

// codexNonInteractiveInvoker is the function used for non-interactive Codex invocation (mockable for tests)
var codexNonInteractiveInvoker = defaultCodexNonInteractiveInvoker

// buildCodexInteractiveCmd constructs the exec.Cmd for interactive Codex invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildCodexInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("codex", "--dangerously-bypass-approvals-and-sandbox", prompt)
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// isTerminal reports whether f is connected to a terminal (TTY)
// using the TIOCGWINSZ ioctl, which only succeeds on real terminals.
func isTerminal(f *os.File) bool {
	// struct winsize: rows, cols, xpixel, ypixel
	var ws [4]uint16
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws[0]))) //nolint:gosec // G103: deliberate unsafe for TIOCGWINSZ ioctl
	return err == 0
}

func defaultCodexInvoker(workDir, prompt, agentName string) error {
	// When stdin is not a TTY (e.g. daemon subprocess), Codex interactive
	// mode fails with "stdin is not a terminal". Fall back to non-interactive
	// exec mode which works headlessly.
	if !isTerminal(os.Stdin) {
		fmt.Println("Launching Codex agent (non-interactive, no TTY)...")
		fmt.Println("")

		cmd := exec.Command("codex", "exec", "--dangerously-bypass-approvals-and-sandbox", prompt)
		cmd.Dir = workDir
		env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "BD_ACTOR="+agentName)
		}
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd := buildCodexInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Codex agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultCodexNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	cmd := exec.Command("codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox")
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env

	// Pass prompt via stdin pipe (not CLI args) to avoid exposure in process listings
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		return fmt.Errorf("failed to write prompt to stdin: %w", err)
	}
	w.Close()
	cmd.Stdin = r

	// Pipe stdout directly (no stream-json parsing for Codex in v1)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	fmt.Println("Launching Codex agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		r.Close()
		return fmt.Errorf("failed to start codex: %w", err)
	}

	// Monitor for shutdown signal
	var exited atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case <-shutdown:
			// Only signal if process hasn't exited yet to avoid
			// sending SIGTERM to a reused PID.
			if !exited.Load() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		case <-done:
		}
	}()

	// Pipe stdout directly to os.Stdout (no JSON parsing)
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}

	runErr := cmd.Wait()
	exited.Store(true) // Mark exited before closing done channel
	close(done)        // Signal goroutine to exit
	r.Close()
	return runErr
}

func init() {
	RegisterBackend(&CodexBackend{})
}
