package webui

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
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
	TaskID   string            `json:"task_id"`
	Sessions []SessionListItem `json:"sessions"`
}

// SessionListItem wraps a SessionRecord with computed display fields.
type SessionListItem struct {
	sessions.SessionRecord
	IsActive      bool `json:"is_active"`
	HasTranscript bool `json:"has_transcript"`
	HasDiff       bool `json:"has_diff"`
}

// SessionDetailResponse is the JSON envelope for a single session's metadata.
type SessionDetailResponse struct {
	Success bool               `json:"success"`
	Data    *SessionDetailData `json:"data,omitempty"`
	Error   string             `json:"error,omitempty"`
}

// SessionDetailData is the session metadata plus computed is_active field.
type SessionDetailData struct {
	sessions.SessionMetadata
	IsActive bool `json:"is_active"`
}

// TranscriptResponse is the JSON envelope for a session transcript.
type TranscriptResponse struct {
	Success bool            `json:"success"`
	Data    *TranscriptData `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// TranscriptData contains the session ID and its transcript entries.
type TranscriptData struct {
	SessionID string                     `json:"session_id"`
	Entries   []sessions.TranscriptEntry `json:"entries"`
}

// --- Handlers ---

// handleListTaskSessions returns all sessions for a given task.
func handleListTaskSessions(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("taskId")

		items, err := svc.ListTaskSessions(r.Context(), taskID)
		if err != nil {
			status := serviceErrorStatus(err)
			respondJSON(w, status, SessionListResponse{
				Success: false,
				Error:   serviceErrorMessage(err),
			})
			return
		}

		respondJSON(w, http.StatusOK, SessionListResponse{
			Success: true,
			Data: &SessionListData{
				TaskID:   taskID,
				Sessions: items,
			},
		})
	}
}

// handleGetSession returns metadata for a single session.
func handleGetSession(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		result, err := svc.GetSession(r.Context(), taskID, sessionID)
		if err != nil {
			status := serviceErrorStatus(err)
			respondJSON(w, status, SessionDetailResponse{
				Success: false,
				Error:   serviceErrorMessage(err),
			})
			return
		}

		respondJSON(w, http.StatusOK, SessionDetailResponse{
			Success: true,
			Data:    result,
		})
	}
}

// handleGetSessionTranscript returns the transcript entries for a session.
func handleGetSessionTranscript(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		entries, err := svc.GetSessionTranscript(r.Context(), taskID, sessionID)
		if err != nil {
			status := serviceErrorStatus(err)
			respondJSON(w, status, TranscriptResponse{
				Success: false,
				Error:   serviceErrorMessage(err),
			})
			return
		}

		respondJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data: &TranscriptData{
				SessionID: sessionID,
				Entries:   entries,
			},
		})
	}
}

// sessionNotifyRequest is the JSON body expected by handleNotifySessionChange.
type sessionNotifyRequest struct {
	TaskID      string `json:"task_id"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	WorkspaceID string `json:"workspace_id"`
}

// handleNotifySessionChange receives fire-and-forget notifications from local
// agent processes when a session status changes, and broadcasts a session_change
// SSE event to all connected web UI clients.
// POST /api/sessions/notify
func handleNotifySessionChange(hub *realtime.Hub, notifyToken string) http.HandlerFunc {
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
			Type:        rpc.MutationSessionChange,
			IssueID:     req.TaskID,
			NewStatus:   req.Status,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: req.WorkspaceID,
		})

		w.WriteHeader(http.StatusNoContent)
	}
}

// handleGetSessionDiff returns the diff.patch file for a session as plain text.
func handleGetSessionDiff(svc SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		diff, err := svc.GetSessionDiff(r.Context(), taskID, sessionID)
		if err != nil {
			status := serviceErrorStatus(err)
			respondJSON(w, status, map[string]interface{}{
				"success": false,
				"error":   serviceErrorMessage(err),
			})
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
	}
}
