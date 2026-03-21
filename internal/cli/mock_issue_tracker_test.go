package cli

import (
	"context"
	"sync"
	"testing"
)

// Compile-time interface check.
var _ IssueTracker = (*MockIssueTracker)(nil)

// MockTrackerCall records a single method invocation on MockIssueTracker.
type MockTrackerCall struct {
	Method string
	Args   []interface{}
}

// MockIssueTracker implements IssueTracker for testing. It records all calls
// and returns configurable results. Optional per-method Func overrides take
// precedence over the static result fields.
type MockIssueTracker struct {
	mu    sync.Mutex
	Calls []MockTrackerCall

	// Ready
	ReadyResult []BdIssue
	ReadyErr    error
	ReadyFunc   func(ctx context.Context, opts ReadyOpts) ([]BdIssue, error)

	// List
	ListResult []BdIssue
	ListErr    error
	ListFunc   func(ctx context.Context, opts ListOpts) ([]BdIssue, error)

	// Blocked
	BlockedResult []BdIssue
	BlockedErr    error
	BlockedFunc   func(ctx context.Context) ([]BdIssue, error)

	// Stats
	StatsResult *BdStats
	StatsErr    error
	StatsFunc   func(ctx context.Context) (*BdStats, error)

	// GetIssue
	GetIssueResult *BdIssue
	GetIssueErr    error
	GetIssueFunc   func(ctx context.Context, id string) (*BdIssue, error)

	// GetIssueText
	GetIssueTextResult string
	GetIssueTextErr    error
	GetIssueTextFunc   func(ctx context.Context, id string) (string, error)

	// UpdateIssue
	UpdateIssueErr  error
	UpdateIssueFunc func(ctx context.Context, id string, opts UpdateOpts) error

	// UpdateExternalRef
	UpdateExternalRefErr  error
	UpdateExternalRefFunc func(ctx context.Context, id, ref string) error

	// CloseIssue
	CloseIssueErr  error
	CloseIssueFunc func(ctx context.Context, id, reason string) error

	// BackendName
	BackendNameResult string
	BackendNameFunc   func() string
}

// NewMockTracker returns a MockIssueTracker with zero-value defaults.
func NewMockTracker() *MockIssueTracker {
	return &MockIssueTracker{}
}

// MockTrackerWithReady returns a MockIssueTracker pre-configured with Ready results.
func MockTrackerWithReady(issues []BdIssue) *MockIssueTracker {
	return &MockIssueTracker{ReadyResult: issues}
}

func (m *MockIssueTracker) record(method string, args ...interface{}) {
	m.Calls = append(m.Calls, MockTrackerCall{Method: method, Args: args})
}

// Ready implements IssueTracker.
func (m *MockIssueTracker) Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error) {
	m.mu.Lock()
	m.record("Ready", opts)
	fn := m.ReadyFunc
	result, resultErr := m.ReadyResult, m.ReadyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// List implements IssueTracker.
func (m *MockIssueTracker) List(ctx context.Context, opts ListOpts) ([]BdIssue, error) {
	m.mu.Lock()
	m.record("List", opts)
	fn := m.ListFunc
	result, resultErr := m.ListResult, m.ListErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return result, resultErr
}

// Blocked implements IssueTracker.
func (m *MockIssueTracker) Blocked(ctx context.Context) ([]BdIssue, error) {
	m.mu.Lock()
	m.record("Blocked")
	fn := m.BlockedFunc
	result, resultErr := m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return result, resultErr
}

// Stats implements IssueTracker.
func (m *MockIssueTracker) Stats(ctx context.Context) (*BdStats, error) {
	m.mu.Lock()
	m.record("Stats")
	fn := m.StatsFunc
	result, resultErr := m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return result, resultErr
}

// GetIssue implements IssueTracker.
func (m *MockIssueTracker) GetIssue(ctx context.Context, id string) (*BdIssue, error) {
	m.mu.Lock()
	m.record("GetIssue", id)
	fn := m.GetIssueFunc
	result, resultErr := m.GetIssueResult, m.GetIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return result, resultErr
}

// GetIssueText implements IssueTracker.
func (m *MockIssueTracker) GetIssueText(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	m.record("GetIssueText", id)
	fn := m.GetIssueTextFunc
	result, resultErr := m.GetIssueTextResult, m.GetIssueTextErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return result, resultErr
}

// UpdateIssue implements IssueTracker.
func (m *MockIssueTracker) UpdateIssue(ctx context.Context, id string, opts UpdateOpts) error {
	m.mu.Lock()
	m.record("UpdateIssue", id, opts)
	fn := m.UpdateIssueFunc
	resultErr := m.UpdateIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, opts)
	}
	return resultErr
}

// UpdateExternalRef implements IssueTracker.
func (m *MockIssueTracker) UpdateExternalRef(ctx context.Context, id, ref string) error {
	m.mu.Lock()
	m.record("UpdateExternalRef", id, ref)
	fn := m.UpdateExternalRefFunc
	resultErr := m.UpdateExternalRefErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, ref)
	}
	return resultErr
}

// CloseIssue implements IssueTracker.
func (m *MockIssueTracker) CloseIssue(ctx context.Context, id, reason string) error {
	m.mu.Lock()
	m.record("CloseIssue", id, reason)
	fn := m.CloseIssueFunc
	resultErr := m.CloseIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, reason)
	}
	return resultErr
}

// BackendName implements IssueTracker.
func (m *MockIssueTracker) BackendName() string {
	m.mu.Lock()
	m.record("BackendName")
	fn := m.BackendNameFunc
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
func (m *MockIssueTracker) CallCount(method string) int {
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
func (m *MockIssueTracker) Called(method string) bool {
	return m.CallCount(method) > 0
}

// --- Tests for MockIssueTracker ---

func TestMockIssueTracker_RecordsCalls(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	_, _ = m.Ready(ctx, ReadyOpts{Limit: 10})
	_, _ = m.GetIssue(ctx, "task-1")
	_ = m.CloseIssue(ctx, "task-1", "done")

	if len(m.Calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(m.Calls))
	}
	if m.Calls[0].Method != "Ready" {
		t.Errorf("call 0: got %q, want Ready", m.Calls[0].Method)
	}
	if m.Calls[1].Method != "GetIssue" {
		t.Errorf("call 1: got %q, want GetIssue", m.Calls[1].Method)
	}
	if m.Calls[2].Method != "CloseIssue" {
		t.Errorf("call 2: got %q, want CloseIssue", m.Calls[2].Method)
	}
	// Verify args recorded for GetIssue
	if id, ok := m.Calls[1].Args[0].(string); !ok || id != "task-1" {
		t.Errorf("GetIssue arg: got %v, want task-1", m.Calls[1].Args[0])
	}
}

func TestMockIssueTracker_ReturnsConfiguredResults(t *testing.T) {
	issues := []BdIssue{{ID: "t-1", Title: "Test"}}
	m := MockTrackerWithReady(issues)
	m.GetIssueResult = &BdIssue{ID: "t-2", Title: "Detail"}
	m.BackendNameResult = "test-backend"

	ctx := context.Background()

	got, err := m.Ready(ctx, ReadyOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t-1" {
		t.Errorf("Ready: got %v, want [{ID:t-1}]", got)
	}

	issue, err := m.GetIssue(ctx, "t-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.ID != "t-2" {
		t.Errorf("GetIssue: got %q, want t-2", issue.ID)
	}

	if name := m.BackendName(); name != "test-backend" {
		t.Errorf("BackendName: got %q, want test-backend", name)
	}
}

func TestMockIssueTracker_FuncOverride(t *testing.T) {
	m := NewMockTracker()
	m.ReadyResult = []BdIssue{{ID: "static"}}
	m.ReadyFunc = func(_ context.Context, opts ReadyOpts) ([]BdIssue, error) {
		return []BdIssue{{ID: "dynamic", Title: opts.ParentID}}, nil
	}

	ctx := context.Background()
	got, err := m.Ready(ctx, ReadyOpts{ParentID: "parent-1"})
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

func TestMockIssueTracker_CallCount(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	_, _ = m.Ready(ctx, ReadyOpts{})
	_, _ = m.Ready(ctx, ReadyOpts{})
	_, _ = m.List(ctx, ListOpts{})

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

func TestMockIssueTracker_BackendNameDefault(t *testing.T) {
	m := NewMockTracker()
	if name := m.BackendName(); name != "mock" {
		t.Errorf("default BackendName: got %q, want mock", name)
	}
}
