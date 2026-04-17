package terminal

import (
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// screenResetSeq clears the terminal and homes the cursor. Emitted at the
// start of a scrollback replay so the client's grid doesn't render old
// contents interleaved with the replayed bytes.
var screenResetSeq = []byte("\x1b[2J\x1b[H")

// attachBufferSize is the capacity of an Attachment's output channel.
// Non-blocking sends from the drain goroutine mean a slow WebSocket drops
// frames rather than back-pressuring the shell. The ring buffer always has
// the ground truth.
const attachBufferSize = 64

// Attachment is the handle returned to the WebSocket handler. The handler
// reads output frames from Output() and writes input to WriteInput(). Calling
// manager.Detach(connID) releases the attachment.
type Attachment struct {
	connID     string
	pty        *os.File
	output     chan []byte
	scrollback []byte
}

// ConnID returns the opaque per-attach identifier used by the resize path.
func (a *Attachment) ConnID() string { return a.connID }

// Output returns the channel from which the WS handler reads live output.
// Closed when the attachment is released.
func (a *Attachment) Output() <-chan []byte { return a.output }

// WriteInput sends user input bytes to the underlying PTY. Thread-safe w.r.t.
// the drain goroutine because read and write ends of a PTY are independent.
func (a *Attachment) WriteInput(p []byte) (int, error) { return a.pty.Write(p) }

// Scrollback returns the reset-sequence + ring contents the handler should
// emit before live output. nil on the first attach to a brand-new session.
func (a *Attachment) Scrollback() []byte { return a.scrollback }

// Resize satisfies realtime.Resizer so the WS handler can pass the
// Attachment directly to WSToPTY; no per-session lookup table needed.
// The connID argument is ignored because an attachment already knows its PTY.
func (a *Attachment) Resize(_ string, cols, rows uint16) error {
	return pty.Setsize(a.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

var _ realtime.Resizer = (*Attachment)(nil)

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
	done      chan struct{}
}

// attachmentState is the internal mutable half of an Attachment. `ch` is
// owned here and closed exactly once via closeOnce; callers detect the
// closed state by the channel being closed, not a separate bool.
type attachmentState struct {
	connID    string
	ch        chan []byte
	closeOnce sync.Once
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

// drain reads from the PTY forever: bytes go into the ring buffer and a
// best-effort copy is sent to the current attachment. Exits when the PTY
// returns an error (child exit, fd close). On exit it asks the manager to
// clean up the session unless close() has already marked it done.
func (s *ptySession) drain(m *PTYManager) {
	buf := make([]byte, realtime.TerminalReadBufSize)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.scrollback.Append(chunk)
			s.lastOutput.Store(time.Now().UnixNano())

			s.attachMu.Lock()
			cur := s.attach
			s.attachMu.Unlock()
			if cur != nil {
				cur.send(chunk)
			}
		}
		if err != nil {
			select {
			case <-s.done:
				// close() already tore the session down.
			default:
				m.onSessionExited(s.key)
			}
			return
		}
	}
}

// attachNew replaces any existing attachment with a fresh one. Returns an
// Attachment preloaded with the replay bytes (reset escape + current
// scrollback) if this is a reattach.
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

	var replay []byte
	if body := s.scrollback.Bytes(); len(body) > 0 {
		replay = make([]byte, 0, len(screenResetSeq)+len(body))
		replay = append(replay, screenResetSeq...)
		replay = append(replay, body...)
	}

	return &Attachment{connID: connID, pty: s.pty, output: ch, scrollback: replay}
}

// detach releases the attachment identified by connID and reports whether
// the session is now fully detached.
func (s *ptySession) detach(connID string) (empty bool) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if s.attach != nil && s.attach.connID == connID {
		s.attach.close()
		s.attach = nil
	}
	return s.attach == nil
}

// attached reports whether a WebSocket is currently attached.
func (s *ptySession) attached() bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return s.attach != nil
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

// close tears down the PTY and child process. Idempotent.
func (s *ptySession) close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		close(s.done)
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
	})
	return firstErr
}

// send attempts a non-blocking copy to the attachment's channel. Dropped
// frames are fine — the scrollback ring always has the ground truth and a
// reattach will replay it.
func (a *attachmentState) send(data []byte) {
	defer func() {
		// Send on a closed channel panics; recover rather than guarding
		// every send under a mutex. Close happens exactly once, so this
		// is a bounded, rare event (at most one panic per attachment).
		_ = recover()
	}()
	select {
	case a.ch <- data:
	default:
	}
}

// close closes the attachment's output channel exactly once so a waiting
// pump goroutine exits.
func (a *attachmentState) close() {
	a.closeOnce.Do(func() { close(a.ch) })
}
