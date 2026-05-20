package health

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// RuntimeReadyResponse is the JSON body returned by
// HandleWorkspaceRuntimeReady.
type RuntimeReadyResponse struct {
	Ready     bool   `json:"ready"`
	Mode      string `json:"mode"`
	Workspace string `json:"workspace"`
	Reason    string `json:"reason,omitempty"`
}

// HandleWorkspaceRuntimeReady reports whether the runtime can serve agent work
// for the workspace named by the {ws} path parameter.
func HandleWorkspaceRuntimeReady(pool daemon.Pool, daemonExpected bool, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := r.PathValue("ws")
		if ws == "" {
			handler.WriteJSON(w, http.StatusBadRequest, RuntimeReadyResponse{
				Ready:  false,
				Reason: "missing workspace path parameter",
			})
			return
		}

		resp := probeFleet(r.Context(), backendFn, ws)
		if daemonExpected {
			resp = probeDaemon(r.Context(), pool, ws)
		}

		status := http.StatusOK
		if !resp.Ready {
			status = http.StatusServiceUnavailable
		}
		handler.WriteJSON(w, status, resp)
	}
}

func probeDaemon(ctx context.Context, pool daemon.Pool, ws string) RuntimeReadyResponse {
	resp := RuntimeReadyResponse{Mode: "daemon", Workspace: ws}
	if pool == nil {
		resp.Reason = "connection pool not initialized"
		return resp
	}

	ctx, cancel := context.WithTimeout(middleware.WithWorkspace(ctx, ws), 2*time.Second)
	defer cancel()

	client, err := pool.Get(ctx)
	if err != nil {
		if errors.Is(err, daemon.ErrWorkspaceNotRegistered) {
			resp.Reason = "workspace not registered"
		} else {
			resp.Reason = err.Error()
		}
		return resp
	}

	rpcOK := false
	defer func() {
		if rpcOK {
			pool.Put(client)
		} else {
			pool.Discard(client)
		}
	}()

	health, err := client.Health()
	if err != nil {
		resp.Reason = err.Error()
		return resp
	}
	if health.Status == "unhealthy" {
		resp.Reason = health.Error
		if resp.Reason == "" {
			resp.Reason = "daemon unhealthy"
		}
		return resp
	}

	rpcOK = true
	resp.Ready = true
	return resp
}

func probeFleet(ctx context.Context, backendFn IssueBackendFn, ws string) RuntimeReadyResponse {
	resp := RuntimeReadyResponse{Mode: "fleet", Workspace: ws}
	if backendFn == nil {
		resp.Reason = "issue backend not configured"
		return resp
	}

	ctx, cancel := context.WithTimeout(middleware.WithWorkspace(ctx, ws), 2*time.Second)
	defer cancel()

	be := backendFn(ctx)
	if be == nil {
		resp.Reason = "issue backend not configured"
		return resp
	}
	if _, err := be.Stats(ctx); err != nil {
		resp.Reason = err.Error()
		return resp
	}

	resp.Ready = true
	return resp
}
