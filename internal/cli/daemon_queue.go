package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var daemonQueueCmd = &cobra.Command{
	Use:   "queue <agent-name>",
	Short: "Preview an agent's filtered work queue",
	Long: `Show what tasks a specific agent would pick up next.

This command loads loom.yaml, resolves the named agent's role constraints
(task filter, skills, max priority, source repos), fetches ready issues,
scores them through the task router, and displays the results.

Works without the daemon running — pure CLI, no IPC/socket needed.

Examples:
  loom daemon queue spark       Show what spark would pick up
  loom daemon queue falcon      Show falcon's filtered queue`,
	Args: cobra.ExactArgs(1),
	Run:  runDaemonQueue,
}

// ResolveRoleConfigStatic looks up a role by name without requiring a Daemon instance.
// For built-in roles, merges any user-defined config on top of defaults.
// For custom roles, requires a prompt_file that must exist.
func ResolveRoleConfigStatic(roleName string, config *DaemonConfig, projectDir string) (RoleConfig, error) {
	if builtInRoles[roleName] {
		rc := RoleConfig{Description: fmt.Sprintf("Built-in %s agent", roleName)}
		if userRC, ok := config.ResolveRole(roleName); ok {
			rc = mergeRoleConfig(rc, userRC)
		}
		return rc, nil
	}

	rc, ok := config.ResolveRole(roleName)
	if !ok {
		return RoleConfig{}, fmt.Errorf("role %q not found (not a built-in role and not defined in config.Roles)", roleName)
	}

	if rc.PromptFile == "" {
		return RoleConfig{}, fmt.Errorf("custom role %q missing prompt_file", roleName)
	}

	promptPath := rc.PromptFile
	if !filepath.IsAbs(promptPath) {
		promptPath = filepath.Join(projectDir, promptPath)
	}
	if _, err := os.Stat(promptPath); err != nil {
		return RoleConfig{}, fmt.Errorf("prompt file %q not found: %w", promptPath, err)
	}
	rc.PromptFile = promptPath

	return rc, nil
}

// findAgentEntry finds an agent by worktree name in the config.
// Returns the agent entry and nil error on success, or an error listing available names.
func findAgentEntry(config *DaemonConfig, worktreeName string) (*AgentEntry, error) {
	for i := range config.Agents {
		if config.Agents[i].Worktree == worktreeName {
			return &config.Agents[i], nil
		}
	}

	available := make([]string, 0, len(config.Agents))
	for _, a := range config.Agents {
		available = append(available, a.Worktree)
	}
	return nil, fmt.Errorf("agent %q not found in loom.yaml agents; available: %s", worktreeName, strings.Join(available, ", "))
}

func runDaemonQueue(cmd *cobra.Command, args []string) {
	agentName := args[0]

	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}

	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: loading config: %v\n", err)
		os.Exit(1)
	}

	agent, err := findAgentEntry(config, agentName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	rc, err := ResolveRoleConfigStatic(agent.Role, config, projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolving role %q: %v\n", agent.Role, err)
		os.Exit(1)
	}

	resolveQueueSourceRepos(agent)
	constraints := MergeRoleConstraints(rc, *agent)

	printQueueHeader(agentName, agent, constraints)

	issues, err := fetchReadyIssues(agent.Parent, agent.Repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetching ready issues: %v\n", err)
		os.Exit(1)
	}

	if len(issues) == 0 {
		fmt.Println("\nNo tasks in the ready queue.")
		return
	}

	unclosedIDs, err := fetchUnclosedIssueIDs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: fetching unclosed issues: %v\n", err)
		os.Exit(1)
	}

	matched, rejections := scoreQueueCandidates(issues, constraints, unclosedIDs)
	printQueueResults(matched, rejections)
}

// resolveQueueSourceRepos populates agent.SourceRepos from workspace config.
func resolveQueueSourceRepos(agent *AgentEntry) {
	ws, wsErr := ResolveActiveWorkspace()
	if wsErr == nil && ws != nil && len(ws.Repos) > 0 {
		sourceRepos, repoErr := resolveAgentRepos(*agent, ws.Repos)
		if repoErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: resolving agent repos: %v\n", repoErr)
		} else {
			agent.SourceRepos = sourceRepos
		}
	} else if (len(agent.Repos) > 0 || len(agent.RepoGroups) > 0) && (ws == nil || len(ws.Repos) == 0) {
		fmt.Fprintf(os.Stderr, "Warning: no workspace configured; repo affinity disabled\n")
	}
}

// scoreQueueCandidates scores all candidate issues and partitions into matched/rejected.
func scoreQueueCandidates(issues []BdIssue, constraints RoleConstraints, unclosedIDs map[string]bool) ([]TaskMatch, map[string]int) {
	var matched []TaskMatch
	rejections := make(map[string]int)

	for _, issue := range issues {
		m := MatchTask(issue, constraints, unclosedIDs)
		if m.Score > 0 {
			matched = append(matched, m)
		} else {
			rejections[m.Reason]++
		}
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Score != matched[j].Score {
			return matched[i].Score > matched[j].Score
		}
		if matched[i].Issue.Priority != matched[j].Issue.Priority {
			return matched[i].Issue.Priority < matched[j].Issue.Priority
		}
		return matched[i].Issue.ID < matched[j].Issue.ID
	})

	return matched, rejections
}

// printQueueResults displays matched tasks (top 5) and rejection summary.
func printQueueResults(matched []TaskMatch, rejections map[string]int) {
	fmt.Printf("\n%d tasks match agent constraints", len(matched))
	if len(matched) > 5 {
		fmt.Printf(" (showing top 5)")
	}
	fmt.Println()

	limit := 5
	if len(matched) < limit {
		limit = len(matched)
	}
	for i := 0; i < limit; i++ {
		m := matched[i]
		fmt.Printf("  %s [P%d] score:%d  %s\n", m.Issue.ID, m.Issue.Priority, m.Score, m.Issue.Title)
		fmt.Printf("    %s\n", m.Reason)
	}

	if len(rejections) == 0 {
		return
	}

	fmt.Println()
	totalRejected := 0
	for _, count := range rejections {
		totalRejected += count
	}
	fmt.Printf("%d filtered:\n", totalRejected)

	reasons := make([]string, 0, len(rejections))
	for r := range rejections {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Printf("  %d %s\n", rejections[r], r)
	}
}

func printQueueHeader(agentName string, agent *AgentEntry, constraints RoleConstraints) {
	fmt.Printf("Agent: %s\n", agentName)
	fmt.Printf("Role:  %s\n", agent.Role)

	taskFilter := constraints.TaskFilter
	if taskFilter == "" {
		taskFilter = "has_design (default)"
	}
	fmt.Printf("Filter: %s\n", taskFilter)

	if len(constraints.Skills) > 0 {
		fmt.Printf("Skills: %s\n", strings.Join(constraints.Skills, ", "))
	}
	if constraints.MaxPriority != nil {
		fmt.Printf("Max priority: P%d\n", *constraints.MaxPriority)
	}
	if len(constraints.SourceRepos) > 0 {
		fmt.Printf("Source repos: %s\n", strings.Join(constraints.SourceRepos, ", "))
	}
	if agent.Parent != "" {
		fmt.Printf("Epic: %s\n", agent.Parent)
	}
}
