package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
)

func TestSuperviseLocalServeRestartsAfterHealthyChildExit(t *testing.T) {
	originalStart := localServiceStartServe
	originalAwait := localServiceAwaitServe
	originalWait := localServiceWaitServe
	originalDelay := localServiceRestartDelay
	t.Cleanup(func() {
		localServiceStartServe = originalStart
		localServiceAwaitServe = originalAwait
		localServiceWaitServe = originalWait
		localServiceRestartDelay = originalDelay
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starts := 0
	localServiceRestartDelay = 0
	localServiceStartServe = func(context.Context, *localServiceConfig, io.Writer, *runtimeInfo) (*localServeProcess, error) {
		starts++
		return &localServeProcess{cmd: &exec.Cmd{}}, nil
	}
	localServiceAwaitServe = func(context.Context, *localServiceConfig, *runtimeInfo, *localServeProcess) error {
		return nil
	}
	localServiceWaitServe = func(context.Context, *localServeProcess, string, *runtimeInfo) error {
		if starts == 1 {
			return errors.New("signal: killed")
		}
		cancel()
		return nil
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
	localServiceStartServe = func(context.Context, *localServiceConfig, io.Writer, *runtimeInfo) (*localServeProcess, error) {
		starts++
		done := make(chan struct{})
		close(done)
		return &localServeProcess{cmd: &exec.Cmd{}, done: done}, nil
	}
	localServiceAwaitServe = func(context.Context, *localServiceConfig, *runtimeInfo, *localServeProcess) error {
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
