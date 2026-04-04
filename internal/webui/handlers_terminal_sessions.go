package webui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// terminalSessionInfo describes a terminal session visible to the frontend.
type terminalSessionInfo struct {
	Name    string `json:"name"`               // user-facing name, e.g. "talk-to-lead"
	Label   string `json:"label"`              // display label (same as name for now)
	Created int64  `json:"created"`            // Unix timestamp, 0 if session not yet created
	IssueID string `json:"issue_id,omitempty"` // optional linked issue ID
}

type terminalSessionsResponse struct {
	Success bool                  `json:"success"`
	Data    *terminalSessionsData `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

type terminalSessionsData struct {
	Sessions []terminalSessionInfo `json:"sessions"`
}

// ListActiveSessions returns sessions owned by this server instance.
func (m *TerminalManager) ListActiveSessions() ([]terminalSessionInfo, error) {
	m.mu.RLock()
	sessionPrefix := m.sessionPrefix
	m.mu.RUnlock()

	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := sessionPrefix + "-"
	hasTalkToLead := false
	var result []terminalSessionInfo

	for _, s := range allSessions {
		var name string
		if sessionPrefix == "" {
			name = s.name
		} else if strings.HasPrefix(s.name, prefix) {
			name = strings.TrimPrefix(s.name, prefix)
		} else {
			continue
		}

		result = append(result, terminalSessionInfo{
			Name:    name,
			Label:   name,
			Created: s.created,
		})
		if name == "talk-to-lead" {
			hasTalkToLead = true
		}
	}

	if !hasTalkToLead {
		result = append([]terminalSessionInfo{{
			Name:    "talk-to-lead",
			Label:   "talk-to-lead",
			Created: 0,
		}}, result...)
	}

	return result, nil
}

func handleListTerminalSessions(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		sessions, err := svc.ListSessions(r.Context(), workspace)
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, terminalSessionsResponse{
			Success: true,
			Data: &terminalSessionsData{
				Sessions: sessions,
			},
		})
	}
}

// seedRequest is the JSON body for POST /api/terminal/sessions/{name}/seed.
type seedRequest struct {
	IssueID     string        `json:"issue_id"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	Design      string        `json:"design,omitempty"`
	Blockers    []seedBlocker `json:"blockers,omitempty"`
}

type seedBlocker struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

const (
	maxDescriptionLen = 800
	maxDesignLen      = 500
	maxBlockers       = 5
)

// truncate returns s truncated to maxLen runes with "..." suffix if needed.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// formatSeedPrompt builds the context prompt string from a seed request.
func formatSeedPrompt(req *seedRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "I need help with issue %s: %s", req.IssueID, req.Title)

	if req.Description != "" {
		fmt.Fprintf(&b, "\n\nDescription: %s", truncate(req.Description, maxDescriptionLen))
	}

	if req.Design != "" {
		fmt.Fprintf(&b, "\n\nDesign: %s", truncate(req.Design, maxDesignLen))
	}

	if len(req.Blockers) > 0 {
		b.WriteString("\n\nBlockers:")
		limit := len(req.Blockers)
		if limit > maxBlockers {
			limit = maxBlockers
		}
		for _, blocker := range req.Blockers[:limit] {
			fmt.Fprintf(&b, "\n- %s: %s", blocker.ID, blocker.Title)
		}
	}

	return b.String()
}

// sessionKillGracePeriod is the delay before a scheduled session kill executes.
const sessionKillGracePeriod = 30 * time.Second

// handleScheduleSessionKill schedules a deferred tmux session kill with a grace period.
func handleScheduleSessionKill(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		if err := svc.ScheduleKill(r.Context(), session); err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// handleListSessionsByIssue returns a map of issue_id → session_names.
func handleListSessionsByIssue(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionMap, err := svc.ListSessionsByIssue(r.Context())
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    sessionMap,
		})
	}
}

// handleCloseAllSessions kills all tmux sessions and metadata.
func handleCloseAllSessions(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := svc.CloseAllSessions(r.Context())
		if err != nil {
			WriteServiceError(w, err)
			return
		}

		resp := map[string]interface{}{"success": true}
		if result.MetaCleanupIncomplete {
			resp["warning"] = "sessions killed but metadata cleanup incomplete"
		}

		respondJSON(w, http.StatusOK, resp)
	}
}

func handleSeedTerminalSession(svc TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionName := r.PathValue("name")

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req seedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid JSON body: " + err.Error(),
			})
			return
		}

		params := &SeedParams{
			IssueID:     req.IssueID,
			Title:       req.Title,
			Description: req.Description,
			Design:      req.Design,
		}
		for _, b := range req.Blockers {
			params.Blockers = append(params.Blockers, SeedBlocker(b))
		}

		if err := svc.SeedSession(r.Context(), sessionName, params); err != nil {
			WriteServiceError(w, err)
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}
