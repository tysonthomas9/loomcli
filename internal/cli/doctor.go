package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// CheckStatus represents the outcome of a doctor check.
type CheckStatus int

const (
	StatusPass CheckStatus = iota
	StatusWarn
	StatusFail
)

func (s CheckStatus) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "unknown"
	}
}

func (s CheckStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// CheckResult holds the outcome of a single doctor check.
type CheckResult struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Summary string      `json:"summary"`
	Detail  string      `json:"detail,omitempty"`
}

// DoctorOutput is the top-level JSON output for loom doctor.
type DoctorOutput struct {
	Checks  []CheckResult `json:"checks"`
	Summary DoctorSummary `json:"summary"`
}

// DoctorSummary tallies check results.
type DoctorSummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	Short:   "Check loom installation and configuration health",
	GroupID: "workspace",
	Long: `Diagnose the health of your loom installation, configuration, and runtime.

Runs a series of checks covering prerequisite tools, daemon status,
configuration validity, worktree state, and connectivity. Reports
actionable pass/warn/fail results.

Examples:
  loom doctor              # Human-readable health report
  loom doctor --json       # Machine-readable JSON output`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
	// Override PersistentPreRunE: doctor must run even when the backend
	// binary is missing (that is one of the things it diagnoses).
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output in JSON format")
	rootCmd.AddCommand(doctorCmd)
}

// checkFunc is the signature for individual doctor checks.
type checkFunc func() CheckResult

func runDoctor(cmd *cobra.Command, args []string) error {
	checks := []checkFunc{checkGit, checkGitRepo, checkTmux, checkIssueBackend}
	if isFleetDBActive() {
		checks = append(checks, checkFleetDB)
	} else {
		checks = append(checks, checkBdCLI, checkBdDaemon, checkBdSocket, checkBeadsInit)
	}
	checks = append(checks, checkBackendCLI, checkProjectConfig, checkGlobalConfig,
		checkWorktrees, checkStaleLocks, checkLoomDaemon, checkRedis)

	var results []CheckResult
	for _, check := range checks {
		result := check()
		// Skip checks that returned empty (conditional checks that don't apply)
		if result.Name == "" {
			continue
		}
		results = append(results, result)
	}

	summary := DoctorSummary{}
	for _, r := range results {
		switch r.Status {
		case StatusPass:
			summary.Pass++
		case StatusWarn:
			summary.Warn++
		case StatusFail:
			summary.Fail++
		}
	}

	output := DoctorOutput{Checks: results, Summary: summary}

	if doctorJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(output); err != nil {
			return err
		}
	} else {
		renderDoctorHuman(output)
	}

	if summary.Fail > 0 {
		cmd.SilenceErrors = true
		return fmt.Errorf("doctor found %d failure(s)", summary.Fail)
	}
	return nil
}

func renderDoctorHuman(output DoctorOutput) {
	fmt.Println("Loom Doctor")
	fmt.Println("===========")
	fmt.Println()

	for _, r := range output.Checks {
		var icon string
		switch r.Status {
		case StatusPass:
			icon = "\u2713" // ✓
		case StatusWarn:
			icon = "\u26a0" // ⚠
		case StatusFail:
			icon = "\u2717" // ✗
		}
		fmt.Printf("%s %s\n", icon, r.Summary)
		if r.Detail != "" {
			for _, line := range strings.Split(r.Detail, "\n") {
				fmt.Printf("  \u2192 %s\n", line)
			}
		}
	}

	fmt.Printf("\n%d checks passed, %d warnings, %d failures\n",
		output.Summary.Pass, output.Summary.Warn, output.Summary.Fail)
}

// --- individual checks ---

// versionRegex extracts major.minor from version strings.
var versionRegex = regexp.MustCompile(`(\d+)\.(\d+)`)

func checkGit() CheckResult {
	result := execCommand(".", "git", "--version")
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

func checkGitRepo() CheckResult {
	result := execCommand(".", "git", "rev-parse", "--is-inside-work-tree")
	if result.Err != nil {
		return CheckResult{
			Name:    "git_repo",
			Status:  StatusWarn,
			Summary: "not inside a git repository",
			Detail:  "Run from inside a git repository for full functionality",
		}
	}

	// Check if inside a worktree (not the main working tree)
	mainResult := execCommand(".", "git", "rev-parse", "--git-common-dir")
	gitDirResult := execCommand(".", "git", "rev-parse", "--git-dir")
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

func checkTmux() CheckResult {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return CheckResult{
			Name:    "tmux",
			Status:  StatusWarn,
			Summary: "tmux not installed",
			Detail:  "Required for loom daemon and auto mode",
		}
	}

	result := execCommand(".", "tmux", "-V")
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

func checkBdCLI() CheckResult {
	_, err := exec.LookPath("bd")
	if err != nil {
		return CheckResult{
			Name:    "bd_cli",
			Status:  StatusFail,
			Summary: "bd CLI not found",
			Detail:  "Install with: make install-bd",
		}
	}

	result := execCommand(".", "bd", "--version")
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

func checkBdDaemon() CheckResult {
	if _, err := lookPath("bd"); err != nil {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusFail,
			Summary: "bd not found (cannot check daemon)",
			Detail:  "Install with: make install-bd",
		}
	}

	result := execCommand(GetBeadsDir(), "bd", "daemon", "status", "--json")
	if result.Err != nil {
		return CheckResult{
			Name:    "bd_daemon",
			Status:  StatusWarn,
			Summary: "bd daemon not running",
			Detail:  "Start with: bd daemon start",
		}
	}

	var status daemonStatus
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
	beadsDir := GetBeadsDir()
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
	name := ResolveBackendName()
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
	beadsDir := GetBeadsDir()
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
	beadsDir := GetBeadsDir()
	pf, err := LoadProjectFile(beadsDir)
	if err != nil {
		return CheckResult{
			Name:    "project_config",
			Status:  StatusFail,
			Summary: "loom.yaml has parse errors",
			Detail:  err.Error(),
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
	dc, err := LoadDaemonConfig(beadsDir)
	if err == nil && dc != nil {
		vr := ValidateProjectConfig(dc, beadsDir)
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
	cfg, err := LoadConfig()
	if err != nil {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusFail,
			Summary: "~/.loom/config.yaml has errors",
			Detail:  err.Error(),
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
	vr := ValidateGlobalConfig(cfg)
	if vr.HasErrors() {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusFail,
			Summary: "~/.loom/config.yaml has validation errors",
			Detail:  vr.FormatIssues(),
		}
	}
	if len(vr.Issues) > 0 {
		return CheckResult{
			Name:    "global_config",
			Status:  StatusWarn,
			Summary: "~/.loom/config.yaml valid (with warnings)",
			Detail:  vr.FormatIssues(),
		}
	}

	return CheckResult{
		Name:    "global_config",
		Status:  StatusPass,
		Summary: "~/.loom/config.yaml valid",
	}
}

func checkWorktrees() CheckResult {
	worktrees, err := DiscoverWorktrees()
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
	worktrees, err := DiscoverWorktrees()
	if err != nil || len(worktrees) == 0 {
		return CheckResult{} // skip — worktrees check already reported
	}

	var stale []string
	for _, wt := range worktrees {
		info, running, checkErr := CheckLock(wt.Path)
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

	config, err := LoadDaemonConfig(projectDir)
	if err != nil {
		config = &DaemonConfig{
			Daemon: DaemonSettings{
				PIDFile: ".loom/daemon.pid",
			},
		}
	}

	pidFilePath := resolveDaemonPath(projectDir, config.Daemon.PIDFile)
	pid, running := isLoomDaemonRunning(pidFilePath)

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

func checkRedis() CheckResult {
	addr := os.Getenv("LOOM_REDIS_ADDR")
	if addr == "" {
		// Redis not configured — skip silently
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	password := os.Getenv("LOOM_REDIS_PASSWORD")
	client := kv.NewClient(addr, password, 0)
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		return CheckResult{
			Name:    "redis",
			Status:  StatusFail,
			Summary: fmt.Sprintf("Redis not reachable at %s", addr),
			Detail:  err.Error(),
		}
	}

	return CheckResult{
		Name:    "redis",
		Status:  StatusPass,
		Summary: fmt.Sprintf("Redis reachable at %s", addr),
	}
}
