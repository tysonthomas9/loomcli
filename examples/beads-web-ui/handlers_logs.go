package main

import (
	"encoding/json"
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
		w.Header().Set("Content-Type", "application/json")

		// Get agent name from path
		agentName := r.PathValue("name")
		if agentName == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing agent name",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate agent name
		if !validAgentName.MatchString(agentName) {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid agent name: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
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
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			w.WriteHeader(http.StatusNotFound)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "log file not found - agent may not be active",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Read log content
		content, lineCount, err := readFileLastLines(logPath, lines)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     content,
				LineCount: lineCount + int64(len(content)) - 1,
			},
		}); err != nil {
			log.Printf("Failed to encode log response: %v", err)
		}
	}
}

// handleAgentLogStream returns an SSE endpoint for real-time agent log streaming.
// GET /api/agents/{name}/logs/stream
// Query params: ?since=<line_number> for catch-up
// Events: event: log-line, data: {line: "...", lineNumber: N}
func handleAgentLogStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get agent name from path
		agentName := r.PathValue("name")
		if agentName == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing agent name",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate agent name
		if !validAgentName.MatchString(agentName) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid agent name: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Get log file path
		logPath, err := getAgentLogPath(agentName)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "log file not found - agent may not be active",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Parse since parameter
		var startLine int64 = 1
		if since := r.URL.Query().Get("since"); since != "" {
			if n, err := strconv.ParseInt(since, 10, 64); err == nil && n > 0 {
				startLine = n
			}
		}

		// Create log streamer
		streamer, err := NewLogStreamerFixed(logPath)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}
		defer streamer.Close()

		// Stream logs (blocks until context cancelled)
		if err := streamer.Stream(r.Context(), w, startLine); err != nil {
			log.Printf("Log stream error for agent %s: %v", agentName, err)
		}
	}
}

// handleListTaskPhases returns the available log phases for a task.
// GET /api/tasks/{id}/logs
// Response: {success: true, data: {phases: ["planning", "implementation"]}}
func handleListTaskPhases() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(TaskPhasesResponse{
				Success: false,
				Error:   "missing task ID",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(TaskPhasesResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// List available phases
		phases, err := listTaskPhases(taskID)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(TaskPhasesResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(TaskPhasesResponse{
			Success: true,
			Data: &TaskPhasesData{
				Phases: phases,
			},
		}); err != nil {
			log.Printf("Failed to encode log response: %v", err)
		}
	}
}

// handleGetTaskLog returns the current log file content for a task phase.
// GET /api/tasks/{id}/logs/{phase}
// Query params: ?lines=N (default 200, max 10000)
// Response: {success: true, data: {lines: [...], lineCount: N}}
func handleGetTaskLog() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing task ID",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Get phase from path
		phase := r.PathValue("phase")
		if phase == "" {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing phase",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate phase
		if !validPhase.MatchString(phase) {
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid phase: must be 'planning' or 'implementation'",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
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
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			w.WriteHeader(http.StatusNotFound)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "log file not found - task phase may not have started",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Read log content
		content, lineCount, err := readFileLastLines(logPath, lines)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(LogContentResponse{
			Success: true,
			Data: &LogContentData{
				Lines:     content,
				LineCount: lineCount + int64(len(content)) - 1,
			},
		}); err != nil {
			log.Printf("Failed to encode log response: %v", err)
		}
	}
}

// handleTaskLogStream returns an SSE endpoint for real-time task log streaming.
// GET /api/tasks/{id}/logs/{phase}/stream
// Query params: ?since=<line_number> for catch-up
// Events: event: log-line, data: {line: "...", lineNumber: N}
func handleTaskLogStream() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get task ID from path
		taskID := r.PathValue("id")
		if taskID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing task ID",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate task ID
		if !validTaskID.MatchString(taskID) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid task ID: must match [a-zA-Z0-9_-]+",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Get phase from path
		phase := r.PathValue("phase")
		if phase == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "missing phase",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Validate phase
		if !validPhase.MatchString(phase) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "invalid phase: must be 'planning' or 'implementation'",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Get log file path
		logPath, err := getTaskLogPath(taskID, phase)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Check if file exists
		if !fileExists(logPath) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   "log file not found - task phase may not have started",
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}

		// Parse since parameter
		var startLine int64 = 1
		if since := r.URL.Query().Get("since"); since != "" {
			if n, err := strconv.ParseInt(since, 10, 64); err == nil && n > 0 {
				startLine = n
			}
		}

		// Create log streamer
		streamer, err := NewLogStreamerFixed(logPath)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			if err := json.NewEncoder(w).Encode(LogContentResponse{
				Success: false,
				Error:   err.Error(),
			}); err != nil {
				log.Printf("Failed to encode log response: %v", err)
			}
			return
		}
		defer streamer.Close()

		// Stream logs (blocks until context cancelled)
		if err := streamer.Stream(r.Context(), w, startLine); err != nil {
			log.Printf("Log stream error for task %s/%s: %v", taskID, phase, err)
		}
	}
}
