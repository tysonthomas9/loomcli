package doctor

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// fleet_reachability answers a question fleet_starvation structurally cannot:
// is any ready issue claimable by nobody at all?
//
// The two checks read the same three inputs and differ in their subject.
// fleet_starvation measures ROLES — Q >= 1 with intended-but-absent capacity —
// so when the only queued work matches a role whose every agent is parked,
// every *enabled* role has Q = 0 and starvation passes. On 2026-09-07 that is
// exactly what happened: the check printed "28 unreachable issue(s)" inside an
// `ok` line all night while nothing moved. This check measures ISSUES, and it
// fails on its own, with its own name and its own exit contribution, so the
// same fleet cannot read healthy again.
//
// It enforces PUPPET-464's second acceptance clause: no open task may carry a
// label that no live role accepts.

// reachabilityCheckName is the check's name in the report and in --json.
const reachabilityCheckName = "fleet_reachability"

// reachabilityReadyLimit bounds the ready query. It is loomcli's own limit and
// the ONLY bound on this queue: fleet-db's maxLimit = 200 applies to issue
// search and parseListOpts, not to /ready, which parses its own limit with no
// cap. So a queue of 200 — or 999 — is an exact count and must be printed as
// one; only a result that comes back at exactly this many rows is a floor.
const reachabilityReadyLimit = 1000

// reachabilityReadyTimeout bounds the one backend call this check makes.
// doctor runs its checks serially, so a hung backend must not hold the report.
const reachabilityReadyTimeout = 5 * time.Second

// operatorStaleAge is when a human queue stops being normal. Work reserved for
// a human with the `operator` label is expected to wait: a 20-minute-old
// operator ticket is the system working. A ticket that has waited a day is
// PUPPET-464 happening again — nobody looked. It warns and never fails:
// nothing is broken, a human is slow, and a FAIL here would train operators to
// ignore the line that reports genuinely unclaimable work.
const operatorStaleAge = 24 * time.Hour

// reachabilitySampleIDs is how many issue IDs each group names. The label is
// the actionable output; the IDs are there to make it checkable.
const reachabilitySampleIDs = 3

// noLabelsKey is the group for an issue carrying no labels that is still
// unreachable, because every enabled role requires a label it lacks.
const noLabelsKey = "(no labels)"

// orphanGroup is one culprit key — a label, an AND-combination of labels, or
// the no-labels sentinel — with the work stranded behind it.
type orphanGroup struct {
	Key    string   `json:"key"`
	Labels []string `json:"labels,omitempty"`
	Count  int      `json:"count"`
	// OldestAge is the age of the oldest issue in the group. It is published
	// as both a number (for an ops runner to threshold on) and a string (for a
	// human), never as a raw Duration, whose JSON form is unreadable nanoseconds.
	OldestAge        time.Duration `json:"-"`
	OldestAgeSeconds int           `json:"oldest_age_seconds"`
	OldestAgeHuman   string        `json:"oldest_age"`
	OldestID         string        `json:"oldest_id,omitempty"`
	SampleIDs        []string      `json:"sample_ids,omitempty"`
	// ScopeConstrained is true when the labels are fine for some enabled role
	// and repo/epic affinity is what rejected the issue. Reporting a "label"
	// verdict for a repo-affinity cause sends the operator to the wrong file.
	ScopeConstrained bool `json:"scope_constrained,omitempty"`
}

// humanQueueEntry is one issue deliberately reserved for a human.
type humanQueueEntry struct {
	ID         string `json:"id"`
	Title      string `json:"title,omitempty"`
	AgeSeconds int    `json:"age_seconds"`
	Age        string `json:"age"`
}

// reachabilityReport is the machine-readable payload published on
// CheckResult.Data.
type reachabilityReport struct {
	// Computed is false when the question was not asked at all — degraded
	// inputs, or a fleet in which no role carries a label filter. A count of 0
	// there would be a lie a dashboard cannot recover from.
	Computed  bool `json:"computed"`
	Evaluated int  `json:"evaluated"`
	// Truncated is true only when the ready query came back at exactly
	// reachabilityReadyLimit rows. Truncation can only undercount unreachable
	// work, so it never suppresses a FAIL.
	Truncated         bool              `json:"truncated,omitempty"`
	Unreachable       []string          `json:"unreachable,omitempty"`
	Groups            []orphanGroup     `json:"groups,omitempty"`
	OperatorQueue     []humanQueueEntry `json:"operator_queue,omitempty"`
	OperatorOldestAge time.Duration     `json:"-"`
	OperatorOldest    int               `json:"operator_oldest_age_seconds,omitempty"`
	EnabledRoles      []string          `json:"enabled_roles,omitempty"`
	DisabledRoles     []string          `json:"disabled_roles,omitempty"`
	NotComputedReason string            `json:"not_computed_reason,omitempty"`
}

// checkFleetReachability gathers the same three inputs as fleet_starvation and
// hands them to the pure computeReachability. Everything untestable lives here.
//
// Every degraded input yields WARN, never a verdict. The one deliberate
// difference from starvation: a stale state file warns here rather than
// failing, because fleet_starvation already fails loudly on that exact
// condition and a second failure for one cause only inflates the exit summary.
func checkFleetReachability(deps *cli.Deps) CheckResult {
	runtimeDir := cli.GetWorkspaceRuntimeDir()

	dc, err := cfgpkg.LoadDaemonConfig(runtimeDir)
	if err != nil || dc == nil {
		return notComputed("daemon config unavailable", starvationErrDetail(err))
	}

	state, stateErr := loadDaemonStateView(cfgpkg.ResolveDaemonStatePath(runtimeDir))
	if stateErr != nil {
		return notComputed("daemon state unavailable", stateErr.Error())
	}
	if _, isStale := staleStateResult(state); isStale {
		return notComputed(
			fmt.Sprintf("daemon state file is stale (pid %d not running)", state.PID),
			"a verdict computed from a frozen state file is worse than no verdict; fleet_starvation reports the staleness itself.")
	}

	if deps == nil || deps.IssueBackend == nil {
		return notComputed("issue backend unavailable",
			"the ready queue is unknown, and reachability is never asserted from an unknown queue.")
	}

	ready, readyErr := fetchReadyQueueWith(deps, reachabilityReadyLimit, reachabilityReadyTimeout)
	if readyErr != nil {
		return notComputed("ready queue unavailable", readyErr.Error())
	}

	return renderReachability(computeReachability(dc, workspaceRepos(), state, ready, reachabilityReadyLimit, time.Now()))
}

// notComputed is the one shape every degraded input takes.
func notComputed(reason, detail string) CheckResult {
	return CheckResult{
		Name:    reachabilityCheckName,
		Status:  StatusWarn,
		Summary: reason + "; label reachability not computed",
		Detail:  detail,
		Data:    reachabilityReport{NotComputedReason: reason},
	}
}

// computeReachability is the whole check, as a pure function of its inputs.
// Purity is the point: every case is a fixture with no daemon, no network and
// no clock.
func computeReachability(
	cfg *cfgpkg.DaemonConfig,
	repos []cfgpkg.RepoConfig,
	state *daemonStateView,
	ready []backend.IssueData,
	limit int,
	now time.Time,
) reachabilityReport {
	report := reachabilityReport{}
	if limit > 0 && len(ready) >= limit {
		report.Truncated = true
	}
	if cfg == nil || state == nil {
		report.NotComputedReason = "daemon config or daemon state unavailable"
		return report
	}

	acc, _ := accumulateRoles(cfg, repos, indexStateByWorktree(state))
	filtered, _ := filteredRoles(cfg)
	if len(filtered) == 0 {
		// Not a PASS and not a FAIL: with no role carrying a label filter,
		// every role matches everything and the question was never asked.
		report.NotComputedReason = "no role carries a label filter"
		return report
	}
	report.Computed = true
	report.EnabledRoles, report.DisabledRoles = splitFilteredRoles(filtered, acc)

	members := classifyReady(cfg, acc, ready, now, &report)
	report.Groups = groupOrphans(members, now)
	sortOperatorQueue(report.OperatorQueue)
	report.OperatorOldestAge = oldestOperatorAge(report.OperatorQueue)
	report.OperatorOldest = int(report.OperatorOldestAge / time.Second)
	return report
}

// classifyReady sorts the ready queue into the three buckets the verdict is
// made of — human-reserved, reachable, and stranded — and returns the stranded
// ones grouped by culprit key. It fills the counting fields of report as it
// goes; the report is passed by pointer only so the single pass over the queue
// stays a single pass.
func classifyReady(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	ready []backend.IssueData,
	now time.Time,
	report *reachabilityReport,
) map[string][]orphanMember {
	var members map[string][]orphanMember
	for _, issue := range ready {
		if issue.Assignee != "" {
			// Ready() should not return assigned work; belt and braces.
			continue
		}
		report.Evaluated++
		if hasLabel(issue.Labels, cli.OperatorLabel) {
			// Reserved for a human. Named and aged in its own section — never
			// in the FAIL set, and never silently dropped, which is how the
			// operator queue went unwatched for two days.
			report.OperatorQueue = append(report.OperatorQueue, humanEntry(issue, now))
			continue
		}
		if issueReachable(cfg, acc, report.EnabledRoles, issue) {
			continue
		}
		report.Unreachable = append(report.Unreachable, issue.ID)
		key, labels, scoped := culpritKey(cfg, acc, report.EnabledRoles, issue)
		if members == nil {
			members = make(map[string][]orphanMember)
		}
		members[key] = append(members[key], orphanMember{
			issue:            issue,
			labels:           labels,
			scopeConstrained: scoped,
		})
	}
	return members
}

// splitFilteredRoles partitions the label-filtered roles into those with
// intended capacity and those without.
//
// A role is ENABLED iff acc[role].cInt > 0: at least one of its configured
// agents is present in the local state file and its effective desired_state is
// not "stopped". That is the same intent signal fleet_starvation uses for
// C_int, and it is what makes a parked ci-verifier read as disabled.
func splitFilteredRoles(filtered []string, acc map[string]*roleAccumulator) (enabled, disabled []string) {
	for _, role := range filtered {
		if a := acc[role]; a != nil && a.cInt > 0 {
			enabled = append(enabled, role)
			continue
		}
		disabled = append(disabled, role)
	}
	return enabled, disabled
}

// issueReachable reports whether any enabled, label-filtered role admits the
// issue. It calls the same roleAdmitsIssue fleet_starvation uses: two divergent
// copies of "which role admits which issue" is precisely the class of bug this
// check exists to catch.
func issueReachable(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	roles []string,
	issue backend.IssueData,
) bool {
	for _, role := range roles {
		a := acc[role]
		if a == nil || a.cInt == 0 {
			continue
		}
		if roleAdmitsIssue(cfg.Roles[role], a, issue) {
			return true
		}
	}
	return false
}

// orphanMember is one unreachable issue while it waits to be grouped.
type orphanMember struct {
	issue            backend.IssueData
	labels           []string
	scopeConstrained bool
}

// culpritKey names WHY an issue is unreachable. The per-issue ID is not the
// actionable output; the label is.
//
//  1. If removing a single label L makes the issue reachable, L is a culprit.
//  2. If no single removal helps, the issue's whole (sorted, deduped) label set
//     is an AND-combination nobody serves, and is rendered as the joint key
//     `a+b+c`. For a single label that renders as exactly `delivered` — bare,
//     with no decoration — which is the 2026-09-07 case: removing `delivered`
//     from a delivered-only issue does not make it reachable, because planner
//     requires `needs-plan` and coder requires `approved`, so branch 1 finds
//     nothing and this branch produces the key.
//  3. An issue with no labels at all gets the `(no labels)` sentinel.
func culpritKey(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	roles []string,
	issue backend.IssueData,
) (key string, labels []string, scopeConstrained bool) {
	labels = dedupeSorted(issue.Labels)
	scopeConstrained = labelsAcceptedSomewhere(cfg, acc, roles, issue)
	if len(labels) == 0 {
		return noLabelsKey, nil, scopeConstrained
	}

	var culprits []string
	for _, l := range labels {
		probe := issue
		probe.Labels = without(labels, l)
		if issueReachable(cfg, acc, roles, probe) {
			culprits = append(culprits, l)
		}
	}
	if len(culprits) > 0 {
		return strings.Join(culprits, ", "), culprits, scopeConstrained
	}
	return strings.Join(labels, "+"), labels, scopeConstrained
}

// labelsAcceptedSomewhere reports whether some enabled role's label predicates
// admit the issue. For an issue already known to be unreachable that can only
// mean repo/epic scope rejected it, which is a different remediation from a
// label nobody serves.
func labelsAcceptedSomewhere(
	cfg *cfgpkg.DaemonConfig,
	acc map[string]*roleAccumulator,
	roles []string,
	issue backend.IssueData,
) bool {
	for _, role := range roles {
		a := acc[role]
		if a == nil || a.cInt == 0 {
			continue
		}
		rc := cfg.Roles[role]
		if hasAnyLabel(issue.Labels, rc.ExcludeLabels) {
			continue
		}
		if hasAllLabels(issue.Labels, rc.Labels) {
			return true
		}
	}
	return false
}

// groupOrphans folds the unreachable issues into their culprit groups, in a
// deterministic order: count desc, then oldest age desc, then key asc.
func groupOrphans(members map[string][]orphanMember, now time.Time) []orphanGroup {
	if len(members) == 0 {
		return nil
	}
	groups := make([]orphanGroup, 0, len(members))
	for key, ms := range members {
		g := orphanGroup{Key: key, Count: len(ms), Labels: ms[0].labels, ScopeConstrained: true}
		for _, m := range ms {
			if !m.scopeConstrained {
				g.ScopeConstrained = false
			}
			if len(g.SampleIDs) < reachabilitySampleIDs {
				g.SampleIDs = append(g.SampleIDs, m.issue.ID)
			}
			age, known := issueAge(m.issue, now)
			if known && age > g.OldestAge {
				g.OldestAge = age
				g.OldestID = m.issue.ID
			}
		}
		g.OldestAgeSeconds = int(g.OldestAge / time.Second)
		g.OldestAgeHuman = humanAge(g.OldestAge)
		if g.OldestID == "" {
			// Nothing in the group carried a usable CreatedAt.
			g.OldestAgeHuman = unknownAge
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		if groups[i].OldestAge != groups[j].OldestAge {
			return groups[i].OldestAge > groups[j].OldestAge
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

// humanEntry renders one operator-reserved issue.
func humanEntry(issue backend.IssueData, now time.Time) humanQueueEntry {
	age, known := issueAge(issue, now)
	entry := humanQueueEntry{ID: issue.ID, Title: issue.Title, Age: unknownAge}
	if known {
		entry.AgeSeconds = int(age / time.Second)
		entry.Age = humanAge(age)
	}
	return entry
}

// sortOperatorQueue puts the oldest waiting human work first: that is the row
// an operator acts on.
func sortOperatorQueue(queue []humanQueueEntry) {
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].AgeSeconds != queue[j].AgeSeconds {
			return queue[i].AgeSeconds > queue[j].AgeSeconds
		}
		return queue[i].ID < queue[j].ID
	})
}

func oldestOperatorAge(queue []humanQueueEntry) time.Duration {
	var oldest time.Duration
	for _, e := range queue {
		if d := time.Duration(e.AgeSeconds) * time.Second; d > oldest {
			oldest = d
		}
	}
	return oldest
}

// issueAge is the issue's age against the injected clock. A zero CreatedAt is
// reported as unknown and excluded from every oldest computation, rather than
// rendered as a 56-year age.
func issueAge(issue backend.IssueData, now time.Time) (time.Duration, bool) {
	if issue.CreatedAt.IsZero() {
		return 0, false
	}
	age := now.Sub(issue.CreatedAt)
	if age < 0 {
		age = 0
	}
	return age, true
}

// unknownAge is what an unusable CreatedAt renders as.
const unknownAge = "unknown"

// humanAge renders a duration the way an operator reads one: coarse, one unit,
// no decimals. A local helper rather than a dependency.
func humanAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
}

func dedupeSorted(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func without(labels []string, drop string) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l != drop {
			out = append(out, l)
		}
	}
	return out
}

// renderReachability turns the report into a doctor CheckResult.
//
// Unreachable work is a FAIL, which makes `loom doctor` exit non-zero. That is
// the entire point of this check being separate from fleet_starvation: a fleet
// with no starved roles and unclaimable work must fail on its own line.
func renderReachability(report reachabilityReport) CheckResult {
	if !report.Computed {
		reason := report.NotComputedReason
		if reason == "" {
			reason = "inputs unavailable"
		}
		return CheckResult{
			Name:    reachabilityCheckName,
			Status:  StatusWarn,
			Summary: fmt.Sprintf("label reachability not computed (%s)", reason),
			Detail:  renderReachabilityDetail(report),
			Data:    report,
		}
	}

	detail := renderReachabilityDetail(report)
	human := renderHumanQueueSummary(report)
	if len(report.Groups) > 0 {
		return CheckResult{
			Name:   reachabilityCheckName,
			Status: StatusFail,
			Summary: fmt.Sprintf("%s no enabled role can claim: %s%s",
				countPhrase(len(report.Unreachable), report.Truncated), summariseGroups(report.Groups, report.Truncated), human),
			Detail: detail,
			Data:   report,
		}
	}
	status := StatusPass
	if report.OperatorOldestAge >= operatorStaleAge {
		status = StatusWarn
	}
	return CheckResult{
		Name:    reachabilityCheckName,
		Status:  status,
		Summary: fmt.Sprintf("every one of %d open issue(s) is claimable by an enabled role%s", report.Evaluated, human),
		Detail:  detail,
		Data:    report,
	}
}

// countPhrase renders a count that may be a floor.
func countPhrase(n int, truncated bool) string {
	if truncated {
		return fmt.Sprintf(">=%d issue(s)", n)
	}
	return fmt.Sprintf("%d issue(s)", n)
}

// summariseGroups names the biggest culprit keys on the summary line, which is
// the line that prevents a recurrence — an operator who sees the label knows
// which role to un-park or which ticket to re-label.
func summariseGroups(groups []orphanGroup, truncated bool) string {
	const shown = 3
	parts := make([]string, 0, shown)
	for i, g := range groups {
		if i == shown {
			parts = append(parts, fmt.Sprintf("+%d more", len(groups)-shown))
			break
		}
		count := fmt.Sprintf("%d", g.Count)
		if truncated {
			count = ">=" + count
		}
		parts = append(parts, fmt.Sprintf("%s (%s, oldest %s)", g.Key, count, g.OldestAgeHuman))
	}
	return strings.Join(parts, "; ")
}

// renderHumanQueueSummary keeps operator-reserved work on the summary line even
// when everything else is healthy. Dropping it from the summary is how a
// two-day-old human queue stayed invisible.
func renderHumanQueueSummary(report reachabilityReport) string {
	if len(report.OperatorQueue) == 0 {
		return ""
	}
	return fmt.Sprintf("; %d issue(s) awaiting a human, oldest %s",
		len(report.OperatorQueue), report.OperatorQueue[0].Age)
}

func renderReachabilityDetail(report reachabilityReport) string {
	var b strings.Builder
	if !report.Computed {
		fmt.Fprintf(&b, "not computed: %s\n", report.NotComputedReason)
		if report.NotComputedReason == "no role carries a label filter" {
			b.WriteString("every role matches everything, so the reachability question cannot be asked. " +
				"Remediation: give each role a `labels` or `exclude_labels` filter in the daemon config.\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}

	fmt.Fprintf(&b, "evaluated %d open issue(s); enabled roles: %s\n", report.Evaluated, joinOrNone(report.EnabledRoles))
	if len(report.DisabledRoles) > 0 {
		fmt.Fprintf(&b, "disabled roles (no agent with intended capacity): %s\n", strings.Join(report.DisabledRoles, ", "))
	}
	for _, g := range report.Groups {
		count := fmt.Sprintf("%d issues", g.Count)
		if report.Truncated {
			count = ">=" + count
		}
		cause := ""
		if g.ScopeConstrained {
			cause = ", scope-constrained"
		}
		fmt.Fprintf(&b, "no enabled role claims: %s (%s%s, oldest %s%s)\n",
			g.Key, count, cause, g.OldestAgeHuman, renderSamples(g.SampleIDs))
	}
	renderHumanQueueDetail(&b, report)
	if report.Truncated {
		fmt.Fprintf(&b, "ready query came back at the %d-row limit; every count above is a floor\n", reachabilityReadyLimit)
	}
	if len(report.Groups) > 0 {
		b.WriteString("remediation: enable a role that accepts the label above (`pm2 restart loom-daemon` after un-parking), " +
			"or re-label the work for a role that is live. Then re-run `loom doctor`.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderHumanQueueDetail(b *strings.Builder, report reachabilityReport) {
	if len(report.OperatorQueue) == 0 {
		return
	}
	const shown = 10
	fmt.Fprintf(b, "awaiting a human (label %q): %d issue(s), oldest %s\n",
		cli.OperatorLabel, len(report.OperatorQueue), report.OperatorQueue[0].Age)
	for i, e := range report.OperatorQueue {
		if i == shown {
			fmt.Fprintf(b, "  ... and %d more\n", len(report.OperatorQueue)-shown)
			break
		}
		fmt.Fprintf(b, "  %s (%s)\n", e.ID, e.Age)
	}
	if report.OperatorOldestAge >= operatorStaleAge {
		fmt.Fprintf(b, "the oldest human-reserved issue has waited over %s; nobody is looking at it.\n", humanAge(operatorStaleAge))
	}
}

func renderSamples(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ", e.g. " + strings.Join(ids, ", ")
}

func joinOrNone(roles []string) string {
	if len(roles) == 0 {
		return "(none)"
	}
	return strings.Join(roles, ", ")
}
