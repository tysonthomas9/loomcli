package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var prAll bool
var prWorkspace string

var prCmd = &cobra.Command{
	Use:               "pr [worktree] [target]",
	Short:             "Create GitHub PR from worktree branch",
	GroupID:           "git",
	ValidArgsFunction: branchCompletion,
	Long: `Create a GitHub pull request from a worktree branch to a target branch.

Unlike 'loom push' which directly merges, 'loom pr' creates a PR for code
review before merging. Uses 'gh pr create' under the hood.

Arguments:
  worktree    Source worktree to create PR from (e.g., falcon)
  target      Target branch for the PR (default: main or per-repo default)

Flags:
  -a, --all          Create PRs for all worktree branches
  -W, --workspace    Workspace to operate on (workspace mode only)

Examples:
  loom pr falcon                        # Create PR from falcon to main
  loom pr falcon develop                # Create PR from falcon to develop
  loom pr --all                         # Create PRs for all worktrees
  loom pr --all main                    # Create PRs for all worktrees to main
  loom pr -W myworkspace falcon         # Create PR in specific workspace`,
	Args: func(cmd *cobra.Command, args []string) error {
		if IsWorkspaceMode() {
			if prAll {
				if len(args) > 1 {
					return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
				}
				return nil
			}
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("requires 1-2 arguments: <worktree> [target]")
			}
			return nil
		}
		// Legacy mode
		if prAll {
			if len(args) > 1 {
				return fmt.Errorf("--all flag accepts at most 1 argument (target branch)")
			}
			return nil
		}
		if len(args) < 1 || len(args) > 2 {
			return fmt.Errorf("requires 1-2 arguments: <worktree> [target]")
		}
		return nil
	},
	Run: runPR,
}

func init() {
	prCmd.Flags().BoolVarP(&prAll, "all", "a", false, "Create PRs for all worktree branches")
	prCmd.Flags().StringVarP(&prWorkspace, "workspace", "W", "", "Workspace to operate on")
	rootCmd.AddCommand(prCmd)
}

func runPR(cmd *cobra.Command, args []string) {
	if err := checkGhInstalled(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if IsWorkspaceMode() {
		if prAll && prWorkspace != "" {
			fmt.Fprintln(os.Stderr, "Error: --all and --workspace are mutually exclusive")
			os.Exit(1)
		}

		targetBranch := ""
		sourceBranch := ""

		if prAll {
			if len(args) == 1 {
				targetBranch = args[0]
			}
			prAllWorkspaces(targetBranch)
		} else {
			sourceBranch = args[0]
			if len(args) == 2 {
				targetBranch = args[1]
			}

			resolver, err := NewResolver()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
				os.Exit(1)
			}

			wsName := prWorkspace
			if wsName != "" {
				if err := resolver.SetWorkspace(wsName); err != nil {
					available := resolver.WorkspaceNames()
					fmt.Fprintf(os.Stderr, "Error: workspace %q not found. Available: %v\n", wsName, available)
					os.Exit(1)
				}
			}

			prWorkspaceRepos(resolver, sourceBranch, targetBranch)
		}
		return
	}

	// Legacy mode
	targetBranch := ""
	sourceBranch := ""

	if prAll {
		if len(args) == 1 {
			targetBranch = args[0]
		}
		prAllWorktrees(targetBranch)
	} else {
		sourceBranch = args[0]
		if len(args) == 2 {
			targetBranch = args[1]
		}
		if targetBranch == "" {
			targetBranch = "main"
		}
		prWorktree(sourceBranch, targetBranch)
	}
}

func prAllWorkspaces(targetBranch string) {
	resolver, err := NewResolver()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating resolver: %v\n", err)
		os.Exit(1)
	}

	wsNames := resolver.WorkspaceNames()
	if len(wsNames) == 0 {
		fmt.Println("No workspaces found.")
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Creating PRs for all workspaces -> %s\n", targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	for _, wsName := range wsNames {
		fmt.Printf("--- Workspace: %s ---\n", wsName)
		if err := resolver.SetWorkspace(wsName); err != nil {
			fmt.Fprintf(os.Stderr, "Error setting workspace %s: %v\n", wsName, err)
			continue
		}

		worktrees, err := resolver.DiscoverWorktrees()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error discovering repos in workspace %s: %v\n", wsName, err)
			continue
		}

		if len(worktrees) == 0 {
			fmt.Printf("No repos found in workspace %s\n", wsName)
			continue
		}

		prWorkspaceWorktrees(worktrees, "", targetBranch)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Println("All workspace PRs created!")
	fmt.Println("=========================================")
}

func prWorkspaceRepos(resolver *Resolver, sourceBranch, targetBranch string) {
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering repos: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Printf("No repos found in workspace %s\n", resolver.WorkspaceName())
		return
	}

	fmt.Println("=========================================")
	fmt.Printf("Creating PRs for workspace %q: %s -> %s\n", resolver.WorkspaceName(), sourceBranch, targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	prWorkspaceWorktrees(worktrees, sourceBranch, targetBranch)

	fmt.Println("=========================================")
	fmt.Printf("Workspace %q PRs complete!\n", resolver.WorkspaceName())
	fmt.Println("=========================================")
}

func prWorkspaceWorktrees(worktrees []WorktreeInfo, sourceBranch, targetBranch string) {
	type result struct {
		repo    string
		success bool
		err     string
		url     string
	}
	var results []result

	for _, wt := range worktrees {
		if wt.Repo == nil {
			continue
		}

		target := targetBranch
		if target == "" {
			target = wt.Repo.DefaultBranch
			if target == "" {
				target = "main"
			}
		}

		source := sourceBranch
		if source == "" {
			source = wt.Branch
		}

		remote := wt.Repo.Remote

		url, err := createPR(wt.Path, source, target, remote)
		if err != nil {
			results = append(results, result{repo: wt.Name, success: false, err: err.Error()})
		} else {
			results = append(results, result{repo: wt.Name, success: true, url: url})
		}
		fmt.Println("")
	}

	// Print summary
	fmt.Println("--- Summary ---")
	for _, r := range results {
		if r.success {
			if r.url != "" {
				fmt.Printf("  ✓ %s: %s\n", r.repo, r.url)
			} else {
				fmt.Printf("  ✓ %s\n", r.repo)
			}
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.repo, r.err)
		}
	}
}

func createPR(repoPath, sourceBranch, targetBranch, remote string) (string, error) {
	r := resolveRemote(remote)

	// Validate refs
	if err := validateGitRef(sourceBranch); err != nil {
		return "", err
	}
	if err := validateGitRef(targetBranch); err != nil {
		return "", err
	}
	if err := validateGitRef(r); err != nil {
		return "", err
	}

	fmt.Println("=========================================")
	fmt.Printf("PR: %s -> %s (repo: %s, remote: %s)\n", sourceBranch, targetBranch, repoPath, r)
	fmt.Println("=========================================")

	// Fetch latest
	if err := GitFetchRemote(repoPath, remote); err != nil {
		return "", fmt.Errorf("fetching: %v", err)
	}

	// Check if there are commits to create PR for
	hasCommits, err := HasCommitsBetweenRemote(repoPath, remote, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return "", nil
	}

	// Push source branch to remote
	fmt.Printf("Pushing %s to %s...\n", sourceBranch, r)
	if err := GitPushRemote(repoPath, remote, sourceBranch); err != nil {
		return "", fmt.Errorf("pushing branch: %v", err)
	}

	// Generate PR title and body
	title, body := generatePRInfo(repoPath, r, targetBranch, sourceBranch)

	// Create PR using gh CLI
	result := defaultDeps.Exec.Run(repoPath, "gh", "pr", "create",
		"--base", targetBranch,
		"--head", sourceBranch,
		"--title", title,
		"--body", body)

	if result.Err != nil {
		// Check if PR already exists
		errMsg := result.Stderr + result.Stdout
		if strings.Contains(errMsg, "already exists") {
			return getExistingPRURL(repoPath, sourceBranch)
		}
		return "", fmt.Errorf("creating PR: %s", strings.TrimSpace(errMsg))
	}

	prURL := strings.TrimSpace(result.Stdout)
	fmt.Printf("✓ PR created: %s\n", prURL)
	return prURL, nil
}

func createPRLegacy(repoPath, sourceBranch, targetBranch string) (string, error) {
	// Validate refs
	if err := validateGitRef(sourceBranch); err != nil {
		return "", err
	}
	if err := validateGitRef(targetBranch); err != nil {
		return "", err
	}

	fmt.Println("=========================================")
	fmt.Printf("PR: %s -> %s\n", sourceBranch, targetBranch)
	fmt.Println("=========================================")

	// Fetch latest
	if err := GitFetch(repoPath); err != nil {
		return "", fmt.Errorf("fetching: %v", err)
	}

	// Check if there are commits to create PR for
	hasCommits, err := HasCommitsBetween(repoPath, targetBranch, sourceBranch)
	if err == nil && !hasCommits {
		fmt.Printf("✓ Already up to date (no new commits in %s)\n", sourceBranch)
		return "", nil
	}

	// Push source branch to remote
	if err := GitPush(repoPath, sourceBranch); err != nil {
		return "", fmt.Errorf("pushing branch: %v", err)
	}

	// Generate PR title and body
	title, body := generatePRInfo(repoPath, "origin", targetBranch, sourceBranch)

	// Create PR using gh CLI
	result := defaultDeps.Exec.Run(repoPath, "gh", "pr", "create",
		"--base", targetBranch,
		"--head", sourceBranch,
		"--title", title,
		"--body", body)

	if result.Err != nil {
		// Check if PR already exists
		errMsg := result.Stderr + result.Stdout
		if strings.Contains(errMsg, "already exists") {
			return getExistingPRURL(repoPath, sourceBranch)
		}
		return "", fmt.Errorf("creating PR: %s", strings.TrimSpace(errMsg))
	}

	prURL := strings.TrimSpace(result.Stdout)
	fmt.Printf("✓ PR created: %s\n", prURL)
	return prURL, nil
}

func prAllWorktrees(targetBranch string) {
	fmt.Println("=========================================")
	fmt.Printf("Creating PRs for all worktrees -> %s\n", targetBranchDisplay(targetBranch))
	fmt.Println("=========================================")
	fmt.Println("")

	worktrees, err := DiscoverWorktrees()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering worktrees: %v\n", err)
		os.Exit(1)
	}

	if len(worktrees) == 0 {
		fmt.Println("No worktrees found.")
		return
	}

	target := targetBranch
	if target == "" {
		target = "main"
	}

	for _, wt := range worktrees {
		prWorktree(wt.Branch, target)
		fmt.Println("")
	}

	fmt.Println("=========================================")
	fmt.Printf("All worktree PRs created!\n")
	fmt.Println("=========================================")
}

func prWorktree(sourceBranch, targetBranch string) {
	scriptDir, err := GetScriptDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	url, prErr := createPRLegacy(scriptDir, sourceBranch, targetBranch)
	if prErr != nil {
		fmt.Fprintf(os.Stderr, "Error creating PR: %v\n", prErr)
		return
	}
	if url != "" {
		fmt.Printf("PR URL: %s\n", url)
	}
}

func generatePRInfo(repoPath, remote, targetBranch, sourceBranch string) (string, string) {
	// Get commit messages between target and source
	logRange := fmt.Sprintf("%s/%s..%s/%s", remote, targetBranch, remote, sourceBranch)
	output, err := RunGitCommand(repoPath, "log", logRange, "--format=%s", "--reverse")
	if err != nil {
		// Fallback: try without remote prefix for source (local branch)
		logRange = fmt.Sprintf("%s/%s..%s", remote, targetBranch, sourceBranch)
		output, err = RunGitCommand(repoPath, "log", logRange, "--format=%s", "--reverse")
		if err != nil {
			// Use branch name as title if we can't get commit messages
			return sourceBranch, ""
		}
	}

	subjects := strings.Split(strings.TrimSpace(output), "\n")
	if len(subjects) == 0 || (len(subjects) == 1 && subjects[0] == "") {
		return sourceBranch, ""
	}

	title := strings.ReplaceAll(subjects[0], "\n", " ")

	var body string
	if len(subjects) == 1 {
		// Single commit: just use the footer
		body = "---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"
	} else {
		// Multiple commits: bulleted list
		var lines []string
		for _, s := range subjects {
			if s != "" {
				lines = append(lines, fmt.Sprintf("- %s", s))
			}
		}
		body = strings.Join(lines, "\n") + "\n\n---\nCreated with [loom](https://github.com/tysonthomas9/loomcli)"
	}

	return title, body
}

func getExistingPRURL(repoPath, sourceBranch string) (string, error) {
	if err := validateGitRef(sourceBranch); err != nil {
		return "", err
	}
	result := defaultDeps.Exec.Run(repoPath, "gh", "pr", "view", sourceBranch, "--json", "url", "-q", ".url")
	if result.Err != nil {
		return "", fmt.Errorf("getting existing PR URL: %s", strings.TrimSpace(result.Stderr))
	}
	url := strings.TrimSpace(result.Stdout)
	fmt.Printf("✓ PR already exists: %s\n", url)
	return url, nil
}

func checkGhInstalled() error {
	result := defaultDeps.Exec.Run(".", "gh", "--version")
	if result.Err != nil {
		return fmt.Errorf("'gh' CLI not found. Install it from https://cli.github.com/ and run 'gh auth login'")
	}
	return nil
}
