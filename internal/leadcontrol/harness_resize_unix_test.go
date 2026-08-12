//go:build unix

package leadcontrol

import (
	"context"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestStartHarnessResizeForwarderStopIsIdempotent(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("open test PTY: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: 144, Rows: 85}); err != nil {
		t.Fatalf("size test PTY: %v", err)
	}

	fake := newFakeHarnessConversation()
	stop := startHarnessResizeForwarder(context.Background(), HarnessLeadRuntimeConfig{
		Stdout: ptmx,
		Logger: resizeTestLogger(),
	}, fake)
	waitForResizeCalls(t, fake, 1)

	const callers = 8
	var stopped sync.WaitGroup
	stopped.Add(callers)
	for range callers {
		go func() {
			defer stopped.Done()
			stop()
		}()
	}
	done := make(chan struct{})
	go func() {
		stopped.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent repeated stop calls did not return")
	}

	// stop waits for the forwarder and unregisters SIGWINCH. A later signal
	// therefore cannot produce another resize call.
	wantCalls := len(fake.resizeCalls())
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("send SIGWINCH after stop: %v", err)
	}
	assertResizeCallCountStays(t, fake, wantCalls, 2*harnessResizeRetryDelay)
}
