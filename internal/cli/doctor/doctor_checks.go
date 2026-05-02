package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// --- individual checks ---

// versionRegex extracts major.minor from version strings.
var versionRegex = regexp.MustCompile(`(\d+)\.(\d+)`)

func checkGit(deps *cli.Deps) CheckResult {
	result := deps.Exec.Run(".", "git", "--version")
	if result.Err != nil {
		return CheckResult{
			Name:    "git",
			Status:  StatusFail,
			Summary: "git not found",
			Detail:  "Install from https://git-scm.com",
		}
	}

	versionStr := strings.TrimSpace(result.Stdout)
	matches := versionRegex.FindStringSubmatch(versionStr)
	if len(matches) < 3 {
		return CheckResult{
			Name:    "git",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("git found but version unparseable: %s", versionStr),
		}
	}

	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	if major < 2 || (major == 2 && minor < 20) {
		return CheckResult{
			Name:    "git",
			Status:  StatusFail,
			Summary: fmt.Sprintf("git %d.%d found (requires >= 2.20 for worktree support)", major, minor),
			Detail:  "Upgrade git from https://git-scm.com",
		}
	}

	return CheckResult{
		Name:    "git",
		Status:  StatusPass,
		Summary: fmt.Sprintf("git %d.%d found", major, minor),
	}
}

func checkGitRepo(deps *cli.Deps) CheckResult {
	result := deps.Exec.Run(".", "git", "rev-parse", "--is-inside-work-tree")
	if result.Err != nil {
		return CheckResult{
			Name:    "git_repo",
			Status:  StatusWarn,
			Summary: "not inside a git repository",
			Detail:  "Run from inside a git repository for full functionality",
		}
	}

	// Check if inside a worktree (not the main working tree)
	mainResult := deps.Exec.Run(".", "git", "rev-parse", "--git-common-dir")
	gitDirResult := deps.Exec.Run(".", "git", "rev-parse", "--git-dir")
	if mainResult.Err == nil && gitDirResult.Err == nil {
		commonDir := strings.TrimSpace(mainResult.Stdout)
		gitDir := strings.TrimSpace(gitDirResult.Stdout)
		if commonDir != gitDir && commonDir != "" {
			return CheckResult{
				Name:    "git_repo",
				Status:  StatusWarn,
				Summary: "inside a git worktree (not the main working tree)",
				Detail:  "Consider running from the main repository",
			}
		}
	}

	return CheckResult{
		Name:    "git_repo",
		Status:  StatusPass,
		Summary: "inside git repository",
	}
}

func checkTmux(deps *cli.Deps) CheckResult {
	_, err := deps.LookPath("tmux")
	if err != nil {
		return CheckResult{
			Name:    "tmux",
			Status:  StatusWarn,
			Summary: "tmux not installed",
			Detail:  "Required for loom daemon and auto mode",
		}
	}

	result := deps.Exec.Run(".", "tmux", "-V")
	versionStr := strings.TrimSpace(result.Stdout)
	matches := versionRegex.FindStringSubmatch(versionStr)
	if len(matches) >= 3 {
		return CheckResult{
			Name:    "tmux",
			Status:  StatusPass,
			Summary: fmt.Sprintf("tmux %s.%s found", matches[1], matches[2]),
		}
	}

	return CheckResult{
		Name:    "tmux",
		Status:  StatusPass,
		Summary: "tmux found",
	}
}

func checkBdCLI(deps *cli.Deps) CheckResult {
	_, err := deps.LookPath("bd")
	if err != nil {
		return CheckResult{
			Name:    "bd_cli",
			Status:  StatusFail,
			Summary: "bd CLI not found",
			Detail:  "Install bd separately if using legacy task storage",
		}
	}

	result := deps.Exec.Run(".", "bd", "--version")
	versionStr := strings.TrimSpace(result.Stdout)
	if versionStr != "" {
		return CheckResult{
			Name:    "bd_cli",
			Status:  StatusPass,
			Summary: fmt.Sprintf("bd %s found", versionStr),
		}
	}

	return CheckResult{
		Name:    "bd_cli",
		Status:  StatusPass,
		Summary: "bd CLI found",
	}
}

func checkBdDaemon(deps *cli.Deps) CheckResult {
	if _, err := deps.LookPath("bd"); err != nil {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusFail,
			Summary: "bd not found (cannot check daemon)",
			Detail:  "Install bd separately if using legacy task storage",
		}
	}

	result := deps.Exec.Run(cli.GetBeadsDir(), "bd", "daemon", "status", "--json")
	if result.Err != nil {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusWarn,
			Summary: "bd daemon not running",
			Detail:  "Start with: bd daemon start",
		}
	}

	var status cli.DaemonStatus
	if err := json.Unmarshal([]byte(result.Stdout), &status); err != nil {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusWarn,
			Summary: "bd daemon status unparseable",
		}
	}

	if status.Status != "running" {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("bd daemon status: %s", status.Status),
			Detail:  "Start with: bd daemon start",
		}
	}

	return CheckResult{
		Name:    "bd_daemon",
		Status:  StatusPass,
		Summary: fmt.Sprintf("bd daemon running (PID %d)", status.PID),
	}
}

func checkBdSocket() CheckResult {
	beadsDir := cli.GetBeadsDir()
	socketPath := rpc.ShortSocketPath(beadsDir)

	client, err := rpc.TryConnect(socketPath)
	if err != nil || client == nil {
		return CheckResult{
			Name:    "bd_socket",
			Status:  StatusWarn,
			Summary: "bd daemon socket not reachable",
			Detail:  fmt.Sprintf("Expected at: %s", socketPath),
		}
	}
	_ = client.Close()

	return CheckResult{
		Name:    "bd_socket",
		Status:  StatusPass,
		Summary: "bd daemon socket reachable",
	}
}

func checkBackendCLI() CheckResult {
	name := cli.ResolveBackendName()
	_, err := exec.LookPath(name)
	if err != nil {
		return CheckResult{
			Name:    "backend_cli",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%s CLI not found (active backend)", name),
			Detail:  fmt.Sprintf("Install the %s CLI and ensure it is on your PATH", name),
		}
	}

	return CheckResult{
		Name:    "backend_cli",
		Status:  StatusPass,
		Summary: fmt.Sprintf("%s CLI found (active backend)", name),
	}
}

func checkBeadsInit() CheckResult {
	beadsDir := cli.GetBeadsDir()
	beadsPath := filepath.Join(beadsDir, ".beads")

	if _, err := os.Stat(beadsPath); err != nil {
		return CheckResult{
			Name:    "beads_init",
			Status:  StatusFail,
			Summary: ".beads/ not found",
			Detail:  "Run: bd init",
		}
	}

	return CheckResult{
		Name:    "beads_init",
		Status:  StatusPass,
		Summary: ".beads/ initialized",
	}
}

func checkProjectConfig() CheckResult {
	beadsDir := cli.GetBeadsDir()
	pf, err := cfgpkg.LoadProjectFile(beadsDir)
	if err != nil {
		return CheckResult{
			Name:    "project_config",
			Status:  StatusFail,
			Summary: "loom.yaml has parse errors",
			Detail:  cfgpkg.FormatYAMLDiagnostic(filepath.Join(beadsDir, "loom.yaml"), err),
		}
	}

	if pf == nil {
		return CheckResult{
			Name:    "project_config",
			Status:  StatusWarn,
			Summary: "no loom.yaml found (optional)",
			Detail:  "Needed for daemon mode agent configuration",
		}
	}

	agentCount := len(pf.Agents)
	summary := fmt.Sprintf("loom.yaml valid (%d agents configured)", agentCount)

	// Run deeper validation if available
	dc, err := cfgpkg.LoadDaemonConfig(beadsDir)
	if err == nil && dc != nil {
		vr := cli.ValidateProjectConfig(dc, beadsDir)
		if vr.HasErrors() {
			return CheckResult{
				Name:    "project_config",
				Status:  StatusFail,
				Summary: "loom.yaml has validation errors",
				Detail:  vr.FormatIssues(),
			}
		}
		if len(vr.Issues) > 0 {
			return CheckResult{
				Name:    "project_config",
				Status:  StatusWarn,
				Summary: summary + " (with warnings)",
				Detail:  vr.FormatIssues(),
			}
		}
	}

	return CheckResult{
		Name:    "project_config",
		Status:  StatusPass,
		Summary: summary,
	}
}

func checkGlobalConfig() CheckResult {
	cfg, err := cfgpkg.LoadConfig()
	if err != nil {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusFail,
			Summary: "~/.loom/cfgpkg.yaml has errors",
			Detail:  cfgpkg.FormatYAMLDiagnostic(cfgpkg.GetConfigPath(), err),
		}
	}

	if cfg == nil {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusWarn,
			Summary: "no global config found (optional)",
			Detail:  "Create with: loom config init",
		}
	}

	// Run validation
	vr := cli.ValidateGlobalConfig(cfg)
	if vr.HasErrors() {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusFail,
			Summary: "~/.loom/cfgpkg.yaml has validation errors",
			Detail:  vr.FormatIssues(),
		}
	}
	if len(vr.Issues) > 0 {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusWarn,
			Summary: "~/.loom/cfgpkg.yaml valid (with warnings)",
			Detail:  vr.FormatIssues(),
		}
	}

	return CheckResult{
		Name:    "global_config",
		Status:  StatusPass,
		Summary: "~/.loom/cfgpkg.yaml valid",
	}
}

func checkWorktrees() CheckResult {
	worktrees, err := cli.DiscoverWorktrees()
	if err != nil {
		return CheckResult{
			Name:    "worktrees",
			Status:  StatusWarn,
			Summary: "could not discover worktrees",
			Detail:  err.Error(),
		}
	}

	if len(worktrees) == 0 {
		return CheckResult{
			Name:    "worktrees",
			Status:  StatusWarn,
			Summary: "no worktrees found",
			Detail:  "Run: loom init",
		}
	}

	names := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		names = append(names, wt.Name)
	}

	return CheckResult{
		Name:    "worktrees",
		Status:  StatusPass,
		Summary: fmt.Sprintf("%d worktrees found (%s)", len(worktrees), strings.Join(names, ", ")),
	}
}

func checkStaleLocks() CheckResult {
	worktrees, err := cli.DiscoverWorktrees()
	if err != nil || len(worktrees) == 0 {
		return CheckResult{} // skip -- worktrees check already reported
	}

	var stale []string
	for _, wt := range worktrees {
		info, running, checkErr := cli.CheckLock(wt.Path)
		if checkErr != nil || info == nil {
			continue
		}
		if !running {
			stale = append(stale, fmt.Sprintf("%s (PID %d dead)", wt.Name, info.PID))
		}
	}

	if len(stale) > 0 {
		return CheckResult{
			Name:    "stale_locks",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("%d stale lock(s) found", len(stale)),
			Detail:  fmt.Sprintf("Stale: %s\nRun: loom recover <name>", strings.Join(stale, ", ")),
		}
	}

	return CheckResult{
		Name:    "stale_locks",
		Status:  StatusPass,
		Summary: "no stale locks",
	}
}

func checkLoomDaemon() CheckResult {
	projectDir, err := os.Getwd()
	if err != nil {
		return CheckResult{
			Name:    "loom_daemon",
			Status:  StatusWarn,
			Summary: "could not determine working directory",
		}
	}

	dcfg, err := cfgpkg.LoadDaemonConfig(projectDir)
	if err != nil {
		dcfg = &cfgpkg.DaemonConfig{
			Daemon: cfgpkg.DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}

	pidFilePath := daemon.ResolveDaemonPath(projectDir, dcfg.Daemon.PIDFile)
	pid, running := daemon.IsLoomDaemonRunning(pidFilePath)

	if running {
		return CheckResult{
			Name:    "loom_daemon",
			Status:  StatusPass,
			Summary: fmt.Sprintf("loom daemon running (PID %d)", pid),
		}
	}

	// Check for stale PID file
	if _, statErr := os.Stat(pidFilePath); statErr == nil {
		return CheckResult{
			Name:    "loom_daemon",
			Status:  StatusWarn,
			Summary: "loom daemon not running (stale PID file)",
			Detail:  "Start with: loom daemon",
		}
	}

	return CheckResult{
		Name:    "loom_daemon",
		Status:  StatusWarn,
		Summary: "loom daemon not running",
		Detail:  "Start with: loom daemon (optional)",
	}
}
