package automode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamContextActivityAndLogOutputBranches(t *testing.T) {
	oldFixed := useFixedPolling
	useFixedPolling = false
	t.Cleanup(func() { useFixedPolling = oldFixed })

	poller := newAdaptivePoller()
	sc := &streamCtx{poller: poller}
	sc.markNoActivity()
	if poller.currentInterval <= poller.minInterval {
		t.Fatalf("markNoActivity interval = %s, want backed off", poller.currentInterval)
	}
	sc.markActivity()
	if poller.currentInterval != poller.minInterval {
		t.Fatalf("markActivity interval = %s, want %s", poller.currentInterval, poller.minInterval)
	}
	if sc.getTickChan() == nil {
		t.Fatal("adaptive getTickChan returned nil")
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	fixed := &streamCtx{ticker: ticker}
	useFixedPolling = true
	if fixed.getTickChan() == nil {
		t.Fatal("fixed getTickChan returned nil")
	}
	useFixedPolling = false

	logFile := filepath.Join(t.TempDir(), "agent.log")
	missing := &streamCtx{logFile: logFile, poller: newAdaptivePoller()}
	streamLogOutput(missing)
	if missing.poller.currentInterval <= missing.poller.minInterval {
		t.Fatalf("missing log should mark no activity, interval=%s", missing.poller.currentInterval)
	}

	if err := os.WriteFile(logFile, []byte("hello\x1b[I world"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	sc = &streamCtx{logFile: logFile, poller: newAdaptivePoller()}
	streamLogOutput(sc)
	if sc.lastOffset != int64(len("hello\x1b[I world")) {
		t.Fatalf("lastOffset = %d", sc.lastOffset)
	}
	if sc.poller.currentInterval != sc.poller.minInterval {
		t.Fatalf("read should mark activity, interval=%s", sc.poller.currentInterval)
	}
	streamLogOutput(sc)
	if sc.poller.currentInterval <= sc.poller.minInterval {
		t.Fatalf("unchanged log should mark no activity, interval=%s", sc.poller.currentInterval)
	}

	if err := os.WriteFile(logFile, []byte("short"), 0600); err != nil {
		t.Fatalf("truncate log: %v", err)
	}
	sc.lastOffset = 99
	streamLogOutput(sc)
	if sc.lastOffset != int64(len("short")) {
		t.Fatalf("truncated log offset = %d", sc.lastOffset)
	}
}

func TestStreamRemainingLogContentAndCanonicalizePathBranches(t *testing.T) {
	var offset int64
	streamRemainingLogContent(filepath.Join(t.TempDir(), "missing.log"), &offset)
	if offset != 0 {
		t.Fatalf("missing log offset = %d, want 0", offset)
	}

	logFile := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(logFile, []byte("abcdef\x1b[O"), 0600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	offset = 3
	streamRemainingLogContent(logFile, &offset)
	if offset != int64(len("abcdef\x1b[O")) {
		t.Fatalf("remaining offset = %d", offset)
	}

	if err := os.WriteFile(logFile, []byte("xy"), 0600); err != nil {
		t.Fatalf("truncate log: %v", err)
	}
	offset = 99
	streamRemainingLogContent(logFile, &offset)
	if offset != 2 {
		t.Fatalf("truncated remaining offset = %d, want 2", offset)
	}

	realDir := t.TempDir()
	linkDir := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	wantRealDir, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("eval real dir: %v", err)
	}
	if got := canonicalizePath(linkDir); got != wantRealDir {
		t.Fatalf("canonicalize symlink = %q, want %q", got, wantRealDir)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	got := canonicalizePath(missing)
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, "missing") {
		t.Fatalf("canonicalize missing = %q, want absolute missing path", got)
	}
}

func TestHandleTmuxAttachSuccessMarksActivity(t *testing.T) {
	binDir := t.TempDir()
	tmuxPath := filepath.Join(binDir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	poller := newAdaptivePoller()
	sc := &streamCtx{sessionName: "attach-test", poller: poller}
	sc.markNoActivity()
	if poller.currentInterval <= poller.minInterval {
		t.Fatalf("setup interval = %s, want backed off", poller.currentInterval)
	}
	handleTmuxAttach(sc, make(chan struct{}))
	if poller.currentInterval != poller.minInterval {
		t.Fatalf("handleTmuxAttach interval = %s, want %s", poller.currentInterval, poller.minInterval)
	}
}
