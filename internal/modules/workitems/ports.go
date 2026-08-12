package workitems

import "context"

// MutationStream is the Work Items-owned durable event port used by realtime
// delivery. FleetDB cursors are opaque; the timestamp-based compatibility path
// was intentionally deleted because it could duplicate or skip same-millisecond
// events.
type MutationStream interface {
	GetMutationsAfter(context.Context, string) ([]Mutation, error)
	WaitForMutationsAfter(context.Context, string, int64) ([]Mutation, error)
}

// Store is the consumer-owned durable port for the Work Items capability. It
// exposes aggregate-specific queries and commands instead of generic CRUD or
// a process-wide Store.
type Store interface {
	RequireRepositoryAdmission(context.Context) error
	Create(context.Context, CreateCommand) (*IssueSummary, error)
	BlockRepositoryRequired(context.Context, string) (*RepositoryAdmissionResult, error)
	List(context.Context, ListFilter) ([]IssueSummary, error)
	Stats(context.Context) (*Stats, error)
	Blocked(context.Context, AvailabilityQuery) ([]IssueSummary, error)
	Ready(context.Context, AvailabilityQuery) ([]IssueSummary, error)
	Deferred(context.Context, AvailabilityQuery) ([]IssueSummary, error)
	Search(context.Context, SearchQuery) ([]IssueSummary, error)
	Get(context.Context, GetQuery) (*IssueDetail, error)
	Patch(context.Context, PatchCommand) error
	Close(context.Context, CloseCommand) (*CloseResult, error)
	Claim(context.Context, ClaimCommand) (*IssueDetail, error)
	Reopen(context.Context, ReopenCommand) error
	AssignRepository(context.Context, AssignRepositoryCommand) (*IssueSummary, error)
	Delete(context.Context, DeleteCommand) (DeleteResult, error)
	ListEvents(context.Context, ListEventsQuery) ([]*Event, error)
	AddComment(context.Context, AddCommentCommand) (*Comment, error)
	ListComments(context.Context, ListCommentsQuery) ([]*Comment, error)
	AddDependency(context.Context, AddDependencyCommand) error
	RemoveDependency(context.Context, RemoveDependencyCommand) error
	ListDependencies(context.Context, ListDependenciesQuery) ([]Dependency, error)
}
