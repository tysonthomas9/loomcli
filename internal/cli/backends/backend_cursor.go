package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
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

var cursorAuthStatus = defaultCursorAuthStatus

// buildCursorInteractiveCmd constructs the exec.Cmd for interactive Cursor invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildCursorInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("cursor-agent", "--force", prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
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

func defaultCursorInvoker(workDir, prompt, agentName string) error {
	if err := validateSafetyKnobsFromEnv("cursor"); err != nil {
		return err
	}
	// When stdin is not a TTY (e.g. daemon subprocess), Cursor interactive
	// mode may fail. Fall back to non-interactive print mode which works headlessly.
	if !isTerminal(os.Stdin) {
		fmt.Println("Launching Cursor agent (non-interactive, no TTY)...")
		fmt.Println("")

		cmd := exec.Command("cursor-agent", "-p", "--force", prompt) //nolint:gosec // G204: prompt is from the CLI operator, not untrusted input
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

	cmd := buildCursorInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Cursor agent...")
	fmt.Println("")

	return cmd.Run()
}

func defaultCursorNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	if err := validateSafetyKnobsFromEnv("cursor"); err != nil {
		return err
	}
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}

	fmt.Println("Launching Cursor agent (non-interactive)...")
	fmt.Println("")

	// Cursor consumes the prompt as a CLI argument; pass empty stdin
	// so the wrapper's PTY sees immediate EOF if the harness reads.
	return runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName:  "cursor-agent",
		Args:        []string{"-p", "--output-format", "stream-json", "--force", prompt},
		WorkDir:     workDir,
		Env:         env,
		Prompt:      "",
		HarnessName: "", // no built-in classifier; fall back to generic cost/quota patterns
		LineHandler: func(line string) {
			fmt.Println(line)
			if collector != nil {
				collectCursorStreamUsage(line, collector)
			}
		},
		RetryPolicy: harness.DefaultRetryPolicy(),
	})
}

// Meta returns descriptive metadata about the Cursor backend.
func (c *CursorBackend) Meta() BackendMeta {
	version := detectBinaryVersion("cursor-agent")
	return BackendMeta{
		DisplayName: "Cursor",
		Version:     version,
		Description: "Cursor AI CLI",
		URL:         "https://cursor.com/cli",
		BinaryName:  "cursor-agent",
	}
}

// HealthCheck reports the installation and readiness status of the Cursor backend.
func (c *CursorBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("cursor-agent"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("cursor-agent")
	} else {
		issues = append(issues, "cursor-agent binary not found on PATH")
	}

	if os.Getenv("CURSOR_API_KEY") != "" {
		hs.APIKeySet = true
	} else if hs.Installed && cursorAuthStatus() == nil {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "CURSOR_API_KEY not set and cursor-agent status is not logged in")
	}

	hs.Healthy = hs.Installed && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

func defaultCursorAuthStatus() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cursor-agent", "status")
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func init() {
	cli.RegisterBackend(&CursorBackend{})
}

// cursorUsageEvent is the minimal structure for Cursor --output-format stream-json
// output that contains a usage object. cursor-agent (2026.08) emits the usage
// on the final "result" event with camelCase keys:
//
//	{"type":"result",...,"usage":{"inputTokens":134,"outputTokens":20,
//	 "cacheReadTokens":19328,"cacheWriteTokens":0}}
//
// Older builds used snake_case; both spellings are accepted and the first
// non-nil one wins, so a usage object is never silently counted as zero.
type cursorUsageEvent struct {
	Type  string `json:"type"`
	Usage *struct {
		InputTokens       *int64 `json:"inputTokens"`
		OutputTokens      *int64 `json:"outputTokens"`
		CacheReadTokens   *int64 `json:"cacheReadTokens"`
		CacheWriteTokens  *int64 `json:"cacheWriteTokens"`
		InputTokensSnake  *int64 `json:"input_tokens"`
		OutputTokensSnake *int64 `json:"output_tokens"`
		CacheReadSnake    *int64 `json:"cache_read_tokens"`
		CacheWriteSnake   *int64 `json:"cache_write_tokens"`
	} `json:"usage,omitempty"`
}

func firstInt64(candidates ...*int64) int64 {
	for _, c := range candidates {
		if c != nil {
			return *c
		}
	}
	return 0
}

// collectCursorStreamUsage is best-effort: Cursor emits stream-json events
// with a usage object. If the line doesn't contain usage data, it's silently ignored.
func collectCursorStreamUsage(line string, collector *usage.Collector) {
	var event cursorUsageEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	// Only the final result event carries the turn's usage; ignore any other
	// event that happens to embed a usage object so a turn is counted once.
	if event.Usage == nil || event.Type != "result" {
		return
	}
	u := event.Usage
	// No message-level dedup needed for Cursor (one usage per event)
	collector.Accumulate("",
		firstInt64(u.InputTokens, u.InputTokensSnake),
		firstInt64(u.OutputTokens, u.OutputTokensSnake),
		firstInt64(u.CacheReadTokens, u.CacheReadSnake),
		firstInt64(u.CacheWriteTokens, u.CacheWriteSnake))
}
