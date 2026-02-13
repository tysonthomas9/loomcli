package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// repoRoot returns the directory containing the Makefile under test.
// It defaults to the working directory but respects MAKEFILE_TEST_DIR if set,
// so the test can be invoked from anywhere.
func repoRoot(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("MAKEFILE_TEST_DIR"); d != "" {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	return wd
}

// runMake is a small helper that runs make with the given args in the repo root
// and returns combined stdout+stderr. It fails the test on non-zero exit.
func runMake(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestMakefilePhonyDeclarations verifies that both sync-beads and update-beads
// are declared as .PHONY targets by inspecting the Makefile source directly.
func TestMakefilePhonyDeclarations(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	for _, target := range []string{"sync-beads", "update-beads"} {
		found := false
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, ".PHONY") && strings.Contains(line, target) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(".PHONY declaration missing target %q", target)
		}
	}
}

// TestMakefileBeadsVariables verifies that the Makefile defines the expected
// BEADS_REMOTE, BEADS_BRANCH, and BEADS_PREFIX variables.
func TestMakefileBeadsVariables(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	expected := map[string]string{
		"BEADS_REMOTE": "https://github.com/tysonthomas9/beads",
		"BEADS_BRANCH": "feature/web-ui",
		"BEADS_PREFIX": "third_party/beads",
	}

	for varName, wantValue := range expected {
		found := false
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, varName) && strings.Contains(trimmed, ":=") {
				if strings.Contains(trimmed, wantValue) {
					found = true
				} else {
					t.Errorf("variable %s has unexpected value in line: %s (want %q)", varName, trimmed, wantValue)
				}
				break
			}
		}
		if !found {
			t.Errorf("variable %s := %s not found in Makefile", varName, wantValue)
		}
	}
}

// TestMakeHelp_IncludesBeadsTargets verifies that `make help` output
// mentions both sync-beads and update-beads.
func TestMakeHelp_IncludesBeadsTargets(t *testing.T) {
	t.Parallel()

	out := runMake(t, "help")

	for _, target := range []string{"sync-beads", "update-beads"} {
		if !strings.Contains(out, target) {
			t.Errorf("make help output missing %q\nOutput:\n%s", target, out)
		}
	}
}

// TestMakeDryRun_SyncBeads verifies that `make -n sync-beads` invokes
// the sync-beads.sh script.
func TestMakeDryRun_SyncBeads(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "sync-beads")

	if !strings.Contains(out, "./scripts/sync-beads.sh") {
		t.Errorf("make -n sync-beads should reference ./scripts/sync-beads.sh\nOutput:\n%s", out)
	}
}

// TestMakeDryRun_UpdateBeads verifies that `make -n update-beads` runs
// git subtree pull with the correct remote/branch/prefix and then invokes
// make sync-beads recursively.
func TestMakeDryRun_UpdateBeads(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "update-beads")

	checks := []struct {
		name   string
		substr string
	}{
		{"git subtree pull", "git subtree pull"},
		{"prefix flag", "--prefix=third_party/beads"},
		{"remote URL", "https://github.com/tysonthomas9/beads"},
		{"branch", "feature/web-ui"},
		{"squash flag", "--squash"},
		{"recursive make sync-beads", "sync-beads"},
	}

	for _, c := range checks {
		if !strings.Contains(out, c.substr) {
			t.Errorf("make -n update-beads: expected %s (%q) in output\nOutput:\n%s", c.name, c.substr, out)
		}
	}
}

// ---------------------------------------------------------------------------
// Dev workflow tests
// ---------------------------------------------------------------------------

// TestMakefileDevPhonyDeclarations verifies that dev and dev-check are
// declared as .PHONY targets.
func TestMakefileDevPhonyDeclarations(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}
	content := string(data)

	for _, target := range []string{"dev", "dev-check"} {
		found := false
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, ".PHONY") && strings.Contains(line, target) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf(".PHONY declaration missing target %q", target)
		}
	}
}

// TestMakeHelp_IncludesDevTargets verifies that `make help` output
// mentions both dev and dev-check targets.
func TestMakeHelp_IncludesDevTargets(t *testing.T) {
	t.Parallel()

	out := runMake(t, "help")

	for _, target := range []string{"dev", "dev-check"} {
		if !strings.Contains(out, target) {
			t.Errorf("make help output missing %q\nOutput:\n%s", target, out)
		}
	}
}

// TestMakeDryRun_Dev verifies that `make -n dev` invokes the dev-check
// prerequisite and then runs ./scripts/dev.sh.
func TestMakeDryRun_Dev(t *testing.T) {
	t.Parallel()

	out := runMake(t, "-n", "dev")

	if !strings.Contains(out, "./scripts/dev.sh") {
		t.Errorf("make -n dev should reference ./scripts/dev.sh\nOutput:\n%s", out)
	}
}

// TestMakeDevCheck verifies that `make dev-check` succeeds when all
// dependencies (air, node) are present and prints a confirmation message.
func TestMakeDevCheck(t *testing.T) {
	t.Parallel()

	// This test only runs if both air and node are installed.
	// Skip gracefully if they're not present (CI may not have them).
	if _, err := exec.LookPath("air"); err != nil {
		t.Skip("air not installed, skipping dev-check test")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed, skipping dev-check test")
	}

	out := runMake(t, "dev-check")

	if !strings.Contains(out, "All dev dependencies found") {
		t.Errorf("make dev-check should print success message\nOutput:\n%s", out)
	}
}

// TestMakeDevCheckFailsWithoutAir verifies that `make dev-check` exits
// non-zero and prints an error when air is not on PATH.
func TestMakeDevCheckFailsWithoutAir(t *testing.T) {
	t.Parallel()

	// Run make dev-check with a PATH that excludes air.
	// We set PATH to only include system essentials so air won't be found.
	cmd := exec.Command("make", "dev-check")
	cmd.Dir = repoRoot(t)
	// Use a minimal PATH that has make but likely not air
	cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	if err == nil {
		// If air happens to be in /usr/bin or /bin, skip the test
		if strings.Contains(string(out), "All dev dependencies found") {
			t.Skip("air found on minimal PATH, cannot test failure mode")
		}
		t.Errorf("make dev-check should fail when air is not found, but it succeeded\nOutput:\n%s", out)
		return
	}

	if !strings.Contains(string(out), "air not found") {
		t.Errorf("make dev-check error output should mention 'air not found'\nOutput:\n%s", out)
	}
}

// TestMakefileDevTargetDependsOnDevCheck verifies that the dev target
// has dev-check as a prerequisite by inspecting the Makefile source.
func TestMakefileDevTargetDependsOnDevCheck(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/Makefile")
	if err != nil {
		t.Fatalf("reading Makefile: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		// Look for "dev: dev-check" target rule
		if strings.HasPrefix(line, "dev:") && strings.Contains(line, "dev-check") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Makefile 'dev' target should depend on 'dev-check'")
	}
}

// ---------------------------------------------------------------------------
// .gitignore tests
// ---------------------------------------------------------------------------

// TestGitignoreIncludesTmp verifies that .gitignore contains a tmp/ entry
// to exclude air's build artifacts.
func TestGitignoreIncludesTmp(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	found := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "tmp/" || trimmed == "tmp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(".gitignore should contain 'tmp/' entry for air build artifacts")
	}
}

// TestGitignoreIncludesNodeModules verifies that .gitignore excludes
// frontend node_modules (pre-existing requirement).
func TestGitignoreIncludesNodeModules(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.gitignore")
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	if !strings.Contains(string(data), "node_modules") {
		t.Errorf(".gitignore should contain 'node_modules' entry")
	}
}

// TestAirTomlExists verifies that the .air.toml configuration file
// exists at the repo root for the dev workflow.
func TestAirTomlExists(t *testing.T) {
	t.Parallel()

	path := repoRoot(t) + "/.air.toml"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf(".air.toml should exist at repo root for dev workflow")
	}
}

// TestAirTomlTmpDir verifies that .air.toml configures tmp/ as the
// temporary build directory, matching the .gitignore entry.
func TestAirTomlTmpDir(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}

	if !strings.Contains(string(data), `tmp_dir = "tmp"`) {
		t.Errorf(`.air.toml should set tmp_dir = "tmp"`)
	}
}

// TestAirTomlBuildCmd verifies that .air.toml builds the correct binary.
func TestAirTomlBuildCmd(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "./cmd/loom") {
		t.Errorf(".air.toml build cmd should reference ./cmd/loom")
	}
	if !strings.Contains(content, `bin = "./tmp/loom"`) {
		t.Errorf(`.air.toml should set bin = "./tmp/loom"`)
	}
}

// TestAirTomlExcludesFrontend verifies that .air.toml excludes the
// frontend directory to avoid triggering Go rebuilds on JS changes.
func TestAirTomlExcludesFrontend(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(repoRoot(t) + "/.air.toml")
	if err != nil {
		t.Fatalf("reading .air.toml: %v", err)
	}

	if !strings.Contains(string(data), "internal/webui/frontend") {
		t.Errorf(".air.toml should exclude internal/webui/frontend from watch")
	}
}

// TestDevShExists verifies that scripts/dev.sh exists and is executable.
func TestDevShExists(t *testing.T) {
	t.Parallel()

	path := repoRoot(t) + "/scripts/dev.sh"
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		t.Fatalf("scripts/dev.sh should exist")
	}
	if err != nil {
		t.Fatalf("stat scripts/dev.sh: %v", err)
	}

	// Check executable bit (owner)
	if info.Mode()&0100 == 0 {
		t.Errorf("scripts/dev.sh should be executable (mode: %o)", info.Mode())
	}
}
