package webui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
)

// tabMetadataResponse wraps tab metadata API responses.
type tabMetadataResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// handleListTerminalTabs returns all tab metadata, auto-creating defaults for new sessions.
func handleListTerminalTabs(store *tabmeta.Store, manager *TerminalManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not available (no Redis)",
			})
			return
		}

		workspace := WorkspaceFromContext(r.Context())

		// Get active sessions from tmux
		var activeNames []string
		if manager != nil {
			sessions, err := manager.ListActiveSessions()
			if err != nil {
				log.Printf("Failed to list active sessions for tab metadata: %v", err)
			} else {
				for _, s := range sessions {
					activeNames = append(activeNames, s.Name)
				}
			}
		}

		tabs, err := store.EnsureDefaults(r.Context(), workspace, activeNames)
		if err != nil {
			log.Printf("Failed to list tab metadata: %v", err)
			respondJSON(w, http.StatusInternalServerError, tabMetadataResponse{
				Success: false,
				Error:   "failed to list tab metadata",
			})
			return
		}

		respondJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    tabs,
		})
	}
}

// handleGetTerminalTab returns metadata for a single tab.
func handleGetTerminalTab(store *tabmeta.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not available (no Redis)",
			})
			return
		}

		workspace := WorkspaceFromContext(r.Context())

		session := r.PathValue("session")
		if err := tabmeta.ValidateSessionName(session); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		meta, err := store.Get(r.Context(), workspace, session)
		if err != nil {
			log.Printf("Failed to get tab metadata for %s: %v", session, err)
			respondJSON(w, http.StatusInternalServerError, tabMetadataResponse{
				Success: false,
				Error:   "failed to get tab metadata",
			})
			return
		}
		if meta == nil {
			respondJSON(w, http.StatusNotFound, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not found",
			})
			return
		}

		respondJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    meta,
		})
	}
}

// tabPatchRequest represents the partial update body for PATCH.
type tabPatchRequest struct {
	Label     *string `json:"label"`
	Notes     *string `json:"notes"`
	SortOrder *int    `json:"sort_order"`
	Pinned    *bool   `json:"pinned"`
	IssueID   *string `json:"issue_id"`
}

// buildPatchFields converts a tabPatchRequest into a partial fields map for store.Patch.
func buildPatchFields(req tabPatchRequest) (fields map[string]string, issueIDChanged bool) {
	fields = make(map[string]string)
	if req.Label != nil {
		fields["label"] = *req.Label
	}
	if req.Notes != nil {
		fields["notes"] = *req.Notes
	}
	if req.SortOrder != nil {
		fields["sort_order"] = fmt.Sprintf("%d", *req.SortOrder)
	}
	if req.Pinned != nil {
		fields["pinned"] = strconv.FormatBool(*req.Pinned)
	}
	if req.IssueID != nil {
		fields["issue_id"] = *req.IssueID
		issueIDChanged = true
	}
	return fields, issueIDChanged
}

// handlePatchTerminalTab partially updates tab metadata and broadcasts an SSE event.
func handlePatchTerminalTab(store *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not available (no Redis)",
			})
			return
		}

		workspace := WorkspaceFromContext(r.Context())

		session := r.PathValue("session")
		if err := tabmeta.ValidateSessionName(session); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req tabPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		fields, issueIDChanged := buildPatchFields(req)

		if len(fields) == 0 {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "no fields to update",
			})
			return
		}

		meta, err := store.Patch(r.Context(), workspace, session, fields)
		if err != nil {
			log.Printf("Failed to patch tab metadata for %s: %v", session, err)
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			}
			respondJSON(w, status, tabMetadataResponse{
				Success: false,
				Error:   "failed to update tab metadata",
			})
			return
		}

		// Broadcast SSE event for real-time sync
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:      "terminal_metadata",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		// Broadcast additional terminal_session_change event when issue linkage changes
		if issueIDChanged && hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:      "terminal_session_change",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		respondJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    meta,
		})
	}
}

// tabPutRequest represents the full create-or-replace body for PUT.
type tabPutRequest struct {
	Label     string `json:"label"`
	SortOrder int    `json:"sort_order"`
	Notes     string `json:"notes"`
	Pinned    bool   `json:"pinned"`
}

// handlePutTerminalTab creates or replaces tab metadata and broadcasts an SSE event.
func handlePutTerminalTab(store *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not available (no Redis)",
			})
			return
		}

		workspace := WorkspaceFromContext(r.Context())

		session := r.PathValue("session")
		if err := tabmeta.ValidateSessionName(session); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		var req tabPutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		if req.Label == "" {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "label is required",
			})
			return
		}

		now := time.Now().UTC()
		meta := &tabmeta.TabMetadata{
			SessionName: session,
			Workspace:   workspace,
			Label:       req.Label,
			Notes:       req.Notes,
			SortOrder:   req.SortOrder,
			Pinned:      req.Pinned,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := store.Set(r.Context(), meta); err != nil {
			log.Printf("Failed to put tab metadata for %s: %v", session, err)
			respondJSON(w, http.StatusInternalServerError, tabMetadataResponse{
				Success: false,
				Error:   "failed to create/replace tab metadata",
			})
			return
		}

		// Broadcast SSE event for real-time sync
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:      "terminal_metadata",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		respondJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    meta,
		})
	}
}

// handleDeleteTerminalTab removes tab metadata and broadcasts an SSE event.
func handleDeleteTerminalTab(store *tabmeta.Store, hub *SSEHub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			respondJSON(w, http.StatusServiceUnavailable, tabMetadataResponse{
				Success: false,
				Error:   "tab metadata not available (no Redis)",
			})
			return
		}

		workspace := WorkspaceFromContext(r.Context())

		session := r.PathValue("session")
		if err := tabmeta.ValidateSessionName(session); err != nil {
			respondJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		if err := store.Delete(r.Context(), workspace, session); err != nil {
			log.Printf("Failed to delete tab metadata for %s: %v", session, err)
			respondJSON(w, http.StatusInternalServerError, tabMetadataResponse{
				Success: false,
				Error:   "failed to delete tab metadata",
			})
			return
		}

		// Broadcast SSE event for real-time sync
		if hub != nil {
			hub.Broadcast(&MutationPayload{
				Type:      "terminal_metadata",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}

		respondJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
		})
	}
}
