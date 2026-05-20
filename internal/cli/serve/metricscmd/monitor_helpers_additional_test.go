package metricscmd

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

func TestMonitorSessionComparisonAndRepoSelectionBranches(t *testing.T) {
	if monitorSessionMoreRecent(nil, &domain.AgentSession{}) {
		t.Fatal("nil candidate should not be more recent")
	}
	if !monitorSessionMoreRecent(&domain.AgentSession{SessionID: "a"}, nil) {
		t.Fatal("candidate should be more recent than nil current")
	}
	if !monitorSessionMoreRecent(
		&domain.AgentSession{SessionID: "active", Status: domain.AgentSessionRunning},
		&domain.AgentSession{SessionID: "done", Status: domain.AgentSessionCompleted},
	) {
		t.Fatal("active session should sort ahead of terminal session")
	}
	oldTime := time.Unix(10, 0)
	newTime := time.Unix(20, 0)
	if !monitorSessionMoreRecent(
		&domain.AgentSession{SessionID: "new", Status: domain.AgentSessionCompleted, CreatedAt: newTime},
		&domain.AgentSession{SessionID: "old", Status: domain.AgentSessionCompleted, CreatedAt: oldTime},
	) {
		t.Fatal("newer CreatedAt should sort ahead when UpdatedAt is zero")
	}
	if !monitorSessionMoreRecent(
		&domain.AgentSession{SessionID: "z", Status: domain.AgentSessionCompleted, UpdatedAt: newTime},
		&domain.AgentSession{SessionID: "a", Status: domain.AgentSessionCompleted, UpdatedAt: newTime},
	) {
		t.Fatal("session ID should break timestamp ties")
	}

	repos := []ops.WorkspaceRepo{
		{Name: "api", Groups: []string{"backend"}},
		{Name: "ui", Groups: []string{"frontend"}},
	}
	if _, ok := selectMonitorAgentRepo(nil, ops.WorkspaceAgentInfo{}); ok {
		t.Fatal("empty repo list should not resolve")
	}
	if got, ok := selectMonitorAgentRepo(repos, ops.WorkspaceAgentInfo{}); !ok || got.Name != "api" {
		t.Fatalf("default repo selection = %+v %v, want api", got, ok)
	}
	if got, ok := selectMonitorAgentRepo(repos, ops.WorkspaceAgentInfo{RepoGroups: []string{"frontend"}}); !ok || got.Name != "ui" {
		t.Fatalf("group repo selection = %+v %v, want ui", got, ok)
	}
	if _, ok := selectMonitorAgentRepo(repos, ops.WorkspaceAgentInfo{Repos: []string{"missing"}}); ok {
		t.Fatal("unmatched explicit repo should not resolve")
	}
}

func TestMonitorConstructorAndErrorBranches(t *testing.T) {
	if collector := NewCollector(time.Hour); collector == nil {
		t.Fatal("NewCollector returned nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if collector := NewCollectorWithBackgroundFunc(ctx, time.Hour, time.Hour, nil); collector == nil {
		t.Fatal("NewCollectorWithBackgroundFunc nil fallback returned nil")
	}

	rr := httptest.NewRecorder()
	writeJSON(rr, func() {})
	if rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", rr.Header().Get("Content-Type"))
	}

	if got := monitorRepoFromAgent(nil); got != "" {
		t.Fatalf("monitorRepoFromAgent(nil) = %q", got)
	}
	if got := mergeStoreAgentsWithRuntime(nil, []monitor.AgentStatus{{Name: "runtime"}}, nil); len(got) != 0 {
		t.Fatalf("nil store agents merge = %+v", got)
	}
}

func TestCollectWorkerStatusCountsRPCBranches(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	startMetricsRPCServer(t, runtimeDir, func(req rpc.Request) rpc.Response {
		if req.Operation != rpc.OpGetWorkerStatus {
			return rpc.Response{Success: false, Error: "unexpected operation"}
		}
		data, err := json.Marshal(rpc.GetWorkerStatusResponse{
			Workers: []rpc.WorkerStatus{
				{Status: "in_progress", Assignee: "worker-a"},
				{Status: "active", Assignee: "worker-b"},
				{Status: "idle", Assignee: "worker-c"},
				{Status: "", Assignee: "worker-d"},
				{Status: "blocked", Assignee: "worker-e"},
				{Status: "mystery", Assignee: "worker-f"},
			},
		})
		if err != nil {
			t.Fatalf("marshal worker status: %v", err)
		}
		return rpc.Response{Success: true, Data: data}
	})

	counts := collectWorkerStatusCounts()
	if counts["active"] != 2 || counts["idle"] != 2 || counts["blocked"] != 1 {
		t.Fatalf("worker counts = %+v, want active=2 idle=2 blocked=1", counts)
	}
}

func TestCollectWorkerStatusCountsRPCFailureReturnsZeros(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)

	startMetricsRPCServer(t, runtimeDir, func(req rpc.Request) rpc.Response {
		if req.Operation == rpc.OpGetWorkerStatus {
			return rpc.Response{Success: false, Error: "boom"}
		}
		return rpc.Response{Success: false, Error: "unexpected operation"}
	})

	counts := collectWorkerStatusCounts()
	if counts["active"] != 0 || counts["idle"] != 0 || counts["blocked"] != 0 {
		t.Fatalf("worker counts after RPC error = %+v, want all zero", counts)
	}
}

func startMetricsRPCServer(t *testing.T, runtimeDir string, handle func(rpc.Request) rpc.Response) {
	t.Helper()

	absRuntimeDir, err := filepath.Abs(runtimeDir)
	if err != nil {
		t.Fatalf("abs runtime dir: %v", err)
	}
	socketPath := rpc.ShortSocketPath(absRuntimeDir)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				reader := bufio.NewReader(c)
				for {
					line, err := reader.ReadBytes('\n')
					if err != nil {
						return
					}
					var req rpc.Request
					if err := json.Unmarshal(line, &req); err != nil {
						return
					}

					resp := handle(req)
					if req.Operation == rpc.OpHealth {
						data, _ := json.Marshal(rpc.HealthResponse{
							Status:     "healthy",
							Version:    "test",
							Compatible: true,
							Uptime:     1,
						})
						resp = rpc.Response{Success: true, Data: data}
					}
					respJSON, _ := json.Marshal(resp)
					respJSON = append(respJSON, '\n')
					_, _ = c.Write(respJSON)
				}
			}(conn)
		}
	}()
}
