package cmdstore

import (
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
