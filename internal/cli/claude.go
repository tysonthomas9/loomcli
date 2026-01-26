package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// claudeInvoker is the function used to invoke Claude (mockable for tests)
var claudeInvoker = defaultClaudeInvoker

// defaultClaudeInvoker is the real Claude invocation
func defaultClaudeInvoker(workDir, prompt string) error {
	cmd := exec.Command("claude", "--dangerously-skip-permissions", prompt)
	cmd.Dir = workDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("Launching Claude agent...")
	fmt.Println("")

	return cmd.Run()
}

// InvokeClaude runs Claude with the given prompt using --dangerously-skip-permissions
func InvokeClaude(workDir, prompt string) error {
	return claudeInvoker(workDir, prompt)
}

// claudeNonInteractiveInvoker is the function used for non-interactive Claude invocation (mockable for tests)
var claudeNonInteractiveInvoker func(workDir, prompt string, shutdown <-chan struct{}) error = defaultClaudeNonInteractiveInvoker

// defaultClaudeNonInteractiveInvoker is the real non-interactive Claude invocation
func defaultClaudeNonInteractiveInvoker(workDir, prompt string, shutdown <-chan struct{}) error {
	cmd := exec.Command("claude", "-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions", prompt)
	cmd.Dir = workDir

	// Create a pipe and close write end to send EOF
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
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
	done := make(chan struct{})
	go func() {
		select {
		case <-shutdown:
			// Kill the child process
			cmd.Process.Signal(syscall.SIGTERM)
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
	close(done) // Signal goroutine to exit
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

// InvokeClaudeNonInteractive runs Claude in non-interactive mode (for auto mode)
// Claude will process the prompt and exit, rather than waiting for more input
// The shutdown channel allows the caller to signal that the process should be terminated
func InvokeClaudeNonInteractive(workDir, prompt string, shutdown <-chan struct{}) error {
	return claudeNonInteractiveInvoker(workDir, prompt, shutdown)
}

// InvokeClaudeForConflicts runs Claude to resolve merge conflicts
func InvokeClaudeForConflicts(workDir, sourceBranch, targetBranch string, conflicts []string) error {
	prompt := GenerateConflictResolutionPrompt(sourceBranch, targetBranch, conflicts)
	return InvokeClaude(workDir, prompt)
}
