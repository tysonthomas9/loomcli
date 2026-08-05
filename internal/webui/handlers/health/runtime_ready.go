package health

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// RuntimeReadyResponse is the JSON body returned by HandleWorkspaceRuntimeReady.
//
// Distinct from /api/health (LB liveness) and /api/workspaces/{ws}/ready
// (ready-issue listing) — this reports whether the runtime can actually
// serve agent work for the named workspace right now.
type RuntimeReadyResponse struct {
	Ready     bool   `json:"ready"`
	Mode      string `json:"mode"` // "workflow-catalog"
	Workspace string `json:"workspace"`
	Reason    string `json:"reason,omitempty"` // populated when Ready=false
}

// WorkspaceLocalPathFn resolves a workspace's machine-local root path. FleetDB
// is the source of truth for workspace existence, but local terminals and
// local agent runtimes require this per-machine path to exist.
type WorkspaceLocalPathFn func(workspace string) string

// HandleWorkspaceRuntimeReadyWithLocalPath reports whether the owned issue
// backend and the optional machine-local workspace path are available.
func HandleWorkspaceRuntimeReadyWithLocalPath(backendFn IssueBackendFn, localPathFn WorkspaceLocalPathFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := r.PathValue("ws")
		if ws == "" {
			handler.WriteJSON(w, http.StatusBadRequest, RuntimeReadyResponse{Reason: "missing workspace path parameter"})
			return
		}
		resp := RuntimeReadyResponse{Mode: "workflow-catalog", Workspace: ws}
		if reason := localWorkspacePathNotReady(localPathFn, ws); reason != "" {
			resp.Reason = reason
			handler.WriteJSON(w, http.StatusServiceUnavailable, resp)
			return
		}
		resp = probeIssueBackend(r.Context(), backendFn, ws)
		status := http.StatusOK
		if !resp.Ready {
			status = http.StatusServiceUnavailable
		}
		handler.WriteJSON(w, status, resp)
	}
}

func localWorkspacePathNotReady(localPathFn WorkspaceLocalPathFn, ws string) string {
	if localPathFn == nil {
		return ""
	}
	path := strings.TrimSpace(localPathFn(ws))
	if path == "" {
		return "workspace has no local path on this machine"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "workspace local path unavailable: " + err.Error()
	}
	if !info.IsDir() {
		return "workspace local path is not a directory: " + path
	}
	return ""
}

func probeIssueBackend(ctx context.Context, backendFn IssueBackendFn, ws string) RuntimeReadyResponse {
	resp := RuntimeReadyResponse{Mode: "workflow-catalog", Workspace: ws}
	if backendFn == nil {
		resp.Reason = "issue backend not configured"
		return resp
	}
	ctx = middleware.WithWorkspace(ctx, ws)
	be := backendFn(ctx)
	if be == nil {
		resp.Reason = "issue backend not configured"
		return resp
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := be.Stats(ctx); err != nil {
		resp.Reason = err.Error()
		return resp
	}
	resp.Ready = true
	return resp
}
