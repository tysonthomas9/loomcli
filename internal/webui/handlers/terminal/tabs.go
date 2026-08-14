package terminal

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// tabMetadataResponse wraps tab metadata API responses.
type tabMetadataResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// HandleListTerminalTabs returns all tab metadata, auto-creating defaults for new sessions.
func HandleListTerminalTabs(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())

		tabs, err := svc.ListTabs(r.Context(), workspace)
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    terminalTabDTOs(tabs),
		})
	}
}

// HandleGetTerminalTab returns metadata for a single tab.
func HandleGetTerminalTab(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		meta, err := svc.GetTab(r.Context(), workspace, session)
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    terminalTabDTO(meta),
		})
	}
}

// buildPatchFields converts the generated request into a partial fields map for store.Patch.
func buildPatchFields(req loomapi.TabPatchRequest) map[string]string {
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
	if req.IssueId != nil {
		fields["issue_id"] = *req.IssueId
	}
	return fields
}

// HandlePatchTerminalTab partially updates tab metadata and broadcasts an SSE event.
func HandlePatchTerminalTab(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		var req loomapi.TabPatchRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{DisallowUnknownFields: true}); err != nil {
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
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    terminalTabDTO(result.Tab),
		})
	}
}

// HandlePutTerminalTab creates or replaces tab metadata and broadcasts an SSE event.
func HandlePutTerminalTab(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		var req loomapi.TabPutRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{DisallowUnknownFields: true}); err != nil {
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
		meta, err := svc.PutTab(r.Context(), interaction.PutTerminalTabCommand{
			WorkspaceKey: workspace,
			TerminalID:   session,
			Label:        req.Label,
			Notes:        req.Notes,
			SortOrder:    req.SortOrder,
			Pinned:       req.Pinned,
			Backend:      string(req.Backend),
		})
		if err != nil {
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    terminalTabDTO(meta),
		})
	}
}

// HandleDeleteTerminalTab removes tab metadata and broadcasts an SSE event.
func HandleDeleteTerminalTab(svc interaction.TerminalTabs) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspace := middleware.WorkspaceFromContext(r.Context())
		session := r.PathValue("session")

		if err := svc.DeleteTab(r.Context(), workspace, session); err != nil {
			handler.HandleTerminalError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
		})
	}
}
