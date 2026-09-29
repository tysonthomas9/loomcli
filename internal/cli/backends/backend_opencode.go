package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// OpenCodeBackend implements the Backend interface for the OpenCode CLI.
type OpenCodeBackend struct{}

func (o *OpenCodeBackend) Name() string { return "opencode" }

func (o *OpenCodeBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return openCodeInvoker(workDir, prompt, agentName)
}

func (o *OpenCodeBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return openCodeNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// openCodeInvoker is the function used to invoke OpenCode interactively (mockable for tests)
var openCodeInvoker = defaultOpenCodeInvoker

// openCodeNonInteractiveInvoker is the function used for non-interactive OpenCode invocation (mockable for tests)
var openCodeNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultOpenCodeNonInteractiveInvoker

// buildOpenCodeInteractiveCmd constructs the exec.Cmd for interactive OpenCode invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildOpenCodeInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	args := append(openCodeInteractiveArgs(), "--prompt", prompt)
	cmd := exec.Command("opencode", args...)
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// openCodeInteractiveArgs is shared by plain and controlled interactive
// launches so the two paths cannot drift onto different OpenCode CLI flags.
// The interactive command is OpenCode's root TUI; cmd.Dir supplies the project.
func openCodeInteractiveArgs() []string {
	return openCodeModelArgs()
}

func defaultOpenCodeInvoker(workDir, prompt, agentName string) error {
	if err := validateSafetyKnobsFromEnv("opencode"); err != nil {
		return err
	}
	cmd := buildOpenCodeInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching OpenCode agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultOpenCodeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	if err := validateSafetyKnobsFromEnv("opencode"); err != nil {
		return err
	}
	args := append([]string{"run", "--format", "json", "--dir", workDir}, openCodeModelArgs()...)

	fmt.Println("Launching OpenCode agent (non-interactive)...")
	fmt.Println("")

	var streamErrMsg string
	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName: "opencode",
		Args:       args,
		WorkDir:    workDir,
		Env:        buildBackendEnv(workDir, agentName),
		Prompt:     prompt,
		// HarnessName left empty: OpenCode has no built-in classifier;
		// the generic cost/quota classifier handles it.
		HarnessName: "",
		LineHandler: func(line string) {
			fmt.Println(line)
			if streamErrMsg == "" {
				if msg, ok := extractOpenCodeStreamError(line); ok {
					streamErrMsg = msg
				}
			}
			if collector != nil {
				collectOpenCodeStreamUsage(line, collector)
			}
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
		Finalize: func(res wrapper.Result, runErr error, outputTail string) error {
			if runErr != nil {
				return finalizeOpenCodeRun(runErr, outputTail, streamErrMsg)
			}
			// Convert the wrapper's terminal Result into the same
			// error shape the legacy exec path produced, then layer
			// the stream-error captured from the JSON event stream
			// on top — OpenCode reports recoverable-looking exits
			// but the JSON channel can still flag a fatal error.
			mapped := wrapWrapperResult(res, outputTail)
			return finalizeOpenCodeRun(mapped, outputTail, streamErrMsg)
		},
	})
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
	return o.healthCheck(true)
}

// HealthCheckForAdmission reports readiness without collecting the CLI version.
func (o *OpenCodeBackend) HealthCheckForAdmission(context.Context) HealthStatus {
	return o.healthCheck(false)
}

func (o *OpenCodeBackend) healthCheck(includeVersion bool) HealthStatus {
	var hs HealthStatus

	if _, err := exec.LookPath("opencode"); err == nil {
		hs.Installed = true
		if includeVersion {
			hs.Version = detectBinaryVersion("opencode")
		}
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
	cli.RegisterBackend(&OpenCodeBackend{})
}

func openCodeModelArgs() []string {
	model := strings.TrimSpace(os.Getenv("LOOM_OPENCODE_MODEL"))
	if model == "" {
		model = resolveAgentModel()
	}
	if model == "" {
		return nil
	}
	return []string{"--model", model}
}

// openCodeUsageEvent is the minimal structure for OpenCode --format json output.
// Best-effort: we look for a usage object with input_tokens/output_tokens.
type openCodeUsageEvent struct {
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage,omitempty"`
}

type openCodeErrorEvent struct {
	Type  string `json:"type"`
	Error *struct {
		Message string `json:"message,omitempty"`
		Data    *struct {
			Message string `json:"message,omitempty"`
		} `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func extractOpenCodeStreamError(line string) (string, bool) {
	var event openCodeErrorEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", false
	}
	if event.Type != "error" || event.Error == nil {
		return "", false
	}
	if msg := strings.TrimSpace(event.Error.Message); msg != "" {
		return msg, true
	}
	if event.Error.Data != nil {
		if msg := strings.TrimSpace(event.Error.Data.Message); msg != "" {
			return msg, true
		}
	}
	return "opencode reported an error", true
}

func finalizeOpenCodeRun(runErr error, outputTail, streamErrMsg string) error {
	streamErrMsg = strings.TrimSpace(streamErrMsg)
	if streamErrMsg != "" && !strings.Contains(outputTail, streamErrMsg) {
		if strings.TrimSpace(outputTail) == "" {
			outputTail = streamErrMsg
		} else {
			outputTail = streamErrMsg + "\n" + outputTail
		}
	}
	if runErr == nil && streamErrMsg != "" {
		runErr = errors.New(streamErrMsg)
	}
	return wrapInvocationError(runErr, outputTail)
}

// collectOpenCodeStreamUsage is best-effort: parse JSON lines for a usage field.
// If no usage data is found, the call is a no-op.
func collectOpenCodeStreamUsage(line string, collector *usage.Collector) {
	var event openCodeUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	if event.Usage == nil {
		return
	}
	collector.Accumulate("", event.Usage.InputTokens, event.Usage.OutputTokens, 0, 0)
}
