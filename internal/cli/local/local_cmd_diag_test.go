package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalServeStartupTimeoutCoversDurableReplay(t *testing.T) {
	if localServeStartupTimeout < 2*time.Minute {
		t.Fatalf("localServeStartupTimeout = %s, want at least 2m for durable FleetDB replay", localServeStartupTimeout)
	}
}

func TestAwaitServeHealthyReturnsWhenChildExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper command uses a POSIX shell")
	}

	dataDir := t.TempDir()
	writeServeLog(t, dataDir, "fleet-db compatibility check failed\n")
	cfg := &localServiceConfig{
		dataDir: dataDir,
		url:     "http://127.0.0.1:1",
	}
	info := &runtimeInfo{}
	serveCmd := exec.Command("/bin/sh", "-c", "exit 42") //nolint:norawexec // deterministic child-exit regression fixture.
	if err := serveCmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	startedAt := time.Now()
	err := awaitServeHealthy(ctx, cfg, info, trackLocalServeProcess(serveCmd))
	elapsed := time.Since(startedAt)

	if err == nil {
		t.Fatal("awaitServeHealthy() error = nil, want exited-child error")
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("awaitServeHealthy() elapsed = %s, want immediate child-exit detection", elapsed)
	}
	if !strings.Contains(err.Error(), "exit status 42") {
		t.Fatalf("awaitServeHealthy() error = %q, want child exit status", err)
	}
	if !strings.Contains(err.Error(), "fleet-db compatibility check failed") {
		t.Fatalf("awaitServeHealthy() error = %q, want recent startup log", err)
	}
	if info.Status != "failed" {
		t.Fatalf("runtime status = %q, want failed", info.Status)
	}
}

func writeServeLog(t *testing.T, dataDir, contents string) {
	t.Helper()
	logsDir := filepath.Join(dataDir, logsDirName)
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.WriteFile(serveLogPath(dataDir), []byte(contents), 0600); err != nil {
		t.Fatalf("write serve log: %v", err)
	}
}

func TestServeStartupLogTailReturnsTailOfLog(t *testing.T) {
	tmp := t.TempDir()
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("line-%d", i))
	}
	writeServeLog(t, tmp, strings.Join(lines, "\n")+"\n")

	got := serveStartupLogTail(tmp, 1024)
	if got == "" {
		t.Fatal("serveStartupLogTail() = \"\", want last lines")
	}
	if !strings.HasSuffix(got, "line-9") {
		t.Fatalf("serveStartupLogTail() = %q, want trailing line-9", got)
	}
	if !strings.Contains(got, "line-0") {
		t.Fatalf("serveStartupLogTail() = %q, want all 10 lines (well under 1024 bytes)", got)
	}
}

func TestServeStartupLogTailMissingLog(t *testing.T) {
	tmp := t.TempDir()
	if got := serveStartupLogTail(tmp, 1024); got != "" {
		t.Fatalf("serveStartupLogTail() = %q, want \"\" for missing log", got)
	}
}

func TestServeStartupLogTailEmptyLog(t *testing.T) {
	tmp := t.TempDir()
	writeServeLog(t, tmp, "")
	if got := serveStartupLogTail(tmp, 1024); got != "" {
		t.Fatalf("serveStartupLogTail() = %q, want \"\" for empty log", got)
	}
}

func TestServeStartupLogTailKeepsFullLineWhenSeekLandsOnBoundary(t *testing.T) {
	tmp := t.TempDir()
	// "line1\nline2\n" is 12 bytes; tailing with maxBytes==6 seeks to byte 6,
	// which sits exactly on the start of "line2". The byte at offset 5 is
	// '\n', so we must return "line2" intact rather than treating it as a
	// partial line and emitting "".
	writeServeLog(t, tmp, "line1\nline2\n")
	got := serveStartupLogTail(tmp, 6)
	if got != "line2" {
		t.Fatalf("serveStartupLogTail() = %q, want %q (boundary seek must preserve full line)", got, "line2")
	}
}

func TestServeStartupLogTailDropsPartialFirstLine(t *testing.T) {
	tmp := t.TempDir()
	var b strings.Builder
	// Produce ~8 KB of well-formed lines.
	for i := 0; b.Len() < 8*1024; i++ {
		fmt.Fprintf(&b, "line-%04d %s\n", i, strings.Repeat("x", 64))
	}
	writeServeLog(t, tmp, b.String())

	got := serveStartupLogTail(tmp, 1024)
	if got == "" {
		t.Fatal("serveStartupLogTail() = \"\", want non-empty tail")
	}
	if len(got) > 1024 {
		t.Fatalf("serveStartupLogTail() len = %d, want <= 1024", len(got))
	}
	// First line in the result should begin with "line-" — i.e. a complete
	// log line, never a fragment of one cut mid-token.
	first := strings.SplitN(got, "\n", 2)[0]
	if !strings.HasPrefix(first, "line-") {
		t.Fatalf("serveStartupLogTail() first line = %q, want clean line beginning with 'line-'", first)
	}
}

func TestWrapServeStartupErrorIncludesLogTail(t *testing.T) {
	tmp := t.TempDir()
	writeServeLog(t, tmp, "failed to open fleet-db store: ... fleet-db binary not found; checked PATH:fleet-db, /Users/oleh/go/bin/fleet-db\n")
	healthErr := errors.New("connect: connection refused")

	wrapped := wrapServeStartupError(tmp, healthErr)
	msg := wrapped.Error()
	if !strings.Contains(msg, "connect: connection refused") {
		t.Fatalf("wrapped.Error() = %q, want to contain health error", msg)
	}
	if !strings.Contains(msg, "fleet-db binary not found") {
		t.Fatalf("wrapped.Error() = %q, want to contain log tail with 'fleet-db binary not found'", msg)
	}
	if got := errors.Unwrap(wrapped); got != healthErr {
		t.Fatalf("errors.Unwrap(wrapped) = %v, want original healthErr", got)
	}
	if !errors.Is(wrapped, healthErr) {
		t.Fatalf("errors.Is(wrapped, healthErr) = false, want true")
	}
}

func TestWrapServeStartupErrorFallsBackWhenLogEmpty(t *testing.T) {
	tmp := t.TempDir()
	healthErr := errors.New("connect: connection refused")

	wrapped := wrapServeStartupError(tmp, healthErr)
	want := "local runtime did not become healthy: connect: connection refused"
	if got := wrapped.Error(); got != want {
		t.Fatalf("wrapped.Error() = %q, want %q", got, want)
	}
	if !errors.Is(wrapped, healthErr) {
		t.Fatal("errors.Is(wrapped, healthErr) = false, want true (fallback path)")
	}
}
