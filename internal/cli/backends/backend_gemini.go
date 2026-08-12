package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
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
	cmd := exec.Command("gemini", geminiApprovalModeArg(), prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
	cmd.Dir = workDir
	cmd.Env = buildGeminiEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// buildGeminiEnv constructs the environment variables for Gemini subprocess invocations.
func buildGeminiEnv(workDir, agentName string) []string {
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	return env
}

func defaultGeminiInvoker(workDir, prompt, agentName string) error {
	if err := validateSafetyKnobsFromEnv("gemini"); err != nil {
		return err
	}
	// When stdin is not a TTY (e.g. daemon subprocess), Gemini interactive
	// mode may fail. Fall back to non-interactive -p mode which works headlessly.
	if !isTerminal(os.Stdin) {
		fmt.Println("Launching Gemini agent (non-interactive, no TTY)...")
		fmt.Println("")

		cmd := exec.Command("gemini", geminiApprovalModeArg(), "-p", prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
		cmd.Dir = workDir
		cmd.Env = buildGeminiEnv(workDir, agentName)
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
	if err := validateSafetyKnobsFromEnv("gemini"); err != nil {
		return err
	}
	fmt.Println("Launching Gemini agent (non-interactive)...")
	fmt.Println("")

	// Gemini takes the prompt as a -p argv flag, not stdin, so the
	// harnessInvocation's Prompt is empty (the wrapper still attaches
	// an empty stdin so the harness sees EOF immediately if it reads).
	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName:  "gemini",
		Args:        []string{geminiApprovalModeArg(), "-p", prompt, "-o", "stream-json"},
		WorkDir:     workDir,
		Env:         buildGeminiEnv(workDir, agentName),
		Prompt:      "",
		HarnessName: "gemini",
		Effort:      resolveAgentEffort(),
		LineHandler: func(line string) {
			fmt.Println(line)
			if collector != nil {
				collectGeminiStreamUsage(line, collector)
			}
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
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
