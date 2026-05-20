package epic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestEpicRunFlagValidationAndDerivedValues(t *testing.T) {
	resetEpicRunFlags(t)
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--parent") {
		t.Fatalf("missing parent err = %v", err)
	}
	runParent = "EPIC-1"
	runMaxConcurrency = 0
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--max-concurrency") {
		t.Fatalf("bad concurrency err = %v", err)
	}
	runMaxConcurrency = 2
	runIntervalSeconds = 0
	if err := validateEpicRunFlags(); err == nil || !strings.Contains(err.Error(), "--interval-seconds") {
		t.Fatalf("bad interval err = %v", err)
	}
	runIntervalSeconds = 5
	if err := validateEpicRunFlags(); err != nil {
		t.Fatalf("valid flags err = %v", err)
	}
	if got := epicRunWorkerPrefix(); got != "epic-1" {
		t.Fatalf("derived worker prefix = %q", got)
	}
	runWorkerPrefix = "custom"
	if got := epicRunWorkerPrefix(); got != "custom" {
		t.Fatalf("custom worker prefix = %q", got)
	}
}

func TestResolveLeadNameAndSignalContext(t *testing.T) {
	t.Setenv(envAgentName, "env-lead")
	if got := resolveLeadName(" flag-lead "); got != "flag-lead" {
		t.Fatalf("flag lead = %q", got)
	}
	if got := resolveLeadName(""); got != "env-lead" {
		t.Fatalf("env lead = %q", got)
	}
	ctx, cancel := signalContext(context.Background())
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("signal context not cancelled after cancel")
	}
}

func TestRunEpicRunUsesHookedRuntime(t *testing.T) {
	resetEpicRunFlags(t)
	restore := replaceEpicRunHooks(t)
	defer restore()

	runParent = "EPIC-1"
	st := memstore.New()
	var opened, resolved, built, ran bool
	epicSignalContextFn = func(parent context.Context) (context.Context, context.CancelFunc) {
		if parent == nil {
			parent = context.Background()
		}
		return context.WithCancel(parent)
	}
	epicOpenStoreFn = func(ctx context.Context) (*bootstrap.StoreHandle, error) {
		if ctx == nil {
			t.Fatal("open store context = nil")
		}
		opened = true
		return &bootstrap.StoreHandle{Store: st}, nil
	}
	epicResolveWorkspaceKeyFn = func(ctx context.Context, workspaces store.WorkspaceStore) (string, error) {
		if ctx == nil || workspaces == nil {
			t.Fatalf("resolve args ctx=%v workspaces=%v", ctx, workspaces)
		}
		resolved = true
		return "WS", nil
	}
	epicDefaultIssueBackendFn = func() backend.IssueBackend {
		return clitest.NewMockIssueBackend()
	}
	epicNewRunnerFromFlagsFn = func(ctx context.Context, gotStore store.Store, ib backend.IssueBackend, ws string) (*epicrunner.Runner, error) {
		if ctx == nil || gotStore != st || ib == nil || ws != "WS" {
			t.Fatalf("new runner args ctx=%v store=%v ib=%v ws=%q", ctx, gotStore, ib, ws)
		}
		built = true
		return nil, nil
	}
	epicRunConfiguredRunnerFn = func(ctx context.Context, r *epicrunner.Runner) error {
		if ctx == nil || r != nil {
			t.Fatalf("run configured args ctx=%v runner=%v", ctx, r)
		}
		ran = true
		return nil
	}

	if err := runEpicRun(&cobra.Command{}, nil); err != nil {
		t.Fatalf("runEpicRun: %v", err)
	}
	if !opened || !resolved || !built || !ran {
		t.Fatalf("opened=%v resolved=%v built=%v ran=%v", opened, resolved, built, ran)
	}
}

func TestRunEpicRunErrorBranches(t *testing.T) {
	resetEpicRunFlags(t)
	restore := replaceEpicRunHooks(t)
	defer restore()

	if err := runEpicRun(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "--parent") {
		t.Fatalf("missing parent err = %v", err)
	}

	runParent = "EPIC-1"
	epicSignalContextFn = func(parent context.Context) (context.Context, context.CancelFunc) {
		if parent == nil {
			parent = context.Background()
		}
		return context.WithCancel(parent)
	}
	epicOpenStoreFn = func(context.Context) (*bootstrap.StoreHandle, error) {
		return nil, errors.New("store unavailable")
	}
	if err := runEpicRun(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "open store") {
		t.Fatalf("open store err = %v", err)
	}

	epicOpenStoreFn = func(context.Context) (*bootstrap.StoreHandle, error) {
		return &bootstrap.StoreHandle{Store: memstore.New()}, nil
	}
	epicResolveWorkspaceKeyFn = func(context.Context, store.WorkspaceStore) (string, error) {
		return "", errors.New("no workspace")
	}
	if err := runEpicRun(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "resolve workspace") {
		t.Fatalf("resolve workspace err = %v", err)
	}

	epicResolveWorkspaceKeyFn = func(context.Context, store.WorkspaceStore) (string, error) { return "WS", nil }
	epicDefaultIssueBackendFn = func() backend.IssueBackend { return nil }
	if err := runEpicRun(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "no issue backend") {
		t.Fatalf("nil backend err = %v", err)
	}
}

func TestNewRunnerFromFlagsDryRunAndConfiguredRunner(t *testing.T) {
	resetEpicRunFlags(t)
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "WS",
		Name:         "nova",
		RoleName:     "lead",
	}); err != nil {
		t.Fatalf("create lead: %v", err)
	}
	ib := clitest.NewMockIssueBackend()

	runParent = "EPIC-1"
	runLead = " nova "
	runRole = "task"
	runMaxConcurrency = 3
	runIntervalSeconds = 1
	runWorkerPrefix = ""
	runNodeID = "node-1"
	runDryRun = true
	t.Setenv(envOrchestratorSessionID, " lead-session ")

	var stdout bytes.Buffer
	var r *epicrunner.Runner
	out := captureEpicStdout(t, func() {
		var err error
		r, err = newRunnerFromFlags(ctx, st, ib, "WS")
		if err != nil {
			t.Fatalf("newRunnerFromFlags: %v", err)
		}
	})
	stdout.WriteString(out)
	if r == nil {
		t.Fatal("newRunnerFromFlags returned nil runner")
	}
	if got := stdout.String(); !strings.Contains(got, "DRY-RUN would assign lead nova to epic EPIC-1") {
		t.Fatalf("dry-run output = %q", got)
	}
	lead, err := st.Agents().Get(ctx, "WS", "nova")
	if err != nil {
		t.Fatalf("get lead: %v", err)
	}
	if lead.Parent != "" {
		t.Fatalf("dry-run lead parent = %q, want unchanged", lead.Parent)
	}

	// A runner with no child work should print its header and drain immediately.
	drained, _, err := epicrunner.NewRunner(ctx, epicrunner.RunnerConfig{
		Store:          st,
		IssueBackend:   ib,
		WorkspaceKey:   "WS",
		EpicID:         "EPIC-1",
		Role:           "task",
		MaxConcurrency: 1,
		Interval:       time.Millisecond,
		DryRun:         true,
		Out:            &stdout,
		ErrOut:         &stdout,
	})
	if err != nil {
		t.Fatalf("NewRunner drained: %v", err)
	}
	if err := runConfiguredRunner(ctx, drained); err != nil {
		t.Fatalf("runConfiguredRunner: %v", err)
	}
	if !strings.Contains(stdout.String(), "Epic runner") {
		t.Fatalf("configured runner output missing header: %q", stdout.String())
	}
}

func replaceEpicRunHooks(t *testing.T) func() {
	t.Helper()
	oldSignal := epicSignalContextFn
	oldOpen := epicOpenStoreFn
	oldResolve := epicResolveWorkspaceKeyFn
	oldBackend := epicDefaultIssueBackendFn
	oldNewRunner := epicNewRunnerFromFlagsFn
	oldRunRunner := epicRunConfiguredRunnerFn
	return func() {
		epicSignalContextFn = oldSignal
		epicOpenStoreFn = oldOpen
		epicResolveWorkspaceKeyFn = oldResolve
		epicDefaultIssueBackendFn = oldBackend
		epicNewRunnerFromFlagsFn = oldNewRunner
		epicRunConfiguredRunnerFn = oldRunRunner
	}
}

func resetEpicRunFlags(t *testing.T) {
	t.Helper()
	origParent, origPrefix, origRole, origNode, origLead := runParent, runWorkerPrefix, runRole, runNodeID, runLead
	origMax, origInterval := runMaxConcurrency, runIntervalSeconds
	origDry := runDryRun
	t.Cleanup(func() {
		runParent, runWorkerPrefix, runRole, runNodeID, runLead = origParent, origPrefix, origRole, origNode, origLead
		runMaxConcurrency, runIntervalSeconds = origMax, origInterval
		runDryRun = origDry
	})
	runParent = ""
	runWorkerPrefix = ""
	runRole = "task"
	runNodeID = ""
	runLead = ""
	runMaxConcurrency = 2
	runIntervalSeconds = 5
	runDryRun = false
}

func captureEpicStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout: %v", err)
	}
	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})
	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return out.String()
}
