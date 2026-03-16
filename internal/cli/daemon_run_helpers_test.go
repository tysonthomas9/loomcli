package cli

import "testing"

func TestInitFleetDBServer_NilFleetDBSettings(t *testing.T) {
	config := &DaemonConfig{
		Daemon: DaemonSettings{
			FleetDB: nil,
		},
	}
	// Ensure env var is not set so resolveFleetDBEnabled falls back to settings.
	t.Setenv("LOOM_FLEETDB_ENABLED", "")

	srv, err := initFleetDBServer(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv != nil {
		t.Error("expected nil when FleetDB settings is nil and env var unset")
	}
}

func TestInitFleetDBServer_DisabledExplicitly(t *testing.T) {
	config := &DaemonConfig{
		Daemon: DaemonSettings{
			FleetDB: &FleetDBSettings{
				Enabled: false,
			},
		},
	}
	t.Setenv("LOOM_FLEETDB_ENABLED", "")

	srv, err := initFleetDBServer(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv != nil {
		t.Error("expected nil when FleetDB.Enabled is false and env var unset")
	}
}

func TestInitFleetDBServer_DisabledByEnvVar(t *testing.T) {
	config := &DaemonConfig{
		Daemon: DaemonSettings{
			FleetDB: &FleetDBSettings{
				Enabled: true, // config says enabled...
			},
		},
	}
	t.Setenv("LOOM_FLEETDB_ENABLED", "false") // ...but env var overrides

	srv, err := initFleetDBServer(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv != nil {
		t.Error("expected nil when env LOOM_FLEETDB_ENABLED=false overrides settings")
	}
}

func TestInitFleetDBServer_EmptyDaemonSettings(t *testing.T) {
	config := &DaemonConfig{
		Daemon: DaemonSettings{}, // FleetDB is nil by default
	}
	t.Setenv("LOOM_FLEETDB_ENABLED", "")

	srv, err := initFleetDBServer(config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv != nil {
		t.Error("expected nil when DaemonSettings has zero-value FleetDB (nil)")
	}
}
