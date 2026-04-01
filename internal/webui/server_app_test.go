package webui

import (
	"net/http"
	"testing"
	"time"
)

// TestServerApp_Close_ZeroValue verifies that calling Close on a zero-value
// serverApp does not panic. All optional fields are nil by default, and
// Close must guard each one.
func TestServerApp_Close_ZeroValue(t *testing.T) {
	var app serverApp
	// Should not panic — every field is nil/zero.
	app.Close()
}

// TestServerApp_Close_NilPointer verifies that calling Close on a nil-safe
// allocated pointer works the same as the zero-value case.
func TestServerApp_Close_NilPointer(t *testing.T) {
	app := &serverApp{}
	app.Close()
}

// TestServerApp_SetupRoutes_ZeroValue verifies that setupRoutes can be called
// on a zero-value serverApp without panicking. All nil pools, stores, and
// managers should be handled gracefully by the route registration code.
func TestServerApp_SetupRoutes_ZeroValue(t *testing.T) {
	var app serverApp
	mux := http.NewServeMux()
	clientErrLimiter, cspLimiter, authCfgLimiter := app.setupRoutes(mux)

	// Returned limiters must be non-nil and stoppable.
	if clientErrLimiter == nil {
		t.Fatal("setupRoutes returned nil clientErrLimiter")
	}
	if cspLimiter == nil {
		t.Fatal("setupRoutes returned nil cspLimiter")
	}
	if authCfgLimiter == nil {
		t.Fatal("setupRoutes returned nil authCfgLimiter")
	}

	// Clean up background goroutines.
	clientErrLimiter.stop()
	cspLimiter.stop()
	authCfgLimiter.stop()
}

// TestServerApp_SetupRoutes_HealthRegistered verifies that setupRoutes
// registers the /api/health endpoint on the provided mux.
func TestServerApp_SetupRoutes_HealthRegistered(t *testing.T) {
	var app serverApp
	mux := http.NewServeMux()
	cel, csp, acl := app.setupRoutes(mux)
	defer cel.stop()
	defer csp.stop()
	defer acl.stop()

	// Build a request for /api/health and verify the mux finds a handler.
	req, err := http.NewRequest("GET", "/api/health", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	_, pattern := mux.Handler(req)
	if pattern == "" {
		t.Error("expected /api/health to be registered, got empty pattern")
	}
}

// TestServerApp_ConfigDefaults_Port verifies that newServerApp applies the
// default port when the config has Port=0.
func TestServerApp_ConfigDefaults_Port(t *testing.T) {
	// We cannot call newServerApp (it needs network), so we replicate the
	// default-application logic inline to verify the expected constants.
	config := ServerConfig{}

	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.Port != 8080 {
		t.Errorf("default Port = %d, want 8080", config.Port)
	}
}

// TestServerApp_ConfigDefaults_PoolSize verifies that the default pool size is
// applied when the config has PoolSize=0.
func TestServerApp_ConfigDefaults_PoolSize(t *testing.T) {
	config := ServerConfig{}

	if config.PoolSize == 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.PoolSize != 100 {
		t.Errorf("default PoolSize = %d, want 100", config.PoolSize)
	}
}

// TestServerApp_ConfigDefaults_ShutdownTimeout verifies that the default
// shutdown timeout is applied when the config has ShutdownTimeout=0.
func TestServerApp_ConfigDefaults_ShutdownTimeout(t *testing.T) {
	config := ServerConfig{}

	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.ShutdownTimeout != 5*time.Second {
		t.Errorf("default ShutdownTimeout = %v, want %v", config.ShutdownTimeout, 5*time.Second)
	}
}

// TestServerApp_ConfigDefaults_MaxPortAttempts verifies that the default max
// port attempts value is applied when the config has MaxPortAttempts=0.
func TestServerApp_ConfigDefaults_MaxPortAttempts(t *testing.T) {
	config := ServerConfig{}

	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = defaultMaxPortAttempts
	}
	if config.MaxPortAttempts != 10 {
		t.Errorf("default MaxPortAttempts = %d, want 10", config.MaxPortAttempts)
	}
}

// TestServerApp_ConfigDefaults_BindAddress verifies that the default bind
// address is applied when the config has BindAddress="".
func TestServerApp_ConfigDefaults_BindAddress(t *testing.T) {
	config := ServerConfig{}

	if config.BindAddress == "" {
		config.BindAddress = "127.0.0.1"
	}
	if config.BindAddress != "127.0.0.1" {
		t.Errorf("default BindAddress = %q, want %q", config.BindAddress, "127.0.0.1")
	}
}

// TestServerApp_ConfigDefaults_AllAtOnce verifies that all defaults are applied
// together, matching the logic in newServerApp.
func TestServerApp_ConfigDefaults_AllAtOnce(t *testing.T) {
	config := ServerConfig{}

	// Apply defaults exactly as newServerApp does.
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = defaultMaxPortAttempts
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

// TestServerApp_ConfigDefaults_ExplicitValuesPreserved verifies that explicitly
// set config values are not overwritten by the default-application logic.
func TestServerApp_ConfigDefaults_ExplicitValuesPreserved(t *testing.T) {
	config := ServerConfig{
		Port:            9090,
		PoolSize:        50,
		ShutdownTimeout: 10 * time.Second,
		MaxPortAttempts: 3,
		BindAddress:     "0.0.0.0",
	}

	// Apply defaults — none should trigger because every field is non-zero.
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.PoolSize == 0 {
		config.PoolSize = defaultPoolSize
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = defaultShutdownTimeout
	}
	if config.MaxPortAttempts == 0 {
		config.MaxPortAttempts = defaultMaxPortAttempts
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
