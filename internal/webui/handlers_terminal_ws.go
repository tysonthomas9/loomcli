package webui

import (
	"context"
	"errors"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// terminalMonitor adapts TerminalManager to the realtime.SessionMonitor interface.
// Uses the raw internal tmux session name (no prefix applied).
type terminalMonitor struct {
	mgr *TerminalManager
}

func (m *terminalMonitor) HasSession(name string) bool { return m.mgr.tmuxHasSession(name) }
func (m *terminalMonitor) PaneDead(name string) bool   { return m.mgr.paneDead(name) }
func (m *terminalMonitor) CapturePaneRaw(name string, lines int) string {
	return m.mgr.capturePaneRaw(name, lines)
}

// handleTerminalWS returns a WebSocket handler for terminal relay.
// It upgrades HTTP connections to WebSocket, bridges them to tmux sessions
// via the TerminalManager, and handles bidirectional binary data relay
// plus an in-band resize protocol. The manager's current default command
// is used for new terminal sessions.
//
// allowedOrigins is a list of full origin URLs (e.g. "http://localhost:3000")
// whose host portions are used as OriginPatterns for the WebSocket upgrade.
// When nil or empty, only same-origin and non-browser (no Origin header)
// connections are accepted.
func handleTerminalWS(manager *TerminalManager, auth *realtime.TerminalAuth, allowedOrigins []string, loomServerURL string, workspaceConfigByIDFn func(string) (*service.WorkspaceData, error), tabMetaStore *tabmeta.Store, hub *realtime.Hub) http.HandlerFunc {
	// Compute origin host patterns once at construction time.
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		// Check if manager is available
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		// Parse session parameter. Workspace is derived from the URL path
		// via WorkspaceMiddleware (injected into context).
		session := r.URL.Query().Get("session")
		workspace := middleware.WorkspaceFromContext(r.Context())
		if session == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "missing session parameter",
			})
			return
		}

		if !validTerminalSession.MatchString(session) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid session name: must match [a-zA-Z0-9_-]+",
			})
			return
		}

		// Validate one-time terminal token before WebSocket upgrade
		if auth != nil {
			token := r.URL.Query().Get("token")
			userID, err := auth.ValidateToken(token, session)
			if err != nil {
				respondJSON(w, http.StatusUnauthorized, map[string]interface{}{
					"success": false,
					"error":   "terminal authentication failed",
				})
				logger.Warn("terminal auth failed", "session", session, "err", err)
				return
			}
			if userID != "" {
				logger.Info("terminal session authenticated", "session", session, "user_id", userID)
			}
		}

		// Pre-upgrade check: reject before WebSocket upgrade if at session limit.
		if manager.SessionCount() >= manager.MaxSessions() {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "maximum terminal sessions reached",
			})
			return
		}

		// Disable write timeout for this long-lived WebSocket connection.
		// Must be called before websocket.Accept which hijacks the connection.
		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			logger.Warn("terminal ws: failed to disable write deadline", "err", err)
		}

		// Accept WebSocket upgrade with origin validation.
		// OriginPatterns checks the Origin header against allowed hosts.
		// If no Origin header is present (non-browser clients), the connection is allowed.
		// If Origin matches the request Host (same-origin), the connection is allowed.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: patterns,
		})
		if err != nil {
			logger.Error("failed to accept websocket", "err", err)
			return
		}
		conn.SetReadLimit(realtime.WSReadLimit)

		// Track close status for deferred cleanup
		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason)
		}()

		// Check whether the tmux session already exists before Attach creates it.
		// Only inject the context banner for freshly created talk-to-lead sessions.
		isNewSession := session == "talk-to-lead" && !manager.SessionExists(session)

		// Attach to terminal session with default 80x24 size
		// (frontend sends resize immediately after connect)
		termSession, err := manager.Attach(session, attachCommandForSession(session), 80, 24)
		if err != nil {
			if errors.Is(err, ErrMaxSessionsReached) {
				logger.Info("terminal session limit reached", "session", session)
			} else {
				logger.Error("failed to attach terminal session", "session", session, "err", err)
			}
			closeReason = err.Error()
			return
		}
		connID := termSession.ConnID

		// Record workspace ownership (first-write-wins — no-op if already set by spawn handler).
		if workspace != "" {
			manager.SetSessionOwner(session, workspace)
		}

		// Inject project context banner into freshly created talk-to-lead sessions.
		if isNewSession && loomServerURL != "" {
			wsID := middleware.WorkspaceFromContext(r.Context())
			wsConfigFn := func() (*service.WorkspaceData, error) { return workspaceConfigByIDFn(wsID) }
			injectTerminalContextBanner(termSession, loomServerURL, wsConfigFn)
		}

		// Broadcast SSE event if this session is linked to an issue, so indicators update on connect.
		realtime.BroadcastSessionIssueEvent(tabMetaStore, hub, workspace, session)

		// Create context for coordinating goroutines
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Channel to signal when PTY reader finishes and communicate crash state
		crashCh := make(chan realtime.CrashInfo, 1)

		// Start PTY -> WebSocket goroutine (with scrollback capture)
		scrollback := manager.GetScrollbackBuffer(session)
		monitor := &terminalMonitor{mgr: manager}
		go func() {
			result := realtime.PtyToWS(ctx, cancel, conn, termSession.PTY, termSession.Name, monitor, scrollback)
			crashCh <- result
		}()

		// Run WebSocket -> PTY relay (blocks until WebSocket closes)
		realtime.WSToPTY(ctx, conn, termSession.PTY, manager, connID)

		// WebSocket closed - detach connection to close PTY and unblock PtyToWS
		// (Detach closes the PTY, causing the Read in PtyToWS to return an error)
		if err := manager.Detach(connID); err != nil {
			logger.Error("failed to detach terminal connection", "conn_id", connID, "err", err)
		}

		// Wait for PTY reader to finish and check for backend crash
		closeStatus, closeReason = (<-crashCh).WSClose()
	}
}

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the terminal session's PTY.
func injectTerminalContextBanner(session *TerminalSession, loomServerURL string, workspaceConfigFn func() (*service.WorkspaceData, error)) {
	tc, err := FetchTerminalContext(loomServerURL)
	if err != nil {
		logger.Error("terminal context fetch failed, skipping banner", "err", err)
		return
	}

	var wsName string
	if workspaceConfigFn != nil {
		if wsData, wsErr := workspaceConfigFn(); wsErr == nil && wsData != nil {
			wsName = wsData.Name
		} else if wsErr != nil {
			logger.Warn("workspace config unavailable for terminal context", "err", wsErr)
		}
	}

	banner := FormatContextBanner(tc, wsName)
	if _, writeErr := session.PTY.Write([]byte(banner)); writeErr != nil {
		logger.Warn("failed to write context banner to pty", "err", writeErr)
	}
}
