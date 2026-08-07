package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	sdkdaytona "github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	sdktypes "github.com/daytonaio/daytona/libs/sdk-go/pkg/types"
)

const (
	// DefaultDaytonaLeadPTYSessionID is the broker-created Daytona PTY id for
	// the lead process. Placement owns creation; terminal attaches by id only.
	// It must equal placement.LeadPTYSessionID; a drift test pins them rather
	// than importing placement here, since attaching does not otherwise need
	// the broker.
	DefaultDaytonaLeadPTYSessionID = "lead"
)

var (
	// ErrDaytonaPTYSessionNotFound means the requested durable PTY session was
	// absent in the sandbox. Attaching must fail rather than creating a second
	// lead process.
	ErrDaytonaPTYSessionNotFound = errors.New("daytona pty session not found")

	newDaytonaPTYUpstreamForManager = NewDaytonaPTYUpstream
)

// DaytonaPTYConfig carries optional Daytona client settings. Empty fields fall
// back to the SDK's environment handling.
type DaytonaPTYConfig struct {
	APIKey         string
	APIURL         string
	OrganizationID string
	Target         string
}

type daytonaPTYHandle interface {
	DataChan() <-chan []byte
	SendInput([]byte) error
	Resize(context.Context, int, int) (*sdktypes.PtySessionInfo, error)
	Disconnect() error
	WaitForConnection(context.Context) error
}

type daytonaPTYConnector interface {
	ListPtySessions(context.Context) ([]daytonaPTYSession, error)
	ConnectPty(context.Context, string) (daytonaPTYHandle, error)
}

type daytonaPTYSession struct {
	ID string
}

type daytonaProcessService struct {
	process *sdkdaytona.ProcessService
}

func (s daytonaProcessService) ListPtySessions(ctx context.Context) ([]daytonaPTYSession, error) {
	sessions, err := s.process.ListPtySessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]daytonaPTYSession, 0, len(sessions))
	for _, session := range sessions {
		if session == nil {
			continue
		}
		out = append(out, daytonaPTYSession{ID: session.ID})
	}
	return out, nil
}

func (s daytonaProcessService) ConnectPty(ctx context.Context, sessionID string) (daytonaPTYHandle, error) {
	return s.process.ConnectPty(ctx, sessionID)
}

type daytonaPTYUpstream struct {
	handle daytonaPTYHandle

	outputOnce sync.Once
	output     chan []byte
	stop       chan struct{}
	readerWG   sync.WaitGroup

	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

// NewDaytonaPTYUpstream attaches to an existing Daytona PTY session. It never
// creates a PTY and never starts a process; absence is returned clearly.
func NewDaytonaPTYUpstream(ctx context.Context, sandboxID, ptySessionID string, cfg DaytonaPTYConfig) (PTYUpstream, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	ptySessionID = strings.TrimSpace(ptySessionID)
	if sandboxID == "" {
		return nil, fmt.Errorf("daytona sandbox id required")
	}
	if ptySessionID == "" {
		return nil, fmt.Errorf("daytona pty session id required")
	}

	client, err := sdkdaytona.NewClientWithConfig(&sdktypes.DaytonaConfig{
		APIKey:         strings.TrimSpace(cfg.APIKey),
		APIUrl:         strings.TrimSpace(cfg.APIURL),
		OrganizationID: strings.TrimSpace(cfg.OrganizationID),
		Target:         strings.TrimSpace(cfg.Target),
	})
	if err != nil {
		return nil, fmt.Errorf("daytona pty client: %w", err)
	}
	sandbox, err := client.Get(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("daytona get sandbox %q: %w", sandboxID, err)
	}
	if sandbox == nil || sandbox.Process == nil {
		return nil, fmt.Errorf("daytona sandbox %q has no process service", sandboxID)
	}
	return newDaytonaPTYUpstreamFromConnector(ctx, daytonaProcessService{process: sandbox.Process}, sandboxID, ptySessionID)
}

func newDaytonaPTYUpstreamFromConnector(ctx context.Context, connector daytonaPTYConnector, sandboxID, ptySessionID string) (*daytonaPTYUpstream, error) {
	if connector == nil {
		return nil, fmt.Errorf("daytona pty connector required")
	}
	if err := verifyDaytonaPTYSession(ctx, connector, sandboxID, ptySessionID); err != nil {
		return nil, err
	}
	handle, err := connector.ConnectPty(ctx, ptySessionID)
	if err != nil {
		return nil, fmt.Errorf("daytona connect pty %q in sandbox %q: %w", ptySessionID, sandboxID, err)
	}
	if handle == nil {
		return nil, fmt.Errorf("daytona connect pty %q in sandbox %q returned nil handle", ptySessionID, sandboxID)
	}
	if err := handle.WaitForConnection(ctx); err != nil {
		_ = handle.Disconnect()
		return nil, fmt.Errorf("daytona wait for pty %q in sandbox %q connection: %w", ptySessionID, sandboxID, err)
	}
	return newDaytonaPTYUpstream(handle), nil
}

func verifyDaytonaPTYSession(ctx context.Context, connector daytonaPTYConnector, sandboxID, ptySessionID string) error {
	sessions, err := connector.ListPtySessions(ctx)
	if err != nil {
		return fmt.Errorf("daytona list pty sessions in sandbox %q: %w", sandboxID, err)
	}
	for _, session := range sessions {
		if strings.TrimSpace(session.ID) == ptySessionID {
			return nil
		}
	}
	return fmt.Errorf("%w: sandbox %q session %q", ErrDaytonaPTYSessionNotFound, sandboxID, ptySessionID)
}

func newDaytonaPTYUpstream(handle daytonaPTYHandle) *daytonaPTYUpstream {
	return &daytonaPTYUpstream{handle: handle, stop: make(chan struct{})}
}

func (u *daytonaPTYUpstream) Output() <-chan []byte {
	u.startReader()
	return u.output
}

func (u *daytonaPTYUpstream) startReader() {
	u.outputOnce.Do(func() {
		u.output = make(chan []byte)
		u.readerWG.Add(1)
		go func() {
			defer u.readerWG.Done()
			u.readOutput()
		}()
	})
}

func (u *daytonaPTYUpstream) readOutput() {
	defer close(u.output)
	if u.handle == nil {
		return
	}
	dataCh := u.handle.DataChan()
	for {
		select {
		case <-u.stop:
			return
		case data, ok := <-dataCh:
			if !ok {
				return
			}
			if len(data) == 0 {
				continue
			}
			chunk := make([]byte, len(data))
			copy(chunk, data)
			select {
			case u.output <- chunk:
			case <-u.stop:
				return
			}
		}
	}
}

func (u *daytonaPTYUpstream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// The Daytona handle writes over a websocket and gorilla/websocket panics
	// on concurrent writers. The SDK does not serialize for us -- SendInput
	// takes only a read lock and calls WriteMessage outside it -- so writers
	// are serialized here. Close deliberately does NOT take this lock: see
	// there.
	u.writeMu.Lock()
	defer u.writeMu.Unlock()
	if err := u.handle.SendInput(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (u *daytonaPTYUpstream) Resize(ctx context.Context, cols, rows uint16) error {
	_, err := u.handle.Resize(ctx, int(cols), int(rows))
	return err
}

// Close disconnects this client. It never kills the PTY session: the lead
// keeps running when a tab closes, a websocket drops, or serve restarts.
// Killing a lead belongs to the broker and reaper.
//
// It deliberately does NOT take writeMu. The SDK sets no write deadline, so a
// half-open connection parks SendInput indefinitely -- and closing the
// underlying connection is exactly what unblocks it. Taking the lock here
// would make Close wait on the write it is supposed to rescue, wedging
// PTYManager.Shutdown and with it the whole serve process. Disconnect closes
// the connection rather than writing to it, so it does not race a writer.
func (u *daytonaPTYUpstream) Close() error {
	u.closeOnce.Do(func() {
		close(u.stop)
		if u.handle != nil {
			dataCh := u.handle.DataChan()
			u.closeErr = u.handle.Disconnect()
			// The SDK's message pump sends to dataChan without selecting on
			// any done signal, so if it is parked on a full buffer it stays
			// parked forever -- closing the connection does not free a
			// goroutine blocked on a channel send. Draining lets it observe
			// the closed connection and return.
			go func() {
				for range dataCh { //nolint:revive // drain to release the SDK pump
				}
			}()
		}
		u.startReader()
		u.readerWG.Wait()
	})
	return u.closeErr
}

var _ PTYUpstream = (*daytonaPTYUpstream)(nil)
