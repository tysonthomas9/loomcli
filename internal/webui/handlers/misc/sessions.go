package misc

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/sessioncoord"
)

// validSessionID matches session IDs produced by GenerateSessionID:
// YYYYMMDD-HHMMSS-<agent>-<taskshort>-<8hexrand>
// Allows alphanumeric, hyphens, underscores, and dots.
var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// --- Response types ---

// SessionListResponse is the JSON envelope for listing sessions by task.
type SessionListResponse struct {
	Success bool             `json:"success"`
	Data    *SessionListData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// SessionListData contains the task ID and its sessions.
type SessionListData struct {
	TaskID   string                         `json:"task_id"`
	Sessions []sessioncoord.SessionListItem `json:"sessions"`
}

// SessionDetailResponse is the JSON envelope for a single session's metadata.
type SessionDetailResponse struct {
	Success bool                            `json:"success"`
	Data    *sessioncoord.SessionDetailData `json:"data,omitempty"`
	Error   string                          `json:"error,omitempty"`
}

// TranscriptResponse is the JSON envelope for a session transcript.
type TranscriptResponse struct {
	Success bool            `json:"success"`
	Data    *TranscriptData `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// TranscriptData contains the session ID and its canonical event stream.
type TranscriptData struct {
	SessionID string             `json:"session_id"`
	Entries   []transcript.Event `json:"entries"`
}

// SubagentListResponse is the JSON envelope for listing subagents on a session.
type SubagentListResponse struct {
	Success bool              `json:"success"`
	Data    *SubagentListData `json:"data,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// SubagentListData contains captured subagent IDs for a session.
type SubagentListData struct {
	SessionID   string   `json:"session_id"`
	SubagentIDs []string `json:"subagent_ids"`
}

// --- Handlers ---

// HandleListTaskSessions returns all sessions for a given task.
func HandleListTaskSessions(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")

		items, err := svc.ListTaskSessions(r.Context(), wsID, taskID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, SessionListResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, SessionListResponse{
			Success: true,
			Data: &SessionListData{
				TaskID:   taskID,
				Sessions: items,
			},
		})
	}
}

// HandleGetSession returns metadata for a single session.
func HandleGetSession(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		result, err := svc.GetSession(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, SessionDetailResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, SessionDetailResponse{
			Success: true,
			Data:    result,
		})
	}
}

// HandleGetSessionTranscript returns the transcript entries for a session.
func HandleGetSessionTranscript(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		entries, err := svc.GetSessionTranscript(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, TranscriptResponse{
				Success: false,
				Error:   msg,
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data: &TranscriptData{
				SessionID: sessionID,
				Entries:   entries,
			},
		})
	}
}

// sessionNotifyRequest is the JSON body expected by HandleNotifySessionChange.
type sessionNotifyRequest struct {
	TaskID      string `json:"task_id"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspace_id"`
}

// HandleNotifySessionChange receives fire-and-forget notifications from local
// agent processes when a session status changes, and broadcasts a session_change
// SSE event to all connected web UI clients.
// POST /api/sessions/notify
func HandleNotifySessionChange(hub *realtime.Hub, notifyToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Validate bearer token — fail-closed if server token is empty.
		if notifyToken == "" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		authHeader := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		token := authHeader[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(token), []byte(notifyToken)) != 1 {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		var req sessionNotifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		if req.TaskID == "" || req.SessionID == "" {
			http.Error(w, "Bad Request: task_id and session_id required", http.StatusBadRequest)
			return
		}

		if req.WorkspaceID == "" {
			slog.Warn("session notify missing workspace_id, mutation will be dropped", "task_id", req.TaskID)
		}
		hub.Broadcast(&realtime.MutationPayload{
			Type:        realtime.MutationSessionChange,
			EntityType:  "session",
			EntityID:    req.SessionID,
			Action:      "session.change",
			IssueID:     req.TaskID,
			NewStatus:   req.Status,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: req.WorkspaceID,
		})

		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleListSessionSubagents returns the list of captured subagent IDs for a session.
func HandleListSessionSubagents(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		ids, err := svc.ListSessionSubagents(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, SubagentListResponse{Success: false, Error: msg})
			return
		}
		if ids == nil {
			ids = []string{}
		}
		handler.WriteJSON(w, http.StatusOK, SubagentListResponse{
			Success: true,
			Data:    &SubagentListData{SessionID: sessionID, SubagentIDs: ids},
		})
	}
}

// HandleGetSessionSubagentTranscript returns the canonical event stream for a
// captured subagent transcript.
func HandleGetSessionSubagentTranscript(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")
		subagentID := r.PathValue("subagentId")

		events, err := svc.GetSessionSubagentTranscript(r.Context(), wsID, taskID, sessionID, subagentID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, TranscriptResponse{Success: false, Error: msg})
			return
		}
		handler.WriteJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data:    &TranscriptData{SessionID: sessionID + "/" + subagentID, Entries: events},
		})
	}
}

// HandleGetSessionDiff returns the diff.patch file for a session as plain text.
func HandleGetSessionDiff(svc sessioncoord.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		diff, err := svc.GetSessionDiff(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			var svcErr *apperrors.ServiceError
			status := http.StatusInternalServerError
			msg := "internal server error"
			if errors.As(err, &svcErr) {
				status = handler.StatusForKind(svcErr.Kind)
				msg = svcErr.Message
			}
			handler.WriteJSON(w, status, map[string]interface{}{
				"success": false,
				"error":   msg,
			})
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
	}
}
