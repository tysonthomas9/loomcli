package cmdstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

func TestEnsureFleetDBEnvFromFleetEnv(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-db:8080")
	t.Setenv("LOOM_FLEET_ACTOR", "parity-harness")

	ensureFleetDBEnvFromFleetEnv()

	if got := os.Getenv(bootstrap.EnvFleetDBURL); got != "http://fleet-db:8080" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBURL, got, "http://fleet-db:8080")
	}
	if got := os.Getenv(bootstrap.EnvFleetDBActor); got != "parity-harness" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBActor, got, "parity-harness")
	}
}

func TestEnsureFleetDBEnvFromFleetEnv_DoesNotOverrideExplicitFleetDBEnv(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "http://explicit:8080")
	t.Setenv(bootstrap.EnvFleetDBActor, "explicit-actor")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-db:8080")
	t.Setenv("LOOM_FLEET_ACTOR", "parity-harness")

	ensureFleetDBEnvFromFleetEnv()

	if got := os.Getenv(bootstrap.EnvFleetDBURL); got != "http://explicit:8080" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBURL, got, "http://explicit:8080")
	}
	if got := os.Getenv(bootstrap.EnvFleetDBActor); got != "explicit-actor" {
		t.Fatalf("%s = %q, want %q", bootstrap.EnvFleetDBActor, got, "explicit-actor")
	}
}

func TestOpenStoreWithCapabilitiesForwardsCallerRequirements(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != fleetdb.CapabilitiesAPIPath {
			t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, fleetdb.CapabilitiesAPIPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":["workflow_catalog.version_lifecycle.v1"]}`))
	}))
	defer server.Close()

	t.Setenv(bootstrap.EnvFleetDBURL, server.URL)
	t.Setenv("LOOM_FLEET_URL", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	handle, err := OpenStoreWithCapabilities(context.Background(), []string{"workflow_catalog.version_lifecycle.v1"})
	if err != nil {
		t.Fatalf("OpenStoreWithCapabilities: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("capability probe calls = %d, want 1", got)
	}
}
