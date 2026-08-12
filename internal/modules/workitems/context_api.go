package workitems

import "context"

// Provider resolves the canonical Work Items capability for the workspace
// carried by ctx. It is a composition boundary, not a persistence abstraction.
type Provider func(context.Context) API

// ContextAPI routes each request to its workspace-scoped Work Items
// capability. All policy remains in the resolved capability; this type only
// preserves request context across a server assembled once for many
// workspaces.
type ContextAPI struct {
	provider Provider
}

var _ API = (*ContextAPI)(nil)
var _ StatsQueries = (*ContextAPI)(nil)

func Route(provider Provider) *ContextAPI {
	if provider == nil {
		return nil
	}
	return &ContextAPI{provider: provider}
}

func (r *ContextAPI) resolve(ctx context.Context) (API, error) {
	if r == nil || r.provider == nil {
		return nil, ErrUnavailable
	}
	api := r.provider(ctx)
	if api == nil {
		return nil, ErrUnavailable
	}
	return api, nil
}

func (r *ContextAPI) BackendName() string {
	api, err := r.resolve(context.Background())
	if err != nil {
		return ""
	}
	if named, ok := api.(interface{ BackendName() string }); ok {
		return named.BackendName()
	}
	return ""
}

func (r *ContextAPI) Create(ctx context.Context, command CreateCommand) (*IssueSummary, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Create(ctx, command)
}

func (r *ContextAPI) List(ctx context.Context, query ListQuery) (*ListResult, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.List(ctx, query)
}

func (r *ContextAPI) Ready(ctx context.Context, query AvailabilityQuery) ([]IssueSummary, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Ready(ctx, query)
}

func (r *ContextAPI) Blocked(ctx context.Context, query AvailabilityQuery) ([]IssueSummary, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Blocked(ctx, query)
}

func (r *ContextAPI) Search(ctx context.Context, query SearchQuery) ([]IssueSummary, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Search(ctx, query)
}

func (r *ContextAPI) Get(ctx context.Context, query GetQuery) (*IssueDetail, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Get(ctx, query)
}

func (r *ContextAPI) Patch(ctx context.Context, command PatchCommand) (*IssueDetail, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Patch(ctx, command)
}

func (r *ContextAPI) Close(ctx context.Context, command CloseCommand) (*CloseResult, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Close(ctx, command)
}

func (r *ContextAPI) Claim(ctx context.Context, command ClaimCommand) (*IssueDetail, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.Claim(ctx, command)
}

func (r *ContextAPI) Reopen(ctx context.Context, command ReopenCommand) error {
	api, err := r.resolve(ctx)
	if err != nil {
		return err
	}
	return api.Reopen(ctx, command)
}

func (r *ContextAPI) BlockRepositoryRequired(ctx context.Context, command BlockRepositoryRequiredCommand) (*RepositoryAdmissionResult, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.BlockRepositoryRequired(ctx, command)
}

func (r *ContextAPI) AssignRepository(ctx context.Context, command AssignRepositoryCommand) (*IssueSummary, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.AssignRepository(ctx, command)
}

func (r *ContextAPI) Delete(ctx context.Context, command DeleteCommand) (DeleteResult, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return DeleteResult{}, err
	}
	return api.Delete(ctx, command)
}

func (r *ContextAPI) ListEvents(ctx context.Context, query ListEventsQuery) ([]*Event, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListEvents(ctx, query)
}

func (r *ContextAPI) AddComment(ctx context.Context, command AddCommentCommand) (*Comment, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.AddComment(ctx, command)
}

func (r *ContextAPI) ListComments(ctx context.Context, query ListCommentsQuery) ([]*Comment, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListComments(ctx, query)
}

func (r *ContextAPI) AddDependency(ctx context.Context, command AddDependencyCommand) error {
	api, err := r.resolve(ctx)
	if err != nil {
		return err
	}
	return api.AddDependency(ctx, command)
}

func (r *ContextAPI) RemoveDependency(ctx context.Context, command RemoveDependencyCommand) error {
	api, err := r.resolve(ctx)
	if err != nil {
		return err
	}
	return api.RemoveDependency(ctx, command)
}

func (r *ContextAPI) ListDependencies(ctx context.Context, query ListDependenciesQuery) ([]Dependency, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return api.ListDependencies(ctx, query)
}

func (r *ContextAPI) Stats(ctx context.Context) (*Stats, error) {
	api, err := r.resolve(ctx)
	if err != nil {
		return nil, err
	}
	queries, ok := api.(StatsQueries)
	if !ok {
		return nil, ErrUnavailable
	}
	return queries.Stats(ctx)
}
