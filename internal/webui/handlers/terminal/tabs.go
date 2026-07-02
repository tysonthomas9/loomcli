package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/worktreegroups"
)

// tabMetadataResponse wraps tab metadata API responses.
type tabMetadataResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// HandleListTerminalTabs returns all tab metadata, auto-creating defaults for new sessions.
func HandleListTerminalTabs(svc service.TerminalService) http.HandlerFunc {
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
func HandleGetTerminalTab(svc service.TerminalService) http.HandlerFunc {
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
func HandlePatchTerminalTab(svc service.TerminalService) http.HandlerFunc {
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
	Label           string `json:"label"`
	SortOrder       int    `json:"sort_order"`
	Notes           string `json:"notes"`
	Pinned          bool   `json:"pinned"`
	WorktreeGroupID string `json:"worktree_group_id"`
}

// HandlePutTerminalTab creates or replaces tab metadata and broadcasts an SSE event.
func HandlePutTerminalTab(svc service.TerminalService, workspaceStore store.Store, worktreeGroupStore *worktreegroups.Store) http.HandlerFunc {
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

		worktreeGroupID, launch, err := resolveTabWorktreeLaunch(r.Context(), workspaceStore, worktreeGroupStore, workspace, session, req.WorktreeGroupID)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}

		meta := newTabPutMetadata(workspace, session, req, worktreeGroupID, launch)

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

func newTabPutMetadata(workspace, session string, req tabPutRequest, worktreeGroupID string, launch *tabmeta.LaunchSpec) *tabmeta.TabMetadata {
	now := time.Now().UTC()
	return &tabmeta.TabMetadata{
		SessionName:     session,
		Workspace:       workspace,
		Label:           req.Label,
		Notes:           req.Notes,
		SortOrder:       req.SortOrder,
		Pinned:          req.Pinned,
		WorktreeGroupID: worktreeGroupID,
		Launch:          launch,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func resolveTabWorktreeLaunch(ctx context.Context, workspaceStore store.Store, worktreeGroupStore *worktreegroups.Store, workspace, session, requestedID string) (string, *tabmeta.LaunchSpec, error) {
	groupID := strings.TrimSpace(requestedID)
	if groupID == "" || groupID == tabmeta.DefaultWorktreeGroupID {
		return tabmeta.DefaultWorktreeGroupID, nil, nil
	}
	if workspaceStore == nil || worktreeGroupStore == nil {
		slog.Warn("terminal worktree group store unavailable; falling back to workspace group",
			"workspace", workspace, "session", session, "worktree_group_id", groupID)
		return tabmeta.DefaultWorktreeGroupID, nil, nil
	}

	group, err := findWorktreeGroupByID(ctx, worktreeGroupStore, workspace, groupID)
	if err != nil {
		return "", nil, service.ErrInternal("find terminal worktree group", err)
	}
	if group == nil {
		slog.Warn("terminal worktree group not found; falling back to workspace group",
			"workspace", workspace, "session", session, "worktree_group_id", groupID)
		return tabmeta.DefaultWorktreeGroupID, nil, nil
	}

	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, workspaceStore, workspace)
	if err != nil {
		return "", nil, service.ErrNotFound(fmt.Sprintf("workspace %q not found", workspace))
	}
	cwd, err := localworkspace.TerminalGroupRootPath(wsData.Path, group.Name)
	if err != nil {
		return "", nil, service.ErrValidation(err.Error())
	}
	return groupID, &tabmeta.LaunchSpec{
		Cwd:  cwd,
		Argv: webuterminal.ArgvForSession(session),
	}, nil
}

func findWorktreeGroupByID(ctx context.Context, store *worktreegroups.Store, workspace, groupID string) (*worktreegroups.TerminalWorktreeGroup, error) {
	groups, err := store.List(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		if group.ID == groupID {
			found := group
			return &found, nil
		}
	}
	return nil, nil
}

// HandleDeleteTerminalTab removes tab metadata and broadcasts an SSE event.
func HandleDeleteTerminalTab(svc service.TerminalService) http.HandlerFunc {
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
