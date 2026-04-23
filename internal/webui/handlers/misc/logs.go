package misc

import (
	"log/slog"
	"net/http"
	"regexp"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// parseBeforeLine extracts and validates the before_line query parameter.
// Returns 0 if not present or invalid (meaning: read from EOF).
func parseBeforeLine(r *http.Request) int64 {
	if blParam := r.URL.Query().Get("before_line"); blParam != "" {
		if bl, err := strconv.ParseInt(blParam, 10, 64); err == nil && bl > 0 {
			return bl
		}
	}
	return 0
}

// validAgentName matches alphanumeric characters, hyphens, and underscores.
var validAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validTaskID matches task IDs (e.g., "bd-abc123", "loomcli-5y1sd.1").
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validPhase matches allowed phase names.
var validPhase = regexp.MustCompile(`^(planning|implementation)$`)

// HandleGetAgentLog returns the current log file content for an agent.
// GET /api/workspaces/{ws}/agents/{name}/logs
// Query params: ?lines=N (default 200, max 10000)
// Response: {success: true, data: {lines: [...], lineCount: N}}
func HandleGetAgentLog(svc service.AgentService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wsID := middleware.WorkspaceFromContext(r.Context())
		agentName := r.PathValue("name")

		// Parse lines parameter (HTTP concern)
		lines := logReadDefaultLines
		if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
			if n, err := strconv.Atoi(linesParam); err == nil && n > 0 {
				lines = n
				if lines > logReadMaxLines {
					lines = logReadMaxLines
				}
			}
		}

		beforeLine := parseBeforeLine(r)

		result, err := svc.GetLog(r.Context(), wsID, agentName, lines, beforeLine)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     result.Lines,
				LineCount: result.LineCount,
				StartLine: result.StartLine,
			},
		})
	}
}

// HandleListTaskPhases returns the available log phases for a task.
// GET /api/workspaces/{ws}/tasks/{id}/logs
// Response: {success: true, data: {phases: ["planning", "implementation"]}}
func HandleListTaskPhases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get workspace ID from context (injected by WorkspaceMiddleware)
		wsID := middleware.WorkspaceFromContext(r.Context())

		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, TaskPhasesResponse{
				Success: false,
				Error:   "missing task ID",
			})
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			handler.WriteJSON(w, http.StatusBadRequest, TaskPhasesResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9._-]+",
			})
			return
		}

		// List available phases (workspace-scoped)
		phases, err := listTaskPhases(wsID, taskID)
		if err != nil {
			slog.Error("failed to list task phases", "task_id", taskID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, TaskPhasesResponse{
				Success: false,
				Error:   "failed to list task phases",
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, TaskPhasesResponse{
			Success: true,
			Data: &TaskPhasesData{
				Phases: phases,
			},
		})
	}
}

// HandleGetTaskLog returns the current log file content for a task phase.
// GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}
// Query params: ?lines=N (default 200, max 10000)
// Response: {success: true, data: {lines: [...], lineCount: N}}
func HandleGetTaskLog() http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		// Get workspace ID from context (injected by WorkspaceMiddleware)
		wsID := middleware.WorkspaceFromContext(r.Context())

		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			handler.WriteJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "missing task ID",
			})
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			handler.WriteJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9._-]+",
			})
			return
		}

		// Get phase from path
		phase := r.PathValue("phase")
		if phase == "" {
			handler.WriteJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "missing phase",
			})
			return
		}

		// Validate phase
		if !validPhase.MatchString(phase) {
			handler.WriteJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "invalid phase: must be 'planning' or 'implementation'",
			})
			return
		}

		// Parse lines parameter
		lines := logReadDefaultLines
		if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
			if n, err := strconv.Atoi(linesParam); err == nil && n > 0 {
				lines = n
				if lines > logReadMaxLines {
					lines = logReadMaxLines
				}
			}
		}

		// Get log file path (workspace-scoped)
		logPath, err := getTaskLogPath(wsID, taskID, phase)
		if err != nil {
			slog.Error("task log path error", "task_id", taskID, "phase", phase, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to resolve log path",
			})
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			handler.WriteJSON(w, http.StatusNotFound, LogContentResponse{
				Success: false,
				Error:   "log file not found - task phase may not have started",
			})
			return
		}

		// Parse before_line for pagination
		beforeLine := parseBeforeLine(r)

		// Read log content
		content, startLine, err := readFileLastLines(logPath, lines, beforeLine)
		if err != nil {
			slog.Error("failed to read task log", "task_id", taskID, "phase", phase, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to read log file",
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     content,
				LineCount: startLine + int64(len(content)) - 1,
				StartLine: startLine,
			},
		})
	}
}
