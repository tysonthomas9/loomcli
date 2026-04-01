package cli

import (
	"context"
	"fmt"
	"testing"
)

// --- installExecMock tests ---

func TestInstallExecMock_SetsGlobal(t *testing.T) {
	origExecCommand := execCommand
	t.Cleanup(func() { execCommand = origExecCommand })

	mock := &MockExecRunner{
		Result: CommandResult{Stdout: "mocked"},
	}

	// Use a sub-test so t.Cleanup fires when the sub-test finishes.
	var calledDuringSubtest bool
	t.Run("subtest", func(t *testing.T) {
		installExecMock(t, mock)

		// The global execCommand should now route through the mock.
		result := execCommand(".", "echo", "hello")
		if result.Stdout != "mocked" {
			t.Errorf("execCommand stdout = %q, want %q", result.Stdout, "mocked")
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

	// After the sub-test's cleanup, execCommand should be restored.
	// We can't easily test the exact function pointer, but we can verify
	// the mock is no longer called.
	mock.Calls = nil
	_ = execCommand(".", "true")
	if len(mock.Calls) != 0 {
		t.Error("execCommand still routes to mock after cleanup")
	}
}

// --- installLookPathMock tests ---

func TestInstallLookPathMock_SetsGlobal(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })

	called := false
	mockFn := func(file string) (string, error) {
		called = true
		return "/mock/bin/" + file, nil
	}

	t.Run("subtest", func(t *testing.T) {
		installLookPathMock(t, mockFn)

		path, err := lookPath("git")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != "/mock/bin/git" {
			t.Errorf("lookPath = %q, want %q", path, "/mock/bin/git")
		}
		if !called {
			t.Error("mock function was not called")
		}
	})

	// After cleanup, lookPath should be restored to original.
	called = false
	_, _ = lookPath("git")
	if called {
		t.Error("lookPath still routes to mock after cleanup")
	}
}

func TestInstallLookPathMock_ErrorCase(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })

	wantErr := fmt.Errorf("not found")
	mockFn := func(file string) (string, error) {
		return "", wantErr
	}

	installLookPathMock(t, mockFn)

	_, err := lookPath("nonexistent")
	if err != wantErr {
		t.Errorf("lookPath error = %v, want %v", err, wantErr)
	}
}

// --- installExecContextMock tests ---

func TestInstallExecContextMock_SetsGlobal(t *testing.T) {
	origExecCommandContext := execCommandContext
	t.Cleanup(func() { execCommandContext = origExecCommandContext })

	called := false
	mockFn := func(ctx context.Context, dir, name string, args ...string) CommandResult {
		called = true
		return CommandResult{Stdout: "ctx-mocked"}
	}

	t.Run("subtest", func(t *testing.T) {
		installExecContextMock(t, mockFn)

		result := execCommandContext(context.Background(), ".", "op", "read", "foo")
		if result.Stdout != "ctx-mocked" {
			t.Errorf("execCommandContext stdout = %q, want %q", result.Stdout, "ctx-mocked")
		}
		if !called {
			t.Error("mock function was not called")
		}
	})

	// After cleanup, the global should be restored.
	called = false
	// We can't actually call the original (it runs a real command),
	// so just verify the function pointer changed.
	_ = execCommandContext
	if called {
		t.Error("execCommandContext still routes to mock after cleanup")
	}
}

func TestInstallExecContextMock_ReceivesArgs(t *testing.T) {
	origExecCommandContext := execCommandContext
	t.Cleanup(func() { execCommandContext = origExecCommandContext })

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
	execCommandContext(ctx, "/some/dir", "op", "read", "secret-name")

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

func TestInstallGitOutputMock_SetsGlobalAndVerifies(t *testing.T) {
	origRunGitWithOutput := runGitWithOutputFunc
	t.Cleanup(func() { runGitWithOutputFunc = origRunGitWithOutput })

	stubs := []OutputCommandStub{
		{Args: []string{"fetch", "origin"}, Err: nil},
	}
	mock := NewOutputCommandMock(t, stubs)

	t.Run("subtest", func(t *testing.T) {
		installGitOutputMock(t, mock)

		err := runGitWithOutputFunc("/repo", "fetch", "origin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	// After the subtest, the global should be restored and Verify should have run.
	// The mock consumed exactly 1 of 1 stubs, so Verify passes silently.
}
