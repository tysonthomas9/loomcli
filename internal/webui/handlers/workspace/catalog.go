package workspace

import (
	"context"
	"errors"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// CatalogProjection provides machine-local and cross-capability read data for
// the workspace HTTP representation. It cannot mutate the Workspace aggregate.
type CatalogProjection interface {
	ActiveWorkspaceKey(context.Context) string
	WorkspacePath(string) string
	WorkspaceTopology(context.Context, string) (*operationalview.Workspace, error)
}

type CatalogListItem struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Active    bool   `json:"active"`
	IsDefault bool   `json:"is_default"`
}

type WorkspaceRenameRequest struct {
	NewName string `json:"new_name"`
}

type WorkspaceDesignFormatPatchRequest struct {
	DesignFormat string `json:"design_format"`
}

func catalogUnavailable(w http.ResponseWriter, api workspacemodule.API, projection CatalogProjection) bool {
	if api != nil && projection != nil {
		return false
	}
	handler.RespondError(w, http.StatusServiceUnavailable, "Workspace capability unavailable")
	return true
}

func HandleCatalogList(api workspacemodule.API, projection CatalogProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogUnavailable(w, api, projection) {
			return
		}
		values, err := api.List(r.Context(), workspacemodule.ListQuery{})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		active := projection.ActiveWorkspaceKey(r.Context())
		items := make([]CatalogListItem, 0, len(values))
		for _, value := range values {
			items = append(items, CatalogListItem{
				ID: value.Key, Name: value.Name, Path: projection.WorkspacePath(value.Key),
				Active: value.Key == active, IsDefault: false,
			})
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "workspaces": items})
	}
}

func HandleCatalogGet(api workspacemodule.API, projection CatalogProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogUnavailable(w, api, projection) {
			return
		}
		reference := workspaceIDFromRequest(r)
		if reference == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		value, err := api.Resolve(r.Context(), workspacemodule.ResolveQuery{Reference: reference})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		data, err := projection.WorkspaceTopology(r.Context(), value.Key)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

func HandleCatalogRepositories(api workspacemodule.API, projection CatalogProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogUnavailable(w, api, projection) {
			return
		}
		reference := workspaceIDFromRequest(r)
		if reference == "" {
			handler.RespondError(w, http.StatusBadRequest, "workspace ID is required")
			return
		}
		workspace, err := api.Resolve(r.Context(), workspacemodule.ResolveQuery{Reference: reference})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		repositories, err := api.ListRepositories(r.Context(), workspacemodule.ListRepositoriesQuery{WorkspaceReference: workspace.Key})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		topology, err := projection.WorkspaceTopology(r.Context(), workspace.Key)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		localByName := make(map[string]operationalview.Repository, len(topology.Repos))
		for _, repository := range topology.Repos {
			localByName[repository.Name] = repository
		}
		items := make([]operationalview.Repository, 0, len(repositories))
		for _, repository := range repositories {
			local := localByName[repository.Name]
			defaultBranch := repository.DefaultBranch
			if defaultBranch == "" {
				defaultBranch = "main"
			}
			remote := repository.Remote
			if remote == "" {
				remote = "origin"
			}
			items = append(items, operationalview.Repository{
				Name: repository.Name, Path: local.Path, DefaultBranch: defaultBranch,
				CurrentBranch: local.CurrentBranch, Remote: remote, RemoteURL: repository.RemoteURL,
				SourceRepoID: repository.SourceRepoID, Groups: append([]string(nil), repository.Groups...),
				IsLinkedWorktree: local.IsLinkedWorktree,
			})
		}
		handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "repos": items})
	}
}

func HandleCatalogRename(api workspacemodule.API, projection CatalogProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogUnavailable(w, api, projection) {
			return
		}
		reference := middleware.WorkspaceFromContext(r.Context())
		if reference == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		var req WorkspaceRenameRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		value, err := api.Rename(r.Context(), workspacemodule.RenameCommand{Reference: reference, Name: req.NewName})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		data, err := projection.WorkspaceTopology(r.Context(), value.Key)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}

func HandleCatalogDesignFormatPatch(api workspacemodule.API, projection CatalogProjection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if catalogUnavailable(w, api, projection) {
			return
		}
		reference := middleware.WorkspaceFromContext(r.Context())
		if reference == "" {
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "workspace ID is required"})
			return
		}
		var req WorkspaceDesignFormatPatchRequest
		if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{MaxBytes: handler.MaxRequestBody}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				handler.WriteJSON(w, http.StatusRequestEntityTooLarge, WorkspaceResponse{Success: false, Error: "request body too large"})
				return
			}
			handler.WriteJSON(w, http.StatusBadRequest, WorkspaceResponse{Success: false, Error: "invalid request body"})
			return
		}
		value, err := api.SetDesignFormat(r.Context(), workspacemodule.SetDesignFormatCommand{Reference: reference, Format: req.DesignFormat})
		if err != nil {
			handler.HandleWorkspaceError(w, err)
			return
		}
		data, err := projection.WorkspaceTopology(r.Context(), value.Key)
		if err != nil {
			handler.HandleServiceError(w, err)
			return
		}
		handler.WriteJSON(w, http.StatusOK, WorkspaceResponse{Success: true, Data: data})
	}
}
