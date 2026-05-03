package backends

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// CodexBackend implements the Backend interface for the OpenAI Codex CLI.
type CodexBackend struct{}

func (c *CodexBackend) Name() string { return "codex" }

func (c *CodexBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return codexInvoker(workDir, prompt, agentName)
}

func (c *CodexBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return codexNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// codexInvoker is the function used to invoke Codex interactively (mockable for tests)
var codexInvoker = defaultCodexInvoker

// codexNonInteractiveInvoker is the function used for non-interactive Codex invocation (mockable for tests)
var codexNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultCodexNonInteractiveInvoker

// buildCodexInteractiveCmd constructs the exec.Cmd for interactive Codex invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildCodexInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("codex", "--dangerously-bypass-approvals-and-sandbox", prompt)
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)
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
		cmd.Env = buildBackendEnv(workDir, agentName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd := buildCodexInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Codex agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultCodexNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	cmd := exec.Command("codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox")
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)

	r, err := pipePromptToCmd(cmd, prompt)
	if err != nil {
		return wrapInvocationError(err, "")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return wrapInvocationError(fmt.Errorf("failed to create stdout pipe: %w", err), "")
	}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	fmt.Println("Launching Codex agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		r.Close()
		return wrapInvocationError(fmt.Errorf("failed to start codex: %w", err), "")
	}

	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	outputTail := scanStreamOutput(stdout, func(line string) {
		fmt.Println(line)
		if collector != nil {
			collectCodexStreamUsage(line, collector)
		}
	})

	runErr := cmd.Wait()
	guard.WaitAndMark()
	r.Close()
	return wrapInvocationError(runErr, outputTail)
}

// buildBackendEnv constructs the standard environment for backend subprocess invocations.
func buildBackendEnv(workDir, agentName string) []string {
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	return env
}

// pipePromptToCmd writes the prompt to a pipe and attaches it to cmd.Stdin.
// Returns the read end (caller must close) or an error.
func pipePromptToCmd(cmd *exec.Cmd, prompt string) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %w", err)
	}
	if _, err := io.WriteString(w, prompt); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("failed to write prompt to stdin: %w", err)
	}
	w.Close()
	cmd.Stdin = r
	return r, nil
}

// Meta returns descriptive metadata about the Codex backend.
func (c *CodexBackend) Meta() BackendMeta {
	version := detectBinaryVersion("codex")
	return BackendMeta{
		DisplayName: "Codex",
		Version:     version,
		Description: "OpenAI Codex CLI",
		URL:         "https://github.com/openai/codex",
		BinaryName:  "codex",
	}
}

// HealthCheck reports the installation and readiness status of the Codex backend.
func (c *CodexBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("codex"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("codex")
	} else {
		issues = append(issues, "codex binary not found on PATH")
	}

	if os.Getenv("OPENAI_API_KEY") != "" {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "OPENAI_API_KEY not set")
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
	cli.RegisterBackend(&CodexBackend{})
}

// codexUsageEvent is the minimal structure for Codex --json output
// that contains a usage object (emitted on turn.completed events).
type codexUsageEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

// collectCodexStreamUsage is best-effort: Codex emits turn.completed events
// with a usage object when running with --json. If the line doesn't contain
// usage data, it's silently ignored.
func collectCodexStreamUsage(line string, collector *usage.Collector) {
	var event codexUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Usage == nil {
		return
	}
	// No message-level dedup needed for Codex (one usage per turn)
	collector.Accumulate("", event.Usage.InputTokens, event.Usage.OutputTokens, 0, 0)
}
