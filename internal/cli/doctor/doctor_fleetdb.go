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

// orphanLockGracePeriod is how long an in_progress issue may go unmatched by a
// live daemon claim before it is reported. The daemon state updater ticks every
// ~5s and an agent claims its task mid-session, so a freshly claimed issue is
// briefly absent from the live set. 10 minutes leaves generous headroom.
const orphanLockGracePeriod = 10 * time.Minute

// orphanLockNow is overridden in tests.
var orphanLockNow = time.Now

// checkOrphanedFleetLocks reports in_progress issues that no running
// daemon-managed agent currently names as its task, so the operator can return
// a genuinely abandoned claim to the queue.
//
// It compares issue IDs to the task IDs recorded in the daemon state file.
// Before PUPPET-240 it looked up issue.Assignee (the shared fleet-db actor,
// e.g. "loom") in a map keyed by agent worktree name ("planner", "worker-2"),
// two namespaces that never intersect — so every in_progress issue was reported
// orphaned no matter how healthy the fleet was.
//
// Report-only. Returns an empty CheckResult (skipped) when no IssueBackend is
// configured, when listing fails, when no in_progress issues exist, or when the
// daemon supplies no liveness data at all.
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
	if managed == nil {
		// No liveness signal (state file missing/unparseable, or the daemon
		// PID is dead). checkLoomDaemon and checkDaemonStuck own that failure
		// mode; reporting every in_progress issue as orphaned here would be
		// noise, not information.
		return CheckResult{}
	}

	// Compare issue IDs to issue IDs. A stale-but-present state file (updater
	// goroutine dead) can yield a false PASS here; that is deliberate —
	// checkDaemonStuck owns state-file staleness, and PASS is the safe
	// direction when the alternative invites a destructive recovery.
	live := make(map[string]struct{}, len(managed))
	for _, info := range managed {
		if info.CurrentTaskID != "" {
			live[info.CurrentTaskID] = struct{}{}
		}
	}

	return evaluateOrphanedFleetLocks(issues, live, orphanLockNow())
}

// evaluateOrphanedFleetLocks is the pure decision half of
// checkOrphanedFleetLocks: given the in_progress issues, the set of task IDs
// live agents currently hold, and a clock, it decides PASS or WARN. The grace
// window is orphanLockGracePeriod; tests vary the clock instead.
func evaluateOrphanedFleetLocks(issues []backend.IssueData, live map[string]struct{}, now time.Time) CheckResult {
	const grace = orphanLockGracePeriod
	var orphans []string
	held, recent := 0, 0
	for _, issue := range issues {
		if _, ok := live[issue.ID]; ok {
			held++
			continue
		}
		// A freshly updated claim may not have reached the state file yet.
		if !issue.UpdatedAt.IsZero() && now.Sub(issue.UpdatedAt) < grace {
			recent++
			continue
		}
		orphans = append(orphans, fmt.Sprintf("issue=%s assignee=%s %s (no running agent names it as its task)",
			issue.ID, issue.Assignee, orphanIdleField(issue, now)))
	}

	if len(orphans) == 0 {
		return CheckResult{
			Name:   "orphaned_fleet_locks",
			Status: StatusPass,
			Summary: fmt.Sprintf("no orphaned fleet-db issue locks (%d in_progress: %d held by live agents, %d claimed within %s)",
				len(issues), held, recent, grace),
		}
	}

	sort.Strings(orphans)
	return CheckResult{
		Name:   "orphaned_fleet_locks",
		Status: StatusWarn,
		Summary: fmt.Sprintf("%d of %d in_progress issue(s) not claimed by any running agent",
			len(orphans), len(issues)),
		Detail: strings.Join(orphans, "\n") +
			"\nremediation: confirm with `loom monitor`, then for each issue above run" +
			" `loom data update <id> --status open --assignee=\"\"` to return it to the queue," +
			" or wait for claim TTL expiry.",
	}
}

// orphanIdleField renders the idle term of an orphan line. A zero UpdatedAt
// means the backend gave no activity timestamp, not that the issue has been
// idle since the year zero.
func orphanIdleField(issue backend.IssueData, now time.Time) string {
	if issue.UpdatedAt.IsZero() {
		return "idle=unknown"
	}
	return fmt.Sprintf("idle=%s", now.Sub(issue.UpdatedAt).Truncate(time.Second))
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
