package webui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

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

// handleGetAgentTerminalInfo reports whether an agent has a live tmux session
// suitable for terminal streaming, or should fall back to archive logs.
func handleGetAgentTerminalInfo(svc AgentService) http.HandlerFunc {
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
			respondJSON(w, status, agentTerminalInfoResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		respondJSON(w, http.StatusOK, agentTerminalInfoResponse{
			Success: true,
			Data: &agentTerminalInfoData{
				Agent: result.Agent,
				Mode:  result.Mode,
			},
		})
	}
}

// handleGetAgentTerminalToken generates a one-time token scoped to an agent logs stream.
func handleGetAgentTerminalToken(svc AgentService) http.HandlerFunc {
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
			respondJSON(w, status, agentTerminalTokenResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		respondJSON(w, http.StatusOK, agentTerminalTokenResponse{
			Success: true,
			Data: &agentTerminalTokenData{
				Token: token,
			},
		})
	}
}

// handleAgentTerminalWS streams a live read-only tmux session for an agent.
func handleAgentTerminalWS(manager *TerminalManager, auth *realtime.TerminalAuth, allowedOrigins []string) http.HandlerFunc {
	patterns := originHosts(allowedOrigins)

	return func(w http.ResponseWriter, r *http.Request) {
		agentName := r.PathValue("name")
		if agentName == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "missing agent name",
			})
			return
		}
		if !validAgentName.MatchString(agentName) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid agent name: must match [a-zA-Z0-9_-]+",
			})
			return
		}
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}
		if auth == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal authentication not initialized",
			})
			return
		}

		token := r.URL.Query().Get("token")
		userID, err := auth.ValidateToken(token, agentLogTokenScope(agentName))
		if err != nil {
			respondJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"success": false,
				"error":   "terminal authentication failed",
			})
			slog.Warn("agent terminal auth failed", "agent", agentName, "err", err)
			return
		}
		if userID != "" {
			slog.Info("agent terminal authenticated", "agent", agentName, "user_id", userID)
		}

		wsID := middleware.WorkspaceFromContext(r.Context())

		sessionName, found, err := manager.FindLatestAgentSession(wsID, agentName)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to inspect terminal sessions",
			})
			return
		}
		if !found {
			respondJSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "no active terminal session for agent",
			})
			return
		}

		if manager.SessionCount() >= manager.MaxSessions() {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "maximum terminal sessions reached",
			})
			return
		}

		rc := http.NewResponseController(w)
		if err := rc.SetWriteDeadline(time.Time{}); err != nil {
			slog.Warn("agent terminal ws: failed to disable write deadline", "err", err)
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: patterns,
		})
		if err != nil {
			slog.Error("failed to accept agent terminal websocket", "err", err)
			return
		}
		conn.SetReadLimit(realtime.WSReadLimit)

		closeStatus := websocket.StatusInternalError
		closeReason := "connection closed"
		defer func() {
			_ = conn.Close(closeStatus, closeReason)
		}()

		termSession, err := manager.AttachExistingRaw(sessionName, 80, 24)
		if err != nil {
			if errors.Is(err, ErrMaxSessionsReached) {
				slog.Info("agent terminal session limit reached", "agent", agentName)
			} else {
				slog.Error("failed to attach agent terminal session", "agent", agentName, "session", sessionName, "err", err)
			}
			closeReason = err.Error()
			return
		}
		connID := termSession.ConnID

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		monitor := &terminalMonitor{mgr: manager}
		crashCh := make(chan realtime.CrashInfo, 1)
		go func() {
			result := realtime.PtyToWS(ctx, cancel, conn, termSession.PTY, termSession.Name, monitor, nil)
			crashCh <- result
		}()

		realtime.WSToPTY(ctx, conn, termSession.PTY, manager, connID)

		if err := manager.Detach(connID); err != nil {
			slog.Error("failed to detach agent terminal connection", "conn_id", connID, "err", err)
		}

		closeStatus, closeReason = (<-crashCh).WSClose()
	}
}
