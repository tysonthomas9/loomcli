package webui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
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
// It filters tmux sessions by the manager's sessionPrefix and always
// includes "talk-to-lead" as the default session.
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

func handleListTerminalSessions(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, terminalSessionsResponse{
				Success: false,
				Error:   "terminal manager not initialized",
			})
			return
		}

		sessions, err := manager.ListActiveSessions()
		if err != nil {
			log.Printf("Failed to list terminal sessions: %v", err)
			respondJSON(w, http.StatusInternalServerError, terminalSessionsResponse{
				Success: false,
				Error:   "failed to list terminal sessions",
			})
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
// POST /api/terminal/sessions/{session}/kill
func handleScheduleSessionKill(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		session := r.PathValue("session")
		if err := tabmeta.ValidateSessionName(session); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
			})
			return
		}

		manager.ScheduleKill(session, sessionKillGracePeriod)

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// handleListSessionsByIssue returns a map of issue_id → session_names for all sessions linked to issues.
// GET /api/terminal/sessions/by-issue
func handleListSessionsByIssue(tabMetaStore *tabmeta.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if tabMetaStore == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "tab metadata not available (no Redis)",
			})
			return
		}

		sessionMap, err := tabMetaStore.ListIssueSessionMap(r.Context())
		if err != nil {
			log.Printf("Failed to list sessions by issue: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to list sessions by issue",
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"data":    sessionMap,
		})
	}
}

// handleCloseAllSessions kills all tmux sessions, deletes all tab metadata, and broadcasts SSE event.
// POST /api/terminal/sessions/close-all
func handleCloseAllSessions(manager *TerminalManager, tabMetaStore *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		// Kill all tmux sessions
		if err := manager.KillAllSessions(); err != nil {
			log.Printf("Failed to kill all sessions: %v", err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to kill all sessions",
			})
			return
		}

		// Delete all tab metadata (across all workspaces)
		metaCleanupFailed := false
		if tabMetaStore != nil {
			allTabs, err := tabMetaStore.ListAll(r.Context())
			if err != nil {
				log.Printf("Failed to list tab metadata for cleanup: %v", err)
				metaCleanupFailed = true
			} else {
				for _, tab := range allTabs {
					if err := tabMetaStore.Delete(r.Context(), tab.Workspace, tab.SessionName); err != nil {
						log.Printf("Failed to delete tab metadata for %s: %v", tab.SessionName, err)
						metaCleanupFailed = true
					}
				}
			}
		}

		// Broadcast SSE event
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:      "terminal_session_change",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		if metaCleanupFailed {
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"success": true,
				"warning": "sessions killed but metadata cleanup incomplete",
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

func handleSeedTerminalSession(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		sessionName := r.PathValue("name")
		if sessionName == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "missing session name",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req seedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "invalid JSON body: " + err.Error(),
			})
			return
		}

		if req.IssueID == "" || req.Title == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "issue_id and title are required",
			})
			return
		}

		prompt := formatSeedPrompt(&req)

		if err := manager.SendKeys(sessionName, prompt); err != nil {
			if strings.Contains(err.Error(), "not found") {
				respondJSON(w, http.StatusNotFound, map[string]interface{}{
					"success": false,
					"error":   "session not found: " + sessionName,
				})
				return
			}
			log.Printf("Failed to seed terminal session %q: %v", sessionName, err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to seed terminal session",
			})
			return
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}
