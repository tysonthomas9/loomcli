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
// session limit has been hit.
var ErrPTYMaxSessionsReached = errors.New("maximum terminal sessions reached")

const (
	defaultPTYMaxSessions = 40
	defaultGracePeriod    = 60 * time.Second
	defaultIdleTimeout    = 30 * time.Minute
	defaultReaperTick     = 60 * time.Second
)

// termEnv is the TERM environment value injected for every PTY-backed
// child process. xterm-256color gives child shells color support by default
// and is what tmux 3.6+ requires to start.
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
		sessions:    make(map[SessionKey]*ptySession),
		shell:       shell,
		argv:        argv,
		env:         env,
		cwd:         cwd,
		max:         maxSessions,
		gracePeriod: defaultGracePeriod,
		idleTimeout: defaultIdleTimeout,
		reaperStop:  make(chan struct{}),
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
// Callers should check Attachment.Scrollback() for replay bytes.
func (m *PTYManager) AttachSession(key SessionKey, cols, rows uint16, argv []string) (att Attachment, reattached bool, err error) {
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
		_ = pty.Setsize(sess.pty, &pty.Winsize{Cols: cols, Rows: rows})
	}

	sess.cancelKillTimer()

	connID := fmt.Sprintf("pty-%d", m.counter.Add(1))
	return sess.attachNew(connID), existed, nil
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
	go sess.drain(m)
	return sess, nil
}

// Detach releases the connID attached to key and arms the grace timer if
// the session has no other attachments. Does not close the PTY.
func (m *PTYManager) Detach(key SessionKey, connID string) {
	m.mu.Lock()
	sess := m.sessions[key]
	grace := m.gracePeriod
	m.mu.Unlock()

	if sess == nil {
		return
	}
	if sess.detach(connID) {
		sess.armKillTimer(grace, func() { m.killSession(key) })
	}
}

// Kill immediately terminates the session for key. Idempotent.
func (m *PTYManager) Kill(key SessionKey) error {
	return m.killSession(key)
}

func (m *PTYManager) killSession(key SessionKey) error {
	m.mu.Lock()
	sess, ok := m.sessions[key]
	if ok {
		delete(m.sessions, key)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return sess.close()
}

// SessionCount returns the number of live sessions, including detached ones
// still within the grace/idle windows.
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
	m.mu.Unlock()

	var firstErr error
	for _, s := range sessions {
		if err := s.close(); err != nil && firstErr == nil {
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
	// Snapshot under the manager lock, evaluate per-session state outside it
	// to avoid lock-ordering dependencies with ptySession.attachMu.
	m.mu.Lock()
	idle := m.idleTimeout
	snapshot := make(map[SessionKey]*ptySession, len(m.sessions))
	for k, s := range m.sessions {
		snapshot[k] = s
	}
	m.mu.Unlock()

	now := time.Now().UnixNano()
	var victims []SessionKey
	for key, sess := range snapshot {
		if sess.attached() {
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
	for _, key := range victims {
		_ = m.killSession(key)
	}
}

// onSessionExited is invoked by a session's drain goroutine when the child
// process exits on its own (PTY EOF). Cleans up manager-side state.
func (m *PTYManager) onSessionExited(key SessionKey) {
	_ = m.killSession(key)
}
