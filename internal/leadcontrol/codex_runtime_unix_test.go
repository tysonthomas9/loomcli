//go:build unix

package leadcontrol

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigureCodexAppServerProcessCreatesDedicatedGroup(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("true") //nolint:norawexec // Test-only command object; it is never started.
	configureCodexAppServerProcess(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Codex app-server process was not configured with Setpgid")
	}
}

func TestStartAndStopCodexAppServerUsesLeadHomeAndCleansProcessGroup(t *testing.T) {
	runtimeHome := t.TempDir()
	stateDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "fake-codex")
	script := "#!/bin/sh\n" +
		"printf '%s' \"$CODEX_HOME\" > \"$LEAD_TEST_STATE/codex-home\"\n" +
		"trap 'wait; exit 0' TERM INT\n" +
		"sleep 30 &\n" +
		"wait\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	t.Setenv("LEAD_TEST_STATE", stateDir)
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "inherited"))

	leadHome := filepath.Join(runtimeHome, "codex-home")
	cmd, appErr, cancel, logFile, err := startCodexAppServer(
		context.Background(),
		CodexLeadRuntimeConfig{CodexPath: scriptPath, WorkDir: runtimeHome},
		runtimeHome,
		leadHome,
		filepath.Join(runtimeHome, "sqlite"),
		"ws://127.0.0.1:1",
	)
	if err != nil {
		t.Fatalf("startCodexAppServer() error: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	homePath := filepath.Join(stateDir, "codex-home")
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(homePath)
		if readErr == nil {
			if got := strings.TrimSpace(string(data)); got != leadHome {
				t.Fatalf("child CODEX_HOME = %q, want %q", got, leadHome)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake Codex did not record CODEX_HOME: %v", readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := stopCodexAppServer(cmd, appErr, cancel); err != nil {
		t.Fatalf("stopCodexAppServer() error: %v", err)
	}
	if codexAppServerProcessGroupAlive(cmd) {
		t.Fatal("Codex app-server process group still alive after stop")
	}
}
