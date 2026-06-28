package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

// TerminalHostServer owns PTY sessions in a process outside loom serve. Serve
// instances connect through TerminalHostClient, so browser reloads and serve
// restarts only detach clients; the child PTYs remain in this host.
type TerminalHostServer struct {
	SocketPath string
	Manager    *MultiPTYManager
	Logger     *slog.Logger
}

func NewTerminalHostServer(socketPath, command string, maxSessions int, logger *slog.Logger) *TerminalHostServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &TerminalHostServer{
		SocketPath: socketPath,
		Manager:    NewMultiPTYManager(command, maxSessions),
		Logger:     logger,
	}
}

func (s *TerminalHostServer) Serve(ctx context.Context) error {
	if s.Manager == nil {
		return errors.New("terminal host manager is nil")
	}
	if s.SocketPath == "" {
		return errors.New("terminal host socket path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0755); err != nil {
		return fmt.Errorf("create terminal host socket dir: %w", err)
	}
	_ = os.Remove(s.SocketPath)
	ln, err := net.Listen("unix", s.SocketPath)
	if err != nil {
		return fmt.Errorf("listen terminal host socket: %w", err)
	}
	defer func() { _ = ln.Close() }()
	defer func() { _ = os.Remove(s.SocketPath) }()
	defer func() { _ = s.Manager.Close() }()

	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(true)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *TerminalHostServer) handleConn(conn net.Conn) {
	dec := json.NewDecoder(conn)
	var req terminalHostRequest
	if err := dec.Decode(&req); err != nil {
		_ = conn.Close()
		return
	}
	if req.ProtocolVersion != TerminalHostProtocolVersion {
		writeTerminalHostResponse(conn, terminalHostResponse{
			OK:        false,
			Error:     fmt.Sprintf("unsupported terminal host protocol %d", req.ProtocolVersion),
			ErrorCode: terminalHostErrUnknown,
		})
		_ = conn.Close()
		return
	}
	if req.Op == terminalHostOpAttach {
		s.handleAttach(conn, dec, req)
		return
	}
	resp := s.handleUnary(req)
	writeTerminalHostResponse(conn, resp)
	_ = conn.Close()
}

func (s *TerminalHostServer) handleUnary(req terminalHostRequest) terminalHostResponse {
	resp := terminalHostResponse{OK: true}
	switch req.Op {
	case terminalHostOpPing:
	case terminalHostOpKill:
		resp = responseForErr(s.Manager.Kill(req.Key))
	case terminalHostOpHasSession:
		resp.Bool = s.Manager.HasSession(req.Key)
	case terminalHostOpSessionClosed:
		resp.Bool = s.Manager.SessionClosed(req.Key)
	case terminalHostOpAttachmentCount:
		resp.Count = s.Manager.AttachmentCount(req.Key)
	case terminalHostOpSessionCount:
		resp.Count = s.Manager.SessionCount()
	case terminalHostOpSessionCountFor:
		resp.Count = s.Manager.SessionCountFor(req.WorkspaceID)
	case terminalHostOpMaxSessions:
		resp.MaxSessions = s.Manager.MaxSessions()
	case terminalHostOpRegister:
		resp = responseForErr(s.Manager.EnsureRegistered(req.WorkspaceID, req.Path))
	case terminalHostOpEnsureSession:
		created, err := s.Manager.EnsureSession(req.Key, req.Cols, req.Rows, req.Argv)
		resp = responseForErr(err)
		resp.Created = created
	case terminalHostOpWriteToSession:
		resp = responseForErr(s.Manager.WriteToSession(req.Key, req.Data))
	default:
		resp.OK = false
		resp.Error = "unknown terminal host operation"
		resp.ErrorCode = terminalHostErrUnknown
	}
	return resp
}

func (s *TerminalHostServer) handleAttach(conn net.Conn, dec *json.Decoder, req terminalHostRequest) {
	enc := json.NewEncoder(conn)
	att, reattached, err := s.Manager.AttachSession(req.Key, req.Cols, req.Rows, req.Launch)
	if err != nil {
		_ = enc.Encode(responseForErr(err))
		_ = conn.Close()
		return
	}
	connID := att.ConnID()
	if err := enc.Encode(&terminalHostResponse{
		ProtocolVersion: TerminalHostProtocolVersion,
		OK:              true,
		ConnID:          connID,
		Reattached:      reattached,
		Scrollback:      att.Scrollback(),
	}); err != nil {
		s.Manager.Detach(req.Key, connID)
		_ = conn.Close()
		return
	}

	controlDone := make(chan struct{})
	go s.readAttachControl(dec, req.Key, connID, att, controlDone)
	s.writeAttachOutput(conn, enc, att, controlDone)
}

func (s *TerminalHostServer) readAttachControl(dec *json.Decoder, key SessionKey, connID string, att Attachment, controlDone chan<- struct{}) {
	defer close(controlDone)
	defer s.Manager.Detach(key, connID)
	for {
		var msg terminalHostStreamMessage
		if err := dec.Decode(&msg); err != nil {
			return
		}
		switch msg.Op {
		case terminalHostOpInput:
			_, _ = att.WriteInput(msg.Data)
		case terminalHostOpResize:
			if len(msg.Data) == 4 {
				cols := uint16(msg.Data[0])<<8 | uint16(msg.Data[1])
				rows := uint16(msg.Data[2])<<8 | uint16(msg.Data[3])
				_ = att.Resize(connID, cols, rows)
			}
		case terminalHostOpDetach:
			return
		}
	}
}

func (s *TerminalHostServer) writeAttachOutput(conn net.Conn, enc *json.Encoder, att Attachment, controlDone <-chan struct{}) {
	for {
		select {
		case chunk, ok := <-att.Output():
			if !ok {
				_ = enc.Encode(&terminalHostStreamMessage{Op: terminalHostOpClosed, ExitReason: att.ExitReason()})
				_ = conn.Close()
				<-controlDone
				return
			}
			if err := enc.Encode(&terminalHostStreamMessage{Op: terminalHostOpOutput, Data: chunk}); err != nil {
				_ = conn.Close()
				<-controlDone
				return
			}
		case <-controlDone:
			_ = conn.Close()
			return
		}
	}
}

func writeTerminalHostResponse(w io.Writer, resp terminalHostResponse) {
	resp.ProtocolVersion = TerminalHostProtocolVersion
	_ = json.NewEncoder(w).Encode(&resp)
}

func responseForErr(err error) terminalHostResponse {
	if err == nil {
		return terminalHostResponse{ProtocolVersion: TerminalHostProtocolVersion, OK: true}
	}
	return terminalHostResponse{
		ProtocolVersion: TerminalHostProtocolVersion,
		OK:              false,
		Error:           err.Error(),
		ErrorCode:       terminalHostErrorCode(err),
	}
}
