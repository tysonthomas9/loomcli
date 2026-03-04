package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
)

// OpenCodeBackend implements the Backend interface for the OpenCode CLI.
type OpenCodeBackend struct{}

func (o *OpenCodeBackend) Name() string { return "opencode" }

func (o *OpenCodeBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return openCodeInvoker(workDir, prompt, agentName)
}

func (o *OpenCodeBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	return openCodeNonInteractiveInvoker(workDir, prompt, agentName, shutdown)
}

// openCodeInvoker is the function used to invoke OpenCode interactively (mockable for tests)
var openCodeInvoker = defaultOpenCodeInvoker

// openCodeNonInteractiveInvoker is the function used for non-interactive OpenCode invocation (mockable for tests)
var openCodeNonInteractiveInvoker = defaultOpenCodeNonInteractiveInvoker

// buildOpenCodeInteractiveCmd constructs the exec.Cmd for interactive OpenCode invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildOpenCodeInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("opencode", "run", prompt)
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

func defaultOpenCodeInvoker(workDir, prompt, agentName string) error {
	cmd := buildOpenCodeInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching OpenCode agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultOpenCodeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	cmd := exec.Command("opencode", "run", "--format", "json")
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

	// Pipe stdout directly (no stream-json parsing for OpenCode in v1)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	fmt.Println("Launching OpenCode agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		r.Close()
		return fmt.Errorf("failed to start opencode: %w", err)
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

// Meta returns descriptive metadata about the OpenCode backend.
func (o *OpenCodeBackend) Meta() BackendMeta {
	version := detectBinaryVersion("opencode")
	return BackendMeta{
		DisplayName: "OpenCode",
		Version:     version,
		Description: "OpenCode CLI",
		URL:         "https://github.com/opencode-ai/opencode",
		BinaryName:  "opencode",
	}
}

// HealthCheck reports the installation and readiness status of the OpenCode backend.
func (o *OpenCodeBackend) HealthCheck() HealthStatus {
	var hs HealthStatus

	if _, err := exec.LookPath("opencode"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("opencode")
	} else {
		hs.Message = "opencode binary not found on PATH"
		return hs
	}

	// OpenCode supports multiple providers, so no single API key to check.
	hs.Healthy = hs.Installed
	if hs.Healthy {
		hs.Message = "ready"
	}
	return hs
}

func init() {
	RegisterBackend(&OpenCodeBackend{})
}
