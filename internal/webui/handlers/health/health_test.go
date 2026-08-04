package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
