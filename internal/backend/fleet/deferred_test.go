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
// ready queue outright: `loom data create` drops --source-repo,
// so in practice most issues carry no source repo at all and every
// repo-scoped agent saw an empty queue.

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

// No repo filter at all must leave every issue eligible, scoped or not. This is
// the baseline the repo predicate narrows from; pinning it keeps a future edit
// from turning "no filter" into "match nothing".
func TestIssueDataMatches_NoRepoFilterKeepsEverything(t *testing.T) {
	for _, issue := range []backend.IssueData{
		{ID: "X-1"},
		{ID: "X-2", SourceRepo: "loomcli"},
	} {
		if !issueDataMatches(issue, issueDataFilter{SourceRepos: nil}) {
			t.Errorf("issue %q must match when no repo filter is set", issue.ID)
		}
	}
}

// Unscoped means "not a repo mismatch", not "exempt from filtering". An
// over-broad edit that short-circuits the whole predicate for unscoped issues
// would pass every other test in this file, so pin the interaction directly.
func TestIssueDataMatches_UnscopedIssueStillObeysOtherFilters(t *testing.T) {
	issue := backend.IssueData{ID: "X-1", Labels: []string{"chore"}}
	opts := issueDataFilter{
		SourceRepos: []string{"loomcli"},
		Labels:      []string{"needs-revision"},
	}
	if issueDataMatches(issue, opts) {
		t.Error("unscoped issue must still be dropped by a non-matching label filter")
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
