package cmdstore

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func TestEnsureFleetDBEnvFromFleetEnvIgnoresLegacyFleetEnv(t *testing.T) {
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBActor, "")
	t.Setenv("LOOM_FLEET_URL", "http://fleet-db:8080")
	t.Setenv("LOOM_FLEET_ACTOR", "parity-harness")

	ensureFleetDBEnvFromFleetEnv()

	if got := os.Getenv(bootstrap.EnvFleetDBURL); got != "" {
		t.Fatalf("%s = %q, want empty", bootstrap.EnvFleetDBURL, got)
	}
	if got := os.Getenv(bootstrap.EnvFleetDBActor); got != "" {
		t.Fatalf("%s = %q, want empty", bootstrap.EnvFleetDBActor, got)
	}
}

func TestEnsureFleetDBEnvFromFleetEnvDoesNotOverrideExplicitFleetDBEnv(t *testing.T) {
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

func TestOpenStoreRejectsServerURLRemoteMode(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_SERVER_URL", "http://127.0.0.1:18080")
	t.Setenv(bootstrap.EnvFleetDBURL, "")
	t.Setenv(bootstrap.EnvFleetDBBin, "/missing/fleet-db")

	_, err := OpenStore(context.Background())
	if err == nil {
		t.Fatal("OpenStore succeeded, want LOOM_SERVER_URL routing error")
	}
	if !strings.Contains(err.Error(), "LOOM_SERVER_URL") {
		t.Fatalf("error = %v, want LOOM_SERVER_URL routing error", err)
	}
}

func TestOpenStoreAllowsExplicitFleetDBBackendWithServerURL(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_SERVER_URL", "http://127.0.0.1:18080")
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	t.Setenv(bootstrap.EnvFleetDBURL, "http://fleet-db:8080")

	handle, err := OpenStore(context.Background())
	if err != nil {
		t.Fatalf("OpenStore returned error with explicit fleetdb backend: %v", err)
	}
	defer func() { _ = handle.Close() }()

	if got := handle.URL(); got != "http://fleet-db:8080" {
		t.Fatalf("handle.URL() = %q, want fleet-db URL", got)
	}
}
