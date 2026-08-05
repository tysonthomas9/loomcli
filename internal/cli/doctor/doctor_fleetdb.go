package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/kv"
)

// checkOrphanedFleetLocks scans all in_progress issues and warns when the
// recorded assignee is not currently visible in durable/live agent status. This
// surfaces "claim survived agent exit" situations so the operator can either
// `loom recover <worktree>` (which releases the fleet-db lock) or wait for
// the TTL to expire.
//
// Report-only. Returns an empty CheckResult (skipped) when no IssueBackend is
// configured, when listing fails, or when no in_progress issues exist.
func checkOrphanedFleetLocks(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	issues, err := deps.IssueBackend.List(ctx, backend.ListOpts{Status: "in_progress"})
	if err != nil {
		return CheckResult{}
	}
	if len(issues) == 0 {
		return CheckResult{}
	}

	orphans := orphanedFleetLocks(issues, activeFleetAgentNames())

	if len(orphans) == 0 {
		return CheckResult{
			Name:    "orphaned_fleet_locks",
			Status:  StatusPass,
			Summary: fmt.Sprintf("no orphaned fleet-db issue locks (%d in_progress claimed by live agents)", len(issues)),
		}
	}

	sort.Strings(orphans)
	return CheckResult{
		Name:    "orphaned_fleet_locks",
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d in_progress issue(s) claimed by agents without live runtime status", len(orphans)),
		Detail:  strings.Join(orphans, "\n") + "\nremediation: run `loom recover <worktree>` to release the fleet-db lock, or wait for TTL expiry.",
	}
}

func activeFleetAgentNames() map[string]struct{} {
	active := make(map[string]struct{})
	for _, agent := range monitor.CollectAgentStatusOnly("") {
		if agent.LiveStatus == "working" || agent.ActiveTaskID != "" || agent.CurrentTaskID != "" ||
			strings.HasPrefix(agent.Status, "working:") || strings.HasPrefix(agent.Status, "planning:") ||
			strings.HasPrefix(agent.Status, "review:") {
			active[agent.Name] = struct{}{}
		}
	}
	return active
}

func orphanedFleetLocks(issues []backend.IssueData, active map[string]struct{}) []string {
	orphans := make([]string, 0)
	for _, issue := range issues {
		if issue.Assignee == "" {
			continue
		}
		if _, ok := active[issue.Assignee]; ok {
			continue
		}
		orphans = append(orphans, fmt.Sprintf("issue=%s holder=%s status=stopped-or-dead", issue.ID, issue.Assignee))
	}
	return orphans
}

// fleetHealthProbe is overridden in tests to avoid real network calls.
// Default probes <url>/healthz with a short timeout.
var fleetHealthProbe = defaultFleetHealthProbe

func defaultFleetHealthProbe(ctx context.Context, baseURL string) error {
	url := strings.TrimRight(baseURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return nil
}

func checkIssueBackend() CheckResult {
	if cli.IsFleetActive() {
		return CheckResult{
			Name:    "issue_backend",
			Status:  StatusPass,
			Summary: "Issue backend: fleet (remote server)",
		}
	}
	if cli.IsFleetDBActive() {
		return CheckResult{
			Name:    "issue_backend",
			Status:  StatusPass,
			Summary: "Issue backend: fleet-db",
		}
	}
	return CheckResult{
		Name:    "issue_backend",
		Status:  StatusPass,
		Summary: "Issue backend: fleet-db",
	}
}

func checkFleetDB() CheckResult {
	if bootstrap.DetectMode() == bootstrap.ModeLocal {
		return checkEmbeddedFleetDB()
	}
	return reportFleetDBConfig(cfgpkg.ResolveFleetDBConfig())
}

func checkEmbeddedFleetDB() CheckResult {
	diag := bootstrap.DiagnoseFleetDBBinary()
	if diag.Err != nil {
		return CheckResult{
			Name:    "fleetdb",
			Status:  StatusFail,
			Summary: "embedded fleet-db binary is not ready",
			Detail:  fmt.Sprintf("%v\nchecked: %s\nremediation: %s", diag.Err, strings.Join(diag.Checked, ", "), diag.Remediation),
		}
	}
	return CheckResult{
		Name:    "fleetdb",
		Status:  StatusPass,
		Summary: fmt.Sprintf("embedded fleet-db ready (%s)", diag.Path),
		Detail:  fmt.Sprintf("checked: %s", strings.Join(diag.Checked, ", ")),
	}
}

func reportFleetDBConfig(cfg cfgpkg.FleetDBServerConfig) CheckResult {
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
			Detail:  "Set LOOM_FLEETDB_REDIS_URL or LOOM_FLEETDB_AUTO_START=true",
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
	fleetCfg := cfgpkg.ResolveFleetConfig()

	if fleetCfg.URL == "" {
		return CheckResult{
			Name:    "fleet",
			Status:  StatusFail,
			Summary: "fleet mode active but no fleet URL configured",
			Detail:  "Set LOOM_FLEET_URL",
		}
	}

	// Probe /healthz so the agent CLI's expected backend is actually
	// reachable. Configured-but-down was reporting PASS before this.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := fleetHealthProbe(ctx, fleetCfg.URL); err != nil {
		return CheckResult{
			Name:    "fleet",
			Status:  StatusFail,
			Summary: fmt.Sprintf("fleet URL configured but not reachable at %s", fleetCfg.URL),
			Detail:  fmt.Sprintf("probe failed: %v. Check fleet-db is running and the URL is correct.", err),
		}
	}

	return CheckResult{
		Name:    "fleet",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fleet configured and reachable (workspace: %s, URL: %s)", fleetCfg.Workspace, fleetCfg.URL),
	}
}
