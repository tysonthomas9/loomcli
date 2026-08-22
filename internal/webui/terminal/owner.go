package terminal

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

const terminalReadBufferSize = 4096

type ownerCommand interface{}

type outputChunkCommand struct{ data []byte }
type injectOutputCommand struct{ data []byte }

type attachCommand struct {
	connID     string
	cols, rows uint16
	reply      chan *localAttachment
}

type detachCommand struct {
	connID string
	reason CloseReason
	reply  chan bool
}

type inputCommand struct {
	connID string
	data   []byte
	reply  chan bool
}

type trustedInputCommand struct {
	data  []byte
	reply chan error
}

type resizeRequestCommand struct {
	connID     string
	cols, rows uint16
	reply      chan error
}

type canonicalResizeCommand struct {
	cols, rows uint16
	reply      chan error
}

type focusCommand struct {
	connID string
	reply  chan error
}

type closeCommand struct {
	reason CloseReason
	reply  chan error
}

func (s *ptySession) readPTY(manager *PTYManager) {
	defer close(s.readerDone)
	buf := make([]byte, terminalReadBufferSize)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if !s.sendCommand(outputChunkCommand{data: chunk}) {
				return
			}
		}
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if manager != nil {
				manager.onSessionExited(s.key)
			}
			return
		}
	}
}

func (s *ptySession) runOwner() {
	defer close(s.ownerDone)
	for {
		cmd := <-s.commands
		switch command := cmd.(type) {
		case outputChunkCommand:
			s.emitOutput(command.data)
		case injectOutputCommand:
			s.emitOutput(command.data)
		case attachCommand:
			command.reply <- s.handleAttach(command)
		case detachCommand:
			s.removeSubscriber(command.connID, command.reason)
			command.reply <- len(s.subs) == 0
		case inputCommand:
			command.reply <- s.handleInput(command.connID, command.data)
		case trustedInputCommand:
			command.reply <- s.enqueueInput(command.data)
		case resizeRequestCommand:
			command.reply <- s.handleResizeRequest(command)
		case canonicalResizeCommand:
			s.applyGeometry(command.cols, command.rows)
			command.reply <- nil
		case focusCommand:
			command.reply <- s.handleFocus(command.connID)
		case closeCommand:
			command.reply <- s.handleClose(command.reason)
			return
		}
	}
}

func (s *ptySession) handleAttach(command attachCommand) *localAttachment {
	if existing := s.subs[command.connID]; existing != nil {
		s.removeSubscriber(command.connID, CloseReplaced)
	}

	data := interimInitialStateData(s.scrollback)
	initial := TerminalInitialState{
		Generation: s.generation,
		Sequence:   s.seq,
		Cols:       s.cols,
		Rows:       s.rows,
		Encoding:   "xterm-vt/1",
		Data:       data,
	}

	s.attachOrder++
	sub := newSubscriber(command.connID, command.cols, command.rows, s.attachOrder)
	s.subs[command.connID] = sub
	s.attachCount.Store(int64(len(s.subs)))
	att := &localAttachment{
		connID:  command.connID,
		initial: initial,
		sub:     sub,
		session: s,
	}
	if s.controller == "" {
		s.controller = command.connID
		s.applyInitialControllerGeometry(command.cols, command.rows)
	}
	return att
}

func (s *ptySession) applyInitialControllerGeometry(cols, rows uint16) {
	if cols == 0 || rows == 0 {
		return
	}
	s.writer.enqueueResize(cols, rows)
	s.cols = cols
	s.rows = rows
	s.emitEvent(TerminalEvent{Kind: EventResize, Cols: cols, Rows: rows})
}

func interimInitialStateData(scrollback *ringBuffer) []byte {
	checkpoint, body := scrollback.ReplaySnapshot()
	if len(checkpoint) == 0 && len(body) == 0 {
		return nil
	}
	data := make([]byte, 0, len(checkpoint)+len(screenResetSeq)+len(body))
	data = append(data, checkpoint...)
	data = append(data, screenResetSeq...)
	data = append(data, body...)
	return data
}

func (s *ptySession) handleInput(connID string, data []byte) bool {
	if s.subs[connID] == nil || s.controller != connID {
		return false
	}
	return s.enqueueInput(data) == nil
}

func (s *ptySession) enqueueInput(data []byte) error {
	if s.writer.enqueueInput(data) {
		return nil
	}
	notice, _ := json.Marshal(map[string]string{"code": "input_dropped"})
	s.emitEvent(TerminalEvent{Kind: EventNotice, Data: notice})
	return errors.New("terminal writer queue full")
}

func (s *ptySession) handleResizeRequest(command resizeRequestCommand) error {
	sub := s.subs[command.connID]
	if sub == nil {
		return errAttachmentClosed
	}
	sub.cols = command.cols
	sub.rows = command.rows
	if s.controller == command.connID {
		s.applyGeometry(command.cols, command.rows)
	}
	return nil
}

func (s *ptySession) handleFocus(connID string) error {
	sub := s.subs[connID]
	if sub == nil {
		return errAttachmentClosed
	}
	s.controller = connID
	s.applyGeometry(sub.cols, sub.rows)
	return nil
}

func (s *ptySession) applyGeometry(cols, rows uint16) {
	if cols == 0 || rows == 0 || (cols == s.cols && rows == s.rows) {
		return
	}
	s.writer.enqueueResize(cols, rows)
	s.cols = cols
	s.rows = rows
	s.emitEvent(TerminalEvent{Kind: EventResize, Cols: cols, Rows: rows})
}

func (s *ptySession) emitOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	s.scrollback.Append(data)
	s.lastOutput.Store(time.Now().UnixNano())
	s.emitEvent(TerminalEvent{Kind: EventOutput, Data: data})
}

func (s *ptySession) emitEvent(event TerminalEvent) {
	s.seq++
	event.Sequence = s.seq
	controllerLost := false
	for connID, sub := range s.subs {
		if sub.enqueue(event) {
			continue
		}
		sub.closeImmediate(CloseSlowConsumer)
		delete(s.subs, connID)
		if s.controller == connID {
			controllerLost = true
			s.controller = ""
		}
	}
	s.attachCount.Store(int64(len(s.subs)))
	if controllerLost {
		s.handoffController()
	}
}

func (s *ptySession) removeSubscriber(connID string, reason CloseReason) {
	sub := s.subs[connID]
	if sub == nil {
		return
	}
	delete(s.subs, connID)
	sub.closeImmediate(reason)
	s.attachCount.Store(int64(len(s.subs)))
	if s.controller == connID {
		s.controller = ""
		s.handoffController()
	}
}

func (s *ptySession) handoffController() {
	var newest *subscriber
	for _, sub := range s.subs {
		if newest == nil || sub.attachedAt > newest.attachedAt {
			newest = sub
		}
	}
	if newest == nil {
		return
	}
	s.controller = newest.connID
	s.applyGeometry(newest.cols, newest.rows)
}

func (s *ptySession) handleClose(reason CloseReason) error {
	// Close consumes a server-event sequence. A subscriber may be blocked, so
	// teardown closes it immediately; Slice B uses the authoritative WebSocket
	// close code when the informational EventClose cannot be delivered.
	s.seq++
	for _, sub := range s.subs {
		sub.closeImmediate(reason)
	}
	s.subs = nil
	s.controller = ""
	s.attachCount.Store(0)
	s.writer.close()
	close(s.done)

	var closeErr error
	if s.pty != nil {
		if err := s.pty.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
			closeErr = err
		}
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	return closeErr
}
