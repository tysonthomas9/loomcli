package git

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestApproveCmdFollowsApprovedRevision(t *testing.T) {
	oldApprove, oldResolver := approveLocal, approveResolver
	oldWorkspace, oldLead := approveWorkspace, approveLead
	t.Cleanup(func() {
		approveLocal, approveResolver = oldApprove, oldResolver
		approveWorkspace, approveLead = oldWorkspace, oldLead
	})
	approveWorkspace, approveLead = "", "lead-1"
	approveResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{
			Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "workspace-1"}},
		}}, nil
	}
	called := false
	approveLocal = func(_ context.Context, workspace, lead, change string, revision int, actor review.Actor) (apply.FollowResult, error) {
		called = true
		if workspace != "workspace-1" || lead != "lead-1" || change != "change-1" || revision != 2 ||
			actor.Kind != "human" || actor.ID == "" {
			t.Fatalf("unexpected approval: %s %s %s %d %+v", workspace, lead, change, revision, actor)
		}
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	if err := runApprove(approveCmd, []string{"change-1", "2"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("CLI approval did not follow the revision")
	}
}
