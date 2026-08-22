package terminal

import (
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// screenResetSeq clears the terminal and homes the cursor. It follows any
// ring-buffer mode checkpoint in an interim Phase 1 initial state.
var screenResetSeq = []byte("\x1b[2J\x1b[H")

var errAttachmentClosed = errors.New("terminal attachment closed")

type localAttachment struct {
	connID  string
	initial TerminalInitialState
	sub     *subscriber
	session *ptySession
}

func (a *localAttachment) ConnID() string                     { return a.connID }
func (a *localAttachment) InitialState() TerminalInitialState { return cloneInitialState(a.initial) }
func (a *localAttachment) Output() <-chan TerminalEvent       { return a.sub.output }
func (a *localAttachment) CloseReason() CloseReason           { return a.sub.reason() }

func (a *localAttachment) WriteInput(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	reply := make(chan error, 1)
	cmd := inputCommand{connID: a.connID, data: append([]byte(nil), p...), reply: reply}
	if !a.session.sendCommand(cmd) {
		return 0, errAttachmentClosed
	}
	select {
	case err := <-reply:
		if err != nil {
			return 0, err
		}
		return len(p), nil
	case <-a.session.done:
		return 0, errAttachmentClosed
	}
}

func (a *localAttachment) RequestResize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("terminal dimensions must be non-zero")
	}
	reply := make(chan error, 1)
	if !a.session.sendCommand(resizeRequestCommand{
		connID: a.connID,
		cols:   cols,
		rows:   rows,
		reply:  reply,
	}) {
		return errAttachmentClosed
	}
	select {
	case err := <-reply:
		return err
	case <-a.session.done:
		return errAttachmentClosed
	}
}

func (a *localAttachment) Focus() error {
	reply := make(chan error, 1)
	if !a.session.sendCommand(focusCommand{connID: a.connID, reply: reply}) {
		return errAttachmentClosed
	}
	select {
	case err := <-reply:
		return err
	case <-a.session.done:
		return errAttachmentClosed
	}
}

func cloneInitialState(in TerminalInitialState) TerminalInitialState {
	out := in
	out.Data = append([]byte(nil), in.Data...)
	return out
}

// ptySession owns all mutable transport state. Only runOwner reads or writes
// the owner-state fields; atomics below are observation points for manager
// diagnostics and idle reaping.
type ptySession struct {
	key       SessionKey
	pty       terminalPTY
	cmd       *exec.Cmd
	createdAt int64

	commands   chan ownerCommand
	done       chan struct{}
	ownerDone  chan struct{}
	readerDone chan struct{}
	writer     *writerFIFO
	writerDone chan struct{}

	seq         uint64
	generation  Generation
	cols        uint16
	rows        uint16
	controller  string
	attachOrder uint64
	subs        map[string]*subscriber
	scrollback  *ringBuffer

	lastOutput  atomic.Int64
	attachCount atomic.Int64

	killMu    sync.Mutex
	killTimer *time.Timer

	closeOnce sync.Once
	closeErr  error
}

func newPtySession(key SessionKey, device terminalPTY, cmd *exec.Cmd, cols, rows uint16) (*ptySession, error) {
	generation, err := makeGeneration()
	if err != nil {
		return nil, err
	}
	s := &ptySession{
		key:        key,
		pty:        device,
		cmd:        cmd,
		createdAt:  time.Now().UnixNano(),
		commands:   make(chan ownerCommand),
		done:       make(chan struct{}),
		ownerDone:  make(chan struct{}),
		readerDone: make(chan struct{}),
		generation: generation,
		cols:       cols,
		rows:       rows,
		subs:       make(map[string]*subscriber),
		scrollback: newRingBuffer(defaultRingCapacity),
	}
	s.writer = newWriterFIFO(device)
	return s, nil
}

func (s *ptySession) start(manager *PTYManager) {
	s.writerDone = make(chan struct{})
	go func() { defer close(s.writerDone); s.writer.run() }()
	go s.runOwner()
	go s.readPTY(manager)
}

func (s *ptySession) sendCommand(cmd ownerCommand) bool {
	select {
	case s.commands <- cmd:
		return true
	case <-s.done:
		return false
	}
}

func (s *ptySession) attachNew(connID string, cols, rows uint16) *localAttachment {
	reply := make(chan *localAttachment, 1)
	if !s.sendCommand(attachCommand{connID: connID, cols: cols, rows: rows, reply: reply}) {
		return nil
	}
	select {
	case att := <-reply:
		return att
	case <-s.done:
		return nil
	}
}

func (s *ptySession) detach(connID string) bool {
	reply := make(chan bool, 1)
	if !s.sendCommand(detachCommand{connID: connID, reason: CloseReplaced, reply: reply}) {
		return true
	}
	select {
	case empty := <-reply:
		return empty
	case <-s.done:
		return true
	}
}

func (s *ptySession) resizeCanonical(cols, rows uint16) error {
	reply := make(chan error, 1)
	if !s.sendCommand(canonicalResizeCommand{cols: cols, rows: rows, reply: reply}) {
		return errAttachmentClosed
	}
	select {
	case err := <-reply:
		return err
	case <-s.done:
		return errAttachmentClosed
	}
}

func (s *ptySession) writeTrusted(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	reply := make(chan error, 1)
	cmd := trustedInputCommand{data: append([]byte(nil), p...), reply: reply}
	if !s.sendCommand(cmd) {
		return errAttachmentClosed
	}
	select {
	case err := <-reply:
		return err
	case <-s.done:
		return errAttachmentClosed
	}
}

func (s *ptySession) attached() bool            { return s.attachCount.Load() > 0 }
func (s *ptySession) attachmentCount() int      { return int(s.attachCount.Load()) }
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

// close serializes teardown through the owner. The first reason wins.
func (s *ptySession) close(reason string) error {
	s.closeOnce.Do(func() {
		reply := make(chan error, 1)
		if s.sendCommand(closeCommand{reason: CloseReason(reason), reply: reply}) {
			s.closeErr = <-reply
		}
		<-s.ownerDone
	})
	return s.closeErr
}
