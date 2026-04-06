//go:build ignore

package cli

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- MergeRoleConstraints tests ---

func TestMergeRoleConstraints_AllFieldsCopied(t *testing.T) {
	maxP := 2
	rc := RoleConfig{
		TaskFilter:   "needs_plan",
		Backend:      "claude",
		PathPatterns: []string{"internal/**"},
		Skills:       []string{"go", "routing"},
		MaxPriority:  &maxP,
		ReadOnly:     true,
		AllowedTools: []string{"read"},
		DeniedTools:  []string{"write"},
	}
	ae := AgentEntry{}

	got := MergeRoleConstraints(rc, ae)

	if got.TaskFilter != "needs_plan" {
		t.Errorf("TaskFilter = %q, want %q", got.TaskFilter, "needs_plan")
	}
	if got.Backend != "claude" {
		t.Errorf("Backend = %q, want %q", got.Backend, "claude")
	}
	if len(got.PathPatterns) != 1 || got.PathPatterns[0] != "internal/**" {
		t.Errorf("PathPatterns = %v, want [internal/**]", got.PathPatterns)
	}
	if len(got.Skills) != 2 {
		t.Errorf("Skills = %v, want [go routing]", got.Skills)
	}
	if got.MaxPriority == nil || *got.MaxPriority != 2 {
		t.Errorf("MaxPriority = %v, want 2", got.MaxPriority)
	}
	if !got.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
	if len(got.AllowedTools) != 1 || got.AllowedTools[0] != "read" {
		t.Errorf("AllowedTools = %v, want [read]", got.AllowedTools)
	}
	if len(got.DeniedTools) != 1 || got.DeniedTools[0] != "write" {
		t.Errorf("DeniedTools = %v, want [write]", got.DeniedTools)
	}
}

func TestMergeRoleConstraints_AgentPathPatternsOverride(t *testing.T) {
	rc := RoleConfig{PathPatterns: []string{"internal/**"}}
	ae := AgentEntry{PathPatterns: []string{"cmd/**", "pkg/**"}}

	got := MergeRoleConstraints(rc, ae)

	if len(got.PathPatterns) != 2 || got.PathPatterns[0] != "cmd/**" {
		t.Errorf("PathPatterns = %v, want [cmd/** pkg/**]", got.PathPatterns)
	}
}

func TestMergeRoleConstraints_AgentBackendOverride(t *testing.T) {
	rc := RoleConfig{Backend: "claude"}
	ae := AgentEntry{Backend: "codex"}

	got := MergeRoleConstraints(rc, ae)

	if got.Backend != "codex" {
		t.Errorf("Backend = %q, want %q", got.Backend, "codex")
	}
}

func TestMergeRoleConstraints_EmptyOverrides(t *testing.T) {
	rc := RoleConfig{Backend: "claude", PathPatterns: []string{"src/**"}}
	ae := AgentEntry{}

	got := MergeRoleConstraints(rc, ae)

	if got.Backend != "claude" {
		t.Errorf("Backend = %q, want %q", got.Backend, "claude")
	}
	if len(got.PathPatterns) != 1 || got.PathPatterns[0] != "src/**" {
		t.Errorf("PathPatterns = %v, want [src/**]", got.PathPatterns)
	}
}

func TestMergeRoleConstraints_NilPathPatterns(t *testing.T) {
	rc := RoleConfig{PathPatterns: []string{"internal/**"}}
	ae := AgentEntry{PathPatterns: nil}

	got := MergeRoleConstraints(rc, ae)

	if len(got.PathPatterns) != 1 || got.PathPatterns[0] != "internal/**" {
		t.Errorf("PathPatterns = %v, want [internal/**]", got.PathPatterns)
	}
}

func TestMergeRoleConstraints_EmptyPathPatternsOverride(t *testing.T) {
	// Explicitly empty slice (not nil) should replace
	rc := RoleConfig{PathPatterns: []string{"internal/**"}}
	ae := AgentEntry{PathPatterns: []string{}}

	got := MergeRoleConstraints(rc, ae)

	if len(got.PathPatterns) != 0 {
		t.Errorf("PathPatterns = %v, want []", got.PathPatterns)
	}
}

// --- MatchTask tests ---

func TestMatchTask_BaseScore(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 0, Design: "plan",
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := MatchTask(issue, c)

	// base(100) + priority(20 for P0) = 120
	if got.Score != 120 {
		t.Errorf("Score = %d, want 120", got.Score)
	}
}

func TestMatchTask_RejectEpic(t *testing.T) {
	issue := backend.IssueData{ID: "E-1", Status: "open", IssueType: "epic"}
	c := RoleConstraints{}

	got := MatchTask(issue, c)

	if got.Score != 0 {
		t.Errorf("Score = %d, want 0", got.Score)
	}
	if got.Reason != "epic" {
		t.Errorf("Reason = %q, want %q", got.Reason, "epic")
	}
}

func TestMatchTask_RejectNonWorkType(t *testing.T) {
	nonWorkTypes := []string{"merge-request", "gate", "molecule", "message", "agent", "role", "rig"}
	for _, typ := range nonWorkTypes {
		t.Run(typ, func(t *testing.T) {
			issue := backend.IssueData{ID: "T-1", Status: "open", IssueType: typ}
			c := RoleConstraints{}

			got := MatchTask(issue, c)

			if got.Score != 0 {
				t.Errorf("Score = %d, want 0", got.Score)
			}
			if got.Reason != "non-work type" {
				t.Errorf("Reason = %q, want %q", got.Reason, "non-work type")
			}
		})
	}
}

func TestMatchTask_RejectNonOpen(t *testing.T) {
	issue := backend.IssueData{ID: "T-1", Status: "in_progress", IssueType: "task"}
	c := RoleConstraints{}

	got := MatchTask(issue, c)

	if got.Score != 0 {
		t.Errorf("Score = %d, want 0", got.Score)
	}
	if got.Reason != "not open" {
		t.Errorf("Reason = %q, want %q", got.Reason, "not open")
	}
}

// TestMatchTask_RejectBlocked removed: MatchTask no longer checks blockers.
// Blocker filtering is now handled by the backend's Ready endpoint.

func TestMatchTask_RejectPriorityExceedsMax(t *testing.T) {
	maxP := 1
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
	}
	c := RoleConstraints{TaskFilter: "has_design", MaxPriority: &maxP}

	got := MatchTask(issue, c)

	if got.Score != 0 {
		t.Errorf("Score = %d, want 0", got.Score)
	}
}

func TestMatchTask_MaxPriorityNil(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 4, Design: "plan",
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := MatchTask(issue, c)

	if got.Score == 0 {
		t.Error("Score = 0, want non-zero (nil MaxPriority should accept all)")
	}
}

func TestMatchTask_MaxPriorityZero(t *testing.T) {
	maxP := 0
	c := RoleConstraints{TaskFilter: "has_design", MaxPriority: &maxP}

	// P0 should be accepted
	p0 := backend.IssueData{ID: "T-1", Status: "open", IssueType: "task", Priority: 0, Design: "plan"}
	got := MatchTask(p0, c)
	if got.Score == 0 {
		t.Error("P0 Score = 0, want non-zero")
	}

	// P1 should be rejected
	p1 := backend.IssueData{ID: "T-2", Status: "open", IssueType: "task", Priority: 1, Design: "plan"}
	got = MatchTask(p1, c)
	if got.Score != 0 {
		t.Errorf("P1 Score = %d, want 0", got.Score)
	}
}

func TestMatchTask_SkillMatch(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		Labels: []string{"go", "routing", "daemon"},
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go"},
	}

	got := MatchTask(issue, c)

	// base(100) + skill(50) + priority(12) = 162
	if got.Score != 162 {
		t.Errorf("Score = %d, want 162", got.Score)
	}
}

func TestMatchTask_MultipleSkillMatches(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		Labels: []string{"go", "routing", "daemon"},
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go", "daemon"},
	}

	got := MatchTask(issue, c)

	// base(100) + skills(100) + priority(12) = 212
	if got.Score != 212 {
		t.Errorf("Score = %d, want 212", got.Score)
	}
}

func TestMatchTask_SkillFallback(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		Labels: []string{"frontend", "css"},
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go", "daemon"},
	}

	got := MatchTask(issue, c)

	if got.Score != 10 {
		t.Errorf("Score = %d, want 10 (fallback)", got.Score)
	}
	if got.Reason != "fallback: no skill match" {
		t.Errorf("Reason = %q, want %q", got.Reason, "fallback: no skill match")
	}
}

func TestMatchTask_SkillFallbackNoLabels(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go"},
	}

	got := MatchTask(issue, c)

	if got.Score != 10 {
		t.Errorf("Score = %d, want 10 (fallback for unlabeled issue)", got.Score)
	}
}

func TestMatchTask_NegativePriority(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: -1, Design: "plan",
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := MatchTask(issue, c)

	// Should clamp at P0 bonus (20), not inflate to 24
	if got.Score > 120 {
		t.Errorf("Score = %d, negative priority inflated score above P0 maximum 120", got.Score)
	}
	if got.Score != 120 {
		t.Errorf("Score = %d, want 120 (base 100 + clamped priority 20)", got.Score)
	}
}

func TestMatchTask_NoSkillsNoBonus(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		Labels: []string{"go", "routing"},
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := MatchTask(issue, c)

	// base(100) + priority(12) = 112 — no skill bonus since no skills configured
	if got.Score != 112 {
		t.Errorf("Score = %d, want 112", got.Score)
	}
}

func TestMatchTask_PriorityBonus(t *testing.T) {
	tests := []struct {
		priority  int
		wantBonus int
	}{
		{0, 20},
		{1, 16},
		{2, 12},
		{3, 8},
		{4, 4},
	}

	for _, tt := range tests {
		issue := backend.IssueData{
			ID: "T-1", Status: "open", IssueType: "task",
			Priority: tt.priority, Design: "plan",
		}
		c := RoleConstraints{TaskFilter: "has_design"}

		got := MatchTask(issue, c)

		wantScore := 100 + tt.wantBonus
		if got.Score != wantScore {
			t.Errorf("P%d: Score = %d, want %d (base 100 + priority %d)", tt.priority, got.Score, wantScore, tt.wantBonus)
		}
	}
}

func TestMatchTask_TaskFilterNeedsPlan(t *testing.T) {
	c := RoleConstraints{TaskFilter: "needs_plan"}

	// Issue without design should match
	noDesign := backend.IssueData{ID: "T-1", Status: "open", IssueType: "task"}
	got := MatchTask(noDesign, c)
	if got.Score == 0 {
		t.Error("no-design issue rejected by needs_plan filter, want accepted")
	}

	// Issue with design but needs-revision should match
	revision := backend.IssueData{
		ID: "T-2", Status: "open", IssueType: "task",
		Design: "plan", Labels: []string{"needs-revision"},
	}
	got = MatchTask(revision, c)
	if got.Score == 0 {
		t.Error("needs-revision issue rejected by needs_plan filter, want accepted")
	}

	// Issue with approved design should NOT match
	hasDesign := backend.IssueData{
		ID: "T-3", Status: "open", IssueType: "task",
		Design: "plan",
	}
	got = MatchTask(hasDesign, c)
	if got.Score != 0 {
		t.Errorf("has-design issue passed needs_plan filter, want rejected (Score=%d)", got.Score)
	}
}

func TestMatchTask_TaskFilterHasDesign(t *testing.T) {
	c := RoleConstraints{TaskFilter: "has_design"}

	// Issue with approved design should match
	ready := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Design: "plan",
	}
	got := MatchTask(ready, c)
	if got.Score == 0 {
		t.Error("ready issue rejected by has_design filter, want accepted")
	}

	// Issue without design should NOT match
	noDesign := backend.IssueData{ID: "T-2", Status: "open", IssueType: "task"}
	got = MatchTask(noDesign, c)
	if got.Score != 0 {
		t.Errorf("no-design issue passed has_design filter, want rejected (Score=%d)", got.Score)
	}
}

func TestMatchTask_TaskFilterAny(t *testing.T) {
	c := RoleConstraints{TaskFilter: "any"}

	// Both designed and undesigned should match
	noDesign := backend.IssueData{ID: "T-1", Status: "open", IssueType: "task"}
	got := MatchTask(noDesign, c)
	if got.Score == 0 {
		t.Error("no-design issue rejected by any filter, want accepted")
	}

	hasDesign := backend.IssueData{
		ID: "T-2", Status: "open", IssueType: "task",
		Design: "plan",
	}
	got = MatchTask(hasDesign, c)
	if got.Score == 0 {
		t.Error("has-design issue rejected by any filter, want accepted")
	}
}

func TestMatchTask_TaskFilterDefault(t *testing.T) {
	// Empty TaskFilter defaults to "has_design"
	c := RoleConstraints{}

	ready := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Design: "plan",
	}
	got := MatchTask(ready, c)
	if got.Score == 0 {
		t.Error("ready issue rejected by default filter, want accepted")
	}

	noDesign := backend.IssueData{ID: "T-2", Status: "open", IssueType: "task"}
	got = MatchTask(noDesign, c)
	if got.Score != 0 {
		t.Errorf("no-design issue passed default filter, want rejected (Score=%d)", got.Score)
	}
}

func TestMatchTask_TaskFilterUnknown(t *testing.T) {
	// Unknown filter value falls back to "has_design" behavior
	c := RoleConstraints{TaskFilter: "invalid_filter"}

	ready := backend.IssueData{ID: "T-1", Status: "open", IssueType: "task", Design: "plan"}
	got := MatchTask(ready, c)
	if got.Score == 0 {
		t.Error("ready issue rejected by unknown filter, want has_design fallback")
	}

	noDesign := backend.IssueData{ID: "T-2", Status: "open", IssueType: "task"}
	got = MatchTask(noDesign, c)
	if got.Score != 0 {
		t.Errorf("no-design issue passed unknown filter, want rejected (Score=%d)", got.Score)
	}
}

func TestMatchTask_CombinedScore(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 1, Design: "plan",
		Labels: []string{"go", "daemon", "routing"},
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go", "daemon", "security"},
	}

	got := MatchTask(issue, c)

	// base(100) + skills(2*50=100) + priority(16) = 216
	if got.Score != 216 {
		t.Errorf("Score = %d, want 216", got.Score)
	}
}

// --- SelectBestTask tests ---

func TestSelectBestTask_Empty(t *testing.T) {
	c := RoleConstraints{TaskFilter: "has_design"}

	got := SelectBestTask(nil, c)

	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestSelectBestTask_AllRejected(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "E-1", Status: "open", IssueType: "epic"},
		{ID: "T-1", Status: "closed", IssueType: "task"},
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := SelectBestTask(issues, c)

	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestSelectBestTask_SingleMatch(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "E-1", Status: "open", IssueType: "epic"},
		{ID: "T-1", Status: "open", IssueType: "task", Design: "plan"},
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want match")
	}
	if got.Issue.ID != "T-1" {
		t.Errorf("ID = %q, want %q", got.Issue.ID, "T-1")
	}
}

func TestSelectBestTask_HighestScore(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "T-1", Status: "open", IssueType: "task", Priority: 2, Design: "plan", Labels: []string{"go"}},
		{ID: "T-2", Status: "open", IssueType: "task", Priority: 2, Design: "plan", Labels: []string{"frontend"}},
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go"},
	}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want match")
	}
	if got.Issue.ID != "T-1" {
		t.Errorf("ID = %q, want %q (higher skill match score)", got.Issue.ID, "T-1")
	}
}

func TestSelectBestTask_HigherPriorityWins(t *testing.T) {
	// T-2 wins because P0 produces a higher priority bonus (20) vs P2 (12),
	// giving it a higher overall score.
	issues := []backend.IssueData{
		{ID: "T-1", Status: "open", IssueType: "task", Priority: 2, Design: "plan"},
		{ID: "T-2", Status: "open", IssueType: "task", Priority: 0, Design: "plan"},
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want match")
	}
	if got.Issue.ID != "T-2" {
		t.Errorf("ID = %q, want %q (P0 has higher priority bonus)", got.Issue.ID, "T-2")
	}
}

func TestSelectBestTask_TiebreakByID(t *testing.T) {
	// Same priority, same score — tiebreak by ID
	issues := []backend.IssueData{
		{ID: "T-B", Status: "open", IssueType: "task", Priority: 2, Design: "plan"},
		{ID: "T-A", Status: "open", IssueType: "task", Priority: 2, Design: "plan"},
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want match")
	}
	if got.Issue.ID != "T-A" {
		t.Errorf("ID = %q, want %q (alphabetical tiebreak)", got.Issue.ID, "T-A")
	}
}

func TestSelectBestTask_MixedScores(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "E-1", Status: "open", IssueType: "epic"},                                                                // rejected (epic)
		{ID: "T-1", Status: "open", IssueType: "task", Priority: 2, Design: "plan", Labels: []string{"frontend"}},     // fallback (skill mismatch)
		{ID: "T-2", Status: "open", IssueType: "task", Priority: 1, Design: "plan", Labels: []string{"go"}},           // high score (skill match)
		{ID: "T-3", Status: "open", IssueType: "task", Priority: 0, Design: "plan", Labels: []string{"go", "daemon"}}, // highest score
	}
	c := RoleConstraints{
		TaskFilter: "has_design",
		Skills:     []string{"go", "daemon"},
	}

	got := SelectBestTask(issues, c)

	if got == nil {
		t.Fatal("got nil, want match")
	}
	if got.Issue.ID != "T-3" {
		t.Errorf("ID = %q, want %q (highest combined score)", got.Issue.ID, "T-3")
	}
}

// --- countSkillMatches tests ---

func TestCountSkillMatches(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		skills []string
		want   int
	}{
		{"nil labels", nil, []string{"go"}, 0},
		{"nil skills", []string{"go"}, nil, 0},
		{"both nil", nil, nil, 0},
		{"no match", []string{"frontend"}, []string{"go"}, 0},
		{"one match", []string{"go", "routing"}, []string{"go"}, 1},
		{"two matches", []string{"go", "daemon", "routing"}, []string{"go", "daemon"}, 2},
		{"all match", []string{"go", "daemon"}, []string{"go", "daemon"}, 2},
		{"skill not in labels", []string{"go"}, []string{"go", "security"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countSkillMatches(tt.labels, tt.skills); got != tt.want {
				t.Errorf("countSkillMatches() = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- RoleConfigFromEnv tests ---

func TestRoleConfigFromEnv(t *testing.T) {
	t.Setenv("LOOM_ROLE_SKILLS", "go,daemon")
	t.Setenv("LOOM_ROLE_PATH_PATTERNS", "internal/**,cmd/**")
	t.Setenv("LOOM_ROLE_MAX_PRIORITY", "2")
	t.Setenv("LOOM_ROLE_TASK_FILTER", "needs_plan")

	rc := RoleConfigFromEnv()

	if len(rc.Skills) != 2 || rc.Skills[0] != "go" || rc.Skills[1] != "daemon" {
		t.Errorf("Skills = %v, want [go daemon]", rc.Skills)
	}
	if len(rc.PathPatterns) != 2 || rc.PathPatterns[0] != "internal/**" {
		t.Errorf("PathPatterns = %v, want [internal/** cmd/**]", rc.PathPatterns)
	}
	if rc.MaxPriority == nil || *rc.MaxPriority != 2 {
		t.Errorf("MaxPriority = %v, want 2", rc.MaxPriority)
	}
	if rc.TaskFilter != "needs_plan" {
		t.Errorf("TaskFilter = %q, want %q", rc.TaskFilter, "needs_plan")
	}
}

func TestRoleConfigFromEnv_Empty(t *testing.T) {
	// Ensure no LOOM_ROLE_* env vars are set
	t.Setenv("LOOM_ROLE_SKILLS", "")
	t.Setenv("LOOM_ROLE_PATH_PATTERNS", "")
	t.Setenv("LOOM_ROLE_MAX_PRIORITY", "")
	t.Setenv("LOOM_ROLE_TASK_FILTER", "")

	rc := RoleConfigFromEnv()

	if len(rc.Skills) != 0 {
		t.Errorf("Skills = %v, want empty", rc.Skills)
	}
	if len(rc.PathPatterns) != 0 {
		t.Errorf("PathPatterns = %v, want empty", rc.PathPatterns)
	}
	if rc.MaxPriority != nil {
		t.Errorf("MaxPriority = %v, want nil", rc.MaxPriority)
	}
	if rc.TaskFilter != "" {
		t.Errorf("TaskFilter = %q, want empty", rc.TaskFilter)
	}
}

func TestRoleConfigFromEnv_InvalidMaxPriority(t *testing.T) {
	t.Setenv("LOOM_ROLE_MAX_PRIORITY", "notanumber")

	rc := RoleConfigFromEnv()

	if rc.MaxPriority != nil {
		t.Errorf("MaxPriority = %v, want nil for invalid input", rc.MaxPriority)
	}
}

func TestAgentEntryFromEnv(t *testing.T) {
	t.Setenv("LOOM_AGENT_PATH_PATTERNS", "cmd/**,pkg/**")
	t.Setenv("BD_ACTOR", "falcon")
	t.Setenv("LOOM_ROLE", "task")

	ae := AgentEntryFromEnv()

	if len(ae.PathPatterns) != 2 || ae.PathPatterns[0] != "cmd/**" {
		t.Errorf("PathPatterns = %v, want [cmd/** pkg/**]", ae.PathPatterns)
	}
	if ae.Worktree != "falcon" {
		t.Errorf("Worktree = %q, want %q", ae.Worktree, "falcon")
	}
	if ae.Role != "task" {
		t.Errorf("Role = %q, want %q", ae.Role, "task")
	}
}

func TestAgentEntryFromEnv_Empty(t *testing.T) {
	t.Setenv("LOOM_AGENT_PATH_PATTERNS", "")
	t.Setenv("BD_ACTOR", "")
	t.Setenv("LOOM_ROLE", "")
	t.Setenv("LOOM_AGENT_REPO", "")

	ae := AgentEntryFromEnv()

	if len(ae.PathPatterns) != 0 {
		t.Errorf("PathPatterns = %v, want empty", ae.PathPatterns)
	}
	if ae.Worktree != "" {
		t.Errorf("Worktree = %q, want empty", ae.Worktree)
	}
	if ae.Role != "" {
		t.Errorf("Role = %q, want empty", ae.Role)
	}
}

func TestAgentEntryFromEnv_WithLOOM_AGENT_REPO(t *testing.T) {
	t.Setenv("LOOM_AGENT_PATH_PATTERNS", "")
	t.Setenv("BD_ACTOR", "")
	t.Setenv("LOOM_ROLE", "")
	t.Setenv("LOOM_AGENT_REPO", "myrepo")

	ae := AgentEntryFromEnv()

	if ae.Repo != "myrepo" {
		t.Errorf("Repo = %q, want %q", ae.Repo, "myrepo")
	}
}

// TestBuildRouterTaskCheck_RepoOnlyConstraint verifies non-nil returned when only Repo is set.
func TestBuildRouterTaskCheck_RepoOnlyConstraint(t *testing.T) {
	rc := RoleConfig{Description: "frontend repo agent"}
	ae := AgentEntry{Worktree: "falcon", Role: "task", Repo: "frontend"}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil when AgentEntry.Repo is set")
	}
}

// TestBuildRouterTaskCheck_NoConstraints verifies nil returned for empty RoleConfig and empty AgentEntry.
func TestBuildRouterTaskCheck_NoConstraints(t *testing.T) {
	rc := RoleConfig{}
	ae := AgentEntry{}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check != nil {
		t.Error("BuildRouterTaskCheck() should return nil for completely empty RoleConfig and AgentEntry")
	}
}

// --- Repo affinity tests ---

func TestMatchTask_RepoAffinityMatch(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		SourceRepo: "repo-a",
	}
	c := RoleConstraints{
		TaskFilter:  "has_design",
		SourceRepos: []string{"repo-a"},
	}

	got := MatchTask(issue, c)

	// base(100) + repo(30) + priority(12) = 142
	if got.Score != 142 {
		t.Errorf("Score = %d, want 142", got.Score)
	}
}

func TestMatchTask_RepoAffinityMismatch(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		SourceRepo: "repo-b",
	}
	c := RoleConstraints{
		TaskFilter:  "has_design",
		SourceRepos: []string{"repo-a"},
	}

	got := MatchTask(issue, c)

	if got.Score != 5 {
		t.Errorf("Score = %d, want 5 (repo mismatch)", got.Score)
	}
	if got.Reason != "repo mismatch" {
		t.Errorf("Reason = %q, want %q", got.Reason, "repo mismatch")
	}
}

func TestMatchTask_RepoAffinityNoConstraint(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		SourceRepo: "repo-a",
	}
	c := RoleConstraints{TaskFilter: "has_design"}

	got := MatchTask(issue, c)

	// base(100) + priority(12) = 112 — no repo penalty when agent has no SourceRepos
	if got.Score != 112 {
		t.Errorf("Score = %d, want 112 (no repo constraint, normal scoring)", got.Score)
	}
}

func TestMatchTask_RepoAffinityEmptyIssueRepo(t *testing.T) {
	issue := backend.IssueData{
		ID: "T-1", Status: "open", IssueType: "task",
		Priority: 2, Design: "plan",
		SourceRepo: "",
	}
	c := RoleConstraints{
		TaskFilter:  "has_design",
		SourceRepos: []string{"repo-a"},
	}

	got := MatchTask(issue, c)

	// base(100) + priority(12) = 112 — repo-neutral issue, no penalty
	if got.Score != 112 {
		t.Errorf("Score = %d, want 112 (repo-neutral issue, no penalty)", got.Score)
	}
}

func TestMergeRoleConstraints_SourceReposPropagated(t *testing.T) {
	rc := RoleConfig{
		TaskFilter: "has_design",
		Backend:    "claude",
	}
	ae := AgentEntry{
		SourceRepos: []string{"backend", "frontend"},
	}

	got := MergeRoleConstraints(rc, ae)

	if len(got.SourceRepos) != 2 {
		t.Fatalf("SourceRepos len = %d, want 2", len(got.SourceRepos))
	}
	if got.SourceRepos[0] != "backend" || got.SourceRepos[1] != "frontend" {
		t.Errorf("SourceRepos = %v, want [backend frontend]", got.SourceRepos)
	}
}

func TestAgentEntryFromEnv_SourceRepos(t *testing.T) {
	t.Setenv("LOOM_SOURCE_REPOS", "repo-a,repo-b")
	t.Setenv("LOOM_AGENT_PATH_PATTERNS", "")
	t.Setenv("BD_ACTOR", "")
	t.Setenv("LOOM_ROLE", "")
	t.Setenv("LOOM_AGENT_REPO", "")

	ae := AgentEntryFromEnv()

	if len(ae.SourceRepos) != 2 {
		t.Fatalf("SourceRepos len = %d, want 2", len(ae.SourceRepos))
	}
	if ae.SourceRepos[0] != "repo-a" || ae.SourceRepos[1] != "repo-b" {
		t.Errorf("SourceRepos = %v, want [repo-a repo-b]", ae.SourceRepos)
	}
}

func TestBuildRouterTaskCheck_SourceReposOnlyConstraint(t *testing.T) {
	rc := RoleConfig{}
	ae := AgentEntry{SourceRepos: []string{"repo-a"}}

	check := BuildRouterTaskCheck(rc, ae, "")
	if check == nil {
		t.Error("BuildRouterTaskCheck() should return non-nil when AgentEntry.SourceRepos is set")
	}
}
