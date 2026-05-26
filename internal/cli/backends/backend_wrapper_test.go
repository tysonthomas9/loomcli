package backends

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/harness"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// fakeWrapperRun is a recording wrapperRunFn for tests. It writes the
// configured Stdout payload into the wrapper's Stdout pipe so the
// LineHandler scanner inside runHarness sees real bytes, then returns
// the scripted Result/err.
type fakeWrapperRun struct {
	mu         sync.Mutex
	calls      []wrapper.Config
	stdoutBody string
	result     wrapper.Result
	err        error
}

func (f *fakeWrapperRun) Run(ctx context.Context, cfg wrapper.Config, p harness.RetryPolicy) (wrapper.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cfg)
	body := f.stdoutBody
	f.mu.Unlock()
	if body != "" && cfg.Stdout != nil {
		_, _ = io.WriteString(cfg.Stdout, body)
	}
	// Drain stdin so the prompt reader doesn't sit unread (mirrors
	// what wrapper.Start would do via the PTY).
	if cfg.Stdin != nil {
		_, _ = io.Copy(io.Discard, cfg.Stdin)
	}
	return f.result, f.err
}

func (f *fakeWrapperRun) Calls() []wrapper.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wrapper.Config, len(f.calls))
	copy(out, f.calls)
	return out
}

// requireBinaryOnPath skips the test when name isn't on PATH. The
// wrapper path calls exec.LookPath inside runHarness, so without a
// real binary the call short-circuits before reaching the wrapperRun
// seam. We side-step this by creating a tiny fake binary in a temp
// dir and prepending it to PATH.
func requireBinaryOnPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %s binary: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shortBackendSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "loom-sock-")
	if err != nil {
		t.Fatalf("creating short socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func startBackendIPCServer(t *testing.T, socketPath string, handler func(cli.AgentIPCRequest) cli.AgentIPCResponse) {
	t.Helper()
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on %s: %v", socketPath, err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()

				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				scanner := bufio.NewScanner(c)
				if !scanner.Scan() {
					return
				}

				var req cli.AgentIPCRequest
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					return
				}

				resp := handler(req)
				data, _ := json.Marshal(resp)
				data = append(data, '\n')
				_, _ = c.Write(data)
			}(conn)
		}
	}()
}

func TestRunHarness_PipesPromptAndCallsWrapper(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")
	fake := &fakeWrapperRun{
		stdoutBody: "line one\nline two\n",
		result:     wrapper.Result{Status: wrapper.StatusIdle},
	}
	installWrapperRunMock(t, fake.Run)

	var seen []string
	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fake-tool",
		Args:        []string{"--flag", "value"},
		WorkDir:     "/tmp",
		Env:         []string{"FOO=bar"},
		Prompt:      "hello",
		HarnessName: "claude",
		LineHandler: func(line string) { seen = append(seen, line) },
		RetryPolicy: harness.RetryPolicy{Max: 0, BaseBackoff: 1, MaxBackoff: 1},
	})
	if err != nil {
		t.Fatalf("runHarness returned err: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("wrapperRun calls: got %d, want 1", len(calls))
	}
	cfg := calls[0]
	if !strings.HasSuffix(cfg.BinaryPath, "/fake-tool") {
		t.Errorf("BinaryPath: got %q, want suffix /fake-tool", cfg.BinaryPath)
	}
	if got, want := strings.Join(cfg.Args, " "), "--flag value"; got != want {
		t.Errorf("Args: got %q, want %q", got, want)
	}
	if cfg.WorkingDir != "/tmp" {
		t.Errorf("WorkingDir: got %q, want /tmp", cfg.WorkingDir)
	}
	if len(cfg.Env) != 1 || cfg.Env[0] != "FOO=bar" {
		t.Errorf("Env: got %v, want [FOO=bar]", cfg.Env)
	}
	if cfg.Harness != "claude" {
		t.Errorf("Harness: got %q, want claude", cfg.Harness)
	}
	if got, want := seen, []string{"line one", "line two"}; !equalStrings(got, want) {
		t.Errorf("line handler saw %v, want %v", got, want)
	}
}

func TestRunHarness_BinaryNotFound(t *testing.T) {
	// Empty PATH so exec.LookPath fails predictably.
	t.Setenv("PATH", "")

	fake := &fakeWrapperRun{result: wrapper.Result{Status: wrapper.StatusIdle}}
	installWrapperRunMock(t, fake.Run)

	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "definitely-not-on-path-xyz",
		LineHandler: func(string) {},
	})
	if err == nil {
		t.Fatal("got nil, want non-nil error")
	}
	if len(fake.Calls()) != 0 {
		t.Errorf("wrapperRun should not be called on lookup failure; got %d calls", len(fake.Calls()))
	}
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T (%v), want *InvocationError", err, err)
	}
	if !strings.Contains(invErr.Error(), "not found on PATH") {
		t.Errorf("err message %q missing 'not found on PATH'", invErr.Error())
	}
}

func TestRunHarness_StatusFailedMapsToInvocationError(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")
	fake := &fakeWrapperRun{
		result: wrapper.Result{Status: wrapper.StatusFailed, ExitCode: 2, Reason: "oops"},
	}
	installWrapperRunMock(t, fake.Run)

	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fake-tool",
		LineHandler: func(string) {},
	})
	if err == nil {
		t.Fatal("got nil, want non-nil error")
	}
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T, want *InvocationError", err)
	}
	if invErr.ExitCode != 2 {
		t.Errorf("ExitCode: got %d, want 2", invErr.ExitCode)
	}
	if !strings.Contains(invErr.OutputTail, "oops") {
		t.Errorf("OutputTail %q missing reason 'oops'", invErr.OutputTail)
	}
}

func TestRunHarness_FinalizeHookOverridesDefault(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")
	fake := &fakeWrapperRun{
		stdoutBody: `{"type":"error","error":{"message":"hidden failure"}}` + "\n",
		result:     wrapper.Result{Status: wrapper.StatusIdle}, // Wrapper thinks it's fine
	}
	installWrapperRunMock(t, fake.Run)

	var finalizeCalled bool
	var sawResult wrapper.Result
	err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fake-tool",
		LineHandler: func(string) {},
		Finalize: func(res wrapper.Result, runErr error, outputTail string) error {
			finalizeCalled = true
			sawResult = res
			return fmt.Errorf("finalize overrode: tail=%q", outputTail)
		},
	})
	if !finalizeCalled {
		t.Fatal("Finalize was not called")
	}
	if sawResult.Status != wrapper.StatusIdle {
		t.Errorf("Finalize received status %q, want idle", sawResult.Status)
	}
	if err == nil || !strings.Contains(err.Error(), "finalize overrode") {
		t.Errorf("err: got %v, want one containing 'finalize overrode'", err)
	}
}

func TestRunHarness_ShutdownCancelsContext(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")
	ctxObserved := make(chan context.Context, 1)
	installWrapperRunMock(t, func(ctx context.Context, cfg wrapper.Config, p harness.RetryPolicy) (wrapper.Result, error) {
		select {
		case ctxObserved <- ctx:
		default:
		}
		// Wait until ctx is cancelled by the shutdown adapter.
		<-ctx.Done()
		return wrapper.Result{Status: wrapper.StatusInterrupted, Reason: "ctx cancel"}, nil
	})

	shutdown := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runHarness(context.Background(), shutdown, harnessInvocation{
			BinaryName:  "fake-tool",
			LineHandler: func(string) {},
		})
	}()

	<-ctxObserved
	close(shutdown)
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err: got %v, want errors.Is(context.Canceled)", err)
	}
}

func TestContextFromShutdown_NilShutdownIsPassthrough(t *testing.T) {
	parent := context.Background()
	ctx, cancel := contextFromShutdown(parent, nil)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Error("ctx should not be done yet")
	default:
	}
}

func TestContextFromShutdown_NilParentDefaultsToBackground(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally passing nil to assert contextFromShutdown's nil-parent fallback to background.
	ctx, cancel := contextFromShutdown(nil, nil)
	defer cancel()
	if ctx == nil {
		t.Fatal("ctx is nil")
	}
}

// --- Per-backend wiring tests: each invoker must drive the wrapper
//     with the right binary, args, and Harness name. ---

func TestDefaultClaudeNonInteractiveInvoker_UsesWrapperWithClaudeHarness(t *testing.T) {
	requireBinaryOnPath(t, "claude")
	fake := &fakeWrapperRun{result: wrapper.Result{Status: wrapper.StatusIdle}}
	installWrapperRunMock(t, fake.Run)

	collector := usage.NewCollector("claude", "agent-A")
	if err := defaultClaudeNonInteractiveInvoker(t.TempDir(), "prompt body", "agent-A", nil, collector); err != nil {
		t.Fatalf("invoker err: %v", err)
	}

	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("wrapperRun calls: got %d, want 1", len(calls))
	}
	cfg := calls[0]
	if cfg.Harness != "claude" {
		t.Errorf("Harness: got %q, want claude", cfg.Harness)
	}
	if !containsArg(cfg.Args, "-p") || !containsArg(cfg.Args, "--output-format") {
		t.Errorf("Args: got %v, want to contain '-p' and '--output-format'", cfg.Args)
	}
}

func TestDefaultCodexNonInteractiveInvoker_UsesWrapperWithCodexHarness(t *testing.T) {
	requireBinaryOnPath(t, "codex")
	fake := &fakeWrapperRun{result: wrapper.Result{Status: wrapper.StatusIdle}}
	installWrapperRunMock(t, fake.Run)

	if err := defaultCodexNonInteractiveInvoker(t.TempDir(), "prompt body", "agent-A", nil, nil); err != nil {
		t.Fatalf("invoker err: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Harness != "codex" {
		t.Fatalf("wrapperRun calls: %+v, want one call with Harness=codex", calls)
	}
	if !containsArg(calls[0].Args, "exec") || !containsArg(calls[0].Args, "--json") {
		t.Errorf("codex args: got %v, want to contain 'exec' and '--json'", calls[0].Args)
	}
}

func TestDefaultGeminiNonInteractiveInvoker_UsesWrapperWithGeminiHarness(t *testing.T) {
	requireBinaryOnPath(t, "gemini")
	fake := &fakeWrapperRun{result: wrapper.Result{Status: wrapper.StatusIdle}}
	installWrapperRunMock(t, fake.Run)

	if err := defaultGeminiNonInteractiveInvoker(t.TempDir(), "prompt body", "agent-A", nil, nil); err != nil {
		t.Fatalf("invoker err: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Harness != "gemini" {
		t.Fatalf("wrapperRun calls: %+v, want one call with Harness=gemini", calls)
	}
	if !containsArg(calls[0].Args, "-p") || !containsArg(calls[0].Args, "prompt body") {
		t.Errorf("gemini args: got %v, want '-p prompt body' (prompt passed via argv)", calls[0].Args)
	}
}

func TestDefaultCursorNonInteractiveInvoker_UsesWrapperWithGenericClassifier(t *testing.T) {
	requireBinaryOnPath(t, "cursor")
	fake := &fakeWrapperRun{result: wrapper.Result{Status: wrapper.StatusIdle}}
	installWrapperRunMock(t, fake.Run)

	if err := defaultCursorNonInteractiveInvoker(t.TempDir(), "prompt body", "agent-A", nil, nil); err != nil {
		t.Fatalf("invoker err: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("wrapperRun calls: got %d, want 1", len(calls))
	}
	if calls[0].Harness != "" {
		t.Errorf("Harness: got %q, want empty (generic classifier)", calls[0].Harness)
	}
}

func TestDefaultOpenCodeNonInteractiveInvoker_StreamErrorOverridesIdle(t *testing.T) {
	requireBinaryOnPath(t, "opencode")
	fake := &fakeWrapperRun{
		stdoutBody: `{"type":"error","error":{"message":"opencode boom"}}` + "\n",
		result:     wrapper.Result{Status: wrapper.StatusIdle},
	}
	installWrapperRunMock(t, fake.Run)

	err := defaultOpenCodeNonInteractiveInvoker(t.TempDir(), "prompt body", "agent-A", nil, nil)
	if err == nil {
		t.Fatal("got nil, want error from stream-error finalization")
	}
	var invErr *InvocationError
	if !errors.As(err, &invErr) {
		t.Fatalf("err: got %T, want *InvocationError", err)
	}
	if !strings.Contains(invErr.OutputTail, "opencode boom") {
		t.Errorf("OutputTail %q missing 'opencode boom'", invErr.OutputTail)
	}
}

// --- RunWithRetry-vs-runHarness invariant: retry happens at the
//     wrapperRun layer, so backends that use DefaultRetryPolicy
//     surface the final terminal status after the retry budget is
//     spent. ---

func TestRunHarness_RetryablePolicyPassedThrough(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")
	var sawPolicy atomic.Value
	mockFn := func(ctx context.Context, cfg wrapper.Config, p harness.RetryPolicy) (wrapper.Result, error) {
		sawPolicy.Store(p)
		return wrapper.Result{Status: wrapper.StatusIdle}, nil
	}
	installWrapperRunMock(t, mockFn)

	want := harness.RetryPolicy{Max: 7, BaseBackoff: 5, MaxBackoff: 13}
	if err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fake-tool",
		LineHandler: func(string) {},
		RetryPolicy: want,
	}); err != nil {
		t.Fatalf("runHarness err: %v", err)
	}
	got, _ := sawPolicy.Load().(harness.RetryPolicy)
	// Compare field-by-field: RetryPolicy now contains an OnActivity func
	// that runHarness auto-attaches when no daemon socket is set. The
	// retry/backoff fields are the actual contract under test here.
	if got.Max != want.Max || got.BaseBackoff != want.BaseBackoff || got.MaxBackoff != want.MaxBackoff {
		t.Errorf("RetryPolicy: got %+v, want %+v", got, want)
	}
}

func TestRunHarness_AutoAttachesDaemonActivityObserver(t *testing.T) {
	requireBinaryOnPath(t, "fake-tool")

	socketPath := filepath.Join(shortBackendSocketDir(t), "ipc.sock")
	captured := make(chan cli.AgentIPCRequest, 1)
	startBackendIPCServer(t, socketPath, func(req cli.AgentIPCRequest) cli.AgentIPCResponse {
		captured <- req
		return cli.AgentIPCResponse{Success: true}
	})

	t.Setenv("LOOM_DAEMON_SOCKET", socketPath)
	t.Setenv("LOOM_AGENT_NAME", "worker-1")
	t.Setenv("LOOM_SESSION_ID", "session-1")
	t.Setenv("LOOM_AGENT_LEASE_ID", "lease-1")
	t.Setenv("LOOM_AGENT_LEASE_TOKEN", "token-1")
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_ISSUE_BACKEND", "")

	cli.ResetDefaultIssueBackend()
	t.Cleanup(cli.ResetDefaultIssueBackend)

	activityAt := time.Unix(1700000000, 123456000).UTC()
	installWrapperRunMock(t, func(ctx context.Context, cfg wrapper.Config, p harness.RetryPolicy) (wrapper.Result, error) {
		if p.OnActivity == nil {
			t.Fatal("runHarness did not auto-attach a daemon activity observer")
		}
		p.OnActivity(wrapper.Snapshot{LastOutputAt: activityAt})
		return wrapper.Result{Status: wrapper.StatusIdle}, nil
	})

	if err := runHarness(context.Background(), nil, harnessInvocation{
		BinaryName:  "fake-tool",
		LineHandler: func(string) {},
		RetryPolicy: harness.RetryPolicy{Max: 0, BaseBackoff: 1, MaxBackoff: 1},
	}); err != nil {
		t.Fatalf("runHarness err: %v", err)
	}

	select {
	case req := <-captured:
		if req.Operation != cli.IPCOpHeartbeat {
			t.Errorf("operation = %q, want %q", req.Operation, cli.IPCOpHeartbeat)
		}
		if req.AgentName != "worker-1" {
			t.Errorf("agent_name = %q, want worker-1", req.AgentName)
		}
		if req.SessionID != "session-1" || req.LeaseID != "lease-1" || req.LeaseToken != "token-1" {
			t.Errorf("lease/session metadata = session:%q lease:%q token:%q", req.SessionID, req.LeaseID, req.LeaseToken)
		}
		if !req.LastActivityAt.Equal(activityAt) {
			t.Errorf("last_activity_at = %v, want %v", req.LastActivityAt, activityAt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for auto-attached heartbeat")
	}
}

// --- helpers ---

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
