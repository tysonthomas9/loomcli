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
	// Local `loom serve` keeps detached sessions alive indefinitely: no
	// grace-period kill and no idle reap. The assumption is one developer
	// per server, so leaking a few PTYs is cheaper than surprising the
	// user with a killed shell. Remote `loom-agentd` (Firecracker) sets
	// non-zero values.
	defaultGracePeriod = 0
	defaultIdleTimeout = 0
	defaultReaperTick  = 60 * time.Second
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
	cwd   string   // initial working directory for spawned shells (required; no default)

	max     int
	counter atomic.Uint64

	gracePeriod time.Duration
	idleTimeout time.Duration

	reaperStop chan struct{}
	reaperWG   sync.WaitGroup

	// closed is set by Shutdown under mu. Once true, AttachSession returns
	// ErrPTYManagerClosed instead of spawning a new session. Prevents a
	// concurrent AttachSession racing with MultiPTYManager.Deregister from
	// resurrecting a shut-down manager with an orphan session that the
	// outer dispatcher can no longer route Detach/Kill to.
	closed bool
}

// NewPTYManager constructs a manager. command is the default shell command
// to execute (as `sh -c command`); if empty, the user's login shell is
// started with `-l`. maxSessions <= 0 falls back to the default. cwd is the
// initial working directory for every PTY the manager spawns and is required:
// an empty cwd is a programmer error and panics. There is no silent fallback
// to $HOME or any other default — callers must supply a real directory
// (typically a workspace.Path).
func NewPTYManager(command string, maxSessions int, cwd string) *PTYManager {
	if cwd == "" {
		panic("terminal.NewPTYManager: cwd is required (pass workspace.Path or a concrete directory; no silent HOME fallback)")
	}
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

	// Between the manager-lock lookup and sess.attachNew below, a concurrent
	// Kill / grace-timer fire / idle reaper / child-exit can close the
	// session. attachNew signals that race by returning nil; we then retry
	// the full lookup so a fresh session is spawned instead of writing into
	// a closed one (nil-map panic). A small bound is enough — each retry
	// either observes the session gone from m.sessions and spawns a new
	// one, or finds a freshly-spawned session that isn't concurrently
	// closing.
	const maxAttachRetries = 3
	for attempt := 0; attempt < maxAttachRetries; attempt++ {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, false, ErrPTYManagerClosed
		}
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
		if local := sess.attachNew(connID); local != nil {
			return local, existed, nil
		}
		// Session was closed between lookup and attach. Retry.
	}
	return nil, false, fmt.Errorf("terminal attach: session %q repeatedly closed during attach", key)
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
	if sess.detach(connID) && grace > 0 {
		sess.armKillTimer(grace, func() { _ = m.killSession(key, ExitReasonKilled) })
	}
}

// Kill immediately terminates the session for key. Idempotent.
func (m *PTYManager) Kill(key SessionKey) error {
	return m.killSession(key, ExitReasonKilled)
}

func (m *PTYManager) killSession(key SessionKey, reason string) error {
	m.mu.Lock()
	sess, ok := m.sessions[key]
	if ok {
		delete(m.sessions, key)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return sess.close(reason)
}

// SessionCount returns the number of live sessions, including detached ones
// still within the grace/idle windows.
func (m *PTYManager) SessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// SessionCountFor satisfies PTYSource. A bare PTYManager owns a single
// session namespace, so the returned count is the same as SessionCount
// regardless of wsID. MultiPTYManager provides the per-workspace variant.
func (m *PTYManager) SessionCountFor(_ string) int {
	return m.SessionCount()
}

// HasSession reports whether a (live or gracefully-detached) session exists
// for key. "Live" means not yet killed by Kill / Shutdown / reaper — it does
// not guarantee the underlying child process is still running, only that the
// manager has not released it.
func (m *PTYManager) HasSession(key SessionKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[key]
	return ok
}

// AttachmentCount returns the number of concurrent clients attached to the
// session identified by key, or 0 if the session is unknown.
func (m *PTYManager) AttachmentCount(key SessionKey) int {
	m.mu.Lock()
	sess, ok := m.sessions[key]
	m.mu.Unlock()
	if !ok {
		return 0
	}
	return sess.attachmentCount()
}

// MaxSessions returns the configured concurrent-session cap.
func (m *PTYManager) MaxSessions() int { return m.max }

// GracePeriod returns the post-detach kill delay. Zero means disabled —
// detached sessions live until explicit Kill or Shutdown.
func (m *PTYManager) GracePeriod() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gracePeriod
}

// IdleTimeout returns the idle-reap threshold. Zero means disabled — the
// reaper never kills sessions based on inactivity.
func (m *PTYManager) IdleTimeout() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.idleTimeout
}

// Shutdown terminates every live session and stops the reaper. Idempotent:
// once closed, future AttachSession calls return ErrPTYManagerClosed and
// repeat Shutdown calls are no-ops.
func (m *PTYManager) Shutdown() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	m.mu.Unlock()

	close(m.reaperStop)
	m.reaperWG.Wait()

	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[SessionKey]*ptySession)
	m.mu.Unlock()

	var firstErr error
	for _, s := range sessions {
		if err := s.close(ExitReasonShutdown); err != nil && firstErr == nil {
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

	if idle <= 0 {
		return
	}

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
		_ = m.killSession(key, ExitReasonKilled)
	}
}

// onSessionExited is invoked by a session's drain goroutine when the child
// process exits on its own (PTY EOF). Cleans up manager-side state.
func (m *PTYManager) onSessionExited(key SessionKey) {
	_ = m.killSession(key, ExitReasonExited)
}
