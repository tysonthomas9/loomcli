package sourcecontrol

import (
	"context"
	"errors"
	"testing"
)

type checkoutLayoutStub struct {
	checkout     AgentCheckout
	agents       []AgentCheckout
	repositories []RepositoryCheckoutView
	setRepo      string
	setBranch    string
}

func (layout *checkoutLayoutStub) ResolveAgentCheckout(_ context.Context, workspace, agent string) (AgentCheckout, error) {
	checkout := layout.checkout
	checkout.WorkspaceKey = workspace
	checkout.AgentID = agent
	return checkout, nil
}
func (layout *checkoutLayoutStub) ListAgentCheckouts(context.Context, string) ([]AgentCheckout, error) {
	return append([]AgentCheckout(nil), layout.agents...), nil
}
func (layout *checkoutLayoutStub) ListRepositoryCheckouts(context.Context, string) ([]RepositoryCheckoutView, error) {
	return append([]RepositoryCheckoutView(nil), layout.repositories...), nil
}
func (layout *checkoutLayoutStub) SetRepositoryDefaultBranch(_ context.Context, _ string, repository, branch string) error {
	layout.setRepo, layout.setBranch = repository, branch
	return nil
}

type branchMechanicsStub struct {
	pushCalls int
	push      *PushResult
	pull      *PullResult
	reset     *ResetResult
	status    *AgentStatusResult
	current   string
}

func (mechanics *branchMechanicsStub) Push(context.Context, string, string, string, string) (*PushResult, error) {
	mechanics.pushCalls++
	if mechanics.push == nil {
		return &PushResult{Success: true}, nil
	}
	return mechanics.push, nil
}
func (mechanics *branchMechanicsStub) Pull(context.Context, string, string, string, string) (*PullResult, error) {
	if mechanics.pull == nil {
		return &PullResult{Success: true}, nil
	}
	return mechanics.pull, nil
}
func (mechanics *branchMechanicsStub) CurrentBranch(context.Context, string) (string, error) {
	if mechanics.current == "" {
		return "feature", nil
	}
	return mechanics.current, nil
}
func (mechanics *branchMechanicsStub) Reset(context.Context, string, string, string, bool, bool) (*ResetResult, error) {
	if mechanics.reset == nil {
		return &ResetResult{Success: true}, nil
	}
	return mechanics.reset, nil
}
func (mechanics *branchMechanicsStub) Status(context.Context, string, string) (*AgentStatusResult, error) {
	if mechanics.status == nil {
		return &AgentStatusResult{Clean: true}, nil
	}
	return mechanics.status, nil
}

type forgeStub struct {
	available error
	pulls     map[string][]PullRequest
}

func (forge *forgeStub) Available(context.Context) error { return forge.available }
func (forge *forgeStub) CreatePullRequest(context.Context, string, string, string, string) (*PullRequestCreation, error) {
	return &PullRequestCreation{Created: true}, nil
}
func (forge *forgeStub) ListPullRequests(_ context.Context, path, _ string, _ int) ([]PullRequest, error) {
	return append([]PullRequest(nil), forge.pulls[path]...), nil
}

func newCheckoutFixture(t *testing.T) (*checkoutOperations, *checkoutLayoutStub, *branchMechanicsStub, *forgeStub) {
	t.Helper()
	layout := &checkoutLayoutStub{checkout: AgentCheckout{
		RepositoryRef: "repo", CheckoutPath: "/workspace/agent", Branch: "feature",
		DefaultBranch: "main", Remote: "origin", IsWorkspace: true,
	}}
	branches := &branchMechanicsStub{}
	forge := &forgeStub{}
	operations, err := newCheckoutOperations(layout, branches, forge)
	if err != nil {
		t.Fatalf("newCheckoutOperations() error = %v", err)
	}
	return operations, layout, branches, forge
}

func TestCheckoutSyncStopsAfterPushConflict(t *testing.T) {
	operations, _, branches, _ := newCheckoutFixture(t)
	branches.push = &PushResult{ConflictedFiles: []string{"conflict.go"}}

	result, err := operations.Sync(context.Background(), SyncCommand{WorkspaceKey: "WS", AgentID: "agent"})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if result.Push == nil || result.Pull != nil {
		t.Fatalf("Sync() = %#v, want push-only conflict result", result)
	}
}

func TestCheckoutPushAllOwnsAggregateConvergence(t *testing.T) {
	operations, layout, branches, _ := newCheckoutFixture(t)
	layout.agents = []AgentCheckout{
		{AgentID: "one", CheckoutPath: "/one", Branch: "one", DefaultBranch: "main"},
		{AgentID: "two", CheckoutPath: "/two", Branch: "two", DefaultBranch: "main"},
	}
	branches.push = &PushResult{Success: true, Message: "published"}

	result, err := operations.PushAll(context.Background(), PushAllCommand{WorkspaceKey: "WS"})
	if err != nil {
		t.Fatalf("PushAll() error = %v", err)
	}
	if branches.pushCalls != 2 || result.Pushed != 2 || result.Failed != 0 || len(result.Results) != 2 {
		t.Fatalf("PushAll() = %#v with %d calls", result, branches.pushCalls)
	}
}

func TestCheckoutListPullRequestsOwnsFilteringDeduplicationAndOrdering(t *testing.T) {
	operations, layout, _, forge := newCheckoutFixture(t)
	layout.repositories = []RepositoryCheckoutView{
		{RepositoryRef: "app", CheckoutPath: "/app", ProviderName: "org/app"},
		{RepositoryRef: "docs", CheckoutPath: "/docs", ProviderName: "org/docs"},
	}
	forge.pulls = map[string][]PullRequest{
		"/app": {
			{URL: "https://example/pr/1", State: "OPEN", UpdatedAt: "2026-01-01", ReviewDecision: ""},
			{URL: "https://example/pr/2", State: "OPEN", UpdatedAt: "2026-01-03", ReviewDecision: "APPROVED"},
		},
		"/docs": {{URL: "https://example/pr/1", State: "OPEN", UpdatedAt: "2026-01-01"}},
	}

	result, err := operations.ListPullRequests(context.Background(), ListPullRequestsQuery{WorkspaceKey: "WS", State: "review"})
	if err != nil {
		t.Fatalf("ListPullRequests() error = %v", err)
	}
	if len(result.PullRequests) != 1 || result.PullRequests[0].URL != "https://example/pr/1" || result.PullRequests[0].SourceRepo != "app" {
		t.Fatalf("ListPullRequests() = %#v", result)
	}
}

func TestCheckoutForgeUnavailableIsVisibleAndReadListingDegrades(t *testing.T) {
	operations, _, _, forge := newCheckoutFixture(t)
	forge.available = errors.New("missing")

	if _, err := operations.CreatePullRequest(context.Background(), CreatePullRequestCommand{WorkspaceKey: "WS", AgentID: "agent"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("CreatePullRequest() error = %v, want ErrUnavailable", err)
	}
	result, err := operations.ListPullRequests(context.Background(), ListPullRequestsQuery{WorkspaceKey: "WS"})
	if err != nil || len(result.Warnings) != 1 || result.PullRequests == nil {
		t.Fatalf("ListPullRequests() = %#v, %v", result, err)
	}
}

func TestCheckoutSetTargetBranchUsesWorkspaceRepositoryIdentity(t *testing.T) {
	operations, layout, _, _ := newCheckoutFixture(t)
	if err := operations.SetTargetBranch(context.Background(), SetTargetBranchCommand{
		WorkspaceKey: "WS", AgentID: "agent", Branch: "release",
	}); err != nil {
		t.Fatalf("SetTargetBranch() error = %v", err)
	}
	if layout.setRepo != "repo" || layout.setBranch != "release" {
		t.Fatalf("SetTargetBranch() wrote repo=%q branch=%q", layout.setRepo, layout.setBranch)
	}
}
