package cli

import (
	"fmt"
	"os"
	"os/exec"
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

// InvokeClaudeForConflicts runs Claude to resolve merge conflicts
func InvokeClaudeForConflicts(workDir, sourceBranch, targetBranch string, conflicts []string) error {
	prompt := GenerateConflictResolutionPrompt(sourceBranch, targetBranch, conflicts)
	return InvokeClaude(workDir, prompt)
}
