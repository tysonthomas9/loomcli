package cli

import "fmt"

// resolveConflictsWithAgent handles the full conflict resolution flow:
// prints conflict info, invokes Claude agent, and returns error on failure.
func resolveConflictsWithAgent(repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	return resolveConflictsWithAgentDeps(defaultDeps, repoPath, sourceBranch, targetBranch, conflicts)
}

// resolveConflictsWithAgentDeps is the deps-aware variant of resolveConflictsWithAgent.
func resolveConflictsWithAgentDeps(deps *Deps, repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	printConflictInfo(conflicts)
	return invokeAgentForConflictsDeps(deps, repoPath, sourceBranch, targetBranch, conflicts)
}

// resolveConflictsDetached handles conflict resolution for the detached-HEAD
// push approach, using a refspec-aware prompt so Claude knows to push via refspec.
func resolveConflictsDetached(repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	return resolveConflictsDetachedDeps(defaultDeps, repoPath, sourceBranch, targetBranch, conflicts, pushRef)
}

// resolveConflictsDetachedDeps is the deps-aware variant of resolveConflictsDetached.
func resolveConflictsDetachedDeps(deps *Deps, repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	printConflictInfo(conflicts)
	prompt := generateConflictResolutionPromptWithPush(sourceBranch, targetBranch, conflicts, pushRef)
	return invokeAgentDeps(deps, repoPath, prompt, "")
}

// invokeAgentDeps invokes the agent through the deps-injected AgentInvoker.
func invokeAgentDeps(deps *Deps, workDir, prompt, agentName string) error {
	return deps.Agent.InvokeInteractive(workDir, prompt, agentName)
}

// invokeAgentForConflictsDeps runs the deps-injected agent to resolve merge conflicts.
func invokeAgentForConflictsDeps(deps *Deps, workDir, sourceBranch, targetBranch string, conflicts []string) error {
	prompt := GenerateConflictResolutionPrompt(sourceBranch, targetBranch, conflicts)
	return invokeAgentDeps(deps, workDir, prompt, "")
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
