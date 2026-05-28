package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// stubNotRegisteredPool is the daemon-mode pool variant where Get returns the
// sentinel ErrWorkspaceNotRegistered wrapped via fmt.Errorf — this is the
// real path callers hit when a workspace ID is not in the MultiPool.
type stubNotRegisteredPool struct{}

func (stubNotRegisteredPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, fmt.Errorf("%w: %q", daemon.ErrWorkspaceNotRegistered, "LOOM")
}
func (stubNotRegisteredPool) Put(_ *rpc.Client)           {}
func (stubNotRegisteredPool) PutAfterError(_ *rpc.Client) {}
func (stubNotRegisteredPool) Discard(_ *rpc.Client)       {}
func (stubNotRegisteredPool) Stats() daemon.PoolStats     { return daemon.PoolStats{Size: 1} }
func (stubNotRegisteredPool) Close() error                { return nil }

// stubStartingDaemonPool models the transient state where the daemon lock is
// held but the socket hasn't been bound yet. Get must return the sentinel
// ErrDaemonStarting so the readiness probe can surface a calm "starting" reason
// instead of a generic error string.
type stubStartingDaemonPool struct{}

func (stubStartingDaemonPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, daemon.ErrDaemonStarting
}
func (stubStartingDaemonPool) Put(_ *rpc.Client)           {}
func (stubStartingDaemonPool) PutAfterError(_ *rpc.Client) {}
func (stubStartingDaemonPool) Discard(_ *rpc.Client)       {}
func (stubStartingDaemonPool) Stats() daemon.PoolStats     { return daemon.PoolStats{Size: 1} }
func (stubStartingDaemonPool) Close() error                { return nil }

// stubRuntimeReadyClient implements the narrow runtimeReadyClient interface so
// tests can exercise the daemon-mode Health() success / failure branches
// without standing up a real rpc.Client.
type stubRuntimeReadyClient struct {
	resp *rpc.HealthResponse
	err  error
}

func (s *stubRuntimeReadyClient) Health() (*rpc.HealthResponse, error) {
	return s.resp, s.err
}

// stubRuntimeReadyPool is the test-side runtimeReadyPool whose Get returns a
// stub client. Put/Discard are no-ops; the handler simply releases the client
// it received.
type stubRuntimeReadyPool struct {
	client runtimeReadyClient
	err    error
}

func (s *stubRuntimeReadyPool) Get(_ context.Context) (runtimeReadyClient, error) {
	return s.client, s.err
}
func (s *stubRuntimeReadyPool) Put(_ runtimeReadyClient)     {}
func (s *stubRuntimeReadyPool) Discard(_ runtimeReadyClient) {}

// stubIssueBackend embeds backend.IssueBackend so we only have to override
// Stats — the only method the readiness probe calls in fleet mode.
type stubIssueBackend struct {
	backend.IssueBackend
	stats *backend.StatsData
	err   error
}

func (s *stubIssueBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	return s.stats, s.err
}

func decodeRuntimeReady(t *testing.T, body []byte) RuntimeReadyResponse {
	t.Helper()
	var resp RuntimeReadyResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode RuntimeReadyResponse: %v (body=%q)", err, string(body))
	}
	return resp
}

func newRuntimeReadyRequest(ws string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws+"/readyz", nil)
	if ws != "" {
		req.SetPathValue("ws", ws)
	}
	return req
}

// TestHandleWorkspaceRuntimeReady_DaemonMode_PoolGetErrors covers the
// production "daemon expected but dead" path: any pool.Get error must surface
// as 503 with the verbatim error message so operators can see what broke.
func TestHandleWorkspaceRuntimeReady_DaemonMode_PoolGetErrors(t *testing.T) {
	h := HandleWorkspaceRuntimeReady(stubDeadDaemonPool{}, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if body.Ready {
		t.Errorf("Ready = true, want false")
	}
	if body.Mode != "daemon" {
		t.Errorf("Mode = %q, want %q", body.Mode, "daemon")
	}
	if body.Workspace != "LOOM" {
		t.Errorf("Workspace = %q, want %q", body.Workspace, "LOOM")
	}
	if body.Reason != "connection refused" {
		t.Errorf("Reason = %q, want %q", body.Reason, "connection refused")
	}
}

// TestHandleWorkspaceRuntimeReady_DaemonMode_WorkspaceNotRegistered covers
// the targeted error path for unknown workspace IDs: the handler must map
// the sentinel error to a human-readable Reason.
func TestHandleWorkspaceRuntimeReady_DaemonMode_WorkspaceNotRegistered(t *testing.T) {
	h := HandleWorkspaceRuntimeReady(stubNotRegisteredPool{}, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if !strings.Contains(body.Reason, "workspace not registered") {
		t.Errorf("Reason = %q, want substring %q", body.Reason, "workspace not registered")
	}
}

// TestHandleWorkspaceRuntimeReady_DaemonMode_DaemonStarting verifies the
// transient-startup mapping: ErrDaemonStarting must produce 503 with a
// "starting" reason rather than echoing the sentinel error verbatim, so the
// caller's polling loop can distinguish "wait a beat" from "actually broken".
func TestHandleWorkspaceRuntimeReady_DaemonMode_DaemonStarting(t *testing.T) {
	h := HandleWorkspaceRuntimeReady(stubStartingDaemonPool{}, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if !strings.Contains(body.Reason, "starting") {
		t.Errorf("Reason = %q, want substring %q", body.Reason, "starting")
	}
}

// TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCSucceeds is the
// happy-path probe: pool returns a client, client.Health returns ok →
// the handler must report Ready=true with 200.
func TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCSucceeds(t *testing.T) {
	pool := &stubRuntimeReadyPool{
		client: &stubRuntimeReadyClient{resp: &rpc.HealthResponse{Status: "ok"}},
	}
	h := handleWorkspaceRuntimeReady(pool, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if !body.Ready {
		t.Errorf("Ready = false, want true")
	}
	if body.Mode != "daemon" {
		t.Errorf("Mode = %q, want daemon", body.Mode)
	}
}

// TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCFails verifies the
// post-Get failure path: pool returns a client, but client.Health errors.
// The Reason must surface the RPC error message.
func TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCFails(t *testing.T) {
	pool := &stubRuntimeReadyPool{
		client: &stubRuntimeReadyClient{err: errors.New("rpc broken")},
	}
	h := handleWorkspaceRuntimeReady(pool, true, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if body.Reason != "rpc broken" {
		t.Errorf("Reason = %q, want %q", body.Reason, "rpc broken")
	}
}

// TestHandleWorkspaceRuntimeReady_FleetMode_NilBackend covers the
// misconfiguration path: fleet mode with no IssueBackend factory wired must
// 503 with a clear "issue backend not configured" reason so operators don't
// silently see Ready=true with no underlying backend.
func TestHandleWorkspaceRuntimeReady_FleetMode_NilBackend(t *testing.T) {
	h := handleWorkspaceRuntimeReady(nil, false, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if body.Reason != "issue backend not configured" {
		t.Errorf("Reason = %q, want %q", body.Reason, "issue backend not configured")
	}
	if body.Mode != "fleet" {
		t.Errorf("Mode = %q, want fleet", body.Mode)
	}
}

// TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsErrors covers the
// "backend wired but unreachable" path: the Stats RPC error message must be
// surfaced verbatim in Reason so operators can diagnose backend outages.
func TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsErrors(t *testing.T) {
	backendFn := func(_ context.Context) backend.IssueBackend {
		return &stubIssueBackend{err: errors.New("backend down")}
	}
	h := handleWorkspaceRuntimeReady(nil, false, backendFn)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if body.Reason != "backend down" {
		t.Errorf("Reason = %q, want %q", body.Reason, "backend down")
	}
}

// TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsOK is the fleet-mode
// happy path: backend.Stats succeeds → Ready=true with 200.
func TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsOK(t *testing.T) {
	backendFn := func(_ context.Context) backend.IssueBackend {
		return &stubIssueBackend{stats: &backend.StatsData{}}
	}
	h := handleWorkspaceRuntimeReady(nil, false, backendFn)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRuntimeReadyRequest("LOOM"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if !body.Ready {
		t.Errorf("Ready = false, want true")
	}
	if body.Mode != "fleet" {
		t.Errorf("Mode = %q, want fleet", body.Mode)
	}
}

// TestHandleWorkspaceRuntimeReady_MissingPathValue ensures the handler
// surfaces a 400 (not 503) when the {ws} path parameter is absent — a
// programming error, not a runtime outage.
func TestHandleWorkspaceRuntimeReady_MissingPathValue(t *testing.T) {
	h := handleWorkspaceRuntimeReady(nil, true, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces//readyz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeRuntimeReady(t, rec.Body.Bytes())
	if body.Reason != "missing workspace path parameter" {
		t.Errorf("Reason = %q, want %q", body.Reason, "missing workspace path parameter")
	}
}
