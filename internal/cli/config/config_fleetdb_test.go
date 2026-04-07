package config

import (
	"testing"
)

func TestOverlayFleetDBSettings_NilFieldsPreserved(t *testing.T) {
	dst := &FleetDBSettings{
		Enabled:   BoolPtr(true),
		RedisURL:  "redis://dst:6379",
		Workspace: "prod",
		AutoStart: BoolPtr(false),
	}
	src := &FleetDBSettings{} // all zero/nil

	overlayFleetDBSettings(dst, src)

	if dst.Enabled == nil || *dst.Enabled != true {
		t.Error("Enabled should remain true")
	}
	if dst.RedisURL != "redis://dst:6379" {
		t.Errorf("RedisURL should remain, got %q", dst.RedisURL)
	}
	if dst.Workspace != "prod" {
		t.Errorf("Workspace should remain, got %q", dst.Workspace)
	}
	if dst.AutoStart == nil || *dst.AutoStart != false {
		t.Error("AutoStart should remain false")
	}
}

func TestOverlayFleetDBSettings_SrcOverridesDst(t *testing.T) {
	dst := &FleetDBSettings{
		Enabled:   BoolPtr(false),
		RedisURL:  "redis://old:6379",
		Workspace: "old-ws",
		AutoStart: BoolPtr(false),
	}
	src := &FleetDBSettings{
		Enabled:   BoolPtr(true),
		RedisURL:  "redis://new:6379",
		Workspace: "new-ws",
		AutoStart: BoolPtr(true),
	}

	overlayFleetDBSettings(dst, src)

	if *dst.Enabled != true {
		t.Error("Enabled should be overridden to true")
	}
	if dst.RedisURL != "redis://new:6379" {
		t.Errorf("RedisURL should be overridden, got %q", dst.RedisURL)
	}
	if dst.Workspace != "new-ws" {
		t.Errorf("Workspace should be overridden, got %q", dst.Workspace)
	}
	if *dst.AutoStart != true {
		t.Error("AutoStart should be overridden to true")
	}
}

func TestOverlayFleetDBSettings_PartialOverride(t *testing.T) {
	dst := &FleetDBSettings{
		Enabled:   BoolPtr(true),
		RedisURL:  "redis://old:6379",
		Workspace: "keep-this",
	}
	src := &FleetDBSettings{
		RedisURL: "redis://new:6379",
	}

	overlayFleetDBSettings(dst, src)

	if *dst.Enabled != true {
		t.Error("Enabled should be preserved")
	}
	if dst.RedisURL != "redis://new:6379" {
		t.Errorf("RedisURL should be overridden, got %q", dst.RedisURL)
	}
	if dst.Workspace != "keep-this" {
		t.Errorf("Workspace should be preserved, got %q", dst.Workspace)
	}
	if dst.AutoStart != nil {
		t.Error("AutoStart should remain nil")
	}
}

func TestResolveFleetDBConfig_Defaults(t *testing.T) {
	daemon := &DaemonSettings{}
	cfg, enabled := ResolveFleetDBConfig(daemon)

	if enabled {
		t.Error("enabled should default to false")
	}
	if cfg.Workspace != "default" {
		t.Errorf("workspace should default to %q, got %q", "default", cfg.Workspace)
	}
	if cfg.AutoStart {
		t.Error("auto_start should default to false")
	}
	if cfg.RedisURL != "" {
		t.Errorf("redis_url should default to empty, got %q", cfg.RedisURL)
	}
}

func TestResolveFleetDBConfig_NilFleetDB(t *testing.T) {
	daemon := &DaemonSettings{FleetDB: nil}
	cfg, enabled := ResolveFleetDBConfig(daemon)

	if enabled {
		t.Error("enabled should be false with nil FleetDB")
	}
	if cfg.Workspace != "default" {
		t.Errorf("workspace should be %q, got %q", "default", cfg.Workspace)
	}
}

func TestResolveFleetDBConfig_YAMLValues(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled:   BoolPtr(true),
			RedisURL:  "redis://myhost:6379",
			Workspace: "staging",
			AutoStart: BoolPtr(true),
		},
	}

	cfg, enabled := ResolveFleetDBConfig(daemon)

	if !enabled {
		t.Error("enabled should be true from YAML")
	}
	if cfg.RedisURL != "redis://myhost:6379" {
		t.Errorf("redis_url = %q, want redis://myhost:6379", cfg.RedisURL)
	}
	if cfg.Workspace != "staging" {
		t.Errorf("workspace = %q, want staging", cfg.Workspace)
	}
	if !cfg.AutoStart {
		t.Error("auto_start should be true from YAML")
	}
}

func TestResolveFleetDBConfig_EnvVarOverride(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled:   BoolPtr(false),
			RedisURL:  "redis://yaml:6379",
			Workspace: "yaml-ws",
			AutoStart: BoolPtr(false),
		},
	}

	t.Setenv("LOOM_FLEETDB_ENABLED", "true")
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env:6379")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "env-ws")
	t.Setenv("LOOM_FLEETDB_AUTO_START", "true")

	cfg, enabled := ResolveFleetDBConfig(daemon)

	if !enabled {
		t.Error("env var should override enabled to true")
	}
	if cfg.RedisURL != "redis://env:6379" {
		t.Errorf("redis_url = %q, want redis://env:6379", cfg.RedisURL)
	}
	if cfg.Workspace != "env-ws" {
		t.Errorf("workspace = %q, want env-ws", cfg.Workspace)
	}
	if !cfg.AutoStart {
		t.Error("auto_start env var should override to true")
	}
}

func TestResolveFleetDBConfig_EnvVarPrecedence(t *testing.T) {
	daemon := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled: BoolPtr(true),
		},
	}

	t.Setenv("LOOM_FLEETDB_ENABLED", "false")

	_, enabled := ResolveFleetDBConfig(daemon)
	if enabled {
		t.Error("env var false should override YAML true")
	}
}

func TestResolveFleetDBConfig_EnabledParsing(t *testing.T) {
	tests := []struct {
		envVal      string
		wantEnabled bool
	}{
		{"true", true},
		{"1", true},
		{"TRUE", true},
		{"True", true},
		{"false", false},
		{"0", false},
		{"FALSE", false},
		{"invalid", false}, // invalid → warning + false
		{"yes", false},     // not recognized by strconv.ParseBool
	}

	for _, tt := range tests {
		t.Run(tt.envVal, func(t *testing.T) {
			t.Setenv("LOOM_FLEETDB_ENABLED", tt.envVal)
			daemon := &DaemonSettings{}
			_, enabled := ResolveFleetDBConfig(daemon)
			if enabled != tt.wantEnabled {
				t.Errorf("LOOM_FLEETDB_ENABLED=%q → enabled=%v, want %v", tt.envVal, enabled, tt.wantEnabled)
			}
		})
	}
}

func TestOverlayDaemonSettings_FleetDB(t *testing.T) {
	dst := &DaemonSettings{}
	src := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled:  BoolPtr(true),
			RedisURL: "redis://test:6379",
		},
	}

	OverlayDaemonSettings(dst, src)

	if dst.FleetDB == nil {
		t.Fatal("FleetDB should be set after overlay")
	}
	if *dst.FleetDB.Enabled != true {
		t.Error("Enabled should be true")
	}
	if dst.FleetDB.RedisURL != "redis://test:6379" {
		t.Errorf("RedisURL = %q, want redis://test:6379", dst.FleetDB.RedisURL)
	}
}

func TestOverlayDaemonSettings_FleetDB_MergesExisting(t *testing.T) {
	dst := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled:   BoolPtr(false),
			Workspace: "global-ws",
		},
	}
	src := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled: BoolPtr(true),
		},
	}

	OverlayDaemonSettings(dst, src)

	if *dst.FleetDB.Enabled != true {
		t.Error("Enabled should be overridden to true")
	}
	if dst.FleetDB.Workspace != "global-ws" {
		t.Errorf("Workspace should be preserved, got %q", dst.FleetDB.Workspace)
	}
}
