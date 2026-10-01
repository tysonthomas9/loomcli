package svcimpl

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// noAreaGitOps refuses Apply until the lead's working area has been opened.
type noAreaGitOps struct {
	ops.GitOps
	areaOpen bool
	calls    int
}

func (g *noAreaGitOps) ApplyRevision(context.Context, ops.ApplyRevisionRequest) (*ops.GitPushResult, error) {
	g.calls++
	if !g.areaOpen {
		return nil, loomgit.NewError(loomgit.AttentionRequired, "working area for change repo and lead is unavailable", nil)
	}
	return &ops.GitPushResult{Success: true}, nil
}

func stubLeadWorkingArea(t *testing.T, fn func(domain.Agent) error) {
	t.Helper()
	prior := ensureLeadWorkingArea
	ensureLeadWorkingArea = func(_ context.Context, _ *agentServiceImpl, agent domain.Agent) error { return fn(agent) }
	t.Cleanup(func() { ensureLeadWorkingArea = prior })
}

func TestGitApplyOpensExistingLeadWorkingAreaThenApplies(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "W", Name: "W", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "W", Name: "lead-a", RoleName: "lead"}); err != nil {
		t.Fatal(err)
	}
	gitOps := &noAreaGitOps{}
	var opened []string
	stubLeadWorkingArea(t, func(agent domain.Agent) error {
		opened = append(opened, agent.Name)
		gitOps.areaOpen = true
		return nil
	})
	svc := NewAgentService(gitOps, nil, nil, st)
	result, err := svc.GitApply(ctx, ops.ApplyRevisionRequest{Workspace: "W", Change: "C", Revision: 1, Lead: "lead-a"})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("GitApply = %+v, %v; want success", result, err)
	}
	if len(opened) != 1 || opened[0] != "lead-a" || gitOps.calls != 2 {
		t.Fatalf("opened %v with %d Apply calls; want [lead-a] then a second Apply", opened, gitOps.calls)
	}
}

func TestGitApplyMissingLeadAgentAsksToCreateItFirst(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "W", Name: "W", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	gitOps := &noAreaGitOps{}
	stubLeadWorkingArea(t, func(domain.Agent) error {
		t.Fatal("a working area must not be opened without a lead agent")
		return nil
	})
	svc := NewAgentService(gitOps, nil, nil, st)
	_, err := svc.GitApply(ctx, ops.ApplyRevisionRequest{Workspace: "W", Change: "C", Revision: 1, Lead: "lead-a"})
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindNotFound || svcErr.Message != `lead agent "lead-a" does not exist: create the lead agent first` {
		t.Fatalf("GitApply error = %v; want not-found asking to create the lead agent", err)
	}
	if gitOps.calls != 1 {
		t.Fatalf("Apply calls = %d, want 1", gitOps.calls)
	}
}
