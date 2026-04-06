//go:build ignore

package cli

import (
	"context"
	"fmt"
	"testing"
)

// --- installExecMock tests ---

func TestInstallExecMock_SwapsDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	mock := &MockExecRunner{
		Result: CommandResult{Stdout: "mocked"},
	}

	// Use a sub-test so t.Cleanup fires when the sub-test finishes.
	var calledDuringSubtest bool
	t.Run("subtest", func(t *testing.T) {
		installExecMock(t, mock)

		// defaultDeps.Exec should now route through the mock.
		result := defaultDeps.Exec.Run(".", "echo", "hello")
		if result.Stdout != "mocked" {
			t.Errorf("defaultDeps.Exec.Run stdout = %q, want %q", result.Stdout, "mocked")
		}
		if len(mock.Calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(mock.Calls))
		}
		if mock.Calls[0].Name != "echo" {
			t.Errorf("call name = %q, want %q", mock.Calls[0].Name, "echo")
		}
		calledDuringSubtest = true
	})

	if !calledDuringSubtest {
		t.Fatal("subtest did not run")
	}

	// After the sub-test's cleanup, defaultDeps.Exec should be restored.
	// Verify the mock is no longer called.
	mock.Calls = nil
	_ = defaultDeps.Exec.Run(".", "true")
	if len(mock.Calls) != 0 {
		t.Error("defaultDeps.Exec still routes to mock after cleanup")
	}
}

// --- installLookPathMock tests ---

func TestInstallLookPathMock_SwapsDepsLookPath(t *testing.T) {
	origLookPath := defaultDeps.LookPath
	t.Cleanup(func() { defaultDeps.LookPath = origLookPath })

	called := false
	mockFn := func(file string) (string, error) {
		called = true
		return "/mock/bin/" + file, nil
	}

	t.Run("subtest", func(t *testing.T) {
		installLookPathMock(t, mockFn)

		path, err := defaultDeps.LookPath("git")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != "/mock/bin/git" {
			t.Errorf("defaultDeps.LookPath = %q, want %q", path, "/mock/bin/git")
		}
		if !called {
			t.Error("mock function was not called")
		}
	})

	// After cleanup, defaultDeps.LookPath should be restored to original.
	called = false
	_, _ = defaultDeps.LookPath("git")
	if called {
		t.Error("defaultDeps.LookPath still routes to mock after cleanup")
	}
}

func TestInstallLookPathMock_ErrorCase(t *testing.T) {
	origLookPath := defaultDeps.LookPath
	t.Cleanup(func() { defaultDeps.LookPath = origLookPath })

	wantErr := fmt.Errorf("not found")
	mockFn := func(file string) (string, error) {
		return "", wantErr
	}

	installLookPathMock(t, mockFn)

	_, err := defaultDeps.LookPath("nonexistent")
	if err != wantErr {
		t.Errorf("defaultDeps.LookPath error = %v, want %v", err, wantErr)
	}
}

// --- installExecContextMock tests ---

func TestInstallExecContextMock_SwapsDepsExecCtx(t *testing.T) {
	origExecCtx := defaultDeps.ExecCtx
	t.Cleanup(func() { defaultDeps.ExecCtx = origExecCtx })

	called := false
	mockFn := func(ctx context.Context, dir, name string, args ...string) CommandResult {
		called = true
		return CommandResult{Stdout: "ctx-mocked"}
	}

	t.Run("subtest", func(t *testing.T) {
		installExecContextMock(t, mockFn)

		result := defaultDeps.ExecCtx.Run(context.Background(), ".", "op", "read", "foo")
		if result.Stdout != "ctx-mocked" {
			t.Errorf("defaultDeps.ExecCtx.Run stdout = %q, want %q", result.Stdout, "ctx-mocked")
		}
		if !called {
			t.Error("mock function was not called")
		}
	})

	// After cleanup, the ExecCtx should be restored.
	called = false
	_ = defaultDeps.ExecCtx
	if called {
		t.Error("defaultDeps.ExecCtx still routes to mock after cleanup")
	}
}

func TestInstallExecContextMock_ReceivesArgs(t *testing.T) {
	origExecCtx := defaultDeps.ExecCtx
	t.Cleanup(func() { defaultDeps.ExecCtx = origExecCtx })

	var capturedDir, capturedName string
	var capturedArgs []string
	var capturedCtx context.Context

	mockFn := func(ctx context.Context, dir, name string, args ...string) CommandResult {
		capturedCtx = ctx
		capturedDir = dir
		capturedName = name
		capturedArgs = args
		return CommandResult{}
	}

	installExecContextMock(t, mockFn)

	ctx := context.WithValue(context.Background(), depsKey, "test-marker")
	defaultDeps.ExecCtx.Run(ctx, "/some/dir", "op", "read", "secret-name")

	if capturedCtx != ctx {
		t.Error("context was not passed through")
	}
	if capturedDir != "/some/dir" {
		t.Errorf("dir = %q, want %q", capturedDir, "/some/dir")
	}
	if capturedName != "op" {
		t.Errorf("name = %q, want %q", capturedName, "op")
	}
	if !slicesEqual(capturedArgs, []string{"read", "secret-name"}) {
		t.Errorf("args = %v, want [read secret-name]", capturedArgs)
	}
}

// --- MockExecContextRunner tests ---

func TestMockExecContextRunner_RecordsCalls(t *testing.T) {
	m := &MockExecContextRunner{
		Result: CommandResult{Stdout: "ok"},
	}

	ctx := context.Background()
	r1 := m.Run(ctx, "/dir1", "cmd1", "arg1", "arg2")
	r2 := m.Run(ctx, "/dir2", "cmd2")

	if r1.Stdout != "ok" {
		t.Errorf("r1.Stdout = %q, want %q", r1.Stdout, "ok")
	}
	if r2.Stdout != "ok" {
		t.Errorf("r2.Stdout = %q, want %q", r2.Stdout, "ok")
	}

	if len(m.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(m.Calls))
	}
	if m.Calls[0].Dir != "/dir1" || m.Calls[0].Name != "cmd1" || !slicesEqual(m.Calls[0].Args, []string{"arg1", "arg2"}) {
		t.Errorf("call[0] = %+v", m.Calls[0])
	}
	if m.Calls[1].Dir != "/dir2" || m.Calls[1].Name != "cmd2" || m.Calls[1].Args != nil {
		t.Errorf("call[1] = %+v", m.Calls[1])
	}
}

func TestMockExecContextRunner_RunFuncOverride(t *testing.T) {
	m := &MockExecContextRunner{
		Result: CommandResult{Stdout: "default"},
		RunFunc: func(ctx context.Context, dir, name string, args ...string) CommandResult {
			return CommandResult{Stdout: "overridden:" + name}
		},
	}

	result := m.Run(context.Background(), ".", "mycmd", "a")
	if result.Stdout != "overridden:mycmd" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "overridden:mycmd")
	}

	// Calls should still be recorded even when RunFunc is used.
	if len(m.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(m.Calls))
	}
	if m.Calls[0].Name != "mycmd" {
		t.Errorf("call name = %q, want %q", m.Calls[0].Name, "mycmd")
	}
}

func TestMockExecContextRunner_ErrorResult(t *testing.T) {
	wantErr := fmt.Errorf("exec failed")
	m := &MockExecContextRunner{
		Result: CommandResult{
			Stderr: "something went wrong",
			Err:    wantErr,
		},
	}

	result := m.Run(context.Background(), ".", "fail-cmd")
	if result.Err != wantErr {
		t.Errorf("err = %v, want %v", result.Err, wantErr)
	}
	if result.Stderr != "something went wrong" {
		t.Errorf("stderr = %q, want %q", result.Stderr, "something went wrong")
	}
}

func TestMockExecContextRunner_ImplementsInterface(t *testing.T) {
	// Compile-time check that MockExecContextRunner satisfies ExecContextRunner.
	var _ ExecContextRunner = (*MockExecContextRunner)(nil)
}

// --- defaultExecContextRunner interface satisfaction ---

func TestDefaultExecContextRunner_ImplementsInterface(t *testing.T) {
	// Compile-time check that defaultExecContextRunner satisfies ExecContextRunner.
	var _ ExecContextRunner = defaultExecContextRunner{}
}

// --- CommandMock.Run() implements ExecRunner ---

func TestCommandMock_Run_ImplementsExecRunner(t *testing.T) {
	// Compile-time check that *CommandMock satisfies ExecRunner.
	var _ ExecRunner = (*CommandMock)(nil)
}

func TestCommandMock_Run_DelegatesToExec(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "echo", Stdout: "hello"},
	})

	result := mock.Run("/tmp", "echo", "hello")

	if result.Stdout != "hello" {
		t.Errorf("Run stdout = %q, want %q", result.Stdout, "hello")
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "echo" {
		t.Errorf("call name = %q, want %q", calls[0].Name, "echo")
	}
	if calls[0].Dir != "/tmp" {
		t.Errorf("call dir = %q, want %q", calls[0].Dir, "/tmp")
	}
}

// --- FlexibleCommandMock.Run() implements ExecRunner ---

func TestFlexibleCommandMock_Run_ImplementsExecRunner(t *testing.T) {
	// Compile-time check that *FlexibleCommandMock satisfies ExecRunner.
	var _ ExecRunner = (*FlexibleCommandMock)(nil)
}

func TestFlexibleCommandMock_Run_DelegatesToExec(t *testing.T) {
	mock := NewFlexibleCommandMock(t)
	mock.AddStub("git", []string{"status"}, CommandResult{Stdout: "clean"})

	result := mock.Run("/repo", "git", "status")

	if result.Stdout != "clean" {
		t.Errorf("Run stdout = %q, want %q", result.Stdout, "clean")
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "git" {
		t.Errorf("call name = %q, want %q", calls[0].Name, "git")
	}
}

// --- CommandMock.Install() swaps defaultDeps.Exec ---

func TestCommandMock_Install_SwapsDefaultDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	var calledDuringSubtest bool
	t.Run("subtest", func(t *testing.T) {
		mock := NewCommandMock(t, []CommandStub{
			{Name: "echo", Stdout: "from-mock"},
		})
		mock.Install()

		// defaultDeps.Exec should now be the CommandMock.
		result := defaultDeps.Exec.Run(".", "echo", "hi")
		if result.Stdout != "from-mock" {
			t.Errorf("defaultDeps.Exec.Run stdout = %q, want %q", result.Stdout, "from-mock")
		}
		calledDuringSubtest = true
	})

	if !calledDuringSubtest {
		t.Fatal("subtest did not run")
	}

	// After subtest cleanup, defaultDeps.Exec should be restored.
	if defaultDeps.Exec == nil {
		t.Fatal("defaultDeps.Exec is nil after cleanup")
	}
}

// --- FlexibleCommandMock.Install() swaps defaultDeps.Exec ---

func TestFlexibleCommandMock_Install_SwapsDefaultDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	var calledDuringSubtest bool
	t.Run("subtest", func(t *testing.T) {
		mock := NewFlexibleCommandMock(t)
		mock.AddStub("ls", nil, CommandResult{Stdout: "file.txt"})
		mock.Install()

		// defaultDeps.Exec should now be the FlexibleCommandMock.
		result := defaultDeps.Exec.Run(".", "ls", "-la")
		if result.Stdout != "file.txt" {
			t.Errorf("defaultDeps.Exec.Run stdout = %q, want %q", result.Stdout, "file.txt")
		}
		calledDuringSubtest = true
	})

	if !calledDuringSubtest {
		t.Fatal("subtest did not run")
	}
}

// --- defaultGitRunner.Run() delegates to defaultDeps.Exec.Run() ---

func TestDefaultGitRunner_Run_DelegatesToDefaultDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	mock := &MockExecRunner{
		Result: CommandResult{Stdout: "abc123\n"},
	}
	defaultDeps.Exec = mock

	runner := defaultGitRunner{}
	result := runner.Run("/repo", "rev-parse", "HEAD")

	if result.Stdout != "abc123\n" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "abc123\n")
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	// defaultGitRunner.Run() should prepend "git" as the command name.
	if mock.Calls[0].Name != "git" {
		t.Errorf("call name = %q, want %q", mock.Calls[0].Name, "git")
	}
	if mock.Calls[0].Dir != "/repo" {
		t.Errorf("call dir = %q, want %q", mock.Calls[0].Dir, "/repo")
	}
	if !slicesEqual(mock.Calls[0].Args, []string{"rev-parse", "HEAD"}) {
		t.Errorf("call args = %v, want [rev-parse HEAD]", mock.Calls[0].Args)
	}
}

// --- defaultBDRunnerImpl.Run() delegates to defaultDeps.Exec.Run() ---

func TestDefaultBDRunnerImpl_Run_DelegatesToDefaultDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	mock := &MockExecRunner{
		Result: CommandResult{Stdout: "bd-output"},
	}
	defaultDeps.Exec = mock

	runner := defaultBDRunnerImpl{}
	result := runner.Run("/work", "list", "--json")

	if result.Stdout != "bd-output" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "bd-output")
	}
	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.Calls))
	}
	// defaultBDRunnerImpl.Run() should prepend "bd" as the command name.
	if mock.Calls[0].Name != "bd" {
		t.Errorf("call name = %q, want %q", mock.Calls[0].Name, "bd")
	}
	if mock.Calls[0].Dir != "/work" {
		t.Errorf("call dir = %q, want %q", mock.Calls[0].Dir, "/work")
	}
	if !slicesEqual(mock.Calls[0].Args, []string{"list", "--json"}) {
		t.Errorf("call args = %v, want [list --json]", mock.Calls[0].Args)
	}
}

// --- GetDeps(nil) returns the defaultDeps singleton (not a fresh DefaultDeps()) ---

func TestGetDeps_NilCmd_ReturnsSingleton(t *testing.T) {
	// GetDeps(nil) must return the exact same pointer as the package-level
	// defaultDeps, so that test-time swaps (e.g., installExecMock) are visible.
	got := GetDeps(nil)
	if got != defaultDeps {
		t.Error("GetDeps(nil) did not return the defaultDeps singleton pointer")
	}
}

// --- gitOutputMockRunner.Run() delegates to defaultDeps.Exec ---

func TestGitOutputMockRunner_Run_DelegatesToDefaultDepsExec(t *testing.T) {
	origExec := defaultDeps.Exec
	t.Cleanup(func() { defaultDeps.Exec = origExec })

	execMock := &MockExecRunner{
		Result: CommandResult{Stdout: "mock-git-output"},
	}
	defaultDeps.Exec = execMock

	runner := &gitOutputMockRunner{
		outputFn: func(dir string, args ...string) error { return nil },
	}

	result := runner.Run("/repo", "log", "--oneline")

	if result.Stdout != "mock-git-output" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "mock-git-output")
	}
	if len(execMock.Calls) != 1 {
		t.Fatalf("expected 1 call to defaultDeps.Exec, got %d", len(execMock.Calls))
	}
	if execMock.Calls[0].Name != "git" {
		t.Errorf("call name = %q, want %q", execMock.Calls[0].Name, "git")
	}
	if !slicesEqual(execMock.Calls[0].Args, []string{"log", "--oneline"}) {
		t.Errorf("call args = %v, want [log --oneline]", execMock.Calls[0].Args)
	}
}

// --- defaultExecRunner does not recurse through defaultDeps ---

func TestDefaultExecRunner_ImplementsExecRunner(t *testing.T) {
	// Compile-time check that defaultExecRunner satisfies ExecRunner.
	var _ ExecRunner = defaultExecRunner{}
}

// --- funcExecContextRunner correctly delegates ---

func TestFuncExecContextRunner_DelegatesToFunction(t *testing.T) {
	var capturedCtx context.Context
	var capturedDir, capturedName string
	var capturedArgs []string

	fn := func(ctx context.Context, dir, name string, args ...string) CommandResult {
		capturedCtx = ctx
		capturedDir = dir
		capturedName = name
		capturedArgs = args
		return CommandResult{Stdout: "func-result"}
	}

	runner := &funcExecContextRunner{fn: fn}
	ctx := context.WithValue(context.Background(), depsKey, "marker")
	result := runner.Run(ctx, "/dir", "op", "read", "secret")

	if result.Stdout != "func-result" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "func-result")
	}
	if capturedCtx != ctx {
		t.Error("context was not passed through")
	}
	if capturedDir != "/dir" {
		t.Errorf("dir = %q, want %q", capturedDir, "/dir")
	}
	if capturedName != "op" {
		t.Errorf("name = %q, want %q", capturedName, "op")
	}
	if !slicesEqual(capturedArgs, []string{"read", "secret"}) {
		t.Errorf("args = %v, want [read secret]", capturedArgs)
	}
}

func TestFuncExecContextRunner_ImplementsExecContextRunner(t *testing.T) {
	// Compile-time check that *funcExecContextRunner satisfies ExecContextRunner.
	var _ ExecContextRunner = (*funcExecContextRunner)(nil)
}

// --- NewTestDeps wiring for new fields ---

func TestNewTestDeps_LookPathField(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)

	if deps.LookPath == nil {
		t.Fatal("deps.LookPath is nil")
	}

	// Verify the default mock returns /usr/bin/<file>.
	path, err := deps.LookPath("git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/usr/bin/git" {
		t.Errorf("LookPath(git) = %q, want %q", path, "/usr/bin/git")
	}
}

func TestNewTestDeps_ExecCtxField(t *testing.T) {
	deps, _, _, _, _ := NewTestDeps(t)

	if deps.ExecCtx == nil {
		t.Fatal("deps.ExecCtx is nil")
	}

	// Verify it is a *MockExecContextRunner.
	mock, ok := deps.ExecCtx.(*MockExecContextRunner)
	if !ok {
		t.Fatalf("deps.ExecCtx type = %T, want *MockExecContextRunner", deps.ExecCtx)
	}

	// Verify it works (zero-value Result).
	result := mock.Run(context.Background(), ".", "test")
	if result.Err != nil {
		t.Errorf("unexpected error: %v", result.Err)
	}
	if len(mock.Calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.Calls))
	}
}

// --- installGitOutputMock tests ---

func TestInstallGitOutputMock_SwapsDepsGitAndVerifies(t *testing.T) {
	origGit := defaultDeps.Git
	t.Cleanup(func() { defaultDeps.Git = origGit })

	stubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
	}
	mock := NewOutputCommandMock(t, stubs)

	t.Run("subtest", func(t *testing.T) {
		installGitOutputMock(t, mock)

		err := defaultDeps.Git.RunWithOutput("/repo", "fetch", "origin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// After the subtest, defaultDeps.Git should be restored and Verify should have run.
	// The mock consumed exactly 1 of 1 stubs, so Verify passes silently.
}
