package clitest

import (
	"context"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

	GetResult              *backend.IssueDetailData
	GetErr                 error
	GetFn                  func(ctx context.Context, id string) (*backend.IssueDetailData, error)
	ListResult             []backend.IssueData
	ListErr                error
	ListFn                 func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error)
	ReadyResult            []backend.IssueData
	ReadyErr               error
	ReadyFn                func(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error)
	BlockedResult          []backend.IssueData
	BlockedErr             error
	BlockedFn              func(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error)
	StatsResult            *backend.StatsData
	StatsErr               error
	StatsFn                func(ctx context.Context) (*backend.StatsData, error)
	CountResult            int
	CountErr               error
	CountFn                func(ctx context.Context, opts backend.CountOpts) (int, error)
	GetChildrenResult      []backend.IssueData
	GetChildrenErr         error
	GetChildrenFn          func(ctx context.Context, id string) ([]backend.IssueData, error)
	SearchIssuesResult     []backend.IssueData
	SearchIssuesErr        error
	SearchIssuesFn         func(ctx context.Context, query string, limit int) ([]backend.IssueData, error)
	CreateResult           *backend.IssueData
	CreateErr              error
	CreateFn               func(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error)
	UpdateErr              error
	UpdateFn               func(ctx context.Context, id string, params backend.UpdateParams) error
	ClaimIssueErr          error
	ClaimIssueFn           func(ctx context.Context, params backend.ClaimIssueParams) error
	ReleaseIssueLockErr    error
	ReleaseIssueLockFn     func(ctx context.Context, id, actor string) error
	DeferIssueErr          error
	DeferIssueFn           func(ctx context.Context, id string, until time.Time) error
	UndeferIssueErr        error
	UndeferIssueFn         func(ctx context.Context, id string) error
	CloseResult            *backend.CloseResult
	CloseErr               error
	CloseFn                func(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error)
	ReopenErr              error
	ReopenFn               func(ctx context.Context, id string, params backend.ReopenParams) error
	DeleteErr              error
	DeleteFn               func(ctx context.Context, params backend.DeleteParams) error
	AddDependencyErr       error
	AddDependencyFn        func(ctx context.Context, params backend.DepAddParams) error
	RemoveDependencyErr    error
	RemoveDependencyFn     func(ctx context.Context, params backend.DepRemoveParams) error
	AddLabelErr            error
	AddLabelFn             func(ctx context.Context, id string, label string) error
	RemoveLabelErr         error
	RemoveLabelFn          func(ctx context.Context, id string, label string) error
	ListCommentsResult     []backend.CommentData
	ListCommentsErr        error
	ListCommentsFn         func(ctx context.Context, id string) ([]backend.CommentData, error)
	AddCommentResult       *backend.CommentData
	AddCommentErr          error
	AddCommentFn           func(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error)
	ListEventsResult       []backend.EventData
	ListEventsErr          error
	ListEventsFn           func(ctx context.Context, id string, limit int) ([]backend.EventData, error)
	BatchResult            []backend.BatchResult
	BatchErr               error
	BatchFn                func(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error)
	GetMutationsResult     []backend.MutationData
	GetMutationsErr        error
	GetMutationsFn         func(ctx context.Context, sinceMs int64) ([]backend.MutationData, error)
	WaitForMutationsResult []backend.MutationData
	WaitForMutationsErr    error
	WaitForMutationsFn     func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error)
	BackendNameResult      string
	BackendNameFn          func() string
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
func (m *MockIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("Blocked", opts)
	fn, r, e := m.BlockedFn, m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return r, e
}
func (m *MockIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	m.mu.Lock()
	m.record("Stats")
	fn, r, e := m.StatsFn, m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return r, e
}
func (m *MockIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	m.mu.Lock()
	m.record("Count", opts)
	fn, r, e := m.CountFn, m.CountResult, m.CountErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return r, e
}
func (m *MockIssueBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("GetChildren", id)
	fn, r, e := m.GetChildrenFn, m.GetChildrenResult, m.GetChildrenErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return r, e
}
func (m *MockIssueBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("SearchIssues", query, limit)
	fn, r, e := m.SearchIssuesFn, m.SearchIssuesResult, m.SearchIssuesErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, query, limit)
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
func (m *MockIssueBackend) ClaimIssue(ctx context.Context, params backend.ClaimIssueParams) error {
	m.mu.Lock()
	m.record("ClaimIssue", params)
	fn, e := m.ClaimIssueFn, m.ClaimIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
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
func (m *MockIssueBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	m.mu.Lock()
	m.record("DeferIssue", id, until)
	fn, e := m.DeferIssueFn, m.DeferIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, until)
	}
	return e
}
func (m *MockIssueBackend) UndeferIssue(ctx context.Context, id string) error {
	m.mu.Lock()
	m.record("UndeferIssue", id)
	fn, e := m.UndeferIssueFn, m.UndeferIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
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
func (m *MockIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	m.mu.Lock()
	m.record("AddLabel", id, label)
	fn, e := m.AddLabelFn, m.AddLabelErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, label)
	}
	return e
}
func (m *MockIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	m.mu.Lock()
	m.record("RemoveLabel", id, label)
	fn, e := m.RemoveLabelFn, m.RemoveLabelErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, label)
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
func (m *MockIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	m.mu.Lock()
	m.record("Batch", ops)
	fn, r, e := m.BatchFn, m.BatchResult, m.BatchErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, ops)
	}
	return r, e
}
func (m *MockIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	m.mu.Lock()
	m.record("GetMutations", sinceMs)
	fn, r, e := m.GetMutationsFn, m.GetMutationsResult, m.GetMutationsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, sinceMs)
	}
	return r, e
}
func (m *MockIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	m.mu.Lock()
	m.record("WaitForMutations", sinceMs, timeoutMs)
	fn, r, e := m.WaitForMutationsFn, m.WaitForMutationsResult, m.WaitForMutationsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, sinceMs, timeoutMs)
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
