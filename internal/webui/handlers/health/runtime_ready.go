package health

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
	Mode      string `json:"mode"` // "daemon" | "fleet"
	Workspace string `json:"workspace"`
	Reason    string `json:"reason,omitempty"` // populated when Ready=false
}

// runtimeReadyClient is the narrow rpc.Client surface exercised by the
// daemon-mode readiness probe. Defined for test substitution; production
// uses *rpc.Client via runtimeReadyPoolAdapter.
type runtimeReadyClient interface {
	Health() (*rpc.HealthResponse, error)
}

// runtimeReadyPool is the pool surface used by the readiness probe. Mirrors
// the StatsConnectionGetter pattern: a tiny interface so unit tests can
// supply a fake pool/client without standing up a real rpc.Client.
type runtimeReadyPool interface {
	Get(ctx context.Context) (runtimeReadyClient, error)
	Put(client runtimeReadyClient)
	Discard(client runtimeReadyClient)
}

type runtimeReadyPoolAdapter struct {
	pool daemon.Pool
}

func (a *runtimeReadyPoolAdapter) Get(ctx context.Context) (runtimeReadyClient, error) {
	return a.pool.Get(ctx)
}

func (a *runtimeReadyPoolAdapter) Put(client runtimeReadyClient) {
	if c, ok := client.(*rpc.Client); ok {
		a.pool.Put(c)
	}
}

func (a *runtimeReadyPoolAdapter) Discard(client runtimeReadyClient) {
	if c, ok := client.(*rpc.Client); ok {
		a.pool.Discard(c)
	}
}

// HandleWorkspaceRuntimeReady reports whether the runtime can actually serve
// agent work for the workspace named by the {ws} path parameter.
//
//   - daemonExpected=true (daemon mode): Pool.Get + client.Health must succeed.
//     ErrWorkspaceNotRegistered and other Get errors → 503 with reason.
//   - daemonExpected=false (fleet client): backendFn must return a non-nil
//     IssueBackend whose Stats(ctx) succeeds. A nil backend or any error → 503
//     with reason.
//
// This is the missing "is this workspace serviceable right now" probe consumed
// by ensure-runtime; /api/health stays as the LB liveness signal.
func HandleWorkspaceRuntimeReady(pool daemon.Pool, daemonExpected bool, backendFn IssueBackendFn) http.HandlerFunc {
	var adapter runtimeReadyPool
	if pool != nil {
		adapter = &runtimeReadyPoolAdapter{pool: pool}
	}
	return handleWorkspaceRuntimeReady(adapter, daemonExpected, backendFn)
}

// handleWorkspaceRuntimeReady is the internal implementation that accepts the
// narrow pool interface used by the tests.
func handleWorkspaceRuntimeReady(pool runtimeReadyPool, daemonExpected bool, backendFn IssueBackendFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws := r.PathValue("ws")
		if ws == "" {
			handler.WriteJSON(w, http.StatusBadRequest, RuntimeReadyResponse{
				Ready:  false,
				Reason: "missing workspace path parameter",
			})
			return
		}

		var resp RuntimeReadyResponse
		if daemonExpected {
			resp = probeDaemon(r.Context(), pool, ws)
		} else {
			resp = probeFleet(r.Context(), backendFn, ws)
		}

		httpStatus := http.StatusOK
		if !resp.Ready {
			httpStatus = http.StatusServiceUnavailable
		}
		handler.WriteJSON(w, httpStatus, resp)
	}
}

// probeDaemon checks daemon-mode workspace readiness: a registered workspace
// pool exists and its Health RPC succeeds.
func probeDaemon(ctx context.Context, pool runtimeReadyPool, ws string) RuntimeReadyResponse {
	resp := RuntimeReadyResponse{Mode: "daemon", Workspace: ws}
	if pool == nil {
		resp.Reason = "connection pool not initialized"
		return resp
	}

	ctx, cancel := context.WithTimeout(middleware.WithWorkspace(ctx, ws), 2*time.Second)
	defer cancel()

	client, err := pool.Get(ctx)
	if err != nil {
		switch {
		case errors.Is(err, daemon.ErrWorkspaceNotRegistered):
			resp.Reason = "workspace not registered: " + ws
		case errors.Is(err, daemon.ErrDaemonStarting):
			resp.Reason = "daemon is starting up"
		default:
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
	rpcOK = true

	if health != nil && health.Status == "unhealthy" {
		if health.Error != "" {
			resp.Reason = health.Error
		} else {
			resp.Reason = "daemon reports unhealthy"
		}
		return resp
	}

	resp.Ready = true
	return resp
}

// probeFleet checks fleet-client-mode workspace readiness: the IssueBackend
// factory returns a non-nil backend and its Stats RPC succeeds.
func probeFleet(ctx context.Context, backendFn IssueBackendFn, ws string) RuntimeReadyResponse {
	resp := RuntimeReadyResponse{Mode: "fleet", Workspace: ws}
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
