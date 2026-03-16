package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
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

// MockBDRunner records calls and returns configurable results.
type MockBDRunner struct {
	mu      sync.Mutex
	Calls   []mockBDCall
	Result  CommandResult
	RunFunc func(dir string, args ...string) CommandResult
}

type mockBDCall struct {
	Dir  string
	Args []string
}

func (m *MockBDRunner) Run(dir string, args ...string) CommandResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, mockBDCall{Dir: dir, Args: args})
	if m.RunFunc != nil {
		return m.RunFunc(dir, args...)
	}
	return m.Result
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
func NewTestDeps(t *testing.T) (*Deps, *MockGitRunner, *MockBDRunner, *MockExecRunner, *MockFileSystem, *MockIssueTracker) {
	t.Helper()
	git := &MockGitRunner{}
	bd := &MockBDRunner{}
	execR := &MockExecRunner{}
	fs := NewMockFileSystem()
	tracker := NewMockTracker()
	deps := &Deps{
		Git:     git,
		Exec:    execR,
		FS:      fs,
		Logger:  slog.Default(),
		BD:      bd,
		Clock:   func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Tracker: tracker,
	}
	return deps, git, bd, execR, fs, tracker
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
	if d.BD == nil {
		t.Error("BD is nil")
	}
	if d.Tracker == nil {
		t.Error("Tracker is nil")
	}
}

func TestDefaultDeps_TrackerField(t *testing.T) {
	d := DefaultDeps()
	if d.Tracker == nil {
		t.Fatal("Tracker is nil")
	}
	if d.Tracker.BackendName() != "beads" {
		t.Errorf("Tracker.BackendName() = %q, want %q", d.Tracker.BackendName(), "beads")
	}
}

func TestDefaultTracker_InitializedByInit(t *testing.T) {
	// The init() function in deps.go should have called setDefaultTracker,
	// so defaultTracker() should not panic and should return a valid tracker.
	tracker := defaultTracker()
	if tracker == nil {
		t.Fatal("defaultTracker() returned nil")
	}
	if tracker.BackendName() != "beads" {
		t.Errorf("defaultTracker().BackendName() = %q, want %q", tracker.BackendName(), "beads")
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

func TestFetchReadyIssues_UsesBDRunner(t *testing.T) {
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	issues := []BdIssue{
		{ID: "test-1", Title: "Test task", Status: "open"},
	}
	jsonData, _ := json.Marshal(issues)

	mock := &MockBDRunner{
		Result: CommandResult{Stdout: string(jsonData)},
	}
	defaultDeps = &Deps{BD: mock}

	got, err := fetchReadyIssues("epic-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "test-1" {
		t.Errorf("got %v, want [{ID:test-1 ...}]", got)
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 BD call, got %d", len(mock.Calls))
	}
	args := mock.Calls[0].Args
	if !slicesEqual(args, []string{"ready", "--json", "--limit", "100", "--parent", "epic-1"}) {
		t.Errorf("BD args = %v", args)
	}
}

func TestFetchReadyIssues_NoParent(t *testing.T) {
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	mock := &MockBDRunner{
		Result: CommandResult{Stdout: "[]"},
	}
	defaultDeps = &Deps{BD: mock}

	_, err := fetchReadyIssues("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := mock.Calls[0].Args
	if !slicesEqual(args, []string{"ready", "--json", "--limit", "100"}) {
		t.Errorf("BD args = %v (should not have --parent)", args)
	}
}

func TestFetchUnclosedIssueIDs_UsesBDRunner(t *testing.T) {
	orig := defaultDeps
	t.Cleanup(func() { defaultDeps = orig })

	issues := []BdIssue{
		{ID: "open-1", Status: "open"},
		{ID: "closed-1", Status: "closed"},
		{ID: "review-1", Status: "review"},
	}
	jsonData, _ := json.Marshal(issues)

	mock := &MockBDRunner{
		Result: CommandResult{Stdout: string(jsonData)},
	}
	defaultDeps = &Deps{BD: mock}

	got, err := fetchUnclosedIssueIDs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got["open-1"] {
		t.Error("expected open-1 in unclosed set")
	}
	if got["closed-1"] {
		t.Error("closed-1 should not be in unclosed set")
	}
	if !got["review-1"] {
		t.Error("expected review-1 in unclosed set")
	}
}

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
