package cli

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// RoleConstraints holds the resolved routing constraints from a config.RoleConfig
// merged with any per-agent config.AgentEntry overrides.
type RoleConstraints struct {
	TaskFilter   string   // "needs_plan", "has_design", "any", or "" (defaults to "has_design")
	Backend      string   // resolved backend name
	PathPatterns []string // not used in routing decisions; carried through for subprocess env var propagation
	Skills       []string // skill labels this role handles
	// Labels and ExcludeLabels are ROUTING inputs, not carried-through
	// informational fields: MatchTask rejects on them outright (Score=0).
	// Labels require ALL of the listed labels (AND); ExcludeLabels rejects on
	// ANY (OR) and is evaluated first, so a label in both lists excludes.
	// Comparison is exact and case-sensitive. Empty lists gate nothing.
	Labels        []string
	ExcludeLabels []string
	MaxPriority   *int     // reject tasks with priority > this value (nil = no cap)
	SourceRepos   []string // resolved source repo IDs for affinity scoring
	ReadOnly      bool     // informational, carried through for downstream use
	AllowedTools  []string // informational, carried through for downstream use
	DeniedTools   []string // informational, carried through for downstream use
	// InputPolicy is informational here too — the leaf reads the policy off
	// the env directly at invocation time. Nil means deny every prompt.
	InputPolicy *domain.RoleInputPolicy
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
		InputPolicy:   rc.InputPolicy,
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

	// ExcludeLabels: OR-reject. Evaluated before Labels — if a label appears in
	// both lists, exclusion wins (fleet-db/internal/models/role.go:200-211).
	for _, ex := range constraints.ExcludeLabels {
		if hasLabel(issue.Labels, ex) {
			return TaskMatch{Issue: issue, Score: 0, Reason: "excluded label: " + ex}
		}
	}
	// Labels: AND-require. Empty means no requirement.
	for _, req := range constraints.Labels {
		if !hasLabel(issue.Labels, req) {
			return TaskMatch{Issue: issue, Score: 0, Reason: "missing required label: " + req}
		}
	}

	// Apply TaskFilter
	if reason := applyTaskFilter(issue, constraints.TaskFilter); reason != "" {
		return TaskMatch{Issue: issue, Score: 0, Reason: reason}
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
	if v := os.Getenv("LOOM_ROLE_PATH_PATTERNS"); v != "" {
		rc.PathPatterns = strings.Split(v, ",")
	}
	// The label gate lives in MatchTask, and the in-worker router rebuilds its
	// constraints from here — without these two the gate would only ever be
	// enforced on the daemon's claim path, never inside the worker.
	if v := os.Getenv("LOOM_ROLE_LABELS"); v != "" {
		rc.Labels = strings.Split(v, ",")
	}
	if v := os.Getenv("LOOM_ROLE_EXCLUDE_LABELS"); v != "" {
		rc.ExcludeLabels = strings.Split(v, ",")
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
	// A malformed or truncated policy leaves rc.InputPolicy nil, which is the
	// deny-everything zero value: the agent auto-answers no harness prompt at
	// all. Decoding is deliberately not fatal here — this reconstruction runs
	// inside an already-spawned agent, so erroring out would turn a bad env var
	// into a crash loop, and the fallback is the restrictive one either way.
	if v := os.Getenv("LOOM_ROLE_INPUT_POLICY"); v != "" {
		policy, err := domain.DecodeRoleInputPolicy(v)
		if err != nil {
			log.Printf("[router] Warning: invalid LOOM_ROLE_INPUT_POLICY: %v; denying every harness prompt", err)
		}
		rc.InputPolicy = policy
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

// applyTaskFilter checks if the issue passes the given task filter.
// Returns an empty string if the issue passes, or a rejection reason.
func applyTaskFilter(issue backend.IssueData, filter string) string {
	if filter == "" {
		filter = "has_design"
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

// hasLabel reports whether label appears in labels. The comparison is exact and
// case-sensitive: fleet-db stores role label constraints verbatim and never
// normalises them, so neither does the gate that reads them.
func hasLabel(labels []string, label string) bool {
	for _, l := range labels {
		if l == label {
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
	// A label-gated role has something to filter on even with no skills, no
	// priority cap and no task filter, so the gate has to count here too —
	// otherwise the role falls back to the unfiltered availability check and
	// the gate is silently skipped on this path.
	if len(constraints.Skills) == 0 && len(constraints.Labels) == 0 && len(constraints.ExcludeLabels) == 0 &&
		constraints.MaxPriority == nil && constraints.TaskFilter == "" && repoLabel == "" && len(constraints.SourceRepos) == 0 {
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
