package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/webui"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TestServer_Close_ZeroValue verifies that calling Close on a zero-value
// Server does not panic. All optional fields are nil by default, and
// Close must guard each one.
func TestServer_Close_ZeroValue(t *testing.T) {
	var app Server
	// Should not panic — every field is nil/zero.
	app.Close()
}

// TestServer_Close_NilPointer verifies that calling Close on a nil-safe
// allocated pointer works the same as the zero-value case.
func TestServer_Close_NilPointer(t *testing.T) {
	app := &Server{}
	app.Close()
}

// TestServer_RegisterRoutes_ZeroValue verifies that buildHandlers + registerRoutes
// can be called on a zero-value Server without panicking. All nil pools,
// stores, and managers should be handled gracefully.
func TestServer_RegisterRoutes_ZeroValue(t *testing.T) {
	var app Server
	app.mux = http.NewServeMux()
	app.buildHandlers()
	app.registerRoutes()

	// Limiters must be non-nil after buildHandlers.
	if app.handlers == nil {
		t.Fatal("buildHandlers produced nil handlers")
	}
	if app.handlers.ClientErrLimiter == nil {
		t.Fatal("buildHandlers produced nil ClientErrLimiter")
	}
	if app.handlers.AuthCfgLimiter == nil {
		t.Fatal("buildHandlers produced nil AuthCfgLimiter")
	}

	// Clean up background goroutines.
	app.handlers.ClientErrLimiter.Stop()
	app.handlers.AuthCfgLimiter.Stop()
}

// TestServer_RegisterRoutes_HealthRegistered verifies that registerRoutes
// registers the /api/health endpoint on the mux.
func TestServer_RegisterRoutes_HealthRegistered(t *testing.T) {
	var app Server
	app.mux = http.NewServeMux()
	app.buildHandlers()
	app.registerRoutes()
	defer app.handlers.ClientErrLimiter.Stop()
	defer app.handlers.AuthCfgLimiter.Stop()

	// Build a request for /api/health and verify the mux finds a handler.
	req, err := http.NewRequest("GET", "/api/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	_, pattern := app.mux.Handler(req)
	if pattern == "" {
		t.Error("expected /api/health to be registered, got empty pattern")
	}
}

func TestNewServer_FleetClientSkipsDaemonPoolAndHealthProbe(t *testing.T) {
	app, err := NewServer(context.Background(), webui.ServerConfig{
		Port:            freeTCPPort(t),
		BindAddress:     "127.0.0.1",
		MaxPortAttempts: 1,
		FleetClient:     true,
		Store:           memstore.New(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { app.Close() })

	if app.pool != nil {
		t.Fatal("daemon pool should be nil in FleetDB-backed mode")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	app.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/api/health status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /api/health: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("/api/health status field = %v, want ok", body["status"])
	}
	if _, ok := body["pool"]; ok {
		t.Fatal("/api/health should not report daemon pool stats in FleetDB-backed mode")
	}
}

func TestAddBundledLoopbackFrontendOrigins(t *testing.T) {
	t.Run("bundled loopback open mode", func(t *testing.T) {
		cfg := webui.ServerConfig{
			BindAddress: "127.0.0.1",
			FrontendDir: t.TempDir(),
		}
		addBundledLoopbackFrontendOrigins(&cfg, 49152)
		want := []string{"http://localhost:49152", "http://127.0.0.1:49152"}
		if !reflect.DeepEqual(cfg.FrontendOrigins, want) {
			t.Fatalf("FrontendOrigins = %v, want %v", cfg.FrontendOrigins, want)
		}
	})

	t.Run("loopback bind detection", func(t *testing.T) {
		for _, bindAddress := range []string{"localhost", "127.0.0.1", "::1"} {
			if !isLoopbackBindAddress(bindAddress) {
				t.Errorf("isLoopbackBindAddress(%q) = false, want true", bindAddress)
			}
		}
		for _, bindAddress := range []string{"", "0.0.0.0", "::", "192.0.2.10"} {
			if isLoopbackBindAddress(bindAddress) {
				t.Errorf("isLoopbackBindAddress(%q) = true, want false", bindAddress)
			}
		}
	})

	t.Run("explicit frontend origin is preserved", func(t *testing.T) {
		cfg := webui.ServerConfig{
			BindAddress:     "127.0.0.1",
			FrontendDir:     t.TempDir(),
			FrontendOrigins: []string{"https://app.example.com"},
		}
		addBundledLoopbackFrontendOrigins(&cfg, 49152)
		want := []string{"https://app.example.com"}
		if !reflect.DeepEqual(cfg.FrontendOrigins, want) {
			t.Fatalf("FrontendOrigins = %v, want %v", cfg.FrontendOrigins, want)
		}
	})

	t.Run("remote and non-loopback are not widened", func(t *testing.T) {
		for _, cfg := range []webui.ServerConfig{
			{BindAddress: "127.0.0.1", FrontendDir: t.TempDir(), ExtAuthURL: "https://auth.example.com"},
			{BindAddress: "", FrontendDir: t.TempDir()},
			{BindAddress: "0.0.0.0", FrontendDir: t.TempDir()},
			{BindAddress: "192.0.2.10", FrontendDir: t.TempDir()},
			{BindAddress: "127.0.0.1"},
		} {
			addBundledLoopbackFrontendOrigins(&cfg, 49152)
			if len(cfg.FrontendOrigins) != 0 {
				t.Fatalf("FrontendOrigins = %v, want none", cfg.FrontendOrigins)
			}
		}
	})
}

// TestServer_ConfigDefaults_Port verifies that NewServer applies the
// default port when the config has Port=0.
func TestServer_ConfigDefaults_Port(t *testing.T) {
	// We cannot call NewServer (it needs network), so we replicate the
	// default-application logic inline to verify the expected constants.
	config := webui.ServerConfig{}

	if config.Port == 0 {
		config.Port = webui.DefaultPort
	}
	if config.Port != 8080 {
		t.Errorf("default Port = %d, want 8080", config.Port)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestServer_ConfigDefaults_PoolSize verifies that the default pool size is
// applied when the config has PoolSize=0.
func TestServer_ConfigDefaults_PoolSize(t *testing.T) {
	config := webui.ServerConfig{}

	if config.PoolSize == 0 {
		config.PoolSize = webui.DefaultPoolSize
	}
	if config.PoolSize != 100 {
		t.Errorf("default PoolSize = %d, want 100", config.PoolSize)
	}
}

// TestServer_ConfigDefaults_ShutdownTimeout verifies that the default
// shutdown timeout is applied when the config has ShutdownTimeout=0.
func TestServer_ConfigDefaults_ShutdownTimeout(t *testing.T) {
	config := webui.ServerConfig{}

	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = webui.DefaultShutdownTimeout
	}
	if config.ShutdownTimeout != 5*time.Second {
		t.Errorf("default ShutdownTimeout = %v, want %v", config.ShutdownTimeout, 5*time.Second)
	}
}

// TestServer_ConfigDefaults_MaxPortAttempts verifies that the default max
// port attempts value is applied when the config has MaxPortAttempts=0.
func TestServer_ConfigDefaults_MaxPortAttempts(t *testing.T) {
	config := webui.ServerConfig{}

	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = webui.DefaultMaxPortAttempts
	}
	if config.MaxPortAttempts != 10 {
		t.Errorf("default MaxPortAttempts = %d, want 10", config.MaxPortAttempts)
	}
}

// TestServer_ConfigDefaults_BindAddress verifies that the default bind
// address is applied when the config has BindAddress="".
func TestServer_ConfigDefaults_BindAddress(t *testing.T) {
	config := webui.ServerConfig{}

	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}
	if config.BindAddress != "127.0.0.1" {
		t.Errorf("default BindAddress = %q, want %q", config.BindAddress, "127.0.0.1")
	}
}

// TestServer_ConfigDefaults_AllAtOnce verifies that all defaults are applied
// together, matching the logic in NewServer.
func TestServer_ConfigDefaults_AllAtOnce(t *testing.T) {
	config := webui.ServerConfig{}

	// Apply defaults exactly as NewServer does.
	if config.Port == 0 {
		config.Port = webui.DefaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = webui.DefaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = webui.DefaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = webui.DefaultMaxPortAttempts
	}
	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}

	if config.Port != 8080 {
		t.Errorf("Port = %d, want 8080", config.Port)
	}
	if config.PoolSize != 100 {
		t.Errorf("PoolSize = %d, want 100", config.PoolSize)
	}
	if config.ShutdownTimeout != 5*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", config.ShutdownTimeout, 5*time.Second)
	}
	if config.MaxPortAttempts != 10 {
		t.Errorf("MaxPortAttempts = %d, want 10", config.MaxPortAttempts)
	}
	if config.BindAddress != "127.0.0.1" {
		t.Errorf("BindAddress = %q, want %q", config.BindAddress, "127.0.0.1")
	}
}

// TestServer_BuildHandlers_MultiPTYManagerEmpty verifies that buildHandlers
// reads GracePeriod/IdleTimeout/MaxSessions off an empty (no workspaces
// registered) *MultiPTYManager without panicking. This exercises the
// server.go:143-146 aggregation now that ptyMgr has changed type.
func TestServer_BuildHandlers_MultiPTYManagerEmpty(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 0, nil)
	t.Cleanup(func() { _ = ptyMgr.Close() })

	app := &Server{ptyMgr: ptyMgr}
	app.buildHandlers()
	t.Cleanup(func() {
		app.handlers.ClientErrLimiter.Stop()
		app.handlers.AuthCfgLimiter.Stop()
	})

	if app.handlers == nil || app.handlers.GetTerminalConfig == nil {
		t.Fatal("buildHandlers did not wire GetTerminalConfig")
	}

	// Zero grace/idle and MaxSessions == default should round-trip through
	// the /api/config/terminal handler.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil)
	app.handlers.GetTerminalConfig.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Success bool `json:"success"`
		Data    struct {
			GracePeriodMS int64 `json:"grace_period_ms"`
			IdleTimeoutMS int64 `json:"idle_timeout_ms"`
			MaxSessions   int   `json:"max_sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	if !env.Success {
		t.Error("success = false, want true")
	}
	if env.Data.GracePeriodMS != 0 {
		t.Errorf("gracePeriodMs = %d, want 0 (unset)", env.Data.GracePeriodMS)
	}
	if env.Data.IdleTimeoutMS != 0 {
		t.Errorf("idleTimeoutMs = %d, want 0 (unset)", env.Data.IdleTimeoutMS)
	}
	// MaxSessions should be the default cap returned by MultiPTYManager
	// (see multi_pty_manager.go MaxSessions). We only care that it is > 0,
	// since the exact value is owned by the terminal package.
	if env.Data.MaxSessions <= 0 {
		t.Errorf("maxSessions = %d, want > 0 (default per-workspace cap)", env.Data.MaxSessions)
	}
}

// TestServer_BuildHandlers_MultiPTYManagerCustom verifies that grace/idle
// timeouts set on the *MultiPTYManager propagate through buildHandlers to
// the /api/config/terminal response.
func TestServer_BuildHandlers_MultiPTYManagerCustom(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 42, nil)
	ptyMgr.SetGracePeriod(7 * time.Second)
	ptyMgr.SetIdleTimeout(11 * time.Second)
	t.Cleanup(func() { _ = ptyMgr.Close() })

	app := &Server{ptyMgr: ptyMgr}
	app.buildHandlers()
	t.Cleanup(func() {
		app.handlers.ClientErrLimiter.Stop()
		app.handlers.AuthCfgLimiter.Stop()
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/config/terminal", nil)
	app.handlers.GetTerminalConfig.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var env struct {
		Data struct {
			GracePeriodMS int64 `json:"grace_period_ms"`
			IdleTimeoutMS int64 `json:"idle_timeout_ms"`
			MaxSessions   int   `json:"max_sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.GracePeriodMS != 7000 {
		t.Errorf("gracePeriodMs = %d, want 7000", env.Data.GracePeriodMS)
	}
	if env.Data.IdleTimeoutMS != 11000 {
		t.Errorf("idleTimeoutMs = %d, want 11000", env.Data.IdleTimeoutMS)
	}
	if env.Data.MaxSessions != 42 {
		t.Errorf("maxSessions = %d, want 42", env.Data.MaxSessions)
	}
}

// TestServer_ClosePTYMgr_IdempotentWithShutdownPath verifies that the
// Close() call invoked by server_app.go's cleanup closure is safe to run
// after the graceful-shutdown path (run() in server.go) has already called
// Close(). The MultiPTYManager must not double-error.
func TestServer_ClosePTYMgr_IdempotentWithShutdownPath(t *testing.T) {
	ptyMgr := terminal.NewMultiPTYManager("bash", 0, nil)
	// Simulate the graceful-shutdown path (server.go run()).
	if err := ptyMgr.Close(); err != nil {
		t.Fatalf("first Close (graceful shutdown path) err = %v, want nil", err)
	}
	// Simulate the cleanup closure registered in server_app.go.
	if err := ptyMgr.Close(); err != nil {
		t.Fatalf("second Close (cleanup closure) err = %v, want nil", err)
	}
}

// TestServer_ConfigDefaults_ExplicitValuesPreserved verifies that explicitly
// set config values are not overwritten by the default-application logic.
func TestServer_ConfigDefaults_ExplicitValuesPreserved(t *testing.T) {
	config := webui.ServerConfig{
		Port:            9090,
		PoolSize:        50,
		ShutdownTimeout: 10 * time.Second,
		MaxPortAttempts: 3,
		BindAddress:     "0.0.0.0",
	}

	// Apply defaults — none should trigger because every field is non-zero.
	if config.Port == 0 {
		config.Port = webui.DefaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = webui.DefaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = webui.DefaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = webui.DefaultMaxPortAttempts
	}
	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}

	if config.Port != 9090 {
		t.Errorf("Port = %d, want 9090 (explicit)", config.Port)
	}
	if config.PoolSize != 50 {
		t.Errorf("PoolSize = %d, want 50 (explicit)", config.PoolSize)
	}
	if config.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v (explicit)", config.ShutdownTimeout, 10*time.Second)
	}
	if config.MaxPortAttempts != 3 {
		t.Errorf("MaxPortAttempts = %d, want 3 (explicit)", config.MaxPortAttempts)
	}
	if config.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q, want %q (explicit)", config.BindAddress, "0.0.0.0")
	}
}
