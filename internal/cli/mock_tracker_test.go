package cli

import (
	"context"
	"sync"
	"testing"
)

// Compile-time interface check.
var _ IssueTracker = (*MockIssueTracker)(nil)

// mockTrackerCall records a single method invocation.
type mockTrackerCall struct {
	Method string
	Args   []interface{}
}

// MockIssueTracker implements IssueTracker for unit tests.
type MockIssueTracker struct {
	mu    sync.Mutex
	Calls []mockTrackerCall

	// RunCommand (from IssueBackend)
	RunCommandResult string
	RunCommandErr    error
	RunCommandFunc   func(dir string, args ...string) (string, error)

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
	StatsResult BdStats
	StatsErr    error
	StatsFunc   func(ctx context.Context) (BdStats, error)

	// GetIssue
	GetIssueResult *BdIssue
	GetIssueErr    error
	GetIssueFunc   func(ctx context.Context, id string) (*BdIssue, error)

	// GetIssueText
	GetIssueTextResult string
	GetIssueTextErr    error
	GetIssueTextFunc   func(ctx context.Context, id string) (string, error)

	// UpdateStatus
	UpdateStatusErr  error
	UpdateStatusFunc func(ctx context.Context, id, status, assignee string) error

	// UpdateExternalRef
	UpdateExternalRefErr  error
	UpdateExternalRefFunc func(ctx context.Context, id, ref string) error

	// CloseIssue
	CloseIssueErr  error
	CloseIssueFunc func(ctx context.Context, id, reason string) error

	// SyncStatus
	SyncStatusResult string
	SyncStatusErr    error
	SyncStatusFunc   func(ctx context.Context) (string, error)

	// BackendName
	BackendNameResult string
	BackendNameFunc   func() string
}

func (m *MockIssueTracker) RunCommand(dir string, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "RunCommand", Args: []interface{}{dir, args}})
	if m.RunCommandFunc != nil {
		return m.RunCommandFunc(dir, args...)
	}
	return m.RunCommandResult, m.RunCommandErr
}

func (m *MockIssueTracker) Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Ready", Args: []interface{}{ctx, opts}})
	if m.ReadyFunc != nil {
		return m.ReadyFunc(ctx, opts)
	}
	return m.ReadyResult, m.ReadyErr
}

func (m *MockIssueTracker) List(ctx context.Context, opts ListOpts) ([]BdIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "List", Args: []interface{}{ctx, opts}})
	if m.ListFunc != nil {
		return m.ListFunc(ctx, opts)
	}
	return m.ListResult, m.ListErr
}

func (m *MockIssueTracker) Blocked(ctx context.Context) ([]BdIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Blocked", Args: []interface{}{ctx}})
	if m.BlockedFunc != nil {
		return m.BlockedFunc(ctx)
	}
	return m.BlockedResult, m.BlockedErr
}

func (m *MockIssueTracker) Stats(ctx context.Context) (BdStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Stats", Args: []interface{}{ctx}})
	if m.StatsFunc != nil {
		return m.StatsFunc(ctx)
	}
	return m.StatsResult, m.StatsErr
}

func (m *MockIssueTracker) GetIssue(ctx context.Context, id string) (*BdIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "GetIssue", Args: []interface{}{ctx, id}})
	if m.GetIssueFunc != nil {
		return m.GetIssueFunc(ctx, id)
	}
	return m.GetIssueResult, m.GetIssueErr
}

func (m *MockIssueTracker) GetIssueText(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "GetIssueText", Args: []interface{}{ctx, id}})
	if m.GetIssueTextFunc != nil {
		return m.GetIssueTextFunc(ctx, id)
	}
	return m.GetIssueTextResult, m.GetIssueTextErr
}

func (m *MockIssueTracker) UpdateStatus(ctx context.Context, id, status, assignee string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "UpdateStatus", Args: []interface{}{ctx, id, status, assignee}})
	if m.UpdateStatusFunc != nil {
		return m.UpdateStatusFunc(ctx, id, status, assignee)
	}
	return m.UpdateStatusErr
}

func (m *MockIssueTracker) UpdateExternalRef(ctx context.Context, id, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "UpdateExternalRef", Args: []interface{}{ctx, id, ref}})
	if m.UpdateExternalRefFunc != nil {
		return m.UpdateExternalRefFunc(ctx, id, ref)
	}
	return m.UpdateExternalRefErr
}

func (m *MockIssueTracker) CloseIssue(ctx context.Context, id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "CloseIssue", Args: []interface{}{ctx, id, reason}})
	if m.CloseIssueFunc != nil {
		return m.CloseIssueFunc(ctx, id, reason)
	}
	return m.CloseIssueErr
}

func (m *MockIssueTracker) SyncStatus(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "SyncStatus", Args: []interface{}{ctx}})
	if m.SyncStatusFunc != nil {
		return m.SyncStatusFunc(ctx)
	}
	return m.SyncStatusResult, m.SyncStatusErr
}

func (m *MockIssueTracker) BackendName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "BackendName", Args: nil})
	if m.BackendNameFunc != nil {
		return m.BackendNameFunc()
	}
	return m.BackendNameResult
}

// NewMockTracker returns a MockIssueTracker with BackendNameResult set to "mock".
func NewMockTracker() *MockIssueTracker {
	return &MockIssueTracker{BackendNameResult: "mock"}
}

// MockTrackerWithIssues returns a MockIssueTracker pre-populated with ready and list results.
func MockTrackerWithIssues(ready, list []BdIssue) *MockIssueTracker {
	return &MockIssueTracker{
		ReadyResult:       ready,
		ListResult:        list,
		BackendNameResult: "mock",
	}
}

// CallCount returns the number of recorded calls matching the given method name.
func (m *MockIssueTracker) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.Calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

// LastCall returns the most recent call matching the given method name, or nil.
func (m *MockIssueTracker) LastCall(method string) *mockTrackerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.Calls) - 1; i >= 0; i-- {
		if m.Calls[i].Method == method {
			c := m.Calls[i]
			return &c
		}
	}
	return nil
}

// --- Tests ---

func TestMockTracker_RecordsCalls(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()
	opts := ReadyOpts{Limit: 5, ParentID: "epic-1"}

	m.Ready(ctx, opts)

	if len(m.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.Calls))
	}
	if m.Calls[0].Method != "Ready" {
		t.Errorf("method = %q, want Ready", m.Calls[0].Method)
	}
	gotOpts, ok := m.Calls[0].Args[1].(ReadyOpts)
	if !ok {
		t.Fatal("Args[1] is not ReadyOpts")
	}
	if gotOpts.ParentID != "epic-1" {
		t.Errorf("ParentID = %q, want epic-1", gotOpts.ParentID)
	}
}

func TestMockTracker_FuncOverride(t *testing.T) {
	m := NewMockTracker()
	m.ReadyResult = []BdIssue{{ID: "default"}}
	m.ReadyFunc = func(_ context.Context, _ ReadyOpts) ([]BdIssue, error) {
		return []BdIssue{{ID: "override"}}, nil
	}

	got, err := m.Ready(context.Background(), ReadyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "override" {
		t.Errorf("got %v, want [{ID:override}]", got)
	}
}

func TestMockTracker_DefaultReturns(t *testing.T) {
	m := &MockIssueTracker{}
	ctx := context.Background()

	issues, err := m.Ready(ctx, ReadyOpts{})
	if issues != nil || err != nil {
		t.Errorf("Ready: got (%v, %v), want (nil, nil)", issues, err)
	}

	name := m.BackendName()
	if name != "" {
		t.Errorf("BackendName: got %q, want empty", name)
	}

	issue, err := m.GetIssue(ctx, "x")
	if issue != nil || err != nil {
		t.Errorf("GetIssue: got (%v, %v), want (nil, nil)", issue, err)
	}
}

func TestMockTracker_CallCount(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	m.Ready(ctx, ReadyOpts{})
	m.Ready(ctx, ReadyOpts{})
	m.Ready(ctx, ReadyOpts{})
	m.List(ctx, ListOpts{})

	if got := m.CallCount("Ready"); got != 3 {
		t.Errorf("CallCount(Ready) = %d, want 3", got)
	}
	if got := m.CallCount("List"); got != 1 {
		t.Errorf("CallCount(List) = %d, want 1", got)
	}
	if got := m.CallCount("Stats"); got != 0 {
		t.Errorf("CallCount(Stats) = %d, want 0", got)
	}
}

func TestMockTracker_LastCall(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	m.GetIssue(ctx, "id-1")
	m.GetIssue(ctx, "id-2")

	last := m.LastCall("GetIssue")
	if last == nil {
		t.Fatal("LastCall returned nil")
	}
	if id, ok := last.Args[1].(string); !ok || id != "id-2" {
		t.Errorf("last call id = %v, want id-2", last.Args[1])
	}

	if m.LastCall("Stats") != nil {
		t.Error("LastCall(Stats) should be nil for uncalled method")
	}
}

func TestMockTracker_ConcurrentSafety(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Ready(ctx, ReadyOpts{})
		}()
	}
	wg.Wait()

	if got := m.CallCount("Ready"); got != 10 {
		t.Errorf("CallCount(Ready) = %d, want 10", got)
	}
}
