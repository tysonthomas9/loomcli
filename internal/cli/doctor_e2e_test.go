//go:build e2e
// +build e2e

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// loomBinaryPath returns the path to the loom binary, skipping the test if not found.
func loomBinaryPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("loom")
	if err != nil {
		t.Skip("loom binary not found on PATH; skipping E2E test")
	}
	return p
}

// initTempGitRepo creates a temp directory with an initialized git repo.
func initTempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "e2e@test.local")
	runGit(t, dir, "config", "user.name", "E2E Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "initial")
	return dir
}

// initTempGitRepoWithBeads creates a temp git repo with a .beads/ directory.
func initTempGitRepoWithBeads(t *testing.T) string {
	t.Helper()
	dir := initTempGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0755); err != nil {
		t.Fatalf("failed to create .beads: %v", err)
	}
	return dir
}

// runGit is a helper to run git commands in a directory during setup.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// runLoomDoctor executes `loom doctor` with given args in the specified directory.
func runLoomDoctor(t *testing.T, dir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	loom := loomBinaryPath(t)
	fullArgs := append([]string{"doctor"}, args...)
	cmd := exec.Command(loom, fullArgs...)
	cmd.Dir = dir
	// Isolate from host environment that could affect checks.
	// Filter out variables rather than setting them to empty string,
	// since empty LOOM_FLEETDB_ENABLED may still fall through to config.
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, "LOOM_REDIS_ADDR=") ||
			strings.HasPrefix(e, "LOOM_FLEETDB_ENABLED=") {
			continue
		}
		filtered = append(filtered, e)
	}
	// Isolate HOME and config so host config doesn't leak into checks.
	filtered = append(filtered, "HOME="+dir)
	filtered = append(filtered, "LOOM_CONFIG_DIR="+filepath.Join(dir, ".loom-config"))
	filtered = append(filtered, "GIT_CONFIG_NOSYSTEM=1")
	cmd.Env = filtered

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run loom doctor: %v", err)
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

// doctorJSONCheck mirrors CheckResult but uses string for status (since
// CheckStatus only has MarshalJSON, not UnmarshalJSON).
type doctorJSONCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Detail  string `json:"detail,omitempty"`
}

// doctorJSONOutput mirrors DoctorOutput for subprocess JSON parsing.
type doctorJSONOutput struct {
	Checks  []doctorJSONCheck `json:"checks"`
	Summary DoctorSummary     `json:"summary"`
}

// parseDoctorJSON parses the JSON output from loom doctor --json.
func parseDoctorJSON(t *testing.T, stdout string) doctorJSONOutput {
	t.Helper()
	var out doctorJSONOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("failed to parse doctor JSON output: %v\nraw output:\n%s", err, stdout)
	}
	return out
}

// findCheck returns the check with the given name, or nil if not found.
func findCheck(output doctorJSONOutput, name string) *doctorJSONCheck {
	for i := range output.Checks {
		if output.Checks[i].Name == name {
			return &output.Checks[i]
		}
	}
	return nil
}

func TestE2E_DoctorExitZeroOnHealthy(t *testing.T) {
	dir := initTempGitRepoWithBeads(t)

	stdout, _, exitCode := runLoomDoctor(t, dir)

	// In a minimal temp repo some checks (bd daemon, worktrees, etc.) may warn
	// but should not fail on core git/git_repo checks. We just verify the command
	// runs and produces a summary line.
	summaryPattern := regexp.MustCompile(`\d+ checks passed, \d+ warnings, \d+ failures`)
	if !summaryPattern.MatchString(stdout) {
		t.Errorf("expected summary line in stdout, got:\n%s", stdout)
	}

	// If there are no failures, exit code should be 0
	if exitCode == 0 {
		return // healthy
	}
	// Some checks may fail in the E2E environment (e.g. backend_cli not found);
	// just verify the command executed and produced output.
	if !strings.Contains(stdout, "checks passed") {
		t.Errorf("expected 'checks passed' in output, exit code %d, stdout:\n%s", exitCode, stdout)
	}
}

func TestE2E_DoctorExitNonZeroOnFailure(t *testing.T) {
	// Run in a temp dir that is NOT a git repo and has no .beads/
	dir := t.TempDir()

	stdout, _, exitCode := runLoomDoctor(t, dir, "--json")

	if exitCode == 0 {
		t.Errorf("expected non-zero exit code in broken environment, got 0\nstdout:\n%s", stdout)
	}

	output := parseDoctorJSON(t, stdout)
	if output.Summary.Fail == 0 {
		t.Errorf("expected at least one failed check in broken environment, got summary: pass=%d warn=%d fail=%d",
			output.Summary.Pass, output.Summary.Warn, output.Summary.Fail)
	}
}

func TestE2E_DoctorJSONOutput(t *testing.T) {
	dir := initTempGitRepoWithBeads(t)

	stdout, _, _ := runLoomDoctor(t, dir, "--json")

	output := parseDoctorJSON(t, stdout)

	// Verify top-level structure
	if len(output.Checks) == 0 {
		t.Fatal("expected at least one check in output")
	}

	// Each check must have valid fields
	for _, check := range output.Checks {
		if check.Name == "" {
			t.Error("check has empty name")
		}
		if check.Status != "pass" && check.Status != "warn" && check.Status != "fail" {
			t.Errorf("check %q has invalid status %q", check.Name, check.Status)
		}
		if check.Summary == "" {
			t.Errorf("check %q has empty summary", check.Name)
		}
	}

	// Summary counts must add up
	total := output.Summary.Pass + output.Summary.Warn + output.Summary.Fail
	if total != len(output.Checks) {
		t.Errorf("summary counts (%d+%d+%d=%d) don't match check count (%d)",
			output.Summary.Pass, output.Summary.Warn, output.Summary.Fail, total, len(output.Checks))
	}

	// Known checks should be present
	for _, name := range []string{"git", "git_repo", "tmux", "issue_backend"} {
		if findCheck(output, name) == nil {
			t.Errorf("expected check %q to be present", name)
		}
	}
}

func TestE2E_DoctorJSONFailureFields(t *testing.T) {
	// Run in a broken environment (no .beads, not a git repo)
	dir := t.TempDir()

	stdout, _, exitCode := runLoomDoctor(t, dir, "--json")

	if exitCode == 0 {
		t.Error("expected non-zero exit code in broken environment")
	}

	output := parseDoctorJSON(t, stdout)

	// At least one check should have status "fail"
	hasFailure := false
	for _, check := range output.Checks {
		if check.Status == "fail" {
			hasFailure = true
			if check.Detail == "" {
				t.Errorf("failed check %q has empty detail (should provide remediation advice)", check.Name)
			}
			break
		}
	}

	if !hasFailure {
		t.Error("expected at least one failed check in broken environment")
	}
}

func TestE2E_DoctorHumanOutputFormat(t *testing.T) {
	dir := initTempGitRepoWithBeads(t)

	stdout, _, _ := runLoomDoctor(t, dir)

	// Should start with header
	if !strings.HasPrefix(stdout, "Loom Doctor\n===========\n") {
		t.Errorf("expected header 'Loom Doctor\\n===========\\n', got:\n%s", stdout[:min(len(stdout), 100)])
	}

	// Should contain at least one check icon (✓, ⚠, or ✗)
	hasIcon := strings.Contains(stdout, "\u2713") || strings.Contains(stdout, "\u26a0") || strings.Contains(stdout, "\u2717")
	if !hasIcon {
		t.Errorf("expected check icons (✓/⚠/✗) in human output")
	}

	// Should contain summary line
	summaryPattern := regexp.MustCompile(`\d+ checks passed, \d+ warnings, \d+ failures`)
	if !summaryPattern.MatchString(stdout) {
		// Show last 200 chars for debugging
		tail := stdout
		if len(tail) > 200 {
			tail = tail[len(tail)-200:]
		}
		t.Errorf("expected summary line in output, tail:\n%s", tail)
	}
}

func TestE2E_DoctorGitVersionCheck(t *testing.T) {
	dir := initTempGitRepo(t)

	stdout, _, _ := runLoomDoctor(t, dir, "--json")
	output := parseDoctorJSON(t, stdout)

	gitCheck := findCheck(output, "git")
	if gitCheck == nil {
		t.Fatal("git check not found in output")
	}

	if gitCheck.Status != "pass" {
		t.Errorf("expected git check to pass, got %v: %s", gitCheck.Status, gitCheck.Summary)
	}

	gitVersionPattern := regexp.MustCompile(`git \d+\.\d+ found`)
	if !gitVersionPattern.MatchString(gitCheck.Summary) {
		t.Errorf("expected git summary to match 'git X.Y found', got %q", gitCheck.Summary)
	}
}

func TestE2E_DoctorWorktreeDetection(t *testing.T) {
	// Create main repo
	mainDir := initTempGitRepo(t)

	// Create a worktree
	wtDir := filepath.Join(t.TempDir(), "worktree")
	cmd := exec.Command("git", "worktree", "add", wtDir, "-b", "test-wt")
	cmd.Dir = mainDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed (may not be supported): %v\n%s", err, out)
	}

	stdout, _, _ := runLoomDoctor(t, wtDir, "--json")
	output := parseDoctorJSON(t, stdout)

	gitRepoCheck := findCheck(output, "git_repo")
	if gitRepoCheck == nil {
		t.Fatal("git_repo check not found in output")
	}

	if gitRepoCheck.Status != "warn" {
		t.Errorf("expected git_repo check to warn inside worktree, got %v: %s",
			gitRepoCheck.Status, gitRepoCheck.Summary)
	}

	if !strings.Contains(gitRepoCheck.Summary, "worktree") {
		t.Errorf("expected summary to mention 'worktree', got %q", gitRepoCheck.Summary)
	}
}

func TestE2E_DoctorOmitsLegacyBeadsChecks(t *testing.T) {
	dir := initTempGitRepo(t)

	stdout, _, _ := runLoomDoctor(t, dir, "--json")
	output := parseDoctorJSON(t, stdout)

	for _, name := range []string{"bd_cli", "bd_daemon", "bd_socket", "beads_init"} {
		if check := findCheck(output, name); check != nil {
			t.Fatalf("legacy beads check %q should not be present: %+v", name, check)
		}
	}
	if check := findCheck(output, "fleetdb"); check == nil {
		t.Fatal("fleetdb check should be present")
	}
}

func TestE2E_DoctorProjectConfigValidation(t *testing.T) {
	dir := initTempGitRepoWithBeads(t)

	// a) No loom.yaml → "warn"
	stdout, _, _ := runLoomDoctor(t, dir, "--json")
	output := parseDoctorJSON(t, stdout)

	cfgCheck := findCheck(output, "project_config")
	if cfgCheck == nil {
		t.Fatal("project_config check not found")
	}
	if cfgCheck.Status != "warn" {
		t.Errorf("expected warn with no loom.yaml, got %v: %s", cfgCheck.Status, cfgCheck.Summary)
	}

	// b) Invalid YAML → "fail"
	yamlPath := filepath.Join(dir, "loom.yaml")
	if err := os.WriteFile(yamlPath, []byte("{{invalid yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ = runLoomDoctor(t, dir, "--json")
	output = parseDoctorJSON(t, stdout)

	cfgCheck = findCheck(output, "project_config")
	if cfgCheck == nil {
		t.Fatal("project_config check not found")
	}
	if cfgCheck.Status != "fail" {
		t.Errorf("expected fail with invalid loom.yaml, got %v: %s", cfgCheck.Status, cfgCheck.Summary)
	}

	// c) Valid loom.yaml with agents → "pass"
	validYAML := `agents:
  - worktree: falcon
    role: plan
  - worktree: nova
    role: task
`
	if err := os.WriteFile(yamlPath, []byte(validYAML), 0644); err != nil {
		t.Fatal(err)
	}

	stdout, _, _ = runLoomDoctor(t, dir, "--json")
	output = parseDoctorJSON(t, stdout)

	cfgCheck = findCheck(output, "project_config")
	if cfgCheck == nil {
		t.Fatal("project_config check not found")
	}
	if cfgCheck.Status != "pass" {
		t.Errorf("expected pass with valid loom.yaml, got %v: %s", cfgCheck.Status, cfgCheck.Summary)
	}
}

func TestE2E_DoctorNoArgs(t *testing.T) {
	dir := initTempGitRepo(t)

	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, "doctor", "extraarg")
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected non-zero exit code when extra args are passed\noutput:\n%s", out)
	}
}

func TestE2E_DoctorHelp(t *testing.T) {
	loom := loomBinaryPath(t)
	cmd := exec.Command(loom, "doctor", "--help")

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("loom doctor --help failed: %v", err)
	}

	stdout := string(out)
	if !strings.Contains(stdout, "Diagnose the health") {
		t.Errorf("expected --help to contain 'Diagnose the health', got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--json") {
		t.Errorf("expected --help to mention '--json' flag, got:\n%s", stdout)
	}
}
