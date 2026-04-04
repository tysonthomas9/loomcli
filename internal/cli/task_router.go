package cli

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// RoleConstraints holds the resolved routing constraints from a RoleConfig
// merged with any per-agent AgentEntry overrides.
type RoleConstraints struct {
	TaskFilter   string   // "needs_plan", "has_design", "any", or "" (defaults to "has_design")
	Backend      string   // resolved backend name
	PathPatterns []string // not used in routing decisions; carried through for subprocess env var propagation
	Skills       []string // skill labels this role handles
	MaxPriority  *int     // reject tasks with priority > this value (nil = no cap)
	SourceRepos  []string // resolved source repo IDs for affinity scoring
	ReadOnly     bool     // informational, carried through for downstream use
	AllowedTools []string // informational, carried through for downstream use
	DeniedTools  []string // informational, carried through for downstream use
}

// TaskMatch represents the result of matching a single issue against role constraints.
type TaskMatch struct {
	Issue  backend.IssueData
	Score  int    // 0 = rejected, 10 = fallback, 100+ = matched
	Reason string // human-readable explanation of the score
}

// MergeRoleConstraints resolves a RoleConstraints from a RoleConfig and optional
// AgentEntry overrides. AgentEntry.PathPatterns replaces (not appends to)
// RoleConfig.PathPatterns when non-nil. AgentEntry.Backend overrides RoleConfig.Backend
// when non-empty.
func MergeRoleConstraints(rc RoleConfig, ae AgentEntry) RoleConstraints {
	c := RoleConstraints{
		TaskFilter:   rc.TaskFilter,
		Backend:      rc.Backend,
		PathPatterns: rc.PathPatterns,
		Skills:       rc.Skills,
		MaxPriority:  rc.MaxPriority,
		ReadOnly:     rc.ReadOnly,
		AllowedTools: rc.AllowedTools,
		DeniedTools:  rc.DeniedTools,
	}

	// AgentEntry overrides
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

	// Reject non-work types (infrastructure beads like merge-request, gate, etc.)
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
		// when skill matches are equal. Retained for forward-compatibility.
		if matches[i].Issue.Priority != matches[j].Issue.Priority {
			return matches[i].Issue.Priority < matches[j].Issue.Priority
		}
		return matches[i].Issue.ID < matches[j].Issue.ID
	})

	return &matches[0]
}

// checkTaskAvailability uses routerCheck if non-nil, otherwise falls back to defaultCheck.
func checkTaskAvailability(routerCheck, defaultCheck func() (bool, error)) (bool, error) {
	if routerCheck != nil {
		return routerCheck()
	}
	return defaultCheck()
}

// RouterTaskCheckFromEnv builds a router-based task check from daemon env vars.
// Returns nil when no routing env vars are set (backward compatible default).
func RouterTaskCheckFromEnv(parentID string) func() (bool, error) {
	return BuildRouterTaskCheck(RoleConfigFromEnv(), AgentEntryFromEnv(), parentID)
}

// RoleConfigFromEnv reconstructs a partial RoleConfig from LOOM_ROLE_* environment
// variables set by the daemon's buildCommand.
func RoleConfigFromEnv() RoleConfig {
	var rc RoleConfig
	if v := os.Getenv("LOOM_ROLE_SKILLS"); v != "" {
		rc.Skills = strings.Split(v, ",")
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

// AgentEntryFromEnv reconstructs a partial AgentEntry from LOOM_AGENT_*
// and LOOM_ROLE environment variables set by the daemon's buildCommand.
func AgentEntryFromEnv() AgentEntry {
	var ae AgentEntry
	if v := os.Getenv("LOOM_AGENT_PATH_PATTERNS"); v != "" {
		ae.PathPatterns = strings.Split(v, ",")
	}
	if v := os.Getenv("BD_ACTOR"); v != "" {
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
