package cli

import (
	"context"
	"sync"
	"testing"
)

// Compile-time interface check.
var _ IssueTracker = (*MockIssueTracker)(nil)

// mockTrackerCall records a single method invocation on MockIssueTracker.
type mockTrackerCall struct {
	Method string
	Args   []interface{}
}

// MockIssueTracker implements IssueTracker for tests. Each method has a
// Result/Err pair for static returns and an optional Func override for
// dynamic behavior. All calls are recorded thread-safely.
//
// Func overrides are called after releasing the mutex, so they may safely
// call other mock methods without deadlocking.
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
}

func (m *MockIssueTracker) RunCommand(dir string, args ...string) (string, error) {
	m.mu.Lock()
	callArgs := make([]interface{}, 0, 1+len(args))
	callArgs = append(callArgs, dir)
	for _, a := range args {
		callArgs = append(callArgs, a)
	}
	m.Calls = append(m.Calls, mockTrackerCall{Method: "RunCommand", Args: callArgs})
	fn := m.RunCommandFunc
	res, err := m.RunCommandResult, m.RunCommandErr
	m.mu.Unlock()
	if fn != nil {
		return fn(dir, args...)
	}
	return res, err
}

func (m *MockIssueTracker) Ready(ctx context.Context, opts ReadyOpts) ([]BdIssue, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Ready", Args: []interface{}{ctx, opts}})
	fn := m.ReadyFunc
	res, err := m.ReadyResult, m.ReadyErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return res, err
}

func (m *MockIssueTracker) List(ctx context.Context, opts ListOpts) ([]BdIssue, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "List", Args: []interface{}{ctx, opts}})
	fn := m.ListFunc
	res, err := m.ListResult, m.ListErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return res, err
}

func (m *MockIssueTracker) Blocked(ctx context.Context) ([]BdIssue, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Blocked", Args: []interface{}{ctx}})
	fn := m.BlockedFunc
	res, err := m.BlockedResult, m.BlockedErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return res, err
}

func (m *MockIssueTracker) Stats(ctx context.Context) (BdStats, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "Stats", Args: []interface{}{ctx}})
	fn := m.StatsFunc
	res, err := m.StatsResult, m.StatsErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return res, err
}

func (m *MockIssueTracker) GetIssue(ctx context.Context, id string) (*BdIssue, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "GetIssue", Args: []interface{}{ctx, id}})
	fn := m.GetIssueFunc
	res, err := m.GetIssueResult, m.GetIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return res, err
}

func (m *MockIssueTracker) GetIssueText(ctx context.Context, id string) (string, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "GetIssueText", Args: []interface{}{ctx, id}})
	fn := m.GetIssueTextFunc
	res, err := m.GetIssueTextResult, m.GetIssueTextErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id)
	}
	return res, err
}

func (m *MockIssueTracker) UpdateStatus(ctx context.Context, id, status, assignee string) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "UpdateStatus", Args: []interface{}{ctx, id, status, assignee}})
	fn := m.UpdateStatusFunc
	err := m.UpdateStatusErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, status, assignee)
	}
	return err
}

func (m *MockIssueTracker) UpdateExternalRef(ctx context.Context, id, ref string) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "UpdateExternalRef", Args: []interface{}{ctx, id, ref}})
	fn := m.UpdateExternalRefFunc
	err := m.UpdateExternalRefErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, ref)
	}
	return err
}

func (m *MockIssueTracker) CloseIssue(ctx context.Context, id, reason string) error {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "CloseIssue", Args: []interface{}{ctx, id, reason}})
	fn := m.CloseIssueFunc
	err := m.CloseIssueErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, id, reason)
	}
	return err
}

func (m *MockIssueTracker) SyncStatus(ctx context.Context) (string, error) {
	m.mu.Lock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "SyncStatus", Args: []interface{}{ctx}})
	fn := m.SyncStatusFunc
	res, err := m.SyncStatusResult, m.SyncStatusErr
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return res, err
}

func (m *MockIssueTracker) BackendName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockTrackerCall{Method: "BackendName", Args: nil})
	return m.BackendNameResult
}

// NewMockTracker returns a zero-value MockIssueTracker with BackendNameResult
// set to "mock".
func NewMockTracker() *MockIssueTracker {
	return &MockIssueTracker{BackendNameResult: "mock"}
}

// MockTrackerWithIssues returns a MockIssueTracker pre-populated with
// ReadyResult and ListResult.
func MockTrackerWithIssues(ready, list []BdIssue) *MockIssueTracker {
	return &MockIssueTracker{
		ReadyResult:       ready,
		ListResult:        list,
		BackendNameResult: "mock",
	}
}

// CallCount returns the number of recorded calls matching method.
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

// LastCall returns the most recent recorded call matching method, or nil.
func (m *MockIssueTracker) LastCall(method string) *mockTrackerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.Calls) - 1; i >= 0; i-- {
		if m.Calls[i].Method == method {
			c := m.Calls[i] // copy while holding the lock
			return &c
		}
	}
	return nil
}

// --- Tests ---

func TestMockTracker_RecordsCalls(t *testing.T) {
	m := NewMockTracker()
	opts := ReadyOpts{Limit: 10, ParentID: "epic-1"}
	_, _ = m.Ready(context.Background(), opts)

	if len(m.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.Calls))
	}
	if m.Calls[0].Method != "Ready" {
		t.Errorf("method = %q, want Ready", m.Calls[0].Method)
	}
	gotOpts, ok := m.Calls[0].Args[1].(ReadyOpts)
	if !ok {
		t.Fatal("args[1] is not ReadyOpts")
	}
	if gotOpts.ParentID != "epic-1" {
		t.Errorf("ParentID = %q, want epic-1", gotOpts.ParentID)
	}
}

func TestMockTracker_FuncOverride(t *testing.T) {
	m := NewMockTracker()
	m.ReadyResult = []BdIssue{{ID: "static"}}
	m.ReadyFunc = func(_ context.Context, _ ReadyOpts) ([]BdIssue, error) {
		return []BdIssue{{ID: "dynamic"}}, nil
	}

	got, err := m.Ready(context.Background(), ReadyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "dynamic" {
		t.Errorf("expected dynamic result, got %v", got)
	}
}

func TestMockTracker_DefaultReturns(t *testing.T) {
	m := &MockIssueTracker{}

	issues, err := m.Ready(context.Background(), ReadyOpts{})
	if issues != nil || err != nil {
		t.Errorf("Ready: got (%v, %v), want (nil, nil)", issues, err)
	}

	name := m.BackendName()
	if name != "" {
		t.Errorf("BackendName: got %q, want empty", name)
	}

	issue, err := m.GetIssue(context.Background(), "x")
	if issue != nil || err != nil {
		t.Errorf("GetIssue: got (%v, %v), want (nil, nil)", issue, err)
	}
}

func TestMockTracker_CallCount(t *testing.T) {
	m := NewMockTracker()
	ctx := context.Background()

	_, _ = m.Ready(ctx, ReadyOpts{})
	_, _ = m.Ready(ctx, ReadyOpts{})
	_, _ = m.Ready(ctx, ReadyOpts{})
	_, _ = m.List(ctx, ListOpts{})

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

	_, _ = m.GetIssue(ctx, "id-1")
	_, _ = m.GetIssue(ctx, "id-2")

	lc := m.LastCall("GetIssue")
	if lc == nil {
		t.Fatal("LastCall returned nil")
	}
	if id, ok := lc.Args[1].(string); !ok || id != "id-2" {
		t.Errorf("LastCall args[1] = %v, want id-2", lc.Args[1])
	}

	if got := m.LastCall("Stats"); got != nil {
		t.Errorf("LastCall(Stats) = %v, want nil", got)
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
			_, _ = m.Ready(ctx, ReadyOpts{})
		}()
	}
	wg.Wait()

	if got := m.CallCount("Ready"); got != 10 {
		t.Errorf("CallCount(Ready) = %d, want 10", got)
	}
}
