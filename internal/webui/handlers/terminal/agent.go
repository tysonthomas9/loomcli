package terminal

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"nhooyr.io/websocket" //nolint:staticcheck // SA1019: websocket migration tracked separately

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/agentcoord"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

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

type agentTerminalMonitorAdapter struct {
	interaction.AgentTerminalMonitor
}

func (adapter agentTerminalMonitorAdapter) CapturePaneRaw(name string, lines int) string {
	return adapter.CapturePane(name, lines)
}

// HandleGetAgentTerminalInfo reports whether an agent has a live tmux session
// suitable for terminal streaming, or should fall back to archive logs.
func HandleGetAgentTerminalInfo(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		wsID := middleware.WorkspaceFromContext(r.Context())

		result, err := svc.GetTerminalInfo(r.Context(), wsID, agentName)
		if err != nil {
			var svcErr *apperrors.ServiceError
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
func HandleGetAgentTerminalToken(svc agentcoord.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")

		var userID string
		if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok {
			userID = identity.UserID
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		token, err := svc.GenerateTerminalToken(r.Context(), wsID, agentName, userID)
		if err != nil {
			var svcErr *apperrors.ServiceError
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
func HandleAgentTerminalWS(terminals interaction.TerminalTabs, auth *realtime.TerminalAuth, allowedOrigins []string) http.HandlerFunc {
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		agentName, ok := validateAgentWSRequest(w, r, terminals, auth)
		if !ok {
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionName, err := resolveAgentSession(r.Context(), w, terminals, wsID, agentName)
		if err != nil {
			return
		}

		conn, upgradeCtx, ok := upgradeAgentTerminalWithSpan(w, r, patterns, wsID, agentName, sessionName)
		if !ok {
			return
		}

		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason) //nolint:staticcheck // SA1019: websocket migration tracked separately
			emitAgentDisconnectSpan(upgradeCtx, wsID, agentName, sessionName, closeStatus)
		}()

		closeStatus, closeReason = runAgentTerminalRelay(r.Context(), conn, terminals, wsID, sessionName, agentName)
	}
}

// upgradeAgentTerminalWithSpan opens a short-lived ws.upgrade span around
// the agent-terminal handshake. Mirrors upgradeTerminalWithSpan (main
// terminal) but tags with loom.agent. See trace contract §3.
func upgradeAgentTerminalWithSpan(w http.ResponseWriter, r *http.Request, patterns []string, wsID, agentName, sessionName string) (*websocket.Conn, context.Context, bool) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	upgradeCtx, upgradeSpan := otel.Tracer(terminalTracerName).Start(r.Context(), "ws.upgrade",
		trace.WithAttributes(
			attribute.String("loom.workspace", wsID),
			attribute.String("loom.agent", agentName),
			attribute.String("loom.session_id", sessionName),
			attribute.String("network.peer.address", r.RemoteAddr),
		),
	)
	conn, ok := upgradeAgentWS(w, r, patterns)
	if !ok {
		upgradeSpan.SetStatus(codes.Error, "network")
		upgradeSpan.End()
		return nil, upgradeCtx, false
	}
	upgradeSpan.End()
	return conn, upgradeCtx, true
}

// emitAgentDisconnectSpan records a sibling span on agent-terminal close,
// tagged with loom.agent in addition to the workspace and session.
func emitAgentDisconnectSpan(upgradeCtx context.Context, wsID, agentName, sessionName string, closeStatus websocket.StatusCode) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	_, discSpan := otel.Tracer(terminalTracerName).Start(context.Background(), "ws.disconnect",
		trace.WithLinks(trace.LinkFromContext(upgradeCtx)),
		trace.WithAttributes(
			attribute.String("loom.workspace", wsID),
			attribute.String("loom.agent", agentName),
			attribute.String("loom.session_id", sessionName),
			attribute.String("disconnect.reason", wsCloseReason(closeStatus)),
		),
	)
	if closeStatus == websocket.StatusInternalError { //nolint:staticcheck // SA1019
		discSpan.SetStatus(codes.Error, "crash")
	}
	discSpan.End()
}

// validateAgentWSRequest validates the agent name, manager, auth, and token.
func validateAgentWSRequest(w http.ResponseWriter, r *http.Request, terminals interaction.TerminalTabs, auth *realtime.TerminalAuth) (string, bool) {
	agentName := r.PathValue("name")
	if agentName == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "missing agent name",
		})
		return "", false
	}
	if !agentcoord.IsValidAgentName(agentName) {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "invalid agent name",
		})
		return "", false
	}
	if terminals == nil {
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
	wsID := middleware.WorkspaceFromContext(r.Context())
	userID, err := auth.ValidateToken(token, agentLogTokenScope(agentName), wsID)
	if err != nil {
		handler.WriteJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   "terminal authentication failed",
		})
		slog.Warn("agent terminal auth failed", "agent", agentName, "workspace", wsID, "err", err)
		return "", false
	}
	if userID != "" {
		slog.Info("agent terminal authenticated", "agent", agentName, "workspace", wsID, "user_id", userID)
	}
	return agentName, true
}

// resolveAgentSession finds the tmux session for an agent and checks the session limit.
func resolveAgentSession(ctx context.Context, w http.ResponseWriter, terminals interaction.TerminalTabs, wsID, agentName string) (string, error) {
	info, err := terminals.AgentTerminalInfo(ctx, wsID, agentName)
	if err != nil {
		handler.WriteJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to inspect terminal sessions",
		})
		return "", err
	}
	if info == nil || !info.Live {
		handler.WriteJSON(w, http.StatusNotFound, map[string]interface{}{
			"success": false,
			"error":   "no active terminal session for agent",
		})
		return "", errors.New("no active terminal session for agent")
	}

	return info.SessionName, nil
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
func runAgentTerminalRelay(reqCtx context.Context, conn *websocket.Conn, terminals interaction.TerminalTabs, workspace, sessionName, agentName string) (websocket.StatusCode, string) { //nolint:staticcheck // SA1019: websocket migration tracked separately
	result, err := terminals.AttachAgentTerminal(reqCtx, interaction.AttachAgentTerminalCommand{
		WorkspaceKey: workspace, AgentID: agentName, Columns: 80, Rows: 24,
	})
	if err != nil {
		if errors.Is(err, interaction.ErrTerminalCapacity) {
			slog.Info("agent terminal session limit reached", "agent", agentName)
		} else {
			slog.Error("failed to attach agent terminal session", "agent", agentName, "session", sessionName, "err", err)
		}
		return websocket.StatusInternalError, err.Error()
	}
	if result == nil || result.Connection == nil || result.Monitor == nil {
		return websocket.StatusInternalError, "agent terminal attachment unavailable"
	}
	termSession := result.Connection
	connID := termSession.ConnectionID()

	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()

	// Watch for an external kill of the tmux session so we can close the WS.
	go func() {
		select {
		case <-termSession.Killed():
			_ = conn.Close(websocket.StatusCode(realtime.WSCloseSessionKilled), "session killed") //nolint:staticcheck // SA1019: websocket migration tracked separately
			cancel()
		case <-ctx.Done():
		}
	}()

	monitor := agentTerminalMonitorAdapter{AgentTerminalMonitor: result.Monitor}
	crashCh := make(chan realtime.CrashInfo, 1)
	go func() {
		result := realtime.PtyToWS(ctx, cancel, conn, termSession, termSession.SessionName(), monitor, nil)
		crashCh <- result
	}()

	realtime.WSToPTY(ctx, conn, termSession, termSession, connID)

	if err := terminals.DetachAgentTerminal(reqCtx, connID); err != nil {
		slog.Error("failed to detach agent terminal connection", "conn_id", connID, "err", err)
	}

	return (<-crashCh).WSClose()
}
