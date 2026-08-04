package backends

import (
	"os"
	"sync"
)

// processGuard serializes signal delivery with the post-Wait state transition
// to eliminate the TOCTOU race between checking if a process has exited and
// sending it a signal. Without this guard, the OS could reuse the PID between
// the check and the signal, causing SIGTERM to hit an unrelated process.
type processGuard struct {
	mu     sync.Mutex
	exited bool
	proc   *os.Process
	done   chan struct{}
}

// newProcessGuard creates a guard for the given process. The caller must call
// WaitAndMark exactly once after cmd.Wait() returns.
func newProcessGuard(proc *os.Process) *processGuard {
	return &processGuard{
		proc: proc,
		done: make(chan struct{}),
	}
}

// Signal sends sig to the guarded process if it has not yet exited. Returns
// true if the signal was successfully sent. The mutex ensures that the exited
// flag cannot change between the check and the signal delivery.
func (g *processGuard) Signal(sig os.Signal) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.exited {
		return false
	}
	if err := g.proc.Signal(sig); err != nil {
		return false
	}
	return true
}

// WaitAndMark marks the process as exited and closes the done channel. Should
// be called immediately after cmd.Wait() returns. Safe to call multiple times;
// subsequent calls are no-ops.
func (g *processGuard) WaitAndMark() {
	g.mu.Lock()
	if g.exited {
		g.mu.Unlock()
		return
	}
	g.exited = true
	g.mu.Unlock()
	close(g.done)
}

// Done returns a channel that is closed when the process has exited (after
// WaitAndMark is called).
func (g *processGuard) Done() <-chan struct{} {
	return g.done
}
