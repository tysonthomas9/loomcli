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
// The tmux-backed manager lives alongside in agent_tmux.go for the live
// agent-terminal view; it is not used by the main web terminal path.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/creack/pty"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

// ErrPTYMaxSessionsReached is returned by AttachSession when the concurrent-
// session limit has been hit.
var ErrPTYMaxSessionsReached = errors.New("maximum terminal sessions reached")

// ErrPTYSessionNotFound is returned when backend-owned input targets a
// session that is not live in this manager.
var ErrPTYSessionNotFound = errors.New("terminal session not found")

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
	beforeKillTimeout  = 5 * time.Second

	beforeKillRetryAttempts = 6
	beforeKillRetryBase     = 100 * time.Millisecond
	beforeKillRetryMax      = time.Second
)

// termEnv is the TERM environment value injected for every PTY-backed
// child process. xterm-256color gives child shells color support by default
// and is what tmux 3.6+ requires to start.
const termEnv = "TERM=xterm-256color"
const workspaceEnvPrefix = "LOOM_WORKSPACE="

func terminalSpawnEnv(base []string) []string {
	filtered := pinCurrentLoomOnPath(platformruntime.FilterSubprocessEnv(platformruntime.SubprocessEnvInteractionChild, base))
	env := make([]string, 0, len(filtered)+1)
	for _, entry := range filtered {
		switch {
		case strings.HasPrefix(entry, "COLUMNS="),
			strings.HasPrefix(entry, "LINES="),
			strings.HasPrefix(entry, "TERM="):
			continue
		default:
			env = append(env, entry)
		}
	}
	return append(env, termEnv)
}

// pinCurrentLoomOnPath makes commands launched from an agent's AI shell use
// the same Loom binary that started the terminal. Packaged Desktop launches
// the outer agent with an absolute sidecar path, but the AI later invokes
// plain `loom` commands from its shell. Without this pin an older user-global
// binary can win PATH and speak an incompatible local FleetDB protocol.
func pinCurrentLoomOnPath(env []string) []string {
	return platformruntime.PinExecutableDirOnPath(env, loomExecutableForTerminal())
}

func terminalSessionEnv(base []string, key SessionKey) []string {
	env := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(entry, workspaceEnvPrefix) {
			continue
		}
		env = append(env, entry)
	}
	if key.Workspace != "" {
		env = append(env, workspaceEnvPrefix+key.Workspace)
	}
	return env
}

func overlayTerminalEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	blocked := make(map[string]struct{}, len(extra))
	for key := range extra {
		if !interaction.ChildLaunchEnvAllowed(key) {
			continue
		}
		blocked[key] = struct{}{}
	}
	env := make([]string, 0, len(base)+len(extra))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, found := blocked[key]; found {
				continue
			}
		}
		env = append(env, entry)
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		if !interaction.ChildLaunchEnvAllowed(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+extra[key])
	}
	return env
}

// SessionKey identifies a persistent terminal session. Two WebSockets opened
// sequentially with the same key attach to the same underlying PTY.
type SessionKey struct {
	Workspace string
	Name      string
}

// BeforeKillFunc converges durable lifecycle state before manager ownership of
// a live PTY ends. Natural child exit is different: local process state is
// removed immediately and durable convergence is retried asynchronously.
type BeforeKillFunc func(context.Context, SessionKey, string) error

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
	ended    map[SessionKey]string
	// converged records ended tombstones whose durable Interaction lifecycle
	// hook completed. Natural exits create an ended tombstone first and only
	// mark it converged after the synchronous attempt or a retry succeeds.
	converged  map[SessionKey]bool
	converging map[SessionKey]bool

	shell string   // absolute path to the login shell (e.g. /bin/bash)
	argv  []string // default args when a session's argv is nil
	env   []string // cached environment including TERM=xterm-256color
	cwd   string   // initial working directory for spawned shells (required; no default)

	max     int
	counter atomic.Uint64

	gracePeriod time.Duration
	idleTimeout time.Duration
	beforeKill  BeforeKillFunc

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

	env := terminalSpawnEnv(os.Environ())

	m := &PTYManager{
		sessions:    make(map[SessionKey]*ptySession),
		ended:       make(map[SessionKey]string),
		converged:   make(map[SessionKey]bool),
		converging:  make(map[SessionKey]bool),
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

// SetBeforeKill installs the server-owned lifecycle hook used by every
// destructive PTY path: explicit Kill, detach grace, idle reap, and Shutdown.
func (m *PTYManager) SetBeforeKill(hook BeforeKillFunc) {
	m.mu.Lock()
	m.beforeKill = hook
	m.mu.Unlock()
}

// AttachSession returns an attachment to the session identified by key. If
// the session does not exist, it is created with the given launch spec
// (nil or empty argv = manager default). If an attachment already exists, it is replaced — the previous
// WebSocket's output channel is closed so its pump goroutine exits.
//
// reattached is true when the returned attachment is to a session that
// existed before this call (typical for page refresh or network blip).
// Callers should check Attachment.Scrollback() for replay bytes.
func (m *PTYManager) AttachSession(key SessionKey, cols, rows uint16, launch *LaunchSpec) (att Attachment, reattached bool, err error) {
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
			newSess, spawnErr := m.spawnSession(key, cols, rows, launch)
			if spawnErr != nil {
				m.mu.Unlock()
				return nil, false, spawnErr
			}
			m.sessions[key] = newSess
			delete(m.ended, key)
			delete(m.converged, key)
			delete(m.converging, key)
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

// EnsureSession starts the session for key if it does not already exist. It
// does not create a browser attachment; the session's drain goroutine still
// captures output into scrollback so a later WebSocket attach can replay it.
func (m *PTYManager) EnsureSession(key SessionKey, cols, rows uint16, argv []string) (bool, error) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false, ErrPTYManagerClosed
	}
	sess, existed := m.sessions[key]
	if !existed {
		if len(m.sessions) >= m.max {
			m.mu.Unlock()
			return false, ErrPTYMaxSessionsReached
		}
		var launch *LaunchSpec
		if len(argv) > 0 {
			launch = &LaunchSpec{Argv: argv}
		}
		newSess, spawnErr := m.spawnSession(key, cols, rows, launch)
		if spawnErr != nil {
			m.mu.Unlock()
			return false, spawnErr
		}
		m.sessions[key] = newSess
		m.mu.Unlock()
		return true, nil
	}
	m.mu.Unlock()

	_ = pty.Setsize(sess.pty, &pty.Winsize{Cols: cols, Rows: rows})
	sess.cancelKillTimer()
	return false, nil
}

// WriteToSession writes backend-owned input into a live session's PTY. The
// user sees the exact same bytes in the attached web terminal and can
// interrupt or continue from there.
func (m *PTYManager) WriteToSession(key SessionKey, p []byte) error {
	m.mu.Lock()
	sess := m.sessions[key]
	m.mu.Unlock()
	if sess == nil {
		return ErrPTYSessionNotFound
	}
	_, err := sess.pty.Write(p)
	return err
}

// spawnSession must be called with m.mu held.
func (m *PTYManager) spawnSession(key SessionKey, cols, rows uint16, launch *LaunchSpec) (*ptySession, error) {
	var useArgv []string
	if launch != nil {
		useArgv = launch.Argv
	}
	if len(useArgv) == 0 {
		useArgv = m.argv
	}
	cmd := exec.Command(m.shell, useArgv...) //nolint:gosec // shell + argv sourced from server config, not request data
	env := terminalSessionEnv(m.env, key)
	if launch != nil {
		env = overlayTerminalEnv(env, launch.Env)
	}
	cmd.Env = env
	cmd.Dir = m.cwd
	if launch != nil && strings.TrimSpace(launch.Cwd) != "" {
		cmd.Dir = launch.Cwd
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		clearTerminalCommandEnvironment(cmd, env)
		return nil, fmt.Errorf("pty.StartWithSize: %w", err)
	}
	// exec.Cmd retains Env after Start. The child has already received its own
	// copy, so discard the parent-side launch envelope immediately. This is
	// especially important for the one-use Interaction session credential.
	clearTerminalCommandEnvironment(cmd, env)

	sess := newPtySession(key, ptmx, cmd)
	go sess.drain(m)
	return sess, nil
}

func clearTerminalCommandEnvironment(cmd *exec.Cmd, env []string) {
	for index := range env {
		env[index] = ""
	}
	if cmd != nil {
		cmd.Env = nil
	}
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
		attempt := 0
		var convergeAndKill func()
		convergeAndKill = func() {
			if err := m.killSession(key, ExitReasonKilled); err != nil {
				slog.Warn("terminal detach grace could not converge lifecycle before PTY kill",
					"session", key.String(), "err", err)
				attempt++
				m.mu.Lock()
				current := m.sessions[key]
				m.mu.Unlock()
				if current == sess && attempt < beforeKillRetryAttempts && !sess.attached() {
					retryAfter := beforeKillRetryDelay(attempt)
					sess.armKillTimer(retryAfter, convergeAndKill)
				}
			}
		}
		sess.armKillTimer(grace, convergeAndKill)
	}
}

// Kill immediately terminates the session for key. Idempotent.
func (m *PTYManager) Kill(key SessionKey) error {
	return m.killSession(key, ExitReasonKilled)
}

func (m *PTYManager) killSession(key SessionKey, reason string) error {
	m.mu.Lock()
	sess, ok := m.sessions[key]
	hook := m.beforeKill
	_, ended := m.ended[key]
	durableConverged := m.converged[key]
	durableConvergenceInFlight := m.converging[key]
	m.mu.Unlock()

	// A same-key tombstone proves this manager already completed local
	// teardown. Do not let a later defense-in-depth Kill turn that committed
	// success into an availability error by re-running the durable hook.
	if !ok && ended && (durableConverged || durableConvergenceInFlight) {
		return nil
	}
	if reason != ExitReasonExited && hook != nil {
		if err := invokeBeforeKill(hook, key, reason); err != nil {
			return err
		}
	}
	if !ok {
		if ended {
			m.markDurableConverged(key)
		}
		return nil
	}

	m.mu.Lock()
	current, ok := m.sessions[key]
	if !ok || current != sess {
		m.mu.Unlock()
		return nil
	}
	delete(m.sessions, key)
	if reason != "" {
		m.ended[key] = reason
		m.converged[key] = reason != ExitReasonExited
		m.converging[key] = reason == ExitReasonExited
	}
	m.mu.Unlock()
	return sess.close(reason)
}

func invokeBeforeKill(hook BeforeKillFunc, key SessionKey, reason string) error {
	if hook == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), beforeKillTimeout)
	err := hook(ctx, key, reason)
	cancel()
	if err != nil {
		return fmt.Errorf("converge terminal lifecycle before PTY %s: %w", reason, err)
	}
	return nil
}

func beforeKillRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := beforeKillRetryBase
	for index := 1; index < attempt && delay < beforeKillRetryMax; index++ {
		delay *= 2
	}
	if delay > beforeKillRetryMax {
		return beforeKillRetryMax
	}
	return delay
}

func (m *PTYManager) retryDurableConvergence(
	key SessionKey,
	reason string,
	attempt int,
) {
	if attempt >= beforeKillRetryAttempts {
		slog.Error("terminal lifecycle convergence retries exhausted",
			"session", key.String(), "reason", reason, "attempts", attempt)
		return
	}
	time.AfterFunc(beforeKillRetryDelay(attempt), func() {
		m.mu.Lock()
		hook := m.beforeKill
		endedReason, ended := m.ended[key]
		_, live := m.sessions[key]
		if hook != nil && ended && !live && endedReason == reason {
			m.converging[key] = true
		}
		m.mu.Unlock()
		if hook == nil || !ended || live || endedReason != reason {
			return
		}
		if err := invokeBeforeKill(hook, key, reason); err != nil {
			m.finishDurableConvergence(key, false)
			slog.Warn("terminal lifecycle convergence retry failed",
				"session", key.String(), "reason", reason,
				"attempt", attempt, "err", err)
			m.retryDurableConvergence(key, reason, attempt+1)
			return
		}
		m.finishDurableConvergence(key, true)
	})
}

func (m *PTYManager) markDurableConverged(key SessionKey) {
	m.finishDurableConvergence(key, true)
}

func (m *PTYManager) finishDurableConvergence(key SessionKey, succeeded bool) {
	m.mu.Lock()
	delete(m.converging, key)
	if _, ended := m.ended[key]; ended && succeeded {
		m.converged[key] = true
	}
	m.mu.Unlock()
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

// SessionClosed reports whether key had a PTY in this process and that PTY
// has since exited or been killed. A later explicit AttachSession clears the
// tombstone after spawning a fresh PTY.
func (m *PTYManager) SessionClosed(key SessionKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.ended[key]
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
	first := !m.closed
	if first {
		m.closed = true
	}
	m.mu.Unlock()

	if first {
		close(m.reaperStop)
		m.reaperWG.Wait()
	}

	m.mu.Lock()
	keys := make([]SessionKey, 0, len(m.sessions))
	for key := range m.sessions {
		keys = append(keys, key)
	}
	m.mu.Unlock()

	var errs []error
	for _, key := range keys {
		if err := m.killSession(key, ExitReasonShutdown); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
		if err := m.killSession(key, ExitReasonKilled); err != nil {
			slog.Warn("terminal idle reaper could not converge lifecycle before PTY kill",
				"session", key.String(), "err", err)
		}
	}
}

// onSessionExited is invoked by a session's drain goroutine when the child
// process exits on its own (PTY EOF). Cleans up manager-side state.
func (m *PTYManager) onSessionExited(key SessionKey) {
	// The child is already dead. Always remove process-local state first so
	// attachments close and SessionCount/HasSession remain truthful even when
	// FleetDB is temporarily unavailable. Canonical tab metadata remains
	// durable outside the PTY manager and drives the bounded repair below.
	if err := m.killSession(key, ExitReasonExited); err != nil {
		slog.Warn("terminal natural exit local cleanup failed",
			"session", key.String(), "err", err)
	}
	m.mu.Lock()
	hook := m.beforeKill
	m.mu.Unlock()
	if hook == nil {
		m.markDurableConverged(key)
		return
	}
	if err := invokeBeforeKill(hook, key, ExitReasonExited); err != nil {
		m.finishDurableConvergence(key, false)
		slog.Warn("terminal natural exit could not converge durable lifecycle",
			"session", key.String(), "err", err)
		m.retryDurableConvergence(key, ExitReasonExited, 1)
		return
	}
	m.markDurableConverged(key)
}
