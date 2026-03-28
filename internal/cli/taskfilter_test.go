package cli

import "testing"

// --- Level 1: Simple predicates ---

func TestIsEpic(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{"epic", "epic", true},
		{"task", "task", false},
		{"bug", "bug", false},
		{"feature", "feature", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{IssueType: tt.typ}
			if got := IsEpic(issue); got != tt.want {
				t.Errorf("IsEpic() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOpen(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"open", "open", true},
		{"in_progress", "in_progress", false},
		{"review", "review", false},
		{"closed", "closed", false},
		{"blocked", "blocked", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Status: tt.status}
			if got := IsOpen(issue); got != tt.want {
				t.Errorf("IsOpen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasDesign(t *testing.T) {
	tests := []struct {
		name   string
		design string
		want   bool
	}{
		{"has design", "## Design\nSome plan", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Design: tt.design}
			if got := HasDesign(issue); got != tt.want {
				t.Errorf("HasDesign() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasNeedsRevision(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   bool
	}{
		{"has needs-revision", []string{"needs-revision"}, true},
		{"needs-revision among others", []string{"bug", "needs-revision", "urgent"}, true},
		{"no labels", nil, false},
		{"empty labels", []string{}, false},
		{"other labels only", []string{"bug", "feature"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Labels: tt.labels}
			if got := HasNeedsRevision(issue); got != tt.want {
				t.Errorf("HasNeedsRevision() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNonWorkType(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		// Non-work types (should return true)
		{"merge-request", "merge-request", true},
		{"gate", "gate", true},
		{"molecule", "molecule", true},
		{"message", "message", true},
		{"agent", "agent", true},
		{"role", "role", true},
		{"rig", "rig", true},
		// Work types (should return false)
		{"task", "task", false},
		{"bug", "bug", false},
		{"feature", "feature", false},
		{"epic", "epic", false},
		{"chore", "chore", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{IssueType: tt.typ}
			if got := IsNonWorkType(issue); got != tt.want {
				t.Errorf("IsNonWorkType() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Level 2: Workflow predicates ---

func TestNeedsPlan(t *testing.T) {
	tests := []struct {
		name   string
		design string
		labels []string
		want   bool
	}{
		{"no design, no labels", "", nil, true},
		{"no design, other labels", "", []string{"bug"}, true},
		{"has design, no labels", "plan text", nil, false},
		{"has design, other labels", "plan text", []string{"bug"}, false},
		{"has design, needs-revision", "plan text", []string{"needs-revision"}, true},
		{"no design, needs-revision", "", []string{"needs-revision"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Design: tt.design, Labels: tt.labels}
			if got := NeedsPlan(issue); got != tt.want {
				t.Errorf("NeedsPlan() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadyToImplement(t *testing.T) {
	tests := []struct {
		name   string
		design string
		labels []string
		want   bool
	}{
		{"has design, no labels", "plan text", nil, true},
		{"has design, other labels", "plan text", []string{"bug"}, true},
		{"has design, needs-revision", "plan text", []string{"needs-revision"}, false},
		{"no design, no labels", "", nil, false},
		{"no design, needs-revision", "", []string{"needs-revision"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Design: tt.design, Labels: tt.labels}
			if got := ReadyToImplement(issue); got != tt.want {
				t.Errorf("ReadyToImplement() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsWorkableTask(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		issueType string
		want      bool
	}{
		{"open task", "open", "task", true},
		{"open bug", "open", "bug", true},
		{"open feature", "open", "feature", true},
		{"open epic", "open", "epic", false},
		{"open agent", "open", "agent", false},
		{"open role", "open", "role", false},
		{"open merge-request", "open", "merge-request", false},
		{"open gate", "open", "gate", false},
		{"open molecule", "open", "molecule", false},
		{"open message", "open", "message", false},
		{"open rig", "open", "rig", false},
		{"in_progress task", "in_progress", "task", false},
		{"closed task", "closed", "task", false},
		{"review task", "review", "task", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := BdIssue{Status: tt.status, IssueType: tt.issueType}
			if got := IsWorkableTask(issue); got != tt.want {
				t.Errorf("IsWorkableTask() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Level 3: Agent predicates ---

func TestHasUnclosedBlockers(t *testing.T) {
	tests := []struct {
		name        string
		deps        []Dependency
		unclosedIDs map[string]bool
		want        bool
	}{
		{"nil deps", nil, nil, false},
		{"empty deps", []Dependency{}, nil, false},
		{"only parent-child deps", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true}, false},
		{"blocks dep with unclosed blocker", []Dependency{
			{Type: "blocks", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true}, true},
		{"blocks dep with closed blocker", []Dependency{
			{Type: "blocks", DependsOnID: "B-1"},
		}, map[string]bool{"B-2": true}, false},
		{"blocker in_progress (unclosed)", []Dependency{
			{Type: "blocks", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true, "B-2": true}, true},
		{"mixed deps one unclosed blocker", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
			{Type: "blocks", DependsOnID: "B-2"},
		}, map[string]bool{"B-2": true}, true},
		{"blocks dep resolved but parent-child still open", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
			{Type: "blocks", DependsOnID: "B-2"},
		}, map[string]bool{"B-1": true}, false},
		{"conditional-blocks dep unclosed", []Dependency{
			{Type: "conditional-blocks", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true}, true},
		{"waits-for dep unclosed", []Dependency{
			{Type: "waits-for", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true}, true},
		{"related dep unclosed (non-blocking)", []Dependency{
			{Type: "related", DependsOnID: "B-1"},
		}, map[string]bool{"B-1": true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasUnclosedBlockers(tt.deps, tt.unclosedIDs); got != tt.want {
				t.Errorf("HasUnclosedBlockers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsDirectBlocker(t *testing.T) {
	blocking := []string{"blocks", "conditional-blocks", "waits-for"}
	for _, typ := range blocking {
		if !isDirectBlocker(typ) {
			t.Errorf("isDirectBlocker(%q) = false, want true", typ)
		}
	}
	nonBlocking := []string{"parent-child", "related", "discovered-from", "replies-to",
		"relates-to", "duplicates", "supersedes", "authored-by",
		"assigned-to", "approved-by", "attests", "tracks", "until",
		"caused-by", "validates", "delegated-from", ""}
	for _, typ := range nonBlocking {
		if isDirectBlocker(typ) {
			t.Errorf("isDirectBlocker(%q) = true, want false", typ)
		}
	}
}

func TestIsAvailableForPlanning(t *testing.T) {
	// unclosedIDs represents all issues that are NOT closed
	// (open, in_progress, review, blocked — anything except closed)
	unclosedIDs := map[string]bool{
		"T-1":     true, // open
		"T-2":     true, // open (has design)
		"BLOCKER": true, // open (unclosed blocker)
	}

	tests := []struct {
		name  string
		issue BdIssue
		want  bool
	}{
		{"open task no design", BdIssue{ID: "T-1", Status: "open", IssueType: "task"}, true},
		{"open task with needs-revision", BdIssue{ID: "T-3", Status: "open", IssueType: "task", Design: "plan", Labels: []string{"needs-revision"}}, true},
		{"open task with design (ready to implement)", BdIssue{ID: "T-2", Status: "open", IssueType: "task", Design: "plan"}, false},
		{"epic", BdIssue{ID: "E-1", Status: "open", IssueType: "epic"}, false},
		{"in_progress", BdIssue{ID: "T-4", Status: "in_progress", IssueType: "task"}, false},
		{"blocked by unclosed issue", BdIssue{
			ID: "T-5", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "BLOCKER"}},
		}, false},
		{"blocked by closed issue", BdIssue{
			ID: "T-6", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "RESOLVED"}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAvailableForPlanning(tt.issue, unclosedIDs); got != tt.want {
				t.Errorf("IsAvailableForPlanning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAvailableForImplementation(t *testing.T) {
	unclosedIDs := map[string]bool{
		"T-1":     true,
		"BLOCKER": true,
	}

	tests := []struct {
		name  string
		issue BdIssue
		want  bool
	}{
		{"open task with design", BdIssue{ID: "T-2", Status: "open", IssueType: "task", Design: "plan"}, true},
		{"open task no design", BdIssue{ID: "T-1", Status: "open", IssueType: "task"}, false},
		{"open task with needs-revision", BdIssue{ID: "T-3", Status: "open", IssueType: "task", Design: "plan", Labels: []string{"needs-revision"}}, false},
		{"epic with design", BdIssue{ID: "E-1", Status: "open", IssueType: "epic", Design: "plan"}, false},
		{"blocked with design", BdIssue{
			ID: "T-4", Status: "open", IssueType: "task", Design: "plan",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "BLOCKER"}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAvailableForImplementation(tt.issue, unclosedIDs); got != tt.want {
				t.Errorf("IsAvailableForImplementation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAvailableForAny(t *testing.T) {
	unclosedIDs := map[string]bool{
		"T-1":     true,
		"BLOCKER": true,
	}

	tests := []struct {
		name  string
		issue BdIssue
		want  bool
	}{
		{"open task no design", BdIssue{ID: "T-1", Status: "open", IssueType: "task"}, true},
		{"open task with design", BdIssue{ID: "T-2", Status: "open", IssueType: "task", Design: "plan"}, true},
		{"open task with needs-revision", BdIssue{ID: "T-3", Status: "open", IssueType: "task", Labels: []string{"needs-revision"}}, true},
		{"epic", BdIssue{ID: "E-1", Status: "open", IssueType: "epic"}, false},
		{"in_progress", BdIssue{ID: "T-4", Status: "in_progress", IssueType: "task"}, false},
		{"blocked by unclosed issue", BdIssue{
			ID: "T-5", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "BLOCKER"}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAvailableForAny(tt.issue, unclosedIDs); got != tt.want {
				t.Errorf("IsAvailableForAny() = %v, want %v", got, tt.want)
			}
		})
	}
}
