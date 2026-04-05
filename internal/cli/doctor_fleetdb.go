package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/kv"
)

func checkIssueBackend() CheckResult {
	if isFleetActive() {
		return CheckResult{
			Name:    "issue_backend",
			Status:  StatusPass,
			Summary: "Issue backend: fleet (remote server)",
		}
	}
	if isFleetDBActive() {
		return CheckResult{
			Name:    "issue_backend",
			Status:  StatusPass,
			Summary: "Issue backend: fleet-db",
		}
	}
	return CheckResult{
		Name:    "issue_backend",
		Status:  StatusPass,
		Summary: "Issue backend: beads (bd CLI)",
	}
}

func checkFleetDB() CheckResult {
	dc, err := LoadDaemonConfig(GetBeadsDir())
	if err != nil {
		cfg, _ := resolveFleetDBConfig(&DaemonSettings{})
		return reportFleetDBConfig(cfg)
	}
	cfg, _ := resolveFleetDBConfig(&dc.Daemon)
	return reportFleetDBConfig(cfg)
}

func reportFleetDBConfig(cfg FleetDBServerConfig) CheckResult {
	if cfg.AutoStart && cfg.RedisURL == "" {
		return CheckResult{
			Name:    "fleetdb",
			Status:  StatusPass,
			Summary: fmt.Sprintf("fleet-db configured (workspace: %s, miniredis auto-start)", cfg.Workspace),
		}
	}
	if cfg.RedisURL == "" {
		return CheckResult{
			Name:    "fleetdb",
			Status:  StatusFail,
			Summary: "fleet-db enabled but no Redis URL configured and auto-start disabled",
			Detail:  "Set LOOM_FLEETDB_REDIS_URL or enable daemon.fleetdb.auto_start in config",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	password := os.Getenv("LOOM_FLEETDB_REDIS_PASSWORD")
	client := kv.NewClient(cfg.RedisURL, password, 0)
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		return CheckResult{
			Name:    "fleetdb",
			Status:  StatusFail,
			Summary: fmt.Sprintf("fleet-db Redis not reachable at %s", cfg.RedisURL),
			Detail:  err.Error(),
		}
	}

	return CheckResult{
		Name:    "fleetdb",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fleet-db configured (workspace: %s, Redis: %s)", cfg.Workspace, cfg.RedisURL),
	}
}

func checkFleet() CheckResult {
	dc, err := LoadDaemonConfig(GetBeadsDir())
	var fleetCfg FleetClientConfig
	if err != nil {
		fleetCfg = resolveFleetConfig(&DaemonSettings{})
	} else {
		fleetCfg = resolveFleetConfig(&dc.Daemon)
	}

	if fleetCfg.URL == "" {
		return CheckResult{
			Name:    "fleet",
			Status:  StatusFail,
			Summary: "fleet mode active but no fleet URL configured",
			Detail:  "Set daemon.fleet.url in loom.yaml or LOOM_FLEET_URL env var",
		}
	}

	return CheckResult{
		Name:    "fleet",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fleet configured (workspace: %s, URL: %s)", fleetCfg.Workspace, fleetCfg.URL),
	}
}
