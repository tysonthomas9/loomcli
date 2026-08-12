package clitest

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// --- MockIssueBackend ---

// MockBackendCall records a single method invocation on MockIssueBackend.
type MockBackendCall struct {
	Method string
	Args   []interface{}
}

// MockIssueBackend implements backend.IssueBackend for testing.
type MockIssueBackend struct {
	mu    sync.Mutex
	Calls []MockBackendCall

	GetResult           *backend.IssueDetailData
	GetErr              error
	GetFn               func(ctx context.Context, id string) (*backend.IssueDetailData, error)
	ListResult          []backend.IssueData
	ListErr             error
	ListFn              func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error)
	ReadyResult         []backend.IssueData
	ReadyErr            error
	ReadyFn             func(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error)
	BlockedResult       []workitems.IssueSummary
	BlockedErr          error
	BlockedFn           func(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error)
	StatsResult         *workitems.Stats
	StatsErr            error
	StatsFn             func(ctx context.Context) (*workitems.Stats, error)
	SearchResult        []workitems.IssueSummary
	SearchErr           error
	SearchFn            func(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error)
	CreateResult        *backend.IssueData
	CreateErr           error
	CreateFn            func(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error)
	UpdateErr           error
	UpdateFn            func(ctx context.Context, id string, params backend.UpdateParams) error
	ClaimIssueErr       error
	ClaimIssueFn        func(ctx context.Context, id string, lockTTL time.Duration) error
	ReleaseIssueLockErr error
	ReleaseIssueLockFn  func(ctx context.Context, id, actor string) error
	CloseResult         *backend.CloseResult
	CloseErr            error
	CloseFn             func(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error)
	ReopenErr           error
	ReopenFn            func(ctx context.Context, id string, params backend.ReopenParams) error
	DeleteErr           error
	DeleteFn            func(ctx context.Context, params backend.DeleteParams) error
	AddDependencyErr    error
	AddDependencyFn     func(ctx context.Context, params backend.DepAddParams) error
	RemoveDependencyErr error
	RemoveDependencyFn  func(ctx context.Context, params backend.DepRemoveParams) error
	ListCommentsResult  []backend.CommentData
	ListCommentsErr     error
	ListCommentsFn      func(ctx context.Context, id string) ([]backend.CommentData, error)
	AddCommentResult    *backend.CommentData
	AddCommentErr       error
	AddCommentFn        func(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error)
	ListEventsResult    []backend.EventData
	ListEventsErr       error
	ListEventsFn        func(ctx context.Context, id string, limit int) ([]backend.EventData, error)
	BackendNameResult   string
	BackendNameFn       func() string
}

func NewMockIssueBackend() *MockIssueBackend { return &MockIssueBackend{} }

func (m *MockIssueBackend) record(method string, args ...interface{}) {
	m.Calls = append(m.Calls, MockBackendCall{Method: method, Args: args})
}
func (m *MockIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	m.mu.Lock()
	m.record("Get", id)
	fn, r, e := m.GetFn, m.GetResult, m.GetErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return r, e
}
func (m *MockIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("List", opts)
	fn, r, e := m.ListFn, m.ListResult, m.ListErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return r, e
}
func (m *MockIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("Ready", opts)
	fn, r, e := m.ReadyFn, m.ReadyResult, m.ReadyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return r, e
}
func (m *MockIssueBackend) Blocked(ctx context.Context, query workitems.AvailabilityQuery) ([]workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Blocked", query)
	fn, r, e := m.BlockedFn, m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return r, e
}
func (m *MockIssueBackend) Stats(ctx context.Context) (*workitems.Stats, error) {
	m.mu.Lock()
	m.record("Stats")
	fn, r, e := m.StatsFn, m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return r, e
}
func (m *MockIssueBackend) Search(ctx context.Context, query workitems.SearchQuery) ([]workitems.IssueSummary, error) {
	m.mu.Lock()
	m.record("Search", query)
	fn, r, e := m.SearchFn, m.SearchResult, m.SearchErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query)
	}
	return r, e
}
func (m *MockIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	m.mu.Lock()
	m.record("Create", params)
	fn, r, e := m.CreateFn, m.CreateResult, m.CreateErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return r, e
}
func (m *MockIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	m.mu.Lock()
	m.record("Update", id, params)
	fn, e := m.UpdateFn, m.UpdateErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, params)
	}
	return e
}
func (m *MockIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	m.mu.Lock()
	m.record("ClaimIssue", id, lockTTL)
	fn, e := m.ClaimIssueFn, m.ClaimIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, lockTTL)
	}
	return e
}
func (m *MockIssueBackend) ReleaseIssueLock(ctx context.Context, id, actor string) error {
	m.mu.Lock()
	m.record("ReleaseIssueLock", id, actor)
	fn, e := m.ReleaseIssueLockFn, m.ReleaseIssueLockErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, actor)
	}
	return e
}
func (m *MockIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	m.mu.Lock()
	m.record("Close", id, params)
	fn, r, e := m.CloseFn, m.CloseResult, m.CloseErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, params)
	}
	return r, e
}
func (m *MockIssueBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	m.mu.Lock()
	m.record("Reopen", id, params)
	fn, e := m.ReopenFn, m.ReopenErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, params)
	}
	return e
}
func (m *MockIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	m.mu.Lock()
	m.record("Delete", params)
	fn, e := m.DeleteFn, m.DeleteErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return e
}
func (m *MockIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	m.mu.Lock()
	m.record("AddDependency", params)
	fn, e := m.AddDependencyFn, m.AddDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return e
}
func (m *MockIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	m.mu.Lock()
	m.record("RemoveDependency", params)
	fn, e := m.RemoveDependencyFn, m.RemoveDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return e
}
func (m *MockIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	m.mu.Lock()
	m.record("ListComments", id)
	fn, r, e := m.ListCommentsFn, m.ListCommentsResult, m.ListCommentsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return r, e
}
func (m *MockIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	m.mu.Lock()
	m.record("AddComment", params)
	fn, r, e := m.AddCommentFn, m.AddCommentResult, m.AddCommentErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return r, e
}
func (m *MockIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	m.mu.Lock()
	m.record("ListEvents", id, limit)
	fn, r, e := m.ListEventsFn, m.ListEventsResult, m.ListEventsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, limit)
	}
	return r, e
}
func (m *MockIssueBackend) BackendName() string {
	m.mu.Lock()
	m.record("BackendName")
	fn, r := m.BackendNameFn, m.BackendNameResult
	m.mu.Unlock()
	if fn != nil {
		return fn()
	}
	if r == "" {
		return "mock"
	}
	return r
}
func (m *MockIssueBackend) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, c := range m.Calls {
		if c.Method == method {
			count++
		}
	}
	return count
}
func (m *MockIssueBackend) Called(method string) bool { return m.CallCount(method) > 0 }
