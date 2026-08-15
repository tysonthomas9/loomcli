package terminal

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// ErrInvalidWorkspacePath is returned when a workspace's directory path fails
// os.Stat or is not a directory. Produced by Register (pre-store) and, as a
// defense-in-depth branch, by managerForWS.
var ErrInvalidWorkspacePath = errors.New("invalid workspace path")

// ErrWorkspaceNotRegistered is returned when no workspace entry exists for
// the requested ID. Callers can errors.Is against this to surface a clean
// 404 from the terminal websocket handler.
var ErrWorkspaceNotRegistered = errors.New("workspace not registered")

// ErrPTYManagerClosed is returned by Register when the MultiPTYManager has
// already been closed via Close(), and by PTYManager.AttachSession once its
// Shutdown has run. Callers can errors.Is against this to detect "workspace
// was deleted / server is shutting down" during an in-flight attach.
var ErrPTYManagerClosed = errors.New("pty manager closed")

// wsEntry holds the per-workspace state kept by a MultiPTYManager. The
// per-workspace PTYManager is nil until first AttachSession; lazy-create
// is guarded by MultiPTYManager.mu.
type wsEntry struct {
	path string
	mgr  *PTYManager
}

// MultiPTYManager dispatches PTYSource calls to a per-workspace *PTYManager
// based on SessionKey.Workspace. Per-workspace managers are lazily
// constructed on first AttachSession using the validated workspace path as
// cwd, giving each workspace its own isolated session cap and a shell that
// starts inside workspace.Path rather than $HOME.
type MultiPTYManager struct {
	mu      sync.RWMutex
	entries map[string]*wsEntry

	// cmd and maxPerWS are set at construction and never mutated — readable
	// without holding mu.
	cmd      string
	maxPerWS int

	gracePeriod time.Duration
	idleTimeout time.Duration

	closed bool
}

// NewMultiPTYManager constructs a MultiPTYManager. cmd is the default shell
// command passed to every per-workspace PTYManager (see NewPTYManager).
// maxPerWS is the per-workspace concurrent-session cap; values <= 0 use the
// PTYManager default.
func NewMultiPTYManager(cmd string, maxPerWS int) *MultiPTYManager {
	return &MultiPTYManager{
		entries:  make(map[string]*wsEntry),
		cmd:      cmd,
		maxPerWS: maxPerWS,
	}
}

// Register associates a workspace ID with a directory path. The path is
// validated via os.Stat and must be an existing directory; on failure,
// Register returns an error wrapping ErrInvalidWorkspacePath and stores
// nothing. Re-registering an existing wsID shuts down its current
// PTYManager (if created) and replaces the entry.
func (mm *MultiPTYManager) Register(wsID, path string) error {
	if err := validateWorkspaceRegistration(wsID, path); err != nil {
		return err
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	if mm.closed {
		return ErrPTYManagerClosed
	}

	if existing, ok := mm.entries[wsID]; ok {
		slog.Info("replacing existing pty manager for workspace", "workspace", wsID)
		if existing.mgr != nil {
			if err := existing.mgr.Shutdown(); err != nil {
				slog.Warn("shutting down replaced pty manager", "workspace", wsID, "err", err)
			}
		}
	}

	mm.entries[wsID] = &wsEntry{path: path}
	slog.Info("registered workspace pty manager", "workspace", wsID, "path", path)
	return nil
}

// EnsureRegistered associates a workspace ID with a directory path when it is
// not already registered. Unlike Register, an existing entry with the same path
// is left intact so live PTY sessions are not disrupted by just-in-time terminal
// route healing. If the existing path differs, it replaces the entry to keep the
// terminal cwd aligned with local state.
func (mm *MultiPTYManager) EnsureRegistered(wsID, path string) error {
	if err := validateWorkspaceRegistration(wsID, path); err != nil {
		return err
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	if mm.closed {
		return ErrPTYManagerClosed
	}

	if existing, ok := mm.entries[wsID]; ok {
		if existing.path == path {
			return nil
		}
		slog.Info("replacing existing pty manager for workspace after path change", "workspace", wsID)
		if existing.mgr != nil {
			if err := existing.mgr.Shutdown(); err != nil {
				slog.Warn("shutting down replaced pty manager", "workspace", wsID, "err", err)
			}
		}
	}

	mm.entries[wsID] = &wsEntry{path: path}
	slog.Info("registered workspace pty manager", "workspace", wsID, "path", path)
	return nil
}

func validateWorkspaceRegistration(wsID, path string) error {
	if wsID == "" {
		return fmt.Errorf("%w: empty workspace id", ErrWorkspaceNotRegistered)
	}
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidWorkspacePath)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrInvalidWorkspacePath, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q: not a directory", ErrInvalidWorkspacePath, path)
	}
	return nil
}

// Deregister removes the workspace entry and terminates all of its live PTY
// sessions. Unknown wsIDs are a no-op. Safe to call concurrently with
// dispatch methods; in-flight AttachSession on the same wsID either races to
// a managed shutdown or observes ErrWorkspaceNotRegistered on its next
// managerForWS call.
func (mm *MultiPTYManager) Deregister(wsID string) {
	mm.mu.Lock()
	entry, ok := mm.entries[wsID]
	if ok {
		delete(mm.entries, wsID)
	}
	mm.mu.Unlock()

	if !ok {
		return
	}
	if entry.mgr != nil {
		if err := entry.mgr.Shutdown(); err != nil {
			slog.Warn("shutting down pty manager on deregister", "workspace", wsID, "err", err)
		}
	}
	slog.Info("deregistered workspace pty manager", "workspace", wsID)
}

// Close shuts down every live per-workspace PTYManager and prevents further
// Register calls. Idempotent: subsequent Close returns nil.
func (mm *MultiPTYManager) Close() error {
	mm.mu.Lock()
	if mm.closed {
		mm.mu.Unlock()
		return nil
	}
	mm.closed = true
	entries := mm.entries
	mm.entries = make(map[string]*wsEntry)
	mm.mu.Unlock()

	var errs []error
	for wsID, entry := range entries {
		if entry.mgr != nil {
			if err := entry.mgr.Shutdown(); err != nil {
				errs = append(errs, fmt.Errorf("shutting down workspace %q: %w", wsID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// SetGracePeriod overrides the post-detach grace period both for managers
// created in the future AND for every per-workspace manager already created.
func (mm *MultiPTYManager) SetGracePeriod(d time.Duration) {
	mm.mu.Lock()
	mm.gracePeriod = d
	existing := mm.snapshotManagersLocked()
	mm.mu.Unlock()

	for _, m := range existing {
		m.SetGracePeriod(d)
	}
}

// SetIdleTimeout overrides the idle-reap threshold both for managers created
// in the future AND for every per-workspace manager already created.
func (mm *MultiPTYManager) SetIdleTimeout(d time.Duration) {
	mm.mu.Lock()
	mm.idleTimeout = d
	existing := mm.snapshotManagersLocked()
	mm.mu.Unlock()

	for _, m := range existing {
		m.SetIdleTimeout(d)
	}
}

// snapshotManagersLocked returns a slice of all currently-created
// per-workspace managers. Caller must hold mm.mu. Used to drop mm.mu before
// calling into PTYManager.mu, avoiding any cross-lock ordering dependency.
func (mm *MultiPTYManager) snapshotManagersLocked() []*PTYManager {
	out := make([]*PTYManager, 0, len(mm.entries))
	for _, e := range mm.entries {
		if e.mgr != nil {
			out = append(out, e.mgr)
		}
	}
	return out
}

// GracePeriod returns the currently configured post-detach grace period.
func (mm *MultiPTYManager) GracePeriod() time.Duration {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.gracePeriod
}

// IdleTimeout returns the currently configured idle-reap threshold.
func (mm *MultiPTYManager) IdleTimeout() time.Duration {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	return mm.idleTimeout
}

// managerForWS returns the per-workspace PTYManager for wsID, creating it
// lazily on first access. Uses double-checked locking: the fast path takes
// RLock, the slow path takes WLock only if the per-workspace manager has not
// yet been constructed.
func (mm *MultiPTYManager) managerForWS(wsID string) (*PTYManager, error) {
	if wsID == "" {
		return nil, fmt.Errorf("%w: empty workspace id", ErrWorkspaceNotRegistered)
	}

	mm.mu.RLock()
	entry := mm.entries[wsID]
	if entry != nil && entry.mgr != nil {
		m := entry.mgr
		mm.mu.RUnlock()
		return m, nil
	}
	mm.mu.RUnlock()

	if entry == nil {
		return nil, fmt.Errorf("%w: %q", ErrWorkspaceNotRegistered, wsID)
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	// Re-read entry: an intervening Deregister could have removed it.
	entry = mm.entries[wsID]
	if entry == nil {
		return nil, fmt.Errorf("%w: %q", ErrWorkspaceNotRegistered, wsID)
	}
	if entry.mgr != nil {
		return entry.mgr, nil
	}
	if entry.path == "" {
		return nil, fmt.Errorf("%w: empty path for %q", ErrInvalidWorkspacePath, wsID)
	}

	// Construct and initialize the manager before publishing it to
	// entry.mgr. While we hold mm.mu, no other goroutine can observe this
	// manager, so its own lock is uncontended — there is no cross-lock
	// ordering concern here even though SetGracePeriod acquires PTYManager.mu.
	m := NewPTYManager(mm.cmd, mm.maxPerWS, entry.path)
	if mm.gracePeriod != 0 {
		m.SetGracePeriod(mm.gracePeriod)
	}
	if mm.idleTimeout != 0 {
		m.SetIdleTimeout(mm.idleTimeout)
	}
	entry.mgr = m
	return m, nil
}

// existingManagerForWS returns the per-workspace PTYManager only if it has
// already been created. Used by no-op-on-miss dispatch methods (Detach,
// Kill, HasSession, AttachmentCount) to avoid resurrecting a deregistered
// workspace via a late call.
func (mm *MultiPTYManager) existingManagerForWS(wsID string) *PTYManager {
	if wsID == "" {
		return nil
	}
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	entry := mm.entries[wsID]
	if entry == nil {
		return nil
	}
	return entry.mgr
}

// AttachSession routes to the per-workspace PTYManager, creating it lazily
// if necessary. Returns ErrWorkspaceNotRegistered if key.Workspace is empty
// or unknown.
func (mm *MultiPTYManager) AttachSession(key SessionKey, cols, rows uint16, launch *tabmeta.LaunchSpec) (Attachment, bool, error) {
	m, err := mm.managerForWS(key.Workspace)
	if err != nil {
		return nil, false, err
	}
	return m.AttachSession(key, cols, rows, launch)
}

// EnsureSession routes backend-owned session startup to the per-workspace
// manager, creating it lazily if necessary.
func (mm *MultiPTYManager) EnsureSession(key SessionKey, cols, rows uint16, argv []string) (bool, error) {
	m, err := mm.managerForWS(key.Workspace)
	if err != nil {
		return false, err
	}
	return m.EnsureSession(key, cols, rows, argv)
}

// WriteToSession writes backend-owned input to an existing per-workspace PTY.
func (mm *MultiPTYManager) WriteToSession(key SessionKey, p []byte) error {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return fmt.Errorf("%w: %q", ErrWorkspaceNotRegistered, key.Workspace)
	}
	return m.WriteToSession(key, p)
}

// Detach releases the attachment. No-op for unknown workspaces or
// workspaces whose per-workspace manager has not been created yet.
func (mm *MultiPTYManager) Detach(key SessionKey, connID string) {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return
	}
	m.Detach(key, connID)
}

// Kill terminates the session. Returns nil for unknown workspaces.
func (mm *MultiPTYManager) Kill(key SessionKey) error {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return nil
	}
	return m.Kill(key)
}

// HasSession reports whether a live session exists for key. Returns false
// for unknown workspaces.
func (mm *MultiPTYManager) HasSession(key SessionKey) bool {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return false
	}
	return m.HasSession(key)
}

// SessionClosed reports whether a session existed in the current per-workspace
// manager and has since exited or been killed.
func (mm *MultiPTYManager) SessionClosed(key SessionKey) bool {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return false
	}
	return m.SessionClosed(key)
}

// AttachmentCount returns the number of concurrent attachments for key.
// Returns 0 for unknown workspaces.
func (mm *MultiPTYManager) AttachmentCount(key SessionKey) int {
	m := mm.existingManagerForWS(key.Workspace)
	if m == nil {
		return 0
	}
	return m.AttachmentCount(key)
}

// SessionCount returns the sum of live sessions across every per-workspace
// manager that has been created.
func (mm *MultiPTYManager) SessionCount() int {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	total := 0
	for _, e := range mm.entries {
		if e.mgr != nil {
			total += e.mgr.SessionCount()
		}
	}
	return total
}

// SessionCountFor returns the number of live sessions owned by the
// per-workspace PTYManager for wsID. Returns 0 when the workspace is
// unknown or its per-workspace manager has not yet been lazily created.
func (mm *MultiPTYManager) SessionCountFor(wsID string) int {
	m := mm.existingManagerForWS(wsID)
	if m == nil {
		return 0
	}
	return m.SessionCount()
}

// SessionNamesFor satisfies PTYSessionLister by delegating to the
// per-workspace PTYManager for wsID. Returns an empty slice when the
// workspace is unknown or its manager has not yet been lazily created.
func (mm *MultiPTYManager) SessionNamesFor(wsID string) []string {
	m := mm.existingManagerForWS(wsID)
	if m == nil {
		return []string{}
	}
	return m.SessionNamesFor(wsID)
}

// MaxSessions returns the per-workspace session cap. Intentionally not a
// sum across workspaces: the UI status gauge that consumes this value wants
// the cap a single workspace is measured against.
func (mm *MultiPTYManager) MaxSessions() int {
	if mm.maxPerWS <= 0 {
		return defaultPTYMaxSessions
	}
	return mm.maxPerWS
}

// hasManager reports whether a per-workspace PTYManager has been constructed
// for wsID. Test-only helper: used to assert lazy-create semantics.
//
// can query multiple workspaces.
//
//nolint:unparam // test-only helper; wsID is parameterized so future tests
func (mm *MultiPTYManager) hasManager(wsID string) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	e := mm.entries[wsID]
	return e != nil && e.mgr != nil
}
