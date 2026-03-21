package webui

import (
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"

	"github.com/tysonthomas9/loomcli/internal/sessions"
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
	TaskID   string                   `json:"task_id"`
	Sessions []sessions.SessionRecord `json:"sessions"`
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
// GET /api/tasks/{taskId}/sessions
func handleListTaskSessions(sessStore *sessions.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessStore == nil {
			respondJSON(w, http.StatusServiceUnavailable, SessionListResponse{
				Success: false,
				Error:   "session store not available",
			})
			return
		}

		taskID := r.PathValue("taskId")
		if taskID == "" {
			respondJSON(w, http.StatusBadRequest, SessionListResponse{
				Success: false,
				Error:   "missing task ID",
			})
			return
		}

		if !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, SessionListResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			})
			return
		}

		records, err := sessStore.SessionsByTask(taskID)
		if err != nil {
			log.Printf("Failed to list sessions for task %s: %v", taskID, err)
			respondJSON(w, http.StatusInternalServerError, SessionListResponse{
				Success: false,
				Error:   "failed to list sessions",
			})
			return
		}

		// Ensure empty array instead of null in JSON output.
		if records == nil {
			records = []sessions.SessionRecord{}
		}

		respondJSON(w, http.StatusOK, SessionListResponse{
			Success: true,
			Data: &SessionListData{
				TaskID:   taskID,
				Sessions: records,
			},
		})
	}
}

// handleGetSession returns metadata for a single session.
// GET /api/tasks/{taskId}/sessions/{sessionId}
func handleGetSession(sessStore *sessions.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessStore == nil {
			respondJSON(w, http.StatusServiceUnavailable, SessionDetailResponse{
				Success: false,
				Error:   "session store not available",
			})
			return
		}

		taskID := r.PathValue("taskId")
		if taskID == "" || !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, SessionDetailResponse{
				Success: false,
				Error:   "invalid task ID",
			})
			return
		}

		sessionID := r.PathValue("sessionId")
		if sessionID == "" || !validSessionID.MatchString(sessionID) {
			respondJSON(w, http.StatusBadRequest, SessionDetailResponse{
				Success: false,
				Error:   "invalid session ID",
			})
			return
		}

		meta, err := sessStore.LoadMetadata(sessionID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				respondJSON(w, http.StatusNotFound, SessionDetailResponse{
					Success: false,
					Error:   "session not found",
				})
				return
			}
			log.Printf("Failed to load session %s: %v", sessionID, err)
			respondJSON(w, http.StatusInternalServerError, SessionDetailResponse{
				Success: false,
				Error:   "failed to load session",
			})
			return
		}

		respondJSON(w, http.StatusOK, SessionDetailResponse{
			Success: true,
			Data: &SessionDetailData{
				SessionMetadata: *meta,
				IsActive:        meta.Status == sessions.StatusRunning,
			},
		})
	}
}

// handleGetSessionTranscript returns the transcript entries for a session.
// GET /api/tasks/{taskId}/sessions/{sessionId}/transcript
func handleGetSessionTranscript(sessStore *sessions.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessStore == nil {
			respondJSON(w, http.StatusServiceUnavailable, TranscriptResponse{
				Success: false,
				Error:   "session store not available",
			})
			return
		}

		taskID := r.PathValue("taskId")
		if taskID == "" || !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, TranscriptResponse{
				Success: false,
				Error:   "invalid task ID",
			})
			return
		}

		sessionID := r.PathValue("sessionId")
		if sessionID == "" || !validSessionID.MatchString(sessionID) {
			respondJSON(w, http.StatusBadRequest, TranscriptResponse{
				Success: false,
				Error:   "invalid session ID",
			})
			return
		}

		entries, err := sessStore.LoadTranscript(sessionID)
		if err != nil {
			log.Printf("Failed to load transcript for session %s: %v", sessionID, err)
			respondJSON(w, http.StatusInternalServerError, TranscriptResponse{
				Success: false,
				Error:   "failed to load transcript",
			})
			return
		}

		// Ensure empty array instead of null in JSON output.
		if entries == nil {
			entries = []sessions.TranscriptEntry{}
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

// handleGetSessionDiff returns the diff.patch file for a session as plain text.
// GET /api/tasks/{taskId}/sessions/{sessionId}/diff
func handleGetSessionDiff(sessStore *sessions.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if sessStore == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "session store not available",
			})
			return
		}

		taskID := r.PathValue("taskId")
		if taskID == "" || !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid task ID",
			})
			return
		}

		sessionID := r.PathValue("sessionId")
		if sessionID == "" || !validSessionID.MatchString(sessionID) {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid session ID",
			})
			return
		}

		diff, err := sessStore.ReadDiff(sessionID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{
					"success": false,
					"error":   "diff not found",
				})
				return
			}
			log.Printf("Failed to read diff for session %s: %v", sessionID, err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to read diff",
			})
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
	}
}
