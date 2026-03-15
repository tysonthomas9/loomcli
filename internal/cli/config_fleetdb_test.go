package cli

import "testing"

func TestOverlayFleetDBSettings(t *testing.T) {
	t.Run("all fields set", func(t *testing.T) {
		dst := &FleetDBSettings{}
		src := &FleetDBSettings{
			Enabled:   true,
			RedisURL:  "redis://src:6379",
			Workspace: "src-ws",
			AutoStart: true,
		}
		overlayFleetDBSettings(dst, src)
		if !dst.Enabled {
			t.Error("expected Enabled to be true")
		}
		if dst.RedisURL != "redis://src:6379" {
			t.Errorf("expected RedisURL redis://src:6379, got %s", dst.RedisURL)
		}
		if dst.Workspace != "src-ws" {
			t.Errorf("expected Workspace src-ws, got %s", dst.Workspace)
		}
		if !dst.AutoStart {
			t.Error("expected AutoStart to be true")
		}
	})

	t.Run("zero src preserves dst", func(t *testing.T) {
		dst := &FleetDBSettings{
			Enabled:   true,
			RedisURL:  "redis://dst:6379",
			Workspace: "dst-ws",
			AutoStart: true,
		}
		src := &FleetDBSettings{}
		overlayFleetDBSettings(dst, src)
		if !dst.Enabled {
			t.Error("expected Enabled to remain true")
		}
		if dst.RedisURL != "redis://dst:6379" {
			t.Errorf("expected RedisURL preserved, got %s", dst.RedisURL)
		}
		if dst.Workspace != "dst-ws" {
			t.Errorf("expected Workspace preserved, got %s", dst.Workspace)
		}
		if !dst.AutoStart {
			t.Error("expected AutoStart to remain true")
		}
	})

	t.Run("partial overlay", func(t *testing.T) {
		dst := &FleetDBSettings{
			RedisURL:  "redis://old:6379",
			Workspace: "old-ws",
		}
		src := &FleetDBSettings{
			RedisURL: "redis://new:6379",
		}
		overlayFleetDBSettings(dst, src)
		if dst.RedisURL != "redis://new:6379" {
			t.Errorf("expected RedisURL overwritten, got %s", dst.RedisURL)
		}
		if dst.Workspace != "old-ws" {
			t.Errorf("expected Workspace preserved, got %s", dst.Workspace)
		}
	})
}

func TestResolveFleetDBConfig_Defaults(t *testing.T) {
	daemon := &DaemonSettings{}
	cfg := resolveFleetDBConfig(daemon)
	if cfg.RedisURL != "" {
		t.Errorf("expected empty RedisURL, got %s", cfg.RedisURL)
	}
	if cfg.Workspace != "default" {
		t.Errorf("expected Workspace 'default', got %s", cfg.Workspace)
	}
	if cfg.AutoStart {
		t.Error("expected AutoStart false")
	}
}

func TestResolveFleetDBConfig_FromSettings(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			RedisURL:  "redis://yaml:6379",
			Workspace: "yaml-ws",
			AutoStart: true,
		},
	}
	cfg := resolveFleetDBConfig(daemon)
	if cfg.RedisURL != "redis://yaml:6379" {
		t.Errorf("expected RedisURL from settings, got %s", cfg.RedisURL)
	}
	if cfg.Workspace != "yaml-ws" {
		t.Errorf("expected Workspace from settings, got %s", cfg.Workspace)
	}
	if !cfg.AutoStart {
		t.Error("expected AutoStart true from settings")
	}
}

func TestResolveFleetDBConfig_EnvOverride(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			RedisURL:  "redis://yaml:6379",
			Workspace: "yaml-ws",
		},
	}
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env:6379")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "env-ws")
	cfg := resolveFleetDBConfig(daemon)
	if cfg.RedisURL != "redis://env:6379" {
		t.Errorf("expected env RedisURL override, got %s", cfg.RedisURL)
	}
	if cfg.Workspace != "env-ws" {
		t.Errorf("expected env Workspace override, got %s", cfg.Workspace)
	}
}

func TestResolveFleetDBConfig_EnvPrecedence(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			RedisURL: "redis://yaml:6379",
		},
	}
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env-wins:6379")
	cfg := resolveFleetDBConfig(daemon)
	if cfg.RedisURL != "redis://env-wins:6379" {
		t.Errorf("expected env var to take precedence, got %s", cfg.RedisURL)
	}
}

func TestResolveFleetDBEnabled(t *testing.T) {
	t.Run("env true", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "true")
		if !resolveFleetDBEnabled(nil) {
			t.Error("expected true from env")
		}
	})

	t.Run("env false overrides settings", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "false")
		s := &FleetDBSettings{Enabled: true}
		if resolveFleetDBEnabled(s) {
			t.Error("expected env false to override settings true")
		}
	})

	t.Run("env 1", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "1")
		if !resolveFleetDBEnabled(nil) {
			t.Error("expected true from env=1")
		}
	})

	t.Run("invalid env falls back to settings", func(t *testing.T) {
		t.Setenv("LOOM_FLEETDB_ENABLED", "notabool")
		s := &FleetDBSettings{Enabled: true}
		if !resolveFleetDBEnabled(s) {
			t.Error("expected fallback to settings.Enabled=true")
		}
	})

	t.Run("no env nil settings", func(t *testing.T) {
		if resolveFleetDBEnabled(nil) {
			t.Error("expected false with no env and nil settings")
		}
	})

	t.Run("no env uses settings", func(t *testing.T) {
		s := &FleetDBSettings{Enabled: true}
		if !resolveFleetDBEnabled(s) {
			t.Error("expected true from settings")
		}
	})
}

func TestOverlayDaemonSettings_FleetDB(t *testing.T) {
	dst := &DaemonSettings{}
	src := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled:   true,
			RedisURL:  "redis://overlay:6379",
			Workspace: "overlay-ws",
			AutoStart: true,
		},
	}
	overlayDaemonSettings(dst, src)
	if dst.FleetDB == nil {
		t.Fatal("expected FleetDB to be initialized on dst")
	}
	if !dst.FleetDB.Enabled {
		t.Error("expected Enabled true after overlay")
	}
	if dst.FleetDB.RedisURL != "redis://overlay:6379" {
		t.Errorf("expected RedisURL from overlay, got %s", dst.FleetDB.RedisURL)
	}
	if dst.FleetDB.Workspace != "overlay-ws" {
		t.Errorf("expected Workspace from overlay, got %s", dst.FleetDB.Workspace)
	}
	if !dst.FleetDB.AutoStart {
		t.Error("expected AutoStart true after overlay")
	}
}
