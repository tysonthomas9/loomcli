package issues

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/issuetabs"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// issueTabResponse wraps issue tab API responses.
type issueTabResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleGetIssueTabs returns the persisted tab state for an issue,
// filtering out terminal tabs whose tmux sessions have expired.
func handleGetIssueTabs(store *issuetabs.Store, manager *terminal.TerminalManager) http.HandlerFunc { //nolint:funlen
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false,
				Error:   "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		state, err := store.Get(r.Context(), wsID, issueID)
		if err != nil {
			slog.Error("failed to get issue tab state", "issue_id", issueID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, issueTabResponse{
				Success: false,
				Error:   "failed to get issue tab state",
			})
			return
		}

		if state == nil {
			handler.WriteJSON(w, http.StatusOK, issueTabResponse{
				Success: true,
				Data:    nil,
			})
			return
		}

		// Validate terminal tabs against active tmux sessions (filtered by workspace ownership)
		var activeNames []string
		if manager != nil {
			sessions, err := manager.ListActiveSessionsForWorkspace(wsID)
			if err != nil {
				slog.Error("failed to list active sessions for issue tab validation", "err", err)
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
				slog.Error("failed to save filtered issue tab state", "issue_id", issueID, "err", err)
			}
		}

		handler.WriteJSON(w, http.StatusOK, issueTabResponse{
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
func handleSaveIssueTabs(store *issuetabs.Store, hub *realtime.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false, Error: "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, issueTabResponse{Success: false, Error: err.Error()})
			return
		}

		state, err := decodeAndSaveTabState(store, r, w, wsID, issueID)
		if err != nil {
			return // response already written
		}

		broadcastTabChange(hub, wsID, issueID)
		handler.WriteJSON(w, http.StatusOK, issueTabResponse{Success: true, Data: state})
	}
}

// decodeAndSaveTabState decodes the request body and persists the tab state.
// Returns the saved state, or writes an error response and returns an error.
func decodeAndSaveTabState(store *issuetabs.Store, r *http.Request, w http.ResponseWriter, wsID, issueID string) (*issuetabs.IssueTabState, error) {
	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
	var req issueTabSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.WriteJSON(w, http.StatusBadRequest, issueTabResponse{Success: false, Error: "invalid request body"})
		return nil, err
	}

	state := &issuetabs.IssueTabState{
		IssueID:     issueID,
		Tabs:        req.Tabs,
		ActiveTabID: req.ActiveTabID,
	}

	if err := store.Save(r.Context(), wsID, state); err != nil {
		slog.Error("failed to save issue tab state", "issue_id", issueID, "err", err)
		handler.WriteJSON(w, http.StatusInternalServerError, issueTabResponse{
			Success: false, Error: "failed to save issue tab state",
		})
		return nil, err
	}
	return state, nil
}

// broadcastTabChange sends an SSE event for real-time tab sync.
func broadcastTabChange(hub *realtime.Hub, wsID, issueID string) {
	if hub != nil {
		hub.Broadcast(&realtime.MutationPayload{
			Type:        "issue_tabs",
			IssueID:     issueID,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			WorkspaceID: wsID,
		})
	}
}

// handleDeleteIssueTabs removes the tab state for an issue.
func handleDeleteIssueTabs(store *issuetabs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			handler.WriteJSON(w, http.StatusServiceUnavailable, issueTabResponse{
				Success: false,
				Error:   "issue tab state not available (no Redis)",
			})
			return
		}

		wsID := middleware.WorkspaceFromContext(r.Context())
		issueID := r.PathValue("issueId")
		if err := issuetabs.ValidateIssueID(issueID); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, issueTabResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		if err := store.Delete(r.Context(), wsID, issueID); err != nil {
			slog.Error("failed to delete issue tab state", "issue_id", issueID, "err", err)
			handler.WriteJSON(w, http.StatusInternalServerError, issueTabResponse{
				Success: false,
				Error:   "failed to delete issue tab state",
			})
			return
		}

		handler.WriteJSON(w, http.StatusOK, issueTabResponse{
			Success: true,
		})
	}
}
