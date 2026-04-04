package beads

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// beadsClient is a narrow interface wrapping the rpc.Client methods that
// BeadsBackend needs. The production *rpc.Client satisfies it implicitly.
// Tests provide a mock.
type beadsClient interface {
	Ready(args *rpc.ReadyArgs) (*rpc.Response, error)
	List(args *rpc.ListArgs) (*rpc.Response, error)
	Show(args *rpc.ShowArgs) (*rpc.Response, error)
	Blocked(args *rpc.BlockedArgs) (*rpc.Response, error)
	Stats() (*rpc.Response, error)
	Count(args *rpc.CountArgs) (*rpc.Response, error)
	Create(args *rpc.CreateArgs) (*rpc.Response, error)
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
	Delete(args *rpc.DeleteArgs) (*rpc.Response, error)
	AddDependency(args *rpc.DepAddArgs) (*rpc.Response, error)
	RemoveDependency(args *rpc.DepRemoveArgs) (*rpc.Response, error)
	AddLabel(args *rpc.LabelAddArgs) (*rpc.Response, error)
	RemoveLabel(args *rpc.LabelRemoveArgs) (*rpc.Response, error)
	ListComments(args *rpc.CommentListArgs) (*rpc.Response, error)
	AddComment(args *rpc.CommentAddArgs) (*rpc.Response, error)
	ListEvents(args *rpc.EventListArgs) (*rpc.Response, error)
	Batch(args *rpc.BatchArgs) (*rpc.Response, error)
	GetMutations(args *rpc.GetMutationsArgs) (*rpc.Response, error)
	WaitForMutations(args *rpc.WaitForMutationsArgs) (*rpc.Response, error)
}

// BeadsBackend implements backend.IssueBackend by wrapping the beads daemon RPC client.
// It is NOT safe for concurrent use — the underlying rpc.Client connection is single-threaded.
// Use the connection pool (task .10) for concurrent access.
type BeadsBackend struct {
	client beadsClient
}

// Compile-time interface check.
var _ backend.IssueBackend = (*BeadsBackend)(nil)

// New creates a BeadsBackend with the given RPC client.
// Panics if client is nil — a nil client is a programming error.
func New(client beadsClient) *BeadsBackend {
	if client == nil {
		panic("beads.New: client must not be nil")
	}
	return &BeadsBackend{client: client}
}

func (b *BeadsBackend) BackendName() string { return "beads" }

// execAndCheck calls an RPC method and classifies errors.
func (b *BeadsBackend) execAndCheck(op string, fn func() (*rpc.Response, error)) (*rpc.Response, error) {
	resp, err := fn()
	if cerr := classifyError(op, err, resp); cerr != nil {
		return nil, cerr
	}
	return resp, nil
}

// --- Query operations ---

// Get returns the full detail projection for a single issue.
// Note: ctx is accepted per the IssueBackend interface but cannot be propagated
// to the RPC client (rpc.Client does not accept context today).
func (b *BeadsBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	resp, err := b.execAndCheck("Get", func() (*rpc.Response, error) {
		return b.client.Show(&rpc.ShowArgs{ID: id})
	})
	if err != nil {
		return nil, err
	}
	var details types.IssueDetails
	if err := json.Unmarshal(resp.Data, &details); err != nil {
		return nil, backend.ErrInternal("Get", "unmarshal response", err)
	}
	result := detailsToDetailData(&details)
	return &result, nil
}

// List returns a slim projection of issues matching the given filters.
func (b *BeadsBackend) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	rpcArgs := listOptsToArgs(opts)
	resp, err := b.execAndCheck("List", func() (*rpc.Response, error) {
		return b.client.List(rpcArgs)
	})
	if err != nil {
		return nil, err
	}
	var issues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("List", "unmarshal response", err)
	}
	return issuesWithCountsToData(issues), nil
}

// Ready returns issues with no open blockers.
func (b *BeadsBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	rpcArgs := &rpc.ReadyArgs{
		Assignee:        opts.Assignee,
		Unassigned:      opts.Unassigned,
		Priority:        opts.Priority,
		Type:            opts.Type,
		ParentID:        opts.ParentID,
		Limit:           opts.Limit,
		SortPolicy:      opts.SortPolicy,
		Labels:          opts.Labels,
		LabelsAny:       opts.LabelsAny,
		MolType:         opts.MolType,
		IncludeDeferred: opts.IncludeDeferred,
		SourceRepos:     opts.SourceRepos,
	}
	resp, err := b.execAndCheck("Ready", func() (*rpc.Response, error) {
		return b.client.Ready(rpcArgs)
	})
	if err != nil {
		return nil, err
	}
	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("Ready", "unmarshal response", err)
	}
	return issuesToData(issues), nil
}

// Blocked returns issues that have at least one open blocker.
func (b *BeadsBackend) Blocked(_ context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	rpcArgs := &rpc.BlockedArgs{
		ParentID: opts.ParentID,
		Assignee: opts.Assignee,
		Priority: opts.Priority,
		Type:     opts.Type,
		Limit:    opts.Limit,
	}
	resp, err := b.execAndCheck("Blocked", func() (*rpc.Response, error) {
		return b.client.Blocked(rpcArgs)
	})
	if err != nil {
		return nil, err
	}
	var issues []*types.Issue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("Blocked", "unmarshal response", err)
	}
	return issuesToData(issues), nil
}

// Stats returns aggregate issue statistics.
func (b *BeadsBackend) Stats(_ context.Context) (*backend.StatsData, error) {
	resp, err := b.execAndCheck("Stats", func() (*rpc.Response, error) {
		return b.client.Stats()
	})
	if err != nil {
		return nil, err
	}
	var stats types.Statistics
	if err := json.Unmarshal(resp.Data, &stats); err != nil {
		return nil, backend.ErrInternal("Stats", "unmarshal response", err)
	}
	result := statisticsToStatsData(&stats)
	return &result, nil
}

// Count returns the number of issues matching the given filters.
func (b *BeadsBackend) Count(_ context.Context, opts backend.CountOpts) (int, error) {
	rpcArgs := &rpc.CountArgs{
		Status:              opts.Status,
		Priority:            opts.Priority,
		IssueType:           opts.IssueType,
		Assignee:            opts.Assignee,
		Labels:              opts.Labels,
		LabelsAny:           opts.LabelsAny,
		IDs:                 opts.IDs,
		TitleContains:       opts.TitleContains,
		DescriptionContains: opts.DescriptionContains,
		NotesContains:       opts.NotesContains,
		CreatedAfter:        opts.CreatedAfter,
		CreatedBefore:       opts.CreatedBefore,
		UpdatedAfter:        opts.UpdatedAfter,
		UpdatedBefore:       opts.UpdatedBefore,
		ClosedAfter:         opts.ClosedAfter,
		ClosedBefore:        opts.ClosedBefore,
		EmptyDescription:    opts.EmptyDescription,
		NoAssignee:          opts.NoAssignee,
		NoLabels:            opts.NoLabels,
		PriorityMin:         opts.PriorityMin,
		PriorityMax:         opts.PriorityMax,
		GroupBy:             opts.GroupBy,
		SourceRepos:         opts.SourceRepos,
	}
	resp, err := b.execAndCheck("Count", func() (*rpc.Response, error) {
		return b.client.Count(rpcArgs)
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return 0, backend.ErrInternal("Count", "unmarshal response", err)
	}
	return result.Count, nil
}

// --- Mutation operations ---

// Create creates a new issue and returns the slim projection.
func (b *BeadsBackend) Create(_ context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	rpcArgs := &rpc.CreateArgs{
		ID:                 params.ID,
		Parent:             params.Parent,
		Title:              params.Title,
		Description:        params.Description,
		IssueType:          params.IssueType,
		Priority:           params.Priority,
		Design:             params.Design,
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		Assignee:           params.Assignee,
		Owner:              params.Owner,
		CreatedBy:          params.CreatedBy,
		ExternalRef:        params.ExternalRef,
		EstimatedMinutes:   params.EstimatedMinutes,
		Labels:             params.Labels,
		Dependencies:       params.Dependencies,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
	}
	resp, err := b.execAndCheck("Create", func() (*rpc.Response, error) {
		return b.client.Create(rpcArgs)
	})
	if err != nil {
		return nil, err
	}
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, backend.ErrInternal("Create", "unmarshal response", err)
	}
	result := issueToData(&issue)
	return &result, nil
}

// Update applies partial updates to an existing issue.
func (b *BeadsBackend) Update(_ context.Context, id string, params backend.UpdateParams) error {
	rpcArgs := &rpc.UpdateArgs{
		ID:                 id,
		Title:              params.Title,
		Description:        params.Description,
		Status:             params.Status,
		Priority:           params.Priority,
		Design:             params.Design,
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		Assignee:           params.Assignee,
		Owner:              params.Owner,
		IssueType:          params.IssueType,
		ExternalRef:        params.ExternalRef,
		EstimatedMinutes:   params.EstimatedMinutes,
		AddLabels:          params.AddLabels,
		RemoveLabels:       params.RemoveLabels,
		SetLabels:          params.SetLabels,
		Parent:             params.Parent,
		AgentState:         params.AgentState,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
		Claim:              params.Claim,
	}
	_, err := b.execAndCheck("Update", func() (*rpc.Response, error) {
		return b.client.Update(rpcArgs)
	})
	return err
}

// ClaimIssue atomically claims an issue for the current agent by delegating to
// Update with Claim=true. The lockTTL parameter is accepted but ignored because
// beads SQLite claims don't support TTL-based expiry.
func (b *BeadsBackend) ClaimIssue(_ context.Context, id string, lockTTL time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	if lockTTL < 0 {
		return backend.ErrValidation("ClaimIssue", "lockTTL must not be negative")
	}
	_, err := b.execAndCheck("ClaimIssue", func() (*rpc.Response, error) {
		return b.client.Update(&rpc.UpdateArgs{ID: id, Claim: true})
	})
	return err
}

// Close marks an issue as closed and returns the closed issue with unblocked issues.
func (b *BeadsBackend) Close(_ context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	rpcArgs := &rpc.CloseArgs{
		ID:          id,
		Reason:      params.Reason,
		Session:     params.Session,
		SuggestNext: true, // Always request unblocked issues per design
		Force:       params.Force,
	}
	resp, err := b.execAndCheck("Close", func() (*rpc.Response, error) {
		return b.client.CloseIssue(rpcArgs)
	})
	if err != nil {
		return nil, err
	}
	var cr rpc.CloseResult
	if err := json.Unmarshal(resp.Data, &cr); err != nil {
		return nil, backend.ErrInternal("Close", "unmarshal response", err)
	}
	return closeResultToData(&cr), nil
}

// Delete permanently removes one or more issues.
func (b *BeadsBackend) Delete(_ context.Context, params backend.DeleteParams) error {
	rpcArgs := &rpc.DeleteArgs{
		IDs:     params.IDs,
		Reason:  params.Reason,
		Force:   params.Force,
		Cascade: params.Cascade,
	}
	_, err := b.execAndCheck("Delete", func() (*rpc.Response, error) {
		return b.client.Delete(rpcArgs)
	})
	return err
}

// --- Dependency operations ---

func (b *BeadsBackend) AddDependency(_ context.Context, params backend.DepAddParams) error {
	rpcArgs := &rpc.DepAddArgs{
		FromID:  params.FromID,
		ToID:    params.ToID,
		DepType: params.DepType,
	}
	_, err := b.execAndCheck("AddDependency", func() (*rpc.Response, error) {
		return b.client.AddDependency(rpcArgs)
	})
	return err
}

func (b *BeadsBackend) RemoveDependency(_ context.Context, params backend.DepRemoveParams) error {
	rpcArgs := &rpc.DepRemoveArgs{
		FromID:  params.FromID,
		ToID:    params.ToID,
		DepType: params.DepType,
	}
	_, err := b.execAndCheck("RemoveDependency", func() (*rpc.Response, error) {
		return b.client.RemoveDependency(rpcArgs)
	})
	return err
}

// --- Label operations ---

func (b *BeadsBackend) AddLabel(_ context.Context, id string, label string) error {
	_, err := b.execAndCheck("AddLabel", func() (*rpc.Response, error) {
		return b.client.AddLabel(&rpc.LabelAddArgs{ID: id, Label: label})
	})
	return err
}

func (b *BeadsBackend) RemoveLabel(_ context.Context, id string, label string) error {
	_, err := b.execAndCheck("RemoveLabel", func() (*rpc.Response, error) {
		return b.client.RemoveLabel(&rpc.LabelRemoveArgs{ID: id, Label: label})
	})
	return err
}

// --- Comment operations ---

func (b *BeadsBackend) ListComments(_ context.Context, id string) ([]backend.CommentData, error) {
	resp, err := b.execAndCheck("ListComments", func() (*rpc.Response, error) {
		return b.client.ListComments(&rpc.CommentListArgs{ID: id})
	})
	if err != nil {
		return nil, err
	}
	var comments []*types.Comment
	if err := json.Unmarshal(resp.Data, &comments); err != nil {
		return nil, backend.ErrInternal("ListComments", "unmarshal response", err)
	}
	result := make([]backend.CommentData, 0, len(comments))
	for _, c := range comments {
		if c != nil {
			result = append(result, commentToData(c))
		}
	}
	return result, nil
}

func (b *BeadsBackend) AddComment(_ context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	resp, err := b.execAndCheck("AddComment", func() (*rpc.Response, error) {
		return b.client.AddComment(&rpc.CommentAddArgs{
			ID:     params.IssueID,
			Author: params.Author,
			Text:   params.Text,
		})
	})
	if err != nil {
		return nil, err
	}
	var comment types.Comment
	if err := json.Unmarshal(resp.Data, &comment); err != nil {
		return nil, backend.ErrInternal("AddComment", "unmarshal response", err)
	}
	result := commentToData(&comment)
	return &result, nil
}

// --- Event operations ---

func (b *BeadsBackend) ListEvents(_ context.Context, id string, limit int) ([]backend.EventData, error) {
	resp, err := b.execAndCheck("ListEvents", func() (*rpc.Response, error) {
		return b.client.ListEvents(&rpc.EventListArgs{ID: id, Limit: limit})
	})
	if err != nil {
		return nil, err
	}
	var events []*types.Event
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, backend.ErrInternal("ListEvents", "unmarshal response", err)
	}
	result := make([]backend.EventData, 0, len(events))
	for _, e := range events {
		if e != nil {
			result = append(result, eventToData(e))
		}
	}
	return result, nil
}

// --- Batch operations ---

func (b *BeadsBackend) Batch(_ context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	rpcOps := make([]rpc.BatchOperation, len(ops))
	for i, op := range ops {
		rpcOps[i] = rpc.BatchOperation{
			Operation: op.Operation,
			Args:      op.Args,
		}
	}
	resp, err := b.execAndCheck("Batch", func() (*rpc.Response, error) {
		return b.client.Batch(&rpc.BatchArgs{Operations: rpcOps})
	})
	if err != nil {
		return nil, err
	}
	var br rpc.BatchResponse
	if err := json.Unmarshal(resp.Data, &br); err != nil {
		return nil, backend.ErrInternal("Batch", "unmarshal response", err)
	}
	results := make([]backend.BatchResult, len(br.Results))
	for i, r := range br.Results {
		results[i] = backend.BatchResult{
			Success: r.Success,
			Data:    r.Data,
			Error:   r.Error,
		}
	}
	return results, nil
}

// --- Mutation polling ---

func (b *BeadsBackend) GetMutations(_ context.Context, sinceMs int64) ([]backend.MutationData, error) {
	resp, err := b.execAndCheck("GetMutations", func() (*rpc.Response, error) {
		return b.client.GetMutations(&rpc.GetMutationsArgs{Since: sinceMs})
	})
	if err != nil {
		return nil, err
	}
	var mutations []rpc.MutationEvent
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		return nil, backend.ErrInternal("GetMutations", "unmarshal response", err)
	}
	return mutationsToData(mutations), nil
}

func (b *BeadsBackend) WaitForMutations(_ context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	resp, err := b.execAndCheck("WaitForMutations", func() (*rpc.Response, error) {
		return b.client.WaitForMutations(&rpc.WaitForMutationsArgs{Since: sinceMs, Timeout: timeoutMs})
	})
	if err != nil {
		return nil, err
	}
	var mutations []rpc.MutationEvent
	if err := json.Unmarshal(resp.Data, &mutations); err != nil {
		return nil, backend.ErrInternal("WaitForMutations", "unmarshal response", err)
	}
	return mutationsToData(mutations), nil
}
