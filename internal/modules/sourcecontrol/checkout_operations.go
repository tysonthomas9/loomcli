package sourcecontrol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const pullRequestListLimit = 500

// BranchMechanics is Source Control's private local-Git mutation adapter.
// Paths originate only from WorkspaceLayout; callers never provide them.
type BranchMechanics interface {
	Push(context.Context, string, string, string, string) (*PushResult, error)
	Pull(context.Context, string, string, string, string) (*PullResult, error)
	CurrentBranch(context.Context, string) (string, error)
	Reset(context.Context, string, string, string, bool, bool) (*ResetResult, error)
	Status(context.Context, string, string) (*AgentStatusResult, error)
}

// ForgePublication is Source Control's private provider adapter. It executes
// provider protocol operations but owns no workspace selection or fallback
// policy.
type ForgePublication interface {
	Available(context.Context) error
	CreatePullRequest(context.Context, string, string, string, string) (*PullRequestCreation, error)
	ListPullRequests(context.Context, string, string, int) ([]PullRequest, error)
}

// RepositoryCheckoutView is the trusted, credential-free Workspace placement
// used for pull-request enumeration.
type RepositoryCheckoutView struct {
	RepositoryRef string
	CheckoutPath  string
	ProviderName  string
}

// CheckoutLayout is the Workspace-owned placement and repository-policy port
// consumed by Source Control's Checkout lifecycle.
type CheckoutLayout interface {
	WorkspaceLayout
	ListAgentCheckouts(context.Context, string) ([]AgentCheckout, error)
	ListRepositoryCheckouts(context.Context, string) ([]RepositoryCheckoutView, error)
	SetRepositoryDefaultBranch(context.Context, string, string, string) error
}

type PushCommand struct {
	WorkspaceKey string
	AgentID      string
	TargetBranch string
}

type PushAllCommand struct{ WorkspaceKey string }

type PullCommand struct {
	WorkspaceKey string
	AgentID      string
	SourceBranch string
}

type SyncCommand struct {
	WorkspaceKey string
	AgentID      string
}

type CreatePullRequestCommand struct {
	WorkspaceKey string
	AgentID      string
	TargetBranch string
}

type ListPullRequestsQuery struct {
	WorkspaceKey string
	State        string
}

type ResetCommand struct {
	WorkspaceKey string
	AgentID      string
	TargetBranch string
	Force        bool
	Push         bool
}

type AgentStatusQuery struct {
	WorkspaceKey string
	AgentID      string
}

type SetTargetBranchCommand struct {
	WorkspaceKey string
	AgentID      string
	Branch       string
}

type PushResult struct {
	Success         bool
	Message         string
	AlreadyUpToDate bool
	ConflictedFiles []string
}

type PullResult struct {
	Success         bool
	Message         string
	AlreadyUpToDate bool
	ConflictedFiles []string
}

type SyncResult struct {
	Push *PushResult
	Pull *PullResult
}

type PushAllResult struct {
	Results []PushAllCheckoutResult
	Pushed  int
	Failed  int
}

type PushAllCheckoutResult struct {
	AgentID string
	Success bool
	Message string
	Error   string
}

type PullRequest struct {
	Number         int
	Title          string
	URL            string
	State          string
	Draft          bool
	HeadBranch     string
	BaseBranch     string
	Author         string
	CreatedAt      string
	UpdatedAt      string
	ReviewDecision string
	Repository     string
	SourceRepo     string
	Additions      int
	Deletions      int
	ChangedFiles   int
}

type PullRequestList struct {
	PullRequests []PullRequest
	Warnings     []string
}

type PullRequestCreation struct {
	URL           string
	Created       bool
	AlreadyExists bool
	NoCommits     bool
}

type ResetResult struct {
	Success        bool
	Message        string
	PreviousBranch string
	Pushed         bool
}

type ResetLockedError struct {
	AgentID string
	PID     int
	Age     string
	TaskID  string
}

func (failure *ResetLockedError) Error() string { return "agent checkout is locked" }

type AgentStatusResult struct {
	Branch          string
	TargetBranch    string
	Clean           bool
	Ahead           int
	Behind          int
	ChangedFiles    []string
	ConflictedFiles []string
	HasConflicts    bool
	StashCount      int
}

type checkoutOperations struct {
	layout   CheckoutLayout
	branches BranchMechanics
	forge    ForgePublication
}

func newCheckoutOperations(layout CheckoutLayout, branches BranchMechanics, forge ForgePublication) (*checkoutOperations, error) {
	if layout == nil || branches == nil || forge == nil {
		return nil, fmt.Errorf("compose Source Control Checkout: layout, branch mechanics, and forge publication are required: %w", ErrUnavailable)
	}
	return &checkoutOperations{layout: layout, branches: branches, forge: forge}, nil
}

func (operations *checkoutOperations) Push(ctx context.Context, command PushCommand) (*PushResult, error) {
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	target := defaultBranch(command.TargetBranch, checkout.DefaultBranch)
	result, err := operations.branches.Push(ctx, checkout.CheckoutPath, checkout.Branch, target, checkout.Remote)
	if err != nil {
		return nil, fmt.Errorf("push agent checkout: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) PushAll(ctx context.Context, command PushAllCommand) (*PushAllResult, error) {
	workspace := strings.TrimSpace(command.WorkspaceKey)
	if workspace == "" {
		return nil, fmt.Errorf("push all requires workspace: %w", ErrInvalid)
	}
	checkouts, err := operations.layout.ListAgentCheckouts(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list agent checkouts: %w", err)
	}
	result := &PushAllResult{Results: make([]PushAllCheckoutResult, 0, len(checkouts))}
	for _, checkout := range checkouts {
		row := PushAllCheckoutResult{AgentID: checkout.AgentID}
		pushed, pushErr := operations.branches.Push(
			ctx, checkout.CheckoutPath, checkout.Branch,
			defaultBranch("", checkout.DefaultBranch), checkout.Remote,
		)
		switch {
		case pushErr != nil:
			row.Error = PublicErrorMessage(ErrRemote)
			result.Failed++
		case pushed == nil:
			row.Error = "branch adapter returned no result"
			result.Failed++
		case pushed.AlreadyUpToDate:
			row.Success = true
			row.Message = "already up to date"
		case pushed.Success:
			row.Success = true
			row.Message = pushed.Message
			result.Pushed++
		default:
			row.Error = pushed.Message
			result.Failed++
		}
		result.Results = append(result.Results, row)
	}
	return result, nil
}

func (operations *checkoutOperations) Pull(ctx context.Context, command PullCommand) (*PullResult, error) {
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	current, err := operations.branches.CurrentBranch(ctx, checkout.CheckoutPath)
	if err != nil {
		return nil, fmt.Errorf("resolve current branch: %w", err)
	}
	source := defaultBranch(command.SourceBranch, checkout.DefaultBranch)
	result, err := operations.branches.Pull(ctx, checkout.CheckoutPath, current, source, checkout.Remote)
	if err != nil {
		return nil, fmt.Errorf("pull agent checkout: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) Sync(ctx context.Context, command SyncCommand) (*SyncResult, error) {
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	target := defaultBranch("", checkout.DefaultBranch)
	push, err := operations.branches.Push(ctx, checkout.CheckoutPath, checkout.Branch, target, checkout.Remote)
	if err != nil {
		return nil, fmt.Errorf("sync push: %v: %w", err, ErrRemote)
	}
	result := &SyncResult{Push: push}
	if push != nil && !push.Success && len(push.ConflictedFiles) > 0 {
		return result, nil
	}
	current, err := operations.branches.CurrentBranch(ctx, checkout.CheckoutPath)
	if err != nil {
		return nil, fmt.Errorf("resolve current branch: %w", err)
	}
	result.Pull, err = operations.branches.Pull(ctx, checkout.CheckoutPath, current, target, checkout.Remote)
	if err != nil {
		return nil, fmt.Errorf("sync pull: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) CreatePullRequest(ctx context.Context, command CreatePullRequestCommand) (*PullRequestCreation, error) {
	if err := operations.forge.Available(ctx); err != nil {
		return nil, fmt.Errorf("pull-request publication unavailable: %w", ErrUnavailable)
	}
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	target := defaultBranch(command.TargetBranch, checkout.DefaultBranch)
	result, err := operations.forge.CreatePullRequest(ctx, checkout.CheckoutPath, checkout.Branch, target, checkout.Remote)
	if err != nil {
		return nil, fmt.Errorf("create pull request: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) ListPullRequests(ctx context.Context, query ListPullRequestsQuery) (*PullRequestList, error) {
	workspace := strings.TrimSpace(query.WorkspaceKey)
	if workspace == "" {
		return nil, fmt.Errorf("list pull requests requires workspace: %w", ErrInvalid)
	}
	state := normalizePullRequestState(query.State)
	if err := operations.forge.Available(ctx); err != nil {
		return &PullRequestList{PullRequests: []PullRequest{}, Warnings: []string{"pull-request provider CLI is unavailable"}}, nil
	}
	repositories, err := operations.layout.ListRepositoryCheckouts(ctx, workspace)
	if err != nil {
		return nil, fmt.Errorf("list repository checkouts: %w", err)
	}
	result := &PullRequestList{PullRequests: []PullRequest{}}
	seen := make(map[string]struct{})
	providerState := state
	if state == "review" {
		providerState = "open"
	}
	for _, repository := range repositories {
		pulls, listErr := operations.forge.ListPullRequests(ctx, repository.CheckoutPath, providerState, pullRequestListLimit)
		if listErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: pull-request listing failed", repository.RepositoryRef))
			continue
		}
		for _, pull := range pulls {
			if pull.URL == "" {
				continue
			}
			if _, exists := seen[pull.URL]; exists {
				continue
			}
			seen[pull.URL] = struct{}{}
			pull.SourceRepo = repository.RepositoryRef
			if pull.Repository == "" {
				pull.Repository = repository.ProviderName
			}
			if pullRequestMatchesState(pull, state) {
				result.PullRequests = append(result.PullRequests, pull)
			}
		}
	}
	sort.Slice(result.PullRequests, func(i, j int) bool {
		return result.PullRequests[i].UpdatedAt > result.PullRequests[j].UpdatedAt
	})
	return result, nil
}

func (operations *checkoutOperations) Reset(ctx context.Context, command ResetCommand) (*ResetResult, error) {
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return nil, err
	}
	target := defaultBranch(command.TargetBranch, checkout.DefaultBranch)
	result, err := operations.branches.Reset(ctx, checkout.CheckoutPath, checkout.AgentID, target, command.Force, command.Push)
	if err != nil {
		var locked *ResetLockedError
		if errors.As(err, &locked) {
			return nil, err
		}
		return nil, fmt.Errorf("reset agent checkout: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) AgentStatus(ctx context.Context, query AgentStatusQuery) (*AgentStatusResult, error) {
	checkout, err := operations.agentCheckout(ctx, query.WorkspaceKey, query.AgentID)
	if err != nil {
		return nil, err
	}
	result, err := operations.branches.Status(ctx, checkout.CheckoutPath, checkout.DefaultBranch)
	if err != nil {
		return nil, fmt.Errorf("read checkout status: %v: %w", err, ErrRemote)
	}
	return result, nil
}

func (operations *checkoutOperations) SetTargetBranch(ctx context.Context, command SetTargetBranchCommand) error {
	branch := strings.TrimSpace(command.Branch)
	if branch == "" {
		return fmt.Errorf("target branch is required: %w", ErrInvalid)
	}
	checkout, err := operations.agentCheckout(ctx, command.WorkspaceKey, command.AgentID)
	if err != nil {
		return err
	}
	if !checkout.IsWorkspace {
		return fmt.Errorf("target branch update requires workspace checkout: %w", ErrInvalid)
	}
	if strings.TrimSpace(checkout.RepositoryRef) == "" {
		return fmt.Errorf("workspace checkout has no repository identity: %w", ErrInvalidMaterialization)
	}
	return operations.layout.SetRepositoryDefaultBranch(ctx, command.WorkspaceKey, checkout.RepositoryRef, branch)
}

func (operations *checkoutOperations) agentCheckout(ctx context.Context, workspace, agent string) (AgentCheckout, error) {
	workspace = strings.TrimSpace(workspace)
	agent = strings.TrimSpace(agent)
	if ctx == nil || workspace == "" || agent == "" {
		return AgentCheckout{}, fmt.Errorf("checkout operation requires context, workspace, and agent: %w", ErrInvalid)
	}
	checkout, err := operations.layout.ResolveAgentCheckout(ctx, workspace, agent)
	if err != nil {
		return AgentCheckout{}, fmt.Errorf("resolve agent checkout: %w", err)
	}
	if err := validateAgentCheckout(DiffCommitsQuery{WorkspaceKey: workspace, AgentID: agent}, checkout); err != nil {
		return AgentCheckout{}, err
	}
	return checkout, nil
}

func defaultBranch(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	return "main"
}

func normalizePullRequestState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "open", "merged", "closed", "review":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "all"
	}
}

func pullRequestMatchesState(pull PullRequest, state string) bool {
	switch state {
	case "review":
		return !pull.Draft && !strings.EqualFold(pull.State, "merged") &&
			!strings.EqualFold(pull.State, "closed") && !strings.EqualFold(pull.ReviewDecision, "approved")
	case "open":
		return strings.EqualFold(pull.State, "open") && !pull.Draft
	case "merged":
		return strings.EqualFold(pull.State, "merged")
	case "closed":
		return strings.EqualFold(pull.State, "closed")
	default:
		return true
	}
}
