package cli

import "fmt"

// resolveConflictsWithAgent handles the full conflict resolution flow:
// prints conflict info, invokes Claude agent, and returns error on failure.
func resolveConflictsWithAgent(repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	printConflictInfo(conflicts)
	return InvokeAgentForConflicts(repoPath, sourceBranch, targetBranch, conflicts)
}

// resolveConflictsDetached handles conflict resolution for the detached-HEAD
// push approach, using a refspec-aware prompt so Claude knows to push via refspec.
func resolveConflictsDetached(repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	printConflictInfo(conflicts)
	prompt := generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, pushRef)
	return InvokeAgent(repoPath, prompt, "")
}

// printConflictInfo prints the standard merge conflict header and file list.
func printConflictInfo(conflicts []string) {
	fmt.Println("")
	fmt.Println("⚠ Merge conflicts detected. Launching AI agent to resolve...")
	fmt.Println("")
	fmt.Println("Conflicted files:")
	for _, f := range conflicts {
		fmt.Printf("  - %s\n", f)
	}
	fmt.Println("")
}
