package daemonwire

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// The webui agent-queue preview must show the same set a claim would see: a
// bound agent's queue contains no other repo's work. Before the queue builder
// resolved source repos, this panel was computed fleet-wide for every agent.
func TestScoreAndSortQueue_BoundAgentExcludesForeignRepos(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "T-foreign", IssueType: "task", Status: "open", Priority: 0, Design: "plan", SourceRepo: "sr-web"},
		{ID: "T-unset", IssueType: "task", Status: "open", Priority: 0, Design: "plan"},
		{ID: "T-own", IssueType: "task", Status: "open", Priority: 3, Design: "plan", SourceRepo: "sr-api"},
	}
	constraints := cli.RoleConstraints{TaskFilter: "has_design", SourceRepos: []string{"sr-api"}}

	entries := scoreAndSortQueue(issues, constraints)

	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly the sr-api issue", entries)
	}
	if entries[0].IssueID != "T-own" {
		t.Errorf("IssueID = %q, want T-own", entries[0].IssueID)
	}
}

// An unbound agent keeps the fleet-wide preview.
func TestScoreAndSortQueue_UnboundAgentSeesEveryRepo(t *testing.T) {
	issues := []backend.IssueData{
		{ID: "T-a", IssueType: "task", Status: "open", Priority: 1, Design: "plan", SourceRepo: "sr-web"},
		{ID: "T-b", IssueType: "task", Status: "open", Priority: 2, Design: "plan", SourceRepo: "sr-api"},
	}

	entries := scoreAndSortQueue(issues, cli.RoleConstraints{TaskFilter: "has_design"})

	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want both issues", entries)
	}
}
