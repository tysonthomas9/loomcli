package sandbox

import "testing"

func TestFleetDBURL_GatewayRewrite(t *testing.T) {
	// Force the Docker default gateway so the rewrite target is deterministic.
	t.Setenv("LOOM_SANDBOX_HOST_GATEWAY", "")
	t.Setenv("OPENSHELL_DRIVERS", "")

	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "")
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:18099")
	if got := FleetDBURL(); got != "http://host.docker.internal:18099" {
		t.Errorf("got %q, want host-gateway rewrite", got)
	}

	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "http://fleet.internal:9000")
	if got := FleetDBURL(); got != "http://fleet.internal:9000" {
		t.Errorf("explicit override should win, got %q", got)
	}
}
