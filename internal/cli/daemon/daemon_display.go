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

// printAgentDiagnostics prints the post-run signals (last exit code, error
// class, retry counters, stop reason) for an agent. Split out of
// printAgentStatus to keep that function under the funlen threshold.
func printAgentDiagnostics(agent DaemonAgentStatus) {
	if agent.PID == 0 && !agent.LastStart.IsZero() && !agent.LastExit.IsZero() {
		runtime := agent.LastExit.Sub(agent.LastStart)
		fmt.Printf("      Last run: %s (exit %d)\n", formatDaemonDuration(runtime), agent.LastExitCode)
	}
	printAgentProfileError(agent)
	if agent.LastErrorClass != "" {
		fmt.Printf("      Last error: %s\n", agent.LastErrorClass)
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

// printAgentProfileError renders the harness-profile refusal that is keeping
// an agent out of the claim loop, above the transient diagnostics: those churn
// every poll cycle, this does not change until an operator repairs the
// profile. Nothing is printed when the profile verifies, so a healthy fleet's
// status output is unchanged.
func printAgentProfileError(agent DaemonAgentStatus) {
	if agent.ProfileError == "" {
		return
	}
	lines := strings.Split(agent.ProfileError, "\n")
	fmt.Printf("      Profile: INVALID — %s\n", lines[0])
	for _, line := range lines[1:] {
		fmt.Printf("                %s\n", line)
	}
}

// printProfileBlockedBanner states the fleet-level fact once. One `claude`
// auto-update drifts every profiled agent at the same instant, so the count is
// what an operator can act on at a glance; the same per-agent line repeated
// four times is not.
func printProfileBlockedBanner(agents []DaemonAgentStatus) {
	var blocked []string
	for _, a := range agents {
		if a.ProfileError != "" {
			blocked = append(blocked, a.Worktree)
		}
	}
	if len(blocked) == 0 {
		return
	}
	noun := "agents"
	if len(blocked) == 1 {
		noun = "agent"
	}
	fmt.Println("")
	fmt.Printf("⚠ %d %s blocked on profile verification: %s\n",
		len(blocked), noun, strings.Join(blocked, ", "))
	fmt.Println("  No task is claimed while a profile fails to verify. Re-provision the")
	fmt.Println("  profile directories named above, then the agents resume on their own.")
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
