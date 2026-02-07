package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/spf13/cobra"
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

// MonitorData holds all dashboard information
type MonitorData struct {
	Timestamp          time.Time
	Agents             []AgentStatus
	Tasks              TaskSummary
	NeedsPlanningTasks []TaskInfo          // Ready tasks without design (top 5)
	ReadyToImplement   []TaskInfo          // Ready tasks with design (top 5)
	ReviewTasks        []TaskInfo          // top 5 need review tasks
	InProgressTasks    []TaskInfo          // all in_progress tasks
	BacklogTasks       []TaskInfo          // backlog tasks (top 20)
	AgentTasks         map[string]TaskInfo // agent name -> current task (from assignee)
	TaskConflicts      map[string][]string // TaskID -> agent names (if multiple agents claim same task)
	SyncStatus         SyncInfo
	Stats              MonitorStats
}

// AgentStatus represents a single agent/worktree status
type AgentStatus struct {
	Name          string `json:"name"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`                    // "ready", "3 changes", "running (plan, 5m ago)"
	Ahead         int    `json:"ahead"`                     // commits ahead of integration branch
	Behind        int    `json:"behind"`                    // commits behind integration branch
	Workspace     string `json:"workspace"`                 // workspace name (empty in legacy mode)
	DaemonManaged bool   `json:"daemon_managed,omitempty"`  // true if under daemon supervision
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
	Backlog          int `json:"backlog"`
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

// Dependency represents a dependency relationship from bd ready --json
type Dependency struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"` // "parent-child" or "blocks"
	CreatedAt   string `json:"created_at"`
	CreatedBy   string `json:"created_by"`
}

// BdIssue represents an issue from bd list --json
type BdIssue struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Status       string       `json:"status"`
	Priority     int          `json:"priority"`
	IssueType    string       `json:"issue_type"`
	Design       string       `json:"design"`
	Assignee     string       `json:"assignee"`
	Labels       []string     `json:"labels"`
	Dependencies []Dependency `json:"dependencies"`
}

// BdStats represents output from bd stats --json
type BdStats struct {
	Summary struct {
		TotalIssues  int `json:"total_issues"`
		OpenIssues   int `json:"open_issues"`
		ClosedIssues int `json:"closed_issues"`
	} `json:"summary"`
}

// DaemonAgentState represents the daemon-agents.json file format.
// This matches the DaemonState written by daemon_cmd.go.
type DaemonAgentState struct {
	PID    int                     `json:"pid"`
	Agents []DaemonAgentStateEntry `json:"agents"`
}

// DaemonAgentStateEntry represents a single agent in daemon-agents.json
type DaemonAgentStateEntry struct {
	Worktree string `json:"worktree"`
	Status   string `json:"status"`
}

// loadDaemonManagedAgents reads the daemon state file and returns a set of
// worktree names that are under daemon supervision.
// Returns nil if the file doesn't exist, can't be parsed, or daemon isn't running.
// The state file is expected at ~/.loom/daemon-agents.json (global location).
func loadDaemonManagedAgents() map[string]bool {
	daemonStatePath := filepath.Join(GetConfigDir(), "daemon-agents.json")
	data, err := os.ReadFile(daemonStatePath)
	if err != nil {
		return nil // File doesn't exist or can't be read
	}

	var state DaemonAgentState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil // Invalid JSON
	}

	// Check if daemon process is still running (PID must be valid and process alive)
	if state.PID <= 0 || !IsProcessRunning(state.PID) {
		return nil // Invalid PID or daemon died, don't show stale [D] markers
	}

	result := make(map[string]bool)
	for _, agent := range state.Agents {
		if agent.Worktree != "" {
			result[agent.Worktree] = true
		}
	}
	return result
}

func runMonitor(cmd *cobra.Command, args []string) {
	if !monitorNoWatch {
		// Watch mode - show loading message while first data collection runs
		fmt.Print("\033[?25l")  // Hide cursor
		fmt.Print("\033[H")     // Move to home position
		fmt.Print("\033[J")     // Clear screen
		fmt.Print("Loading...")
		fmt.Print("\033[?25h")  // Show cursor

		// Collect first batch before entering loop (loading message visible during this)
		data := collectMonitorData(100)
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
			data = collectMonitorData(100)
			output = renderDashboard(data)

			// Build complete output including status line (no trailing newline)
			fullOutput := output + fmt.Sprintf("\nPress Ctrl+C to exit (refreshing every %ds)", monitorInterval)

			fmt.Print("\033[?25l")  // Hide cursor
			fmt.Print("\033[H")     // Move to home position
			fmt.Print(fullOutput)
			fmt.Print("\033[J")     // Clear from cursor to end of screen
			fmt.Print("\033[?25h")  // Show cursor
		}
	} else {
		// One-shot mode - show loading message on stderr
		fmt.Fprint(os.Stderr, "Loading...")
		data := collectMonitorData(100)
		fmt.Fprint(os.Stderr, "\r          \r") // Clear loading message
		fmt.Print(renderDashboard(data))
	}
}

// CollectMonitorData gathers all dashboard data.
// Exported for use by the HTTP server.
func CollectMonitorData() *MonitorData {
	return collectMonitorData(100)
}

func collectMonitorData(readyLimit int) *MonitorData {
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
	data.Tasks, data.NeedsPlanningTasks, data.ReadyToImplement, data.ReviewTasks, data.InProgressTasks, data.BacklogTasks, data.AgentTasks = collectTaskStatus(readyLimit)

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

	// Wait for stats and sync bd call to finish
	wg.Wait()

	// Combine sync bd result with agent data for git push/pull counts
	data.SyncStatus = completeSyncStatus(syncBdInfo, data.Agents)
	data.Stats = stats

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

	// Load daemon-managed agents (if any)
	daemonManaged := loadDaemonManagedAgents()

	var agents []AgentStatus
	taskIDToAgents := make(map[string][]string) // Track which agents claim which tasks

	// Compute default branch once per tick using already-discovered worktrees
	defaultBranch := GetDefaultBranchForWorktrees(worktrees)

	for _, wt := range worktrees {
		agent := AgentStatus{
			Name:          wt.Name,
			Branch:        wt.Branch,
			Workspace:     wt.Workspace,
			DaemonManaged: daemonManaged[wt.Name],
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
		agent.Ahead, agent.Behind = getWorktreeGitSyncStatus(wt.Path, defaultBranch)

		agents = append(agents, agent)
	}

	return agents, taskIDToAgents
}

func getWorktreeGitSyncStatus(path, defaultBranch string) (ahead, behind int) {
	branch := monitorBranch
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

func collectTaskStatus(readyLimit int) (TaskSummary, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, []TaskInfo, map[string]TaskInfo) {
	var summary TaskSummary
	var needsPlanningTasks []TaskInfo
	var readyToImplementTasks []TaskInfo
	var reviewTasks []TaskInfo
	var inProgressTasks []TaskInfo
	var backlogTasks []TaskInfo
	agentTasks := make(map[string]TaskInfo)

	// Run all 4 bd commands in parallel
	var (
		readyOutput, inProgressOutput, needReviewOutput, backlogOutput string
		readyErr, inProgressErr, needReviewErr, backlogErr             error
		wg                                                              sync.WaitGroup
	)

	wg.Add(4)

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

	wg.Wait()

	// Process ready tasks, split by workflow stage
	// Note: bd ready returns tasks not blocked by dependencies (open, in_progress, review)
	if readyErr == nil {
		var issues []BdIssue
		if json.Unmarshal([]byte(readyOutput), &issues) == nil {
			needsPlanningCount := 0
			readyToImplementCount := 0
			for _, issue := range issues {
				// Skip in_progress tasks - they appear in In Progress section
				if issue.Status == "in_progress" {
					continue
				}
				// Skip review tasks - they appear in Need Review section
				if issue.Status == "review" {
					continue
				}
				// Skip epics - agents shouldn't work on epics directly
				if issue.IssueType == "epic" {
					continue
				}

				// Check for needs-revision label
				hasRevisionLabel := false
				for _, label := range issue.Labels {
					if label == "needs-revision" {
						hasRevisionLabel = true
						break
					}
				}

				// Split by whether task has a design (and no revision label)
				if issue.Design != "" && !hasRevisionLabel {
					// Has design and no revision needed - ready to implement
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
					// No design OR needs revision - needs planning
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
			summary.Backlog = len(issues)
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

	return summary, needsPlanningTasks, readyToImplementTasks, reviewTasks, inProgressTasks, backlogTasks, agentTasks
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
		}
		if agent.Behind > 0 {
			info.GitNeedsPull++
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
			if stats.Total > 0 {
				stats.Completion = float64(stats.Closed) / float64(stats.Total) * 100
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
		if issue.Status == "in_progress" || issue.Status == "review" {
			continue
		}
		if issue.IssueType == "epic" {
			continue
		}
		// Skip tasks with needs-revision label (consistent with collectTaskStatus)
		hasRevisionLabel := false
		for _, label := range issue.Labels {
			if label == "needs-revision" {
				hasRevisionLabel = true
				break
			}
		}
		if hasRevisionLabel {
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

func runBdCommand(args ...string) (string, error) {
	result := execCommand(GetBeadsDir(), "bd", args...)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Stdout, nil
}

// truncateToWidth truncates s to fit within maxWidth display columns,
// appending "..." if truncated. Uses display width (not byte length)
// so multi-byte unicode characters are handled correctly.
func truncateToWidth(s string, maxWidth int) string {
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	return runewidth.Truncate(s, maxWidth, "...")
}

// padRight pads s with spaces to exactly width display columns.
// Unlike fmt.Sprintf("%-Ns"), this uses display width so multi-byte
// unicode characters are handled correctly.
func padRight(s string, width int) string {
	sw := runewidth.StringWidth(s)
	if sw >= width {
		return s
	}
	return s + strings.Repeat(" ", width-sw)
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

	// Detect workspace mode from agent data
	hasWorkspace := false
	for _, agent := range data.Agents {
		if agent.Workspace != "" {
			hasWorkspace = true
			break
		}
	}

	if hasWorkspace {
		renderAgentsWorkspace(&sb, data.Agents)
	} else {
		renderAgentsLegacy(&sb, data.Agents)
	}
	if len(data.Agents) == 0 {
		sb.WriteString(renderBoxLine("  No agents found"))
	}

	// Tasks section
	sb.WriteString(renderBoxSeparator())
	sb.WriteString(renderBoxLine(" WORK QUEUE"))
	sb.WriteString(renderBoxSeparator())
	taskSummary := fmt.Sprintf("  Plan: %-3d  Impl: %-3d  Review: %-3d  Active: %-3d  Backlog: %-3d",
		data.Tasks.NeedsPlanning, data.Tasks.ReadyToImplement, data.Tasks.NeedReview, data.Tasks.InProgress, data.Tasks.Backlog)
	sb.WriteString(renderBoxLine(taskSummary))

	// Needs Planning tasks (top 5)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  NEEDS PLANNING (%d):", data.Tasks.NeedsPlanning)))
	if len(data.NeedsPlanningTasks) > 0 {
		for _, task := range data.NeedsPlanningTasks {
			renderTaskLine(&sb, task)
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
			cleaned := task
			cleaned.Title = strings.TrimPrefix(task.Title, "[Need Review] ")
			renderTaskLine(&sb, cleaned)
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// Ready to Implement tasks (top 5)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  READY TO IMPLEMENT (%d):", data.Tasks.ReadyToImplement)))
	if len(data.ReadyToImplement) > 0 {
		for _, task := range data.ReadyToImplement {
			renderTaskLine(&sb, task)
		}
	} else {
		sb.WriteString(renderBoxLine("    (none)"))
	}

	// In progress tasks (all)
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  IN PROGRESS (%d):", data.Tasks.InProgress)))
	if len(data.InProgressTasks) > 0 {
		for _, task := range data.InProgressTasks {
			renderTaskLine(&sb, task)
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

func renderTaskLine(sb *strings.Builder, task TaskInfo) {
	prefix := fmt.Sprintf("    [P%d] %s: ", task.Priority, task.ID)
	maxTitle := dashboardWidth - 4 - displayWidth(prefix) // content area (66) minus prefix
	title := truncateToWidth(task.Title, maxTitle)
	sb.WriteString(renderBoxLine(prefix + title))
}

func renderAgentLine(sb *strings.Builder, agent AgentStatus, indent string) {
	statusIcon := "✓"
	if strings.HasPrefix(agent.Status, "planning:") ||
		strings.HasPrefix(agent.Status, "working:") ||
		strings.HasPrefix(agent.Status, "done:") ||
		strings.HasPrefix(agent.Status, "review:") ||
		strings.HasPrefix(agent.Status, "error:") {
		statusIcon = "●"
	} else if strings.Contains(agent.Status, "changes") || agent.Status == "dirty" {
		statusIcon = "●"
	}

	// Build agent name with [D] prefix if daemon-managed
	displayName := agent.Name
	if agent.DaemonManaged {
		displayName = "[D] " + agent.Name
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

	// Calculate available width for status dynamically to ensure the line fits
	contentWidth := dashboardWidth - 4 // 66
	nameCol := padRight(truncateToWidth(displayName, 14), 14)
	branchCol := padRight(truncateToWidth(agent.Branch, 18), 18)
	syncWidth := displayWidth(syncIndicator)
	fixedCols := displayWidth(indent) + 14 + 1 + 18 + 1 + 1 + 1 // indent + name + sp + branch + sp + icon + sp
	maxStatusWidth := contentWidth - fixedCols - syncWidth
	if maxStatusWidth < 0 {
		maxStatusWidth = 0
	}
	status := truncateToWidth(agent.Status, maxStatusWidth)

	leftPart := indent + nameCol + " " + branchCol + " " + statusIcon + " " + status

	// Right-align sync indicator
	leftWidth := displayWidth(leftPart)
	padding := contentWidth - leftWidth - syncWidth
	if padding < 0 {
		padding = 0
	}
	line := leftPart + strings.Repeat(" ", padding) + syncIndicator
	sb.WriteString(renderBoxLine(line))
}

func renderAgentsLegacy(sb *strings.Builder, agents []AgentStatus) {
	for _, agent := range agents {
		renderAgentLine(sb, agent, "  ")
	}
}

func renderAgentsWorkspace(sb *strings.Builder, agents []AgentStatus) {
	// Group agents by workspace
	groups := make(map[string][]AgentStatus)
	for _, agent := range agents {
		ws := agent.Workspace
		if ws == "" {
			ws = "(legacy)"
		}
		groups[ws] = append(groups[ws], agent)
	}

	// Sort workspace names
	var wsNames []string
	for name := range groups {
		wsNames = append(wsNames, name)
	}
	sort.Strings(wsNames)

	for _, ws := range wsNames {
		sb.WriteString(renderBoxLine(fmt.Sprintf("  [%s]", ws)))
		for _, agent := range groups[ws] {
			renderAgentLine(sb, agent, "   ")
		}
	}
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
// accounting for Unicode characters that may display as double width
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
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
