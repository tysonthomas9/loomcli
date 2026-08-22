//go:build !windows

package daemon

import (
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func TestResolveDaemonSocketPath(t *testing.T) {
	t.Run("short project uses PID-adjacent path", func(t *testing.T) {
		projectDir := "/tmp/loom-project"
		got := resolveDaemonSocketPath(projectDir, ".runtime/daemon.pid")
		want := filepath.Join(projectDir, ".runtime", "daemon.sock")
		if got != want {
			t.Fatalf("resolveDaemonSocketPath() = %q, want %q", got, want)
		}
	})

	t.Run("long project uses short path", func(t *testing.T) {
		projectDir := "/tmp/" + strings.Repeat("a", 150)
		got := resolveDaemonSocketPath(projectDir, ".loom/daemon.pid")
		if len(got) > rpc.MaxUnixSocketPath || !strings.HasPrefix(got, "/tmp/loom-") {
			t.Fatalf("resolveDaemonSocketPath() = %q (len %d), want short /tmp/loom-* path", got, len(got))
		}
	})
}

func TestControlServerBindsForLongWorkspacePath(t *testing.T) {
	projectDir := "/tmp/" + strings.Repeat("bind-", 30)
	socketPath := resolveDaemonSocketPath(projectDir, ".loom/daemon.pid")
	d := newTestDaemonWithAgents(nil)
	t.Cleanup(func() {
		d.sup.ShutdownOnce.Do(func() { close(d.sup.Shutdown) })
		if d.controlListener != nil {
			_ = d.controlListener.Close()
		}
		_ = rpc.CleanupSocketDir(socketPath)
	})

	if err := d.startControlServer(socketPath); err != nil {
		t.Fatalf("startControlServer(%q): %v", socketPath, err)
	}
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial control socket: %v", err)
	}
	_ = conn.Close()
}
