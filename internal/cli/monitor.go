package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	dashboardWidth = 70 // Width of the monitor dashboard box
)

var (
	monitorNoWatch  bool
	monitorInterval int
	monitorBranch   string
)

var monitorCmd = &cobra.Command{
	Use:     "monitor",
	Aliases: []string{"mon", "status"},
	Short:   "Display comprehensive agent and task dashboard",
	Long: `Display a dashboard showing agents, tasks, sync status, and statistics.

Sections:
  AGENTS     - Worktree status (running/idle, branch, dirty/clean)
  TASKS      - Ready, in_progress, need review, backlog counts
  SYNC       - Database and git sync status
  STATS      - Overall issue counts and completion rate

Flags:
  -b, --branch      Integration branch to compare against
  -n, --no-watch    One-shot mode (disable auto-refresh)
  -i, --interval    Refresh interval in seconds (default: 5)

Examples:
  loom monitor              # Auto-refresh (default)
  loom monitor -n           # One-shot display
  loom monitor -i 10        # Refresh every 10 seconds`,
	Args: cobra.NoArgs,
	Run:  runMonitor,
}

func init() {
	monitorCmd.Flags().StringVarP(&monitorBranch, "branch", "b", "", "Integration branch to compare against (default: LOOM_DEFAULT_BRANCH or main)")
	monitorCmd.Flags().BoolVarP(&monitorNoWatch, "no-watch", "n", false, "Disable auto-refresh (one-shot mode)")
	monitorCmd.Flags().IntVarP(&monitorInterval, "interval", "i", 5, "Refresh interval in seconds")
	rootCmd.AddCommand(monitorCmd)
}

// loadDaemonManagedAgents reads the daemon state file and returns metadata
// for worktrees under daemon supervision, including their role.
// Returns nil if the file doesn't exist, can't be parsed, or daemon isn't running.
// The stateFilePath should be resolved via ResolveDaemonStatePath().
func loadDaemonManagedAgents(stateFilePath string) map[string]DaemonAgentInfo {
	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		return nil // File doesn't exist or can't be read
	}

	var state DaemonAgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil // Invalid JSON
	}

	// Check if daemon process is still running (PID must be valid and process alive)
	if state.PID <= 0 || !lockfile.IsProcessRunning(state.PID) {
		return nil // Invalid PID or daemon died, don't show stale [D] markers
	}

	result := make(map[string]DaemonAgentInfo)
	for _, agent := range state.Agents {
		if agent.Worktree != "" {
			result[agent.Worktree] = DaemonAgentInfo{
				Managed: true,
				Role:    agent.Role,
			}
		}
	}
	return result
}

func runMonitor(cmd *cobra.Command, args []string) {
	if !monitorNoWatch {
		// Watch mode - show loading message while first data collection runs
		fmt.Print("\033[?25l") // Hide cursor
		fmt.Print("\033[H")    // Move to home position
		fmt.Print("\033[J")    // Clear screen
		fmt.Print("Loading...")
		fmt.Print("\033[?25h") // Show cursor

		// Collect first batch before entering loop (loading message visible during this)
		data := collectMonitorData(100, monitorBranch)
		output := renderDashboard(data)
		fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)
		fmt.Print("\033[?25l")
		fmt.Print("\033[H")
		fmt.Print(fullOutput)
		fmt.Print("\033[J")
		fmt.Print("\033[?25h")

		// Watch mode - refresh in place without flickering
		for {
			time.Sleep(time.Duration(monitorInterval) * time.Second)
			data = collectMonitorData(100, monitorBranch)
			output = renderDashboard(data)

			// Build complete output including status line (no trailing newline)
			fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)

			fmt.Print("\033[?25l") // Hide cursor
			fmt.Print("\033[H")    // Move to home position
			fmt.Print(fullOutput)
			fmt.Print("\033[J")    // Clear from cursor to end of screen
			fmt.Print("\033[?25h") // Show cursor
		}
	} else {
		// One-shot mode - show loading message on stderr
		fmt.Fprint(os.Stderr, "Loading...")
		data := collectMonitorData(100, monitorBranch)
		fmt.Fprint(os.Stderr, "\r          \r") // Clear loading message
		fmt.Print(renderDashboard(data))
	}
}

// CollectMonitorData gathers all dashboard data.
// Exported for use by the HTTP server.
func CollectMonitorData(branch string) *MonitorData {
	return collectMonitorData(100, branch)
}

func collectMonitorData(readyLimit int, branch string) *MonitorData {
	data := &MonitorData{Timestamp: time.Now()}

	// Start stats and sync bd call in parallel with task collection
	var (
		stats      MonitorStats
		syncBdInfo SyncInfo
		wg         sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		stats = collectStatistics()
	}()

	go func() {
		defer wg.Done()
		syncBdInfo = collectSyncBdStatus()
	}()

	// Collect tasks (internally parallel) to get agent-task mapping
	data.Tasks, data.NeedsPlanningTasks, data.ReadyToImplement, data.ReviewTasks, data.InProgressTasks, data.BacklogTasks, data.ClosedTasks, data.AgentTasks = collectTaskStatus(readyLimit)

	// Collect agents, passing the task map for fallback lookup
	var taskIDToAgents map[string][]string
	data.Agents, taskIDToAgents = collectAgentStatus(nil, data.AgentTasks, branch)

	// Detect task conflicts (multiple agents claiming same task)
	data.TaskConflicts = make(map[string][]string)
	for taskID, agents := range taskIDToAgents {
		if len(agents) > 1 {
			data.TaskConflicts[taskID] = agents
		}
	}

	// Wait for stats and sync bd call to finish
	wg.Wait()

	// Combine sync bd result with agent data for git push/pull counts
	data.SyncStatus = completeSyncStatus(syncBdInfo, data.Agents)
	data.Stats = stats

	// Compute Remaining as the sum of work queue categories so it always tallies.
	// bd stats computes Remaining by subtraction which can disagree with the
	// work queue counts due to issues falling between bd ready / bd blocked.
	data.Stats.Remaining = data.Tasks.NeedsPlanning + data.Tasks.ReadyToImplement +
		data.Tasks.NeedReview + data.Tasks.InProgress + data.Tasks.Backlog
	data.Stats.Total = data.Stats.Remaining + data.Stats.Closed
	if data.Stats.Total > 0 {
		data.Stats.Completion = float64(data.Stats.Closed) / float64(data.Stats.Total) * 100
	} else {
		data.Stats.Completion = 0
	}

	return data
}

// CollectAgentStatusOnly returns just agent status without task context.
// Exported for use by the HTTP server.
func CollectAgentStatusOnly(branch string) []AgentStatus {
	agents, _ := collectAgentStatus(nil, nil, branch)
	return agents
}

// getGitHubRemoteURL returns the GitHub HTTPS URL for the origin remote.
// Returns empty string if not a GitHub remote or on error.
func getGitHubRemoteURL(path string) string {
	output, err := RunGitCommand(path, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(output)

	// Convert SSH URL: git@github.com:user/repo.git -> https://github.com/user/repo
	if strings.HasPrefix(url, "git@github.com:") {
		url = strings.TrimPrefix(url, "git@github.com:")
		url = strings.TrimSuffix(url, ".git")
		return "https://github.com/" + url
	}

	// Handle HTTPS URL: https://github.com/user/repo.git -> https://github.com/user/repo
	if strings.Contains(url, "github.com") {
		url = strings.TrimSuffix(url, ".git")
		return url
	}

	return ""
}

// getWorktreeCommitDetails returns the recent commits ahead of the integration branch.
func getWorktreeCommitDetails(path, defaultBranch string, limit int, githubURL string, overrideBranch string) []CommitDetail {
	branch := overrideBranch
	if branch == "" {
		branch = defaultBranch
	}

	output, err := RunGitCommand(path, "log",
		fmt.Sprintf("origin/%s..HEAD", branch),
		fmt.Sprintf("--format=%%h|%%s"),
		"-n", strconv.Itoa(limit))
	if err != nil {
		return nil
	}

	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	commits := make([]CommitDetail, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		commit := CommitDetail{
			Hash:    parts[0],
			Message: parts[1],
		}
		if githubURL != "" {
			commit.URL = githubURL + "/commit/" + parts[0]
		}
		commits = append(commits, commit)
	}
	return commits
}

// getWorktreeFileChanges returns uncommitted file changes from git status.
func getWorktreeFileChanges(path string) []FileChange {
	output, err := RunGitCommand(path, "status", "--porcelain")
	if err != nil {
		return nil
	}

	// Only trim trailing whitespace — leading spaces are significant in porcelain format
	trimmed := strings.TrimRight(output, " \t\n\r")
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	changes := make([]FileChange, 0, len(lines))
	for i, line := range lines {
		if i >= 20 { // Limit to 20 files
			break
		}
		if len(line) < 4 {
			continue
		}
		// Porcelain format: XY filename (first 2 chars are status, then space, then path)
		status := strings.TrimSpace(line[:2])
		filePath := line[3:]
		changes = append(changes, FileChange{
			Status: status,
			Path:   filePath,
		})
	}
	return changes
}

func collectAgentStatus(resolver *Resolver, agentTasks map[string]TaskInfo, branch string) ([]AgentStatus, map[string][]string) { //nolint:unparam // resolver is nil from monitor callers but supports injection for daemon/test use
	if resolver == nil {
		resolver = getDefaultResolver()
	}
	worktrees, err := resolver.DiscoverWorktrees()
	if err != nil {
		return nil, nil
	}

	// Load daemon-managed agents (if any)
	var daemonManaged map[string]DaemonAgentInfo
	if projectDir, err2 := os.Getwd(); err2 == nil {
		daemonStatePath := ResolveDaemonStatePath(projectDir)
		daemonManaged = loadDaemonManagedAgents(daemonStatePath)
	}

	var agents []AgentStatus
	taskIDToAgents := make(map[string][]string) // Track which agents claim which tasks

	// Compute default branch once per tick using already-discovered worktrees
	defaultBranch := GetDefaultBranchForWorktrees(worktrees)

	// Get GitHub remote URL once (all worktrees share the same remote)
	githubURL := ""
	if len(worktrees) > 0 {
		githubURL = getGitHubRemoteURL(worktrees[0].Path)
	}

	for _, wt := range worktrees {
		daemonInfo := daemonManaged[wt.Name]
		agent := AgentStatus{
			Name:          wt.Name,
			Branch:        wt.Branch,
			Workspace:     wt.Workspace,
			Role:          daemonInfo.Role,
			DaemonManaged: daemonInfo.Managed,
		}

		// Check for running agent (lock status)
		lockStatus := GetLockStatus(wt.Path)

		// Also check lock file directly to get TaskID for conflict detection
		if lockInfo, running, _ := CheckLock(wt.Path); running && lockInfo != nil && lockInfo.TaskID != "" {
			taskIDToAgents[lockInfo.TaskID] = append(taskIDToAgents[lockInfo.TaskID], wt.Name)
		}

		if lockStatus != "" {
			// Lock file has status - check if it needs task ID from fallback
			if strings.Contains(lockStatus, "...") {
				if task, ok := agentTasks[wt.Name]; ok {
					// Get actual task status to determine correct state
					taskStatus := getTaskStatus(task.ID)
					// Extract duration part (e.g., " (2m8s)")
					durationIdx := strings.Index(lockStatus, " (")
					durationPart := ""
					if durationIdx != -1 {
						durationPart = lockStatus[durationIdx:]
					}
					// Update state based on task status and agent type
					switch taskStatus {
					case "needs_review":
						// Only show "review" for planning agents
						if strings.HasPrefix(lockStatus, "planning:") {
							lockStatus = fmt.Sprintf("review: %s%s", task.ID, durationPart)
						} else {
							// Implementation agents show "working"
							lockStatus = fmt.Sprintf("working: %s%s", task.ID, durationPart)
						}
					case "closed":
						lockStatus = fmt.Sprintf("done: %s%s", task.ID, durationPart)
					default:
						// Keep original state prefix, just replace "..."
						lockStatus = strings.Replace(lockStatus, "...", task.ID, 1)
					}
				}
			}
			agent.Status = lockStatus
		} else if task, ok := agentTasks[wt.Name]; ok && task.Status == "in_progress" {
			// Task still in_progress but no lock - agent died
			agent.Status = fmt.Sprintf("error: %s", task.ID)
		} else {
			// No lock and no in_progress task - check git status
			// (closed tasks don't trigger "done" fallback - "done" only shows while agent is running)
			clean, _ := IsCleanWorkingTree(wt.Path)
			if clean {
				agent.Status = "ready"
			} else {
				changes := getUncommittedChangesCount(wt.Path)
				if changes > 0 {
					agent.Status = fmt.Sprintf("%d changes", changes)
				} else {
					agent.Status = "dirty"
				}
			}
		}

		// Check ahead/behind integration branch
		agent.Ahead, agent.Behind = getWorktreeGitSyncStatus(wt.Path, defaultBranch, branch)

		// Populate commit details when ahead > 0
		if agent.Ahead > 0 {
			agent.Commits = getWorktreeCommitDetails(wt.Path, defaultBranch, 10, githubURL, branch)
		}

		// Populate file changes (returns nil for clean trees)
		agent.Changes = getWorktreeFileChanges(wt.Path)

		agents = append(agents, agent)
	}

	return agents, taskIDToAgents
}

func getWorktreeGitSyncStatus(path, defaultBranch string, overrideBranch string) (ahead, behind int) {
	branch := overrideBranch
	if branch == "" {
		branch = defaultBranch
	}

	// Count commits ahead/behind integration branch
	// Format: "behind\tahead" (from HEAD's perspective)
	output, err := RunGitCommand(path, "rev-list", "--left-right", "--count",
		fmt.Sprintf("origin/%s...HEAD", branch))
	if err != nil {
		return 0, 0
	}

	// Parse "4\t2" format
	parts := strings.Fields(output)
	if len(parts) == 2 {
		behind, _ = strconv.Atoi(parts[0])
		ahead, _ = strconv.Atoi(parts[1])
	}
	return ahead, behind
}

func collectTaskStatus(readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	var summary TaskSummary
	var needsPlanningTasks []TaskInfo
	var readyToImplementTasks []TaskInfo
	var reviewTasks []TaskInfo
	var inProgressTasks []TaskInfo
	var backlogTasks []TaskInfo
	var closedTasks []TaskInfo
	agentTasks := make(map[string]TaskInfo)

	// Run all 5 bd commands in parallel
	var (
		readyOutput, inProgressOutput, needReviewOutput, backlogOutput, closedOutput string
		readyErr, inProgressErr, needReviewErr, backlogErr, closedErr                error
		wg                                                                           sync.WaitGroup
	)

	wg.Add(5)

	go func() {
		defer wg.Done()
		readyOutput, readyErr = runBdCommand("ready", "--json", "--limit", strconv.Itoa(readyLimit))
	}()

	go func() {
		defer wg.Done()
		inProgressOutput, inProgressErr = runBdCommand("list", "--status=in_progress", "--json")
	}()

	go func() {
		defer wg.Done()
		needReviewOutput, needReviewErr = runBdCommand("list", "--status=review", "--json")
	}()

	go func() {
		defer wg.Done()
		backlogOutput, backlogErr = runBdCommand("blocked", "--json")
	}()

	go func() {
		defer wg.Done()
		closedOutput, closedErr = runBdCommand("list", "--status=closed", "--json", "--limit", "50")
	}()

	wg.Wait()

	// Build unclosed issue ID set from existing responses for accurate blocker filtering.
	// A blocker is only resolved when closed — not when it moves to in_progress/review.
	unclosedIDs := buildUnclosedIDsFromResponses(readyOutput, inProgressOutput, needReviewOutput, backlogOutput)

	// Process ready tasks, split by workflow stage
	// Note: bd ready returns tasks not blocked by dependencies (open, in_progress, review)
	if readyErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(readyOutput), &issues) == nil {
			needsPlanningCount := 0
			readyToImplementCount := 0
			for _, issue := range issues {
				// Skip non-open tasks - they appear in their own sections
				if !IsOpen(issue) {
					continue
				}
				if IsEpic(issue) {
					summary.Epics++
					continue
				}
				if HasUnclosedBlockers(issue.Dependencies, unclosedIDs) {
					// Count these in backlog — they have open deps
					summary.Backlog++
					continue
				}

				// Split by workflow stage using shared predicates
				// SYNC: Must match taskfilter.go NeedsPlan() / ReadyToImplement()
				if ReadyToImplement(issue) {
					summary.ReadyToImplement++
					if readyToImplementCount < 5 {
						readyToImplementTasks = append(readyToImplementTasks, TaskInfo{
							ID:       issue.ID,
							Title:    issue.Title,
							Priority: issue.Priority,
						})
						readyToImplementCount++
					}
				} else {
					// NeedsPlan: no design OR needs-revision label
					summary.NeedsPlanning++
					if needsPlanningCount < 5 {
						needsPlanningTasks = append(needsPlanningTasks, TaskInfo{
							ID:       issue.ID,
							Title:    issue.Title,
							Priority: issue.Priority,
						})
						needsPlanningCount++
					}
				}
			}
		}
	}

	// Process in_progress tasks and build agent-task map
	if inProgressErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(inProgressOutput), &issues) == nil {
			summary.InProgress = len(issues)
			for _, issue := range issues {
				taskInfo := TaskInfo{
					ID:       issue.ID,
					Title:    issue.Title,
					Priority: issue.Priority,
					Status:   "in_progress",
				}
				inProgressTasks = append(inProgressTasks, taskInfo)
				// Build agent-task map from assignee field
				if issue.Assignee != "" {
					agentTasks[issue.Assignee] = taskInfo
				}
			}
		}
	}

	// Process need review tasks (top 5)
	// Note: Don't add to agentTasks - these tasks have status=review meaning
	// the planning agent finished and released its lock. The assignee field
	// still points to the planning agent but it's no longer running.
	if needReviewErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(needReviewOutput), &issues) == nil {
			// All tasks with status=review are review tasks
			summary.NeedReview = len(issues)
			for i, issue := range issues {
				if i >= 5 {
					break
				}
				reviewTasks = append(reviewTasks, TaskInfo{
					ID:       issue.ID,
					Title:    issue.Title,
					Priority: issue.Priority,
				})
			}
		}
	}

	// Process backlog tasks
	if backlogErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(backlogOutput), &issues) == nil {
			summary.Backlog += len(issues)
			// Store up to 20 backlog tasks for display
			for i, issue := range issues {
				if i >= 20 {
					break
				}
				backlogTasks = append(backlogTasks, TaskInfo{
					ID:       issue.ID,
					Title:    issue.Title,
					Priority: issue.Priority,
					Status:   issue.Status,
				})
			}
		}
	}

	// Process closed tasks (top 50)
	if closedErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(closedOutput), &issues) == nil {
			for i, issue := range issues {
				if i >= 50 {
					break
				}
				closedTasks = append(closedTasks, TaskInfo{
					ID:       issue.ID,
					Title:    issue.Title,
					Priority: issue.Priority,
					Status:   issue.Status,
				})
			}
		}
	}

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, closedTasks, agentTasks
}

// collectSyncBdStatus runs the bd sync --status command (safe to call concurrently).
func collectSyncBdStatus() SyncInfo {
	var info SyncInfo
	syncOutput, err := runBdCommand("sync", "--status")
	if err == nil {
		info.DBSynced = !strings.Contains(syncOutput, "error") && !strings.Contains(syncOutput, "failed")
		info.DBLastSync = "recently"
	} else {
		info.DBError = "unable to check"
	}
	return info
}

// completeSyncStatus combines the bd sync result with agent data for git push/pull counts.
func completeSyncStatus(info SyncInfo, agents []AgentStatus) SyncInfo {
	for _, agent := range agents {
		if agent.Ahead > 0 {
			info.GitNeedsPush++
			info.GitPushDetails = append(info.GitPushDetails, WorktreeSyncDetail{
				Name:  agent.Name,
				Count: agent.Ahead,
			})
		}
		if agent.Behind > 0 {
			info.GitNeedsPull++
			info.GitPullDetails = append(info.GitPullDetails, WorktreeSyncDetail{
				Name:  agent.Name,
				Count: agent.Behind,
			})
		}
	}
	return info
}

// collectSyncStatus is the original sequential version, kept for external callers.
func collectSyncStatus(agents []AgentStatus) SyncInfo {
	info := collectSyncBdStatus()
	return completeSyncStatus(info, agents)
}

func collectStatistics() MonitorStats {
	var stats MonitorStats

	// Get stats from bd
	statsOutput, err := runBdCommand("stats", "--json")
	if err == nil {
		var bdStats BdStats
		if json.Unmarshal([]byte(statsOutput), &bdStats) == nil {
			stats.Open = bdStats.Summary.OpenIssues
			stats.Closed = bdStats.Summary.ClosedIssues
			stats.Total = bdStats.Summary.TotalIssues
			stats.InProgress = bdStats.Summary.InProgressIssues
			stats.Blocked = bdStats.Summary.BlockedIssues
			if stats.Total > 0 {
				stats.Completion = float64(stats.Closed) / float64(stats.Total) * 100
			}

			// Remaining = total - closed
			// Note: bd stats total_issues already excludes tombstones
			stats.Remaining = stats.Total - stats.Closed
			if stats.Remaining < 0 {
				stats.Remaining = 0
			}

			// Review = total - open - inProgress - closed - blocked - deferred - pinned
			// Note: bd stats total_issues already excludes tombstones
			stats.Review = stats.Total - stats.Open - stats.InProgress - stats.Closed -
				stats.Blocked - bdStats.Summary.DeferredIssues - bdStats.Summary.PinnedIssues
			if stats.Review < 0 {
				stats.Review = 0
			}
		}
	}

	return stats
}

// collectReadyTasksByPriority returns counts of ready tasks grouped by priority (0-4).
// It iterates ready tasks (excluding epics, in_progress, and review) and returns
// a map of priority -> count for Prometheus metrics.
func collectReadyTasksByPriority(readyLimit int) map[int]int {
	counts := make(map[int]int)
	// Initialize all priorities to 0
	for i := 0; i <= 4; i++ {
		counts[i] = 0
	}

	output, err := runBdCommand("ready", "--json", "--limit", strconv.Itoa(readyLimit))
	if err != nil {
		return counts
	}

	var issues []BdIssue
	if json.Unmarshal([]byte(output), &issues) != nil {
		return counts
	}

	for _, issue := range issues {
		if !IsOpen(issue) {
			continue
		}
		if IsEpic(issue) {
			continue
		}
		// Skip tasks with needs-revision label (these are being re-planned)
		if HasNeedsRevision(issue) {
			continue
		}
		p := issue.Priority
		if p < 0 || p > 4 {
			p = 4
		}
		counts[p]++
	}

	return counts
}

// buildUnclosedIDsFromResponses builds a set of unclosed issue IDs from the
// JSON responses already fetched by collectTaskStatus's parallel bd commands.
// Issues from ready/in_progress/review are unclosed by definition; backlog
// issues need a status check since bd blocked may include closed issues.
func buildUnclosedIDsFromResponses(readyJSON, inProgressJSON, reviewJSON, backlogJSON string) map[string]bool {
	unclosed := make(map[string]bool)

	addAll := func(jsonStr string) {
		var issues []BdIssue
		if json.Unmarshal([]byte(jsonStr), &issues) == nil {
			for _, issue := range issues {
				unclosed[issue.ID] = true
			}
		}
	}

	// Ready, in_progress, and review issues are all unclosed by definition
	addAll(readyJSON)
	addAll(inProgressJSON)
	addAll(reviewJSON)

	// Backlog (blocked) issues need status filtering
	var backlogIssues []BdIssue
	if json.Unmarshal([]byte(backlogJSON), &backlogIssues) == nil {
		for _, issue := range backlogIssues {
			if issue.Status != "closed" {
				unclosed[issue.ID] = true
			}
		}
	}

	return unclosed
}

func runBdCommand(args ...string) (string, error) {
	result := execCommand(GetBeadsDir(), "bd", args...)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Stdout, nil
}
