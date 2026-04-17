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
	manager               webuterminal.PTYSource
	auth                  *realtime.TerminalAuth
	patterns              []string
	loomServerURL         string
	workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error)
	tabMetaStore          *tabmeta.Store
	hub                   *realtime.Hub
}

// HandleTerminalWS returns a WebSocket handler for terminal relay. It upgrades
// the HTTP connection to a WebSocket, attaches to (or creates) the persistent
// session identified by the (workspace, session) pair, and bridges
// stdin/stdout bidirectionally using the wterm wire format: binary output
// frames and a \x1b[RESIZE:cols;rows] escape for resize.
//
// Unlike the previous PTY-per-WS model, the session outlives the WebSocket:
// on disconnect the PTY and child process stay alive for a grace period so a
// reconnecting client gets its shell and scrollback back. See PTYManager for
// the lifecycle details.
func HandleTerminalWS(manager webuterminal.PTYSource, auth *realtime.TerminalAuth, allowedOrigins []string, loomServerURL string, workspaceConfigByIDFn func(string) (*ops.WorkspaceData, error), tabMetaStore *tabmeta.Store, hub *realtime.Hub) http.HandlerFunc {
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
func validateTerminalWSRequest(w http.ResponseWriter, r *http.Request, manager webuterminal.PTYSource, auth *realtime.TerminalAuth) (string, string, bool) {
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

// runTerminalRelay attaches to the (workspace, session) PTY session and runs
// the bidirectional relay until the WebSocket closes. On WS close the session
// is detached (grace period armed); the PTY and child process stay alive.
func runTerminalRelay(reqCtx context.Context, conn *websocket.Conn, p *terminalWSParams, session, workspace string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	key := webuterminal.SessionKey{Workspace: workspace, Name: session}

	att, reattach, err := p.manager.AttachSession(key, 80, 24, webuterminal.ArgvForSession(session))
	if err != nil {
		if errors.Is(err, webuterminal.ErrPTYMaxSessionsReached) {
			slog.Info("terminal session limit reached", "session", session)
		} else {
			slog.Error("failed to attach terminal session", "session", session, "err", err)
		}
		return websocket.StatusInternalError, err.Error()
	}
	connID := att.ConnID()

	// Freshly spawned "talk-to-lead" sessions get a project-context banner
	// written before the shell's prompt. On reattach the banner is already
	// part of the scrollback replay.
	if !reattach && session == "talk-to-lead" && p.loomServerURL != "" {
		wsID := middleware.WorkspaceFromContext(reqCtx)
		wsConfigFn := func() (*ops.WorkspaceData, error) {
			if p.workspaceConfigByIDFn == nil {
				return nil, errors.New("no workspace config resolver")
			}
			return p.workspaceConfigByIDFn(wsID)
		}
		injectTerminalContextBanner(att, p.loomServerURL, wsConfigFn)
	}

	realtime.BroadcastSessionIssueEvent(p.tabMetaStore, p.hub, workspace, session)

	// Emit scrollback replay (reset escape + ring bytes) before going live.
	if replay := att.Scrollback(); len(replay) > 0 {
		if err := conn.Write(reqCtx, websocket.MessageBinary, replay); err != nil { //nolint:staticcheck // SA1019
			slog.Warn("scrollback replay write failed", "session", session, "err", err)
		}
	}

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	crashCh := make(chan realtime.CrashInfo, 1)
	go func() {
		// Pump attachment output → WS. Exits when the channel closes
		// (session killed or replaced) or the context is cancelled.
		crashCh <- realtime.AttachmentToWS(ctx, cancel, conn, att.Output())
	}()

	// WS → PTY until the client disconnects. The attachment satisfies
	// realtime.Resizer directly so the manager doesn't need a connID → PTY
	// lookup table.
	realtime.WSToPTY(ctx, conn, attachmentWriter{att}, att, connID)

	// WebSocket gone — detach the attachment. PTY stays alive for the
	// manager's grace period.
	p.manager.Detach(key, connID)

	return (<-crashCh).WSClose()
}

// attachmentWriter adapts Attachment to realtime.WSToPTY's io.Writer input.
type attachmentWriter struct{ a webuterminal.Attachment }

func (w attachmentWriter) Write(p []byte) (int, error) { return w.a.WriteInput(p) }

// injectTerminalContextBanner fetches project context from the loom server
// and writes a formatted banner to the newly attached session.
func injectTerminalContextBanner(att webuterminal.Attachment, loomServerURL string, workspaceConfigFn func() (*ops.WorkspaceData, error)) {
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
	if _, writeErr := att.WriteInput([]byte(banner)); writeErr != nil {
		slog.Warn("failed to write context banner to pty", "err", writeErr)
	}
}
