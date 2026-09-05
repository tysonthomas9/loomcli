package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/monitor"
	"github.com/tysonthomas9/loomcli/internal/kv"
	"github.com/tysonthomas9/loomcli/internal/types"
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

// maxDecomposedScan caps how many `decomposed` issues checkDecomposedWithout-
// Children will fan out child queries for. One List per issue is an N+1 against
// fleet-db; past this many the check reports the truncation instead of issuing
// them.
const maxDecomposedScan = 50

const decomposedCheckName = "decomposed_without_children"

// decomposedLabel returns the workspace's own name for the "this issue was
// split into children" label, or "" when the workspace declares none.
//
// It is read from `defaults.labels.decomposed` in the workspace's
// integration.yaml rather than hardcoded, because nothing in loomcli or
// fleet-db ever WRITES that label — a workspace prompt does. The word is
// therefore workspace vocabulary, not core vocabulary like cli.OperatorLabel,
// and a literal here made the check pass forever for any workspace that spells
// the concept differently: a health check that is silently green is worse than
// no check at all.
//
// A var so tests can supply a label without a workspace on disk, the same seam
// unionWorkspacePath uses.
var decomposedLabel = func() string {
	wsPath := unionWorkspacePath()
	if wsPath == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(wsPath, "integration.yaml")) //nolint:gosec // G304 — path is derived from the resolved workspace
	if err != nil {
		return ""
	}
	// Only the one key is decoded: integration.yaml is large and
	// operator-owned, so a stricter view would turn every unrelated addition
	// into a doctor failure.
	var parsed struct {
		Defaults struct {
			Labels struct {
				Decomposed string `yaml:"decomposed"`
			} `yaml:"labels"`
		} `yaml:"defaults"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Defaults.Labels.Decomposed)
}

// decomposedUnconfiguredResult is what the check reports when the workspace
// names no decomposed label. It warns rather than passing: the check did not
// run, and the one failure mode this whole change exists to remove is a check
// that reports health it never measured.
func decomposedUnconfiguredResult() CheckResult {
	return CheckResult{
		Name:    decomposedCheckName,
		Status:  StatusWarn,
		Summary: "decomposed_without_children skipped: no decomposed label configured",
		Detail: "nothing in loom writes the \"this issue was split\" label — a workspace prompt does — " +
			"so this check cannot guess the word.\n" +
			"remediation: set `defaults.labels.decomposed` in <workspace>/integration.yaml to the " +
			"label your decomposer applies, or drop this check from the run list.",
	}
}

// checkDecomposedWithoutChildren warns when a non-terminal issue carrying the
// workspace's decomposed label (see decomposedLabel) has no children at all. A decomposer that creates children
// without `--parent` leaves such a parent behind: the children run and close
// normally while the parent sits in decomposed + blocked forever, because the
// integrator only un-parks a parent "when every child is closed" and a parent
// with no children never reaches that moment. It looks identical to a parent
// legitimately waiting on work, so nothing else surfaces it.
//
// Report-only. Returns an empty CheckResult (skipped) when no IssueBackend is
// configured, when listing fails, or when no decomposed issues exist, and a
// visible skipped-warning when the workspace configures no label.
func checkDecomposedWithoutChildren(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}

	label := decomposedLabel()
	if label == "" {
		return decomposedUnconfiguredResult()
	}
	return decomposedCheck(deps, label)
}

// decomposedCheck is the check proper, with the label already resolved. It is
// split out so tests can exercise the scan without standing up a workspace.
func decomposedCheck(deps *cli.Deps, label string) CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	issues, err := deps.IssueBackend.List(ctx, backend.ListOpts{Labels: []string{label}})
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
