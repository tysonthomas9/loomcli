package daemon

import (
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
)

// printAgentStatus prints the detailed status for a single agent.
func printAgentStatus(agent DaemonAgentStatus) {
	statusIcon := statusToIcon(agent.Status)
	fmt.Printf("  %s %s (%s)\n", statusIcon, agent.Worktree, agent.Role)

	// A parked agent has no PID, no run history and no task: the only useful
	// things to print are why it is parked and how to un-park it.
	if agent.Status == "parked" {
		printParkedAgentStatus(agent)
		return
	}

	// PID line with uptime for running agents
	if agent.PID > 0 {
		if !agent.LastStart.IsZero() {
			uptime := time.Since(agent.LastStart)
			fmt.Printf("      PID: %d (running %s)\n", agent.PID, formatDaemonDuration(uptime))
		} else {
			fmt.Printf("      PID: %d\n", agent.PID)
		}
	}

	if agent.EpicID != "" {
		fmt.Printf("      Epic: %s\n", agent.EpicID)
	}
	if agent.TaskID != "" {
		fmt.Printf("      Task: %s\n", agent.TaskID)
	}
	if agent.OwnershipLeaseID != "" {
		fmt.Printf("      Ownership: fence %d\n", agent.OwnershipFencingToken)
	}

	printAgentBranchInfo(agent)
	printAgentDiagnostics(agent)
}

// printParkedAgentStatus renders the parked detail line, always ending in the
// command that resumes the agent so an operator never has to look it up.
func printParkedAgentStatus(agent DaemonAgentStatus) {
	detail := "parked"
	if agent.DesiredState != "" {
		detail = fmt.Sprintf("parked (%s", agent.DesiredState)
		if agent.DrainExpiresAt != nil {
			detail += ", expires " + agent.DrainExpiresAt.UTC().Format(time.RFC3339)
		}
		detail += ")"
	}
	resume := agent.ResumeCommand
	if resume == "" {
		resume = "loom data agent start " + agent.Worktree
	}
	fmt.Printf("      %s — resume: %s\n", detail, resume)
}

// printAgentDiagnostics prints the post-run signals (last exit code, error
// class, retry counters, stop reason) for an agent. Split out of
// printAgentStatus to keep that function under the funlen threshold.
func printAgentDiagnostics(agent DaemonAgentStatus) {
	if agent.PID == 0 && !agent.LastStart.IsZero() && !agent.LastExit.IsZero() {
		runtime := agent.LastExit.Sub(agent.LastStart)
		fmt.Printf("      Last run: %s (exit %d)\n", formatDaemonDuration(runtime), agent.LastExitCode)
	}
	if agent.LastErrorClass != "" {
		fmt.Printf("      Last error: %s\n", agent.LastErrorClass)
	}
	if agent.ClaimsGated {
		fmt.Printf("      gated (claims held)\n")
	}
	if agent.NoWorkCount > 0 {
		fmt.Printf("      NoWork: %d\n", agent.NoWorkCount)
	}
	if agent.BlockCount > 0 {
		fmt.Printf("      Block cycles: %d\n", agent.BlockCount)
	}
	if !agent.BackoffUntil.IsZero() && agent.BackoffUntil.After(time.Now()) {
		remaining := time.Until(agent.BackoffUntil)
		fmt.Printf("      Backoff: %s remaining\n", formatDaemonDuration(remaining))
	}
	if agent.RestartCount > 0 {
		fmt.Printf("      Restarts: %d\n", agent.RestartCount)
	}
	if agent.StopReason != "" {
		if agent.StopReason == string(supervisor.StopReasonFatalError) && agent.LastExitCode != 0 {
			fmt.Printf("      Stopped: %s (exit %d)\n", agent.StopReason, agent.LastExitCode)
		} else {
			fmt.Printf("      Stopped: %s\n", agent.StopReason)
		}
	}
}

// printAgentBranchInfo prints the branch and git sync status for an agent.
func printAgentBranchInfo(agent DaemonAgentStatus) {
	if agent.WorktreePath == "" {
		return
	}
	branchName, err := cli.GetCurrentBranch(agent.WorktreePath)
	if err != nil {
		return
	}

	branchLine := fmt.Sprintf("      Branch: %s", branchName)

	// Parse default branch from RemoteBranch (e.g. "origin/main" -> "main")
	defaultBranch := "main"
	if agent.RemoteBranch != "" {
		parts := strings.SplitN(agent.RemoteBranch, "/", 2)
		if len(parts) == 2 {
			defaultBranch = parts[1]
		}
	}

	ahead, behind := monitor.GetWorktreeGitSyncStatus(agent.WorktreePath, defaultBranch, "")
	branchLine += fmt.Sprintf("  ↑%d ↓%d", ahead, behind)

	changes := getUncommittedChangesCount(agent.WorktreePath)
	if changes > 0 {
		branchLine += fmt.Sprintf("  ● %d changes", changes)
	}

	fmt.Println(branchLine)
}

// printQuarantinedTasks prints the quarantined-task section of daemon status.
// Empty for most daemons; a pending (write-failed) entry keeps retrying via
// the supervisor sweep and is flagged rather than hidden.
func printQuarantinedTasks(tasks []supervisor.QuarantinedTaskInfo) {
	if len(tasks) == 0 {
		return
	}
	fmt.Println("")
	fmt.Println("Quarantined tasks:")
	for _, qt := range tasks {
		line := fmt.Sprintf("  %s  kills: %d", qt.TaskID, qt.Count)
		if qt.LastKillReason != "" {
			line += fmt.Sprintf("  last: %s", qt.LastKillReason)
		}
		if qt.WriteFailed {
			line += "  [WRITE FAILED - retrying]"
		} else if !qt.QuarantinedAt.IsZero() {
			line += fmt.Sprintf("  quarantined: %s", qt.QuarantinedAt.Format(time.RFC3339))
		}
		fmt.Println(line)
	}
}

// formatDaemonDuration formats a duration in a human-readable way for daemon status.
// <1s -> "<1s", <1m -> "Ns", <1h -> "Nm Ns", >=1h -> "Nh Nm".
func formatDaemonDuration(d time.Duration) string {
	if d <= 0 {
		return "<1s"
	}
	if d < time.Second {
		return "<1s"
	}

	totalSeconds := int(d.Seconds())
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func getUncommittedChangesCount(path string) int {
	result := cli.GetDeps(nil).Git.Run(path, "status", "--porcelain")
	if result.Err != nil {
		return 0
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return 0
	}
	return len(strings.Split(output, "\n"))
}
