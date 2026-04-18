package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// validAgentName matches alphanumeric characters, hyphens, and underscores.
var validAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// agentLogTokenScope returns the token scope string for an agent's log stream.
func agentLogTokenScope(agentName string) string {
	return "agent:" + agentName + ":logs"
}

const (
	agentTerminalModeTmux    = "tmux"
	agentTerminalModeArchive = "archive"
)

type agentTerminalInfoResponse struct {
	Success bool                   `json:"success"`
	Data    *agentTerminalInfoData `json:"data,omitempty"`
	Error   string                 `json:"error,omitempty"`
}

type agentTerminalInfoData struct {
	Agent string `json:"agent"`
	Mode  string `json:"mode"`
}

type agentTerminalTokenResponse struct {
	Success bool                    `json:"success"`
	Data    *agentTerminalTokenData `json:"data,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

type agentTerminalTokenData struct {
	Token string `json:"token"`
}

// agentTmuxMonitor adapts AgentTmuxManager to the realtime.SessionMonitor
// interface used by the shared PtyToWS relay.
type agentTmuxMonitor struct {
	mgr *webuterminal.AgentTmuxManager
}

func (m *agentTmuxMonitor) HasSession(name string) bool { return m.mgr.HasSession(name) }
func (m *agentTmuxMonitor) PaneDead(name string) bool   { return m.mgr.PaneDead(name) }
func (m *agentTmuxMonitor) CapturePaneRaw(name string, lines int) string {
	return m.mgr.CapturePane(name, lines)
}

// HandleGetAgentTerminalInfo reports whether an agent has a live tmux session
// suitable for terminal streaming, or should fall back to archive logs.
func HandleGetAgentTerminalInfo(svc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GetTerminalInfo(r.Context(), wsID, agentName)
		if err != nil {
			var svcErr *service.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, agentTerminalInfoResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, agentTerminalInfoResponse{
			Success: true,
			Data: &agentTerminalInfoData{
				Agent: result.Agent,
				Mode:  result.Mode,
			},
		})
	}
}

// HandleGetAgentTerminalToken generates a one-time token scoped to an agent logs stream.
func HandleGetAgentTerminalToken(svc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")

		var userID string
		if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok {
			userID = identity.UserID
		}

		token, err := svc.GenerateTerminalToken(r.Context(), agentName, userID)
		if err != nil {
			var svcErr *service.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, agentTerminalTokenResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		handler.WriteJSON(w, http.StatusOK, agentTerminalTokenResponse{
			Success: true,
			Data: &agentTerminalTokenData{
				Token: token,
			},
		})
	}
}

// HandleAgentTerminalWS streams a live read-only tmux session for an agent.
// The auto-mode CLI creates those tmux sessions; this handler lets the web
// UI attach to them via pty.Start("tmux attach -t ..."). Unlike the main
// terminal path (which is tmux-free), this route retains tmux because agent
// processes need to outlive the CLI invocation that spawned them.
func HandleAgentTerminalWS(manager *webuterminal.AgentTmuxManager, auth *realtime.TerminalAuth, allowedOrigins []string) http.HandlerFunc {
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		agentName, ok := validateAgentWSRequest(w, r, manager, auth)
		if !ok {
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionName, err := resolveAgentSession(w, manager, wsID, agentName)
		if err != nil {
			return
		}

		conn, ok := upgradeAgentWS(w, r, patterns)
		if !ok {
			return
		}

		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason) //nolint:staticcheck // SA1019: websocket migration tracked separately
		}()

		closeStatus, closeReason = runAgentTerminalRelay(r.Context(), conn, manager, sessionName, agentName)
	}
}

// validateAgentWSRequest validates the agent name, manager, auth, and token.
func validateAgentWSRequest(w http.ResponseWriter, r *http.Request, manager *webuterminal.AgentTmuxManager, auth *realtime.TerminalAuth) (string, bool) {
	agentName := r.PathValue("name")
	if agentName == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "missing agent name",
		})
		return "", false
	}
	if !validAgentName.MatchString(agentName) {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid agent name: must match [a-zA-Z0-9_-]+",
		})
		return "", false
	}
	if manager == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "agent terminal manager not initialized",
		})
		return "", false
	}
	if auth == nil {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "terminal authentication not initialized",
		})
		return "", false
	}

	token := r.URL.Query().Get("token")
	userID, err := auth.ValidateToken(token, agentLogTokenScope(agentName))
	if err != nil {
		handler.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   "terminal authentication failed",
		})
		slog.Warn("agent terminal auth failed", "agent", agentName, "err", err)
		return "", false
	}
	if userID != "" {
		slog.Info("agent terminal authenticated", "agent", agentName, "user_id", userID)
	}
	return agentName, true
}

// resolveAgentSession finds the tmux session for an agent and checks the session limit.
func resolveAgentSession(w http.ResponseWriter, manager *webuterminal.AgentTmuxManager, wsID, agentName string) (string, error) {
	sessionName, found, err := manager.FindLatestAgentSession(wsID, agentName)
	if err != nil {
		handler.WriteJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to inspect terminal sessions",
		})
		return "", err
	}
	if !found {
		handler.WriteJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "no active terminal session for agent",
		})
		return "", errors.New("no active terminal session for agent")
	}

	if manager.SessionCount() >= manager.MaxSessions() {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false,
			"error":   "maximum terminal sessions reached",
		})
		return "", errors.New("maximum terminal sessions reached")
	}
	return sessionName, nil
}

// upgradeAgentWS performs the WebSocket upgrade for an agent terminal connection.
func upgradeAgentWS(w http.ResponseWriter, r *http.Request, patterns []string) (*websocket.Conn, bool) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("agent terminal ws: failed to disable write deadline", "err", err)
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{ //nolint:staticcheck // SA1019: websocket migration tracked separately
		OriginPatterns: patterns,
	})
	if err != nil {
		slog.Error("failed to accept agent terminal websocket", "err", err)
		return nil, false
	}
	conn.SetReadLimit(realtime.WSReadLimit) //nolint:staticcheck // SA1019: websocket migration tracked separately
	return conn, true
}

// runAgentTerminalRelay attaches to the tmux session and runs the bidirectional relay.
func runAgentTerminalRelay(reqCtx context.Context, conn *websocket.Conn, manager *webuterminal.AgentTmuxManager, sessionName, agentName string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	termSession, err := manager.AttachExistingRaw(sessionName, 80, 24)
	if err != nil {
		if errors.Is(err, webuterminal.ErrMaxSessionsReached) {
			slog.Info("agent terminal session limit reached", "agent", agentName)
		} else {
			slog.Error("failed to attach agent terminal session", "agent", agentName, "session", sessionName, "err", err)
		}
		return websocket.StatusInternalError, err.Error()
	}
	connID := termSession.ConnID

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	// Watch for an external kill of the tmux session so we can close the WS.
	go func() {
		select {
		case <-termSession.KillCh():
			_ = conn.Close(websocket.StatusCode(realtime.WSCloseSessionKilled), "session killed") //nolint:staticcheck // SA1019: websocket migration tracked separately
			cancel()
		case <-ctx.Done():
		}
	}()

	monitor := &agentTmuxMonitor{mgr: manager}
	crashCh := make(chan realtime.CrashInfo, 1)
	go func() {
		result := realtime.PtyToWS(ctx, cancel, conn, termSession.PTY, termSession.SessionName, monitor, nil)
		crashCh <- result
	}()

	realtime.WSToPTY(ctx, conn, termSession.PTY, manager, connID)

	if err := manager.Detach(connID); err != nil {
		slog.Error("failed to detach agent terminal connection", "conn_id", connID, "err", err)
	}

	return (<-crashCh).WSClose()
}
