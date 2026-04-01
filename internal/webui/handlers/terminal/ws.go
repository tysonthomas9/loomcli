package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// terminalMonitor adapts TerminalManager to the realtime.SessionMonitor interface.
// Uses the raw internal tmux session name (no prefix applied).
type terminalMonitor struct {
	mgr *webuterminal.TerminalManager
}

func (m *terminalMonitor) HasSession(name string) bool { return m.mgr.HasSession(name) }
func (m *terminalMonitor) PaneDead(name string) bool   { return m.mgr.PaneDeadRaw(name) }
func (m *terminalMonitor) CapturePaneRaw(name string, lines int) string {
	return m.mgr.CapturePaneRaw(name, lines)
}

// shellCommand returns the user's default shell, falling back to /bin/bash.
func shellCommand() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/bash"
}

// attachCommandForSession returns the shell command for plain terminal tabs,
// or empty string (use manager's defaultCommand) for AI backend sessions.
func attachCommandForSession(session string) string {
	if strings.HasPrefix(session, "lead-shell-") {
		return shellCommand()
	}
	return ""
}

// terminalWSParams holds the dependencies for a terminal WebSocket handler.
type terminalWSParams struct {
	manager               *webuterminal.TerminalManager
	auth                  *realtime.TerminalAuth
	patterns              []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
}

// HandleTerminalWS returns a WebSocket handler for terminal relay.
// It upgrades HTTP connections to WebSocket, bridges them to tmux sessions
// via the TerminalManager, and handles bidirectional binary data relay
// plus an in-band resize protocol. The manager's current default command
// is used for new terminal sessions.
//
// allowedOrigins is a list of full origin URLs (e.g. "http://localhost:3000")
// whose host portions are used as OriginPatterns for the WebSocket upgrade.
// When nil or empty, only same-origin and non-browser (no Origin header)
// connections are accepted.
func HandleTerminalWS(manager *webuterminal.TerminalManager, auth *realtime.TerminalAuth, allowedOrigins []string, loomServerURL string, workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error), tabMetaStore *tabmeta.Store, hub *realtime.Hub) http.HandlerFunc {
	// Compute origin host patterns once at construction time.
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

// validateTerminalWSRequest validates the session parameter, auth token, and session limit.
// Returns the session name, workspace ID, and true on success.
func validateTerminalWSRequest(w http.ResponseWriter, r *http.Request, manager *webuterminal.TerminalManager, auth *realtime.TerminalAuth) (string, string, bool) {
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

	if manager.SessionIsBeingKilled(session) {
		handler.WriteJSON(w, http.StatusConflict, map[string]interface{}{
			"success": false, "error": "session is being killed",
		})
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
// Returns true if authentication succeeds or auth is nil, false otherwise.
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

// runTerminalRelay attaches to the tmux session and runs the bidirectional relay.
func runTerminalRelay(reqCtx context.Context, conn *websocket.Conn, p *terminalWSParams, session, workspace string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	// Check whether the tmux session already exists before Attach creates it.
	// Only inject the context banner for freshly created talk-to-lead sessions.
	isNewSession := session == "talk-to-lead" && !p.manager.SessionExists(session)

	termSession, err := p.manager.Attach(session, attachCommandForSession(session), 80, 24)
	if err != nil {
		if errors.Is(err, webuterminal.ErrSessionBeingKilled) {
			return websocket.StatusCode(wsCloseSessionKilled), "session is being killed" //nolint:staticcheck // SA1019: websocket migration tracked separately
		}
		if errors.Is(err, webuterminal.ErrMaxSessionsReached) {
			slog.Info("terminal session limit reached", "session", session)
		} else {
			slog.Error("failed to attach terminal session", "session", session, "err", err)
		}
		return websocket.StatusInternalError, err.Error()
	}
	connID := termSession.ConnID

	if workspace != "" {
		p.manager.SetSessionOwner(session, workspace)
	}

	if isNewSession && p.loomServerURL != "" {
		wsID := middleware.WorkspaceFromContext(reqCtx)
		wsConfigFn := func() (*ops.WorkspaceData, error) { return p.workspaceConfigByIDFn(wsID) }
		injectTerminalContextBanner(termSession, p.loomServerURL, wsConfigFn)
	}

	realtime.BroadcastSessionIssueEvent(p.tabMetaStore, p.hub, workspace, session)

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	watchSessionKill(ctx, cancel, conn, termSession)

	crashCh := make(chan realtime.CrashInfo, 1)
	scrollback := p.manager.GetScrollbackBuffer(session)
	monitor := &terminalMonitor{mgr: p.manager}
	go func() {
		result := realtime.PtyToWS(ctx, cancel, conn, termSession.PTY, termSession.Name, monitor, scrollback)
		crashCh <- result
	}()

	realtime.WSToPTY(ctx, conn, termSession.PTY, p.manager, connID)

	if err := p.manager.Detach(connID); err != nil {
		slog.Error("failed to detach terminal connection", "conn_id", connID, "err", err)
	}

	return (<-crashCh).WSClose()
}

// wsCloseSessionKilled is the WebSocket close code for a user-initiated session kill.
const wsCloseSessionKilled = 4002

// watchSessionKill closes the WebSocket when the tmux session is killed server-side.
func watchSessionKill(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, termSession *webuterminal.TerminalSession) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	go func() {
		select {
		case <-termSession.KillCh():
			_ = conn.Close(websocket.StatusCode(wsCloseSessionKilled), "session killed") //nolint:staticcheck // SA1019: websocket migration tracked separately
			cancel()
		case <-ctx.Done():
		}
	}()
}

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the terminal session's PTY.
func injectTerminalContextBanner(session *webuterminal.TerminalSession, loomServerURL string, workspaceConfigFn func() (*ops.WorkspaceData, error)) {
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
	if _, writeErr := session.PTY.Write([]byte(banner)); writeErr != nil {
		slog.Warn("failed to write context banner to pty", "err", writeErr)
	}
}
