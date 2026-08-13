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

// Exit reasons stored on ptySession.closeReason and reported by
// Attachment.ExitReason() after the output channel closes. The WS handler
// maps these to WebSocket close codes so the frontend can distinguish an
// explicit server-side Kill (e.g. DeleteTab) from a backend crash or a
// benign detach.
const (
	ExitReasonKilled   = "killed"   // explicit PTYManager.Kill
	ExitReasonExited   = "exited"   // child process exited on its own
	ExitReasonShutdown = "shutdown" // manager-wide Shutdown
)

// attachBufferSize is the capacity of an Attachment's output channel.
// Non-blocking sends from the drain goroutine mean a slow WebSocket drops
// frames rather than back-pressuring the shell. The ring buffer always has
// the ground truth.
const attachBufferSize = 64

// localAttachment is the in-process Attachment implementation. It wraps the
// session's PTY fd directly. An Attachment interface exists in source.go so
// a future gRPC-backed implementation can plug into the WS handler unchanged.
type localAttachment struct {
	connID     string
	pty        *os.File
	output     chan []byte
	scrollback []byte
	// firstScreenLine is the durable coordinate of the first row rendered by
	// the live xterm screen at attach time. History rows stop immediately
	// before it, preventing replay/live double-rendering.
	firstScreenLine uint64
	// session is held so ExitReason can report *why* the output channel
	// closed (the reason is stored on the session). Keeping a pointer after
	// close() is safe — closeReason is written before the channel is closed,
	// and the attachment is only consulted by a goroutine that already
	// observed the close.
	session *ptySession
}

func (a *localAttachment) ConnID() string        { return a.connID }
func (a *localAttachment) Output() <-chan []byte { return a.output }

// WriteInput writes keystrokes to the shared PTY fd. Multiple attachments
// share the same *os.File; POSIX guarantees concurrent write(2) calls up
// to PIPE_BUF (4096 bytes) are atomic, and terminal keystrokes are far
// below that, so no mutex is needed on this path.
func (a *localAttachment) WriteInput(p []byte) (int, error) { return a.pty.Write(p) }
func (a *localAttachment) Scrollback() []byte               { return a.scrollback }
func (a *localAttachment) Resize(_ string, cols, rows uint16) error {
	if a.session != nil {
		return a.session.resize(cols, rows)
	}
	return pty.Setsize(a.pty, &pty.Winsize{Cols: cols, Rows: rows})
}

func (a *localAttachment) FirstScreenLine() uint64 { return a.firstScreenLine }
func (a *localAttachment) RecordingAvailable() bool {
	return a.session != nil && a.session.recorder != nil
}

// ExitReason returns the reason the owning session closed, or "" if the
// session is still live (or was never given a reason). Only meaningful
// after Output() has been observed closed.
func (a *localAttachment) ExitReason() string {
	if a.session == nil {
		return ""
	}
	if v := a.session.closeReason.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ptySession owns the PTY fd, child process, scrollback ring, and the set
// of concurrent attachments (multi-client) for one (workspace, session)
// pair. Each attachment is a separate WebSocket / viewer that sees the
// same output and can write input into the shared PTY.
type ptySession struct {
	key           SessionKey
	pty           *os.File
	cmd           *exec.Cmd
	scrollback    *ringBuffer
	recorder      *SessionRecorder
	recordingCols uint16
	recordingRows uint16
	createdAt     int64 // unix nanos

	lastOutput atomic.Int64 // unix nanos, updated by drain

	attachMu sync.Mutex
	attaches map[string]*attachmentState // keyed by connID

	killMu    sync.Mutex
	killTimer *time.Timer

	closeOnce   sync.Once
	closeReason atomic.Value // string; stored before the output channels close
	done        chan struct{}
}

// attachmentState is the internal mutable half of an Attachment.
//
// Concurrency: drain fans output to every attachment outside attachMu, so a
// Detach / Kill can race with an in-flight send. Guard the channel with an
// RWMutex — RLock per send (multiple senders allowed in parallel for
// different attachments), Lock to close (waits for any in-flight send,
// then closes the channel under the flag set). This also replaces the
// old recover-on-send-to-closed-channel hack, which worked at runtime but
// tripped the race detector.
type attachmentState struct {
	connID  string
	ch      chan []byte
	closeMu sync.RWMutex
	closed  bool // guarded by closeMu
}

func newPtySession(key SessionKey, f *os.File, cmd *exec.Cmd, recorder *SessionRecorder) *ptySession {
	session := &ptySession{
		key:        key,
		pty:        f,
		cmd:        cmd,
		scrollback: newRingBuffer(defaultRingCapacity),
		recorder:   recorder,
		createdAt:  time.Now().UnixNano(),
		attaches:   make(map[string]*attachmentState),
		done:       make(chan struct{}),
	}
	if recorder != nil {
		session.recordingCols = recorder.startMeta.Cols
		session.recordingRows = recorder.startMeta.Rows
		recorder.setReplaySource(session.recordingReplayBaseline)
	}
	return session
}

func (s *ptySession) recordingReplayBaseline() recorderReplayBaseline {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	checkpoint, body := s.scrollback.ReplaySnapshot()
	return recorderReplayBaseline{
		checkpoint: checkpoint, body: body,
		throughSequence: s.recorder.enqueuedSequence(),
		cols:            s.recordingCols, rows: s.recordingRows,
	}
}

// drain reads from the PTY forever: bytes go into the ring buffer and a
// best-effort copy is fanned out to every current attachment. Exits when
// the PTY returns an error (child exit, fd close). On exit it asks the
// manager to clean up the session unless close() has already marked it
// done.
func (s *ptySession) drain(m *PTYManager) {
	buf := make([]byte, realtime.TerminalReadBufSize)
	// Reused across iterations to avoid per-chunk allocation. Sized once
	// to a typical attachment count; grows naturally if needed.
	snapshot := make([]*attachmentState, 0, 4)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			snapshot = s.publishOutput(chunk, snapshot)
			for _, st := range snapshot {
				st.send(chunk)
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

// publishOutput advances the replay ring, durable emulator, and live viewer
// set under one lock. attachNew takes the same lock while sampling all three,
// so a chunk is either entirely before the attach snapshot or entirely after
// it, never both replayed and delivered live.
func (s *ptySession) publishOutput(chunk []byte, scratch []*attachmentState) []*attachmentState {
	s.attachMu.Lock()
	s.scrollback.Append(chunk)
	if s.recorder != nil {
		s.recorder.Append(chunk)
	}
	s.lastOutput.Store(time.Now().UnixNano())

	scratch = scratch[:0]
	for _, st := range s.attaches {
		scratch = append(scratch, st)
	}
	s.attachMu.Unlock()
	return scratch
}

func (s *ptySession) resize(cols, rows uint16) error {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	err := pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
	if err == nil && s.recorder != nil {
		s.recordingCols, s.recordingRows = cols, rows
		s.recorder.Resize(cols, rows)
	}
	return err
}

// attachNew adds a fresh attachment to the session without disturbing any
// existing ones (multi-client). Returns a localAttachment preloaded with
// replay bytes so the new client starts from the same grid state as the
// existing ones: an emulator-rendered screen when the session records, or
// the raw replay ring for recording-less (classic mode) sessions.
//
// Returns nil if the session was closed concurrently (close() nils
// s.attaches). The caller (PTYManager.AttachSession) is expected to retry
// the lookup/spawn in that case.
//
// connID must be unique; PTYManager generates it from a monotonic counter.
// A defensive same-connID check closes any prior attachment under that ID
// — this is guard-rail against a caller bug, not an expected code path.
func (s *ptySession) attachNew(connID string) *localAttachment {
	ch := make(chan []byte, attachBufferSize)
	st := &attachmentState{connID: connID, ch: ch}

	s.attachMu.Lock()
	if s.attaches == nil {
		// Session was closed between the manager-level lookup and this
		// attach. Signal the caller to retry.
		s.attachMu.Unlock()
		close(ch)
		return nil
	}
	// Recorder-less sessions replay the raw ring (classic mode). Sessions
	// with a recorder replay an emulator-rendered screen instead: the raw
	// ring can begin mid-escape-sequence after eviction trims at arbitrary
	// byte offsets, and re-feeding up to 256KB of raw output on every
	// reattach stalls the browser main thread. All three ring samples below
	// are consistent with the barrier sequence because publishOutput appends
	// under the same attachMu this function holds.
	var replay []byte
	var modeCheckpoint, rawTail []byte
	if s.recorder == nil {
		checkpoint, body := s.scrollback.ReplaySnapshot()
		replay = composeReplay(checkpoint, body, nil)
	} else {
		modeCheckpoint = s.scrollback.CurrentModeCheckpoint()
		rawTail = s.scrollback.TailBytes(maxTrailingSequenceScan)
	}

	var firstScreenLine uint64
	var replayReady <-chan attachReplay
	if s.recorder != nil {
		replayReady = s.recorder.attachReplayBarrier()
	}
	if existing, ok := s.attaches[connID]; ok {
		existing.close()
	}
	s.attaches[connID] = st
	s.attachMu.Unlock()
	if s.recorder != nil {
		rendered := <-replayReady
		firstScreenLine = rendered.firstScreenLine
		// If the stream ends inside an escape sequence (or multi-byte rune),
		// append that fragment verbatim: the first live chunk delivered to
		// this attachment starts with the sequence's remaining bytes, which
		// would otherwise print as text.
		replay = composeReplay(modeCheckpoint, rendered.screen, incompleteTrailingSequence(rawTail))
		s.attachMu.Lock()
		if s.attaches == nil || s.attaches[connID] != st {
			s.attachMu.Unlock()
			st.close()
			return nil
		}
		s.attachMu.Unlock()
	}
	return &localAttachment{
		connID: connID, pty: s.pty, output: ch, scrollback: replay,
		session: s, firstScreenLine: firstScreenLine,
	}
}

// composeReplay concatenates the non-empty replay sections around the
// clear/home reset, returning nil when there is nothing to replay.
func composeReplay(checkpoint, body, fragment []byte) []byte {
	if len(checkpoint) == 0 && len(body) == 0 && len(fragment) == 0 {
		return nil
	}
	replay := make([]byte, 0, len(checkpoint)+len(screenResetSeq)+len(body)+len(fragment))
	replay = append(replay, checkpoint...)
	replay = append(replay, screenResetSeq...)
	replay = append(replay, body...)
	replay = append(replay, fragment...)
	return replay
}

// detach releases the attachment identified by connID and reports whether
// the session has no remaining clients. The caller (PTYManager.Detach)
// arms the grace-period kill timer only when empty=true.
func (s *ptySession) detach(connID string) (empty bool) {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	if st, ok := s.attaches[connID]; ok {
		st.close()
		delete(s.attaches, connID)
	}
	return len(s.attaches) == 0
}

// attached reports whether at least one client is currently attached.
func (s *ptySession) attached() bool {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return len(s.attaches) > 0
}

// attachmentCount returns the number of concurrent clients attached to this
// session. Used by the service layer to expose attached_clients on the tab
// DTO for multi-viewer UI treatment.
func (s *ptySession) attachmentCount() int {
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	return len(s.attaches)
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

// close tears down the PTY and child process. Idempotent. reason is
// recorded (first-writer-wins via closeOnce) so attached clients can
// distinguish an explicit kill from a child exit or shutdown via
// Attachment.ExitReason().
func (s *ptySession) close(reason string) error {
	var firstErr error
	s.closeOnce.Do(func() {
		if reason != "" {
			s.closeReason.Store(reason)
		}
		close(s.done)
		s.cancelKillTimer()

		s.attachMu.Lock()
		for _, st := range s.attaches {
			st.close()
		}
		s.attaches = nil
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
		if s.recorder != nil {
			if err := s.recorder.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	})
	return firstErr
}

// send attempts a non-blocking copy to the attachment's channel. Dropped
// frames are fine — the scrollback ring always has the ground truth and a
// reattach will replay it. Holds closeMu.RLock so a concurrent close()
// sees all in-flight sends complete before the channel is closed.
func (a *attachmentState) send(data []byte) {
	a.closeMu.RLock()
	defer a.closeMu.RUnlock()
	if a.closed {
		return
	}
	select {
	case a.ch <- data:
	default:
	}
}

// close closes the attachment's output channel exactly once so a waiting
// pump goroutine exits. Takes closeMu exclusively, which blocks briefly
// for any in-flight sends (each is non-blocking, so the wait is bounded).
func (a *attachmentState) close() {
	a.closeMu.Lock()
	defer a.closeMu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	close(a.ch)
}
