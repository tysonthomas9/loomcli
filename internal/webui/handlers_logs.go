package webui

import (
	"log"
	"net/http"
	"regexp"
	"strconv"
)

// validAgentName matches alphanumeric characters, hyphens, and underscores.
var validAgentName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validTaskID matches UUID-like task IDs (e.g., "bd-abc123").
var validTaskID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validPhase matches allowed phase names.
var validPhase = regexp.MustCompile(`^(planning|implementation)$`)

// handleGetAgentLog returns the current log file content for an agent.
// GET /api/agents/{name}/logs
// Query params: ?lines=N (default 200, max 10000)
// Response: {success: true, data: {lines: [...], lineCount: N}}
func handleGetAgentLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get agent name from path
		agentName := r.PathValue("name")
		if agentName == "" {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "missing agent name",
			})
			return
		}

		// Validate agent name
		if !validAgentName.MatchString(agentName) {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "invalid agent name: must match [a-zA-Z0-9_-]+",
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

		// Get log file path
		logPath, err := getAgentLogPath(agentName)
		if err != nil {
			log.Printf("Agent log path error for %s: %v", agentName, err)
			respondJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to resolve log path",
			})
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			respondJSON(w, http.StatusNotFound, LogContentResponse{
				Success: false,
				Error:   "log file not found - agent may not be active",
			})
			return
		}

		// Read log content
		content, lineCount, err := readFileLastLines(logPath, lines)
		if err != nil {
			log.Printf("Failed to read agent log for %s: %v", agentName, err)
			respondJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to read log file",
			})
			return
		}

		respondJSON(w, http.StatusOK, LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     content,
				LineCount: lineCount + int64(len(content)) - 1,
			},
		})
	}
}

// handleListTaskPhases returns the available log phases for a task.
// GET /api/tasks/{id}/logs
// Response: {success: true, data: {phases: ["planning", "implementation"]}}
func handleListTaskPhases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			respondJSON(w, http.StatusBadRequest, TaskPhasesResponse{
				Success: false,
				Error:   "missing task ID",
			})
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, TaskPhasesResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			})
			return
		}

		// List available phases
		phases, err := listTaskPhases(taskID)
		if err != nil {
			log.Printf("Failed to list task phases for %s: %v", taskID, err)
			respondJSON(w, http.StatusInternalServerError, TaskPhasesResponse{
				Success: false,
				Error:   "failed to list task phases",
			})
			return
		}

		respondJSON(w, http.StatusOK, TaskPhasesResponse{
			Success: true,
			Data: &TaskPhasesData{
				Phases: phases,
			},
		})
	}
}

// handleGetTaskLog returns the current log file content for a task phase.
// GET /api/tasks/{id}/logs/{phase}
// Query params: ?lines=N (default 200, max 10000)
// Response: {success: true, data: {lines: [...], lineCount: N}}
func handleGetTaskLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "missing task ID",
			})
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			})
			return
		}

		// Get phase from path
		phase := r.PathValue("phase")
		if phase == "" {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
				Success: false,
				Error:   "missing phase",
			})
			return
		}

		// Validate phase
		if !validPhase.MatchString(phase) {
			respondJSON(w, http.StatusBadRequest, LogContentResponse{
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

		// Get log file path
		logPath, err := getTaskLogPath(taskID, phase)
		if err != nil {
			log.Printf("Task log path error for %s/%s: %v", taskID, phase, err)
			respondJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to resolve log path",
			})
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			respondJSON(w, http.StatusNotFound, LogContentResponse{
				Success: false,
				Error:   "log file not found - task phase may not have started",
			})
			return
		}

		// Read log content
		content, lineCount, err := readFileLastLines(logPath, lines)
		if err != nil {
			log.Printf("Failed to read task log for %s/%s: %v", taskID, phase, err)
			respondJSON(w, http.StatusInternalServerError, LogContentResponse{
				Success: false,
				Error:   "failed to read log file",
			})
			return
		}

		respondJSON(w, http.StatusOK, LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     content,
				LineCount: lineCount + int64(len(content)) - 1,
			},
		})
	}
}
