package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"
)

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

	// Needs Planning tasks
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  NEEDS PLANNING (%d):", data.Tasks.NeedsPlanning)))
	renderTaskSection(&sb, data.NeedsPlanningTasks)

	// Need review tasks
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  NEEDS REVIEW (%d):", data.Tasks.NeedReview)))
	renderTaskSection(&sb, data.ReviewTasks)

	// Ready to Implement tasks
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  READY TO IMPLEMENT (%d):", data.Tasks.ReadyToImplement)))
	renderTaskSection(&sb, data.ReadyToImplement)

	// In progress tasks
	sb.WriteString(renderBoxLine(""))
	sb.WriteString(renderBoxLine(fmt.Sprintf("  IN PROGRESS (%d):", data.Tasks.InProgress)))
	renderTaskSection(&sb, data.InProgressTasks)

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
	statsLine := fmt.Sprintf("  Remaining: %-4d  Closed: %-4d  Total: %-4d  Done: %.0f%%",
		data.Stats.Remaining, data.Stats.Closed, data.Stats.Total, data.Stats.Completion)
	sb.WriteString(renderBoxLine(statsLine))

	// Footer
	sb.WriteString(renderBoxBottom())

	return sb.String()
}

// renderTaskSection renders a list of tasks, showing at most 10 in the CLI.
// Tasks beyond the limit are summarized as "and N more...".
func renderTaskSection(sb *strings.Builder, tasks []TaskInfo) {
	if len(tasks) == 0 {
		sb.WriteString(renderBoxLine("    (none)"))
		return
	}
	const displayLimit = 10
	display := tasks
	if len(tasks) > displayLimit {
		display = tasks[:displayLimit]
	}
	for _, task := range display {
		renderTaskLine(sb, task)
	}
	if remaining := len(tasks) - displayLimit; remaining > 0 {
		sb.WriteString(renderBoxLine(fmt.Sprintf("    ... and %d more", remaining)))
	}
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
