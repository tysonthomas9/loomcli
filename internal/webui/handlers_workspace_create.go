package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// WorkspaceCreateRequest is the JSON body for POST /api/workspaces.
type WorkspaceCreateRequest struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`       // "empty", "clone", "template"
	Repos     []string `json:"repos"`      // repo paths (for empty type)
	CloneURL  string   `json:"clone_url"`  // single git URL (backward compat)
	CloneURLs []string `json:"clone_urls"` // multiple git URLs (for clone type)
	Branch    string   `json:"branch"`     // optional branch name
	Path      string   `json:"path"`       // optional workspace directory override
}

// WorkspaceCreateResult carries data produced during workspace creation,
// eliminating the need for a post-creation config re-read.
type WorkspaceCreateResult struct {
	WorkspaceID   string // stable UUID assigned during creation
	WorkspacePath string // absolute path to the workspace directory
}

// WorkspaceCreateFn is the function signature for creating a workspace.
// Injected at server startup to decouple webui from CLI internals.
type WorkspaceCreateFn func(ctx context.Context, req WorkspaceCreateRequest) (WorkspaceCreateResult, error)

var cloneURLPattern = regexp.MustCompile(`^(https://|git@)`)

const (
	workspaceCreateTimeoutEmpty = 10 * time.Second
	workspaceCreateTimeoutClone = 60 * time.Second
)

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
		// Normalize: merge single clone_url into clone_urls
		if req.CloneURL != "" && len(req.CloneURLs) == 0 {
			req.CloneURLs = []string{req.CloneURL}
		}
		if len(req.CloneURLs) == 0 {
			return http.StatusBadRequest, "at least one clone URL is required for clone workspace type"
		}
		for _, u := range req.CloneURLs {
			if !cloneURLPattern.MatchString(u) {
				return http.StatusBadRequest, fmt.Sprintf("clone URL must start with https:// or git@: %s", u)
			}
			if err := validateCloneURL(u); err != nil {
				return http.StatusBadRequest, err.Error()
			}
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

// validateCloneURL rejects clone URLs that could inject git flags or contain
// control characters. The prefix check (https:// or git@) is done separately.
func validateCloneURL(u string) error {
	if strings.ContainsAny(u, "\x00\n\r") {
		return fmt.Errorf("clone URL contains control characters: %s", u)
	}
	// After the scheme, check for path segments starting with "-" which git
	// may interpret as flags (e.g. --upload-pack, --config).
	for _, seg := range strings.Split(u, "/") {
		if strings.HasPrefix(seg, "-") {
			return fmt.Errorf("clone URL contains suspicious path segment %q: %s", seg, u)
		}
	}
	return nil
}

// handleWorkspaceCreate returns a handler that creates a new workspace.
// createFn performs the actual workspace creation (git clone/init, config save).
// workspaceConfigFn returns refreshed workspace data after creation.
// jobStore, when non-nil, enables async creation for clone workspaces (202 + job_id).
func handleWorkspaceCreate(createFn WorkspaceCreateFn, workspaceConfigFn func() (*WorkspaceData, error), jobStore *WorkspaceJobStore) http.HandlerFunc {
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

		// Async path: clone with jobStore returns 202 + job_id immediately.
		if req.Type == "clone" && jobStore != nil {
			jobID := jobStore.Start(req, createFn)
			respondJSON(w, http.StatusAccepted, map[string]any{
				"success": true,
				"job_id":  jobID,
			})
			return
		}

		// Sync path: empty workspaces (or clone when jobStore is nil).
		timeout := workspaceCreateTimeoutClone
		if req.Type == "empty" {
			timeout = workspaceCreateTimeoutEmpty
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		ctx = WithCreateWarnings(ctx)

		result, err := createFn(ctx, req)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				respondJSON(w, http.StatusGatewayTimeout, workspaceResponse{
					Success: false,
					Error:   "workspace creation timed out",
				})
				return
			}
			status, msg := classifyWorkspaceCreateError(err)
			slog.Warn("workspace creation failed", "name", req.Name, "type", req.Type, "err", err)
			respondJSON(w, status, workspaceResponse{
				Success: false,
				Error:   msg,
			})
			return
		}
		_ = result // used by wrapWorkspaceCreateFn for registration

		// Return refreshed workspace data
		var data *WorkspaceData
		if workspaceConfigFn != nil {
			d, err := workspaceConfigFn()
			if err == nil && d != nil {
				normalizeWorkspaceData(d)
				data = d
			}
		}
		resp := workspaceResponse{Success: true, Data: data}
		if warnings := GetCreateWarnings(ctx); len(warnings) > 0 {
			resp.Warnings = warnings
		}
		respondJSON(w, http.StatusCreated, resp)
	}
}

// classifyWorkspaceCreateError maps a workspace creation error to an HTTP
// status code and user-facing message. If the error is a *workspaceerrors.CreateError,
// the code determines the status; otherwise a generic 500 is returned.
func classifyWorkspaceCreateError(err error) (int, string) {
	var ce *workspaceerrors.CreateError
	if errors.As(err, &ce) {
		switch ce.Code {
		case workspaceerrors.AlreadyExists:
			return http.StatusConflict, ce.Message
		case workspaceerrors.PathNotFound, workspaceerrors.NotGitRepo, workspaceerrors.GitFailed:
			return http.StatusUnprocessableEntity, ce.Message
		case workspaceerrors.SecurityViolation:
			return http.StatusForbidden, ce.Message
		case workspaceerrors.ConfigFailed:
			return http.StatusInternalServerError, ce.Message
		}
	}
	return http.StatusInternalServerError, "failed to create workspace"
}
