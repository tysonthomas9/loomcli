package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// terminalWSParams holds the dependencies for a terminal WebSocket handler.
type terminalWSParams struct {
	manager               *webuterminal.PTYManager
	auth                  *realtime.TerminalAuth
	patterns              []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
}

// HandleTerminalWS returns a WebSocket handler for terminal relay. It upgrades
// the HTTP connection to a WebSocket, spawns a fresh PTY-backed shell via
// PTYManager, and bridges stdin/stdout bidirectionally using the wterm wire
// format: binary output frames and a \x1b[RESIZE:cols;rows] escape for resize.
//
// allowedOrigins is a list of full origin URLs (e.g. "http://localhost:3000")
// whose host portions are used as OriginPatterns for the WebSocket upgrade.
// When nil or empty, only same-origin and non-browser (no Origin header)
// connections are accepted.
func HandleTerminalWS(manager *webuterminal.PTYManager, auth *realtime.TerminalAuth, allowedOrigins []string, loomServerURL string, workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error), tabMetaStore *tabmeta.Store, hub *realtime.Hub) http.HandlerFunc {
	p := &terminalWSParams{
		manager:               manager,
		auth:                  auth,
		patterns:              originHosts(allowedOrigins),
		loomServerURL:         loomServerURL,
		workspaceConfigByIDFn: workspaceConfigByIDFn,
		tabMetaStore:          tabMetaStore,
		hub:                   hub,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		session, workspace, ok := validateTerminalWSRequest(w, r, p.manager, p.auth)
		if !ok {
			return
		}

		conn, ok := upgradeTerminalWS(w, r, p.patterns)
		if !ok {
			return
		}

		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason) //nolint:staticcheck // SA1019: websocket migration tracked separately
		}()

		closeStatus, closeReason = runTerminalRelay(r.Context(), conn, p, session, workspace)
	}
}

// validateTerminalWSRequest validates the session parameter, auth token, and
// session limit. Returns (session, workspace, true) on success.
func validateTerminalWSRequest(w http.ResponseWriter, r *http.Request, manager *webuterminal.PTYManager, auth *realtime.TerminalAuth) (string, string, bool) {
	if manager == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "terminal manager not initialized",
		})
		return "", "", false
	}

	session := r.URL.Query().Get("session")
	workspace := middleware.WorkspaceFromContext(r.Context())
	if session == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "missing session parameter",
		})
		return "", "", false
	}
	if !validTerminalSession.MatchString(session) {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid session name: must match [a-zA-Z0-9_-]+",
		})
		return "", "", false
	}

	if !authenticateTerminalSession(w, r, auth, session) {
		return "", "", false
	}

	if manager.SessionCount() >= manager.MaxSessions() {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "maximum terminal sessions reached",
		})
		return "", "", false
	}
	return session, workspace, true
}

// authenticateTerminalSession validates the one-time terminal token if auth is configured.
func authenticateTerminalSession(w http.ResponseWriter, r *http.Request, auth *realtime.TerminalAuth, session string) bool {
	if auth == nil {
		return true
	}
	token := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(token, session)
	if err != nil {
		handler.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false, "error": "terminal authentication failed",
		})
		slog.Warn("terminal auth failed", "session", session, "err", err)
		return false
	}
	if userID != "" {
		slog.Info("terminal session authenticated", "session", session, "user_id", userID)
	}
	return true
}

// upgradeTerminalWS performs the WebSocket upgrade for a terminal connection.
func upgradeTerminalWS(w http.ResponseWriter, r *http.Request, patterns []string) (*websocket.Conn, bool) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("terminal ws: failed to disable write deadline", "err", err)
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{ //nolint:staticcheck // SA1019: websocket migration tracked separately
		OriginPatterns: patterns,
	})
	if err != nil {
		slog.Error("failed to accept websocket", "err", err)
		return nil, false
	}
	conn.SetReadLimit(realtime.WSReadLimit) //nolint:staticcheck // SA1019: websocket migration tracked separately
	return conn, true
}

// runTerminalRelay opens a fresh PTY and runs the bidirectional relay until
// the WebSocket or the shell exits.
func runTerminalRelay(reqCtx context.Context, conn *websocket.Conn, p *terminalWSParams, session, workspace string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	ptyConn, err := p.manager.Open(80, 24, webuterminal.ArgvForSession(session))
	if err != nil {
		if errors.Is(err, webuterminal.ErrPTYMaxSessionsReached) {
			slog.Info("terminal session limit reached", "session", session)
		} else {
			slog.Error("failed to open pty", "session", session, "err", err)
		}
		return websocket.StatusInternalError, err.Error()
	}
	connID := ptyConn.ConnID

	// Freshly opened "talk-to-lead" sessions get a project context banner
	// written to the PTY before the user sees the shell prompt. Every open
	// is a fresh shell in the PTYManager model, so the banner runs every
	// time the tab is opened — that matches user expectation (you see the
	// current project status when you open the session).
	if session == "talk-to-lead" && p.loomServerURL != "" {
		wsID := middleware.WorkspaceFromContext(reqCtx)
		wsConfigFn := func() (*ops.WorkspaceData, error) {
			if p.workspaceConfigByIDFn == nil {
				return nil, errors.New("no workspace config resolver")
			}
			return p.workspaceConfigByIDFn(wsID)
		}
		injectTerminalContextBanner(ptyConn, p.loomServerURL, wsConfigFn)
	}

	realtime.BroadcastSessionIssueEvent(p.tabMetaStore, p.hub, workspace, session)

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	crashCh := make(chan realtime.CrashInfo, 1)
	go func() {
		// monitor=nil: there is no tmux to inspect. PtyToWS treats PTY EOF
		// as a clean shell exit, not a crash.
		result := realtime.PtyToWS(ctx, cancel, conn, ptyConn.PTY, connID, nil, nil)
		crashCh <- result
	}()

	realtime.WSToPTY(ctx, conn, ptyConn.PTY, p.manager, connID)

	if err := p.manager.Detach(connID); err != nil {
		slog.Error("failed to detach pty connection", "conn_id", connID, "err", err)
	}

	return (<-crashCh).WSClose()
}

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the newly opened PTY.
func injectTerminalContextBanner(ptyConn *webuterminal.PTYConn, loomServerURL string, workspaceConfigFn func() (*ops.WorkspaceData, error)) {
	tc, err := webuterminal.FetchTerminalContext(loomServerURL)
	if err != nil {
		slog.Error("terminal context fetch failed, skipping banner", "err", err)
		return
	}

	var wsName string
	if workspaceConfigFn != nil {
		if wsData, wsErr := workspaceConfigFn(); wsErr == nil && wsData != nil {
			wsName = wsData.Name
		} else if wsErr != nil {
			slog.Warn("workspace config unavailable for terminal context", "err", wsErr)
		}
	}

	banner := webuterminal.FormatContextBanner(tc, wsName)
	if _, writeErr := ptyConn.PTY.Write([]byte(banner)); writeErr != nil {
		slog.Warn("failed to write context banner to pty", "err", writeErr)
	}
}
