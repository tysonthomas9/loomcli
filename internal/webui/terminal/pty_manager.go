// Package terminal's PTYManager owns per-session PTY-backed shells for the
// web terminal.
//
// Lifetime model: a PTY is owned by (workspace, session), not by any single
// WebSocket. A WebSocket attaches to a session to pipe its output to the
// browser and its input back to the shell; when the WebSocket disconnects,
// the session is *detached* (not killed), and a 60-second grace timer is
// armed. If a new WebSocket attaches to the same (workspace, session) within
// that window — typical for a page refresh or a brief network blip — the
// grace timer is cancelled and the client sees a fresh screen-reset plus the
// session's scrollback replayed, followed by live output.
//
// A session is killed when any of the following happen:
//   - the grace timer fires with no WebSocket attached;
//   - the idle reaper runs and finds no output and no attachment for the
//     configured idle window (30 minutes by default);
//   - the child process exits on its own (bash `exit`, process death);
//   - a client explicitly calls Kill (e.g. from a tab-close).
//
// The legacy tmux-backed manager lives alongside in agent_tmux.go purely for
// the live agent-terminal view — it is not used by the main web terminal
// path.
package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"
)

// ErrPTYMaxSessionsReached is returned by AttachSession when the concurrent-
// session limit has been hit. Handlers should reject the WebSocket upgrade
// with 503 before reaching AttachSession; this is a belt-and-braces check.
var ErrPTYMaxSessionsReached = errors.New("maximum terminal sessions reached")

// ErrSessionGone is returned when an operation targets a session that has
// been killed and removed from the manager.
var ErrSessionGone = errors.New("session gone")

const (
	defaultPTYMaxSessions = 40
	defaultGracePeriod    = 60 * time.Second
	defaultIdleTimeout    = 30 * time.Minute
	defaultReaperTick     = 60 * time.Second
)

// termEnv is the TERM environment value injected for every PTY-backed
// child process (both fresh shells via PTYManager and tmux attaches via
// AgentTmuxManager). tmux 3.6+ refuses to start without a recognized TERM,
// and xterm-256color gives child shells color support by default.
const termEnv = "TERM=xterm-256color"

// SessionKey identifies a persistent terminal session. Two WebSockets opened
// sequentially with the same key attach to the same underlying PTY.
type SessionKey struct {
	Workspace string
	Name      string
}

// String returns a debug-friendly identifier.
func (k SessionKey) String() string {
	if k.Workspace == "" {
		return k.Name
	}
	return k.Workspace + "/" + k.Name
}

// PTYManager owns the process-local set of terminal sessions.
type PTYManager struct {
	mu       sync.Mutex
	sessions map[SessionKey]*ptySession

	// Per-attach connID lookup for the Resizer interface.
	// The WS relay sends resize escapes tagged with a connID; we map it
	// back to the session whose current attachment uses that connID.
	connToSession map[string]SessionKey

	shell string   // absolute path to the login shell (e.g. /bin/bash)
	argv  []string // default args when a session's argv is nil
	env   []string // cached environment including TERM=xterm-256color
	cwd   string   // initial working directory (HOME if set)

	max     int
	counter atomic.Uint64

	gracePeriod time.Duration
	idleTimeout time.Duration

	reaperStop chan struct{}
	reaperWG   sync.WaitGroup
}

// NewPTYManager constructs a manager. command is the default shell command
// to execute (as `sh -c command`); if empty, the user's login shell is
// started with `-l`. maxSessions <= 0 falls back to the default.
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
		argv = []string{"-c", command}
	}

	cwd := os.Getenv("HOME")
	if cwd == "" {
		cwd = "/"
	}

	env := append(os.Environ(), termEnv)

	m := &PTYManager{
		sessions:      make(map[SessionKey]*ptySession),
		connToSession: make(map[string]SessionKey),
		shell:         shell,
		argv:          argv,
		env:           env,
		cwd:           cwd,
		max:           maxSessions,
		gracePeriod:   defaultGracePeriod,
		idleTimeout:   defaultIdleTimeout,
		reaperStop:    make(chan struct{}),
	}
	m.reaperWG.Add(1)
	go m.reapLoop()
	return m
}

// SetGracePeriod overrides the post-detach grace period before a session is
// killed. Tests use this to shrink the default.
func (m *PTYManager) SetGracePeriod(d time.Duration) {
	m.mu.Lock()
	m.gracePeriod = d
	m.mu.Unlock()
}

// SetIdleTimeout overrides the idle-reap threshold. Tests use this to shrink
// the default.
func (m *PTYManager) SetIdleTimeout(d time.Duration) {
	m.mu.Lock()
	m.idleTimeout = d
	m.mu.Unlock()
}

// AttachSession returns an attachment to the session identified by key. If
// the session does not exist, it is created with the given argv (nil = manager
// default). If an attachment already exists, it is replaced — the previous
// WebSocket's output channel is closed so its pump goroutine exits.
//
// reattached is true when the returned attachment is to a session that
// existed before this call (typical for page refresh or network blip).
// Callers should check attachment.Scrollback() for replay bytes.
func (m *PTYManager) AttachSession(key SessionKey, cols, rows uint16, argv []string) (att *Attachment, reattached bool, err error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	m.mu.Lock()
	sess, existed := m.sessions[key]
	if !existed {
		if len(m.sessions) >= m.max {
			m.mu.Unlock()
			return nil, false, ErrPTYMaxSessionsReached
		}
		newSess, spawnErr := m.spawnSession(key, cols, rows, argv)
		if spawnErr != nil {
			m.mu.Unlock()
			return nil, false, spawnErr
		}
		m.sessions[key] = newSess
		sess = newSess
	}
	m.mu.Unlock()

	if existed {
		// Resize the existing PTY to the new client's terminal geometry.
		_ = pty.Setsize(sess.pty, &pty.Winsize{Cols: cols, Rows: rows})
	}

	sess.cancelKillTimer()

	connID := fmt.Sprintf("pty-%d", m.counter.Add(1))
	newAtt := sess.attachNew(connID)

	m.mu.Lock()
	m.connToSession[connID] = key
	m.mu.Unlock()

	return newAtt, existed, nil
}

// spawnSession must be called with m.mu held.
func (m *PTYManager) spawnSession(key SessionKey, cols, rows uint16, argv []string) (*ptySession, error) {
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

	sess := newPtySession(key, ptmx, cmd)
	go sess.drain(m, key)
	return sess, nil
}

// Detach releases the attachment identified by connID and arms the grace
// timer if the session has no other attachments. Does not close the PTY.
func (m *PTYManager) Detach(connID string) {
	m.mu.Lock()
	key, ok := m.connToSession[connID]
	if ok {
		delete(m.connToSession, connID)
	}
	sess := m.sessions[key]
	grace := m.gracePeriod
	m.mu.Unlock()

	if !ok || sess == nil {
		return
	}

	if sess.detach(connID) {
		// No attachments left — arm the grace kill.
		sess.armKillTimer(grace, func() { m.killSession(key, "grace_expired") })
	}
}

// Resize satisfies realtime.Resizer for the WS relay. The resize escape is
// tagged with a connID; we look up the owning session and resize its PTY.
func (m *PTYManager) Resize(connID string, cols, rows uint16) error {
	m.mu.Lock()
	key, ok := m.connToSession[connID]
	sess := m.sessions[key]
	m.mu.Unlock()

	if !ok || sess == nil {
		return fmt.Errorf("connection %q not found", connID)
	}
	if err := pty.Setsize(sess.pty, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("pty.Setsize: %w", err)
	}
	return nil
}

// Kill immediately terminates the session for key. Idempotent.
func (m *PTYManager) Kill(key SessionKey) error {
	return m.killSession(key, "explicit_kill")
}

func (m *PTYManager) killSession(key SessionKey, reason string) error {
	m.mu.Lock()
	sess, ok := m.sessions[key]
	if ok {
		delete(m.sessions, key)
		for connID, k := range m.connToSession {
			if k == key {
				delete(m.connToSession, connID)
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return sess.close(reason)
}

// SessionCount returns the number of live sessions, including detached ones
// that are still within the grace/idle windows.
func (m *PTYManager) SessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// MaxSessions returns the configured concurrent-session cap.
func (m *PTYManager) MaxSessions() int { return m.max }

// Shutdown terminates every live session and stops the reaper.
func (m *PTYManager) Shutdown() error {
	close(m.reaperStop)
	m.reaperWG.Wait()

	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[SessionKey]*ptySession)
	m.connToSession = make(map[string]SessionKey)
	m.mu.Unlock()

	var firstErr error
	for _, s := range sessions {
		if err := s.close("shutdown"); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *PTYManager) reapLoop() {
	defer m.reaperWG.Done()
	tick := time.NewTicker(defaultReaperTick)
	defer tick.Stop()
	for {
		select {
		case <-m.reaperStop:
			return
		case <-tick.C:
			m.reapIdle()
		}
	}
}

func (m *PTYManager) reapIdle() {
	now := time.Now().UnixNano()
	m.mu.Lock()
	idle := m.idleTimeout
	victims := make([]SessionKey, 0)
	for key, sess := range m.sessions {
		if sess.attachedCount() > 0 {
			continue
		}
		last := sess.lastOutputUnixNano()
		if last == 0 {
			last = sess.createdUnixNano()
		}
		if time.Duration(now-last) >= idle {
			victims = append(victims, key)
		}
	}
	m.mu.Unlock()
	for _, key := range victims {
		_ = m.killSession(key, "idle_reap")
	}
}

// onSessionExited is invoked by a session's drain goroutine when the child
// process exits on its own (PTY EOF). Cleans up manager-side state.
func (m *PTYManager) onSessionExited(key SessionKey) {
	_ = m.killSession(key, "child_exited")
}
