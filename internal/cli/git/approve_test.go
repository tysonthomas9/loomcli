package git

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func TestApproveCmdFollowsApprovedRevision(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) {
		return []publish.ApprovalOutcome{{Change: "change-1", Status: "published", PRNumber: 7, PRURL: "https://github.com/o/r/pull/7"}}, nil
	})
	approveWorkspace, approveLead = "", "lead-1"
	approveResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{
			Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "workspace-1"}},
		}}, nil
	}
	called := false
	approveLocal = func(_ context.Context, workspace, lead, change string, revision int, headSHA string, actor review.Actor) (apply.FollowResult, error) {
		called = true
		if workspace != "workspace-1" || lead != "lead-1" || change != "change-1" || revision != 2 || headSHA != "head-2" ||
			actor.Kind != "human" || actor.ID == "" {
			t.Fatalf("unexpected approval: %s %s %s %d %+v", workspace, lead, change, revision, actor)
		}
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	out := runApproveForTest(t)
	if !called {
		t.Fatal("CLI approval did not follow the revision")
	}
	if !strings.Contains(out, "Approved and opened PR #7 https://github.com/o/r/pull/7") {
		t.Fatalf("approve did not report the opened PR: %q", out)
	}
}

func stubApprovePublish(t *testing.T, publisher func(context.Context, string, string) ([]publish.ApprovalOutcome, error)) {
	t.Helper()
	oldApprove, oldResolver, oldPublish := approveLocal, approveResolver, publishApprovedLocal
	oldWorkspace, oldLead := approveWorkspace, approveLead
	t.Cleanup(func() {
		approveLocal, approveResolver, publishApprovedLocal = oldApprove, oldResolver, oldPublish
		approveWorkspace, approveLead = oldWorkspace, oldLead
	})
	publishApprovedLocal = func(ctx context.Context, workspace, lead string, stacks publish.DeclaredStacks) ([]publish.ApprovalOutcome, error) {
		if stacks == nil {
			t.Fatal("approve did not pass the declared stacks to publish")
		}
		return publisher(ctx, workspace, lead)
	}
	approveWorkspace, approveLead = "", "lead-1"
	approveResolver = func() (*cli.Resolver, error) {
		return &cli.Resolver{Workspace: "workspace", Config: &config.LoomConfig{
			Workspaces: map[string]config.WorkspaceConfig{"workspace": {ID: "workspace-1"}},
		}}, nil
	}
	approveLocal = func(_ context.Context, _, _, change string, _ int, _ string, _ review.Actor) (apply.FollowResult, error) {
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	stubVerdictStore(t, humanEnv())
}

func runApproveForTest(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	approveCmd.SetOut(&out)
	t.Cleanup(func() { approveCmd.SetOut(nil) })
	if err := runApprove(approveCmd, []string{"change-1", "2"}); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestApproveCmdHasNoOnlyFlag(t *testing.T) {
	// D40: there is no Approve only; every approval opens its PR.
	if approveCmd.Flags().Lookup("only") != nil {
		t.Fatal("loom approve still has --only")
	}
}

func TestApproveCmdReportsNoProvider(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) {
		return []publish.ApprovalOutcome{{Change: "change-1", Status: "not_published",
			Reason: publish.NoProviderReason + " (the repository has no origin remote)"}}, nil
	})
	if out := runApproveForTest(t); !strings.Contains(out, "not published: no provider") {
		t.Fatalf("no-provider approval did not say so: %q", out)
	}
}

func TestApproveCmdFailsWhenPublishFails(t *testing.T) {
	stubApprovePublish(t, func(context.Context, string, string) ([]publish.ApprovalOutcome, error) {
		return []publish.ApprovalOutcome{{Change: "change-1", Status: "pending", Reason: "provider down"}}, errors.New("provider down")
	})
	err := runApprove(approveCmd, []string{"change-1", "2"})
	if err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("publish failure was not reported: %v", err)
	}
}
