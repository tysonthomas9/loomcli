package fleet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestFleetWrapperHandlersUnavailable(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		req     *http.Request
		want    int
	}{
		{
			name:    "register nil store",
			handler: handleFleetRegister(nil, &TokenConfig{SigningKey: []byte("secret"), Expiry: time.Hour}, &RegisterConfig{APIKey: "key"}),
			req:     httptest.NewRequest(http.MethodPost, "/api/fleet/register", strings.NewReader(`{"worker_id":"worker-1"}`)),
			want:    http.StatusServiceUnavailable,
		},
		{
			name:    "done nil store",
			handler: handleFleetDone(nil),
			req:     httptest.NewRequest(http.MethodPost, "/api/fleet/workers/worker-1/done", strings.NewReader(`{"success":true}`)),
			want:    http.StatusServiceUnavailable,
		},
		{
			name:    "heartbeat nil store",
			handler: handleFleetHeartbeat(nil),
			req:     httptest.NewRequest(http.MethodPost, "/api/fleet/heartbeat", strings.NewReader(`{"worker_id":"worker-1"}`)),
			want:    http.StatusServiceUnavailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.handler(rec, tc.req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestDefaultTimeoutConfig(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	if cfg.TaskTimeout != 30*time.Minute || cfg.CheckInterval != time.Minute {
		t.Fatalf("DefaultTimeoutConfig = %+v", cfg)
	}
}

func TestFleetClaimPoolAdapterDelegatesRPCClients(t *testing.T) {
	client := &rpc.Client{}
	pool := &recordingDaemonPool{client: client}
	adapter := &fleetClaimPoolAdapter{pool: pool}

	got, err := adapter.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != client || pool.gets != 1 {
		t.Fatalf("Get got=%v gets=%d", got, pool.gets)
	}

	adapter.Put(client)
	adapter.Put(fakeClaimClient{})
	adapter.Discard(client)
	adapter.Discard(fakeClaimClient{})
	if pool.puts != 1 || pool.discards != 1 {
		t.Fatalf("delegation puts=%d discards=%d", pool.puts, pool.discards)
	}

	pool.err = errors.New("pool unavailable")
	if _, err := adapter.Get(context.Background()); !errors.Is(err, pool.err) {
		t.Fatalf("Get err = %v, want %v", err, pool.err)
	}
}

type recordingDaemonPool struct {
	client   *rpc.Client
	err      error
	gets     int
	puts     int
	discards int
}

func (p *recordingDaemonPool) Get(context.Context) (*rpc.Client, error) {
	p.gets++
	return p.client, p.err
}

func (p *recordingDaemonPool) Put(*rpc.Client) {
	p.puts++
}

func (p *recordingDaemonPool) PutAfterError(*rpc.Client) {}

func (p *recordingDaemonPool) Discard(*rpc.Client) {
	p.discards++
}

func (p *recordingDaemonPool) Stats() daemon.PoolStats { return daemon.PoolStats{} }

func (p *recordingDaemonPool) Close() error { return nil }

type fakeClaimClient struct{}

func (fakeClaimClient) Update(*rpc.UpdateArgs) (*rpc.Response, error) { return nil, nil }

func (fakeClaimClient) Ready(*rpc.ReadyArgs) (*rpc.Response, error) { return nil, nil }
