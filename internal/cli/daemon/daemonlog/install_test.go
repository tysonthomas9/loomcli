package daemonlog

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestInstall_WritesThroughSlogDefault(t *testing.T) {
	saved := slog.Default()
	t.Cleanup(func() { slog.SetDefault(saved) })

	logDir := t.TempDir()
	sink, err := Install(logDir, "ws-abc")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	defer func() { _ = sink.Close() }()

	want := filepath.Join(logDir, "ws-abc", "daemon.log")
	if sink.Path() != want {
		t.Errorf("Path() = %q, want %q", sink.Path(), want)
	}

	slog.Info("daemon heartbeat", "agents", 3)
	b, readErr := os.ReadFile(want) //nolint:gosec // test-controlled path
	if readErr != nil {
		t.Fatalf("read daemon.log: %v", readErr)
	}
	if !strings.Contains(string(b), "daemon heartbeat") {
		t.Errorf("daemon.log = %q, want it to contain the logged line", b)
	}
}

func TestInstall_OmitsEmptyWorkspaceSegment(t *testing.T) {
	saved := slog.Default()
	t.Cleanup(func() { slog.SetDefault(saved) })

	logDir := t.TempDir()
	sink, err := Install(logDir, "")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	defer func() { _ = sink.Close() }()

	if want := filepath.Join(logDir, "daemon.log"); sink.Path() != want {
		t.Errorf("Path() = %q, want %q", sink.Path(), want)
	}
}

func TestDaemonLogPath(t *testing.T) {
	tests := []struct {
		name        string
		projectDir  string
		cfg         *cfgpkg.DaemonConfig
		workspaceID string
		want        string
	}{
		{
			name:       "nil config defaults to .loom/logs under the project",
			projectDir: "/proj",
			want:       "/proj/.loom/logs/daemon.log",
		},
		{
			name:        "relative log_dir joins the project dir",
			projectDir:  "/proj",
			cfg:         &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: "logs"}},
			workspaceID: "ws-1",
			want:        "/proj/logs/ws-1/daemon.log",
		},
		{
			name:        "absolute log_dir is used as-is",
			projectDir:  "/proj",
			cfg:         &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: "/var/log/loom"}},
			workspaceID: "ws-1",
			want:        "/var/log/loom/ws-1/daemon.log",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DaemonLogPath(tc.projectDir, tc.cfg, tc.workspaceID); got != tc.want {
				t.Errorf("DaemonLogPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
