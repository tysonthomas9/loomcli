package doctor

import (
	"context"
	"errors"
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
	"github.com/tysonthomas9/loomcli/internal/webui/operatorid"
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

// actorAccessChecker is the optional capability a backend advertises when it
// can probe whether a given actor is authorized. Detected the same way as
// backend.ClaimReleaser; only the fleet backend implements it.
type actorAccessChecker interface {
	CheckActorAccess(ctx context.Context, actor string) error
	Workspace() string
}

// checkOperatorActorRole reports whether the operator identity the webui
// attributes board writes to actually holds a role in the issue backend.
//
// When it does not, writes still succeed — the backend falls back to the
// process actor — but attribution is silently lost, and before that fallback
// existed the whole board went read-only. This check surfaces the condition
// before an operator discovers it by clicking.
//
// Report-only. Returns an empty CheckResult (skipped) when there is no issue
// backend, when the backend cannot probe actors, when an API key is configured
// (fleet-db then takes the identity from the key and ignores X-Actor entirely,
// so there is nothing to diagnose), or when the probe fails to reach the
// server (the fleet/fleet-db reachability checks already report that).
func checkOperatorActorRole(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}
	checker, ok := deps.IssueBackend.(actorAccessChecker)
	if !ok {
		return CheckResult{}
	}
	if os.Getenv(bootstrap.EnvFleetDBAPIKey) != "" {
		return CheckResult{}
	}

	actor := operatorid.Resolve()
	workspace := checker.Workspace()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := checker.CheckActorAccess(ctx, actor)
	switch {
	case err == nil:
		return CheckResult{
			Name:    "operator_actor_role",
			Status:  StatusPass,
			Summary: fmt.Sprintf("operator actor %q is authorized in workspace %q", actor, workspace),
		}
	case isActorForbidden(err):
		return CheckResult{
			Name:    "operator_actor_role",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("operator actor %q has no role in fleet-db workspace %q", actor, workspace),
			Detail: "board writes still succeed (they fall back to the process actor) but are " +
				"attributed to the process actor, not the operator.\n" +
				"remediation: grant the actor a role in fleet-db (redis: SET " +
				"fleet-db:acl:global-roles:" + actor + " maintainer), or set " +
				operatorid.EnvOperatorActor + " to an actor that already has one.",
		}
	default:
		// Unreachable or otherwise broken: checkFleet / checkFleetDB own
		// that diagnosis. Do not double-report it here.
		return CheckResult{}
	}
}

// isActorForbidden reports whether err is the authorization rejection the
// probe is looking for, as opposed to a transport failure. The fleet backend
// classifies both as KindUnavailable (see internal/backend/fleet/errors.go,
// where 403 keeps that kind deliberately), so the message is what separates
// them.
func isActorForbidden(err error) bool {
	var be *backend.BackendError
	if !errors.As(err, &be) {
		return false
	}
	return strings.Contains(be.Message, "is not authorized in workspace")
}
