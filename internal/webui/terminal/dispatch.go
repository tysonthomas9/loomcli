package terminal

import (
	"log/slog"
	"sync"
)

// AgentKind enumerates the runtime backend that owns a session. Kept as a
// terminal-package-local alias for the control-plane proto's AgentKind enum
// so callers don't have to import cpb just to classify a session.
type AgentKind int

const (
	// AgentEphemeral routes to the in-process MultiPTYManager (default).
	AgentEphemeral AgentKind = iota
	// AgentPersistent routes to the agentd-backed gRPC PTYSource.
	AgentPersistent
)

// Dispatcher is a PTYSource that forwards each call to one of two underlying
// PTYSource implementations based on a per-key classification function. It
// exists so the WebSocket terminal handler can stay backend-agnostic while
// the loomcli process picks ephemeral vs persistent on a per-session basis.
//
// The "default off" path — Dispatcher with persistent == nil — guarantees
// every call lands on ephemeral with arguments unchanged, which is the
// regression contract Phase 5 commits to.
type Dispatcher struct {
	ephemeral  PTYSource
	persistent PTYSource // may be nil if feature flag off
	classify   func(SessionKey) AgentKind

	// warnedFallback tracks (workspace, name) tuples we've already logged a
	// "persistent-classified but no persistent backend" warning for. Stops
	// the log from spamming on a steady stream of misconfigured attaches.
	mu             sync.Mutex
	warnedFallback map[SessionKey]struct{}
}

// NewDispatcher constructs a Dispatcher. ephemeral must be non-nil; persistent
// may be nil to disable the persistent backend (feature flag off). classify
// may be nil — in that case all keys are treated as ephemeral, which is the
// safe default and what regression tests for the off path rely on.
func NewDispatcher(ephemeral, persistent PTYSource, classify func(SessionKey) AgentKind) *Dispatcher {
	return &Dispatcher{
		ephemeral:      ephemeral,
		persistent:     persistent,
		classify:       classify,
		warnedFallback: make(map[SessionKey]struct{}),
	}
}

// pickFor returns the backend that should handle key, defaulting to the
// ephemeral path. If classify says persistent but persistent==nil (operator
// misconfig), falls back to ephemeral and emits a warning at most once per
// (workspace, name) tuple — user-visible behavior is "got a local shell
// instead of the persistent agent", which is recoverable, so we don't error.
func (d *Dispatcher) pickFor(key SessionKey) PTYSource {
	if d.classify == nil || d.classify(key) != AgentPersistent {
		return d.ephemeral
	}
	if d.persistent == nil {
		d.warnFallbackOnce(key)
		return d.ephemeral
	}
	return d.persistent
}

// warnFallbackOnce emits the persistent-classified-but-no-backend warning
// at most once per (workspace, name) tuple.
func (d *Dispatcher) warnFallbackOnce(key SessionKey) {
	d.mu.Lock()
	if _, seen := d.warnedFallback[key]; seen {
		d.mu.Unlock()
		return
	}
	d.warnedFallback[key] = struct{}{}
	d.mu.Unlock()

	slog.Warn("persistent-classified session falling back to ephemeral backend; check LOOM_ENABLE_PERSISTENT_AGENTS / control-plane wiring",
		"component", "terminal",
		"workspace", key.Workspace,
		"name", key.Name,
	)
}

// AttachSession routes the attach to the chosen backend.
func (d *Dispatcher) AttachSession(key SessionKey, cols, rows uint16, argv []string) (Attachment, bool, error) {
	return d.pickFor(key).AttachSession(key, cols, rows, argv)
}

// Detach forwards to the chosen backend.
func (d *Dispatcher) Detach(key SessionKey, connID string) {
	d.pickFor(key).Detach(key, connID)
}

// Kill forwards to the chosen backend.
func (d *Dispatcher) Kill(key SessionKey) error {
	return d.pickFor(key).Kill(key)
}

// HasSession forwards to the chosen backend.
func (d *Dispatcher) HasSession(key SessionKey) bool {
	return d.pickFor(key).HasSession(key)
}

// AttachmentCount forwards to the chosen backend.
func (d *Dispatcher) AttachmentCount(key SessionKey) int {
	return d.pickFor(key).AttachmentCount(key)
}

// SessionCount returns the ephemeral backend's session count. In dispatch
// mode, server-wide metrics reflect the ephemeral backend; persistent
// metrics live on AgentdClient itself and are aggregated by the
// control-plane operator dashboards rather than the loomcli process.
func (d *Dispatcher) SessionCount() int {
	return d.ephemeral.SessionCount()
}

// SessionCountFor returns the live-session count for wsID against whichever
// backend is responsible for the workspace. Per-workspace gates need the
// right backend's tally — counting persistent sessions against the local
// PTY cap (or vice versa) would lock out users wrongly. We use the
// classify callback with a sentinel SessionKey to avoid asking each
// caller to pre-compute the workspace's kind.
func (d *Dispatcher) SessionCountFor(wsID string) int {
	if d.persistent != nil && d.classify != nil {
		// Sentinel SessionKey: empty Name signals "workspace-level kind
		// query" to classify implementations that key off Workspace. Most
		// classifiers only care about Workspace anyway.
		if d.classify(SessionKey{Workspace: wsID}) == AgentPersistent {
			return d.persistent.SessionCountFor(wsID)
		}
	}
	return d.ephemeral.SessionCountFor(wsID)
}

// MaxSessions returns the ephemeral backend's cap. In dispatch mode, metrics
// reflect the ephemeral backend; persistent metrics live on AgentdClient.
func (d *Dispatcher) MaxSessions() int {
	return d.ephemeral.MaxSessions()
}

// Compile-time assertion that Dispatcher satisfies PTYSource.
var _ PTYSource = (*Dispatcher)(nil)
