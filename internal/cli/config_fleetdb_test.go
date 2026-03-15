package cli

import (
	"strings"
	"testing"
)

func TestOverlayFleetDBSettings_NilFieldsPreserved(t *testing.T) {
	dst := &FleetDBSettings{
		Enabled:   boolPtr(true),
		RedisURL:  "redis://dst:6379",
		Workspace: "prod",
		AutoStart: boolPtr(false),
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
		Enabled:   boolPtr(false),
		RedisURL:  "redis://old:6379",
		Workspace: "old-ws",
		AutoStart: boolPtr(false),
	}
	src := &FleetDBSettings{
		Enabled:   boolPtr(true),
		RedisURL:  "redis://new:6379",
		Workspace: "new-ws",
		AutoStart: boolPtr(true),
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
		Enabled:   boolPtr(true),
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
	cfg, enabled := resolveFleetDBConfig(daemon)

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
	cfg, enabled := resolveFleetDBConfig(daemon)

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
			Enabled:   boolPtr(true),
			RedisURL:  "redis://myhost:6379",
			Workspace: "staging",
			AutoStart: boolPtr(true),
		},
	}

	cfg, enabled := resolveFleetDBConfig(daemon)

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
			Enabled:   boolPtr(false),
			RedisURL:  "redis://yaml:6379",
			Workspace: "yaml-ws",
			AutoStart: boolPtr(false),
		},
	}

	t.Setenv("LOOM_FLEETDB_ENABLED", "true")
	t.Setenv("LOOM_FLEETDB_REDIS_URL", "redis://env:6379")
	t.Setenv("LOOM_FLEETDB_WORKSPACE", "env-ws")
	t.Setenv("LOOM_FLEETDB_AUTO_START", "true")

	cfg, enabled := resolveFleetDBConfig(daemon)

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
			Enabled: boolPtr(true),
		},
	}

	t.Setenv("LOOM_FLEETDB_ENABLED", "false")

	_, enabled := resolveFleetDBConfig(daemon)
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
			_, enabled := resolveFleetDBConfig(daemon)
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
			Enabled:  boolPtr(true),
			RedisURL: "redis://test:6379",
		},
	}

	overlayDaemonSettings(dst, src)

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
			Enabled:   boolPtr(false),
			Workspace: "global-ws",
		},
	}
	src := &DaemonSettings{
		FleetDB: &FleetDBSettings{
			Enabled: boolPtr(true),
		},
	}

	overlayDaemonSettings(dst, src)

	if *dst.FleetDB.Enabled != true {
		t.Error("Enabled should be overridden to true")
	}
	if dst.FleetDB.Workspace != "global-ws" {
		t.Errorf("Workspace should be preserved, got %q", dst.FleetDB.Workspace)
	}
}

func TestValidateFleetDBSettings_ValidRedisURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &FleetDBSettings{
		RedisURL: "redis://localhost:6379",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetDBSettings_ValidRedissURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &FleetDBSettings{
		RedisURL: "rediss://secure-host:6380",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings for rediss://, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetDBSettings_InvalidRedisURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &FleetDBSettings{
		RedisURL: "http://wrong:6379",
	})
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 warning, got %d: %s", len(r.Issues), r.FormatIssues())
	}
	if r.Issues[0].Severity != "warning" {
		t.Errorf("expected warning, got %s", r.Issues[0].Severity)
	}
	if !strings.Contains(r.Issues[0].Message, "redis://") {
		t.Errorf("expected redis:// mention, got %q", r.Issues[0].Message)
	}
}

func TestValidateFleetDBSettings_InvalidWorkspace(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &FleetDBSettings{
		Workspace: "my workspace",
	})
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 warning, got %d: %s", len(r.Issues), r.FormatIssues())
	}
	if r.Issues[0].Severity != "warning" {
		t.Errorf("expected warning, got %s", r.Issues[0].Severity)
	}
}

func TestValidateFleetDBSettings_EmptyFields(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &FleetDBSettings{})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings for empty settings, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_WithFleetDB(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			FleetDB: &FleetDBSettings{
				RedisURL: "http://invalid:6379",
			},
		},
		Roles: make(map[string]RoleConfig),
	}
	r := ValidateProjectConfig(dc, t.TempDir())
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "daemon.fleetdb.redis_url" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fleetdb redis_url warning, got: %s", r.FormatIssues())
	}
}

func TestValidateGlobalConfig_WithFleetDB(t *testing.T) {
	cfg := &LoomConfig{
		Daemon: &DaemonSettings{
			FleetDB: &FleetDBSettings{
				RedisURL: "tcp://bad:6379",
			},
		},
		Workspaces: make(map[string]WorkspaceConfig),
	}
	r := ValidateGlobalConfig(cfg)
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "daemon.fleetdb.redis_url" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fleetdb redis_url warning, got: %s", r.FormatIssues())
	}
}
