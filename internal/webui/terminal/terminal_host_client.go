package terminal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

const terminalHostDialTimeout = 2 * time.Second

// TerminalHostClient is a PTYSource backed by the local terminal-host Unix
// socket. Closing the client detaches this serve process's live WebSocket
// attachments; it does not kill sessions in the host.
type TerminalHostClient struct {
	socketPath string
	max        int

	mu          sync.Mutex
	attachments map[string]*hostAttachment
	closed      bool
}

func NewTerminalHostClient(socketPath string, maxSessions int) *TerminalHostClient {
	if maxSessions <= 0 {
		maxSessions = defaultPTYMaxSessions
	}
	return &TerminalHostClient{
		socketPath:  socketPath,
		max:         maxSessions,
		attachments: make(map[string]*hostAttachment),
	}
}

func (c *TerminalHostClient) AttachSession(key SessionKey, cols, rows uint16, launch *tabmeta.LaunchSpec) (Attachment, bool, error) {
	conn, err := c.dial()
	if err != nil {
		return nil, false, err
	}
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	req := terminalHostRequest{
		ProtocolVersion: TerminalHostProtocolVersion,
		Op:              terminalHostOpAttach,
		Key:             key,
		Cols:            cols,
		Rows:            rows,
		Launch:          launch,
	}
	if err := enc.Encode(&req); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("terminal host attach request: %w", err)
	}
	var resp terminalHostResponse
	if err := dec.Decode(&resp); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("terminal host attach response: %w", err)
	}
	if !resp.OK {
		_ = conn.Close()
		return nil, false, terminalHostErrorFromCode(resp.ErrorCode, resp.Error)
	}
	att := &hostAttachment{
		connID:     resp.ConnID,
		key:        key,
		conn:       conn,
		enc:        enc,
		dec:        dec,
		output:     make(chan []byte, attachBufferSize),
		scrollback: resp.Scrollback,
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = att.close()
		return nil, false, ErrPTYManagerClosed
	}
	c.attachments[att.connID] = att
	c.mu.Unlock()

	go att.readLoop(func(connID string) {
		c.mu.Lock()
		delete(c.attachments, connID)
		c.mu.Unlock()
	})
	return att, resp.Reattached, nil
}

func (c *TerminalHostClient) Detach(_ SessionKey, connID string) {
	c.mu.Lock()
	att := c.attachments[connID]
	delete(c.attachments, connID)
	c.mu.Unlock()
	if att != nil {
		_ = att.detach()
	}
}

func (c *TerminalHostClient) Kill(key SessionKey) error {
	_, err := c.call(terminalHostRequest{Op: terminalHostOpKill, Key: key})
	return err
}

func (c *TerminalHostClient) HasSession(key SessionKey) bool {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpHasSession, Key: key})
	return err == nil && resp.Bool
}

func (c *TerminalHostClient) SessionClosed(key SessionKey) bool {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpSessionClosed, Key: key})
	return err == nil && resp.Bool
}

func (c *TerminalHostClient) AttachmentCount(key SessionKey) int {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpAttachmentCount, Key: key})
	if err != nil {
		return 0
	}
	return resp.Count
}

func (c *TerminalHostClient) SessionCount() int {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpSessionCount})
	if err != nil {
		return 0
	}
	return resp.Count
}

func (c *TerminalHostClient) SessionCountFor(wsID string) int {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpSessionCountFor, WorkspaceID: wsID})
	if err != nil {
		return 0
	}
	return resp.Count
}

func (c *TerminalHostClient) MaxSessions() int {
	resp, err := c.call(terminalHostRequest{Op: terminalHostOpMaxSessions})
	if err != nil || resp.MaxSessions <= 0 {
		return c.max
	}
	return resp.MaxSessions
}

func (c *TerminalHostClient) EnsureRegistered(wsID, path string) error {
	_, err := c.call(terminalHostRequest{Op: terminalHostOpRegister, WorkspaceID: wsID, Path: path})
	return err
}

func (c *TerminalHostClient) Deregister(wsID string) {
	// The terminal host intentionally has no non-destructive workspace
	// unregister operation. Explicit tab/session deletes still call Kill; serve
	// shutdown and workspace reconciliation must not tear down live terminals.
	_ = wsID
}

func (c *TerminalHostClient) EnsureSession(key SessionKey, cols, rows uint16, argv []string) (bool, error) {
	resp, err := c.call(terminalHostRequest{
		Op:   terminalHostOpEnsureSession,
		Key:  key,
		Cols: cols,
		Rows: rows,
		Argv: argv,
	})
	if err != nil {
		return false, err
	}
	return resp.Created, nil
}

func (c *TerminalHostClient) WriteToSession(key SessionKey, p []byte) error {
	_, err := c.call(terminalHostRequest{Op: terminalHostOpWriteToSession, Key: key, Data: p})
	return err
}

func (c *TerminalHostClient) GracePeriod() time.Duration { return 0 }
func (c *TerminalHostClient) IdleTimeout() time.Duration { return 0 }

func (c *TerminalHostClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	attachments := make([]*hostAttachment, 0, len(c.attachments))
	for _, att := range c.attachments {
		attachments = append(attachments, att)
	}
	c.attachments = make(map[string]*hostAttachment)
	c.mu.Unlock()

	var errs []error
	for _, att := range attachments {
		if err := att.detach(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *TerminalHostClient) Ping() error {
	_, err := c.call(terminalHostRequest{Op: terminalHostOpPing})
	return err
}

func (c *TerminalHostClient) call(req terminalHostRequest) (terminalHostResponse, error) {
	req.ProtocolVersion = TerminalHostProtocolVersion
	conn, err := c.dial()
	if err != nil {
		return terminalHostResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	if err := json.NewEncoder(conn).Encode(&req); err != nil {
		return terminalHostResponse{}, fmt.Errorf("terminal host request %q: %w", req.Op, err)
	}
	var resp terminalHostResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return terminalHostResponse{}, fmt.Errorf("terminal host response %q: %w", req.Op, err)
	}
	if !resp.OK {
		return resp, terminalHostErrorFromCode(resp.ErrorCode, resp.Error)
	}
	return resp, nil
}

func (c *TerminalHostClient) dial() (net.Conn, error) {
	if c.socketPath == "" {
		return nil, errors.New("terminal host socket path is empty")
	}
	conn, err := net.DialTimeout("unix", c.socketPath, terminalHostDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial terminal host %s: %w", c.socketPath, err)
	}
	return conn, nil
}

type hostAttachment struct {
	connID     string
	key        SessionKey
	conn       net.Conn
	enc        *json.Encoder
	dec        *json.Decoder
	output     chan []byte
	scrollback []byte
	exitReason atomic.Value

	mu     sync.Mutex
	closed bool
	once   sync.Once
}

func (a *hostAttachment) ConnID() string        { return a.connID }
func (a *hostAttachment) Output() <-chan []byte { return a.output }
func (a *hostAttachment) Scrollback() []byte    { return a.scrollback }

func (a *hostAttachment) WriteInput(p []byte) (int, error) {
	if err := a.send(terminalHostStreamMessage{Op: terminalHostOpInput, Data: p}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (a *hostAttachment) Resize(_ string, cols, rows uint16) error {
	return a.send(terminalHostStreamMessage{
		Op:   terminalHostOpResize,
		Data: []byte{byte(cols >> 8), byte(cols), byte(rows >> 8), byte(rows)},
	})
}

func (a *hostAttachment) ExitReason() string {
	if v := a.exitReason.Load(); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (a *hostAttachment) detach() error {
	err := a.send(terminalHostStreamMessage{Op: terminalHostOpDetach})
	closeErr := a.close()
	return errors.Join(err, closeErr)
}

func (a *hostAttachment) send(msg terminalHostStreamMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return io.ErrClosedPipe
	}
	return a.enc.Encode(&msg)
}

func (a *hostAttachment) close() error {
	var err error
	a.once.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		err = a.conn.Close()
		close(a.output)
	})
	return err
}

func (a *hostAttachment) readLoop(remove func(string)) {
	defer remove(a.connID)
	defer func() { _ = a.close() }()
	for {
		var msg terminalHostStreamMessage
		if err := a.dec.Decode(&msg); err != nil {
			return
		}
		switch msg.Op {
		case terminalHostOpOutput:
			select {
			case a.output <- msg.Data:
			default:
			}
		case terminalHostOpClosed:
			if msg.ExitReason != "" {
				a.exitReason.Store(msg.ExitReason)
			}
			return
		}
	}
}

var (
	_ PTYSource          = (*TerminalHostClient)(nil)
	_ PTYLifetime        = (*TerminalHostClient)(nil)
	_ PTYCommandRunner   = (*TerminalHostClient)(nil)
	_ WorkspaceRegistrar = (*TerminalHostClient)(nil)
	_ Attachment         = (*hostAttachment)(nil)
)
