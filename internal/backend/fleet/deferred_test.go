package fleet

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- source-repo filtering tests ---
//
// Repo-scoped agents send SourceRepos with every repo in their workspace.
// An issue that carries no source repo is unscoped work, not a mismatch
// against that list, so it must stay eligible. Excluding it starved the
// ready queue outright: `loom data create` drops --source-repo (DOGFOOD-8),
// so in practice most issues carry no source repo at all and every
// repo-scoped agent saw an empty queue (DOGFOOD-39).

func TestIssueDataMatches_KeepsIssueWithNoSourceRepo(t *testing.T) {
	issue := backend.IssueData{ID: "X-1"}
	opts := issueDataFilter{SourceRepos: []string{"fleet-db", "loomcli"}}
	if !issueDataMatches(issue, opts) {
		t.Error("issue with no source repo must stay eligible for repo-scoped agents")
	}
}

func TestIssueDataMatches_KeepsIssueInScope(t *testing.T) {
	issue := backend.IssueData{ID: "X-2", SourceRepo: "loomcli"}
	opts := issueDataFilter{SourceRepos: []string{"fleet-db", "loomcli"}}
	if !issueDataMatches(issue, opts) {
		t.Error("issue whose source repo is in the filter must match")
	}
}

func TestIssueDataMatches_DropsIssueOutOfScope(t *testing.T) {
	issue := backend.IssueData{ID: "X-3", SourceRepo: "some-other-repo"}
	opts := issueDataFilter{SourceRepos: []string{"fleet-db", "loomcli"}}
	if issueDataMatches(issue, opts) {
		t.Error("issue whose source repo is outside the filter must not match")
	}
}

func TestFilterIssueData_UnscopedAndInScopeSurviveRepoFilter(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "X-1"},                          // unscoped
		{ID: "X-2", SourceRepo: "loomcli"},   // in scope
		{ID: "X-3", SourceRepo: "unrelated"}, // out of scope
	}
	got := filterIssueData(issues, issueDataFilter{SourceRepos: []string{"loomcli"}})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (unscoped + in-scope)", len(got))
	}
	if got[0].ID != "X-1" || got[1].ID != "X-2" {
		t.Errorf("got %s, %s; want X-1, X-2", got[0].ID, got[1].ID)
	}
}
