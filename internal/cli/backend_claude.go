package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
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

func init() {
	RegisterBackend(&ClaudeBackend{})
}

// debugStreamParsing enables verbose output for JSON parsing errors
var debugStreamParsing = os.Getenv("LOOM_DEBUG_STREAM") != ""

// claudeInvoker is the function used to invoke Claude (mockable for tests)
var claudeInvoker = defaultClaudeInvoker

// defaultClaudeInvoker is the real Claude invocation
func defaultClaudeInvoker(workDir, prompt, agentName string) error {
	cmd := exec.Command("claude", "--dangerously-skip-permissions")
	cmd.Dir = workDir
	env := append(FilteredEnv(), "LOOM_WORKTREE_PATH="+workDir)
	if agentName != "" {
		env = append(env, "BD_ACTOR="+agentName)
	}
	cmd.Env = env
	cmd.Stdin = io.MultiReader(strings.NewReader(prompt+"\n"), os.Stdin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("Launching Claude agent...")
	fmt.Println("")

	return cmd.Run()
}

// InvokeClaude runs Claude directly, bypassing the backend registry.
// Deprecated: Use InvokeAgent() instead which dispatches through the active backend.
func InvokeClaude(workDir, prompt, agentName string) error {
	return claudeInvoker(workDir, prompt, agentName)
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
		displayStreamEvent(scanner.Text())
	}

	runErr := cmd.Wait()
	exited.Store(true) // Mark exited before closing done channel
	close(done)        // Signal goroutine to exit
	r.Close()
	return runErr
}

// StreamEvent represents a Claude stream-json event
type StreamEvent struct {
	Type    string        `json:"type"`
	Message *EventMessage `json:"message,omitempty"`
}

type EventMessage struct {
	Content []ContentBlock `json:"content,omitempty"`
}

type ContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
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
			fmt.Fprintf(os.Stderr, "[debug] JSON parse failed: %v (line: %s)\n", err, truncated)
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

// InvokeClaudeNonInteractive runs Claude directly in non-interactive mode.
// Deprecated: Use InvokeAgentNonInteractive() instead which dispatches through the active backend.
func InvokeClaudeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}) error {
	return claudeNonInteractiveInvoker(workDir, prompt, agentName, shutdown)
}

// InvokeClaudeForConflicts runs Claude to resolve merge conflicts.
// Deprecated: Use InvokeAgentForConflicts() instead which dispatches through the active backend.
func InvokeClaudeForConflicts(workDir, sourceBranch, targetBranch string, conflicts []string) error {
	prompt := GenerateConflictResolutionPrompt(sourceBranch, targetBranch, conflicts)
	return InvokeClaude(workDir, prompt, "")
}
