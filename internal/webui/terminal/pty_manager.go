// Package terminal's PTYManager owns one PTY-backed shell per WebSocket
// connection.
//
// Mirrors wterm/examples/local/server.ts in Go: each connection spawns a fresh
// shell, output is relayed to the WS, input (minus the \x1b[RESIZE:cols;rows]
// escape) is written to the PTY, and closing the WS kills the shell. There is
// no session persistence, no multi-viewer attach, no scrollback buffer. The
// legacy tmux-backed manager lives alongside in agent_tmux.go purely for the
// live agent-terminal view — it is not used by the main web terminal path.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/creack/pty"
)

// ErrPTYMaxSessionsReached is returned by Open when the concurrent-connection
// limit has been hit. Handlers should reject the WebSocket upgrade with 503
// before reaching Open; this is a belt-and-braces check.
var ErrPTYMaxSessionsReached = errors.New("maximum terminal sessions reached")

const defaultPTYMaxSessions = 20

// termEnv is the TERM environment value injected for every PTY-backed
// child process (both fresh shells via PTYManager and tmux attaches via
// AgentTmuxManager). tmux 3.6+ refuses to start without a recognised TERM,
// and xterm-256color gives child shells color support by default.
const termEnv = "TERM=xterm-256color"

// PTYManager is the connection-scoped PTY lifecycle owner used by the web
// terminal WebSocket handler. A single instance is shared across all
// connections; the per-connection state lives in PTYConn.
type PTYManager struct {
	mu    sync.RWMutex
	conns map[string]*PTYConn

	shell string   // absolute path to the login shell (e.g. /bin/bash)
	argv  []string // args to pass; "-l" for default login shell
	env   []string // cached environment including TERM=xterm-256color
	cwd   string   // initial working directory (HOME if set)

	max     int
	counter atomic.Uint64
}

// PTYConn is one PTY-backed shell bound to a single WebSocket.
type PTYConn struct {
	ConnID string
	PTY    *os.File

	cmd *exec.Cmd

	mu     sync.Mutex
	closed bool
}

// NewPTYManager constructs a manager. command is the shell command to execute
// inside a login shell (e.g. "claude", "bash"); if empty, the user's $SHELL
// is started as a login shell with no arguments (matching wterm's default).
// maxSessions <= 0 falls back to defaultPTYMaxSessions.
func NewPTYManager(command string, maxSessions int) *PTYManager {
	if maxSessions <= 0 {
		maxSessions = defaultPTYMaxSessions
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	var argv []string
	command = strings.TrimSpace(command)
	if command == "" {
		argv = []string{"-l"}
	} else {
		// Run through the shell so users' existing TerminalCmd strings
		// ("foo && bar", pipelines, quoting) keep working. This is the
		// same contract tmux offered via `tmux new-session ... <cmd>`.
		argv = []string{"-c", command}
	}

	cwd := os.Getenv("HOME")
	if cwd == "" {
		cwd = "/"
	}

	env := append(os.Environ(), termEnv)

	return &PTYManager{
		conns: make(map[string]*PTYConn),
		shell: shell,
		argv:  argv,
		env:   env,
		cwd:   cwd,
		max:   maxSessions,
	}
}

// Open spawns a fresh shell, attaches it to a new PTY sized cols×rows,
// registers it under a unique connID, and returns it. When argv is nil the
// manager's default argv is used; pass a non-nil slice to override per-open
// (e.g. when a session name encodes a specific backend). Callers must pair
// Open with exactly one Detach (or PTYConn.Close).
func (m *PTYManager) Open(cols, rows uint16, argv []string) (*PTYConn, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	m.mu.Lock()
	if len(m.conns) >= m.max {
		m.mu.Unlock()
		return nil, ErrPTYMaxSessionsReached
	}
	connID := fmt.Sprintf("pty-%d", m.counter.Add(1))
	m.mu.Unlock()

	useArgv := argv
	if useArgv == nil {
		useArgv = m.argv
	}
	cmd := exec.Command(m.shell, useArgv...) //nolint:gosec // shell + argv sourced from server config, not request data
	cmd.Env = m.env
	cmd.Dir = m.cwd

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("pty.StartWithSize: %w", err)
	}

	conn := &PTYConn{ConnID: connID, PTY: ptmx, cmd: cmd}

	m.mu.Lock()
	m.conns[connID] = conn
	m.mu.Unlock()

	return conn, nil
}

// Resize sets the PTY window size for connID.
func (m *PTYManager) Resize(connID string, cols, rows uint16) error {
	m.mu.RLock()
	conn, ok := m.conns[connID]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return fmt.Errorf("connection %q is closed", connID)
	}
	if err := pty.Setsize(conn.PTY, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("pty.Setsize: %w", err)
	}
	return nil
}

// Detach removes connID from the map and closes its PTY+process. Idempotent.
func (m *PTYManager) Detach(connID string) error {
	m.mu.Lock()
	conn, ok := m.conns[connID]
	if ok {
		delete(m.conns, connID)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("connection %q not found", connID)
	}
	return conn.Close()
}

// SessionCount returns the number of active PTY connections.
func (m *PTYManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.conns)
}

// MaxSessions returns the configured concurrent-connection cap.
func (m *PTYManager) MaxSessions() int { return m.max }

// Shutdown closes every live PTY and clears the map. Called at server shutdown.
func (m *PTYManager) Shutdown() error {
	m.mu.Lock()
	conns := m.conns
	m.conns = make(map[string]*PTYConn)
	m.mu.Unlock()

	var firstErr error
	for _, c := range conns {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close tears down the PTY master and terminates the child process. Safe to
// call multiple times. Callers should prefer Detach on the manager so the
// connID is also removed from the map.
func (c *PTYConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	var firstErr error
	if c.PTY != nil {
		if err := c.PTY.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Kill rather than wait: a child ignoring SIGHUP would otherwise stay
	// resident after the PTY master goes away.
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return firstErr
}
