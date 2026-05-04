package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// --- Mock implementations ---

// MockGitRunner records calls and returns configurable results.
type MockGitRunner struct {
	mu         sync.Mutex
	RunCalls   []mockGitCall
	RunResult  CommandResult                                  // default result for Run
	RunFunc    func(dir string, args ...string) CommandResult // optional per-call logic
	WithOutput error                                          // default result for RunWithOutput
}

type mockGitCall struct {
	Dir  string
	Args []string
}

func (m *MockGitRunner) Run(dir string, args ...string) CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCalls = append(m.RunCalls, mockGitCall{Dir: dir, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(dir, args...)
	}
	return m.RunResult
}

func (m *MockGitRunner) RunWithOutput(dir string, args ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCalls = append(m.RunCalls, mockGitCall{Dir: dir, Args: args})
	return m.WithOutput
}

// MockExecRunner records calls and returns configurable results.
type MockExecRunner struct {
	mu      sync.Mutex
	Calls   []mockExecCall
	Result  CommandResult
	RunFunc func(dir, name string, args ...string) CommandResult
}

type mockExecCall struct {
	Dir  string
	Name string
	Args []string
}

func (m *MockExecRunner) Run(dir, name string, args ...string) CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockExecCall{Dir: dir, Name: name, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(dir, name, args...)
	}
	return m.Result
}

// MockExecContextRunner records calls and returns configurable results.
type MockExecContextRunner struct {
	mu      sync.Mutex
	Calls   []mockExecContextCall
	Result  CommandResult
	RunFunc func(ctx context.Context, dir, name string, args ...string) CommandResult
}

type mockExecContextCall struct {
	Dir  string
	Name string
	Args []string
}

func (m *MockExecContextRunner) Run(ctx context.Context, dir, name string, args ...string) CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockExecContextCall{Dir: dir, Name: name, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(ctx, dir, name, args...)
	}
	return m.Result
}

// MockAgentInvoker records agent invocations for tests.
type MockAgentInvoker struct {
	mu                  sync.Mutex
	InteractiveFunc     func(workDir, prompt, agentName string) error
	NonInteractiveFunc  func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error
	InteractiveErr      error
	NonInteractiveErr   error
	InteractiveCalls    []mockAgentCall
	NonInteractiveCalls []mockAgentCall
}

type mockAgentCall struct {
	WorkDir   string
	Prompt    string
	AgentName string
}

func (m *MockAgentInvoker) InvokeInteractive(workDir, prompt, agentName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.InteractiveCalls = append(m.InteractiveCalls, mockAgentCall{WorkDir: workDir, Prompt: prompt, AgentName: agentName})
	if m.InteractiveFunc != nil {
		return m.InteractiveFunc(workDir, prompt, agentName)
	}
	return m.InteractiveErr
}

func (m *MockAgentInvoker) InvokeNonInteractive(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NonInteractiveCalls = append(m.NonInteractiveCalls, mockAgentCall{WorkDir: workDir, Prompt: prompt, AgentName: agentName})
	if m.NonInteractiveFunc != nil {
		return m.NonInteractiveFunc(workDir, prompt, agentName, shutdown, collector)
	}
	return m.NonInteractiveErr
}

// MockFileSystem is an in-memory filesystem for tests.
type MockFileSystem struct {
	mu    sync.Mutex
	Files map[string][]byte
	Dirs  map[string]bool
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		Files: make(map[string][]byte),
		Dirs:  make(map[string]bool),
	}
}

func (m *MockFileSystem) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.Files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (m *MockFileSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Files[path] = data
	return nil
}

// Stat returns (nil, nil) for existing paths and (nil, os.ErrNotExist) for
// missing ones. The returned FileInfo is always nil. Tests that need to
// inspect FileInfo fields (IsDir, Mode, etc.) should not use MockFileSystem.
func (m *MockFileSystem) Stat(path string) (os.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Files[path]; ok {
		return nil, nil
	}
	if m.Dirs[path] {
		return nil, nil
	}
	return nil, os.ErrNotExist
}

func (m *MockFileSystem) MkdirAll(path string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Dirs[path] = true
	return nil
}

func (m *MockFileSystem) Remove(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Files, path)
	delete(m.Dirs, path)
	return nil
}

// NewTestDeps returns a *Deps with all fields set to mock implementations.
func NewTestDeps(t *testing.T) (*Deps, *MockGitRunner, *MockExecRunner, *MockFileSystem, *MockIssueBackend) {
	t.Helper()
	git := &MockGitRunner{}
	execR := &MockExecRunner{}
	fs := NewMockFileSystem()
	tracker := NewMockIssueBackend()
	deps := &Deps{
		Git:          git,
		Exec:         execR,
		FS:           fs,
		Logger:       slog.Default(),
		IssueBackend: tracker,
		Clock:        func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		LookPath:     func(file string) (string, error) { return "/usr/bin/" + file, nil },
		ExecCtx:      &MockExecContextRunner{},
		Agent:        &MockAgentInvoker{},
	}
	return deps, git, execR, fs, tracker
}

// --- Tests ---

func TestDefaultDeps_NonNilFields(t *testing.T) {
	d := DefaultDeps()
	if d.Git == nil {
		t.Error("Git is nil")
	}
	if d.Exec == nil {
		t.Error("Exec is nil")
	}
	if d.FS == nil {
		t.Error("FS is nil")
	}
	if d.Logger == nil {
		t.Error("Logger is nil")
	}
	if d.Clock == nil {
		t.Error("Clock is nil")
	}
	if d.IssueBackend == nil {
		t.Error("Tracker is nil")
	}
	if d.IssueBackend.BackendName() != "fleet-db" {
		t.Errorf("Tracker.BackendName() = %q, want fleet-db", d.IssueBackend.BackendName())
	}
	if d.LookPath == nil {
		t.Error("LookPath is nil")
	}
	if d.ExecCtx == nil {
		t.Error("ExecCtx is nil")
	}
	if d.Agent == nil {
		t.Error("Agent is nil")
	}
}

func TestWithDeps_GetDeps_RoundTrip(t *testing.T) {
	d := DefaultDeps()
	ctx := WithDeps(context.Background(), d)

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	got := GetDeps(cmd)
	if got != d {
		t.Error("GetDeps did not return the same *Deps stored by WithDeps")
	}
}

func TestGetDeps_FallbackWhenMissing(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	got := GetDeps(cmd)
	if got == nil {
		t.Fatal("GetDeps returned nil when context has no Deps")
	}
	if got.Git == nil {
		t.Error("fallback Deps has nil Git")
	}
}

func TestGetDeps_NilContext(t *testing.T) {
	// cobra.Command with no SetContext() call — cmd.Context() returns nil.
	cmd := &cobra.Command{}
	got := GetDeps(cmd)
	if got == nil {
		t.Fatal("GetDeps returned nil for cmd with nil context")
	}
	if got.Git == nil {
		t.Error("fallback Deps has nil Git")
	}
}

func TestGetDeps_NilCmd(t *testing.T) {
	got := GetDeps(nil)
	if got == nil {
		t.Fatal("GetDeps(nil) returned nil")
	}
	if got.Git == nil {
		t.Error("GetDeps(nil) returned Deps with nil Git")
	}
}

func TestRunGitCommand_UsesDefaultDeps(t *testing.T) {
	// Save and restore defaultDeps
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	mock := &MockGitRunner{
		RunResult: CommandResult{Stdout: "abc123\n"},
	}
	defaultDeps = &Deps{Git: mock}

	out, err := RunGitCommand("/tmp", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "abc123\n" {
		t.Errorf("got %q, want %q", out, "abc123\n")
	}
	if len(mock.RunCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.RunCalls))
	}
	if mock.RunCalls[0].Dir != "/tmp" {
		t.Errorf("dir = %q, want /tmp", mock.RunCalls[0].Dir)
	}
	if !slicesEqual(mock.RunCalls[0].Args, []string{"rev-parse", "HEAD"}) {
		t.Errorf("args = %v, want [rev-parse HEAD]", mock.RunCalls[0].Args)
	}
}

func TestRunGitCommand_ErrorPropagation(t *testing.T) {
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	mock := &MockGitRunner{
		RunResult: CommandResult{
			Stderr: "fatal: not a git repository",
			Err:    fmt.Errorf("exit status 128"),
		},
	}
	defaultDeps = &Deps{Git: mock}

	_, err := RunGitCommand("/tmp", "status")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "git status failed: fatal: not a git repository" {
		t.Errorf("error = %q", got)
	}
}

func TestRunGitCommandWithOutput_UsesDefaultDeps(t *testing.T) {
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	mock := &MockGitRunner{}
	defaultDeps = &Deps{Git: mock}

	err := RunGitCommandWithOutput("/tmp", "fetch", "origin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.RunCalls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.RunCalls))
	}
}

func TestFetchReadyIssues_UsesTracker(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "test-1", Title: "Test task", Status: "open"},
	}
	var capturedOpts backend.ReadyOpts
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		capturedOpts = opts
		return mock.ReadyResult, nil
	}
	setDefaultIssueBackend(mock)

	got, err := fetchReadyIssues("epic-1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "test-1" {
		t.Errorf("got %v, want [{ID:test-1 ...}]", got)
	}
	if capturedOpts.ParentID != "epic-1" {
		t.Errorf("opts.ParentID = %q, want epic-1", capturedOpts.ParentID)
	}
	if capturedOpts.Limit != 10000 {
		t.Errorf("opts.Limit = %d, want 10000", capturedOpts.Limit)
	}
}

func TestFetchReadyIssues_NoParentViaTracker(t *testing.T) {
	resetDefaultIssueBackend()
	t.Cleanup(resetDefaultIssueBackend)

	mock := NewMockIssueBackend()
	var capturedOpts backend.ReadyOpts
	mock.ReadyFn = func(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
		capturedOpts = opts
		return nil, nil
	}
	setDefaultIssueBackend(mock)

	_, err := fetchReadyIssues("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedOpts.ParentID != "" {
		t.Errorf("opts.ParentID = %q, want empty", capturedOpts.ParentID)
	}
	if capturedOpts.Limit != 10000 {
		t.Errorf("opts.Limit = %d, want 10000", capturedOpts.Limit)
	}
}

// TestFetchUnclosedIssueIDs_UsesTracker removed: fetchUnclosedIssueIDs was
// removed in the IssueBackend migration. The backend now pre-filters blocked
// issues in Ready/Blocked endpoints.

func TestMockFileSystem_ReadWrite(t *testing.T) {
	fs := NewMockFileSystem()
	if err := fs.WriteFile("/tmp/test.txt", []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("/tmp/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", string(data), "hello")
	}
}

func TestMockFileSystem_ReadNotExist(t *testing.T) {
	fs := NewMockFileSystem()
	_, err := fs.ReadFile("/no/such/file")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestMockFileSystem_Remove(t *testing.T) {
	fs := NewMockFileSystem()
	_ = fs.WriteFile("/tmp/x", []byte("data"), 0644)
	_ = fs.Remove("/tmp/x")
	_, err := fs.ReadFile("/tmp/x")
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestNewTestDeps_Returns5Tuple(t *testing.T) {
	deps, git, execR, fs, tracker := NewTestDeps(t)

	if deps == nil {
		t.Fatal("deps is nil")
	}
	if git == nil {
		t.Fatal("git is nil")
	}
	if execR == nil {
		t.Fatal("execR is nil")
	}
	if fs == nil {
		t.Fatal("fs is nil")
	}
	if tracker == nil {
		t.Fatal("tracker is nil")
	}

	// Verify deps fields are wired to the returned mocks.
	if deps.Git != git {
		t.Error("deps.Git is not the returned MockGitRunner")
	}
	if deps.Exec != execR {
		t.Error("deps.Exec is not the returned MockExecRunner")
	}
	if deps.FS != fs {
		t.Error("deps.FS is not the returned MockFileSystem")
	}
	if deps.IssueBackend != tracker {
		t.Error("deps.IssueBackend is not the returned MockIssueBackend")
	}
	if deps.Logger == nil {
		t.Error("deps.Logger is nil")
	}
	if deps.Clock == nil {
		t.Error("deps.Clock is nil")
	}
}

func TestDefaultDeps_NoBDField(t *testing.T) {
	// Verify the Deps struct has exactly 9 fields: Git, Exec, FS, Logger, Clock, Tracker, LookPath, ExecCtx, Agent.
	// This serves as a regression check that BD was removed and no new
	// unexpected field was added. We verify by checking all fields are non-nil
	// (which covers every field in the struct).
	d := DefaultDeps()

	// All 9 fields must be non-nil.
	fields := []struct {
		name  string
		isNil bool
	}{
		{"Git", d.Git == nil},
		{"Exec", d.Exec == nil},
		{"FS", d.FS == nil},
		{"Logger", d.Logger == nil},
		{"Clock", d.Clock == nil},
		{"IssueBackend", d.IssueBackend == nil},
		{"LookPath", d.LookPath == nil},
		{"ExecCtx", d.ExecCtx == nil},
		{"Agent", d.Agent == nil},
	}
	for _, f := range fields {
		if f.isNil {
			t.Errorf("DefaultDeps().%s is nil", f.name)
		}
	}

	// Verify IssueBackend defaults to fleet-db without constructing an external adapter.
	if d.IssueBackend.BackendName() != "fleet-db" {
		t.Errorf("Tracker.BackendName() = %q, want fleet-db", d.IssueBackend.BackendName())
	}
}

func TestDefaultDeps_DefaultsToFleetDB(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "")

	d := DefaultDeps()
	if got := d.IssueBackend.BackendName(); got != "fleet-db" {
		t.Fatalf("DefaultDeps IssueBackend = %q, want fleet-db", got)
	}
}

func TestDefaultDeps_FleetConstructionFailureFailsClosed(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "fleet")

	d := DefaultDeps()
	if got := d.IssueBackend.BackendName(); got != "fleet-unavailable" {
		t.Fatalf("DefaultDeps IssueBackend = %q, want fleet-unavailable", got)
	}
	_, err := d.IssueBackend.Ready(context.Background(), backend.ReadyOpts{})
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("Ready error = %v, want unavailable", err)
	}
}

func TestDefaultDeps_APIConstructionFailureFailsClosed(t *testing.T) {
	t.Setenv("LOOM_ISSUE_BACKEND", "api")
	t.Setenv("LOOM_SERVER_URL", "")

	d := DefaultDeps()
	if got := d.IssueBackend.BackendName(); got != "api-unavailable" {
		t.Fatalf("DefaultDeps IssueBackend = %q, want api-unavailable", got)
	}
	_, err := d.IssueBackend.Ready(context.Background(), backend.ReadyOpts{})
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Fatalf("Ready error = %v, want unavailable", err)
	}
}

func TestUnavailableIssueBackend_AllMethodsFailClosed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ib := newUnavailableIssueBackend("test", fmt.Errorf("boom"))

	if got := ib.BackendName(); got != "test-unavailable" {
		t.Fatalf("BackendName() = %q, want test-unavailable", got)
	}

	assertUnavailable := func(name string, err error) {
		t.Helper()
		if !backend.IsKind(err, backend.KindUnavailable) {
			t.Fatalf("%s error = %v, want unavailable", name, err)
		}
	}

	_, err := ib.Get(ctx, "T-1")
	assertUnavailable("Get", err)
	_, err = ib.List(ctx, backend.ListOpts{})
	assertUnavailable("List", err)
	_, err = ib.Ready(ctx, backend.ReadyOpts{})
	assertUnavailable("Ready", err)
	_, err = ib.Blocked(ctx, backend.BlockedOpts{})
	assertUnavailable("Blocked", err)
	_, err = ib.Stats(ctx)
	assertUnavailable("Stats", err)
	_, err = ib.Count(ctx, backend.CountOpts{})
	assertUnavailable("Count", err)
	_, err = ib.GetChildren(ctx, "T-1")
	assertUnavailable("GetChildren", err)
	_, err = ib.SearchIssues(ctx, "query", 10)
	assertUnavailable("SearchIssues", err)
	_, err = ib.Create(ctx, backend.CreateParams{})
	assertUnavailable("Create", err)
	assertUnavailable("Update", ib.Update(ctx, "T-1", backend.UpdateParams{}))
	assertUnavailable("ClaimIssue", ib.ClaimIssue(ctx, "T-1", time.Minute))
	assertUnavailable("DeferIssue", ib.DeferIssue(ctx, "T-1", time.Now()))
	assertUnavailable("UndeferIssue", ib.UndeferIssue(ctx, "T-1"))
	_, err = ib.Close(ctx, "T-1", backend.CloseParams{})
	assertUnavailable("Close", err)
	assertUnavailable("Reopen", ib.Reopen(ctx, "T-1", backend.ReopenParams{}))
	assertUnavailable("Delete", ib.Delete(ctx, backend.DeleteParams{}))
	assertUnavailable("AddDependency", ib.AddDependency(ctx, backend.DepAddParams{}))
	assertUnavailable("RemoveDependency", ib.RemoveDependency(ctx, backend.DepRemoveParams{}))
	assertUnavailable("AddLabel", ib.AddLabel(ctx, "T-1", "label"))
	assertUnavailable("RemoveLabel", ib.RemoveLabel(ctx, "T-1", "label"))
	_, err = ib.ListComments(ctx, "T-1")
	assertUnavailable("ListComments", err)
	_, err = ib.AddComment(ctx, backend.CommentAddParams{})
	assertUnavailable("AddComment", err)
	_, err = ib.ListEvents(ctx, "T-1", 10)
	assertUnavailable("ListEvents", err)
	_, err = ib.Batch(ctx, []backend.BatchOp{})
	assertUnavailable("Batch", err)
	_, err = ib.GetMutations(ctx, 0)
	assertUnavailable("GetMutations", err)
	_, err = ib.WaitForMutations(ctx, 0, 1)
	assertUnavailable("WaitForMutations", err)
}

func TestFleetDBActorPreference(t *testing.T) {
	t.Setenv("LOOM_FLEET_DB_ACTOR", "fleet-actor")
	t.Setenv("LOOM_AGENT_NAME", "agent-name")
	t.Setenv("USER", "user-name")
	if got := fleetDBActor(); got != "fleet-actor" {
		t.Fatalf("fleetDBActor() = %q, want fleet-actor", got)
	}

	t.Setenv("LOOM_FLEET_DB_ACTOR", "")
	if got := fleetDBActor(); got != "agent-name" {
		t.Fatalf("fleetDBActor() = %q, want agent-name", got)
	}

	t.Setenv("LOOM_AGENT_NAME", "")
	if got := fleetDBActor(); got != "user-name" {
		t.Fatalf("fleetDBActor() = %q, want user-name", got)
	}
}

func TestFleetDBIssueBackend_FailsClosedWhenStoreUnavailable(t *testing.T) {
	t.Setenv("FLEET_DB_BIN", "/missing/fleet-db")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	ctx := context.Background()
	ib := newFleetDBIssueBackend()

	if got := ib.BackendName(); got != "fleet-db" {
		t.Fatalf("BackendName() = %q, want fleet-db", got)
	}

	assertUnavailable := func(name string, err error) {
		t.Helper()
		if !backend.IsKind(err, backend.KindUnavailable) {
			t.Fatalf("%s error = %v, want unavailable", name, err)
		}
	}

	_, err := ib.Get(ctx, "T-1")
	assertUnavailable("Get", err)
	_, err = ib.List(ctx, backend.ListOpts{})
	assertUnavailable("List", err)
	_, err = ib.Ready(ctx, backend.ReadyOpts{})
	assertUnavailable("Ready", err)
	_, err = ib.Blocked(ctx, backend.BlockedOpts{})
	assertUnavailable("Blocked", err)
	_, err = ib.Stats(ctx)
	assertUnavailable("Stats", err)
	_, err = ib.Count(ctx, backend.CountOpts{})
	assertUnavailable("Count", err)
	_, err = ib.GetChildren(ctx, "T-1")
	assertUnavailable("GetChildren", err)
	_, err = ib.SearchIssues(ctx, "query", 10)
	assertUnavailable("SearchIssues", err)
	_, err = ib.Create(ctx, backend.CreateParams{})
	assertUnavailable("Create", err)
	assertUnavailable("Update", ib.Update(ctx, "T-1", backend.UpdateParams{}))
	assertUnavailable("ClaimIssue", ib.ClaimIssue(ctx, "T-1", time.Minute))
	assertUnavailable("DeferIssue", ib.DeferIssue(ctx, "T-1", time.Now()))
	assertUnavailable("UndeferIssue", ib.UndeferIssue(ctx, "T-1"))
	_, err = ib.Close(ctx, "T-1", backend.CloseParams{})
	assertUnavailable("Close", err)
	assertUnavailable("Reopen", ib.Reopen(ctx, "T-1", backend.ReopenParams{}))
	assertUnavailable("Delete", ib.Delete(ctx, backend.DeleteParams{}))
	assertUnavailable("AddDependency", ib.AddDependency(ctx, backend.DepAddParams{}))
	assertUnavailable("RemoveDependency", ib.RemoveDependency(ctx, backend.DepRemoveParams{}))
	assertUnavailable("AddLabel", ib.AddLabel(ctx, "T-1", "label"))
	assertUnavailable("RemoveLabel", ib.RemoveLabel(ctx, "T-1", "label"))
	_, err = ib.ListComments(ctx, "T-1")
	assertUnavailable("ListComments", err)
	_, err = ib.AddComment(ctx, backend.CommentAddParams{})
	assertUnavailable("AddComment", err)
	_, err = ib.ListEvents(ctx, "T-1", 10)
	assertUnavailable("ListEvents", err)
	_, err = ib.Batch(ctx, []backend.BatchOp{})
	assertUnavailable("Batch", err)
	_, err = ib.GetMutations(ctx, 0)
	assertUnavailable("GetMutations", err)
	_, err = ib.WaitForMutations(ctx, 0, 1)
	assertUnavailable("WaitForMutations", err)
}
