package clitest

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestSmallHelpers(t *testing.T) {
	if got := *IntPtr(12); got != 12 {
		t.Fatalf("IntPtr() = %d", got)
	}
	if got := *BoolPtr(true); !got {
		t.Fatalf("BoolPtr() = false")
	}
	if got := MustJSON(map[string]string{"k": "v"}); got != `{"k":"v"}` {
		t.Fatalf("MustJSON() = %s", got)
	}
	if !SlicesEqual([]string{"a", "b"}, []string{"a", "b"}) {
		t.Fatalf("SlicesEqual() returned false for equal slices")
	}
	if SlicesEqual([]string{"a"}, []string{"a", "b"}) {
		t.Fatalf("SlicesEqual() returned true for different lengths")
	}
	if SlicesEqual([]string{"a", "c"}, []string{"a", "b"}) {
		t.Fatalf("SlicesEqual() returned true for different values")
	}
}

func TestGitEnvHelpers(t *testing.T) {
	t.Setenv("GIT_DIR", "/tmp/git-dir")
	t.Setenv("GIT_WORK_TREE", "/tmp/work-tree")
	t.Setenv("LOOM_KEEP", "yes")

	env := GitSafeEnv("EXTRA=value")
	for _, entry := range env {
		if entry == "GIT_DIR=/tmp/git-dir" || entry == "GIT_WORK_TREE=/tmp/work-tree" {
			t.Fatalf("GitSafeEnv kept git redirect var: %s", entry)
		}
	}
	if !containsEnv(env, "LOOM_KEEP=yes") || !containsEnv(env, "EXTRA=value") {
		t.Fatalf("GitSafeEnv missing expected entries: %v", env)
	}

	ClearGitEnvVars(t)
	if _, ok := os.LookupEnv("GIT_DIR"); ok {
		t.Fatalf("ClearGitEnvVars did not unset GIT_DIR")
	}
}

func TestMockRunners(t *testing.T) {
	git := &MockGitRunner{RunResult: cli.CommandResult{Stdout: "ok"}}
	if got := git.Run("/repo", "status").Stdout; got != "ok" {
		t.Fatalf("MockGitRunner.Run() = %q", got)
	}
	errBoom := errors.New("boom")
	git.WithOutput = errBoom
	if err := git.RunWithOutput("/repo", "fetch"); !errors.Is(err, errBoom) {
		t.Fatalf("MockGitRunner.RunWithOutput() = %v", err)
	}
	git.RunFunc = func(dir string, args ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: dir + ":" + args[0]}
	}
	if got := git.Run("/repo", "branch").Stdout; got != "/repo:branch" {
		t.Fatalf("MockGitRunner RunFunc result = %q", got)
	}

	execR := &MockExecRunner{Result: cli.CommandResult{Stdout: "default"}}
	if got := execR.Run("/repo", "git", "status").Stdout; got != "default" {
		t.Fatalf("MockExecRunner.Run() = %q", got)
	}
	execR.RunFunc = func(dir, name string, args ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: name + ":" + args[0]}
	}
	if got := execR.Run("/repo", "git", "log").Stdout; got != "git:log" {
		t.Fatalf("MockExecRunner RunFunc result = %q", got)
	}

	ctxRunner := &MockExecContextRunner{Result: cli.CommandResult{Stdout: "ctx"}}
	if got := ctxRunner.Run(context.Background(), "/repo", "git", "status").Stdout; got != "ctx" {
		t.Fatalf("MockExecContextRunner.Run() = %q", got)
	}
	ctxRunner.RunFunc = func(ctx context.Context, dir, name string, args ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: dir + ":" + name}
	}
	if got := ctxRunner.Run(context.Background(), "/repo", "git").Stdout; got != "/repo:git" {
		t.Fatalf("MockExecContextRunner RunFunc result = %q", got)
	}
}

func TestMockAgentInvoker(t *testing.T) {
	errInteractive := errors.New("interactive")
	errNonInteractive := errors.New("non-interactive")
	invoker := &MockAgentInvoker{
		InteractiveErr:    errInteractive,
		NonInteractiveErr: errNonInteractive,
	}
	if err := invoker.InvokeInteractive("/repo", "prompt", "agent"); !errors.Is(err, errInteractive) {
		t.Fatalf("InvokeInteractive() = %v", err)
	}
	if err := invoker.InvokeNonInteractive("/repo", "prompt", "agent", nil, nil); !errors.Is(err, errNonInteractive) {
		t.Fatalf("InvokeNonInteractive() = %v", err)
	}

	invoker.InteractiveFunc = func(workDir, prompt, agentName string) error {
		if workDir != "/repo" || prompt != "prompt" || agentName != "agent" {
			t.Fatalf("unexpected interactive args")
		}
		return nil
	}
	invoker.NonInteractiveFunc = func(workDir, prompt, agentName string, shutdown <-chan struct{}, collector *usage.Collector) error {
		if workDir != "/repo" || prompt != "prompt" || agentName != "agent" {
			t.Fatalf("unexpected non-interactive args")
		}
		return nil
	}
	if err := invoker.InvokeInteractive("/repo", "prompt", "agent"); err != nil {
		t.Fatalf("InvokeInteractive func path = %v", err)
	}
	if err := invoker.InvokeNonInteractive("/repo", "prompt", "agent", nil, nil); err != nil {
		t.Fatalf("InvokeNonInteractive func path = %v", err)
	}
}

func TestMockFileSystem(t *testing.T) {
	fs := NewMockFileSystem()
	if _, err := fs.ReadFile("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile missing = %v", err)
	}
	if _, err := fs.Stat("/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat missing = %v", err)
	}
	if err := fs.MkdirAll("/dir", 0755); err != nil {
		t.Fatalf("MkdirAll() = %v", err)
	}
	if err := fs.WriteFile("/dir/file", []byte("data"), 0644); err != nil {
		t.Fatalf("WriteFile() = %v", err)
	}
	if data, err := fs.ReadFile("/dir/file"); err != nil || string(data) != "data" {
		t.Fatalf("ReadFile() = %q, %v", data, err)
	}
	if _, err := fs.Stat("/dir"); err != nil {
		t.Fatalf("Stat dir = %v", err)
	}
	if _, err := fs.Stat("/dir/file"); err != nil {
		t.Fatalf("Stat file = %v", err)
	}
	if err := fs.Remove("/dir/file"); err != nil {
		t.Fatalf("Remove() = %v", err)
	}
	if _, err := fs.ReadFile("/dir/file"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ReadFile removed = %v", err)
	}
}

func TestNewTestDepsAndExecBridge(t *testing.T) {
	deps, git, execR, fs, tracker := NewTestDeps(t)
	if deps.Git != git || deps.Exec != execR || deps.FS != fs || deps.IssueBackend != tracker {
		t.Fatalf("NewTestDeps did not wire mock dependencies")
	}
	if got := deps.Clock(); !got.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Clock() = %s", got)
	}
	if path, err := deps.LookPath("git"); err != nil || path != "/usr/bin/git" {
		t.Fatalf("LookPath() = %q, %v", path, err)
	}

	bridge := &ExecBridgeGitRunner{Exec: execR}
	execR.Result = cli.CommandResult{Stdout: "git output"}
	if got := bridge.Run("/repo", "status").Stdout; got != "git output" {
		t.Fatalf("ExecBridgeGitRunner.Run() = %q", got)
	}
	if err := bridge.RunWithOutput("/repo", "status"); err != nil {
		t.Fatalf("ExecBridgeGitRunner.RunWithOutput() = %v", err)
	}
}

func TestMockIssueBackendDefaultPaths(t *testing.T) {
	ctx := context.Background()
	m := NewMockIssueBackend()
	m.GetResult = &backend.IssueDetailData{}
	m.ListResult = []backend.IssueData{{ID: "list"}}
	m.ReadyResult = []workitems.IssueSummary{{ID: "ready"}}
	m.BlockedResult = []workitems.IssueSummary{{ID: "blocked"}}
	m.StatsResult = &workitems.Stats{}
	m.SearchResult = []workitems.IssueSummary{{ID: "search"}}
	m.CreateResult = &backend.IssueData{ID: "created"}
	m.CloseResult = &backend.CloseResult{}
	m.ListCommentsResult = []*workitems.Comment{{ID: 1}}
	m.AddCommentResult = &workitems.Comment{ID: 2}
	m.ListEventsResult = []*workitems.Event{{ID: 3}}
	m.BackendNameResult = "mock-backend"

	callDefaults(t, ctx, m)

	wantMethods := []string{
		"Get", "List", "Ready", "Blocked", "Stats", "Search",
		"Create", "Update", "ClaimIssue", "Close", "Reopen", "Delete",
		"AddDependency", "RemoveDependency", "ListComments", "AddComment", "ListEvents",
		"BackendName",
	}
	for _, method := range wantMethods {
		if !m.Called(method) {
			t.Fatalf("expected %s to be recorded", method)
		}
		if got := m.CallCount(method); got != 1 {
			t.Fatalf("CallCount(%s) = %d", method, got)
		}
	}
	if got := m.BackendName(); got != "mock-backend" {
		t.Fatalf("BackendName() = %q", got)
	}
	if got := m.CallCount("BackendName"); got != 2 {
		t.Fatalf("CallCount(BackendName) after second call = %d", got)
	}
}

func TestMockIssueBackendFunctionPaths(t *testing.T) {
	ctx := context.Background()
	m := NewMockIssueBackend()
	m.GetFn = func(ctx context.Context, id string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{ID: id}}, nil
	}
	m.UpdateFn = func(ctx context.Context, id string, params backend.UpdateParams) error {
		return errors.New(id)
	}
	m.BackendNameFn = func() string {
		return "custom"
	}

	detail, err := m.Get(ctx, "task-1")
	if err != nil || detail.ID != "task-1" {
		t.Fatalf("Get func path = %+v, %v", detail, err)
	}
	if err := m.Update(ctx, "task-2", backend.UpdateParams{}); err == nil || err.Error() != "task-2" {
		t.Fatalf("Update func path = %v", err)
	}
	if got := m.BackendName(); got != "custom" {
		t.Fatalf("BackendName func path = %q", got)
	}
}

func callDefaults(t *testing.T, ctx context.Context, m *MockIssueBackend) {
	t.Helper()
	mustNoErr := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
	}

	if _, err := m.Get(ctx, "id"); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if _, err := m.List(ctx, backend.ListOpts{}); err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if _, err := m.Ready(ctx, workitems.AvailabilityQuery{}); err != nil {
		t.Fatalf("Ready returned error: %v", err)
	}
	if _, err := m.Blocked(ctx, workitems.AvailabilityQuery{}); err != nil {
		t.Fatalf("Blocked returned error: %v", err)
	}
	if _, err := m.Stats(ctx); err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}
	if _, err := m.Search(ctx, workitems.SearchQuery{Query: "query", Limit: 10}); err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if created, err := m.Create(ctx, backend.CreateParams{}); err != nil || created.ID != "created" {
		t.Fatalf("Create returned %+v, %v", created, err)
	}
	mustNoErr("Update", m.Update(ctx, "id", backend.UpdateParams{}))
	mustNoErr("ClaimIssue", m.ClaimIssue(ctx, "id", time.Second))
	if _, err := m.Close(ctx, "id", backend.CloseParams{}); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	mustNoErr("Reopen", m.Reopen(ctx, "id", backend.ReopenParams{}))
	mustNoErr("Delete", m.Delete(ctx, backend.DeleteParams{}))
	mustNoErr("AddDependency", m.AddDependency(ctx, workitems.AddDependencyCommand{}))
	mustNoErr("RemoveDependency", m.RemoveDependency(ctx, workitems.RemoveDependencyCommand{}))
	if _, err := m.ListComments(ctx, workitems.ListCommentsQuery{IssueID: "id"}); err != nil {
		t.Fatalf("ListComments returned error: %v", err)
	}
	if _, err := m.AddComment(ctx, workitems.AddCommentCommand{}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}
	if _, err := m.ListEvents(ctx, workitems.ListEventsQuery{IssueID: "id", Limit: 10}); err != nil {
		t.Fatalf("ListEvents returned error: %v", err)
	}
	if got := m.BackendName(); got != "mock-backend" {
		t.Fatalf("BackendName returned %q", got)
	}
	if !reflect.DeepEqual(m.Calls[0].Args, []interface{}{"id"}) {
		t.Fatalf("first call args = %#v", m.Calls[0].Args)
	}
}

func containsEnv(env []string, target string) bool {
	for _, entry := range env {
		if entry == target {
			return true
		}
	}
	return false
}
