package terminal

import (
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// screenResetSeq clears the terminal and homes the cursor. Emitted at the
// start of a scrollback replay so the client's grid doesn't render the old
// contents interleaved with the replayed bytes. Harmless on an initial
// attach against an empty session.
var screenResetSeq = []byte("\x1b[2J\x1b[H")

// attachBufferSize is the capacity of an Attachment's output channel.
// Non-blocking sends from the drain goroutine mean a slow WebSocket drops
// frames rather than back-pressuring the shell. The ring buffer always has
// the ground truth.
const attachBufferSize = 64

// Attachment is the handle returned to the WebSocket handler. The handler
// reads output frames from Output() and writes input to WriteInput(). Calling
// Detach() (or letting Output()'s channel close) releases the attachment.
type Attachment struct {
	connID     string
	pty        *os.File
	output     chan []byte // drain → WS; closed when the attachment ends
	scrollback []byte      // replay bytes, populated at attach time
	reattach   bool
}

// ConnID returns the opaque per-attach identifier used by the resize path.
func (a *Attachment) ConnID() string { return a.connID }

// Output returns the channel from which the WS handler reads live output.
// Closed when the attachment is released (either because the session was
// killed or because a newer WebSocket replaced this one).
func (a *Attachment) Output() <-chan []byte { return a.output }

// WriteInput sends user input bytes to the underlying PTY. Thread-safe w.r.t.
// the drain goroutine because read and write ends of a PTY are independent.
func (a *Attachment) WriteInput(p []byte) (int, error) { return a.pty.Write(p) }

// Scrollback returns the reset-sequence + ring contents the handler should
// emit before live output. nil on the first attach to a brand-new session.
func (a *Attachment) Scrollback() []byte { return a.scrollback }

// Reattach reports whether this attachment is to a pre-existing session
// (true) or a freshly spawned one (false).
func (a *Attachment) Reattach() bool { return a.reattach }

// ptySession owns the PTY fd, child process, scrollback ring, and current
// attachment for one (workspace, session) pair.
type ptySession struct {
	key        SessionKey
	pty        *os.File
	cmd        *exec.Cmd
	scrollback *ringBuffer
	createdAt  int64 // unix nanos

	lastOutput atomic.Int64 // unix nanos, updated by drain

	attachMu sync.Mutex
	attach   *attachmentState

	killMu    sync.Mutex
	killTimer *time.Timer

	closeOnce sync.Once
	closed    atomic.Bool
	done      chan struct{}
}

type attachmentState struct {
	connID string
	ch     chan []byte
	closed bool
	mu     sync.Mutex
}

func newPtySession(key SessionKey, f *os.File, cmd *exec.Cmd) *ptySession {
	return &ptySession{
		key:        key,
		pty:        f,
		cmd:        cmd,
		scrollback: newRingBuffer(defaultRingCapacity),
		createdAt:  time.Now().UnixNano(),
		done:       make(chan struct{}),
	}
}

// drain reads from the PTY forever: bytes go into the ring buffer, and a
// best-effort copy is sent to the current attachment. Exits when the PTY
// returns an error (child exit, fd close). On exit it asks the manager to
// clean up the session.
func (s *ptySession) drain(m *PTYManager, key SessionKey) {
	buf := make([]byte, 4096)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.scrollback.Write(chunk)
			s.lastOutput.Store(time.Now().UnixNano())

			s.attachMu.Lock()
			cur := s.attach
			s.attachMu.Unlock()
			if cur != nil {
				cur.send(chunk)
			}
		}
		if err != nil {
			// PTY read error = child exited or fd closed. Tell the
			// manager so the session is removed from the map.
			if !s.closed.Load() {
				m.onSessionExited(key)
			}
			return
		}
	}
}

// attachNew replaces any existing attachment with a fresh one. Returns an
// Attachment preloaded with the replay bytes (reset escape + current
// scrollback) for an existing session.
func (s *ptySession) attachNew(connID string) *Attachment {
	ch := make(chan []byte, attachBufferSize)
	st := &attachmentState{connID: connID, ch: ch}

	s.attachMu.Lock()
	old := s.attach
	s.attach = st
	s.attachMu.Unlock()

	if old != nil {
		old.close()
	}

	// Build replay: reset escape + scrollback contents. Only the bytes
	// already in the ring at this moment — subsequent live output will
	// arrive through the channel naturally.
	var replay []byte
	if body := s.scrollback.Bytes(); len(body) > 0 {
		replay = make([]byte, 0, len(screenResetSeq)+len(body))
		replay = append(replay, screenResetSeq...)
		replay = append(replay, body...)
	}

	return &Attachment{
		connID:     connID,
		pty:        s.pty,
		output:     ch,
		scrollback: replay,
	}
}

// detach releases the attachment identified by connID and reports whether
// the session is now fully detached.
func (s *ptySession) detach(connID string) bool {
	s.attachMu.Lock()
	if s.attach != nil && s.attach.connID == connID {
		s.attach.close()
		s.attach = nil
	}
	empty := s.attach == nil
	s.attachMu.Unlock()
	return empty
}

// attachedCount returns 0 or 1 under the single-attach-per-session model.
func (s *ptySession) attachedCount() int {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attach != nil {
		return 1
	}
	return 0
}

func (s *ptySession) lastOutputUnixNano() int64 { return s.lastOutput.Load() }
func (s *ptySession) createdUnixNano() int64    { return s.createdAt }

func (s *ptySession) armKillTimer(after time.Duration, fire func()) {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	if s.killTimer != nil {
		s.killTimer.Stop()
	}
	s.killTimer = time.AfterFunc(after, fire)
}

func (s *ptySession) cancelKillTimer() {
	s.killMu.Lock()
	defer s.killMu.Unlock()
	if s.killTimer != nil {
		s.killTimer.Stop()
		s.killTimer = nil
	}
}

// close tears down the PTY and child process. Idempotent. The reason is
// retained for logs; callers can inspect it via session state if ever needed.
func (s *ptySession) close(reason string) error {
	var firstErr error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.cancelKillTimer()

		s.attachMu.Lock()
		if s.attach != nil {
			s.attach.close()
			s.attach = nil
		}
		s.attachMu.Unlock()

		if s.pty != nil {
			if err := s.pty.Close(); err != nil {
				firstErr = err
			}
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
		}
		close(s.done)
		_ = reason
	})
	return firstErr
}

// send attempts a non-blocking copy to the attachment's channel. If the
// channel is full the frame is dropped — the scrollback ring always has a
// ground-truth copy, and a reattach will replay.
func (a *attachmentState) send(data []byte) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	ch := a.ch
	a.mu.Unlock()

	select {
	case ch <- data:
	default:
		// Slow consumer; drop.
	}
}

// close marks the attachment closed and closes its channel so a pump
// goroutine waiting on Output() exits.
func (a *attachmentState) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	close(a.ch)
}
