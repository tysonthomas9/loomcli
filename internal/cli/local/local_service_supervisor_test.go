package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
)

func TestSuperviseLocalServeRestartsAfterHealthyChildExit(t *testing.T) {
	originalStart := localServiceStartServe
	originalAwait := localServiceAwaitServe
	originalWait := localServiceWaitServe
	originalStartDaemon := localServiceStartDaemon
	originalAwaitDaemon := localServiceAwaitDaemon
	originalDelay := localServiceRestartDelay
	t.Cleanup(func() {
		localServiceStartServe = originalStart
		localServiceAwaitServe = originalAwait
		localServiceWaitServe = originalWait
		localServiceStartDaemon = originalStartDaemon
		localServiceAwaitDaemon = originalAwaitDaemon
		localServiceRestartDelay = originalDelay
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starts := 0
	daemonStarts := 0
	daemonAwaits := 0
	localServiceRestartDelay = 0
	localServiceStartServe = func(context.Context, *localServiceConfig, *os.File, *runtimeInfo) (*exec.Cmd, error) {
		starts++
		return &exec.Cmd{}, nil
	}
	localServiceAwaitServe = func(context.Context, *localServiceConfig, *runtimeInfo, *exec.Cmd) error {
		return nil
	}
	localServiceWaitServe = func(context.Context, *exec.Cmd, string, *runtimeInfo) error {
		if starts == 1 {
			return errors.New("signal: killed")
		}
		cancel()
		return nil
	}
	localServiceStartDaemon = func(context.Context, string, string, int, string) <-chan struct{} {
		daemonStarts++
		done := make(chan struct{})
		close(done)
		return done
	}
	localServiceAwaitDaemon = func(string, <-chan struct{}) {
		daemonAwaits++
	}

	dataDir := t.TempDir()
	cfg := &localServiceConfig{dataDir: dataDir, url: "http://127.0.0.1:12345"}
	info := newRuntimeInfo(cfg)
	var output bytes.Buffer
	if err := superviseLocalServe(ctx, cfg, nil, info, &output); err != nil {
		t.Fatalf("superviseLocalServe() error = %v", err)
	}
	if starts != 2 {
		t.Fatalf("serve starts = %d, want 2", starts)
	}
	if daemonStarts != 2 || daemonAwaits != 2 {
		t.Fatalf("daemon generations = %d starts, %d awaits; want 2 and 2", daemonStarts, daemonAwaits)
	}
	if got := output.String(); !bytes.Contains([]byte(got), []byte("signal: killed")) {
		t.Fatalf("service output = %q, want crash cause", got)
	}
}

func TestSuperviseLocalServeReturnsInitialHealthFailure(t *testing.T) {
	originalStart := localServiceStartServe
	originalAwait := localServiceAwaitServe
	t.Cleanup(func() {
		localServiceStartServe = originalStart
		localServiceAwaitServe = originalAwait
	})

	starts := 0
	wantErr := errors.New("fleet compatibility failure")
	localServiceStartServe = func(context.Context, *localServiceConfig, *os.File, *runtimeInfo) (*exec.Cmd, error) {
		starts++
		return &exec.Cmd{}, nil
	}
	localServiceAwaitServe = func(context.Context, *localServiceConfig, *runtimeInfo, *exec.Cmd) error {
		return wantErr
	}

	dataDir := t.TempDir()
	cfg := &localServiceConfig{dataDir: dataDir, url: "http://127.0.0.1:12345"}
	info := newRuntimeInfo(cfg)
	err := superviseLocalServe(context.Background(), cfg, nil, info, io.Discard)
	if !errors.Is(err, wantErr) {
		t.Fatalf("superviseLocalServe() error = %v, want %v", err, wantErr)
	}
	if starts != 1 {
		t.Fatalf("serve starts = %d, want 1", starts)
	}
}
