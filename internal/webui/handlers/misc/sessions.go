package misc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
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
	TaskID   string                    `json:"task_id"`
	Sessions []service.SessionListItem `json:"sessions"`
}

// WorkspaceSessionListResponse is the JSON envelope for listing sessions by workspace.
type WorkspaceSessionListResponse struct {
	Success bool                      `json:"success"`
	Data    *WorkspaceSessionListData `json:"data,omitempty"`
	Error   string                    `json:"error,omitempty"`
}

// WorkspaceSessionListData contains workspace-scoped sessions and truncation metadata.
type WorkspaceSessionListData struct {
	Sessions        []service.SessionListItem `json:"sessions"`
	Total           int                       `json:"total"`
	Limit           int                       `json:"limit"`
	ScoreDimensions []string                  `json:"score_dimensions"`
}

// WorkspaceTraceRunResponse is the data envelope for a task-run Traces view.
type WorkspaceTraceRunResponse struct {
	Success bool                           `json:"success"`
	Data    *service.WorkspaceTraceRunData `json:"data,omitempty"`
	Error   string                         `json:"error,omitempty"`
}

// SessionDetailResponse is the JSON envelope for a single session's metadata.
type SessionDetailResponse struct {
	Success bool                       `json:"success"`
	Data    *service.SessionDetailData `json:"data,omitempty"`
	Error   string                     `json:"error,omitempty"`
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

const (
	workspaceSessionsDefaultLimit = 200
	workspaceSessionsMaxLimit     = 1000
)

var workspaceSessionsNow = time.Now

// --- Handlers ---

// HandleListWorkspaceSessions returns workspace-scoped sessions for Traces.
func HandleListWorkspaceSessions(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		opts, err := parseWorkspaceSessionListOptions(r)
		if err != nil {
			writeSessionServiceError(w, err, WorkspaceSessionListResponse{})
			return
		}

		items, total, err := svc.ListWorkspaceSessions(r.Context(), wsID, opts)
		if err != nil {
			writeSessionServiceError(w, err, WorkspaceSessionListResponse{})
			return
		}
		if items == nil {
			items = []service.SessionListItem{}
		}
		dimensions, err := workspaceSessionScoreDimensions(r.Context(), svc, wsID, opts)
		if err != nil {
			writeSessionServiceError(w, err, WorkspaceSessionListResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceSessionListResponse{
			Success: true,
			Data: &WorkspaceSessionListData{
				Sessions:        items,
				Total:           total,
				Limit:           opts.Limit,
				ScoreDimensions: dimensions,
			},
		})
	}
}

func workspaceSessionScoreDimensions(ctx context.Context, svc service.SessionService, wsID string, opts service.WorkspaceSessionListOptions) ([]string, error) {
	dimensionSvc, ok := svc.(service.WorkspaceSessionScoreDimensionService)
	if !ok {
		return []string{}, nil
	}
	dimensions, err := dimensionSvc.ListWorkspaceSessionScoreDimensions(ctx, wsID, opts)
	if err != nil {
		return nil, err
	}
	if dimensions == nil {
		dimensions = []string{}
	}
	return dimensions, nil
}

// HandleGetWorkspaceTraceRun returns backend-composed data for one task-run's
// Traces page. The route intentionally does not perform client metadata joins.
func HandleGetWorkspaceTraceRun(svc service.WorkspaceSessionRunService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		data, err := svc.GetWorkspaceTraceRun(r.Context(), wsID, r.PathValue("taskRunId"))
		if err != nil {
			writeSessionServiceError(w, err, WorkspaceTraceRunResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceTraceRunResponse{Success: true, Data: data})
	}
}

func OptionalWorkspaceTraceRunHandler(svc service.SessionService) http.HandlerFunc {
	runSvc, ok := svc.(service.WorkspaceSessionRunService)
	if !ok {
		return nil
	}
	return HandleGetWorkspaceTraceRun(runSvc)
}

func parseWorkspaceSessionListOptions(r *http.Request) (service.WorkspaceSessionListOptions, error) {
	q := r.URL.Query()
	opts := service.WorkspaceSessionListOptions{
		Limit:     workspaceSessionsDefaultLimit,
		Status:    domain.AgentSessionStatus(strings.TrimSpace(q.Get("status"))),
		AgentID:   strings.TrimSpace(q.Get("agent_id")),
		TaskRunID: strings.TrimSpace(q.Get("task_run_id")),
		Tags:      workspaceSessionTags(q["tag"]),
		Kind:      domain.AgentSessionKind(strings.TrimSpace(q.Get("kind"))),
	}
	sinceRaw := strings.TrimSpace(q.Get("since"))
	untilRaw := strings.TrimSpace(q.Get("until"))
	if sinceRaw == "" && untilRaw == "" {
		opts.Since = workspaceSessionsNow().UTC().Add(-7 * 24 * time.Hour)
	}
	if sinceRaw != "" {
		since, err := time.Parse(time.RFC3339, sinceRaw)
		if err != nil {
			return opts, service.ErrValidation("invalid since: must be RFC3339")
		}
		opts.Since = since
	}
	if untilRaw != "" {
		until, err := time.Parse(time.RFC3339, untilRaw)
		if err != nil {
			return opts, service.ErrValidation("invalid until: must be RFC3339")
		}
		opts.Until = until
	}
	if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
		limit, err := strconv.Atoi(limitRaw)
		if err != nil || limit < 0 {
			return opts, service.ErrValidation("invalid limit: must be a non-negative integer")
		}
		if limit > 0 {
			opts.Limit = limit
		}
		if opts.Limit > workspaceSessionsMaxLimit {
			opts.Limit = workspaceSessionsMaxLimit
		}
	}
	return opts, nil
}

func workspaceSessionTags(raw []string) []string {
	tags := make([]string, 0, len(raw))
	for _, value := range raw {
		if tag := strings.TrimSpace(value); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func writeSessionServiceError(w http.ResponseWriter, err error, response any) {
	var svcErr *service.ServiceError
	status := http.StatusInternalServerError
	msg := "internal server error"
	if errors.As(err, &svcErr) {
		status = handler.StatusForKind(svcErr.Kind)
		msg = svcErr.Message
	}
	switch resp := response.(type) {
	case WorkspaceSessionListResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	case WorkspaceTraceRunResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	case SessionListResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	case SessionDetailResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	case TranscriptResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	case SubagentListResponse:
		resp.Success = false
		resp.Error = msg
		handler.WriteJSON(w, status, resp)
	default:
		handler.WriteJSON(w, status, map[string]interface{}{"success": false, "error": msg})
	}
}

// HandleListTaskSessions returns all sessions for a given task.
func HandleListTaskSessions(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")

		items, err := svc.ListTaskSessions(r.Context(), wsID, taskID)
		if err != nil {
			writeSessionServiceError(w, err, SessionListResponse{})
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

// HandleGetWorkspaceSession returns metadata for a workspace-scoped session.
func HandleGetWorkspaceSession(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionID := r.PathValue("sessionId")

		result, err := svc.GetWorkspaceSession(r.Context(), wsID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, SessionDetailResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, SessionDetailResponse{Success: true, Data: result})
	}
}

// HandleGetSession returns metadata for a single session.
func HandleGetSession(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		result, err := svc.GetSession(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, SessionDetailResponse{})
			return
		}

		handler.WriteJSON(w, http.StatusOK, SessionDetailResponse{
			Success: true,
			Data:    result,
		})
	}
}

// HandleGetWorkspaceSessionTranscript returns transcript entries for a workspace session.
func HandleGetWorkspaceSessionTranscript(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionID := r.PathValue("sessionId")

		entries, err := svc.GetWorkspaceSessionTranscript(r.Context(), wsID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, TranscriptResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data:    &TranscriptData{SessionID: sessionID, Entries: entries},
		})
	}
}

// HandleGetSessionTranscript returns the transcript entries for a session.
func HandleGetSessionTranscript(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		entries, err := svc.GetSessionTranscript(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, TranscriptResponse{})
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
			Type:        rpc.MutationSessionChange,
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
func HandleListSessionSubagents(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		ids, err := svc.ListSessionSubagents(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, SubagentListResponse{})
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

// HandleListWorkspaceSessionSubagents returns captured subagent IDs for a workspace session.
func HandleListWorkspaceSessionSubagents(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionID := r.PathValue("sessionId")

		ids, err := svc.ListWorkspaceSessionSubagents(r.Context(), wsID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, SubagentListResponse{})
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
func HandleGetSessionSubagentTranscript(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")
		subagentID := r.PathValue("subagentId")

		events, err := svc.GetSessionSubagentTranscript(r.Context(), wsID, taskID, sessionID, subagentID)
		if err != nil {
			writeSessionServiceError(w, err, TranscriptResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data:    &TranscriptData{SessionID: sessionID + "/" + subagentID, Entries: events},
		})
	}
}

// HandleGetWorkspaceSessionSubagentTranscript returns the canonical event stream for a
// captured workspace session subagent transcript.
func HandleGetWorkspaceSessionSubagentTranscript(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionID := r.PathValue("sessionId")
		subagentID := r.PathValue("subagentId")

		events, err := svc.GetWorkspaceSessionSubagentTranscript(r.Context(), wsID, sessionID, subagentID)
		if err != nil {
			writeSessionServiceError(w, err, TranscriptResponse{})
			return
		}
		handler.WriteJSON(w, http.StatusOK, TranscriptResponse{
			Success: true,
			Data:    &TranscriptData{SessionID: sessionID + "/" + subagentID, Entries: events},
		})
	}
}

// HandleGetSessionDiff returns the diff.patch file for a session as plain text.
func HandleGetSessionDiff(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		taskID := r.PathValue("taskId")
		sessionID := r.PathValue("sessionId")

		diff, err := svc.GetSessionDiff(r.Context(), wsID, taskID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, map[string]interface{}{})
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
	}
}

// HandleGetWorkspaceSessionDiff returns the diff.patch file for a workspace session as plain text.
func HandleGetWorkspaceSessionDiff(svc service.SessionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		sessionID := r.PathValue("sessionId")

		diff, err := svc.GetWorkspaceSessionDiff(r.Context(), wsID, sessionID)
		if err != nil {
			writeSessionServiceError(w, err, map[string]interface{}{})
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(diff))
	}
}
