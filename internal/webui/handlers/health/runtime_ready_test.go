package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

type workspaceNotRegisteredPool struct{}

func (workspaceNotRegisteredPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, fmt.Errorf("%w: %q", daemon.ErrWorkspaceNotRegistered, "LOOM")
}
func (workspaceNotRegisteredPool) Put(_ *rpc.Client)           {}
func (workspaceNotRegisteredPool) PutAfterError(_ *rpc.Client) {}
func (workspaceNotRegisteredPool) Discard(_ *rpc.Client)       {}
func (workspaceNotRegisteredPool) Stats() daemon.PoolStats     { return daemon.PoolStats{} }
func (workspaceNotRegisteredPool) Close() error                { return nil }

type rpcDaemonPool struct {
	client *rpc.Client
}

func (p *rpcDaemonPool) Get(_ context.Context) (*rpc.Client, error) { return p.client, nil }
func (p *rpcDaemonPool) Put(_ *rpc.Client)                          {}
func (p *rpcDaemonPool) PutAfterError(_ *rpc.Client)                {}
func (p *rpcDaemonPool) Discard(_ *rpc.Client)                      {}
func (p *rpcDaemonPool) Stats() daemon.PoolStats                    { return daemon.PoolStats{} }
func (p *rpcDaemonPool) Close() error                               { return nil }

type runtimeReadyIssueBackend struct {
	backend.IssueBackend
	statsErr error
}

func (b runtimeReadyIssueBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	if b.statsErr != nil {
		return nil, b.statsErr
	}
	return &backend.StatsData{}, nil
}

func TestHandleWorkspaceRuntimeReady_DaemonMode_PoolGetErrors(t *testing.T) {
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(stubDeadDaemonPool{}, true, nil), "LOOM")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if body.Ready || body.Mode != "daemon" || body.Workspace != "LOOM" {
		t.Fatalf("body = %+v", body)
	}
	if body.Reason != "connection refused" {
		t.Fatalf("reason = %q, want connection refused", body.Reason)
	}
}

func TestHandleWorkspaceRuntimeReady_DaemonMode_WorkspaceNotRegistered(t *testing.T) {
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(workspaceNotRegisteredPool{}, true, nil), "LOOM")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if !strings.Contains(body.Reason, "workspace not registered") {
		t.Fatalf("reason = %q, want workspace not registered", body.Reason)
	}
}

func TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCSucceeds(t *testing.T) {
	client := newTestRPCClient(t, func(req rpc.Request) rpc.Response {
		if req.Operation != rpc.OpHealth {
			return rpc.Response{Success: false, Error: "unexpected operation"}
		}
		data, _ := json.Marshal(rpc.HealthResponse{Status: "ok"})
		return rpc.Response{Success: true, Data: data}
	})
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(&rpcDaemonPool{client: client}, true, nil), "LOOM")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := decodeRuntimeReady(t, rec)
	if !body.Ready || body.Mode != "daemon" || body.Workspace != "LOOM" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleWorkspaceRuntimeReady_DaemonMode_HealthRPCFails(t *testing.T) {
	calls := 0
	client := newTestRPCClient(t, func(req rpc.Request) rpc.Response {
		calls++
		if calls == 1 {
			data, _ := json.Marshal(rpc.HealthResponse{Status: "ok"})
			return rpc.Response{Success: true, Data: data}
		}
		return rpc.Response{Success: false, Error: "rpc health failed"}
	})
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(&rpcDaemonPool{client: client}, true, nil), "LOOM")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if !strings.Contains(body.Reason, "rpc health failed") {
		t.Fatalf("reason = %q, want RPC error", body.Reason)
	}
}

func TestHandleWorkspaceRuntimeReady_FleetMode_NilBackend(t *testing.T) {
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(nil, false, nil), "LOOM")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if body.Reason != "issue backend not configured" {
		t.Fatalf("reason = %q", body.Reason)
	}
}

func TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsErrors(t *testing.T) {
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(nil, false, func(context.Context) backend.IssueBackend {
		return runtimeReadyIssueBackend{statsErr: errors.New("stats unavailable")}
	}), "LOOM")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if !strings.Contains(body.Reason, "stats unavailable") {
		t.Fatalf("reason = %q", body.Reason)
	}
}

func TestHandleWorkspaceRuntimeReady_FleetMode_BackendStatsOK(t *testing.T) {
	rec := serveRuntimeReady(HandleWorkspaceRuntimeReady(nil, false, func(context.Context) backend.IssueBackend {
		return runtimeReadyIssueBackend{}
	}), "LOOM")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if !body.Ready || body.Mode != "fleet" || body.Workspace != "LOOM" {
		t.Fatalf("body = %+v", body)
	}
}

func TestHandleWorkspaceRuntimeReady_MissingPathValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/LOOM/runtime-ready", nil)
	rec := httptest.NewRecorder()
	HandleWorkspaceRuntimeReady(nil, false, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeRuntimeReady(t, rec)
	if body.Reason != "missing workspace path parameter" {
		t.Fatalf("reason = %q", body.Reason)
	}
}

func serveRuntimeReady(h http.HandlerFunc, ws string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws+"/runtime-ready", nil)
	req.SetPathValue("ws", ws)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeRuntimeReady(t *testing.T, rec *httptest.ResponseRecorder) RuntimeReadyResponse {
	t.Helper()
	var body RuntimeReadyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body: %s", err, rec.Body.String())
	}
	return body
}

func newTestRPCClient(t *testing.T, respond func(rpc.Request) rpc.Response) *rpc.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "loom-runtime-ready-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			var req rpc.Request
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				return
			}
			resp := respond(req)
			data, _ := json.Marshal(resp)
			_, _ = conn.Write(append(data, '\n'))
		}
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	client, err := rpc.TryConnectWithTimeout(socketPath, time.Second)
	if err != nil {
		t.Fatalf("connect test rpc client: %v", err)
	}
	if client == nil {
		t.Fatal("connect test rpc client: nil client")
	}
	return client
}
