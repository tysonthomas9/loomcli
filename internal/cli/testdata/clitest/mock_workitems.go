package clitest

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type MockWorkItemsCall struct {
	Method string
	Args   []any
}

// MockWorkItems is the canonical test double for the Work Items capability
// and its operational claim-lease port.
type MockWorkItems struct {
	mu    sync.Mutex
	Calls []MockWorkItemsCall

	GetResult                     *workitems.IssueDetail
	GetErr                        error
	GetFn                         func(context.Context, workitems.GetQuery) (*workitems.IssueDetail, error)
	ListResult                    *workitems.ListResult
	ListErr                       error
	ListFn                        func(context.Context, workitems.ListQuery) (*workitems.ListResult, error)
	ReadyResult                   []workitems.IssueSummary
	ReadyErr                      error
	ReadyFn                       func(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	BlockedResult                 []workitems.IssueSummary
	BlockedErr                    error
	BlockedFn                     func(context.Context, workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	StatsResult                   *workitems.Stats
	StatsErr                      error
	StatsFn                       func(context.Context) (*workitems.Stats, error)
	SearchResult                  []workitems.IssueSummary
	SearchErr                     error
	SearchFn                      func(context.Context, workitems.SearchQuery) ([]workitems.IssueSummary, error)
	CreateResult                  *workitems.IssueSummary
	CreateErr                     error
	CreateFn                      func(context.Context, workitems.CreateCommand) (*workitems.IssueSummary, error)
	PatchResult                   *workitems.IssueDetail
	PatchErr                      error
	PatchFn                       func(context.Context, workitems.PatchCommand) (*workitems.IssueDetail, error)
	ClaimResult                   *workitems.IssueDetail
	ClaimErr                      error
	ClaimFn                       func(context.Context, workitems.ClaimCommand) (*workitems.IssueDetail, error)
	CloseResult                   *workitems.CloseResult
	CloseErr                      error
	CloseFn                       func(context.Context, workitems.CloseCommand) (*workitems.CloseResult, error)
	ReopenErr                     error
	ReopenFn                      func(context.Context, workitems.ReopenCommand) error
	BlockRepositoryRequiredResult *workitems.RepositoryAdmissionResult
	BlockRepositoryRequiredErr    error
	BlockRepositoryRequiredFn     func(context.Context, workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error)
	AssignRepositoryResult        *workitems.IssueSummary
	AssignRepositoryErr           error
	AssignRepositoryFn            func(context.Context, workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error)
	DeleteResult                  workitems.DeleteResult
	DeleteErr                     error
	DeleteFn                      func(context.Context, workitems.DeleteCommand) (workitems.DeleteResult, error)
	AddDependencyErr              error
	AddDependencyFn               func(context.Context, workitems.AddDependencyCommand) error
	RemoveDependencyErr           error
	RemoveDependencyFn            func(context.Context, workitems.RemoveDependencyCommand) error
	ListDependenciesResult        []workitems.Dependency
	ListDependenciesErr           error
	ListDependenciesFn            func(context.Context, workitems.ListDependenciesQuery) ([]workitems.Dependency, error)
	ListCommentsResult            []*workitems.Comment
	ListCommentsErr               error
	ListCommentsFn                func(context.Context, workitems.ListCommentsQuery) ([]*workitems.Comment, error)
	AddCommentResult              *workitems.Comment
	AddCommentErr                 error
	AddCommentFn                  func(context.Context, workitems.AddCommentCommand) (*workitems.Comment, error)
	ListEventsResult              []*workitems.Event
	ListEventsErr                 error
	ListEventsFn                  func(context.Context, workitems.ListEventsQuery) ([]*workitems.Event, error)
	ClaimAsActorErr               error
	ClaimAsActorFn                func(context.Context, string, time.Duration, string) error
	RenewClaimAsActorErr          error
	RenewClaimAsActorFn           func(context.Context, string, time.Duration, string) error
	ReleaseIssueLockErr           error
	ReleaseIssueLockFn            func(context.Context, string, string) error
	ReleaseIssueAsActorErr        error
	ReleaseIssueAsActorFn         func(context.Context, string, string) error
	ReleaseClaimErr               error
	ReleaseClaimFn                func(context.Context, string, string) error
	BackendNameResult             string
	BackendNameFn                 func() string
}

var _ workitems.API = (*MockWorkItems)(nil)
var _ workitems.StatsQueries = (*MockWorkItems)(nil)
var _ workitems.ClaimLeaseCommands = (*MockWorkItems)(nil)

func NewMockWorkItems() *MockWorkItems { return &MockWorkItems{} }

func (m *MockWorkItems) record(method string, args ...any) {
	m.Calls = append(m.Calls, MockWorkItemsCall{Method: method, Args: args})
}

func (m *MockWorkItems) Get(ctx context.Context, query workitems.GetQuery) (*workitems.IssueDetail, error) {
	m.mu.Lock()
	m.record("Get", query)
	fn, value, err := m.GetFn, m.GetResult, m.GetErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) List(ctx context.Context, query workitems.ListQuery) (*workitems.ListResult, error) {
	m.mu.Lock()
	m.record("List", query)
	fn, value, err := m.ListFn, m.ListResult, m.ListErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	if value == nil && err == nil {
		value = &workitems.ListResult{}
	}
	return value, err
}

func (m *MockWorkItems) Ready(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Ready", query)
	fn, value, err := m.ReadyFn, m.ReadyResult, m.ReadyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Blocked", query)
	fn, value, err := m.BlockedFn, m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) Stats(ctx context.Context) (*workitems.Stats, error) {
	m.mu.Lock()
	m.record("Stats")
	fn, value, err := m.StatsFn, m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return value, err
}

func (m *MockWorkItems) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Search", query)
	fn, value, err := m.SearchFn, m.SearchResult, m.SearchErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) Create(ctx context.Context, command workitems.CreateCommand) (*workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Create", command)
	fn, value, err := m.CreateFn, m.CreateResult, m.CreateErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) Patch(ctx context.Context, command workitems.PatchCommand) (*workitems.IssueDetail, error) {
	m.mu.Lock()
	m.record("Patch", command)
	fn, value, err := m.PatchFn, m.PatchResult, m.PatchErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) Claim(ctx context.Context, command workitems.ClaimCommand) (*workitems.IssueDetail, error) {
	m.mu.Lock()
	m.record("Claim", command)
	fn, value, err := m.ClaimFn, m.ClaimResult, m.ClaimErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) Close(ctx context.Context, command workitems.CloseCommand) (*workitems.CloseResult, error) {
	m.mu.Lock()
	m.record("Close", command)
	fn, value, err := m.CloseFn, m.CloseResult, m.CloseErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) Reopen(ctx context.Context, command workitems.ReopenCommand) error {
	m.mu.Lock()
	m.record("Reopen", command)
	fn, err := m.ReopenFn, m.ReopenErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return err
}

func (m *MockWorkItems) BlockRepositoryRequired(ctx context.Context, command workitems.BlockRepositoryRequiredCommand) (*workitems.RepositoryAdmissionResult, error) {
	m.mu.Lock()
	m.record("BlockRepositoryRequired", command)
	fn, value, err := m.BlockRepositoryRequiredFn, m.BlockRepositoryRequiredResult, m.BlockRepositoryRequiredErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) AssignRepository(ctx context.Context, command workitems.AssignRepositoryCommand) (*workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("AssignRepository", command)
	fn, value, err := m.AssignRepositoryFn, m.AssignRepositoryResult, m.AssignRepositoryErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) Delete(ctx context.Context, command workitems.DeleteCommand) (workitems.DeleteResult, error) {
	m.mu.Lock()
	m.record("Delete", command)
	fn, value, err := m.DeleteFn, m.DeleteResult, m.DeleteErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) AddDependency(ctx context.Context, command workitems.AddDependencyCommand) error {
	m.mu.Lock()
	m.record("AddDependency", command)
	fn, err := m.AddDependencyFn, m.AddDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return err
}

func (m *MockWorkItems) RemoveDependency(ctx context.Context, command workitems.RemoveDependencyCommand) error {
	m.mu.Lock()
	m.record("RemoveDependency", command)
	fn, err := m.RemoveDependencyFn, m.RemoveDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return err
}

func (m *MockWorkItems) ListDependencies(ctx context.Context, query workitems.ListDependenciesQuery) ([]workitems.Dependency, error) {
	m.mu.Lock()
	m.record("ListDependencies", query)
	fn, value, err := m.ListDependenciesFn, m.ListDependenciesResult, m.ListDependenciesErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) ListComments(ctx context.Context, query workitems.ListCommentsQuery) ([]*workitems.Comment, error) {
	m.mu.Lock()
	m.record("ListComments", query)
	fn, value, err := m.ListCommentsFn, m.ListCommentsResult, m.ListCommentsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) AddComment(ctx context.Context, command workitems.AddCommentCommand) (*workitems.Comment, error) {
	m.mu.Lock()
	m.record("AddComment", command)
	fn, value, err := m.AddCommentFn, m.AddCommentResult, m.AddCommentErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, command)
	}
	return value, err
}

func (m *MockWorkItems) ListEvents(ctx context.Context, query workitems.ListEventsQuery) ([]*workitems.Event, error) {
	m.mu.Lock()
	m.record("ListEvents", query)
	fn, value, err := m.ListEventsFn, m.ListEventsResult, m.ListEventsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return value, err
}

func (m *MockWorkItems) ClaimAsActor(ctx context.Context, id string, ttl time.Duration, actor string) error {
	m.mu.Lock()
	m.record("ClaimAsActor", id, ttl, actor)
	fn, err := m.ClaimAsActorFn, m.ClaimAsActorErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, ttl, actor)
	}
	return err
}

func (m *MockWorkItems) RenewClaimAsActor(ctx context.Context, id string, ttl time.Duration, actor string) error {
	m.mu.Lock()
	m.record("RenewClaimAsActor", id, ttl, actor)
	fn, err := m.RenewClaimAsActorFn, m.RenewClaimAsActorErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, ttl, actor)
	}
	return err
}

func (m *MockWorkItems) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	m.mu.Lock()
	m.record("ReleaseIssueLock", id, actor)
	fn, err := m.ReleaseIssueLockFn, m.ReleaseIssueLockErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, actor)
	}
	return err
}

func (m *MockWorkItems) ReleaseIssueAsActor(ctx context.Context, id, actor string) error {
	m.mu.Lock()
	m.record("ReleaseIssueAsActor", id, actor)
	fn, err := m.ReleaseIssueAsActorFn, m.ReleaseIssueAsActorErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, actor)
	}
	return err
}

func (m *MockWorkItems) ReleaseClaim(ctx context.Context, id, actor string) error {
	m.mu.Lock()
	m.record("ReleaseClaim", id, actor)
	fn, err := m.ReleaseClaimFn, m.ReleaseClaimErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, actor)
	}
	return err
}

func (m *MockWorkItems) BackendName() string {
	m.mu.Lock()
	m.record("BackendName")
	fn, value := m.BackendNameFn, m.BackendNameResult
	m.mu.Unlock()
	if fn != nil {
		return fn()
	}
	if value == "" {
		return "mock"
	}
	return value
}

func (m *MockWorkItems) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, call := range m.Calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

func (m *MockWorkItems) Called(method string) bool { return m.CallCount(method) > 0 }
