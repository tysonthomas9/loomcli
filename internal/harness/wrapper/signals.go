package wrapper

import (
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/tysonthomas9/loomcli/internal/harness/wrapper/trace"
)

// terminalState bundles per-Run terminal control state when both stdin
// and stdout are TTYs. A nil terminalState means raw mode and SIGWINCH
// forwarding are not active for the current Run.
type terminalState struct {
	stdin       *os.File
	oldState    *term.State
	winchSignal chan os.Signal
	stop        chan struct{}
	ptmx        *os.File
	emitter     trace.Emitter
}

// setupTerminalIfTTY puts the user's terminal into raw mode and starts a
// SIGWINCH forwarder when both stdin and stdout are *os.File values
// pointing at TTYs. For headless callers (any non-*os.File io.Reader /
// io.Writer, or non-TTY files) it returns nil and no terminal state is
// touched.
//
// Failure to put the terminal into raw mode is non-fatal: the wrapper
// proceeds without raw-mode passthrough and emits a trace event
// describing why.
func setupTerminalIfTTY(stdin io.Reader, stdout io.Writer, ptmx *os.File, emitter trace.Emitter) *terminalState {
	stdinFile, _ := stdin.(*os.File)
	stdoutFile, _ := stdout.(*os.File)
	if stdinFile == nil || stdoutFile == nil {
		return nil
	}
	if !term.IsTerminal(int(stdinFile.Fd())) || !term.IsTerminal(int(stdoutFile.Fd())) {
		return nil
	}

	if size, err := pty.GetsizeFull(stdinFile); err == nil {
		_ = pty.Setsize(ptmx, size)
		emitter.Emit(trace.Event{
			At:   time.Now(),
			Kind: "winsize_initial",
			Fields: map[string]any{
				"cols": size.Cols,
				"rows": size.Rows,
			},
		})
	}

	oldState, err := term.MakeRaw(int(stdinFile.Fd()))
	if err != nil {
		emitter.Emit(trace.Event{
			At:     time.Now(),
			Kind:   "raw_mode_setup_failed",
			Fields: map[string]any{"error": err.Error()},
		})
		return nil
	}

	ts := &terminalState{
		stdin:       stdinFile,
		oldState:    oldState,
		winchSignal: make(chan os.Signal, 1),
		stop:        make(chan struct{}),
		ptmx:        ptmx,
		emitter:     emitter,
	}
	signal.Notify(ts.winchSignal, syscall.SIGWINCH)
	go ts.forwardWinsize()
	emitter.Emit(trace.Event{At: time.Now(), Kind: "raw_mode_enabled"})
	return ts
}

func (ts *terminalState) forwardWinsize() {
	for {
		select {
		case <-ts.stop:
			return
		case <-ts.winchSignal:
			size, err := pty.GetsizeFull(ts.stdin)
			if err != nil {
				continue
			}
			_ = pty.Setsize(ts.ptmx, size)
			ts.emitter.Emit(trace.Event{
				At:   time.Now(),
				Kind: "winsize_changed",
				Fields: map[string]any{
					"cols": size.Cols,
					"rows": size.Rows,
				},
			})
		}
	}
}

// cleanup restores the user's terminal and stops the SIGWINCH
// forwarder. Safe to call on a nil receiver.
func (ts *terminalState) cleanup() {
	if ts == nil {
		return
	}
	signal.Stop(ts.winchSignal)
	close(ts.stop)
	_ = term.Restore(int(ts.stdin.Fd()), ts.oldState)
}
