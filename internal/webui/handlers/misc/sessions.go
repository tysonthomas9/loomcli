package misc

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// validSessionID matches session IDs produced by GenerateSessionID:
// YYYYMMDD-HHMMSS-<agent>-<taskshort>-<8hexrand>
// Allows alphanumeric, hyphens, underscores, and dots.
var validSessionID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func sessionArchiveHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, sessionarchive.ErrInvalid):
		return http.StatusBadRequest, sessionarchive.PublicErrorMessage(err)
	case errors.Is(err, sessionarchive.ErrNotFound):
		return http.StatusNotFound, sessionarchive.PublicErrorMessage(err)
	case errors.Is(err, sessionarchive.ErrUnavailable):
		return http.StatusServiceUnavailable, sessionarchive.PublicErrorMessage(err)
	case errors.Is(err, sessionarchive.ErrInvalidPersistedState):
		return http.StatusInternalServerError, sessionarchive.PublicErrorMessage(err)
	}
	var svcErr *apperrors.ServiceError
	if errors.As(err, &svcErr) {
		return handler.StatusForKind(svcErr.Kind), svcErr.Message
	}
	return http.StatusInternalServerError, "internal server error"
}

// --- Response types ---

// SessionListResponse is the JSON envelope for listing sessions by task.
type SessionListResponse struct {
	Success bool             `json:"success"`
	Data    *SessionListData `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// SessionListData contains the task ID and its sessions.
type SessionListData struct {
	TaskID   string                           `json:"task_id"`
	Sessions []sessionarchive.SessionListItem `json:"sessions"`
}

// SessionDetailResponse is the JSON envelope for a single session's metadata.
type SessionDetailResponse struct {
	Success bool                              `json:"success"`
	Data    *sessionarchive.SessionDetailData `json:"data,omitempty"`
	Error   string                            `json:"error,omitempty"`
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

// --- Handlers ---

// HandleListTaskSessions returns all sessions for a given task.
func HandleListTaskSessions(svc sessionarchive.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")

		items, err := svc.ListTaskSessions(r.Context(), wsID, taskID)
		if err != nil {
			status, msg := sessionArchiveHTTPError(err)
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
func HandleGetSession(svc sessionarchive.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		result, err := svc.GetSession(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			status, msg := sessionArchiveHTTPError(err)
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
func HandleGetSessionTranscript(svc sessionarchive.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		entries, err := svc.GetSessionTranscript(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			status, msg := sessionArchiveHTTPError(err)
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

// HandleGetSessionDiff returns the diff.patch file for a session as plain text.
func HandleGetSessionDiff(svc sessionarchive.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		diff, err := svc.GetSessionDiff(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			status, msg := sessionArchiveHTTPError(err)
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
