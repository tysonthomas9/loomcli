package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// boxWidth is the fixed width for the monitor dashboard box
const boxWidth = 70

// titleMaxLen is the max length for truncated task titles
const titleMaxLen = 45

var (
	monitorWatch    bool
	monitorInterval int
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
  -w, --watch       Auto-refresh mode (default: 5s interval)
  -i, --interval    Refresh interval in seconds (default: 5)

Examples:
  vibecli monitor              # One-shot display
  vibecli monitor --watch      # Auto-refresh
  vibecli monitor -w -i 10     # Refresh every 10 seconds`,
	Args: cobra.NoArgs,
	Run:  runMonitor,
}

func init() {
	monitorCmd.Flags().BoolVarP(&monitorWatch, "watch", "w", false, "Auto-refresh mode")
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
	AgentTasks         map[string]TaskInfo // agent name -> current task (from assignee)
	TaskConflicts      map[string][]string // TaskID -> agent names (if multiple agents claim same task)
	SyncStatus         SyncInfo
	Stats              MonitorStats
}

// AgentStatus represents a single agent/worktree status
type AgentStatus struct {
	Name   string
	Branch string
	Status string // "ready", "3 changes", "running (plan, 5m ago)"
	Ahead  int    // commits ahead of integration branch
	Behind int    // commits behind integration branch
}

// TaskInfo represents a task with basic info
type TaskInfo struct {
	ID       string
	Title    string
	Priority int
	Status   string // "in_progress", "closed", "open"
}

// TaskSummary holds task counts by category
type TaskSummary struct {
	NeedsPlanning    int // Ready tasks without design
	ReadyToImplement int // Ready tasks with approved design
	InProgress       int
	NeedReview       int
	Blocked          int
}

// SyncInfo holds sync status information
type SyncInfo struct {
	DBSynced     bool
	DBLastSync   string
	DBError      string
	GitNeedsPush int
	GitNeedsPull int
}

// MonitorStats holds overall statistics
type MonitorStats struct {
	Open       int
	Closed     int
	Total      int
	Completion float64
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
	if monitorWatch {
		// Watch mode - clear screen and refresh
		for {
			clearScreen()
			data := collectMonitorData()
			fmt.Print(renderDashboard(data))
			fmt.Printf("\nPress Ctrl+C to exit (refreshing every %ds)\n", monitorInterval)
			time.Sleep(time.Duration(monitorInterval) * time.Second)
		}
	} else {
		// One-shot mode
		data := collectMonitorData()
		fmt.Print(renderDashboard(data))
	}
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func collectMonitorData() *MonitorData {
	data := &MonitorData{Timestamp: time.Now()}

	// Collect tasks FIRST to get agent-task mapping
	data.Tasks, data.NeedsPlanningTasks, data.ReadyToImplement, data.ReviewTasks, data.InProgressTasks, data.AgentTasks = collectTaskStatus()

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
	defaultBranch := GetDefaultBranch()

	// Count commits ahead/behind integration branch
	// Format: "behind\tahead" (from HEAD's perspective)
	output, err := RunGitCommand(path, "rev-list", "--left-right", "--count",
		fmt.Sprintf("origin/%s...HEAD", defaultBranch))
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

func collectTaskStatus() (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
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

	// Get blocked tasks count
	blockedOutput, err := runBdCommand("blocked", "--json")
	if err == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(blockedOutput), &issues) == nil {
			summary.Blocked = len(issues)
		}
	}

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, agentTasks
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
	cmd := exec.Command("bd", args...)
	output, err := cmd.Output()
	return string(output), err
}

func truncateTitle(s string) string {
	if len(s) <= titleMaxLen {
		return s
	}
	return s[:titleMaxLen-3] + "..."
}

// Rendering functions

func renderDashboard(data *MonitorData) string {
	var sb strings.Builder

	// Header
	sb.WriteString(renderBoxTop())
	sb.WriteString(renderBoxLine(centerText("VIBECLI MONITOR", boxWidth-4)))
	sb.WriteString(renderBoxLine(centerText(fmt.Sprintf("Last updated: %s", data.Timestamp.Format("15:04:05")), boxWidth-4)))

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

		// Right-align sync indicator
		contentWidth := boxWidth - 4
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

	// Check for agent/work mismatch warnings
	planningAgents := 0
	implementAgents := 0
	for _, agent := range data.Agents {
		// Count by explicit state prefix
		if strings.HasPrefix(agent.Status, "planning:") {
			planningAgents++
		} else if strings.HasPrefix(agent.Status, "working:") {
			implementAgents++
		}
	}

	// Show warnings if agents don't match available work
	if planningAgents > 0 && data.Tasks.NeedsPlanning == 0 {
		sb.WriteString(renderBoxLine(""))
		sb.WriteString(renderBoxLine("  ⚠️  Planning agents running but no tasks need planning"))
	}
	if implementAgents > 0 && data.Tasks.ReadyToImplement == 0 {
		sb.WriteString(renderBoxLine(""))
		sb.WriteString(renderBoxLine("  ⚠️  Implementation agents running but no tasks ready"))
	}
	if len(data.TaskConflicts) > 0 {
		sb.WriteString(renderBoxLine(""))
		sb.WriteString(renderBoxLine("  ⚠️  TASK CONFLICTS - Multiple agents claiming same task:"))
		for taskID, agents := range data.TaskConflicts {
			agentList := strings.Join(agents, ", ")
			sb.WriteString(renderBoxLine(fmt.Sprintf("    • %s: %s", taskID, agentList)))
		}
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
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateTitle(task.Title))
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
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateTitle(title))
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
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateTitle(task.Title))
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
			line := fmt.Sprintf("    [P%d] %s: %s", task.Priority, task.ID, truncateTitle(task.Title))
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
	return "╔" + strings.Repeat("═", boxWidth-2) + "╗\n"
}

func renderBoxBottom() string {
	return "╚" + strings.Repeat("═", boxWidth-2) + "╝\n"
}

func renderBoxSeparator() string {
	return "╠" + strings.Repeat("═", boxWidth-2) + "╣\n"
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
	padding := boxWidth - 4 - contentWidth
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
