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

// ListWorkspaceSessions returns the sessions owned by this server instance
// that belong to the given workspace. It filters tmux sessions by the
// "<serverPrefix>-<wsShort>-" prefix and always includes "talk-to-lead" as
// the default session for that workspace, whether or not it exists yet.
// wsID must be non-empty.
func (m *TerminalManager) ListWorkspaceSessions(wsID string) ([]terminalSessionInfo, error) {
	if wsID == "" {
		return nil, fmt.Errorf("wsID must not be empty")
	}

	allSessions, err := m.listTmuxSessions()
	if err != nil {
		return nil, err
	}

	prefix := m.workspacePrefix(wsID)
	hasTalkToLead := false
	var result []terminalSessionInfo

	for _, s := range allSessions {
		if !strings.HasPrefix(s.name, prefix) {
			continue
		}
		name := strings.TrimPrefix(s.name, prefix)
		if name == "" {
			// Defensive: an internal name equal to the prefix has no
			// user-facing component — should be impossible but skip just
			// in case tmux ever returns something unexpected.
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

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, terminalSessionsResponse{
				Success: false,
				Error:   "workspace context required",
			})
			return
		}

		sessions, err := manager.ListWorkspaceSessions(wsID)
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
// POST /api/workspaces/{ws}/terminal/sessions/{session}/kill
func handleScheduleSessionKill(manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "workspace context required",
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

		if r.URL.Query().Get("force") == "true" {
			// Force kill: close all connections and destroy the tmux session immediately.
			if err := manager.KillSession(wsID, session); err != nil {
				respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"success": false,
					"error":   err.Error(),
				})
				return
			}
		} else {
			manager.ScheduleKill(wsID, session, sessionKillGracePeriod)
		}

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
		})
	}
}

// handleListSessionsByIssue returns a map of issue_id → session_names for all sessions linked to issues.
// GET /api/workspaces/{ws}/terminal/sessions/by-issue
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

// handleCloseAllSessions kills every tmux session belonging to the request's
// workspace and broadcasts a single SSE event. If a tab metadata store is
// configured, this workspace's metadata entries are also deleted so the UI
// doesn't show phantom tabs after the kill.
//
// Registered on wsMux at POST /api/workspaces/{ws}/terminal/sessions/close-all.
// The workspace is derived from the request context (injected by
// WorkspaceMiddleware). Sessions in other workspaces are untouched — the
// kill is scoped via TerminalManager.KillWorkspaceSessions, which filters by
// the "<serverPrefix>-<wsShort>-" prefix on tmux session names. This works
// with or without Redis because the scoping lives inside TerminalManager.
func handleCloseAllSessions(manager *TerminalManager, tabMetaStore *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			respondJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
				"success": false,
				"error":   "terminal manager not initialized",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "workspace context required",
			})
			return
		}

		// Kill every tmux session in this workspace via the manager's
		// prefix-scoped helper. This catches both sessions with active PTY
		// connections and detached spawned-only sessions — no need to list
		// via tabmeta first.
		if err := manager.KillWorkspaceSessions(wsID); err != nil {
			log.Printf("Failed to kill workspace sessions for %q: %v", wsID, err)
			respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"success": false,
				"error":   "failed to kill workspace sessions",
			})
			return
		}

		// Delete this workspace's tab metadata (best-effort). Kept on the
		// request path rather than inside the manager so TerminalManager
		// stays oblivious to Redis.
		metaCleanupFailed := false
		if tabMetaStore != nil {
			tabs, err := tabMetaStore.List(r.Context(), wsID)
			if err != nil {
				log.Printf("Failed to list tab metadata for workspace %q: %v", wsID, err)
				metaCleanupFailed = true
			} else {
				for _, tab := range tabs {
					if err := tabMetaStore.Delete(r.Context(), wsID, tab.SessionName); err != nil {
						log.Printf("Failed to delete tab metadata for %s: %v", tab.SessionName, err)
						metaCleanupFailed = true
					}
				}
			}
		}

		// Single SSE broadcast for this workspace.
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:        "terminal_session_change",
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				WorkspaceID: wsID,
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

		wsID := WorkspaceFromContext(r.Context())
		if wsID == "" {
			respondJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   "workspace context required",
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

		if err := manager.SendKeys(wsID, sessionName, prompt); err != nil {
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
