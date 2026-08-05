package lockfile

import (
	"os"
	"testing"
)

func TestArgsAreLoomDaemon(t *testing.T) {
	t.Parallel()

	rejected := [][]string{
		{"/usr/local/bin/loom", "serve", "daemon"},
		{"/usr/local/bin/loom", "--workspace", "daemon", "serve"},
		{"/usr/local/bin/loom", "--unknown", "value", "daemon"},
		{"/usr/local/bin/codex", "--remote", "ws://localhost"},
		{"/usr/local/bin/loom"},
	}
	for _, args := range rejected {
		if argsAreLoomDaemon(args) {
			t.Fatalf("args accepted as loom daemon: %q", args)
		}
	}

	accepted := [][]string{
		{"/usr/local/bin/loom", "daemon"},
		{"/usr/local/bin/loom", "--workspace", "ACME", "daemon"},
		{"/usr/local/bin/loom", "--backend=codex", "--log-format", "json", "daemon"},
		{"/usr/local/bin/loom", "-o", "json", "--server=http://localhost:8080", "daemon"},
		{"/usr/local/bin/loom", "--", "daemon"},
	}
	for _, args := range accepted {
		if !argsAreLoomDaemon(args) {
			t.Fatalf("loom daemon argv was rejected: %q", args)
		}
	}
}

func TestCurrentTestProcessIsNotLoomDaemon(t *testing.T) {
	t.Parallel()
	if IsLoomDaemonProcess(os.Getpid()) {
		t.Fatal("Go test process was accepted as loom daemon")
	}
}
