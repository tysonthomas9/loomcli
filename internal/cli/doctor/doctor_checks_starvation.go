package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

// fleet_starvation answers one question the rest of doctor could not: is there
// ready work that nobody is going to pick up?
//
// The metric per role r is
//
//	Q(r)     ready, unassigned issues this role's filters admit
//	C_int(r) agents in this role whose desired state is not "stopped"  (intent)
//	C_act(r) those of C_int that are actually alive                    (reality)
//	starved  Q >= 1 && C_int > 0 && C_act == 0
//
// C_int > 0 is the whole point. It separates capacity that is *intentionally*
// zero (an agent parked with desired_state=stopped: nobody meant to serve that
// role) from capacity that is *accidentally* zero (an agent that died on an
// AuthFailure while work piled up). A check without it fires on every
// deliberately parked agent and gets muted within a day.

// starvationReadyLimit bounds the ready query. fleet-db clamps its own row
// count, so an explicit limit makes the clamp visible instead of silent: when
// the result comes back full, Q is reported as a floor (q_clamped) rather than
// as an exact count. A clamped Q is still >= 1, so it never suppresses an alert.
const starvationReadyLimit = 1000

// starvationReadyTimeout bounds the one backend call this check makes. doctor
// runs its checks serially, so a hung backend must not hold the whole report.
const starvationReadyTimeout = 5 * time.Second

// aliveStatuses are the daemon agent statuses that mean "the supervise loop is
// still there and will pick work up".
//
// Two of these are not obvious and are the reason this set is written out
// rather than expressed as "not failed":
//
//   - "stopped" is the NORMAL IDLE STATE and counts as ALIVE. It is
//     computeAgentStatus's default fall-through (daemon_state.go): no PID, no
//     fatal stop reason, restart budget intact — the supervise goroutine is
//     alive and respawns the moment work appears. Most of the fleet sits at
//     status:"stopped", last_error_class:"NoWork" at any given minute; counting
//     it dead would flag every role on every idle tick.
//   - "blocked" counts as ALIVE. It is set for MaxRetriesBlocked,
//     IssueBackendUnavailable and ProfileInvalid; the first two self-resume on
//     a fixed interval, and ProfileInvalid has its own signal in
//     checkAgentProfiles. An agent blocked *forever* is a dwell problem, which
//     is the ops runner's job, not this one's.
//
// "failed" and "parked" are the dead statuses. An unrecognized status is
// treated as alive: a status this binary has not heard of is not evidence of
// death, and guessing the other way manufactures false alarms.
var aliveStatuses = map[string]bool{
	"running":  true,
	"starting": true,
	"stopped":  true,
	"blocked":  true,
}

// desiredStateStopped is the only desired state that means "no capacity is
// intended here". Note the asymmetry with the absent case below.
const desiredStateStopped = "stopped"

// daemonAgentView is a doctor-local mirror of the fields this check reads from
// daemon-agents.json. It is a mirror rather than a shared type on purpose:
// monitor's DaemonAgentStateEntry lacks desired_state, stop_reason and
// last_error_class, and widening it would couple two consumers with different
// needs to one struct. Unknown JSON fields are ignored, so a state file written
// by a newer daemon still parses.
type daemonAgentView struct {
	Worktree string `json:"worktree"`
	Role     string `json:"role"`
	Status   string `json:"status"`
	// DesiredState is omitempty in the daemon's own writer and is populated
	// ONLY for parked entries. ABSENT THEREFORE MEANS SUPERVISED, i.e. the
	// intent is to run, i.e. the agent counts toward C_int. Reading absent as
	// "stopped" would zero C_int for the entire healthy fleet and make the
	// check incapable of ever firing.
	DesiredState   string `json:"desired_state,omitempty"`
	StopReason     string `json:"stop_reason,omitempty"`
	LastErrorClass string `json:"last_error_class,omitempty"`
}

// daemonStateView is the doctor-local mirror of daemon.DaemonState.
type daemonStateView struct {
	PID    int               `json:"pid"`
	Agents []daemonAgentView `json:"agents"`
}

// roleMetric is the per-role row of the JSON payload.
type roleMetric struct {
	Role string `json:"role"`
	// Kind is the resolved role kind ("worker" or "interactive"). An
	// interactive lane reports a Q it will never serve, so the row has to say
	// why C_int is 0 rather than leaving the reader to guess.
	Kind string `json:"kind,omitempty"`
	Q    int    `json:"q"`
	// QFloor is true when Q is a floor rather than an exact count, because the
	// ready query came back at the row limit.
	QFloor  bool `json:"q_floor,omitempty"`
	CInt    int  `json:"c_int"`
	CAct    int  `json:"c_act"`
	Starved bool `json:"starved"`
	// DeadAgents describes the C_int agents that are not alive, which is what
	// an operator needs to act: which worktree, and why it stopped.
	DeadAgents []string `json:"dead_agents,omitempty"`
}

// configDefect is a finding about the fleet configuration itself, reported
// independently of starvation: a role can be both starved and misconfigured,
// and each is separately actionable.
type configDefect struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
}

// starvationReport is the full machine-readable payload, published on
// CheckResult.Data for an out-of-band ops runner to consume.
type starvationReport struct {
	Roles      []roleMetric `json:"roles"`
	Starved    []string     `json:"starved_roles,omitempty"`
	ReadyCount int          `json:"ready_count"`
	// QClamped is true when the ready query hit its row limit, so every Q in
	// this report is a floor.
	QClamped bool `json:"q_clamped"`
	// Unreachable lists ready, unassigned, non-operator issues that no role
	// carrying a label filter can claim (or whose only matching roles have
	// C_int = 0).
	Unreachable []string `json:"unreachable,omitempty"`
	// UnreachableComputed is false when no role carries a label filter at all.
	// The distinction matters: an unreachable count of 0 means "everything is
	// claimable", whereas a filterless fleet means the question was not asked.
	// Reporting 0 for the second case is a lie a dashboard cannot recover from.
	UnreachableComputed bool           `json:"unreachable_computed"`
	ConfigDefects       []configDefect `json:"config_defects,omitempty"`
}

// fleetStarvationApplies reports whether this workspace runs a loom fleet at
// all, and therefore whether the check is worth registering.
//
// It keys off the daemon config carrying agent entries, NOT off the issue
// backend. The metric reads the daemon config and daemon-agents.json and is
// identical whichever backend serves issues; gating on cli.IsFleetActive() (as
// first written) left the check dead on every workspace not using the `fleet`
// backend — including the one it was written for, which serves issues over the
// `api` backend. A workspace with no daemon agents has no capacity to starve,
// so it gets no row rather than a permanent warning.
func fleetStarvationApplies() bool {
	dc, err := cfgpkg.LoadDaemonConfig(cli.GetWorkspaceRuntimeDir())
	return err == nil && dc != nil && len(dc.Agents) > 0
}

// checkFleetStarvation gathers the three inputs — daemon config (intent),
// daemon-agents.json (reality) and the ready queue (demand) — and hands them to
// the pure computeStarvation. Everything untestable lives here; everything
// interesting lives there.
func checkFleetStarvation(deps *cli.Deps) CheckResult {
	runtimeDir := cli.GetWorkspaceRuntimeDir()

	dc, err := cfgpkg.LoadDaemonConfig(runtimeDir)
	if err != nil || dc == nil {
		// Without intent there is no C_int, and without C_int every starvation
		// verdict would be a guess.
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusWarn,
			Summary: "daemon config unavailable; fleet starvation not computed",
			Detail:  starvationErrDetail(err),
		}
	}

	state, stateErr := loadDaemonStateView(cfgpkg.ResolveDaemonStatePath(runtimeDir))
	if stateErr != nil {
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusWarn,
			Summary: "daemon state unavailable; fleet starvation not computed",
			Detail:  stateErr.Error(),
		}
	}
	if stale, isStale := staleStateResult(state); isStale {
		return stale
	}

	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusWarn,
			Summary: "issue backend unavailable; fleet starvation not computed",
			Detail:  "Q is unknown, and starvation is never asserted from an unknown Q.",
		}
	}

	ready, readyErr := fetchReadyQueue(deps)
	if readyErr != nil {
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusWarn,
			Summary: "ready queue unavailable; fleet starvation not computed",
			Detail:  readyErr.Error(),
		}
	}

	return renderStarvation(computeStarvation(dc, workspaceRepos(), state, ready, starvationReadyLimit))
}

// fetchReadyQueue runs the single backend call this check makes, under a bound
// that keeps a hung backend from holding the whole serial doctor report.
func fetchReadyQueue(deps *cli.Deps) ([]backend.IssueData, error) {
	ctx, cancel := context.WithTimeout(context.Background(), starvationReadyTimeout)
	defer cancel()
	return deps.IssueBackend.Ready(ctx, backend.ReadyOpts{Limit: starvationReadyLimit})
}

// workspaceRepos returns the repo catalog agent affinity resolves against. It
// lives outside the daemon config, and a failure to read it only widens repo
// scope — degrading the metric rather than invalidating it — so the error is
// deliberately swallowed.
func workspaceRepos() []cfgpkg.RepoConfig {
	ws, err := cfgpkg.ResolveActiveWorkspace()
	if err != nil || ws == nil {
		return nil
	}
	return ws.Repos
}

func starvationErrDetail(err error) string {
	if err == nil {
		return "LoadDaemonConfig returned no configuration"
	}
	return err.Error()
}

// loadDaemonStateView reads and parses daemon-agents.json.
func loadDaemonStateView(path string) (*daemonStateView, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the workspace's own .loom/ directory
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var state daemonStateView
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &state, nil
}

// staleStateResult applies the staleness guard and returns the FAIL result when
// the state file no longer describes a live daemon.
//
// It is the same liveness test monitor.LoadDaemonManagedAgents applies, and it
// is a hard stop rather than a warning: daemon-agents.json froze for two hours
// during a disk event and went on describing a fleet that no longer existed. A
// metric computed off a frozen file is worse than no metric, because it reads
// as fresh.
func staleStateResult(state *daemonStateView) (CheckResult, bool) {
	if state == nil {
		return CheckResult{}, false
	}
	if state.PID > 0 && lockfile.IsProcessRunning(state.PID) {
		return CheckResult{}, false
	}
	return CheckResult{
		Name:    "fleet_starvation",
		Status:  StatusFail,
		Summary: fmt.Sprintf("daemon state file is stale (pid %d not running)", state.PID),
		Detail:  "fleet starvation not computed. Remediation: `pm2 restart loom-daemon`, then re-run `loom doctor`.",
	}, true
}

// scopedEntry pairs a config agent entry with the repo scope it resolves to.
// An empty Repos means "any repo"; an empty Parent means "any epic".
type scopedEntry struct {
	Repos  []string
	Parent string
}

// admits reports whether this agent entry's routing would let it claim the
// issue. Both dimensions are permissive when unset, which is how the daemon
// treats them.
func (s scopedEntry) admits(issue backend.IssueData) bool {
	if len(s.Repos) > 0 && issue.SourceRepo != "" {
		found := false
		for _, r := range s.Repos {
			if r == issue.SourceRepo {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if s.Parent != "" && issue.Parent != s.Parent {
		return false
	}
	return true
}

// roleAccumulator gathers, per role, everything the metric needs in one pass
// over the config agent entries.
type roleAccumulator struct {
	cInt       int
	cAct       int
	deadAgents []string
	// scope entries from the agents that count toward C_int, used to decide
	// which issues this role could actually be handed.
	counted []scopedEntry
	// present entries from every agent of this role in the local state file,
	// counted or not. When nothing counts (C_int = 0) these keep Q meaningful:
	// the ci-verifier case must still report the work it is parked on.
	present []scopedEntry
	// interactive marks a role the daemon deliberately never auto-supervises
	// (domain.RoleKindInteractive). Such a lane has no intended auto-capacity,
	// so its agents count toward neither C_int nor C_act — the same treatment a
	// desired_state=stopped park gets, and for the same reason.
	interactive bool
}

// computeStarvation is the whole metric, as a pure function of its three
// inputs. Purity is deliberate — it is what lets every case below be a fixture
// with no daemon, no network and no clock.
//
// It takes two arguments beyond the design's sketch: repos, because repo
// affinity resolves against the workspace catalog rather than the daemon
// config, and limit, because q_clamped is a property of the query that
// produced ready, not of ready itself.
func computeStarvation(
	cfg *cfgpkg.DaemonConfig,
	repos []cfgpkg.RepoConfig,
	state *daemonStateView,
	ready []backend.IssueData,
	limit int,
) starvationReport {
	report := starvationReport{ReadyCount: len(ready)}
	if limit > 0 && len(ready) >= limit {
		report.QClamped = true
	}
	if cfg == nil || state == nil {
		return report
	}

	acc, defects := accumulateRoles(cfg, repos, indexStateByWorktree(state))
	filtered, filterDefects := filteredRoles(cfg)
	defects = append(defects, filterDefects...)

	report.Roles, report.Starved = roleMetrics(cfg, acc, ready, report.QClamped)
	report.UnreachableComputed = len(filtered) > 0
	if report.UnreachableComputed {
		report.Unreachable = unreachableIssues(cfg, acc, filtered, ready)
	}

	sort.Slice(defects, func(i, j int) bool {
		if defects[i].Kind != defects[j].Kind {
			return defects[i].Kind < defects[j].Kind
		}
		return defects[i].Subject < defects[j].Subject
	})
	report.ConfigDefects = defects
	return report
}

// indexStateByWorktree keys the local state file's agents by worktree. Worktree
// names can repeat across nodes, so the first entry wins and later duplicates
// are ignored rather than double-counted.
func indexStateByWorktree(state *daemonStateView) map[string]daemonAgentView {
	byWorktree := make(map[string]daemonAgentView, len(state.Agents))
	for _, a := range state.Agents {
		if a.Worktree == "" {
			continue
		}
		if _, seen := byWorktree[a.Worktree]; !seen {
			byWorktree[a.Worktree] = a
		}
	}
	return byWorktree
}

// accumulateRoles folds the config's agent entries against the local state file
// into per-role capacity counts, collecting configuration defects as it goes.
func accumulateRoles(
	cfg *cfgpkg.DaemonConfig,
	repos []cfgpkg.RepoConfig,
	byWorktree map[string]daemonAgentView,
) (map[string]*roleAccumulator, []configDefect) {
	var defects []configDefect
	acc := make(map[string]*roleAccumulator, len(cfg.Roles))
	roleOf := func(role string) *roleAccumulator {
		if a, ok := acc[role]; ok {
			return a
		}
		a := &roleAccumulator{interactive: roleIsInteractive(cfg.Roles[role], role)}
		acc[role] = a
		return a
	}
	// Every configured role gets a row even with no agents, so a role serving
	// zero agents is visible as C_int = 0 rather than silently absent.
	for role := range cfg.Roles {
		roleOf(role)
	}

	for _, entry := range cfg.Agents {
		if entry.Role == "" {
			continue
		}
		if _, ok := cfg.Roles[entry.Role]; !ok {
			defects = append(defects, configDefect{
				Kind:    "undefined_role",
				Subject: entry.Worktree,
				Detail:  fmt.Sprintf("agent references role %q which has no role config; its include/exclude filters are treated as empty", entry.Role),
			})
		}
		a := roleOf(entry.Role)

		// ResolveNodeID scoping means another node's agents appear in the
		// shared config but never in this node's state file. They are this
		// node's capacity for neither metric, and their absence is worth
		// reporting rather than silently dropping.
		live, present := byWorktree[entry.Worktree]
		if !present {
			defects = append(defects, configDefect{
				Kind:    "agent_absent_from_state",
				Subject: entry.Worktree,
				Detail:  fmt.Sprintf("agent for role %q is configured but absent from the local daemon state file; counted toward neither C_int nor C_act", entry.Role),
			})
			continue
		}
		countAgent(a, entry, live, repos)
	}
	return acc, defects
}

// countAgent applies one live agent to its role's counters.
func countAgent(a *roleAccumulator, entry cfgpkg.AgentEntry, live daemonAgentView, repos []cfgpkg.RepoConfig) {
	scope := scopedEntry{Repos: resolveEntryRepos(entry, repos), Parent: entry.Parent}
	a.present = append(a.present, scope)

	if a.interactive {
		// Deliberately hand-driven: no auto-capacity was ever intended, so this
		// agent is neither C_int nor C_act, and its parked/failed state is not a
		// dead supervised agent. Appending to a.present first is load-bearing:
		// scopeAdmits falls back to present when nothing is counted, which keeps
		// Q scoped to this lane's repos instead of widening to the whole board.
		return
	}

	// The state file's desired_state is authoritative when set (it is what the
	// running daemon believes); the config's value is the fallback for an entry
	// the daemon has not stamped.
	desired := live.DesiredState
	if desired == "" {
		desired = string(entry.DesiredState)
	}
	if desired == desiredStateStopped {
		return // intentionally zero capacity; contributes to neither count
	}

	a.cInt++
	a.counted = append(a.counted, scope)
	if aliveStatuses[live.Status] || live.Status == "" {
		a.cAct++
		return
	}
	a.deadAgents = append(a.deadAgents, describeDeadAgent(entry.Worktree, live))
}

// filteredRoles splits the configured roles into R_filtered — those carrying a
// label filter — and a defect per role carrying none.
//
// A role with neither include nor exclude labels matches everything, so leaving
// it in R_filtered would make all work look reachable and mask the unreachable
// metric entirely. It is dropped from the reachability question AND separately
// reported, because the two findings are independently actionable.
func filteredRoles(cfg *cfgpkg.DaemonConfig) ([]string, []configDefect) {
	var defects []configDefect
	filtered := make([]string, 0, len(cfg.Roles))
	for role, rc := range cfg.Roles {
		if roleIsInteractive(rc, role) {
			// Never a claimant, so it can neither make work reachable nor be
			// faulted for carrying no filter: "it will claim tickets meant for
			// other roles" is false for a lane that is never auto-supervised.
			continue
		}
		if len(rc.Labels) > 0 || len(rc.ExcludeLabels) > 0 {
			filtered = append(filtered, role)
			continue
		}
		defects = append(defects, configDefect{
			Kind:    "filterless_role",
			Subject: role,
			Detail:  "role has no label filter (neither labels nor exclude_labels); it will claim tickets meant for other roles",
		})
	}
	sort.Strings(filtered)
	return filtered, defects
}

// roleIsInteractive answers the same question the supervisor asks in
// AgentEntry.ShouldSuperviseWithRoles: is this a hand-driven lane the daemon
// never auto-supervises? The predicate is read from domain rather than respelt
// here, so doctor cannot drift from the supervisor it is measuring.
//
// rc is the zero value for a role with no role config; ResolveRoleKind then
// falls back to the legacy name convention (lead/orchestrator), which is
// exactly what ShouldSupervise does for the same case.
func roleIsInteractive(rc cfgpkg.RoleConfig, roleName string) bool {
	role := &domain.Role{Kind: domain.RoleKind(rc.Kind)}
	return domain.ResolveRoleKind(role, roleName) == domain.RoleKindInteractive
}

// roleMetrics computes Q and the starvation verdict for every role, in a stable
// role order.
func roleMetrics(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	ready []backend.IssueData,
	qClamped bool,
) ([]roleMetric, []string) {
	roles := make([]string, 0, len(acc))
	for role := range acc {
		roles = append(roles, role)
	}
	sort.Strings(roles)

	metrics := make([]roleMetric, 0, len(roles))
	var starved []string
	for _, role := range roles {
		a := acc[role]
		rc := cfg.Roles[role] // zero value for an undefined role: empty filters
		q := 0
		for _, issue := range ready {
			if roleAdmitsIssue(rc, a, issue) {
				q++
			}
		}
		kind := string(domain.RoleKindWorker)
		if a.interactive {
			kind = string(domain.RoleKindInteractive)
		}
		m := roleMetric{
			Role:       role,
			Kind:       kind,
			Q:          q,
			QFloor:     qClamped,
			CInt:       a.cInt,
			CAct:       a.cAct,
			DeadAgents: a.deadAgents,
		}
		// Q >= 1 with intended-but-absent capacity. Q = 0 and C_act = 0 is an
		// idle fleet with nothing to do, which is healthy, and is exactly the
		// case a plain "no agents running" alarm could not tell apart.
		m.Starved = q >= 1 && a.cInt > 0 && a.cAct == 0
		if m.Starved {
			starved = append(starved, role)
		}
		metrics = append(metrics, m)
	}
	return metrics, starved
}

// unreachableIssues lists ready, unassigned, non-operator work that no
// label-filtered role with intended capacity can claim.
func unreachableIssues(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	filtered []string,
	ready []backend.IssueData,
) []string {
	var unreachable []string
	for _, issue := range ready {
		// cli.OperatorLabel marks work deliberately reserved for a human, so it
		// is excluded from the unreachable-work metric: nobody is expected to
		// claim it. It is core vocabulary — fleet-db enforces it server-side —
		// so it is read from the one declaration in internal/cli, never respelt
		// here.
		if issue.Assignee != "" || hasLabel(issue.Labels, cli.OperatorLabel) {
			continue
		}
		reachable := false
		for _, role := range filtered {
			a := acc[role]
			if a == nil || a.cInt == 0 {
				continue
			}
			if roleAdmitsIssue(cfg.Roles[role], a, issue) {
				reachable = true
				break
			}
		}
		if !reachable {
			unreachable = append(unreachable, issue.ID)
		}
	}
	return unreachable
}

// roleAdmitsIssue applies the role's label filters and the repo/epic scope of
// its agents to a single ready issue.
func roleAdmitsIssue(rc cfgpkg.RoleConfig, a *roleAccumulator, issue backend.IssueData) bool {
	if issue.Assignee != "" {
		return false
	}
	if hasAnyLabel(issue.Labels, rc.ExcludeLabels) {
		return false
	}
	// An empty include set is vacuously satisfied: the role requires no labels.
	if !hasAllLabels(issue.Labels, rc.Labels) {
		return false
	}
	return scopeAdmits(a, issue)
}

// scopeAdmits asks whether any of the role's agents could be handed this issue.
//
// The counted (C_int > 0) agents are the primary answer. When none count, the
// role's agents that are merely present in the state file stand in, and when
// there are none of those either the role is unconstrained. That fallback is
// what keeps Q meaningful for a fully parked role: the whole value of reporting
// ci-verifier is saying "parked, with 4 ready tickets waiting", and a Q forced
// to 0 by an empty scope union would say nothing at all.
func scopeAdmits(a *roleAccumulator, issue backend.IssueData) bool {
	entries := a.counted
	if len(entries) == 0 {
		entries = a.present
	}
	if len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		if e.admits(issue) {
			return true
		}
	}
	return false
}

// resolveEntryRepos expands an agent's repo affinity to the repo IDs it may
// claim from. nil means "any repo" — which is also what cross_repo means, and
// what an entry declaring no affinity at all means.
func resolveEntryRepos(entry cfgpkg.AgentEntry, repos []cfgpkg.RepoConfig) []string {
	if entry.CrossRepo {
		return nil
	}
	resolved, err := cfgpkg.ResolveAgentRepos(entry, repos)
	if err != nil {
		// Affinity was declared but resolved to nothing. Widening to "any repo"
		// is the safe direction: it can only make Q larger, and an inflated Q
		// paired with live capacity does not raise an alarm.
		return nil
	}
	if len(resolved) > 0 {
		return resolved
	}
	if entry.Repo != "" {
		return []string{entry.Repo}
	}
	return nil
}

// describeDeadAgent renders why a C_int agent is not alive, which is the line
// an operator acts on.
func describeDeadAgent(worktree string, a daemonAgentView) string {
	parts := []string{fmt.Sprintf("worktree=%s status=%s", worktree, a.Status)}
	if a.StopReason != "" {
		parts = append(parts, "stop_reason="+a.StopReason)
	}
	if a.LastErrorClass != "" {
		parts = append(parts, "last_error_class="+a.LastErrorClass)
	}
	if a.DesiredState != "" {
		parts = append(parts, "desired_state="+a.DesiredState)
	}
	return strings.Join(parts, " ")
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

func hasAnyLabel(labels, want []string) bool {
	for _, w := range want {
		if hasLabel(labels, w) {
			return true
		}
	}
	return false
}

func hasAllLabels(labels, required []string) bool {
	for _, r := range required {
		if !hasLabel(labels, r) {
			return false
		}
	}
	return true
}

// renderStarvation turns the report into a doctor CheckResult.
//
// Starvation is a FAIL, which makes `loom doctor` exit non-zero. That is the
// intent: ready work with intended-but-absent capacity is the fleet not doing
// its job. Unreachable work and config defects are WARN — real, but not the
// fleet being stopped.
func renderStarvation(report starvationReport) CheckResult {
	detail := renderStarvationDetail(report)
	switch {
	case len(report.Starved) > 0:
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusFail,
			Summary: fmt.Sprintf("%d role(s) starved: ready work with no live agent (%s)", len(report.Starved), strings.Join(report.Starved, ", ")),
			Detail:  detail,
			Data:    report,
		}
	case len(report.Unreachable) > 0 || len(report.ConfigDefects) > 0:
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusWarn,
			Summary: fmt.Sprintf("no starved roles; %d unreachable issue(s), %d config defect(s)", len(report.Unreachable), len(report.ConfigDefects)),
			Detail:  detail,
			Data:    report,
		}
	default:
		return CheckResult{
			Name:    "fleet_starvation",
			Status:  StatusPass,
			Summary: fmt.Sprintf("no starved roles across %d role(s)", len(report.Roles)),
			Detail:  detail,
			Data:    report,
		}
	}
}

func renderStarvationDetail(report starvationReport) string {
	var b strings.Builder
	for _, m := range report.Roles {
		q := fmt.Sprintf("%d", m.Q)
		if m.QFloor {
			q = ">=" + q
		}
		fmt.Fprintf(&b, "role=%s kind=%s Q=%s C_int=%d C_act=%d starved=%t\n", m.Role, m.Kind, q, m.CInt, m.CAct, m.Starved)
		for _, d := range m.DeadAgents {
			fmt.Fprintf(&b, "  dead: %s\n", d)
		}
	}
	if report.QClamped {
		fmt.Fprintf(&b, "ready query hit the %d-row limit; every Q above is a floor\n", starvationReadyLimit)
	}
	if report.UnreachableComputed {
		if len(report.Unreachable) > 0 {
			fmt.Fprintf(&b, "unreachable: %s\n", strings.Join(report.Unreachable, ", "))
		} else {
			b.WriteString("unreachable: 0\n")
		}
	} else {
		b.WriteString("unreachable: n/a (no role carries a label filter)\n")
	}
	for _, d := range report.ConfigDefects {
		fmt.Fprintf(&b, "config_defect: %s %s: %s\n", d.Kind, d.Subject, d.Detail)
	}
	if len(report.Starved) > 0 {
		// `loom data agent start` is a proven no-op after an in-memory terminal
		// stop: the supervise goroutine is gone and only a daemon restart
		// recreates it.
		b.WriteString("remediation: restart the supervisor with `pm2 restart loom-daemon` " +
			"(`loom data agent start` does not revive an agent the daemon stopped in-memory), " +
			"then re-run `loom doctor`.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
