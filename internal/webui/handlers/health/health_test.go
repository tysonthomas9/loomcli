package health

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// stubDeadDaemonPool models the production "daemon expected, daemon dead"
// scenario: pool exists, pool.Get() always errors. The handler must surface
// 503 from /api/health so liveness probes restart the pod.
type stubDeadDaemonPool struct{}

func (stubDeadDaemonPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, errors.New("connection refused")
}
func (stubDeadDaemonPool) Put(_ *rpc.Client)           {}
func (stubDeadDaemonPool) PutAfterError(_ *rpc.Client) {}
func (stubDeadDaemonPool) Discard(_ *rpc.Client)       {}
func (stubDeadDaemonPool) Stats() daemon.PoolStats     { return daemon.PoolStats{Size: 100} }
func (stubDeadDaemonPool) Close() error                { return nil }

type stubStartingDaemonPool struct{}

func (stubStartingDaemonPool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, daemon.ErrDaemonStarting
}
func (stubStartingDaemonPool) Put(_ *rpc.Client)           {}
func (stubStartingDaemonPool) PutAfterError(_ *rpc.Client) {}
func (stubStartingDaemonPool) Discard(_ *rpc.Client)       {}
func (stubStartingDaemonPool) Stats() daemon.PoolStats     { return daemon.PoolStats{Size: 1} }
func (stubStartingDaemonPool) Close() error                { return nil }

// TestHandleAPIHealthNoDaemon verifies the fleet-mode health handler
// returns 200 with status="ok" and daemon.connected=false even with no
// pool wired. Liveness probes must NOT see a 503 here — the server is
// fully functional in fleet mode.
func TestHandleAPIHealthNoDaemon(t *testing.T) {
	handler := HandleAPIHealthNoDaemon()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("Status = %q, want ok", body.Status)
	}
	if body.Daemon.Connected {
		t.Errorf("Daemon.Connected = true, want false")
	}
	// Pool stats must NOT be reported in NoDaemon mode; the absence is
	// the signal that this deployment doesn't use a daemon at all.
	if body.Pool != nil {
		t.Errorf("Pool stats present in NoDaemon mode: %+v", body.Pool)
	}
}

// TestHandleAPIHealth_NilPoolShortCircuit verifies the misconfiguration
// case: HandleAPIHealth(nil) returns 200 (the daemon-mode endpoints were
// already inoperable; failing 503 here would just add noise). The real
// daemon-failure scenario is TestHandleAPIHealth_DaemonDead below.
func TestHandleAPIHealth_NilPoolShortCircuit(t *testing.T) {
	handler := HandleAPIHealth(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestHandleDaemonStatus_NoDaemonMode verifies the workspace-scoped
// daemon-status handler returns the fleet stub when daemonExpected=false,
// instead of the historical 503 ("workspace not registered" / "connection
// pool not initialized") that fleet mode used to throw.
func TestHandleDaemonStatus_NoDaemonMode(t *testing.T) {
	handler := HandleDaemonStatusWithMode(nil, false)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/daemon/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body DaemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Success {
		t.Errorf("Success = false, want true (fleet stub)")
	}
	if body.Data == nil {
		t.Fatalf("Data = nil, want stub StatusResponse")
	}
	if body.Data.DaemonMode != rpc.DaemonModeFleet {
		t.Errorf("DaemonMode = %q, want %q", body.Data.DaemonMode, rpc.DaemonModeFleet)
	}
}

// TestHandleDaemonStatus_DaemonExpectedNilPool keeps the daemon-mode
// contract intact: when daemonExpected=true and no pool is wired, the
// handler returns 503 so operators can detect daemon-mode misconfiguration.
func TestHandleDaemonStatus_DaemonExpectedNilPool(t *testing.T) {
	handler := HandleDaemonStatusWithMode(nil, true)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/daemon/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// TestHandleAPIHealth_DaemonDead is the prod-regression guard for the
// daemon-mode 503 contract. A wired pool whose Get() errors MUST 503 so
// k8s/load-balancer liveness probes restart the pod —
// silently masking this would ship a degraded production.
func TestHandleAPIHealth_DaemonDead(t *testing.T) {
	handler := HandleAPIHealth(stubDeadDaemonPool{})
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (daemon dead → degraded)", rec.Code)
	}
	var body HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "degraded" {
		t.Errorf("Status = %q, want degraded", body.Status)
	}
	if body.Daemon.Connected {
		t.Errorf("Daemon.Connected = true, want false")
	}
	if body.Daemon.Error == "" {
		t.Errorf("Daemon.Error empty, want pool-failure detail")
	}
}

func TestHandleHealthAndStartingDaemon(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleHealth(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("HandleHealth status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandleAPIHealth(stubStartingDaemonPool{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("starting daemon status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "starting" || body.Daemon.Error != "daemon is starting up" || body.Pool == nil {
		t.Fatalf("starting body = %#v", body)
	}
}

func TestHandleStatsWithBackend(t *testing.T) {
	for _, tt := range []struct {
		name      string
		backendFn IssueBackendFn
		wantCode  int
		wantOK    bool
	}{
		{
			name:      "nil backend",
			backendFn: func(context.Context) backend.IssueBackend { return nil },
			wantCode:  http.StatusServiceUnavailable,
		},
		{
			name:      "backend error",
			backendFn: func(context.Context) backend.IssueBackend { return fakeStatsBackend{err: errors.New("stats failed")} },
			wantCode:  http.StatusInternalServerError,
		},
		{
			name: "success",
			backendFn: func(context.Context) backend.IssueBackend {
				return fakeStatsBackend{stats: &backend.StatsData{TotalIssues: 5, OpenIssues: 3, ReadyIssues: 2}}
			},
			wantCode: http.StatusOK,
			wantOK:   true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			HandleStatsWithBackend(nil, tt.backendFn).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			var body StatsResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Success != tt.wantOK {
				t.Fatalf("success=%v body=%#v", body.Success, body)
			}
			if tt.wantOK && (body.Data == nil || body.Data.TotalIssues != 5 || body.Data.ReadyIssues != 2) {
				t.Fatalf("stats body = %#v", body)
			}
		})
	}
}

func TestHandleStatsAndDaemonStatusWrappers(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleStats(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HandleStats(nil) status=%d body=%s, want 503", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	HandleDaemonStatus(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/daemon/status", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("HandleDaemonStatus(nil) status=%d body=%s, want 503", rec.Code, rec.Body.String())
	}
}

func TestHandleStatsWithPool(t *testing.T) {
	statsData, err := json.Marshal(types.Statistics{TotalIssues: 4, OpenIssues: 2})
	if err != nil {
		t.Fatalf("marshal stats: %v", err)
	}
	for _, tt := range []struct {
		name     string
		pool     *fakeStatsPool
		wantCode int
	}{
		{name: "nil pool", wantCode: http.StatusServiceUnavailable},
		{name: "deadline", pool: &fakeStatsPool{getErr: context.DeadlineExceeded}, wantCode: http.StatusGatewayTimeout},
		{name: "get error", pool: &fakeStatsPool{getErr: errors.New("offline")}, wantCode: http.StatusServiceUnavailable},
		{name: "rpc error", pool: &fakeStatsPool{client: fakeStatsClient{err: errors.New("rpc down")}}, wantCode: http.StatusInternalServerError},
		{name: "rpc unsuccessful", pool: &fakeStatsPool{client: fakeStatsClient{resp: &rpc.Response{Success: false, Error: "bad"}}}, wantCode: http.StatusInternalServerError},
		{name: "bad json", pool: &fakeStatsPool{client: fakeStatsClient{resp: &rpc.Response{Success: true, Data: []byte("{")}}}, wantCode: http.StatusInternalServerError},
		{name: "success", pool: &fakeStatsPool{client: fakeStatsClient{resp: &rpc.Response{Success: true, Data: statsData}}}, wantCode: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var pool StatsConnectionGetter
			if tt.pool != nil {
				pool = tt.pool
			}
			rec := httptest.NewRecorder()
			HandleStatsWithPool(pool).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantCode == http.StatusOK && tt.pool.puts != 1 {
				t.Fatalf("puts=%d want 1", tt.pool.puts)
			}
			if tt.pool != nil && tt.pool.client.err != nil && tt.pool.discards != 1 {
				t.Fatalf("discards=%d want 1", tt.pool.discards)
			}
		})
	}
}

func TestHandleMetrics(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleMetrics(nil, nil, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil metrics status=%d", rec.Code)
	}

	claims := fleet.NewClaimMetrics()
	claims.RecordClaim(fleet.ClaimResultSuccess)
	claims.RecordClaim(fleet.ClaimResultCollision)
	hub := realtime.NewHub()
	rec = httptest.NewRecorder()
	HandleMetrics(hub, func() int64 { return 7 }, claims).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body MetricsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if !body.Success || body.Data == nil || body.Data.FleetTimeoutsTotal != 7 || body.Data.FleetClaimsTotal != 2 {
		t.Fatalf("metrics body = %#v", body)
	}
}

// TestHandleDaemonStatus_DaemonDead mirrors the /api/health regression
// guard for the workspace-scoped daemon-status route. With a wired pool
// whose Get() always errors and daemonExpected=true, the
// response MUST be 503 so the FE badge correctly renders "daemon down,
// please restart" rather than the fleet-stub "no daemon expected" state.
func TestHandleDaemonStatus_DaemonDead(t *testing.T) {
	handler := HandleDaemonStatusWithMode(stubDeadDaemonPool{}, true)
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/x/daemon/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body DaemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Success {
		t.Errorf("Success = true, want false (daemon dead)")
	}
}

func TestHealthStatsAdapterAndDaemonStatusSuccess(t *testing.T) {
	client := newHealthRPCClient(t)
	pool := &liveRPCPool{client: client}

	rec := httptest.NewRecorder()
	HandleAPIHealth(pool).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("api health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var healthBody HealthStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &healthBody); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !healthBody.Daemon.Connected || healthBody.Daemon.Version != "test-rpc" || pool.puts != 1 {
		t.Fatalf("health body=%#v puts=%d discards=%d", healthBody, pool.puts, pool.discards)
	}

	rec = httptest.NewRecorder()
	HandleDaemonStatusWithMode(pool, true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/ws/daemon/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("daemon status=%d body=%s", rec.Code, rec.Body.String())
	}
	var statusBody DaemonStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !statusBody.Success || statusBody.Data == nil || statusBody.Data.DaemonMode != rpc.DaemonModeEvents || pool.puts != 2 {
		t.Fatalf("status body=%#v puts=%d", statusBody, pool.puts)
	}

	adapter := &statsPoolAdapter{pool: pool}
	got, err := adapter.Get(context.Background())
	if err != nil || got == nil {
		t.Fatalf("adapter Get got=%#v err=%v", got, err)
	}
	adapter.Put(got)
	adapter.Discard(got)
	if pool.puts != 3 || pool.discards != 1 {
		t.Fatalf("adapter put/discard counts puts=%d discards=%d", pool.puts, pool.discards)
	}
}

type fakeStatsClient struct {
	resp *rpc.Response
	err  error
}

func (f fakeStatsClient) Stats() (*rpc.Response, error) {
	return f.resp, f.err
}

type fakeStatsPool struct {
	client   fakeStatsClient
	getErr   error
	puts     int
	discards int
}

func (f *fakeStatsPool) Get(context.Context) (StatsClient, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.client, nil
}

func (f *fakeStatsPool) Put(StatsClient) {
	f.puts++
}

func (f *fakeStatsPool) Discard(StatsClient) {
	f.discards++
}

type fakeStatsBackend struct {
	stats *backend.StatsData
	err   error
}

func (f fakeStatsBackend) Stats(context.Context) (*backend.StatsData, error) {
	return f.stats, f.err
}

func (f fakeStatsBackend) Get(context.Context, string) (*backend.IssueDetailData, error) {
	return nil, nil
}
func (f fakeStatsBackend) List(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Ready(context.Context, backend.ReadyOpts) ([]backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Blocked(context.Context, backend.BlockedOpts) ([]backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Count(context.Context, backend.CountOpts) (int, error) { return 0, nil }
func (f fakeStatsBackend) GetChildren(context.Context, string) ([]backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) SearchIssues(context.Context, string, int) ([]backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Create(context.Context, backend.CreateParams) (*backend.IssueData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Update(context.Context, string, backend.UpdateParams) error { return nil }
func (f fakeStatsBackend) ClaimIssue(context.Context, string, time.Duration) error    { return nil }
func (f fakeStatsBackend) DeferIssue(context.Context, string, time.Time) error        { return nil }
func (f fakeStatsBackend) UndeferIssue(context.Context, string) error                 { return nil }
func (f fakeStatsBackend) Close(context.Context, string, backend.CloseParams) (*backend.CloseResult, error) {
	return nil, nil
}
func (f fakeStatsBackend) Reopen(context.Context, string, backend.ReopenParams) error { return nil }
func (f fakeStatsBackend) Delete(context.Context, backend.DeleteParams) error         { return nil }
func (f fakeStatsBackend) AddDependency(context.Context, backend.DepAddParams) error  { return nil }
func (f fakeStatsBackend) RemoveDependency(context.Context, backend.DepRemoveParams) error {
	return nil
}
func (f fakeStatsBackend) AddLabel(context.Context, string, string) error    { return nil }
func (f fakeStatsBackend) RemoveLabel(context.Context, string, string) error { return nil }
func (f fakeStatsBackend) ListComments(context.Context, string) ([]backend.CommentData, error) {
	return nil, nil
}
func (f fakeStatsBackend) AddComment(context.Context, backend.CommentAddParams) (*backend.CommentData, error) {
	return nil, nil
}
func (f fakeStatsBackend) ListEvents(context.Context, string, int) ([]backend.EventData, error) {
	return nil, nil
}
func (f fakeStatsBackend) Batch(context.Context, []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, nil
}
func (f fakeStatsBackend) GetMutations(context.Context, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (f fakeStatsBackend) WaitForMutations(context.Context, int64, int64) ([]backend.MutationData, error) {
	return nil, nil
}
func (f fakeStatsBackend) BackendName() string { return "fake" }

type liveRPCPool struct {
	client   *rpc.Client
	puts     int
	discards int
}

func (p *liveRPCPool) Get(context.Context) (*rpc.Client, error) { return p.client, nil }
func (p *liveRPCPool) Put(*rpc.Client)                          { p.puts++ }
func (p *liveRPCPool) PutAfterError(*rpc.Client)                { p.puts++ }
func (p *liveRPCPool) Discard(*rpc.Client)                      { p.discards++ }
func (p *liveRPCPool) Stats() daemon.PoolStats                  { return daemon.PoolStats{Size: 1, Active: 1} }
func (p *liveRPCPool) Close() error                             { return nil }

func newHealthRPCClient(t *testing.T) *rpc.Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loom-health-rpc-*")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("unix sockets blocked by sandbox: %v", err)
		}
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveHealthRPCConn(conn)
		}
	}()

	client, err := rpc.TryConnectWithTimeout(socketPath, time.Second)
	if err != nil {
		t.Fatalf("TryConnectWithTimeout: %v", err)
	}
	if client == nil {
		t.Fatal("TryConnectWithTimeout returned nil client")
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func serveHealthRPCConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var req rpc.Request
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		var data []byte
		switch req.Operation {
		case rpc.OpHealth:
			data, _ = json.Marshal(rpc.HealthResponse{Status: "healthy", Version: "test-rpc", Compatible: true, Uptime: 12.5})
		case rpc.OpStatus:
			data, _ = json.Marshal(rpc.StatusResponse{Version: "test-rpc", DaemonMode: rpc.DaemonModeEvents})
		default:
			data, _ = json.Marshal(map[string]string{"ok": "true"})
		}
		resp, _ := json.Marshal(rpc.Response{Success: true, Data: data})
		resp = append(resp, '\n')
		_, _ = conn.Write(resp)
	}
}
