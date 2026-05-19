package leadcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket" //nolint:staticcheck // package under test uses nhooyr/websocket
)

func TestCodexThreadStatusAndNewestThread(t *testing.T) {
	statuses := []struct {
		status CodexThreadStatus
		want   string
	}{
		{CodexThreadStatus{Type: "idle"}, RuntimeStatusIdle},
		{CodexThreadStatus{Type: "active"}, RuntimeStatusActive},
		{CodexThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnApproval"}}, RuntimeStatusWaitingApproval},
		{CodexThreadStatus{Type: "active", ActiveFlags: []string{"waitingOnUserInput"}}, RuntimeStatusWaitingUserInput},
		{CodexThreadStatus{Type: "systemError"}, RuntimeStatusFailed},
		{CodexThreadStatus{Type: "notLoaded"}, RuntimeStatusDisconnected},
		{CodexThreadStatus{}, RuntimeStatusDisconnected},
		{CodexThreadStatus{Type: "custom"}, "custom"},
	}
	for _, tt := range statuses {
		if got := tt.status.RuntimeStatus(); got != tt.want {
			t.Fatalf("%+v RuntimeStatus = %q, want %q", tt.status, got, tt.want)
		}
	}
	if !(CodexThreadStatus{Type: "idle"}).CanStartTurn() || (CodexThreadStatus{Type: "active"}).CanStartTurn() {
		t.Fatal("CanStartTurn mismatch")
	}

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	threads := []CodexThread{
		{ID: "", Cwd: "/work", CreatedAtMS: float64(base.Add(time.Minute).UnixMilli())},
		{ID: "old", Cwd: "/work", CreatedAtMS: float64(base.Add(-time.Minute).UnixMilli())},
		{ID: "other-cwd", Cwd: "/other", CreatedAtMS: float64(base.Add(time.Hour).UnixMilli())},
		{ID: "best", Cwd: "/work", CreatedAtMS: float64(base.Add(time.Second).UnixMilli()), UpdatedAtMS: float64(base.Add(2 * time.Second).UnixMilli())},
	}
	best := newestCodexThread(threads, "/work", base)
	if best == nil || best.ID != "best" {
		t.Fatalf("best thread = %+v", best)
	}
	if got := newestCodexThread(threads, "/missing", base); got != nil {
		t.Fatalf("missing cwd got %+v", got)
	}
}

func TestCodexRuntimePureHelpers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := normalizeCodexLeadRuntimeConfig(CodexLeadRuntimeConfig{
		Workspace: " WS/One ", LeadName: " Lead One ", SessionID: " Sess:One ",
		WorkDir: " /work ", Prompt: "prompt",
	})
	if cfg.CodexPath != defaultCodexBinary || cfg.Stdin == nil || cfg.Stdout == nil || cfg.Stderr == nil || cfg.Logger == nil {
		t.Fatalf("normalized cfg = %+v", cfg)
	}
	runtimeHome, sqliteHome := codexLeadRuntimeDirs(cfg)
	if !strings.Contains(runtimeHome, filepath.Join("ws-one", "lead-one", "sess-one")) || !strings.HasSuffix(sqliteHome, "sqlite") {
		t.Fatalf("runtime dirs = %q %q", runtimeHome, sqliteHome)
	}
	if got := sanitizeRuntimePathPart(" A/b:C "); got != "a-b-c" {
		t.Fatalf("sanitized = %q", got)
	}
	if got := unixFloatTime(1_700_000_000_000); got.IsZero() || got.UnixMilli() != 1_700_000_000_000 {
		t.Fatalf("millis time = %v", got)
	}
	if got := unixFloatTime(1700000000.5); got.IsZero() || got.Nanosecond() == 0 {
		t.Fatalf("seconds time = %v", got)
	}
	if got := threadSortTime(CodexThread{UpdatedAt: 1700000000}); got.IsZero() {
		t.Fatal("threadSortTime returned zero")
	}
	if got := threadCreatedAt(CodexThread{CreatedAt: 1700000000}); got.IsZero() {
		t.Fatal("threadCreatedAt returned zero")
	}
}

func TestWaitForCodexAppServerBranches(t *testing.T) {
	errCh := make(chan error, 1)
	errCh <- errors.New("boom")
	close(errCh)
	if err := waitForCodexAppServer(context.Background(), "ws://127.0.0.1:1", errCh); err == nil || !strings.Contains(err.Error(), "before ready") {
		t.Fatalf("app exit err = %v", err)
	}
	nilErrCh := make(chan error, 1)
	nilErrCh <- nil
	close(nilErrCh)
	if err := waitForCodexAppServer(context.Background(), "ws://127.0.0.1:1", nilErrCh); err == nil || !strings.Contains(err.Error(), "codex app-server exited") {
		t.Fatalf("nil app exit err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForCodexAppServer(ctx, "ws://127.0.0.1:1", make(chan error)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
}

func TestFindNewestCodexThreadUsesAppServerClient(t *testing.T) {
	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var req rpcRequest
			if err := json.Unmarshal(data, &req); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			if req.ID == 0 {
				continue
			}
			idJSON := strconv.FormatInt(req.ID, 10)
			if req.Method == "thread/list" {
				idJSON = strconv.Quote(idJSON)
			}
			result := `{}`
			if req.Method == "thread/list" {
				result = fmt.Sprintf(`{"data":[
					{"id":"old","cwd":"/work","createdAt":%d,"updatedAt":%d,"status":{"type":"idle"}},
					{"id":"best","cwd":"/work","createdAt":%d,"updatedAt":%d,"status":{"type":"active"}},
					{"id":"other","cwd":"/other","createdAt":%d,"updatedAt":%d,"status":{"type":"idle"}}
				]}`,
					base.Add(-time.Minute).Unix(), base.Add(-time.Minute).Unix(),
					base.Add(time.Second).Unix(), base.Add(2*time.Second).Unix(),
					base.Add(time.Hour).Unix(), base.Add(time.Hour).Unix())
			}
			msg := fmt.Sprintf(`{"id":%s,"result":%s}`, idJSON, result)
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(msg)); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	endpoint := "ws" + strings.TrimPrefix(ts.URL, "http")
	thread, err := findNewestCodexThread(context.Background(), endpoint, "/work", base)
	if err != nil {
		t.Fatalf("findNewestCodexThread: %v", err)
	}
	if thread == nil || thread.ID != "best" {
		t.Fatalf("thread = %+v", thread)
	}
	if _, err := findNewestCodexThread(context.Background(), "ws://127.0.0.1:1", "/work", base); err == nil {
		t.Fatal("findNewestCodexThread with bad endpoint err = nil")
	}
}

func TestRunCodexRemoteTUIWiresCommandAndStreams(t *testing.T) {
	workDir := t.TempDir()
	captureArgs := filepath.Join(t.TempDir(), "args.txt")
	captureInput := filepath.Join(t.TempDir(), "stdin.txt")
	codexPath := writeCodexRuntimeScript(t, `#!/bin/sh
printf '%s\n' "$@" > "$CODEX_CAPTURE_ARGS"
cat > "$CODEX_CAPTURE_STDIN"
echo tui-stdout
echo tui-stderr >&2
exit 0
`)
	t.Setenv("CODEX_CAPTURE_ARGS", captureArgs)
	t.Setenv("CODEX_CAPTURE_STDIN", captureInput)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runCodexRemoteTUI(context.Background(), CodexLeadRuntimeConfig{
		CodexPath: codexPath,
		WorkDir:   workDir,
		Prompt:    "build the thing",
		Stdin:     strings.NewReader("typed input"),
		Stdout:    &stdout,
		Stderr:    &stderr,
	}, "ws://127.0.0.1:12345")
	if err != nil {
		t.Fatalf("runCodexRemoteTUI: %v", err)
	}
	if !strings.Contains(stdout.String(), "Launching controlled Codex lead session") || !strings.Contains(stdout.String(), "tui-stdout") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tui-stderr") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	argsData, err := os.ReadFile(captureArgs)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := string(argsData)
	for _, want := range []string{"--remote", "ws://127.0.0.1:12345", "--no-alt-screen", "--dangerously-bypass-approvals-and-sandbox", "-C", workDir, "build the thing"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q:\n%s", want, args)
		}
	}
	inputData, err := os.ReadFile(captureInput)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(inputData) != "typed input" {
		t.Fatalf("stdin = %q", string(inputData))
	}
}

func TestStartAndStopCodexAppServerWithFakeBinary(t *testing.T) {
	workDir := t.TempDir()
	runtimeHome := t.TempDir()
	sqliteHome := filepath.Join(runtimeHome, "sqlite")
	if err := os.MkdirAll(sqliteHome, 0700); err != nil {
		t.Fatalf("mkdir sqlite: %v", err)
	}
	codexPath := writeCodexRuntimeScript(t, `#!/bin/sh
echo "$@" > "$CODEX_APP_ARGS"
sleep 30
`)
	argsPath := filepath.Join(t.TempDir(), "app-args.txt")
	t.Setenv("CODEX_APP_ARGS", argsPath)

	ctx, cancel := context.WithCancel(context.Background())
	cmd, appErr, cancelApp, logFile, err := startCodexAppServer(ctx, CodexLeadRuntimeConfig{
		CodexPath: codexPath,
		WorkDir:   workDir,
	}, runtimeHome, sqliteHome, "ws://127.0.0.1:45678")
	if err != nil {
		cancel()
		t.Fatalf("startCodexAppServer: %v", err)
	}
	if cmd == nil || cmd.Process == nil || appErr == nil || cancelApp == nil || logFile == nil {
		cancel()
		t.Fatalf("start returned incomplete handles: cmd=%v appErr=%v cancel=%v log=%v", cmd, appErr, cancelApp, logFile)
	}
	data := waitReadCodexRuntimeFile(t, argsPath)
	if err := stopCodexAppServer(cmd, appErr, cancelApp); err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "signal: killed") {
		cancel()
		t.Fatalf("stopCodexAppServer: %v", err)
	}
	cancel()
	_ = logFile.Close()
	if args := string(data); !strings.Contains(args, "app-server --listen ws://127.0.0.1:45678") || !strings.Contains(args, "sqlite_home=") {
		t.Fatalf("app args = %q", args)
	}
	if _, err := os.Stat(filepath.Join(runtimeHome, "app-server.log")); err != nil {
		t.Fatalf("app server log was not created: %v", err)
	}

	if err := stopCodexAppServer(nil, nil, func() {}); err != nil {
		t.Fatalf("nil stop err = %v", err)
	}
}

func TestRunCodexLeadRuntimeEarlyDirectoryError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	badWorkDir := filepath.Join(t.TempDir(), "missing")
	err := RunCodexLeadRuntime(context.Background(), CodexLeadRuntimeConfig{
		Workspace: "WS", LeadName: "lead", SessionID: "sess",
		WorkDir: badWorkDir, CodexPath: filepath.Join(t.TempDir(), "missing-codex"),
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("RunCodexLeadRuntime with missing binary returned nil")
	}
	_ = os.RemoveAll(badWorkDir)
}

func TestRunCodexLeadRuntimeWithHookedProcessLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()

	oldEndpoint := freeLoopbackWSEndpointFn
	oldStart := startCodexAppServerFn
	oldWait := waitForCodexAppServerFn
	oldTUI := runCodexRemoteTUIFn
	oldStop := stopCodexAppServerFn
	oldDiscover := discoverCodexLeadThreadFn
	t.Cleanup(func() {
		freeLoopbackWSEndpointFn = oldEndpoint
		startCodexAppServerFn = oldStart
		waitForCodexAppServerFn = oldWait
		runCodexRemoteTUIFn = oldTUI
		stopCodexAppServerFn = oldStop
		discoverCodexLeadThreadFn = oldDiscover
	})

	freeLoopbackWSEndpointFn = func() (string, error) { return "ws://127.0.0.1:65530", nil }
	appErr := make(chan error, 1)
	cancelCalled := false
	stopCalled := false
	discoverCalled := make(chan struct{}, 1)
	startCodexAppServerFn = func(_ context.Context, cfg CodexLeadRuntimeConfig, runtimeHome, sqliteHome, endpoint string) (*exec.Cmd, chan error, context.CancelFunc, *os.File, error) {
		if cfg.WorkDir != workDir || !strings.Contains(runtimeHome, filepath.Join("ws", "lead", "sess")) || !strings.HasSuffix(sqliteHome, "sqlite") || endpoint == "" {
			t.Fatalf("start args cfg=%+v runtime=%q sqlite=%q endpoint=%q", cfg, runtimeHome, sqliteHome, endpoint)
		}
		logFile, err := os.CreateTemp(t.TempDir(), "codex-log-*")
		if err != nil {
			t.Fatalf("create temp log: %v", err)
		}
		return &exec.Cmd{Process: &os.Process{Pid: 12345}}, appErr, func() { cancelCalled = true }, logFile, nil
	}
	waitForCodexAppServerFn = func(context.Context, string, <-chan error) error { return nil }
	discoverCodexLeadThreadFn = func(ctx context.Context, _ CodexLeadRuntimeConfig, runtime CodexRuntimeMetadata, _ time.Time) {
		if runtime.Status != RuntimeStatusStarting || runtime.Endpoint == "" || runtime.PID != 12345 {
			t.Errorf("discover runtime = %+v", runtime)
		}
		discoverCalled <- struct{}{}
		<-ctx.Done()
	}
	runCodexRemoteTUIFn = func(_ context.Context, cfg CodexLeadRuntimeConfig, endpoint string) error {
		if cfg.WorkDir != workDir || endpoint != "ws://127.0.0.1:65530" {
			t.Fatalf("tui args cfg=%+v endpoint=%q", cfg, endpoint)
		}
		return nil
	}
	stopCodexAppServerFn = func(*exec.Cmd, <-chan error, context.CancelFunc) error {
		stopCalled = true
		return nil
	}

	err := RunCodexLeadRuntime(context.Background(), CodexLeadRuntimeConfig{
		Workspace: "WS", LeadName: "lead", SessionID: "sess",
		WorkDir: workDir, Prompt: "prompt", CodexPath: "codex",
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("RunCodexLeadRuntime: %v", err)
	}
	if !stopCalled || !cancelCalled {
		t.Fatalf("stopCalled=%t cancelCalled=%t", stopCalled, cancelCalled)
	}
	select {
	case <-discoverCalled:
	case <-time.After(time.Second):
		t.Fatal("discover hook was not called")
	}

	waitForCodexAppServerFn = func(context.Context, string, <-chan error) error { return errors.New("not ready") }
	stopCalled = false
	err = RunCodexLeadRuntime(context.Background(), CodexLeadRuntimeConfig{
		Workspace: "WS", LeadName: "lead", SessionID: "sess2",
		WorkDir: workDir, CodexPath: "codex",
		Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil || !strings.Contains(err.Error(), "not ready") || !stopCalled {
		t.Fatalf("failure branch err=%v stopCalled=%t", err, stopCalled)
	}
}

func writeCodexRuntimeScript(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func waitReadCodexRuntimeFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			return data
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
