package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

// ClaudeBackend implements the Backend interface for the Claude CLI.
type ClaudeBackend struct{}

func (c *ClaudeBackend) Name() string { return "claude" }

func (c *ClaudeBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	return claudeInvoker(workDir, prompt, agentName)
}

func (c *ClaudeBackend) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	return claudeNonInteractiveInvoker(workDir, prompt, agentName, shutdown)
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
	cmd := exec.Command("claude", "-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions")
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env

	// Pass prompt via stdin pipe
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
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		r.Close()
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Monitor context cancellation
	var exited atomic.Bool
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if !exited.Load() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		case <-done:
		}
	}()

	return &streamReadCloser{
		ReadCloser: stdout,
		cmd:        cmd,
		stdinPipe:  r,
		exited:     &exited,
		done:       done,
	}, nil
}

// streamReadCloser wraps a stdout pipe and ensures the subprocess is cleaned up on Close.
type streamReadCloser struct {
	io.ReadCloser
	cmd       *exec.Cmd
	stdinPipe *os.File
	exited    *atomic.Bool
	done      chan struct{}
}

func (s *streamReadCloser) Close() error {
	readErr := s.ReadCloser.Close()
	// Mark exited before Wait so the context goroutine won't SIGTERM a reused PID.
	s.exited.Store(true)
	waitErr := s.cmd.Wait()
	// Guard against double-close.
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.stdinPipe.Close()
	if readErr != nil {
		return readErr
	}
	return waitErr
}

// ContinueSession resumes an interactive Claude session by session ID.
func (c *ClaudeBackend) ContinueSession(workDir, sessionID, agentName string) error {
	cmd := exec.Command("claude", "--resume", "--session-id", sessionID,
		"--dangerously-skip-permissions")
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env
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
	RegisterBackend(&ClaudeBackend{})
}

// debugStreamParsing enables verbose output for JSON parsing errors
var debugStreamParsing = os.Getenv("LOOM_DEBUG_STREAM") != ""

// activeUsageCollector is set by the automode loop before each invocation
// and cleared after. The scanner loop reads it during stream parsing.
// Access is sequential (set → invoke blocks → clear) so no mutex is needed.
var activeUsageCollector *usage.Collector

// claudeInvoker is the function used to invoke Claude (mockable for tests)
var claudeInvoker = defaultClaudeInvoker

// buildClaudeInteractiveCmd constructs the exec.Cmd for interactive Claude invocation.
// Extracted for testability — callers can inspect the returned cmd without execution.
func buildClaudeInteractiveCmd(workDir, prompt, agentName string) *exec.Cmd {
	cmd := exec.Command("claude", "--dangerously-skip-permissions", prompt)
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

// defaultClaudeInvoker is the real Claude invocation
func defaultClaudeInvoker(workDir, prompt, agentName string) error {
	cmd := buildClaudeInteractiveCmd(workDir, prompt, agentName)

	fmt.Println("Launching Claude agent...")
	fmt.Println("")

	return cmd.Run()
}

// claudeNonInteractiveInvoker is the function used for non-interactive Claude invocation (mockable for tests)
var claudeNonInteractiveInvoker func(workDir, prompt, agentName string, shutdown <-chan struct{}) error = defaultClaudeNonInteractiveInvoker

// defaultClaudeNonInteractiveInvoker is the real non-interactive Claude invocation
func defaultClaudeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	cmd := exec.Command("claude", "-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions")
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

	// Capture stdout for parsing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Close()
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	fmt.Println("Launching Claude agent (non-interactive)...")
	fmt.Println("")

	if err := cmd.Start(); err != nil {
		r.Close()
		return fmt.Errorf("failed to start claude: %w", err)
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
			// Normal completion
		}
	}()

	// Parse and display streaming output
	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024) // 1MB buffer for large tool results
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		displayStreamEvent(line)
		if activeUsageCollector != nil {
			collectClaudeStreamUsage(line, activeUsageCollector)
		}
	}

	runErr := cmd.Wait()
	exited.Store(true) // Mark exited before closing done channel
	close(done)        // Signal goroutine to exit
	r.Close()
	return runErr
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
