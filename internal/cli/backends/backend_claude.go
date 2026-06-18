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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	hwharness "github.com/olesho/harness-wrapper/pkg/harness"
	_ "github.com/olesho/harness-wrapper/pkg/harness/claude" // register the "claude" profile

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// claudeProfileCaps resolves the Claude harness profile's capabilities once.
// Session-id extraction and the --resume arg prefix now live in harness-wrapper
// (pkg/harness/claude); loom delegates to them so the per-harness knowledge has
// one home and other harnesses can gain the same capabilities centrally.
var claudeProfileCaps = sync.OnceValue(func() hwharness.ResolvedProfile {
	p, ok := hwharness.For("claude")
	if !ok {
		return hwharness.ResolvedProfile{}
	}
	return p.Resolve(hwharness.ResolveContext{})
})

// DefaultMaxBudgetUSD is the default per-session spend ceiling for non-interactive
// Claude invocations, passed through to the CLI as --max-budget-usd. It is a safety
// rail against runaway sessions, not a target: median session cost is well under $2,
// so tasks that finish normally are unaffected. $50 gives long-running tasks ample
// headroom to finish — including their own commit/close finalize steps — instead of
// truncating mid-task when the budget is exhausted. Override per-invocation with
// LOOM_MAX_BUDGET_USD (or per-role max_budget_usd); set it to 0 to opt out.
const DefaultMaxBudgetUSD = 50.0

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

	if os.Getenv("ANTHROPIC_API_KEY") != "" || hasClaudeAuthFile() {
		hs.APIKeySet = true
	} else {
		issues = append(issues, "ANTHROPIC_API_KEY not set and claude OAuth credentials not found")
	}

	hs.Healthy = hs.Installed && hs.APIKeySet
	if len(issues) > 0 {
		hs.Message = strings.Join(issues, "; ")
	} else {
		hs.Message = "ready"
	}
	return hs
}

// hasClaudeAuthFile reports whether claude-code's OAuth credentials file exists
// on disk and is non-empty. claude-code authenticates via this file (under the
// CLAUDE_CONFIG_DIR config dir, default ~/.claude) when ANTHROPIC_API_KEY is
// unset — mirrors hasCodexAuthFile for the codex backend.
func hasClaudeAuthFile() bool {
	path := claudeAuthFilePath()
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// claudeAuthFilePath returns the path to claude-code's OAuth credentials file,
// honoring the CLAUDE_CONFIG_DIR override (the container/smoke-test sets this),
// else defaulting to ~/.claude/.credentials.json. Mirrors codexAuthFilePath.
func claudeAuthFilePath() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".credentials.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", ".credentials.json")
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
	if effort := resolveAgentEffort(); effort != "" {
		args = append(args, "--effort", effort)
	}
	cmd := exec.Command("claude", args...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)

	r := pipePromptToCmd(cmd, prompt)
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
	stdinPipe io.Closer
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
	cmd := exec.Command("claude", buildClaudeContinueSessionArgs(sessionID)...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func buildClaudeContinueSessionArgs(sessionID string) []string {
	args := claudeResumeArgs(sessionID)
	args = append(args, "--dangerously-skip-permissions")
	if effort := resolveAgentEffort(); effort != "" {
		args = append(args, "--effort", effort)
	}
	return args
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
	args := []string{"--dangerously-skip-permissions"}
	if effort := resolveAgentEffort(); effort != "" {
		args = append(args, "--effort", effort)
	}
	args = append(args, prompt)
	cmd := exec.Command("claude", args...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
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
		env = append(env, "LOOM_AGENT_NAME="+agentName)
	}
	return append(env, activeSessionEnvVars()...)
}

// buildClaudeNonInteractiveArgs returns the argument list for a
// non-interactive Claude run. resumeSessionID prepends wrapper-owned resume args.
// When prompt is non-empty, it is appended as the final positional argument:
// under the harness wrapper's PTY, claude's stdin looks like a TTY and `-p`
// then requires the prompt as an argument rather than over stdin. The legacy
// exec path (buildClaudeNonInteractiveCmd) still passes "" and feeds the
// prompt via a real pipe through cmd.StdinPipe.
func buildClaudeNonInteractiveArgs(resumeSessionID, prompt string) []string {
	var args []string
	// Resume prefix comes from the harness profile (pkg/harness/claude), not a
	// hardcoded literal — so resume is owned in one place across harnesses.
	if resumeSessionID != "" {
		args = append(args, claudeResumeArgs(resumeSessionID)...)
	}
	args = append(args, "-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions")
	if budget := resolveMaxBudgetUSD(); budget != "" {
		args = append(args, "--max-budget-usd", budget)
	}
	if effort := resolveAgentEffort(); effort != "" {
		args = append(args, "--effort", effort)
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	return args
}

func claudeResumeArgs(sessionID string) []string {
	if caps := claudeProfileCaps(); caps.Resume != nil {
		return caps.Resume.ResumeArgs(sessionID)
	}
	return []string{"--resume", sessionID}
}

// buildClaudeNonInteractiveCmd constructs the exec.Cmd for non-interactive Claude invocation.
// When resumeSessionID is non-empty, the command includes wrapper-owned resume args.
// Appends --max-budget-usd when resolveMaxBudgetUSD returns a non-empty value.
// The prompt is delivered via stdin by the caller (cmd.StdinPipe), not argv,
// since this path does not go through the wrapper's PTY.
func buildClaudeNonInteractiveCmd(workDir, agentName, resumeSessionID string) *exec.Cmd {
	cmd := exec.Command("claude", buildClaudeNonInteractiveArgs(resumeSessionID, "")...) //nolint:gosec // G204: intentional subprocess launch for claude CLI
	cmd.Dir = workDir
	cmd.Env = buildClaudeEnv(workDir, agentName)
	return cmd
}

// defaultClaudeNonInteractiveInvoker is the real non-interactive Claude
// invocation. It runs the Claude CLI under the in-tree harness-wrapper
// supervisor (internal/harness): the wrapper classifies the harness's
// terminal state, surfaces transient API errors with RetryAfter hints,
// and harness.RunWithRetry transparently respawns on
// StatusRetryLater / StatusAPIError before bubbling up.
func defaultClaudeNonInteractiveInvoker(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	resumeID := consumeResumeSessionID()

	if resumeID != "" {
		fmt.Printf("[auto] Resuming Claude session %s...\n\n", resumeID)
	} else {
		fmt.Println("Launching Claude agent (non-interactive)...")
		fmt.Println("")
	}

	ClearLastCapturedSessionID()

	err := runHarness(context.Background(), shutdown, harnessInvocation{
		BinaryName: "claude",
		// Prompt is delivered as the final positional arg, not via stdin: the
		// wrapper's PTY makes stdin look like a TTY to claude, which then
		// requires the prompt as an argument when `-p` is set.
		Args:        buildClaudeNonInteractiveArgs(resumeID, prompt),
		WorkDir:     workDir,
		Env:         buildClaudeEnv(workDir, agentName),
		Prompt:      "",
		HarnessName: "claude",
		Effort:      resolveAgentEffort(),
		LineHandler: newStreamLineHandler(workDir, collector),
		RetryPolicy: harness.DefaultRetryPolicy(),
	})

	// NOTE: the lock's Claude session ID is intentionally NOT cleared per-invoke.
	// It must survive a failed/killed run so a daemon restart can carry it forward
	// and --resume (P4). Clearing happens only on SUCCESS — in the daemon path
	// (runTaskDaemon/runPlanDaemon) and the auto-loop's task-completion handler.
	return err
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

// extractClaudeSessionID delegates to the Claude harness profile's session-id
// extractor (pkg/harness/claude). The parsing logic now lives in harness-wrapper;
// this thin shim preserves the existing call sites + tests with identical behavior.
//
// It first strips any leading non-JSON prefix (see trimToJSONObject): in headless
// daemon mode the harness runs under a PTY that is not in raw mode, so terminal
// ECHO can prepend the wrapper's stdin-EOF bytes (\x04\x04, rendered "^D\b\b^D\b\b")
// to the FIRST stream-json line — the system:init event that carries session_id.
// Those leading control bytes break json.Unmarshal, so without this the session id
// is silently never captured and daemon --resume can never arm.
func extractClaudeSessionID(line string) (string, bool) {
	caps := claudeProfileCaps()
	if caps.SessionID == nil {
		return "", false
	}
	return caps.SessionID.ExtractSessionID(trimToJSONObject(line))
}

// trimToJSONObject returns line starting at its first '{' so a leading non-JSON
// prefix (PTY-echoed control bytes on the system:init line; see
// extractClaudeSessionID) does not defeat json.Unmarshal. A line with no '{' (a
// non-JSON line) is returned unchanged — the extractor rejects it anyway.
func trimToJSONObject(line string) string {
	if i := strings.IndexByte(line, '{'); i > 0 {
		return line[i:]
	}
	return line
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
