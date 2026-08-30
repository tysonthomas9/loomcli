package exe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"

	"golang.org/x/crypto/ssh"
)

// ErrPTYSessionNotFound means the durable tmux session was absent in the VM.
// Attaching must fail with this rather than creating one: a second lead
// process in the same sandbox would run the same agent twice against the same
// assignment.
var ErrPTYSessionNotFound = errors.New("exe: pty session not found")

const (
	// attachTerm is the terminal type advertised to tmux. The browser side is
	// xterm.js, so this is what it actually is.
	attachTerm = "xterm-256color"

	// attachInitialCols/Rows are placeholders until the first Resize arrives.
	// tmux needs a size at attach time and the real one is only known once a
	// websocket client reports it.
	attachInitialCols = 120
	attachInitialRows = 32
)

// rePTYSession allowlists tmux session names: no ":" or "." so a name can
// never address a different session's window or pane.
var rePTYSession = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// exactTarget forces tmux to match a session name exactly.
//
// tmux target resolution falls back to prefix and fnmatch matching, so a bare
// -t lead would happily attach to "lead-old" if "lead" were gone. The "="
// prefix disables that. Attaching to the wrong session shows one lead's
// terminal under another lead's identity.
func exactTarget(session string) string { return shellQuote("=" + session) }

// tmuxHasSessionCommand asks whether the session exists. It has no side effect
// -- that is the point of checking with has-session rather than by attaching.
func tmuxHasSessionCommand(session string) string {
	return fmt.Sprintf("tmux -L %s has-session -t %s", tmuxSocket, exactTarget(session))
}

// tmuxAttachCommand attaches to an EXISTING session.
//
// attach-session, never "new-session -A": -A would create the session when it
// is missing, so a failed lead boot would silently become an empty shell that
// looks like a healthy terminal while no agent is running.
//
// -d detaches any other client. serve is the only attacher (the PTY manager
// fans one upstream out to every browser tab), so another client is a leftover
// from a crashed serve -- and tmux sizes a shared session to the SMALLEST
// attached client, so a stale one would pin this terminal to its last known
// size forever.
func tmuxAttachCommand(session string) string {
	return fmt.Sprintf("tmux -L %s attach-session -d -t %s", tmuxSocket, exactTarget(session))
}

// AttachPTY attaches to an existing durable tmux session over SSH.
//
// It NEVER creates a session: has-session is checked first, and the attach
// itself uses attach-session (not new-session -A). Creation belongs to the
// broker's PrepareLeadBoot/CreatePty path, which is the only place that knows
// the lead's command, environment and occupant token.
//
// The returned value satisfies the terminal layer's PTYUpstream interface
// structurally, so this package does not import the web UI.
func (p *Provider) AttachPTY(ctx context.Context, sandboxID, ptySessionID string) (*PTYAttachment, error) {
	if err := checkArg("vm name", sandboxID, reVMName); err != nil {
		return nil, err
	}
	// The session name is shell-quoted, but it is also a tmux TARGET, where
	// ":" and "." select a window and pane. A name carrying them would attach
	// to a pane of some other session rather than failing.
	if err := checkArg("pty session", ptySessionID, rePTYSession); err != nil {
		return nil, err
	}

	client, err := p.dialer.dial(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	ok, err := tmuxHasSession(client, ptySessionID)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	if !ok {
		_ = client.Close()
		return nil, fmt.Errorf("%w: vm %q session %q", ErrPTYSessionNotFound, sandboxID, ptySessionID)
	}

	attachment, err := newPTYAttachment(client, ptySessionID)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("exe attach pty %q in vm %q: %w", ptySessionID, sandboxID, err)
	}
	return attachment, nil
}

// tmuxHasSession reports whether the session exists, distinguishing it from a
// transport failure.
//
// "no tmux server" means no sessions, not an error -- the lead has simply not
// been booted yet. Any other nonzero exit is a real failure and is surfaced,
// because reporting "not found" for a broken connection would let a caller
// conclude the lead is gone and reprovision a second one.
func tmuxHasSession(client *ssh.Client, session string) (bool, error) {
	out, err := run(client, tmuxHasSessionCommand(session))
	if err == nil {
		return true, nil
	}
	if tmuxNoServer(out) {
		return false, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		// tmux exits 1 with "can't find session" when the server is up but
		// this session is not. That is a definite absence.
		return false, nil
	}
	return false, fmt.Errorf("exe tmux has-session %q: %w (%s)", session, err, firstLine(out))
}

// PTYAttachment is one attached client to a durable tmux session.
type PTYAttachment struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	outputOnce sync.Once
	output     chan []byte
	stop       chan struct{}
	readerWG   sync.WaitGroup

	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
}

func newPTYAttachment(client *ssh.Client, ptySessionID string) (*PTYAttachment, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open ssh session: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// ECHO off and no input post-processing: tmux drives the inner pty and
	// does its own echoing. Leaving them on double-echoes every keystroke.
	modes := ssh.TerminalModes{ssh.ECHO: 0, ssh.OCRNL: 0}
	if err := session.RequestPty(attachTerm, attachInitialRows, attachInitialCols, modes); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	if err := session.Start(tmuxAttachCommand(ptySessionID)); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("start attach: %w", err)
	}
	return &PTYAttachment{
		client:  client,
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		stop:    make(chan struct{}),
	}, nil
}

// Output returns the byte stream. Closing it signals the upstream ended.
func (a *PTYAttachment) Output() <-chan []byte {
	a.startReader()
	return a.output
}

func (a *PTYAttachment) startReader() {
	a.outputOnce.Do(func() {
		a.output = make(chan []byte)
		a.readerWG.Add(1)
		go func() {
			defer a.readerWG.Done()
			a.readOutput()
		}()
	})
}

func (a *PTYAttachment) readOutput() {
	defer close(a.output)
	buf := make([]byte, 32*1024)
	for {
		n, err := a.stdout.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case a.output <- chunk:
			case <-a.stop:
				return
			}
		}
		if err != nil {
			return
		}
		select {
		case <-a.stop:
			return
		default:
		}
	}
}

func (a *PTYAttachment) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	return a.stdin.Write(p)
}

// Resize propagates the browser's size to the remote pty, which tmux then
// applies to the session.
func (a *PTYAttachment) Resize(_ context.Context, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return a.session.WindowChange(int(rows), int(cols))
}

// Close detaches this client. It never kills the tmux session, so the lead
// keeps running when a tab closes, a websocket drops, or serve restarts.
// Killing a lead belongs to the broker and reaper.
//
// It deliberately does NOT take writeMu: a half-open TCP connection parks a
// write indefinitely, and closing the transport is exactly what unblocks it.
// Taking the lock here would make Close wait on the write it exists to rescue.
func (a *PTYAttachment) Close() error {
	a.closeOnce.Do(func() {
		close(a.stop)
		// Closing the client tears down the session's channel too, which
		// unblocks the reader parked in Read. Closing the session alone does
		// not, because the underlying TCP connection stays open.
		a.closeErr = a.client.Close()
		a.startReader()
		a.readerWG.Wait()
	})
	return a.closeErr
}
