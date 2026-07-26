package hooks

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHooksInstallCmd_FleetModeInstallsWithoutForce(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	origForce := hooksInstallForce
	hooksInstallForce = false
	t.Cleanup(func() { hooksInstallForce = origForce })

	var out bytes.Buffer
	hooksInstallCmd.SetOut(&out)
	t.Cleanup(func() { hooksInstallCmd.SetOut(os.Stdout) })

	dir := t.TempDir()
	if err := hooksInstallCmd.RunE(hooksInstallCmd, []string{dir}); err != nil {
		t.Fatalf("hooks install failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "installing Claude Code hooks as the primary transcript capture path") {
		t.Fatalf("expected fleet-mode install message, got %q", output)
	}

	matchers := readHookMatchers(t, dir, "SessionStart")
	if !matcherContainsCommand(matchers, hookCommands["SessionStart"]) {
		t.Fatalf("expected SessionStart hook to be installed, got %+v", matchers)
	}
}
