package git

import (
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// ConflictPromptGen generates a conflict resolution prompt.
// Set by agent package's init() to break import cycle.
var ConflictPromptGen func(sourceBranch, targetBranch string, conflicts []string) string

// ConflictPromptGenWithPush generates a conflict resolution prompt with custom push ref.
// Set by agent package's init() to break import cycle.
var ConflictPromptGenWithPush func(sourceBranch, targetBranch string, conflicts []string, pushRef string) string

func resolveConflictsWithAgent(repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	return resolveConflictsWithAgentDeps(cli.GetDeps(nil), repoPath, sourceBranch, targetBranch, conflicts)
}

func resolveConflictsWithAgentDeps(deps *cli.Deps, repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	printConflictInfo(conflicts)
	return invokeAgentForConflictsDeps(deps, repoPath, sourceBranch, targetBranch, conflicts)
}

func resolveLocalConflictsWithAgentDeps(deps *cli.Deps, repoPath, sourceBranch, targetBranch string, conflicts []string) error {
	printConflictInfo(conflicts)
	var prompt string
	if ConflictPromptGenWithPush != nil {
		prompt = ConflictPromptGenWithPush(sourceBranch, targetBranch, conflicts, "")
	} else {
		prompt = fmt.Sprintf("Resolve merge conflicts in local-only repo: %s -> %s\n\nConflicted files:\n", sourceBranch, targetBranch)
		for _, f := range conflicts {
			prompt += "  - " + f + "\n"
		}
	}
	return invokeAgentDeps(deps, repoPath, prompt, "")
}

func resolveConflictsDetached(repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	return resolveConflictsDetachedDeps(cli.GetDeps(nil), repoPath, sourceBranch, targetBranch, conflicts, pushRef)
}

func resolveConflictsDetachedDeps(deps *cli.Deps, repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	printConflictInfo(conflicts)
	var prompt string
	if ConflictPromptGenWithPush != nil {
		prompt = ConflictPromptGenWithPush(sourceBranch, targetBranch, conflicts, pushRef)
	} else {
		prompt = fmt.Sprintf("Resolve merge conflicts in: %s -> %s (push to %s)\n\nConflicted files:\n", sourceBranch, targetBranch, pushRef)
		for _, f := range conflicts {
			prompt += "  - " + f + "\n"
		}
	}
	return invokeAgentDeps(deps, repoPath, prompt, "")
}

func invokeAgentDeps(deps *cli.Deps, workDir, prompt, agentName string) error {
	return deps.Agent.InvokeInteractive(workDir, prompt, agentName)
}

func invokeAgentForConflictsDeps(deps *cli.Deps, workDir, sourceBranch, targetBranch string, conflicts []string) error {
	var prompt string
	if ConflictPromptGen != nil {
		prompt = ConflictPromptGen(sourceBranch, targetBranch, conflicts)
	} else {
		// Fallback when agent package init() has not registered the generator
		prompt = fmt.Sprintf("Resolve merge conflicts in: %s -> %s\n\nConflicted files:\n", sourceBranch, targetBranch)
		for _, f := range conflicts {
			prompt += "  - " + f + "\n"
		}
	}
	return invokeAgentDeps(deps, workDir, prompt, "")
}

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
