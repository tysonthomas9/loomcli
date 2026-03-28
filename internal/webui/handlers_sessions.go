package webui

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
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
// GET /api/workspaces/{ws}/tasks/{taskId}/sessions
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
				Error:   "invalid task ID: must match [a-zA-Z0-9._-]+",
			})
			return
		}

		records, err := sessStore.SessionsByTask(taskID)
		if err != nil {
			slog.Error("failed to list sessions", "task_id", taskID, "err", err)
			respondJSON(w, http.StatusInternalServerError, SessionListResponse{
				Success: false,
				Error:   "failed to list sessions",
			})
			return
		}

		// Build list items with computed fields.
		items := make([]SessionListItem, 0, len(records))
		for _, rec := range records {
			item := SessionListItem{
				SessionRecord: rec,
				IsActive:      rec.Status == sessions.StatusRunning,
			}
			// Check if transcript and diff files exist and have content.
			if entries, err := sessStore.LoadTranscript(rec.SessionID); err == nil && len(entries) > 0 {
				item.HasTranscript = true
			}
			if diff, err := sessStore.ReadDiff(rec.SessionID); err == nil && diff != "" {
				item.HasDiff = true
			}
			items = append(items, item)
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
// GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}
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
			slog.Error("failed to load session", "session_id", sessionID, "err", err)
			respondJSON(w, http.StatusInternalServerError, SessionDetailResponse{
				Success: false,
				Error:   "failed to load session",
			})
			return
		}

		// Enforce task ownership — session must belong to the requested task.
		if meta.TaskID != taskID {
			respondJSON(w, http.StatusNotFound, SessionDetailResponse{
				Success: false,
				Error:   "session not found",
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
// GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript
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

		// Enforce task ownership before returning transcript.
		meta, err := sessStore.LoadMetadata(sessionID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				respondJSON(w, http.StatusNotFound, TranscriptResponse{
					Success: false,
					Error:   "session not found",
				})
				return
			}
			slog.Error("failed to load session metadata", "session_id", sessionID, "err", err)
			respondJSON(w, http.StatusInternalServerError, TranscriptResponse{
				Success: false,
				Error:   "failed to load session",
			})
			return
		}
		if meta.TaskID != taskID {
			respondJSON(w, http.StatusNotFound, TranscriptResponse{
				Success: false,
				Error:   "session not found",
			})
			return
		}

		entries, loadErr := sessStore.LoadTranscript(sessionID)
		if loadErr != nil {
			slog.Error("failed to load transcript", "session_id", sessionID, "err", loadErr)
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
func handleNotifySessionChange(hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Restrict to loopback only — this endpoint is for local agents, not external callers.
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
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
		hub.Broadcast(&MutationPayload{
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
// GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff
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

		// Enforce task ownership before returning diff.
		meta, err := sessStore.LoadMetadata(sessionID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{
					"success": false,
					"error":   "session not found",
				})
				return
			}
			slog.Error("failed to load session metadata", "session_id", sessionID, "err", err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to load session",
			})
			return
		}
		if meta.TaskID != taskID {
			respondJSON(w, http.StatusNotFound, map[string]interface{}{
				"success": false,
				"error":   "session not found",
			})
			return
		}

		diff, diffErr := sessStore.ReadDiff(sessionID)
		if diffErr != nil {
			if errors.Is(diffErr, os.ErrNotExist) {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{
					"success": false,
					"error":   "diff not found",
				})
				return
			}
			slog.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
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
