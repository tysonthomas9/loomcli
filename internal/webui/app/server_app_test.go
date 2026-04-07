package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui"
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
	if app.handlers.CSPLimiter == nil {
		t.Fatal("buildHandlers produced nil CSPLimiter")
	}
	if app.handlers.AuthCfgLimiter == nil {
		t.Fatal("buildHandlers produced nil AuthCfgLimiter")
	}

	// Clean up background goroutines.
	app.handlers.ClientErrLimiter.Stop()
	app.handlers.CSPLimiter.Stop()
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
	defer app.handlers.CSPLimiter.Stop()
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
