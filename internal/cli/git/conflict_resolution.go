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

func resolveConflictsDetached(repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	return resolveConflictsDetachedDeps(cli.GetDeps(nil), repoPath, sourceBranch, targetBranch, conflicts, pushRef)
}

func resolveConflictsDetachedDeps(deps *cli.Deps, repoPath, sourceBranch, targetBranch string, conflicts []string, pushRef string) error {
	printConflictInfo(conflicts)
	prompt := ConflictPromptGenWithPush(sourceBranch, targetBranch, conflicts, pushRef)
	return invokeAgentDeps(deps, repoPath, prompt, "")
}

func invokeAgentDeps(deps *cli.Deps, workDir, prompt, agentName string) error {
	return deps.Agent.InvokeInteractive(workDir, prompt, agentName)
}

func invokeAgentForConflictsDeps(deps *cli.Deps, workDir, sourceBranch, targetBranch string, conflicts []string) error {
	prompt := ConflictPromptGen(sourceBranch, targetBranch, conflicts)
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
