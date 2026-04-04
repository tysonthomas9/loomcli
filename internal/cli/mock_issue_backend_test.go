package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Compile-time interface check.
var _ backend.IssueBackend = (*MockIssueBackend)(nil)

// MockBackendCall records a single method invocation on MockIssueBackend.
type MockBackendCall struct {
	Method string
	Args   []interface{}
}

// MockIssueBackend implements backend.IssueBackend for testing. It records all
// calls and returns configurable results. Optional per-method Func overrides
// take precedence over the static result fields.
type MockIssueBackend struct {
	mu    sync.Mutex
	Calls []MockBackendCall

	// Get
	GetResult *backend.IssueDetailData
	GetErr    error
	GetFn     func(ctx context.Context, id string) (*backend.IssueDetailData, error)

	// List
	ListResult []backend.IssueData
	ListErr    error
	ListFn     func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error)

	// Ready
	ReadyResult []backend.IssueData
	ReadyErr    error
	ReadyFn     func(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error)

	// Blocked
	BlockedResult []backend.IssueData
	BlockedErr    error
	BlockedFn     func(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error)

	// Stats
	StatsResult *backend.StatsData
	StatsErr    error
	StatsFn     func(ctx context.Context) (*backend.StatsData, error)

	// Count
	CountResult int
	CountErr    error
	CountFn     func(ctx context.Context, opts backend.CountOpts) (int, error)

	// Create
	CreateResult *backend.IssueData
	CreateErr    error
	CreateFn     func(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error)

	// Update
	UpdateErr error
	UpdateFn  func(ctx context.Context, id string, params backend.UpdateParams) error

	// ClaimIssue
	ClaimIssueErr error
	ClaimIssueFn  func(ctx context.Context, id string, lockTTL time.Duration) error

	// Close
	CloseResult *backend.CloseResult
	CloseErr    error
	CloseFn     func(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error)

	// Delete
	DeleteErr error
	DeleteFn  func(ctx context.Context, params backend.DeleteParams) error

	// AddDependency
	AddDependencyErr error
	AddDependencyFn  func(ctx context.Context, params backend.DepAddParams) error

	// RemoveDependency
	RemoveDependencyErr error
	RemoveDependencyFn  func(ctx context.Context, params backend.DepRemoveParams) error

	// AddLabel
	AddLabelErr error
	AddLabelFn  func(ctx context.Context, id string, label string) error

	// RemoveLabel
	RemoveLabelErr error
	RemoveLabelFn  func(ctx context.Context, id string, label string) error

	// ListComments
	ListCommentsResult []backend.CommentData
	ListCommentsErr    error
	ListCommentsFn     func(ctx context.Context, id string) ([]backend.CommentData, error)

	// AddComment
	AddCommentResult *backend.CommentData
	AddCommentErr    error
	AddCommentFn     func(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error)

	// ListEvents
	ListEventsResult []backend.EventData
	ListEventsErr    error
	ListEventsFn     func(ctx context.Context, id string, limit int) ([]backend.EventData, error)

	// Batch
	BatchResult []backend.BatchResult
	BatchErr    error
	BatchFn     func(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error)

	// GetMutations
	GetMutationsResult []backend.MutationData
	GetMutationsErr    error
	GetMutationsFn     func(ctx context.Context, sinceMs int64) ([]backend.MutationData, error)

	// WaitForMutations
	WaitForMutationsResult []backend.MutationData
	WaitForMutationsErr    error
	WaitForMutationsFn     func(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error)

	// BackendName
	BackendNameResult string
	BackendNameFn     func() string
}

// NewMockIssueBackend returns a MockIssueBackend with zero-value defaults.
func NewMockIssueBackend() *MockIssueBackend {
	return &MockIssueBackend{}
}

// MockBackendWithReady returns a MockIssueBackend pre-configured with Ready results.
func MockBackendWithReady(issues []backend.IssueData) *MockIssueBackend {
	return &MockIssueBackend{ReadyResult: issues}
}

func (m *MockIssueBackend) record(method string, args ...interface{}) {
	m.Calls = append(m.Calls, MockBackendCall{Method: method, Args: args})
}

// Get implements backend.IssueBackend.
func (m *MockIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	m.mu.Lock()
	m.record("Get", id)
	fn := m.GetFn
	result, resultErr := m.GetResult, m.GetErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return result, resultErr
}

// List implements backend.IssueBackend.
func (m *MockIssueBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("List", opts)
	fn := m.ListFn
	result, resultErr := m.ListResult, m.ListErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// Ready implements backend.IssueBackend.
func (m *MockIssueBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("Ready", opts)
	fn := m.ReadyFn
	result, resultErr := m.ReadyResult, m.ReadyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// Blocked implements backend.IssueBackend.
func (m *MockIssueBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	m.mu.Lock()
	m.record("Blocked", opts)
	fn := m.BlockedFn
	result, resultErr := m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// Stats implements backend.IssueBackend.
func (m *MockIssueBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	m.mu.Lock()
	m.record("Stats")
	fn := m.StatsFn
	result, resultErr := m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return result, resultErr
}

// Count implements backend.IssueBackend.
func (m *MockIssueBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	m.mu.Lock()
	m.record("Count", opts)
	fn := m.CountFn
	result, resultErr := m.CountResult, m.CountErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// Create implements backend.IssueBackend.
func (m *MockIssueBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	m.mu.Lock()
	m.record("Create", params)
	fn := m.CreateFn
	result, resultErr := m.CreateResult, m.CreateErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return result, resultErr
}

// Update implements backend.IssueBackend.
func (m *MockIssueBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	m.mu.Lock()
	m.record("Update", id, params)
	fn := m.UpdateFn
	resultErr := m.UpdateErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, params)
	}
	return resultErr
}

// ClaimIssue implements backend.IssueBackend.
func (m *MockIssueBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	m.mu.Lock()
	m.record("ClaimIssue", id, lockTTL)
	fn := m.ClaimIssueFn
	resultErr := m.ClaimIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, lockTTL)
	}
	return resultErr
}

// Close implements backend.IssueBackend.
func (m *MockIssueBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	m.mu.Lock()
	m.record("Close", id, params)
	fn := m.CloseFn
	result, resultErr := m.CloseResult, m.CloseErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, params)
	}
	return result, resultErr
}

// Delete implements backend.IssueBackend.
func (m *MockIssueBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	m.mu.Lock()
	m.record("Delete", params)
	fn := m.DeleteFn
	resultErr := m.DeleteErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return resultErr
}

// AddDependency implements backend.IssueBackend.
func (m *MockIssueBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	m.mu.Lock()
	m.record("AddDependency", params)
	fn := m.AddDependencyFn
	resultErr := m.AddDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return resultErr
}

// RemoveDependency implements backend.IssueBackend.
func (m *MockIssueBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	m.mu.Lock()
	m.record("RemoveDependency", params)
	fn := m.RemoveDependencyFn
	resultErr := m.RemoveDependencyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return resultErr
}

// AddLabel implements backend.IssueBackend.
func (m *MockIssueBackend) AddLabel(ctx context.Context, id string, label string) error {
	m.mu.Lock()
	m.record("AddLabel", id, label)
	fn := m.AddLabelFn
	resultErr := m.AddLabelErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, label)
	}
	return resultErr
}

// RemoveLabel implements backend.IssueBackend.
func (m *MockIssueBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	m.mu.Lock()
	m.record("RemoveLabel", id, label)
	fn := m.RemoveLabelFn
	resultErr := m.RemoveLabelErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, label)
	}
	return resultErr
}

// ListComments implements backend.IssueBackend.
func (m *MockIssueBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	m.mu.Lock()
	m.record("ListComments", id)
	fn := m.ListCommentsFn
	result, resultErr := m.ListCommentsResult, m.ListCommentsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return result, resultErr
}

// AddComment implements backend.IssueBackend.
func (m *MockIssueBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	m.mu.Lock()
	m.record("AddComment", params)
	fn := m.AddCommentFn
	result, resultErr := m.AddCommentResult, m.AddCommentErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, params)
	}
	return result, resultErr
}

// ListEvents implements backend.IssueBackend.
func (m *MockIssueBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	m.mu.Lock()
	m.record("ListEvents", id, limit)
	fn := m.ListEventsFn
	result, resultErr := m.ListEventsResult, m.ListEventsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, limit)
	}
	return result, resultErr
}

// Batch implements backend.IssueBackend.
func (m *MockIssueBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	m.mu.Lock()
	m.record("Batch", ops)
	fn := m.BatchFn
	result, resultErr := m.BatchResult, m.BatchErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, ops)
	}
	return result, resultErr
}

// GetMutations implements backend.IssueBackend.
func (m *MockIssueBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	m.mu.Lock()
	m.record("GetMutations", sinceMs)
	fn := m.GetMutationsFn
	result, resultErr := m.GetMutationsResult, m.GetMutationsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, sinceMs)
	}
	return result, resultErr
}

// WaitForMutations implements backend.IssueBackend.
func (m *MockIssueBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	m.mu.Lock()
	m.record("WaitForMutations", sinceMs, timeoutMs)
	fn := m.WaitForMutationsFn
	result, resultErr := m.WaitForMutationsResult, m.WaitForMutationsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, sinceMs, timeoutMs)
	}
	return result, resultErr
}

// BackendName implements backend.IssueBackend.
func (m *MockIssueBackend) BackendName() string {
	m.mu.Lock()
	m.record("BackendName")
	fn := m.BackendNameFn
	result := m.BackendNameResult
	m.mu.Unlock()
	if fn != nil {
		return fn()
	}
	if result == "" {
		return "mock"
	}
	return result
}

// CallCount returns how many times the given method was called.
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

// Called returns true if the given method was called at least once.
func (m *MockIssueBackend) Called(method string) bool {
	return m.CallCount(method) > 0
}

// --- Tests for MockIssueBackend ---

func TestMockIssueBackend_RecordsCalls(t *testing.T) {
	m := NewMockIssueBackend()
	ctx := context.Background()

	_, _ = m.Ready(ctx, backend.ReadyOpts{Limit: 10})
	_, _ = m.Get(ctx, "task-1")
	_, _ = m.Close(ctx, "task-1", backend.CloseParams{Reason: "done"})

	if len(m.Calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(m.Calls))
	}
	if m.Calls[0].Method != "Ready" {
		t.Errorf("call 0: got %q, want Ready", m.Calls[0].Method)
	}
	if m.Calls[1].Method != "Get" {
		t.Errorf("call 1: got %q, want Get", m.Calls[1].Method)
	}
	if m.Calls[2].Method != "Close" {
		t.Errorf("call 2: got %q, want Close", m.Calls[2].Method)
	}
	// Verify args recorded for Get
	if id, ok := m.Calls[1].Args[0].(string); !ok || id != "task-1" {
		t.Errorf("Get arg: got %v, want task-1", m.Calls[1].Args[0])
	}
}

func TestMockIssueBackend_ReturnsConfiguredResults(t *testing.T) {
	issues := []backend.IssueData{{ID: "t-1", Title: "Test"}}
	m := MockBackendWithReady(issues)
	m.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "t-2", Title: "Detail"}}
	m.BackendNameResult = "test-backend"

	ctx := context.Background()

	got, err := m.Ready(ctx, backend.ReadyOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t-1" {
		t.Errorf("Ready: got %v, want [{ID:t-1}]", got)
	}

	issue, err := m.Get(ctx, "t-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "t-2" {
		t.Errorf("Get: got %q, want t-2", issue.ID)
	}

	if name := m.BackendName(); name != "test-backend" {
		t.Errorf("BackendName: got %q, want test-backend", name)
	}
}

func TestMockIssueBackend_FuncOverride(t *testing.T) {
	m := NewMockIssueBackend()
	m.ReadyResult = []backend.IssueData{{ID: "static"}}
	m.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		return []backend.IssueData{{ID: "dynamic", Title: opts.ParentID}}, nil
	}

	ctx := context.Background()
	got, err := m.Ready(ctx, backend.ReadyOpts{ParentID: "parent-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "dynamic" {
		t.Errorf("expected Func override, got %v", got)
	}
	if got[0].Title != "parent-1" {
		t.Errorf("expected Title=parent-1 from opts, got %q", got[0].Title)
	}
}

func TestMockIssueBackend_CallCount(t *testing.T) {
	m := NewMockIssueBackend()
	ctx := context.Background()

	_, _ = m.Ready(ctx, backend.ReadyOpts{})
	_, _ = m.Ready(ctx, backend.ReadyOpts{})
	_, _ = m.List(ctx, backend.ListOpts{})

	if m.CallCount("Ready") != 2 {
		t.Errorf("Ready count: got %d, want 2", m.CallCount("Ready"))
	}
	if !m.Called("List") {
		t.Error("expected List to be called")
	}
	if m.Called("Blocked") {
		t.Error("Blocked should not have been called")
	}
}

func TestMockIssueBackend_BackendNameDefault(t *testing.T) {
	m := NewMockIssueBackend()
	if name := m.BackendName(); name != "mock" {
		t.Errorf("default BackendName: got %q, want mock", name)
	}
}
