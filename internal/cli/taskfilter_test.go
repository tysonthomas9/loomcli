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

func TestHasOpenBlockersInReadyList(t *testing.T) {
	// Reuse existing TestHasOpenBlockers cases with renamed function
	tests := []struct {
		name      string
		deps      []Dependency
		allIssues []BdIssue
		want      bool
	}{
		{"nil deps", nil, nil, false},
		{"empty deps", []Dependency{}, nil, false},
		{"only parent-child deps", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
		}, []BdIssue{{ID: "B-1"}}, false},
		{"blocks dep with open blocker", []Dependency{
			{Type: "blocks", DependsOnID: "B-1"},
		}, []BdIssue{{ID: "B-1"}}, true},
		{"blocks dep not in ready list (resolved)", []Dependency{
			{Type: "blocks", DependsOnID: "B-1"},
		}, []BdIssue{{ID: "B-2"}}, false},
		{"mixed deps one open blocker", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
			{Type: "blocks", DependsOnID: "B-2"},
		}, []BdIssue{{ID: "B-2"}}, true},
		{"mixed deps resolved blocker", []Dependency{
			{Type: "parent-child", DependsOnID: "B-1"},
			{Type: "blocks", DependsOnID: "B-2"},
		}, []BdIssue{{ID: "B-1"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasOpenBlockersInReadyList(tt.deps, tt.allIssues); got != tt.want {
				t.Errorf("HasOpenBlockersInReadyList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAvailableForPlanning(t *testing.T) {
	allIssues := []BdIssue{
		{ID: "T-1", Status: "open", IssueType: "task"},
		{ID: "T-2", Status: "open", IssueType: "task", Design: "plan"},
		{ID: "BLOCKER", Status: "open", IssueType: "task"},
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
		{"blocked by open issue", BdIssue{
			ID: "T-5", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "BLOCKER"}},
		}, false},
		{"blocked by resolved issue", BdIssue{
			ID: "T-6", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "RESOLVED"}},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAvailableForPlanning(tt.issue, allIssues); got != tt.want {
				t.Errorf("IsAvailableForPlanning() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAvailableForImplementation(t *testing.T) {
	allIssues := []BdIssue{
		{ID: "T-1", Status: "open", IssueType: "task"},
		{ID: "BLOCKER", Status: "open", IssueType: "task"},
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
			if got := IsAvailableForImplementation(tt.issue, allIssues); got != tt.want {
				t.Errorf("IsAvailableForImplementation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAvailableForAny(t *testing.T) {
	allIssues := []BdIssue{
		{ID: "T-1", Status: "open", IssueType: "task"},
		{ID: "BLOCKER", Status: "open", IssueType: "task"},
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
		{"blocked by open issue", BdIssue{
			ID: "T-5", Status: "open", IssueType: "task",
			Dependencies: []Dependency{{Type: "blocks", DependsOnID: "BLOCKER"}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAvailableForAny(tt.issue, allIssues); got != tt.want {
				t.Errorf("IsAvailableForAny() = %v, want %v", got, tt.want)
			}
		})
	}
}
