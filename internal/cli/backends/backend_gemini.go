package backends

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// GeminiBackend implements the Backend interface for the Google Gemini CLI.
type GeminiBackend struct{}

func (g *GeminiBackend) Name() string { return "gemini" }

func (g *GeminiBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return geminiInvoker(workDir, prompt, agentName)
}

func (g *GeminiBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return geminiNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// geminiInvoker is the function used to invoke Gemini interactively (mockable for tests)
var geminiInvoker = defaultGeminiInvoker

// geminiNonInteractiveInvoker is the function used for non-interactive Gemini invocation (mockable for tests)
var geminiNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultGeminiNonInteractiveInvoker

// buildGeminiInteractiveCmd constructs the exec.Cmd for interactive Gemini invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildGeminiInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("gemini", "--approval-mode=yolo", prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
	cmd.Dir = workDir
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func defaultGeminiInvoker(workDir, prompt, agentName string) error {
	// When stdin is not a TTY (e.g. daemon subprocess), Gemini interactive
	// mode may fail. Fall back to non-interactive -p mode which works headlessly.
	if !isTerminal(os.Stdin) {
		fmt.Println("Launching Gemini agent (non-interactive, no TTY)...")
		fmt.Println("")

		cmd := exec.Command("gemini", "--approval-mode=yolo", "-p", prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
		cmd.Dir = workDir
		env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
		if agentName != "" {
			env = append(env, "LOOM_AGENT_NAME="+agentName)
		}
		cmd.Env = env
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	cmd := buildGeminiInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Gemini agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultGeminiNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	cmd := exec.Command("gemini", "--approval-mode=yolo", "-p", prompt, "-o", "stream-json") //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
	cmd.Dir = workDir
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	cmd.Env = env

	// Pipe stdout for JSON stream parsing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return wrapInvocationError(fmt.Errorf("failed to create stdout pipe: %w", err), "")
	}
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	fmt.Println("Launching Gemini agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		return wrapInvocationError(fmt.Errorf("failed to start gemini: %w", err), "")
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

	outputTail := scanStreamOutput(stdout, func(line string) {
		fmt.Println(line)
		if collector != nil {
			collectGeminiStreamUsage(line, collector)
		}
	})

	runErr := cmd.Wait()
	guard.WaitAndMark()
	return wrapInvocationError(runErr, outputTail)
}

// Meta returns descriptive metadata about the Gemini backend.
func (g *GeminiBackend) Meta() BackendMeta {
	version := detectBinaryVersion("gemini")
	return BackendMeta{
		DisplayName: "Gemini",
		Version:     version,
		Description: "Google Gemini CLI",
		URL:         "https://github.com/google-gemini/gemini-cli",
		BinaryName:  "gemini",
	}
}

// HealthCheck reports the installation and readiness status of the Gemini backend.
func (g *GeminiBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("gemini"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("gemini")
	} else {
		issues = append(issues, "gemini binary not found on PATH")
	}

	if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "GEMINI_API_KEY or GOOGLE_API_KEY not set")
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
	cli.RegisterBackend(&GeminiBackend{})
}

// geminiUsageEvent is the minimal structure for Gemini stream-json output
// that may contain a usage/token count object.
type geminiUsageEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
	UsageMetadata *struct {
		PromptTokenCount     int64 `json:"promptTokenCount"`
		CandidatesTokenCount int64 `json:"candidatesTokenCount"`
	} `json:"usageMetadata,omitempty"`
}

// collectGeminiStreamUsage is best-effort: Gemini emits JSON events with usage
// metadata when running with -o stream-json. If the line doesn't contain usage
// data, it's silently ignored.
func collectGeminiStreamUsage(line string, collector *usage.Collector) {
	var event geminiUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	// Try standard usage field first (OpenAI-compatible format)
	if event.Usage != nil {
		collector.Accumulate("", event.Usage.InputTokens, event.Usage.OutputTokens, 0, 0)
		return
	}
	// Try Google-native usageMetadata field
	if event.UsageMetadata != nil {
		collector.Accumulate("", event.UsageMetadata.PromptTokenCount, event.UsageMetadata.CandidatesTokenCount, 0, 0)
	}
}
