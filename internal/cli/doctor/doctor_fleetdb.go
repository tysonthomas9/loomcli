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
	"github.com/tysonthomas9/loomcli/internal/types"
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

// maxDecomposedScan caps how many `decomposed` issues checkDecomposedWithout-
// Children will fan out child queries for. One List per issue is an N+1 against
// fleet-db; past this many the check reports the truncation instead of issuing
// them.
const maxDecomposedScan = 50

const decomposedCheckName = "decomposed_without_children"

// checkDecomposedWithoutChildren warns when a non-terminal issue labeled
// `decomposed` has no children at all. A decomposer that creates children
// without `--parent` leaves such a parent behind: the children run and close
// normally while the parent sits in decomposed + blocked forever, because the
// integrator only un-parks a parent "when every child is closed" and a parent
// with no children never reaches that moment. It looks identical to a parent
// legitimately waiting on work, so nothing else surfaces it.
//
// Report-only. Returns an empty CheckResult (skipped) when no IssueBackend is
// configured, when listing fails, or when no decomposed issues exist.
func checkDecomposedWithoutChildren(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	issues, err := deps.IssueBackend.List(ctx, backend.ListOpts{Labels: []string{"decomposed"}})
	if err != nil || len(issues) == 0 {
		return CheckResult{}
	}

	live := liveDecomposedIssues(issues)
	if len(live) > maxDecomposedScan {
		return decomposedTruncatedResult(len(live))
	}

	offenders, ok := decomposedOffenders(ctx, deps.IssueBackend, live)
	if !ok {
		return CheckResult{}
	}
	if len(offenders) == 0 {
		return CheckResult{
			Name:    decomposedCheckName,
			Status:  StatusPass,
			Summary: fmt.Sprintf("no decomposed issues without children (%d checked)", len(live)),
		}
	}

	sort.Strings(offenders)
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d decomposed issue(s) have no children", len(offenders)),
		Detail: strings.Join(offenders, "\n") +
			"\nremediation: the split lost its parent links \u2014 re-link each child with " +
			"`loom data update <child> --parent <parent>`, then let the integrator un-park the parent.",
	}
}

// liveDecomposedIssues drops the terminal statuses: a closed or tombstoned
// parent is nobody's problem, however it was split.
func liveDecomposedIssues(issues []backend.IssueData) []backend.IssueData {
	live := make([]backend.IssueData, 0, len(issues))
	for _, issue := range issues {
		if issue.Status == string(types.StatusClosed) || issue.Status == string(types.StatusTombstone) {
			continue
		}
		live = append(live, issue)
	}
	return live
}

// decomposedOffenders returns one line per parent whose child query comes back
// empty. The bool is false when a child query failed, which the caller reports
// as skipped rather than as a half-scanned board.
func decomposedOffenders(ctx context.Context, be backend.IssueBackend, live []backend.IssueData) ([]string, bool) {
	var offenders []string
	for _, issue := range live {
		kids, err := be.List(ctx, backend.ListOpts{ParentID: issue.ID, Limit: 1})
		if err != nil {
			return nil, false
		}
		if len(kids) == 0 {
			offenders = append(offenders, fmt.Sprintf("issue=%s status=%s children=0", issue.ID, issue.Status))
		}
	}
	return offenders, true
}

func decomposedTruncatedResult(n int) CheckResult {
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("too many decomposed issues to check (%d open, cap %d)", n, maxDecomposedScan),
		Detail: fmt.Sprintf("truncated: child lookup skipped to avoid %d queries against fleet-db.\n", n) +
			"remediation: narrow the board (close finished decomposed parents) and re-run `loom doctor`.",
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
	dc, err := cfgpkg.LoadDaemonConfig(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		cfg, _ := cfgpkg.ResolveFleetDBConfig(&cfgpkg.DaemonSettings{})
		return reportFleetDBConfig(cfg)
	}
	cfg, _ := cfgpkg.ResolveFleetDBConfig(&dc.Daemon)
	return reportFleetDBConfig(cfg)
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
