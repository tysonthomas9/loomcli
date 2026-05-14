package backends

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/cli"
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
	args := append([]string{"run", "--dir", workDir, "--dangerously-skip-permissions"}, openCodeModelArgs()...)
	args = append(args, prompt)
	cmd := exec.Command("opencode", args...)
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)
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

func defaultOpenCodeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	args := append([]string{"run", "--format", "json", "--dir", workDir, "--dangerously-skip-permissions"}, openCodeModelArgs()...)
	cmd := exec.Command("opencode", args...)
	cmd.Dir = workDir
	cmd.Env = buildBackendEnv(workDir, agentName)

	r := pipePromptToCmd(cmd, prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return wrapInvocationError(fmt.Errorf("failed to create stdout pipe: %w", err), "")
	}
	cmd.Stderr = os.Stderr

	fmt.Println("Launching OpenCode agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		r.Close()
		return wrapInvocationError(fmt.Errorf("failed to start opencode: %w", err), "")
	}

	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	outputTail, streamErrMsg := scanOpenCodeStream(stdout, collector)

	runErr := cmd.Wait()
	guard.WaitAndMark()
	r.Close()
	return finalizeOpenCodeRun(runErr, outputTail, streamErrMsg)
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
	cli.RegisterBackend(&OpenCodeBackend{})
}

func openCodeModelArgs() []string {
	model := strings.TrimSpace(os.Getenv("LOOM_OPENCODE_MODEL"))
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

func scanOpenCodeStream(stdout io.Reader, collector *usage.Collector) (string, string) {
	var streamErrMsg string
	outputTail := scanStreamOutput(stdout, func(line string) {
		fmt.Println(line)
		if streamErrMsg == "" {
			if msg, ok := extractOpenCodeStreamError(line); ok {
				streamErrMsg = msg
			}
		}
		if collector != nil {
			collectOpenCodeStreamUsage(line, collector)
		}
	})
	return outputTail, streamErrMsg
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
