package cli

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// RoleConstraints holds the resolved routing constraints from a config.RoleConfig
// merged with any per-agent config.AgentEntry overrides.
type RoleConstraints struct {
	TaskFilter    string   // "needs_plan", "has_design", "any", or "" (defaults to "has_design")
	Backend       string   // resolved backend name
	PathPatterns  []string // not used in routing decisions; carried through for subprocess env var propagation
	Skills        []string // skill labels this role handles
	Labels        []string // issue must carry ALL of these labels; empty = no requirement
	ExcludeLabels []string // reject issue if it carries ANY of these labels (evaluated before Labels)
	MaxPriority   *int     // reject tasks with priority > this value (nil = no cap)
	SourceRepos   []string // resolved source repo IDs for affinity scoring
	ReadOnly      bool     // informational, carried through for downstream use
	AllowedTools  []string // informational, carried through for downstream use
	DeniedTools   []string // informational, carried through for downstream use
}

// HasRoutingConstraints reports whether any constraint would affect task
// selection. When false, callers fall back to the generic availability check.
// Keep in sync when adding fields to RoleConstraints.
func (c RoleConstraints) HasRoutingConstraints(repoLabel string) bool {
	return len(c.Skills) > 0 || c.MaxPriority != nil || c.TaskFilter != "" ||
		repoLabel != "" || len(c.SourceRepos) > 0 ||
		len(c.Labels) > 0 || len(c.ExcludeLabels) > 0
}

// TaskMatch represents the result of matching a single issue against role constraints.
type TaskMatch struct {
	Issue  backend.IssueData
	Score  int    // 0 = rejected, 10 = fallback, 100+ = matched
	Reason string // human-readable explanation of the score
}

// MergeRoleConstraints resolves a RoleConstraints from a config.RoleConfig and optional
// config.AgentEntry overrides. config.AgentEntry.PathPatterns replaces (not appends to)
// config.RoleConfig.PathPatterns when non-nil. config.AgentEntry.Backend overrides config.RoleConfig.Backend
// when non-empty.
func MergeRoleConstraints(rc config.RoleConfig, ae config.AgentEntry) RoleConstraints {
	c := RoleConstraints{
		TaskFilter:    rc.TaskFilter,
		Backend:       rc.Backend,
		PathPatterns:  rc.PathPatterns,
		Skills:        rc.Skills,
		Labels:        rc.Labels,
		ExcludeLabels: rc.ExcludeLabels,
		MaxPriority:   rc.MaxPriority,
		ReadOnly:      rc.ReadOnly,
		AllowedTools:  rc.AllowedTools,
		DeniedTools:   rc.DeniedTools,
	}

	// config.AgentEntry overrides
	if ae.PathPatterns != nil {
		c.PathPatterns = ae.PathPatterns
	}
	if ae.Backend != "" {
		c.Backend = ae.Backend
	}
	// SourceRepos is always agent-specific (resolved by daemon), never role-level
	c.SourceRepos = ae.SourceRepos

	return c
}

// MatchTask scores a single issue against the given constraints.
// Ready issues are pre-filtered by the backend to exclude blocked issues.
// Returns a TaskMatch with Score=0 for rejected issues.
func MatchTask(issue backend.IssueData, constraints RoleConstraints) TaskMatch {
	// Reject epics
	if IsEpic(issue) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "epic"}
	}

	// Reject non-work types (infrastructure records like merge-request, gate, etc.)
	if IsNonWorkType(issue) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "non-work type"}
	}

	// Reject non-open
	if !IsOpen(issue) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "not open"}
	}

	// Apply TaskFilter
	if reason := applyTaskFilter(issue, constraints.TaskFilter); reason != "" {
		return TaskMatch{Issue: issue, Score: 0, Reason: reason}
	}

	// Reject issues carrying an excluded label (evaluated before required labels).
	if label, found := firstMatchingLabel(issue.Labels, constraints.ExcludeLabels); found {
		return TaskMatch{Issue: issue, Score: 0, Reason: fmt.Sprintf("excluded by label %q", label)}
	}

	// Reject issues missing a required label.
	if label, missing := firstMissingLabel(issue.Labels, constraints.Labels); missing {
		return TaskMatch{Issue: issue, Score: 0, Reason: fmt.Sprintf("missing required label %q", label)}
	}

	// Reject if priority exceeds MaxPriority
	if constraints.MaxPriority != nil && issue.Priority > *constraints.MaxPriority {
		return TaskMatch{Issue: issue, Score: 0, Reason: fmt.Sprintf("priority %d exceeds max %d", issue.Priority, *constraints.MaxPriority)}
	}

	// Base score
	score := 100
	var parts []string
	parts = append(parts, "base:100")

	// Skill matching
	skillMatches := countSkillMatches(issue.Labels, constraints.Skills)
	if len(constraints.Skills) > 0 && skillMatches == 0 {
		return TaskMatch{Issue: issue, Score: 10, Reason: "fallback: no skill match"}
	}
	if skillMatches > 0 {
		bonus := skillMatches * 50
		score += bonus
		parts = append(parts, fmt.Sprintf("skills:+%d(%d match)", bonus, skillMatches))
	}

	// Repo affinity scoring
	if len(constraints.SourceRepos) > 0 && issue.SourceRepo != "" {
		if matchesRepo(constraints.SourceRepos, issue.SourceRepo) {
			score += 30
			parts = append(parts, "repo:+30")
		} else {
			return TaskMatch{Issue: issue, Score: 5, Reason: "repo mismatch"}
		}
	}

	// Priority bonus: 20 - (priority * 4), clamped to [0, 20]
	priorityBonus := 20 - (issue.Priority * 4)
	if priorityBonus > 20 {
		priorityBonus = 20
	}
	if priorityBonus < 0 {
		priorityBonus = 0
	}
	score += priorityBonus
	parts = append(parts, fmt.Sprintf("priority:+%d", priorityBonus))

	return TaskMatch{
		Issue:  issue,
		Score:  score,
		Reason: strings.Join(parts, " "),
	}
}

// SelectBestTask picks the highest-scoring task from a list of candidates.
// Returns nil if no candidates pass filters (Score > 0).
// Ties are broken by: higher score > lower priority number > alphabetical ID.
func SelectBestTask(issues []backend.IssueData, constraints RoleConstraints) *TaskMatch {
	var matches []TaskMatch
	for _, issue := range issues {
		m := MatchTask(issue, constraints)
		if m.Score > 0 {
			matches = append(matches, m)
		}
	}

	if len(matches) == 0 {
		return nil
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		// With current scoring, equal scores typically imply equal priorities
		// when skill matches are equal.
		if matches[i].Issue.Priority != matches[j].Issue.Priority {
			return matches[i].Issue.Priority < matches[j].Issue.Priority
		}
		return matches[i].Issue.ID < matches[j].Issue.ID
	})

	return &matches[0]
}

// checkTaskAvailability uses routerCheck if non-nil, otherwise falls back to defaultCheck.
func CheckTaskAvailability(routerCheck, defaultCheck func() (bool, error)) (bool, error) {
	if routerCheck != nil {
		return routerCheck()
	}
	return defaultCheck()
}

// RouterTaskCheckFromEnv builds a router-based task check from daemon env vars.
// Returns nil when no routing env vars are set.
func RouterTaskCheckFromEnv(parentID string) func() (bool, error) {
	return BuildRouterTaskCheck(RoleConfigFromEnv(), AgentEntryFromEnv(), parentID)
}

// RoleConfigFromEnv reconstructs a partial config.RoleConfig from LOOM_ROLE_* environment
// variables set by the daemon's buildCommand.
func RoleConfigFromEnv() config.RoleConfig {
	var rc config.RoleConfig
	if v := os.Getenv("LOOM_ROLE_SKILLS"); v != "" {
		rc.Skills = strings.Split(v, ",")
	}
	if v := os.Getenv("LOOM_ROLE_LABELS"); v != "" {
		rc.Labels = splitLabelCSV(v)
	}
	if v := os.Getenv("LOOM_ROLE_EXCLUDE_LABELS"); v != "" {
		rc.ExcludeLabels = splitLabelCSV(v)
	}
	if v := os.Getenv("LOOM_ROLE_PATH_PATTERNS"); v != "" {
		rc.PathPatterns = strings.Split(v, ",")
	}
	if v := os.Getenv("LOOM_ROLE_MAX_PRIORITY"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			rc.MaxPriority = &p
		} else {
			log.Printf("[router] Warning: invalid LOOM_ROLE_MAX_PRIORITY %q: %v", v, err)
		}
	}
	if v := os.Getenv("LOOM_ROLE_TASK_FILTER"); v != "" {
		rc.TaskFilter = v
	}
	return rc
}

// AgentEntryFromEnv reconstructs a partial config.AgentEntry from LOOM_AGENT_*
// and LOOM_ROLE environment variables set by the daemon's buildCommand.
func AgentEntryFromEnv() config.AgentEntry {
	var ae config.AgentEntry
	if v := os.Getenv("LOOM_AGENT_PATH_PATTERNS"); v != "" {
		ae.PathPatterns = strings.Split(v, ",")
	}
	if v := os.Getenv("LOOM_AGENT_NAME"); v != "" {
		ae.Worktree = v
	}
	if v := os.Getenv("LOOM_ROLE"); v != "" {
		ae.Role = v
	}
	if v := os.Getenv("LOOM_AGENT_REPO"); v != "" {
		ae.Repo = v
	}
	if v := os.Getenv("LOOM_SOURCE_REPOS"); v != "" {
		ae.SourceRepos = strings.Split(v, ",")
	}
	return ae
}

// TaskFilterAliases maps every accepted spelling of a task filter onto its
// canonical form.
//
// Two vocabularies grew independently: `loom agent --task-filter` documents and
// validates "needs_design" (see mapTaskFilter), while the daemon's router only
// ever matched "needs_plan". An agentdef created the documented way therefore
// fell through to the default branch below and was treated as "has_design", so a
// planner silently ran as a second worker. Both spellings are accepted here, and
// ValidateTaskFilter rejects anything else at input time.
var TaskFilterAliases = map[string]string{
	"needs_design": "needs_plan",
	"needs_plan":   "needs_plan",
	"has_design":   "has_design",
	"any":          "any",
	"":             "",
}

// ValidateTaskFilter returns the canonical spelling of filter, or an error
// naming the accepted values. Callers that persist a filter should run it
// through this so an unrecognized value fails loudly instead of degrading into
// "has_design" at dispatch time.
func ValidateTaskFilter(filter string) (string, error) {
	canonical, ok := TaskFilterAliases[filter]
	if !ok {
		return "", fmt.Errorf("invalid task filter: %s (must be needs_design, has_design, or any)", filter)
	}
	return canonical, nil
}

// warnedTaskFilters records the unrecognized filter values already reported by
// warnUnknownTaskFilterOnce.
var warnedTaskFilters sync.Map

// warnUnknownTaskFilterOnce logs the unrecognized-filter warning at most once
// per distinct value per process. applyTaskFilter runs once per ready issue per
// scoring pass (SelectBestTask fetches up to 10k) on every claim tick and
// availability poll, so an unguarded log.Printf turns one bad stored value into
// thousands of identical lines a minute.
func warnUnknownTaskFilterOnce(filter string) {
	if _, seen := warnedTaskFilters.LoadOrStore(filter, struct{}{}); seen {
		return
	}
	log.Printf("warning: unrecognized task filter %q; treating as has_design", filter)
}

// applyTaskFilter checks if the issue passes the given task filter.
// Returns an empty string if the issue passes, or a rejection reason.
func applyTaskFilter(issue backend.IssueData, filter string) string {
	if filter == "" {
		filter = "has_design"
	}
	if canonical, ok := TaskFilterAliases[filter]; ok && canonical != "" {
		filter = canonical
	}
	switch filter {
	case "needs_plan":
		if !NeedsPlan(issue) {
			return "filter: has design (needs_plan filter)"
		}
	case "has_design":
		if !ReadyToImplement(issue) {
			return "filter: not ready to implement"
		}
	case "any":
		// No additional filter
	default:
		// Reachable only for a filter that bypassed ValidateTaskFilter (a
		// hand-edited config, or one stored before validation existed). Say so
		// rather than quietly behaving as has_design.
		warnUnknownTaskFilterOnce(filter)
		if !ReadyToImplement(issue) {
			return "filter: not ready to implement"
		}
	}
	return ""
}

// matchesRepo returns true if repo appears in the repos list.
func matchesRepo(repos []string, repo string) bool {
	for _, r := range repos {
		if r == repo {
			return true
		}
	}
	return false
}

// countSkillMatches returns the number of role skills that appear in the issue's labels.
func countSkillMatches(labels []string, skills []string) int {
	if len(skills) == 0 || len(labels) == 0 {
		return 0
	}
	labelSet := make(map[string]bool, len(labels))
	for _, l := range labels {
		labelSet[l] = true
	}
	count := 0
	for _, s := range skills {
		if labelSet[s] {
			count++
		}
	}
	return count
}

// firstMatchingLabel returns the first entry of want that appears in labels.
func firstMatchingLabel(labels, want []string) (string, bool) {
	if len(want) == 0 || len(labels) == 0 {
		return "", false
	}
	labelSet := make(map[string]bool, len(labels))
	for _, l := range labels {
		labelSet[l] = true
	}
	for _, w := range want {
		if labelSet[w] {
			return w, true
		}
	}
	return "", false
}

// firstMissingLabel returns the first entry of required that is absent from labels.
func firstMissingLabel(labels, required []string) (string, bool) {
	if len(required) == 0 {
		return "", false
	}
	labelSet := make(map[string]bool, len(labels))
	for _, l := range labels {
		labelSet[l] = true
	}
	for _, r := range required {
		if !labelSet[r] {
			return r, true
		}
	}
	return "", false
}

// splitLabelCSV splits a comma-separated env value, trimming whitespace and
// dropping empty elements. Returns nil for an all-empty input.
//
// The separator is not escapable, so a label containing a comma arrives here as
// two labels — see appendRoutingEnv (supervisor/spawn.go) for why that is
// accepted and what it would take to change.
func splitLabelCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// FetchReadyIssues fetches issues ready for work.
func FetchReadyIssues(parentID string, repoLabel string) ([]backend.IssueData, error) {
	ib := DefaultIssueBackend()
	// Limit 10000: ready queues include open + review + in_progress, and review items
	// can crowd out the few truly-workable open tasks past a small cutoff,
	// causing agents to starve. See monitor_collect.go for the same pattern.
	opts := backend.ReadyOpts{Limit: 10000, ParentID: parentID}
	if repoLabel != "" {
		opts.Labels = []string{"repo:" + repoLabel}
	}
	if sourceRepos := os.Getenv("LOOM_SOURCE_REPOS"); sourceRepos != "" {
		opts.SourceRepos = strings.Split(sourceRepos, ",")
	}
	issues, err := ib.Ready(cmdstore.RootContext(), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to check ready tasks: %w", err)
	}
	return issues, nil
}

// BuildRouterTaskCheck creates a CustomTaskCheck function that uses the task router's
// SelectBestTask to check for available tasks. Returns nil if no filtering is needed.
func BuildRouterTaskCheck(rc config.RoleConfig, ae config.AgentEntry, parentID string) func() (bool, error) {
	constraints := MergeRoleConstraints(rc, ae)
	repoLabel := ae.Repo
	if !constraints.HasRoutingConstraints(repoLabel) {
		return nil
	}
	return func() (bool, error) {
		issues, err := FetchReadyIssues(parentID, repoLabel)
		if err != nil {
			return false, err
		}
		match := SelectBestTask(issues, constraints)
		return match != nil, nil
	}
}
