package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	dashboardWidth   = 70 // Width of the monitor dashboard box
	taskTitleMaxLen  = 45 // Maximum length for task titles in dashboard
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
  TASKS      - Ready, in_progress, need review, blocked counts
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

// MonitorData holds all dashboard information
type MonitorData struct {
	Timestamp          time.Time
	Agents             []AgentStatus
	Tasks              TaskSummary
	NeedsPlanningTasks []TaskInfo          // Ready tasks without design (top 5)
	ReadyToImplement   []TaskInfo          // Ready tasks with design (top 5)
	ReviewTasks        []TaskInfo          // top 5 need review tasks
	InProgressTasks    []TaskInfo          // all in_progress tasks
	BlockedTasks       []TaskInfo          // blocked tasks (top 20)
	AgentTasks         map[string]TaskInfo // agent name -> current task (from assignee)
	TaskConflicts      map[string][]string // TaskID -> agent names (if multiple agents claim same task)
	SyncStatus         SyncInfo
	Stats              MonitorStats
}

// AgentStatus represents a single agent/worktree status
type AgentStatus struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Status string `json:"status"` // "ready", "3 changes", "running (plan, 5m ago)"
	Ahead  int    `json:"ahead"`  // commits ahead of integration branch
	Behind int    `json:"behind"` // commits behind integration branch
}

// TaskInfo represents a task with basic info
type TaskInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	Status   string `json:"status"` // "in_progress", "closed", "open"
}

// TaskSummary holds task counts by category
type TaskSummary struct {
	NeedsPlanning    int `json:"needs_planning"`     // Ready tasks without design
	ReadyToImplement int `json:"ready_to_implement"` // Ready tasks with approved design
	InProgress       int `json:"in_progress"`
	NeedReview       int `json:"need_review"`
	Blocked          int `json:"blocked"`
}


// SyncInfo holds sync status information
type SyncInfo struct {
	DBSynced     bool   `json:"db_synced"`
	DBLastSync   string `json:"db_last_sync"`
	DBError      string `json:"db_error,omitempty"`
	GitNeedsPush int    `json:"git_needs_push"`
	GitNeedsPull int    `json:"git_needs_pull"`
}

// MonitorStats holds overall statistics
type MonitorStats struct {
	Open       int     `json:"open"`
	Closed     int     `json:"closed"`
	Total      int     `json:"total"`
	Completion float64 `json:"completion"`
}

// BdIssue represents an issue from bd list --json
type BdIssue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Priority  int    `json:"priority"`
	IssueType string `json:"issue_type"`
	Design    string `json:"design"`
	Assignee  string `json:"assignee"`
}

// BdStats represents output from bd stats --json
type BdStats struct {
	Summary struct {
		TotalIssues  int `json:"total_issues"`
		OpenIssues   int `json:"open_issues"`
		ClosedIssues int `json:"closed_issues"`
	} `json:"summary"`
}

func runMonitor(cmd *cobra.Command, args []string) {
	if !monitorNoWatch {
		// Watch mode - refresh in place without flickering
		for {
			data := collectMonitorData()
			output := renderDashboard(data)

			// Build complete output including status line (no trailing newline)
			fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)

			fmt.Print("\033[?25l")  // Hide cursor
			fmt.Print("\033[H")     // Move to home position
			fmt.Print(fullOutput)
			fmt.Print("\033[J")     // Clear from cursor to end of screen
			fmt.Print("\033[?25h")  // Show cursor

			time.Sleep(time.Duration(monitorInterval) * time.Second)
		}
	} else {
		// One-shot mode
		data := collectMonitorData()
		fmt.Print(renderDashboard(data))
	}
}

// CollectMonitorData gathers all dashboard data.
// Exported for use by the HTTP server.
func CollectMonitorData() *MonitorData {
	return collectMonitorData()
}

func collectMonitorData() *MonitorData {
	data := &MonitorData{Timestamp: time.Now()}

	// Collect tasks FIRST to get agent-task mapping
	data.Tasks, data.NeedsPlanningTasks, data.ReadyToImplement, data.ReviewTasks, data.InProgressTasks, data.BlockedTasks, data.AgentTasks = collectTaskStatus()

	// Collect agents, passing the task map for fallback lookup
	var taskIDToAgents map[string][]string
	data.Agents, taskIDToAgents = collectAgentStatus(data.AgentTasks)

	// Detect task conflicts (multiple agents claiming same task)
	data.TaskConflicts = make(map[string][]string)
	for taskID, agents := range taskIDToAgents {
		if len(agents) > 1 {
			data.TaskConflicts[taskID] = agents
		}
	}

	// Collect sync status
	data.SyncStatus = collectSyncStatus(data.Agents)

	// Collect stats
	data.Stats = collectStatistics()

	return data
}

// CollectAgentStatusOnly returns just agent status without task context.
// Exported for use by the HTTP server.
func CollectAgentStatusOnly() []AgentStatus {
	agents, _ := collectAgentStatus(nil)
	return agents
}

func collectAgentStatus(agentTasks map[string]TaskInfo) ([]AgentStatus, map[string][]string) {
	worktrees, err := DiscoverWorktrees()
	if err != nil {
		return nil, nil
	}

	var agents []AgentStatus
	taskIDToAgents := make(map[string][]string) // Track which agents claim which tasks

	for _, wt := range worktrees {
		agent := AgentStatus{
			Name:   wt.Name,
			Branch: wt.Branch,
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
		agent.Ahead, agent.Behind = getWorktreeGitSyncStatus(wt.Path)

		agents = append(agents, agent)
	}

	return agents, taskIDToAgents
}

func getWorktreeGitSyncStatus(path string) (ahead, behind int) {
	branch := monitorBranch
	if branch == "" {
		branch = GetDefaultBranch()
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

func collectTaskStatus() (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	var summary TaskSummary
	var needsPlanningTasks []TaskInfo
	var readyToImplementTasks []TaskInfo
	var reviewTasks []TaskInfo
	var inProgressTasks []TaskInfo
	agentTasks := make(map[string]TaskInfo)

	// Get ready tasks, split by workflow stage
	readyOutput, err := runBdCommand("ready", "--json")
	if err == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(readyOutput), &issues) == nil {
			needsPlanningCount := 0
			readyToImplementCount := 0
			for _, issue := range issues {
				// Skip [Need Review] tasks - they appear in Need Review section
				if strings.Contains(issue.Title, "[Need Review]") {
					continue
				}
				// Skip in_progress tasks - they appear in In Progress section
				if issue.Status == "in_progress" {
					continue
				}
				// Skip epics - agents shouldn't work on epics directly
				if issue.IssueType == "epic" {
					continue
				}

				// Split by whether task has a design
				if issue.Design != "" {
					// Has design - ready to implement
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
					// No design - needs planning
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

	// Get in_progress tasks (all) and build agent-task map
	inProgressOutput, err := runBdCommand("list", "--status=in_progress", "--json")
	if err == nil {
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

	// Get need review tasks (top 5)
	// Note: Don't add to agentTasks - these tasks have status=open meaning
	// the planning agent finished and released its lock. The assignee field
	// still points to the planning agent but it's no longer running.
	needReviewOutput, err := runBdCommand("list", "--status=open", "--json")
	if err == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(needReviewOutput), &issues) == nil {
			count := 0
			for _, issue := range issues {
				if strings.Contains(issue.Title, "[Need Review]") {
					summary.NeedReview++
					if count < 5 {
						reviewTasks = append(reviewTasks, TaskInfo{
							ID:       issue.ID,
							Title:    issue.Title,
							Priority: issue.Priority,
						})
						count++
					}
				}
			}
		}
	}

	// Get blocked tasks
	var blockedTasks []TaskInfo
	blockedOutput, err := runBdCommand("blocked", "--json")
	if err == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(blockedOutput), &issues) == nil {
			summary.Blocked = len(issues)
			// Store up to 20 blocked tasks for display
			for i, issue := range issues {
				if i >= 20 {
					break
				}
				blockedTasks = append(blockedTasks, TaskInfo{
					ID:       issue.ID,
					Title:    issue.Title,
					Priority: issue.Priority,
					Status:   issue.Status,
				})
			}
		}
	}

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, blockedTasks, agentTasks
}

func collectSyncStatus(agents []AgentStatus) SyncInfo {
	var info SyncInfo

	// Check bd sync status
	syncOutput, err := runBdCommand("sync", "--status")
	if err == nil {
		info.DBSynced = !strings.Contains(syncOutput, "error") && !strings.Contains(syncOutput, "failed")
		info.DBLastSync = "recently"
	} else {
		info.DBError = "unable to check"
	}

	// Count git push/pull needs from agents
	for _, agent := range agents {
		if agent.Ahead > 0 {
			info.GitNeedsPush++
		}
		if agent.Behind > 0 {
			info.GitNeedsPull++
		}
	}

	return info
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
			if stats.Total > 0 {
				stats.Completion = float64(stats.Closed) / float64(stats.Total) * 100
			}
		}
	}

	return stats
}

func runBdCommand(args ...string) (string, error) {
	result := execCommand(".", "bd", args...)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Stdout, nil
}

func truncateString(s string) string {
	if len(s) <= taskTitleMaxLen {
		return s
	}
	return s[:taskTitleMaxLen-3] + "..."
}

// Rendering functions

func renderDashboard(data *MonitorData) string {
	var sb strings.Builder

	// Header
	sb.WriteString(renderBoxTop())
	sb.WriteString(renderBoxLine(centerText("LOOM", dashboardWidth-4)))
	sb.WriteString(renderBoxLine(centerText(fmt.Sprintf("Last updated: %s", data.Timestamp.Format("15:04:05")), dashboardWidth-4)))

	// Agents section
	sb.WriteString(renderBoxSeparator())
	sb.WriteString(renderBoxLine(" AGENTS"))
	sb.WriteString(renderBoxSeparator())
	for _, agent := range data.Agents {
		statusIcon := "✓"
		// Running agents show explicit state prefixes
		if strings.HasPrefix(agent.Status, "planning:") ||
			strings.HasPrefix(agent.Status, "working:") ||
			strings.HasPrefix(agent.Status, "done:") ||
			strings.HasPrefix(agent.Status, "review:") ||
			strings.HasPrefix(agent.Status, "error:") {
			statusIcon = "●"
		} else if strings.Contains(agent.Status, "changes") || agent.Status == "dirty" {
			statusIcon = "●"
		}

		// Build sync indicator (↑ahead ↓behind)
		syncIndicator := ""
		if agent.Ahead > 0 {
			syncIndicator += fmt.Sprintf("↑%d", agent.Ahead)
		}
		if agent.Behind > 0 {
			if syncIndicator != "" {
				syncIndicator += " "
			}
			syncIndicator += fmt.Sprintf("↓%d", agent.Behind)
		}

		// Format left part with fixed widths
		leftPart := fmt.Sprintf("  %-10s %-18s %s %-24s", agent.Name, agent.Branch, statusIcon, agent.Status)

		// Right-align sync indicator (box content width is 66)
		contentWidth := 66
		leftWidth := displayWidth(leftPart)
		syncWidth := displayWidth(syncIndicator)
		padding := contentWidth - leftWidth - syncWidth
		if padding < 0 {
			padding = 0
		}
		line := leftPart + strings.Repeat(" ", padding) + syncIndicator
		sb.WriteString(renderBoxLine(line))
	}
	if len(data.Agents) == 0 {
		sb.WriteString(renderBoxLine("  No agents found"))
	}

	// Tasks section
	sb.WriteString(renderBoxSeparator())
	sb.WriteString(renderBoxLine(" WORK QUEUE"))
	sb.WriteString(renderBoxSeparator())
	taskSummary := fmt.Sprintf("  Plan: %-3d  Impl: %-3d  Review: %-3d  Active: %-3d  Blocked: %-3d",
		data.Tasks.NeedsPlanning, data.Tasks.ReadyToImplement, data.Tasks.NeedReview, data.Tasks.InProgress, data.Tasks.Blocked)
	sb.WriteString(renderBoxLine(taskSummary))

	// Needs Planning tasks (top 5)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  NEEDS PLANNING (%d):", data.Tasks.NeedsPlanning)))
	if len(data.NeedsPlanningTasks) > 0 {
		for _, task := range data.NeedsPlanningTasks {
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateString(task.Title))
			sb.WriteString(renderBoxLine(line))
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// Need review tasks (top 5)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  NEEDS REVIEW (%d):", data.Tasks.NeedReview)))
	if len(data.ReviewTasks) > 0 {
		for _, task := range data.ReviewTasks {
			// Strip [Need Review] prefix from title for cleaner display
			title := strings.TrimPrefix(task.Title, "[Need Review] ")
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateString(title))
			sb.WriteString(renderBoxLine(line))
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// Ready to Implement tasks (top 5)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  READY TO IMPLEMENT (%d):", data.Tasks.ReadyToImplement)))
	if len(data.ReadyToImplement) > 0 {
		for _, task := range data.ReadyToImplement {
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateString(task.Title))
			sb.WriteString(renderBoxLine(line))
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// In progress tasks (all)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  IN PROGRESS (%d):", data.Tasks.InProgress)))
	if len(data.InProgressTasks) > 0 {
		for _, task := range data.InProgressTasks {
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateString(task.Title))
			sb.WriteString(renderBoxLine(line))
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// Sync section
	sb.WriteString(renderBoxSeparator())
	sb.WriteString(renderBoxLine(" SYNC STATUS"))
	sb.WriteString(renderBoxSeparator())

	dbStatus := "✓ synced"
	if !data.SyncStatus.DBSynced {
		dbStatus = "⚠ " + data.SyncStatus.DBError
	}
	sb.WriteString(renderBoxLine(fmt.Sprintf("  Database:  %s", dbStatus)))

	gitStatus := "✓ all synced"
	if data.SyncStatus.GitNeedsPush > 0 || data.SyncStatus.GitNeedsPull > 0 {
		parts := []string{}
		if data.SyncStatus.GitNeedsPush > 0 {
			parts = append(parts, fmt.Sprintf("%d need push", data.SyncStatus.GitNeedsPush))
		}
		if data.SyncStatus.GitNeedsPull > 0 {
			parts = append(parts, fmt.Sprintf("%d need pull", data.SyncStatus.GitNeedsPull))
		}
		gitStatus = "⚠ " + strings.Join(parts, ", ")
	}
	sb.WriteString(renderBoxLine(fmt.Sprintf("  Git:       %s", gitStatus)))

	// Stats section
	sb.WriteString(renderBoxSeparator())
	sb.WriteString(renderBoxLine(" STATS"))
	sb.WriteString(renderBoxSeparator())
	statsLine := fmt.Sprintf("  Open: %-4d  Closed: %-4d  Total: %-4d  Completion: %.0f%%",
		data.Stats.Open, data.Stats.Closed, data.Stats.Total, data.Stats.Completion)
	sb.WriteString(renderBoxLine(statsLine))

	// Footer
	sb.WriteString(renderBoxBottom())

	return sb.String()
}

func renderBoxTop() string {
	return "╔" + strings.Repeat("═", dashboardWidth-2) + "╗\n"
}

func renderBoxBottom() string {
	return "╚" + strings.Repeat("═", dashboardWidth-2) + "╝\n"
}

func renderBoxSeparator() string {
	return "╠" + strings.Repeat("═", dashboardWidth-2) + "╣\n"
}

// displayWidth returns the terminal display width of a string
// accounting for Unicode characters that display as single width
func displayWidth(s string) int {
	width := 0
	for range s {
		// All runes count as 1 display width in a typical terminal
		// This correctly handles Unicode arrows, symbols, etc.
		width++
	}
	return width
}

func renderBoxLine(content string) string {
	// Use display width instead of byte length for padding calculation
	contentWidth := displayWidth(content)
	padding := dashboardWidth - 4 - contentWidth
	if padding < 0 {
		padding = 0
	}
	return "║ " + content + strings.Repeat(" ", padding) + " ║\n"
}

func centerText(text string, width int) string {
	textWidth := displayWidth(text)
	if textWidth >= width {
		return text
	}
	padding := (width - textWidth) / 2
	return strings.Repeat(" ", padding) + text + strings.Repeat(" ", width-textWidth-padding)
}
