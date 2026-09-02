package daemon

import (
	"fmt"
	"os"
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

// stateStalenessThreshold is how old daemon-agents.json may be before the
// status listing below it stops being trustworthy. The state updater ticks
// every 5s, so 30s is six missed writes — well past a slow tick and well short
// of the two hours the 2026-08-31 outage went unnoticed.
const stateStalenessThreshold = 30 * time.Second

// stateFileAge reports how long ago the state file was written, and whether an
// age could be determined at all.
//
// WrittenAt is authoritative when present. It is absent from files written by
// an older binary, and treating a zero time as the write time would report a
// staleness measured in decades — so that case falls back to the file's mtime,
// which is the pre-existing (weaker, `cp`-forgeable) signal.
func stateFileAge(state *DaemonState, stateFilePath string) (time.Duration, bool) {
	if state != nil && !state.WrittenAt.IsZero() {
		return time.Since(state.WrittenAt), true
	}
	fi, err := os.Stat(stateFilePath)
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// printStateFreshness prints the staleness banner and any active degradations
// BEFORE the agent table, so an operator reading top-down learns the data is
// suspect before they read the data.
func printStateFreshness(state *DaemonState, stateFilePath string) {
	if age, ok := stateFileAge(state, stateFilePath); ok && age > stateStalenessThreshold {
		fmt.Printf("⚠  STALE: daemon-agents.json last written %s ago — the agent list below may not reflect reality.\n", age.Truncate(time.Second))
	}
	if state == nil {
		return
	}
	for _, d := range state.Degradations {
		fmt.Printf("⚠  DEGRADED: %s since %s (%d failures): %s\n", d.Kind, d.Since.Format(time.RFC3339), d.Count, d.LastErr)
	}
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

// printProfileDrifts prints the profile-drift section of daemon status: agent
// profiles running against a harness version their manifest does not pin.
// These agents ARE running — drift within a major is a warning, not a refusal
// (see supervisor.checkProfileManifest) — so the line says what is unverified,
// not what is broken.
func printProfileDrifts(drifts []supervisor.ProfileDrift) {
	if len(drifts) == 0 {
		return
	}
	fmt.Println("")
	fmt.Println("Profile drift (running unverified):")
	for _, d := range drifts {
		fmt.Printf("  %s  manifest %s, %s %s  (%d spawn(s))\n",
			d.Dir, d.Manifest, d.Binary, d.Observed, d.Count)
	}
	fmt.Println("  re-bless with: loom doctor --fix   (once the version has actually been verified)")
}

// printWalls prints the credential walls currently parking agents. The scope
// and the credential are the point of the line: "account" means the whole
// subscription and every agent is held, "profile" names ONE credential set and
// only its agents are held. An operator reading a fleet that has stopped needs
// to tell those apart before anything else.
func printWalls(walls []supervisor.WallInfo) {
	if len(walls) == 0 {
		return
	}
	fmt.Println("")
	fmt.Println("Credential walls (agents parked):")
	for _, w := range walls {
		line := fmt.Sprintf("  %s (%s)  %s  %s remaining",
			w.Scope, w.Credential, w.Class, formatDaemonDuration(time.Until(w.Until)))
		if w.Message != "" {
			line += "  — " + firstLine(w.Message)
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

// ---------------------------------------------------------------------------
// Daemon status view: what `loom daemon status` is willing to assert
// ---------------------------------------------------------------------------
// statusStateStaleThreshold is how old daemon-agents.json may be, relative to
// now, before its contents stop counting as a description of the running
// daemon. The state updater rewrites the file every 5s, so 30s leaves ample
// headroom. Same number and same rationale as the doctor package's
// stateFileStaleThreshold — deliberately not a second opinion.
const statusStateStaleThreshold = 30 * time.Second

// agentCountUnknown marks an agent count that could not be established. It is
// distinct from a genuine, trusted zero, which is what `Agents: 0` claims.
const agentCountUnknown = -1

// daemonStatusInputs is everything `loom daemon status` gathered about one
// daemon target before deciding what it is willing to assert. Collected by the
// caller (which does the I/O) so the decision itself stays pure and testable.
type daemonStatusInputs struct {
	// RT is the detected daemon: the single source of truth for which daemon
	// is being described. Every path below was derived from RT.Dir.
	RT cli.DaemonRuntimeInfo
	// State is the parsed daemon-agents.json, or nil when it is missing or
	// unparseable.
	State *DaemonState
	// StatePath is the file State was read from, named in warnings so the
	// reader can go look at what was distrusted.
	StatePath string
	// StateMTime is StatePath's modification time; zero when unavailable, in
	// which case freshness is not evaluated.
	StateMTime time.Time
	// LiveCount is the daemon's own answer over the control socket, or
	// agentCountUnknown when the socket did not answer.
	LiveCount int
	// Now anchors the freshness comparison. The repo has no fake clock, so it
	// is passed in explicitly (as evaluateDaemonStuck does).
	Now time.Time
}

// daemonStatusView is the header block of `loom daemon status`, resolved from
// the detected daemon and whatever sidecar evidence proved trustworthy.
//
// The invariant this type exists to enforce: a number is printed only when it
// carries the identity of the daemon that was actually detected. Everything
// else renders as "unknown", with a warning naming what was distrusted.
type daemonStatusView struct {
	PID        int
	Source     string
	Dir        string
	StartedAt  time.Time // zero => unknown
	AgentCount int       // agentCountUnknown => unknown
	Trusted    bool      // state file matched the detected daemon and is fresh
	Warnings   []string
}

// buildDaemonStatusView decides what the status header may assert.
//
// The state file is trusted only when it carries the detected daemon's PID and
// is being actively maintained. Untrusted metadata never degrades to a
// plausible-looking zero: the agent count falls back to the live socket, and
// then to "unknown".
func buildDaemonStatusView(in daemonStatusInputs) daemonStatusView {
	v := daemonStatusView{
		PID:        in.RT.PID,
		Source:     in.RT.Source,
		Dir:        in.RT.Dir,
		StartedAt:  in.RT.StartedAt,
		AgentCount: agentCountUnknown,
	}

	v.Trusted, v.Warnings = stateFileTrust(in)

	if v.Trusted {
		// The state file describes this daemon, so its own start time is the
		// most precise one available; fall back to the detection evidence when
		// the record predates the field.
		if !in.State.StartedAt.IsZero() {
			v.StartedAt = in.State.StartedAt
		}
		v.AgentCount = len(in.State.Agents)
		return v
	}

	// Untrusted: prefer the daemon's live answer, else admit we do not know.
	if in.LiveCount >= 0 {
		v.AgentCount = in.LiveCount
	}
	return v
}

// stateFileTrust reports whether the state file may be believed, along with
// the warnings explaining any refusal. Trust requires three things: the file
// exists, it names the PID we detected, and it is still being written.
func stateFileTrust(in daemonStatusInputs) (bool, []string) {
	if in.State == nil {
		// Absence is not suspicious on its own — the daemon may have just
		// started, or the file may live elsewhere. Nothing to warn about.
		return false, nil
	}

	if in.RT.PID <= 0 {
		// Liveness was proved without an identity (lock held, contents
		// unreadable), so there is nothing to match the file against.
		return false, []string{fmt.Sprintf(
			"daemon PID is unknown, so the state file at %s cannot be verified as belonging to it",
			in.StatePath)}
	}

	if in.State.PID != in.RT.PID {
		return false, []string{fmt.Sprintf(
			"state file at %s belongs to PID %d, daemon is PID %d (ignoring its agent list)",
			in.StatePath, in.State.PID, in.RT.PID)}
	}

	if !in.StateMTime.IsZero() && in.Now.Sub(in.StateMTime) > statusStateStaleThreshold {
		return false, []string{fmt.Sprintf(
			"state file at %s was last written %s and is no longer being maintained (ignoring its agent list)",
			in.StatePath, in.StateMTime.Format(time.RFC3339))}
	}

	return true, nil
}

// HeaderLines renders the header block, one string per line, in print order.
// It never formats a zero time or reports an unknown count as zero.
func (v daemonStatusView) HeaderLines() []string {
	lines := []string{fmt.Sprintf("Daemon: running (PID %d)", v.PID)}

	// The workspace lock is the case where the daemon being described is not
	// the one belonging to the caller's directory. Say so, and say where.
	if v.Source == "workspace-lock" {
		lines = append(lines, fmt.Sprintf("Source: %s (%s)", v.Source, v.Dir))
	}

	if v.StartedAt.IsZero() {
		lines = append(lines, "Started: unknown")
	} else {
		lines = append(lines, fmt.Sprintf("Started: %s", v.StartedAt.Format(time.RFC3339)))
	}

	if v.AgentCount == agentCountUnknown {
		lines = append(lines, "Agents: unknown")
	} else {
		lines = append(lines, fmt.Sprintf("Agents: %d", v.AgentCount))
	}

	for _, w := range v.Warnings {
		lines = append(lines, "  warning: "+w)
	}

	return lines
}
