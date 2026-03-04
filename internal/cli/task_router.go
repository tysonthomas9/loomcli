package cli

import (
	"fmt"
	"sort"
	"strings"
)

// RoleConstraints holds the resolved routing constraints from a RoleConfig
// merged with any per-agent AgentEntry overrides.
type RoleConstraints struct {
	TaskFilter   string   // "needs_plan", "has_design", "any", or "" (defaults to "has_design")
	Backend      string   // resolved backend name
	PathPatterns []string // carried through for downstream use (not used in scoring)
	Skills       []string // skill labels this role handles
	MaxPriority  *int     // reject tasks with priority > this value (nil = no cap)
	ReadOnly     bool     // informational, carried through for downstream use
	AllowedTools []string // informational, carried through for downstream use
	DeniedTools  []string // informational, carried through for downstream use
}

// TaskMatch represents the result of matching a single issue against role constraints.
type TaskMatch struct {
	Issue  BdIssue
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

	return c
}

// MatchTask scores a single issue against the given constraints.
// unclosedIDs is the set of issue IDs that have NOT been closed (for blocker checks).
// Returns a TaskMatch with Score=0 for rejected issues.
func MatchTask(issue BdIssue, constraints RoleConstraints, unclosedIDs map[string]bool) TaskMatch {
	// Reject epics
	if IsEpic(issue) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "epic"}
	}

	// Reject non-open
	if !IsOpen(issue) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "not open"}
	}

	// Reject blocked
	if HasUnclosedBlockers(issue.Dependencies, unclosedIDs) {
		return TaskMatch{Issue: issue, Score: 0, Reason: "blocked"}
	}

	// Apply TaskFilter
	filter := constraints.TaskFilter
	if filter == "" {
		filter = "has_design"
	}
	switch filter {
	case "needs_plan":
		if !NeedsPlan(issue) {
			return TaskMatch{Issue: issue, Score: 0, Reason: "filter: has design (needs_plan filter)"}
		}
	case "has_design":
		if !ReadyToImplement(issue) {
			return TaskMatch{Issue: issue, Score: 0, Reason: "filter: not ready to implement"}
		}
	case "any":
		// No additional filter — already passed workable + blocker checks
	default:
		// Unknown filter value — treat as "has_design"
		if !ReadyToImplement(issue) {
			return TaskMatch{Issue: issue, Score: 0, Reason: "filter: not ready to implement"}
		}
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
func SelectBestTask(issues []BdIssue, constraints RoleConstraints, unclosedIDs map[string]bool) *TaskMatch {
	var matches []TaskMatch
	for _, issue := range issues {
		m := MatchTask(issue, constraints, unclosedIDs)
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
