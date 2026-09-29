package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
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
// recorded assignee is not currently a running daemon-managed agent. This
// surfaces "lock survived agent exit" situations so the operator can either
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

	stateFilePath := cfgpkg.ResolveDaemonStatePath(cli.GetWorkspaceRuntimeDir())
	managed := monitor.LoadDaemonManagedAgents(stateFilePath)

	var orphans []string
	for _, issue := range issues {
		holder := issue.Assignee
		if holder == "" {
			continue
		}
		if _, ok := managed[holder]; ok {
			continue
		}
		orphans = append(orphans, fmt.Sprintf("issue=%s holder=%s status=stopped-or-dead", issue.ID, holder))
	}

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
		Summary: fmt.Sprintf("%d in_progress issue(s) claimed by agents that are not running", len(orphans)),
		Detail:  strings.Join(orphans, "\n") + "\nremediation: run `loom recover <worktree>` to release the fleet-db lock, or wait for TTL expiry.",
	}
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
	var cfg cfgpkg.FleetDBServerConfig
	dc, err := cfgpkg.LoadDaemonConfig(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		cfg, _ = cfgpkg.ResolveFleetDBConfig(&cfgpkg.DaemonSettings{})
	} else {
		cfg, _ = cfgpkg.ResolveFleetDBConfig(&dc.Daemon)
	}

	// ResolveFleetDBConfig reads the INVOKING SHELL's environment, so a bare
	// shell reports a hard failure against a stack that is perfectly healthy:
	// the daemon holds the configuration and this shell does not. Fall back to
	// the daemon's own published snapshot. Precedence is shell -> daemon ->
	// hard fail; the shell wins when set, even if the daemon disagrees, because
	// it is the shell's configuration being diagnosed and config_drift reports
	// the disagreement separately.
	if cfg.RedisURL != "" || cfg.AutoStart {
		return reportFleetDBConfig(cfg)
	}
	return reportFleetDBFromDaemon(cfg)
}

// reportFleetDBFromDaemon is the fallback path taken when this shell carries no
// fleet-db configuration at all.
func reportFleetDBFromDaemon(cfg cfgpkg.FleetDBServerConfig) CheckResult {
	snap, running := loadRunningDaemonSnapshot()
	if snap == nil {
		if running {
			// The daemon is up but published no snapshot (a binary predating
			// this reporting). We cannot read its configuration and must not
			// claim the stack is broken.
			return CheckResult{
				Name:    "fleetdb",
				Status:  StatusWarn,
				Summary: "cannot determine fleet-db configuration from this shell",
				Detail: "the loom daemon is running but published no env snapshot " +
					"(binary predates `loom doctor` env reporting); restart the daemon to enable it, " +
					"or set LOOM_FLEETDB_REDIS_URL in this shell",
			}
		}
		// No shell value, no auto-start, no running daemon: a genuine
		// misconfiguration, and the existing hard failure is correct.
		return reportFleetDBConfig(cfg)
	}

	origin := ""
	if v := snap.Plain("LOOM_FLEETDB_REDIS_URL"); v != "" {
		cfg.RedisURL, origin = v, daemonEnvOrigin
	}
	if b := snap.Plain("LOOM_FLEETDB_AUTO_START"); b != "" {
		// An unparseable value means false, exactly as ResolveFleetDBConfig
		// treats it. Never crash on it.
		parsed, _ := strconv.ParseBool(b)
		if parsed {
			cfg.AutoStart, origin = true, daemonEnvOrigin
		}
	}
	res := reportFleetDBConfigFrom(cfg, origin)
	if origin == "" && res.Status == StatusFail {
		res.Detail += "\nchecked: this shell and the running daemon's env snapshot; neither sets LOOM_FLEETDB_REDIS_URL"
	}
	return res
}

// daemonEnvOrigin labels a configuration value that came from the running
// daemon's snapshot rather than from this shell.
const daemonEnvOrigin = "the running daemon's environment"

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

// reportFleetDBConfig reports a configuration resolved from this shell.
func reportFleetDBConfig(cfg cfgpkg.FleetDBServerConfig) CheckResult {
	return reportFleetDBConfigFrom(cfg, "")
}

// reportFleetDBConfigFrom reports a configuration, naming where it came from.
// origin is "" for this shell, or a phrase naming another source; when set it
// is appended to Detail on every branch so the reader can tell which
// environment the verdict describes.
func reportFleetDBConfigFrom(cfg cfgpkg.FleetDBServerConfig, origin string) CheckResult {
	if cfg.AutoStart && cfg.RedisURL == "" {
		return withConfigOrigin(CheckResult{
			Name:    "fleetdb",
			Status:  StatusPass,
			Summary: fmt.Sprintf("fleet-db configured (workspace: %s, miniredis auto-start)", cfg.Workspace),
		}, origin)
	}
	if cfg.RedisURL == "" {
		return withConfigOrigin(CheckResult{
			Name:    "fleetdb",
			Status:  StatusFail,
			Summary: "fleet-db enabled but no Redis URL configured and auto-start disabled",
			Detail:  "Set LOOM_FLEETDB_REDIS_URL or LOOM_FLEETDB_AUTO_START=true",
		}, origin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	password := os.Getenv("LOOM_FLEETDB_REDIS_PASSWORD")
	client := kv.NewClient(cfg.RedisURL, password, 0)
	defer func() { _ = client.Close() }()

	if err := client.Ping(ctx); err != nil {
		return withConfigOrigin(CheckResult{
			Name:    "fleetdb",
			Status:  StatusFail,
			Summary: fmt.Sprintf("fleet-db Redis not reachable at %s", cfg.RedisURL),
			Detail:  err.Error(),
		}, origin)
	}

	return withConfigOrigin(CheckResult{
		Name:    "fleetdb",
		Status:  StatusPass,
		Summary: fmt.Sprintf("fleet-db configured (workspace: %s, Redis: %s)", cfg.Workspace, cfg.RedisURL),
	}, origin)
}

// withConfigOrigin appends a "source:" line naming where the configuration came
// from. A shell-resolved configuration (origin "") is left untouched, so the
// existing reports are unchanged.
func withConfigOrigin(res CheckResult, origin string) CheckResult {
	if origin == "" {
		return res
	}
	line := fmt.Sprintf("source: %s (this shell does not set LOOM_FLEETDB_REDIS_URL)", origin)
	if res.Detail == "" {
		res.Detail = line
	} else {
		res.Detail += "\n" + line
	}
	return res
}

func checkFleet() CheckResult {
	dc, err := cfgpkg.LoadDaemonConfig(cli.GetWorkspaceRuntimeDir())
	var fleetCfg cfgpkg.FleetClientConfig
	if err != nil {
		fleetCfg = cfgpkg.ResolveFleetConfig(&cfgpkg.DaemonSettings{})
	} else {
		fleetCfg = cfgpkg.ResolveFleetConfig(&dc.Daemon)
	}

	if fleetCfg.URL == "" {
		return CheckResult{
			Name:    "fleet",
			Status:  StatusFail,
			Summary: "fleet mode active but no fleet URL configured",
			Detail:  "Set LOOM_FLEET_URL or the daemon fleet URL in FleetDB",
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
