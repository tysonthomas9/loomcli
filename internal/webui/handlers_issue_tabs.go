package webui

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
)

// issueTabResponse wraps issue tab API responses.
type issueTabResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleGetIssueTabs returns the persisted tab state for an issue,
// filtering out terminal tabs whose tmux sessions have expired.
func handleGetIssueTabs(store *issuetabs.Store, manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false,
				Error:   "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			respondJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		state, err := store.Get(r.Context(), wsID, issueID)
		if err != nil {
			log.Printf("Failed to get issue tab state for %s: %v", issueID, err)
			respondJSON(w, http.StatusInternalServerError, issueTabResponse{
				Success: false,
				Error:   "failed to get issue tab state",
			})
			return
		}

		if state == nil {
			respondJSON(w, http.StatusOK, issueTabResponse{
				Success: true,
				Data:    nil,
			})
			return
		}

		// Validate terminal tabs against active tmux sessions in this workspace.
		var activeNames []string
		if manager != nil && wsID != "" {
			sessions, err := manager.ListWorkspaceSessions(wsID)
			if err != nil {
				log.Printf("Failed to list active sessions for issue tab validation: %v", err)
			} else {
				for _, s := range sessions {
					activeNames = append(activeNames, s.Name)
				}
			}
		}

		filtered := issuetabs.ValidateAndFilter(state, activeNames)

		// Save back filtered state if tabs were removed or active tab changed
		if len(filtered.Tabs) != len(state.Tabs) || filtered.ActiveTabID != state.ActiveTabID {
			if err := store.Save(r.Context(), wsID, filtered); err != nil {
				log.Printf("Failed to save filtered issue tab state for %s: %v", issueID, err)
			}
		}

		respondJSON(w, http.StatusOK, issueTabResponse{
			Success: true,
			Data:    filtered,
		})
	}
}

// issueTabSaveRequest represents the body for PUT /api/issues/{issueId}/tabs.
type issueTabSaveRequest struct {
	Tabs        []issuetabs.IssueTab `json:"tabs"`
	ActiveTabID string               `json:"active_tab_id"`
}

// handleSaveIssueTabs saves the full tab state for an issue and broadcasts an SSE event.
func handleSaveIssueTabs(store *issuetabs.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false,
				Error:   "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			respondJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req issueTabSaveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		state := &issuetabs.IssueTabState{
			IssueID:     issueID,
			Tabs:        req.Tabs,
			ActiveTabID: req.ActiveTabID,
		}

		if err := store.Save(r.Context(), wsID, state); err != nil {
			log.Printf("Failed to save issue tab state for %s: %v", issueID, err)
			respondJSON(w, http.StatusInternalServerError, issueTabResponse{
				Success: false,
				Error:   "failed to save issue tab state",
			})
			return
		}

		// Broadcast SSE event for real-time sync — workspace derived from middleware context.
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:        "issue_tabs",
				IssueID:     issueID,
				Timestamp:   time.Now().UTC().Format(time.RFC3339),
				WorkspaceID: wsID,
			})
		}

		respondJSON(w, http.StatusOK, issueTabResponse{
			Success: true,
			Data:    state,
		})
	}
}

// handleDeleteIssueTabs removes the tab state for an issue.
func handleDeleteIssueTabs(store *issuetabs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false,
				Error:   "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			respondJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		if err := store.Delete(r.Context(), wsID, issueID); err != nil {
			log.Printf("Failed to delete issue tab state for %s: %v", issueID, err)
			respondJSON(w, http.StatusInternalServerError, issueTabResponse{
				Success: false,
				Error:   "failed to delete issue tab state",
			})
			return
		}

		respondJSON(w, http.StatusOK, issueTabResponse{
			Success: true,
		})
	}
}
