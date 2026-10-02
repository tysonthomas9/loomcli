package serve

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/agentwire"
	"github.com/tysonthomas9/loomcli/internal/webui"
	webuiapp "github.com/tysonthomas9/loomcli/internal/webui/app"
)

// TestAgentBridgeFollowsFallbackPort: when serve's configured port is taken
// and it binds a fallback, the bridges are pointed at the fallback, where
// they reach serve, not at the taken port.
func TestAgentBridgeFollowsFallbackPort(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { taken.Close() })
	port := taken.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := webui.ServerConfig{Port: port, BindAddress: "127.0.0.1", PoolSize: 1, ShutdownTimeout: time.Second,
		MaxPortAttempts: 20}
	api, err := agentwire.Start(ctx, agentwire.Config{Dir: t.TempDir(), OpenCodeBin: "/nonexistent/opencode",
		LoomBin: "/nonexistent/loom", APIBase: agentAPIBase(cfg.BindAddress, cfg.Port)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Stop)
	wireAgentAPI(&cfg, api)
	done := make(chan error, 1)
	go func() { done <- webuiapp.StartServer(ctx, cfg) }()

	configured := "http://127.0.0.1:" + strconv.Itoa(port)
	for deadline := time.Now().Add(10 * time.Second); api.APIBase() == configured; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the bridges still call back to the taken port %d", port)
		}
	}
	base := api.APIBase()
	var resp *http.Response
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if resp, err = http.Get(base + "/health"); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("serve at the bridges' address %s: %v", base, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serve at the bridges' address %s answered %d", base, resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
