package terminal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// tabMetadataResponse wraps tab metadata API responses.
type tabMetadataResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// HandleListTerminalTabs returns all tab metadata, auto-creating defaults for new sessions.
func HandleListTerminalTabs(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())

		tabs, err := svc.ListTabs(r.Context(), workspace)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    tabs,
		})
	}
}

// HandleGetTerminalTab returns metadata for a single tab.
func HandleGetTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		meta, err := svc.GetTab(r.Context(), workspace, session)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
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
func buildPatchFields(req tabPatchRequest) map[string]string {
	fields := make(map[string]string)
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
	}
	return fields
}

// HandlePatchTerminalTab partially updates tab metadata and broadcasts an SSE event.
func HandlePatchTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req tabPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		fields := buildPatchFields(req)
		if len(fields) == 0 {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "no fields to update",
			})
			return
		}

		result, err := svc.PatchTab(r.Context(), workspace, session, fields)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    result.Tab,
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

// HandlePutTerminalTab creates or replaces tab metadata and broadcasts an SSE event.
func HandlePutTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req tabPutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		if req.Label == "" {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
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

		if err := svc.PutTab(r.Context(), workspace, meta); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    meta,
		})
	}
}

// HandleDeleteTerminalTab removes tab metadata and broadcasts an SSE event.
func HandleDeleteTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		if err := svc.DeleteTab(r.Context(), workspace, session); err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
		})
	}
}
