package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// worktreeCompletion provides completion for worktree names
func worktreeCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Only complete the first argument (worktree name)
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	worktrees, err := DiscoverWorktrees()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var completions []string
	for _, wt := range worktrees {
		// Format: "name\tdescription" for shell completion
		completions = append(completions, wt.Name+"\t"+wt.Branch)
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// branchCompletion provides completion for git branch names
func branchCompletion(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	branches, err := GetGitBranches()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return branches, cobra.ShellCompDirectiveNoFileComp
}

// worktreeThenBranchCompletion provides worktree names for first arg, branches for second
func worktreeThenBranchCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return worktreeCompletion(cmd, args, toComplete)
	}
	if len(args) == 1 {
		return branchCompletion(cmd, args, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// GetGitBranches returns all local and remote branch names
func GetGitBranches() ([]string, error) {
	output, err := RunGitCommand(".", "branch", "-a", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}

	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		// Clean up remote branch names (origin/branch -> branch for display)
		branch := strings.TrimPrefix(line, "origin/")
		// Skip HEAD references
		if strings.Contains(branch, "HEAD") {
			continue
		}
		branches = append(branches, branch)
	}

	// Remove duplicates (local and remote versions of same branch)
	seen := make(map[string]bool)
	var unique []string
	for _, b := range branches {
		if !seen[b] {
			seen[b] = true
			unique = append(unique, b)
		}
	}

	return unique, nil
}
