package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// CursorBackend implements the Backend interface for the Cursor CLI.
type CursorBackend struct{}

func (c *CursorBackend) Name() string { return "cursor" }

func (c *CursorBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return cursorInvoker(workDir, prompt, agentName)
}

func (c *CursorBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return cursorNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// cursorInvoker is the function used to invoke Cursor interactively (mockable for tests)
var cursorInvoker = defaultCursorInvoker

// cursorNonInteractiveInvoker is the function used for non-interactive Cursor invocation (mockable for tests)
var cursorNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultCursorNonInteractiveInvoker

// buildCursorInteractiveCmd constructs the exec.Cmd for interactive Cursor invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildCursorInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("cursor", "--force", prompt)
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

func defaultCursorInvoker(workDir, prompt, agentName string) error {
	// When stdin is not a TTY (e.g. daemon subprocess), Cursor interactive
	// mode may fail. Fall back to non-interactive print mode which works headlessly.
	if !isTerminal(os.Stdin) {
		fmt.Println("Launching Cursor agent (non-interactive, no TTY)...")
		fmt.Println("")

		cmd := exec.Command("cursor", "-p", "--force", prompt)
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

	cmd := buildCursorInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Cursor agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultCursorNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	cmd := exec.Command("cursor", "-p", "--output-format", "stream-json", "--force", prompt)
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env

	// Pipe stdout for stream-json parsing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	fmt.Println("Launching Cursor agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start cursor: %w", err)
	}

	// Monitor for shutdown signal
	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	// Parse stdout lines: display and collect usage if available
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)
		if collector != nil {
			collectCursorStreamUsage(line, collector)
		}
	}

	runErr := cmd.Wait()
	guard.WaitAndMark()
	return runErr
}

// Meta returns descriptive metadata about the Cursor backend.
func (c *CursorBackend) Meta() BackendMeta {
	version := detectBinaryVersion("cursor")
	return BackendMeta{
		DisplayName: "Cursor",
		Version:     version,
		Description: "Cursor AI CLI",
		URL:         "https://cursor.com/cli",
		BinaryName:  "cursor",
	}
}

// HealthCheck reports the installation and readiness status of the Cursor backend.
func (c *CursorBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("cursor"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("cursor")
	} else {
		issues = append(issues, "cursor binary not found on PATH")
	}

	if os.Getenv("CURSOR_API_KEY") != "" {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "CURSOR_API_KEY not set")
	}

	hs.Healthy = hs.Installed && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

func init() {
	RegisterBackend(&CursorBackend{})
}

// cursorUsageEvent is the minimal structure for Cursor --output-format stream-json
// output that contains a usage object.
type cursorUsageEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// collectCursorStreamUsage is best-effort: Cursor emits stream-json events
// with a usage object. If the line doesn't contain usage data, it's silently ignored.
func collectCursorStreamUsage(line string, collector *usage.Collector) {
	var event cursorUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Usage == nil {
		return
	}
	// No message-level dedup needed for Cursor (one usage per event)
	collector.Accumulate("", event.Usage.InputTokens, event.Usage.OutputTokens, 0, 0)
}
