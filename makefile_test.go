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
		name    string
		substr  string
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
