package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractAccessGateFailsWithoutToken(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		name := "missing"
		if configured {
			name = "configured"
		}
		t.Run(name, func(t *testing.T) {
			command := exec.Command("bash", filepath.Join(root, "scripts", "check-fleet-contract-access.sh")) //nolint:norawexec // Fixed repository script under test.
			command.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_STEP_SUMMARY=" + filepath.Join(t.TempDir(), "summary")}
			if configured {
				command.Env = append(command.Env, "FLEET_DB_REPO_TOKEN=non-secret-test-sentinel")
			}
			output, err := command.CombinedOutput()
			if configured && err != nil || !configured && err == nil {
				t.Fatalf("configured=%v: unexpected result %v: %s", configured, err, output)
			}
			if strings.Contains(string(output), "non-secret-test-sentinel") {
				t.Fatal("gate printed credential contents")
			}
			if !configured && !strings.Contains(string(output), "::error") {
				t.Fatal("missing token did not emit a visible CI error")
			}
		})
	}
}
