package cli

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestValidateFleetDBSettings_ValidRedisURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &config.FleetDBSettings{
		RedisURL: "redis://localhost:6379",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetDBSettings_ValidRedissURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &config.FleetDBSettings{
		RedisURL: "rediss://secure-host:6380",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings for rediss://, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetDBSettings_InvalidRedisURL(t *testing.T) {
	r := &ValidationResult{}
	validateFleetDBSettings(r, &config.FleetDBSettings{
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
	validateFleetDBSettings(r, &config.FleetDBSettings{
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
	validateFleetDBSettings(r, &config.FleetDBSettings{})
	if len(r.Issues) != 0 {
		t.Errorf("expected no warnings for empty settings, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_WithFleetDB(t *testing.T) {
	dc := &config.DaemonConfig{
		Daemon: config.DaemonSettings{
			FleetDB: &config.FleetDBSettings{
				RedisURL: "http://invalid:6379",
			},
		},
		Roles: make(map[string]config.RoleConfig),
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
	cfg := &config.LoomConfig{
		Daemon: &config.DaemonSettings{
			FleetDB: &config.FleetDBSettings{
				RedisURL: "tcp://bad:6379",
			},
		},
		Workspaces: make(map[string]config.WorkspaceConfig),
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
