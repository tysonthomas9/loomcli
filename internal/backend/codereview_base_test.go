package backend

import (
	"context"
	"errors"
	"testing"
)

// issuesByID answers Get from a fixed set of tasks; nothing else is called.
type issuesByID struct {
	IssueBackend
	issues map[string]*IssueDetailData
}

func (f issuesByID) Get(_ context.Context, id string) (*IssueDetailData, error) {
	if issue, ok := f.issues[id]; ok {
		return issue, nil
	}
	return nil, errors.New("no task " + id)
}

func task(id, status, parent string, labels []string, blockers map[string]string) *IssueDetailData {
	issue := &IssueDetailData{IssueData: IssueData{ID: id, Status: status, Parent: parent, Labels: labels}}
	for blocker, state := range blockers {
		issue.Dependencies = append(issue.Dependencies, DependencyData{IssueID: id, DependsOnID: blocker, Type: "blocks", Status: state})
	}
	return issue
}

// Tyson, 2026-10-09 (option 1): a dependent is built on its blocker's frozen
// revision only when that blocker is its only open blocker, in code review,
// in the dependent's epic. Everything else waits for its blockers to close.
func TestCodeReviewBase(t *testing.T) {
	review := []string{CodeReviewLabel}
	issues := issuesByID{issues: map[string]*IssueDetailData{
		"A":     task("A", "review", "e1", review, nil),
		"P":     task("P", "review", "e1", nil, nil), // plan review, no label
		"O":     task("O", "review", "e2", review, nil),
		"Z":     task("Z", "closed", "e1", nil, nil),
		"B":     task("B", "in_progress", "e1", nil, map[string]string{"A": "review", "Z": "closed"}),
		"two":   task("two", "open", "e1", nil, map[string]string{"A": "review", "P": "review"}),
		"cross": task("cross", "open", "e1", nil, map[string]string{"O": "review"}),
		"none":  task("none", "open", "", nil, map[string]string{"A": "review"}),
		"plan":  task("plan", "open", "e1", nil, map[string]string{"P": "review"}),
		"root":  task("root", "open", "e1", nil, nil),
		"Q":     task("Q", "review", "", review, nil),
		"loose": task("loose", "open", "", nil, map[string]string{"Q": "review"}),
	}}
	issues.issues["B"].Dependencies = append(issues.issues["B"].Dependencies,
		DependencyData{IssueID: "B", DependsOnID: "e1", Type: "parent-child", Status: "open"},
		DependencyData{IssueID: "B", DependsOnID: "P", Type: "related", Status: "review"})
	for _, tc := range []struct {
		task, want string
	}{
		{"B", "A"},    // only open blocker, in code review, same epic; closed and non-blocking links ignored
		{"two", ""},   // a second open blocker
		{"cross", ""}, // blocker in another epic
		{"none", ""},  // dependent in no epic
		{"plan", ""},  // blocker in plan review
		{"root", ""},  // no blocker
		{"loose", ""}, // dependent and blocker both in no epic
	} {
		got, found, err := CodeReviewBase(context.Background(), issues, tc.task)
		if err != nil || got != tc.want || found != (tc.want != "") {
			t.Errorf("CodeReviewBase(%s) = %q, %v, %v; want %q", tc.task, got, found, err, tc.want)
		}
	}
	if _, _, err := CodeReviewBase(context.Background(), issues, "missing"); err == nil {
		t.Fatal("CodeReviewBase of an unreadable task succeeded")
	}
}
