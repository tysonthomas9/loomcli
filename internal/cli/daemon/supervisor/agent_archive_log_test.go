package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
	webuilog "github.com/tysonthomas9/loomcli/internal/webui/log"
)

// TestSetupAgentLogFile_WritesUIArchive verifies that a daemon-supervised
// agent's stdout/stderr is teed into the canonical archive the web UI Logs tab
// reads (webuilog.GetAgentLogPath), in addition to the watchdog's daemon log.
// This is the regression guard for daemon-mode agents showing empty logs.
func TestSetupAgentLogFile_WritesUIArchive(t *testing.T) {
	tmp := t.TempDir()
	// Anchor the archive resolver (webuilog.GetLogDir) at the temp dir so the
	// test never touches the real ~/.loom/logs.
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)
	t.Setenv("LOOM_CONFIG_DIR", "")

	daemonLogDir := filepath.Join(tmp, "daemon-logs")
	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: daemonLogDir}}
		},
		ProjectDir:    tmp,
		WorkspaceID:   "ws-test",
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "ember", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmp,
	}

	cmd := &exec.Cmd{}
	ap.Mu.Lock()
	s.setupAgentLogFile(ap, cmd)
	ap.Mu.Unlock()

	if cmd.Stdout == nil {
		t.Fatal("cmd.Stdout was not wired to any log sink")
	}
	if ap.ArchiveLogFile == nil {
		t.Fatal("ArchiveLogFile was not opened")
	}
	if ap.LogFile == nil {
		t.Fatal("daemon LogFile was not opened (MultiWriter tee path not exercised)")
	}

	const line = "agent rendered output line\n"
	if _, err := cmd.Stdout.Write([]byte(line)); err != nil {
		t.Fatalf("writing to cmd.Stdout: %v", err)
	}

	ap.Mu.Lock()
	closeAgentLogs(ap)
	ap.Mu.Unlock()

	// The archive must live exactly where the reader looks.
	archivePath, err := webuilog.GetAgentLogPath("ws-test", "ember")
	if err != nil {
		t.Fatalf("GetAgentLogPath: %v", err)
	}
	wantArchive := filepath.Join(tmp, ".loom", "logs", "ws-test", "agents", "ember.log")
	if archivePath != wantArchive {
		t.Fatalf("archive path = %q, want %q", archivePath, wantArchive)
	}
	if got := readFile(t, archivePath); got != line {
		t.Errorf("archive content = %q, want %q", got, line)
	}

	// The daemon log (watchdog target) must still receive the same bytes.
	if got := readFile(t, ap.LogFilePath); got != line {
		t.Errorf("daemon log content = %q, want %q", got, line)
	}
}

// TestSetupAgentLogFile_ArchiveWithoutDaemonLog verifies the archive is written
// even when no daemon LogDir is configured (the daemon log sink is disabled),
// so the Logs tab works regardless of daemon log configuration.
func TestSetupAgentLogFile_ArchiveWithoutDaemonLog(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", tmp)
	t.Setenv("LOOM_CONFIG_DIR", "")

	s := &Supervisor{
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: ""}}
		},
		ProjectDir:    tmp,
		WorkspaceID:   "ws-test",
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		EmitEvent:     func(events.Event) {},
	}
	ap := &AgentProcess{
		Entry:        cfgpkg.AgentEntry{Worktree: "ember", Role: "plan"},
		RoleConfig:   cfgpkg.RoleConfig{Description: "Built-in plan agent"},
		WorktreePath: tmp,
	}

	cmd := &exec.Cmd{}
	ap.Mu.Lock()
	s.setupAgentLogFile(ap, cmd)
	ap.Mu.Unlock()

	if ap.LogFile != nil || ap.LogFilePath != "" {
		t.Fatal("daemon log should be disabled when LogDir is empty")
	}
	if ap.ArchiveLogFile == nil {
		t.Fatal("archive log should still be opened when LogDir is empty")
	}

	const line = "lone archive line\n"
	if _, err := cmd.Stdout.Write([]byte(line)); err != nil {
		t.Fatalf("writing to cmd.Stdout: %v", err)
	}

	ap.Mu.Lock()
	closeAgentLogs(ap)
	ap.Mu.Unlock()

	archivePath, err := webuilog.GetAgentLogPath("ws-test", "ember")
	if err != nil {
		t.Fatalf("GetAgentLogPath: %v", err)
	}
	if got := readFile(t, archivePath); got != line {
		t.Errorf("archive content = %q, want %q", got, line)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: test-controlled path under t.TempDir()
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}
