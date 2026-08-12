package terminal

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
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
func HandlePatchTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
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
			handler.HandleServiceError(w, err)
			return
		}

		handler.WriteJSON(w, http.StatusOK, tabMetadataResponse{
			Success: true,
			Data:    result.Tab,
		})
	}
}

func newTabMetadata(workspace, session string, req loomapi.TabPutRequest) (*terminal.TabMetadata, error) {
	backend := strings.ToLower(strings.TrimSpace(string(req.Backend)))
	launch, err := terminal.LaunchSpecForBackend(backend, bootstrap.LoomDir())
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &terminal.TabMetadata{
		SessionName: session,
		Workspace:   workspace,
		Label:       req.Label,
		Notes:       req.Notes,
		SortOrder:   req.SortOrder,
		Pinned:      req.Pinned,
		Backend:     backend,
		Launch:      launch,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// HandlePutTerminalTab creates or replaces tab metadata and broadcasts an SSE event.
func HandlePutTerminalTab(svc terminal.TerminalService) http.HandlerFunc {
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
		meta, err := newTabMetadata(workspace, session, req)
		if err != nil {
			handler.WriteJSON(w, http.StatusBadRequest, tabMetadataResponse{
				Success: false,
				Error:   err.Error(),
			})
			return
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
