package backends

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// DefaultMaxBudgetUSD is the default per-session budget ceiling for non-interactive
// Claude invocations. Analysis of 287 sessions shows median session cost well under $2;
// $5 provides 2.5x headroom to prevent mid-response truncation.
const DefaultMaxBudgetUSD = 5.0

// resolveMaxBudgetUSD reads LOOM_MAX_BUDGET_USD from the environment and returns the
// value to pass as --max-budget-usd. Returns "" if the flag should be omitted (opt-out).
func resolveMaxBudgetUSD() string {
	raw := os.Getenv("LOOM_MAX_BUDGET_USD")
	if raw == "" {
		return fmt.Sprintf("%.2f", DefaultMaxBudgetUSD)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[warn] invalid LOOM_MAX_BUDGET_USD=%q, using default\n", raw)
		return fmt.Sprintf("%.2f", DefaultMaxBudgetUSD)
	}
	if v < 0 {
		fmt.Fprintf(os.Stderr, "[warn] negative LOOM_MAX_BUDGET_USD=%q, using default\n", raw)
		return fmt.Sprintf("%.2f", DefaultMaxBudgetUSD)
	}
	if v == 0 {
		return "" // explicit opt-out
	}
	return fmt.Sprintf("%.2f", v)
}

// ClaudeBackend implements the Backend interface for the Claude CLI.
type ClaudeBackend struct{}

func (c *ClaudeBackend) Name() string { return "claude" }

func (c *ClaudeBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return claudeInvoker(workDir, prompt, agentName)
}

func (c *ClaudeBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	return claudeNonInteractiveInvoker(workDir, prompt, agentName, shutdown, collector)
}

// Meta returns descriptive metadata about the Claude backend.
func (c *ClaudeBackend) Meta() BackendMeta {
	version := detectBinaryVersion("claude")
	return BackendMeta{
		DisplayName: "Claude",
		Version:     version,
		Description: "Anthropic Claude Code CLI",
		URL:         "https://docs.anthropic.com/en/docs/claude-code",
		BinaryName:  "claude",
	}
}

// HealthCheck reports the installation and readiness status of the Claude backend.
func (c *ClaudeBackend) HealthCheck() HealthStatus {
	var hs HealthStatus
	var issues []string

	if _, err := exec.LookPath("claude"); err == nil {
		hs.Installed = true
		hs.Version = detectBinaryVersion("claude")
	} else {
		issues = append(issues, "claude binary not found on PATH")
	}

	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "ANTHROPIC_API_KEY not set")
	}

	hs.Healthy = hs.Installed && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

// InvokeStreaming starts a Claude agent session and returns a streaming reader
// of JSON events. The caller is responsible for closing the returned ReadCloser,
// which also terminates the subprocess.
func (c *ClaudeBackend) InvokeStreaming(ctx context.Context, workDir, prompt, agentName string) (io.ReadCloser, error) {
	args := []string{"-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions"}
	if budget := resolveMaxBudgetUSD(); budget != "" {
		args = append(args, "--max-budget-usd", budget)
	}
	cmd := exec.Command("claude", args...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)

	r, err := pipePromptToCmd(cmd, prompt)
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		r.Close()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-ctx.Done():
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	return &streamReadCloser{ReadCloser: stdout, cmd: cmd, stdinPipe: r, guard: guard}, nil
}

// streamReadCloser wraps a stdout pipe and ensures the subprocess is cleaned up on Close.
type streamReadCloser struct {
	io.ReadCloser
	cmd       *exec.Cmd
	stdinPipe *os.File
	guard     *processGuard
}

func (s *streamReadCloser) Close() error {
	readErr := s.ReadCloser.Close()
	// Wait for the process to finish FIRST, then mark exited. This ordering
	// ensures the context-cancellation goroutine can still send SIGTERM if the
	// process hangs after stdout is closed.
	waitErr := s.cmd.Wait()
	s.guard.WaitAndMark()
	s.stdinPipe.Close()
	if readErr != nil {
		return readErr
	}
	return waitErr
}

// ContinueSession resumes an interactive Claude session by session ID.
func (c *ClaudeBackend) ContinueSession(workDir, sessionID, agentName string) error {
	cmd := exec.Command("claude", "--resume", "--session-id", sessionID, //nolint:gosec // G204: intentional subprocess launch for claude CLI
		"--dangerously-skip-permissions")
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// LastSessionID returns the most recent session ID. Returns "" because Claude
// CLI manages sessions internally without exposing a listing API.
func (c *ClaudeBackend) LastSessionID(_ string) string {
	return ""
}

func init() {
	cli.RegisterBackend(&ClaudeBackend{})
}

// debugStreamParsing enables verbose output for JSON parsing errors
var debugStreamParsing = os.Getenv("LOOM_DEBUG_STREAM") != ""

// claudeInvoker is the function used to invoke Claude (mockable for tests)
var claudeInvoker = defaultClaudeInvoker

// buildClaudeInteractiveCmd constructs the exec.Cmd for interactive Claude invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildClaudeInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("claude", "--dangerously-skip-permissions", prompt) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// defaultClaudeInvoker is the real Claude invocation
func defaultClaudeInvoker(workDir, prompt, agentName string) error {
	cmd := buildClaudeInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Claude agent...")
	fmt.Println("")

	return cmd.Run()
}

// claudeNonInteractiveInvoker is the function used for non-interactive Claude invocation (mockable for tests)
var claudeNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error = defaultClaudeNonInteractiveInvoker

// buildClaudeEnv constructs the environment variables for Claude subprocess invocations.
func buildClaudeEnv(workDir, agentName string) []string {
	env := append(cli.FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	return append(env, activeSessionEnvVars()...)
}

// buildClaudeNonInteractiveCmd constructs the exec.Cmd for non-interactive Claude invocation.
// When resumeSessionID is non-empty, the command includes --resume --session-id flags.
// Appends --max-budget-usd when resolveMaxBudgetUSD returns a non-empty value.
func buildClaudeNonInteractiveCmd(workDir, agentName, resumeSessionID string) *exec.Cmd {
	var args []string
	if resumeSessionID != "" {
		args = []string{"--resume", "--session-id", resumeSessionID, "-p", "--verbose",
			"--output-format", "stream-json", "--dangerously-skip-permissions"}
	} else {
		args = []string{"-p", "--verbose", "--output-format", "stream-json",
			"--dangerously-skip-permissions"}
	}
	if budget := resolveMaxBudgetUSD(); budget != "" {
		args = append(args, "--max-budget-usd", budget)
	}
	cmd := exec.Command("claude", args...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)
	return cmd
}

// defaultClaudeNonInteractiveInvoker is the real non-interactive Claude invocation
func defaultClaudeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	resumeID := consumeResumeSessionID()
	cmd := buildClaudeNonInteractiveCmd(workDir, agentName, resumeID)

	r, stdout, err := setupNonInteractivePipes(cmd, prompt, resumeID)
	if err != nil {
		return wrapInvocationError(err, "")
	}

	guard := newProcessGuard(cmd.Process)
	go func() {
		select {
		case <-shutdown:
			guard.Signal(syscall.SIGTERM)
		case <-guard.Done():
		}
	}()

	ClearLastCapturedSessionID()
	outputTail := scanStreamOutput(stdout, newStreamLineHandler(workDir, collector))

	runErr := cmd.Wait()
	guard.WaitAndMark()
	r.Close()

	if err := cli.ClearLockClaudeSessionID(workDir); err != nil {
		fmt.Fprintf(os.Stderr, "[loom] failed to clear claude session ID: %v\n", err)
	}

	return wrapInvocationError(runErr, outputTail)
}

// setupNonInteractivePipes configures stdin/stdout pipes, starts the process,
// and prints the launch message. Returns the stdin pipe file and stdout reader.
func setupNonInteractivePipes(cmd *exec.Cmd, prompt, resumeID string) (*os.File, io.Reader, error) {
	r, err := pipePromptToCmd(cmd, prompt)
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if resumeID != "" {
		fmt.Printf("[auto] Resuming Claude session %s...\n\n", resumeID)
	} else {
		fmt.Println("Launching Claude agent (non-interactive)...")
		fmt.Println("")
	}

	if err := cmd.Start(); err != nil {
		r.Close()
		return nil, nil, fmt.Errorf("failed to start claude: %w", err)
	}
	return r, stdout, nil
}

// outputRingBuffer keeps the last N lines of stream output for error classification.
// Uses an index-based circular buffer to avoid pinning evicted strings.
type outputRingBuffer struct {
	lines []string
	idx   int
	count int
}

func newOutputRingBuffer(cap int) *outputRingBuffer {
	return &outputRingBuffer{lines: make([]string, cap)}
}

func (b *outputRingBuffer) Add(line string) {
	b.lines[b.idx] = line
	b.idx = (b.idx + 1) % len(b.lines)
	if b.count < len(b.lines) {
		b.count++
	}
}

func (b *outputRingBuffer) String() string {
	if b.count == 0 {
		return ""
	}
	if b.count < len(b.lines) {
		return strings.Join(b.lines[:b.count], "\n")
	}
	// Buffer is full: oldest entry is at b.idx, wrap around.
	result := make([]string, 0, len(b.lines))
	result = append(result, b.lines[b.idx:]...)
	result = append(result, b.lines[:b.idx]...)
	return strings.Join(result, "\n")
}

// newStreamLineHandler returns a line handler that captures the Claude session ID
// (once) and forwards lines to displayStreamEvent and the usage collector.
func newStreamLineHandler(workDir string, collector *usage.Collector) func(string) {
	var sessionOnce sync.Once
	return func(line string) {
		if sid, ok := extractClaudeSessionID(line); ok {
			sessionOnce.Do(func() {
				SetLastCapturedSessionID(sid)
				if err := cli.UpdateLockClaudeSessionID(workDir, sid); err != nil {
					fmt.Fprintf(os.Stderr, "[loom] failed to persist claude session ID: %v\n", err)
				}
			})
		}
		displayStreamEvent(line)
		if collector != nil {
			collectClaudeStreamUsage(line, collector)
		}
	}
}

// scanStreamOutput reads stdout line by line through a buffered scanner and
// calls handler for each line. Shared by Claude, Codex, and OpenCode backends.
// It returns the last 50 lines so callers can classify invocation failures
// from invocation-local output rather than shared package state.
func scanStreamOutput(stdout io.Reader, handler func(string)) string {
	outputBuf := newOutputRingBuffer(50)
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024) // 1MB buffer for large tool results
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		outputBuf.Add(line)
		handler(line)
	}
	return outputBuf.String()
}

// StreamEvent represents a Claude stream-json event.
// Claude emits usage in message_start (initial) and message_delta (cumulative final).
type StreamEvent struct {
	Type    string        `json:"type"`
	Subtype string        `json:"subtype,omitempty"` // e.g. "message_start", "message_delta"
	Message *EventMessage `json:"message,omitempty"`
	Usage   *StreamUsage  `json:"usage,omitempty"` // top-level usage on message_delta events
}

// EventMessage holds the message body from a Claude stream event.
type EventMessage struct {
	ID      string         `json:"id,omitempty"`
	Content []ContentBlock `json:"content,omitempty"`
	Usage   *StreamUsage   `json:"usage,omitempty"` // usage nested in message on message_start events
}

// ContentBlock represents a single content block in a Claude message.
type ContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

// StreamUsage holds token counts from Claude streaming events.
type StreamUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

// extractClaudeSessionID parses a stream-json line and returns the session_id
// if the line is a system init event (type:"system", subtype:"init").
func extractClaudeSessionID(line string) (string, bool) {
	var event struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", false
	}
	if event.Type != "system" || event.Subtype != "init" {
		return "", false
	}
	if event.SessionID == "" {
		return "", false
	}
	return event.SessionID, true
}

// displayStreamEvent parses JSON event and displays relevant content
func displayStreamEvent(line string) {
	var event StreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		if debugStreamParsing {
			// Truncate long lines in debug output
			truncated := line
			if len(truncated) > 100 {
				truncated = truncated[:100] + "..."
			}
			safeLine := html.EscapeString(truncated)
			fmt.Fprintf(os.Stderr, "[debug] JSON parse failed: %v (line: %s)\n", err, safeLine)
		}
		return
	}

	switch event.Type {
	case "assistant":
		if event.Message == nil {
			return
		}
		for _, block := range event.Message.Content {
			switch block.Type {
			case "text":
				fmt.Print(block.Text)
			case "tool_use":
				// Format tool call nicely
				fmt.Printf("\n[Tool: %s]", block.Name)
				if block.Name == "Bash" {
					if cmd, ok := block.Input["command"].(string); ok {
						fmt.Printf(" %s", cmd)
					}
				} else if block.Name == "Read" || block.Name == "Write" || block.Name == "Edit" {
					if path, ok := block.Input["file_path"].(string); ok {
						fmt.Printf(" %s", path)
					}
				}
				fmt.Println()
			}
		}
	case "result":
		fmt.Println()
	}
}

// collectClaudeStreamUsage extracts token usage from a Claude stream-json line
// and feeds it to the collector. Claude emits usage in two places:
//   - message_start events: message.usage (initial input tokens)
//   - message_delta events: top-level usage (cumulative final output tokens)
//
// We use message_delta usage (cumulative final) when available, falling back
// to message.usage from message_start. Deduplication is by message ID.
func collectClaudeStreamUsage(line string, collector *usage.Collector) {
	var event StreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}

	// Extract message ID for dedup
	var messageID string
	if event.Message != nil {
		messageID = event.Message.ID
	}

	// Prefer top-level usage (message_delta — cumulative final)
	if event.Usage != nil {
		collector.Accumulate(messageID,
			event.Usage.InputTokens,
			event.Usage.OutputTokens,
			event.Usage.CacheReadInputTokens,
			event.Usage.CacheCreationInputTokens,
		)
		return
	}

	// Fall back to message.usage (message_start — initial)
	if event.Message != nil && event.Message.Usage != nil {
		u := event.Message.Usage
		collector.Accumulate(messageID, u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens)
	}
}
