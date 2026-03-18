package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"
)

// WorkspaceCreateRequest is the JSON body for POST /api/workspace/create.
type WorkspaceCreateRequest struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`      // "empty", "clone", "template"
	Repos    []string `json:"repos"`     // repo paths (for empty type)
	CloneURL string   `json:"clone_url"` // git URL (for clone type)
	Branch   string   `json:"branch"`    // optional branch name
	Path     string   `json:"path"`      // optional workspace directory override
}

// WorkspaceCreateFn is the function signature for creating a workspace.
// Injected at server startup to decouple webui from CLI internals.
type WorkspaceCreateFn func(ctx context.Context, req WorkspaceCreateRequest) error

var cloneURLPattern = regexp.MustCompile(`^(https://|git@)`)

const workspaceCreateTimeout = 60 * time.Second

// validateWorkspaceCreateRequest validates the create request fields.
// Returns an HTTP status code and error message, or 0/"" if valid.
func validateWorkspaceCreateRequest(req *WorkspaceCreateRequest) (int, string) {
	if req.Name == "" {
		return http.StatusBadRequest, "name is required"
	}
	if len(req.Name) > maxWorkspaceNameLen {
		return http.StatusBadRequest, fmt.Sprintf("name too long (max %d characters)", maxWorkspaceNameLen)
	}
	if !validWorkspaceName(req.Name) {
		return http.StatusBadRequest, "name must contain only alphanumeric characters, hyphens, and underscores"
	}

	switch req.Type {
	case "empty":
		if len(req.Repos) == 0 {
			return http.StatusBadRequest, "repos is required for empty workspace type"
		}
	case "clone":
		if req.CloneURL == "" {
			return http.StatusBadRequest, "clone_url is required for clone workspace type"
		}
		if !cloneURLPattern.MatchString(req.CloneURL) {
			return http.StatusBadRequest, "clone_url must start with https:// or git@"
		}
	case "template":
		return http.StatusNotImplemented, "template workspace type is not yet supported"
	case "":
		return http.StatusBadRequest, "type is required"
	default:
		return http.StatusBadRequest, fmt.Sprintf("invalid type %q; must be empty, clone, or template", req.Type)
	}

	return 0, ""
}

// handleWorkspaceCreate returns a handler that creates a new workspace.
// createFn performs the actual workspace creation (git clone/init, config save).
// workspaceConfigFn returns refreshed workspace data after creation.
func handleWorkspaceCreate(createFn WorkspaceCreateFn, workspaceConfigFn func() (*WorkspaceData, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if createFn == nil {
			respondJSON(w, http.StatusNotImplemented, workspaceResponse{
				Success: false,
				Error:   "workspace creation is not available",
			})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

		var req WorkspaceCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				respondJSON(w, http.StatusRequestEntityTooLarge, workspaceResponse{
					Success: false,
					Error:   "request body too large",
				})
				return
			}
			respondJSON(w, http.StatusBadRequest, workspaceResponse{
				Success: false,
				Error:   "invalid request body",
			})
			return
		}

		if status, msg := validateWorkspaceCreateRequest(&req); status != 0 {
			respondJSON(w, status, workspaceResponse{Success: false, Error: msg})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), workspaceCreateTimeout)
		defer cancel()

		if err := createFn(ctx, req); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				respondJSON(w, http.StatusGatewayTimeout, workspaceResponse{
					Success: false,
					Error:   "workspace creation timed out",
				})
				return
			}
			slog.Warn("workspace creation failed", "name", req.Name, "type", req.Type, "err", err)
			respondJSON(w, http.StatusInternalServerError, workspaceResponse{
				Success: false,
				Error:   "failed to create workspace",
			})
			return
		}

		// Return refreshed workspace data
		if workspaceConfigFn != nil {
			data, err := workspaceConfigFn()
			if err == nil && data != nil {
				normalizeWorkspaceData(data)
			}
			respondJSON(w, http.StatusCreated, workspaceResponse{Success: true, Data: data})
			return
		}

		respondJSON(w, http.StatusCreated, workspaceResponse{Success: true})
	}
}
