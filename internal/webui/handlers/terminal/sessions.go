package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type terminalSessionsResponse struct {
	Success bool                  `json:"success"`
	Data    *terminalSessionsData `json:"data,omitempty"`
	Error   string                `json:"error,omitempty"`
}

type terminalSessionsData struct {
	Sessions []service.TerminalSessionInfo `json:"sessions"`
}

// HandleListTerminalSessions returns a handler that lists active terminal sessions.
func HandleListTerminalSessions(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		sessions, err := svc.ListSessions(r.Context(), workspace)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, terminalSessionsResponse{
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

// HandleScheduleSessionKill schedules a deferred tmux session kill with a grace period.
// Pass ?force=true to kill immediately (for explicit user close).
func HandleScheduleSessionKill(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		force := r.URL.Query().Get("force") == "true"

		var err error
		if force {
			err = svc.KillSession(r.Context(), session)
		} else {
			err = svc.ScheduleKill(r.Context(), session)
		}
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// HandleListSessionsByIssue returns a map of issue_id -> session_names.
func HandleListSessionsByIssue(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionMap, err := svc.ListSessionsByIssue(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    sessionMap,
		})
	}
}

// HandleCloseAllSessions kills all tmux sessions and metadata.
func HandleCloseAllSessions(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := svc.CloseAllSessions(r.Context())
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		resp := map[string]interface{}{"success": true}
		if result.MetaCleanupIncomplete {
			resp["warning"] = "sessions killed but metadata cleanup incomplete"
		}

		handler.WriteJSON(w, http.StatusOK, resp)
	}
}

// HandleSeedTerminalSession returns a handler that seeds a terminal session with issue context.
func HandleSeedTerminalSession(svc service.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionName := r.PathValue("name")

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req seedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid JSON body: " + err.Error(),
			})
			return
		}

		params := &service.SeedParams{
			IssueID:     req.IssueID,
			Title:       req.Title,
			Description: req.Description,
			Design:      req.Design,
		}
		for _, b := range req.Blockers {
			params.Blockers = append(params.Blockers, service.SeedBlocker(b))
		}

		if err := svc.SeedSession(r.Context(), sessionName, params); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}
