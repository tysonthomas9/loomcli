package leadcontrol

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexThreadStatusAndNewestThread(t *testing.T) {
	statuses := []struct {
		status CodexThreadStatus
		want   string
	}{
		{CodexThreadStatus{Type: "idle"}, RuntimeStatusIdle},
		{CodexThreadStatus{Type: "active"}, RuntimeStatusActive},
		{CodexThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnApproval"}}, RuntimeStatusWaitingApproval},
		{CodexThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnUserInput"}}, RuntimeStatusWaitingUserInput},
		{CodexThreadStatus{Type: "systemError"}, RuntimeStatusFailed},
		{CodexThreadStatus{Type: "notLoaded"}, RuntimeStatusDisconnected},
		{CodexThreadStatus{}, RuntimeStatusDisconnected},
		{CodexThreadStatus{Type: "custom"}, "custom"},
	}
	for _, tt := range statuses {
		if got := tt.status.RuntimeStatus(); got != tt.want {
			t.Fatalf("%+v RuntimeStatus = %q, want %q", tt.status, got, tt.want)
		}
	}
	if !(CodexThreadStatus{Type: "idle"}).CanStartTurn() || (CodexThreadStatus{Type: "active"}).CanStartTurn() {
		t.Fatal("CanStartTurn mismatch")
	}

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	threads := []CodexThread{
		{ID: "", Cwd: "/work", CreatedAtMS: float64(base.Add(time.Minute).UnixMilli())},
		{ID: "old", Cwd: "/work", CreatedAtMS: float64(base.Add(-time.Minute).UnixMilli())},
		{ID: "other-cwd", Cwd: "/other", CreatedAtMS: float64(base.Add(time.Hour).UnixMilli())},
		{ID: "best", Cwd: "/work", CreatedAtMS: float64(base.Add(time.Second).UnixMilli()), UpdatedAtMS: float64(base.Add(2 * time.Second).UnixMilli())},
	}
	best := newestCodexThread(threads, "/work", base)
	if best == nil || best.ID != "best" {
		t.Fatalf("best thread = %+v", best)
	}
	if got := newestCodexThread(threads, "/missing", base); got != nil {
		t.Fatalf("missing cwd got %+v", got)
	}
}

func TestCodexRuntimePureHelpers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace: " WS/One ", LeadName: " Lead One ", SessionID: " Sess:One ",
		WorkDir: " /work ", Prompt: "prompt",
	})
	if cfg.CodexPath != defaultCodexBinary || cfg.Stdin == nil || cfg.Stdout == nil || cfg.Stderr == nil || cfg.Logger == nil {
		t.Fatalf("normalized cfg = %+v", cfg)
	}
	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	if !strings.Contains(runtimeHome, filepath.Join("ws-one", "lead-one", "sess-one")) || !strings.HasSuffix(sqliteHome, "sqlite") {
		t.Fatalf("runtime dirs = %q %q", runtimeHome, sqliteHome)
	}
	if got := sanitizeRuntimePathPart(" A/b:C "); got != "a-b-c" {
		t.Fatalf("sanitized = %q", got)
	}
	if got := unixFloatTime(1_700_000_000_000); got.IsZero() || got.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("millis time = %v", got)
	}
	if got := unixFloatTime(1700000000.5); got.IsZero() || got.Nanosecond() == 0 {
		t.Fatalf("seconds time = %v", got)
	}
	if got := threadSortTime(CodexThread{UpdatedAt: 1700000000}); got.IsZero() {
		t.Fatal("threadSortTime returned zero")
	}
	if got := threadCreatedAt(CodexThread{CreatedAt: 1700000000}); got.IsZero() {
		t.Fatal("threadCreatedAt returned zero")
	}
}

func TestWaitForCodexAppServerBranches(t *testing.T) {
	errCh := make(chan error, 1)
	errCh <- errors.New("boom")
	close(errCh)
	if err := waitForCodexAppServer(context.Background(), "ws://127.0.0.1:1", errCh); err == nil || !strings.Contains(err.Error(), "before ready") {
		t.Fatalf("app exit err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForCodexAppServer(ctx, "ws://127.0.0.1:1", make(chan error)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
}

func TestRunCodexLeadRuntimeEarlyDirectoryError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	badWorkDir := filepath.Join(t.TempDir(), "missing")
	err := RunCodexLeadRuntime(context.Background(), CodexLeadRuntimeConfig{
		Workspace: "WS", LeadName: "lead", SessionID: "sess",
		WorkDir: badWorkDir, CodexPath: filepath.Join(t.TempDir(), "missing-codex"),
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("RunCodexLeadRuntime with missing binary returned nil")
	}
	_ = os.RemoveAll(badWorkDir)
}
