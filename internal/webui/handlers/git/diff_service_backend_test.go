package git

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type issueLookupBackend struct {
	workitems.API
	issue *workitems.IssueDetail
	err   error
}

func (b *issueLookupBackend) Get(_ context.Context, _ workitems.GetQuery) (*workitems.IssueDetail, error) {
	return b.issue, b.err
}

func TestDiffServiceGetIssueDiffStat_UsesWorkspaceBackend(t *testing.T) {
	var backendWorkspace string
	var resolvedAgent string
	gitOps := &mockGitOps{
		resolveFunc: func(name string) (*ops.AgentWorktree, error) {
			resolvedAgent = name
			return &ops.AgentWorktree{Path: "/tmp/agent", Branch: "agent/task-1", DefaultBranch: "main"}, nil
		},
		diffStatFunc: func(path, base string) ops.DiffStatResult {
			if path != "/tmp/agent" || base != "main" {
				t.Fatalf("DiffStat(%q, %q), want /tmp/agent, main", path, base)
			}
			return ops.DiffStatResult{LinesAdded: 12, LinesRemoved: 3}
		},
	}
	be := &issueLookupBackend{issue: &workitems.IssueDetail{
		ID: "TASK-1", Assignee: "coder-1",
	}}
	svc := newTestDiffService(gitOps, func(ctx context.Context) workitems.API {
		backendWorkspace = middleware.WorkspaceFromContext(ctx)
		return be
	})

	result, err := svc.GetIssueDiffStat(context.Background(), "WS-1", "TASK-1")
	if err != nil {
		t.Fatalf("GetIssueDiffStat() error = %v", err)
	}
	if backendWorkspace != "WS-1" {
		t.Fatalf("backend workspace = %q, want WS-1", backendWorkspace)
	}
	if resolvedAgent != "coder-1" {
		t.Fatalf("resolved agent = %q, want coder-1", resolvedAgent)
	}
	if result.Branch != "agent/task-1" || result.Added != 12 || result.Removed != 3 {
		t.Fatalf("result = %+v", result)
	}
}

func TestDiffServiceGetIssueDiffStat_BackendFailuresFailClosed(t *testing.T) {
	t.Run("backend missing", func(t *testing.T) {
		svc := newTestDiffService(&mockGitOps{}, func(context.Context) workitems.API { return nil })
		if _, err := svc.GetIssueDiffStat(context.Background(), "WS-1", "TASK-1"); err == nil {
			t.Fatal("GetIssueDiffStat() error = nil, want unavailable error")
		}
	})

	t.Run("backend read fails", func(t *testing.T) {
		be := &issueLookupBackend{err: errors.New("backend down")}
		svc := newTestDiffService(&mockGitOps{}, func(context.Context) workitems.API { return be })
		if _, err := svc.GetIssueDiffStat(context.Background(), "WS-1", "TASK-1"); err == nil {
			t.Fatal("GetIssueDiffStat() error = nil, want internal error")
		}
	})
}
