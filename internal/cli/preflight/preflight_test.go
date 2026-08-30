package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func commandStore(t *testing.T, workspaceBackend string) store.Store {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "ACME", Name: "Acme"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if workspaceBackend != "" {
		if _, err := st.Daemon().Upsert(ctx, &domain.DaemonProfile{WorkspaceKey: "ACME", AgentBackend: workspaceBackend}); err != nil {
			t.Fatalf("upsert daemon profile: %v", err)
		}
	}
	return st
}

func depsForStore(st store.Store) commandDeps {
	return commandDeps{
		openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
			return &bootstrap.StoreHandle{Store: st}, nil
		},
		activeWorkspace: func(context.Context, store.Store) (string, error) {
			return "ACME", nil
		},
	}
}

func executeCommand(t *testing.T, deps commandDeps, args ...string) (string, error) {
	t.Helper()
	cmd := newCommand(deps)
	cmd.SetContext(context.Background())
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := cmd.ParseFlags(args); err != nil {
		return stdout.String(), err
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	return stdout.String(), err
}

func withHealth(t *testing.T, fn func(string) (runtimepreflight.HealthStatus, bool)) {
	t.Helper()
	restore := runtimepreflight.SetHealthCheckerForTest(fn)
	t.Cleanup(restore)
}

func decodeReport(t *testing.T, output string) report {
	t.Helper()
	var got report
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("decode report: %v\noutput: %s", err, output)
	}
	return got
}

func TestCommandActiveWorkspaceDefault(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "ACME")
	st := commandStore(t, "")
	withHealth(t, func(name string) (runtimepreflight.HealthStatus, bool) {
		if name != "codex" {
			t.Fatalf("health backend = %q, want codex", name)
		}
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"}, true
	})
	stdout, err := executeCommand(t, depsForStore(st), "--json")
	if err != nil {
		t.Fatalf("loom preflight error = %v", err)
	}
	got := decodeReport(t, stdout)
	if got.SchemaVersion != 1 || got.Kind != reportKind || got.Workspace != "ACME" {
		t.Fatalf("report envelope = %+v", got)
	}
	if got.Backend != "codex" || got.BackendSource != runtimepreflight.BackendSourceDefault || !got.Ready {
		t.Fatalf("verdict = %+v", got.Result)
	}
	if !strings.Contains(stdout, `"backend_source": "default"`) {
		t.Fatalf("JSON backend_source wire value changed: %s", stdout)
	}
}

func TestCommandAgentOverride(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "ACME")
	st := commandStore(t, "claude")
	ctx := context.Background()
	if _, err := st.Roles().Create(ctx, store.RoleCreate{WorkspaceKey: "ACME", Name: "worker"}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: "ACME", Name: "worker-a", RoleName: "worker", Backend: "gemini"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	withHealth(t, func(name string) (runtimepreflight.HealthStatus, bool) {
		if name != "gemini" {
			t.Fatalf("health backend = %q, want gemini", name)
		}
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, true
	})
	stdout, err := executeCommand(t, depsForStore(st), "--agent", "worker-a", "--json")
	if err != nil {
		t.Fatalf("loom preflight error = %v", err)
	}
	got := decodeReport(t, stdout)
	if got.Agent != "worker-a" || got.Backend != "gemini" || got.BackendSource != runtimepreflight.BackendSourceAgent {
		t.Fatalf("report = %+v", got)
	}
}

func TestCommandExplicitBackendWithoutWorkspace(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	openCalled := false
	deps := commandDeps{
		openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
			openCalled = true
			return nil, errors.New("unexpected store open")
		},
	}
	withHealth(t, func(name string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, name == "opencode"
	})
	stdout, err := executeCommand(t, deps, "--ai-backend", "opencode", "--json")
	if err != nil {
		t.Fatalf("loom preflight error = %v", err)
	}
	if openCalled {
		t.Fatal("explicit backend without an active workspace opened the store")
	}
	got := decodeReport(t, stdout)
	if got.Workspace != "" || got.Backend != "opencode" || got.BackendSource != runtimepreflight.BackendSourceOverride || !got.Ready {
		t.Fatalf("report = %+v", got)
	}
}

func TestCommandExplicitBackendUsesWorkspaceHintWithoutOpeningStore(t *testing.T) {
	t.Setenv(bootstrap.EnvWorkspace, "ACME")
	openCalled := false
	deps := commandDeps{
		openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
			openCalled = true
			return nil, errors.New("unexpected store open")
		},
	}
	withHealth(t, func(name string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, name == "opencode"
	})
	stdout, err := executeCommand(t, deps, "--ai-backend", "opencode", "--json")
	if err != nil {
		t.Fatalf("loom preflight error = %v", err)
	}
	if openCalled {
		t.Fatal("explicit backend with a workspace hint opened the store")
	}
	if got := decodeReport(t, stdout); got.Workspace != "ACME" || !got.Ready {
		t.Fatalf("report = %+v, want ready result with cosmetic workspace hint", got)
	}
}

func TestCommandExplicitBackendDoesNotMutateConfiguration(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "ACME")
	st := commandStore(t, "claude")
	withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, true
	})
	stdout, err := executeCommand(t, depsForStore(st), "--ai-backend", "gemini", "--json")
	if err != nil {
		t.Fatalf("loom preflight error = %v", err)
	}
	if got := decodeReport(t, stdout); got.Backend != "gemini" || got.BackendSource != runtimepreflight.BackendSourceOverride {
		t.Fatalf("report = %+v", got)
	}
	profile, err := st.Daemon().Get(context.Background(), "ACME")
	if err != nil {
		t.Fatalf("get daemon profile: %v", err)
	}
	if profile.AgentBackend != "claude" {
		t.Fatalf("stored backend = %q, want unchanged claude", profile.AgentBackend)
	}
}

func TestCommandExitCodes(t *testing.T) {
	cases := []struct {
		name      string
		status    runtimepreflight.HealthStatus
		probeOK   bool
		agent     string
		wantCode  int
		wantClass runtimepreflight.ErrorClass
	}{
		{"ready", runtimepreflight.HealthStatus{Healthy: true, Installed: true}, true, "", 0, ""},
		{"not ready", runtimepreflight.HealthStatus{Installed: false}, true, "", 1, runtimepreflight.ErrorClassUnavailable},
		{"agent not found", runtimepreflight.HealthStatus{}, true, "missing", 2, runtimepreflight.ErrorClassResolutionFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOOM_WORKSPACE", "ACME")
			st := commandStore(t, "codex")
			withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) { return tc.status, tc.probeOK })
			args := []string{"--json"}
			if tc.agent != "" {
				args = append(args, "--agent", tc.agent)
			}
			stdout, err := executeCommand(t, depsForStore(st), args...)
			if got := cli.CommandExitCode(err); got != tc.wantCode {
				t.Fatalf("exit code = %d error=%v, want %d", got, err, tc.wantCode)
			}
			got := decodeReport(t, stdout)
			if got.ErrorClass != tc.wantClass {
				t.Fatalf("error class = %q, want %q", got.ErrorClass, tc.wantClass)
			}
			if tc.wantCode > 0 && err.Error() != "preflight: "+string(tc.wantClass) {
				t.Fatalf("stderr error text = %q, want short class line", err)
			}
		})
	}
}

func TestCommandUnknownBackendReachesPreflight(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_BACKEND", "also-unknown")
	withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{}, false
	})
	stdout, err := executeCommand(t, commandDeps{}, "--ai-backend", "made-up", "--json")
	if got := cli.CommandExitCode(err); got != 1 {
		t.Fatalf("exit code = %d error=%v, want 1", got, err)
	}
	got := decodeReport(t, stdout)
	if got.Backend != "made-up" || got.ErrorClass != runtimepreflight.ErrorClassUnavailable {
		t.Fatalf("report = %+v", got)
	}
}

func TestRenderHumanEveryClass(t *testing.T) {
	health := &runtimepreflight.HealthStatus{Installed: true, APIKeySet: true, Message: "probe detail"}
	cases := []struct {
		name      string
		result    runtimepreflight.Result
		wantLines []string
	}{
		{"ready", runtimepreflight.Result{Backend: "codex", BackendSource: runtimepreflight.BackendSourceDefault, Ready: true, Health: &runtimepreflight.HealthStatus{Healthy: true, Installed: true}, Message: "ready"}, []string{"Local task runner: READY", "Backend: codex (built-in default)"}},
		{"unavailable", runtimepreflight.Result{Backend: "codex", BackendSource: runtimepreflight.BackendSourceWorkspace, Health: health, ErrorClass: runtimepreflight.ErrorClassUnavailable, Message: "unavailable", Remediation: []string{"install"}}, []string{"Class: local_backend_unavailable", "Next: install"}},
		{"unsupported", runtimepreflight.Result{Backend: "echo", BackendSource: runtimepreflight.BackendSourceOverride, Health: &runtimepreflight.HealthStatus{Installed: true, APIKeySet: true, Healthy: true, Message: "ready"}, ErrorClass: runtimepreflight.ErrorClassUnsupported, Message: "unsupported", Remediation: []string{"choose"}}, []string{"Backend: echo (explicit override)", "Class: local_backend_unsupported", "Reason: unsupported"}},
		{"auth missing", runtimepreflight.Result{Backend: "gemini", BackendSource: runtimepreflight.BackendSourceAgent, Health: &runtimepreflight.HealthStatus{Installed: true}, ErrorClass: runtimepreflight.ErrorClassAuthMissing, Message: "auth", Remediation: []string{"authenticate"}}, []string{"Backend: gemini (agent override)", "Authenticated: no", "Class: local_backend_auth_missing"}},
		{"unhealthy", runtimepreflight.Result{Backend: "gemini", BackendSource: runtimepreflight.BackendSourceAgent, Health: health, ErrorClass: runtimepreflight.ErrorClassUnhealthy, Message: "unhealthy", Remediation: []string{"repair"}}, []string{"Healthy: no", "Class: local_backend_unhealthy", "Reason: unhealthy", "Detail: probe detail"}},
		{"resolution failed", runtimepreflight.Result{ErrorClass: runtimepreflight.ErrorClassResolutionFailed, Message: "resolution failed", Remediation: []string{"retry"}}, []string{"Class: local_backend_resolution_failed", "Reason: resolution failed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := renderHuman(&output, report{SchemaVersion: 1, Kind: reportKind, Workspace: "ACME", Agent: "worker-a", Result: tc.result})
			if err != nil {
				t.Fatalf("renderHuman() error = %v", err)
			}
			for _, line := range tc.wantLines {
				if !strings.Contains(output.String(), line+"\n") {
					t.Errorf("output missing %q:\n%s", line, output.String())
				}
			}
			if tc.result.ErrorClass != runtimepreflight.ErrorClassAuthMissing &&
				tc.result.ErrorClass != runtimepreflight.ErrorClassUnhealthy &&
				strings.Contains(output.String(), "Detail:") {
				t.Errorf("probe detail must be omitted for class %q:\n%s", tc.result.ErrorClass, output.String())
			}
		})
	}
}

func TestCommandOperationalFailuresIncludeSingleLineCause(t *testing.T) {
	const sentinelURL = "http://127.0.0.1:8280/fleetdb"
	cases := []struct {
		name         string
		deps         commandDeps
		wantContains []string
		wantAbsent   []string
	}{
		{
			name: "open store",
			deps: commandDeps{openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
				return nil, fmt.Errorf("fleet unavailable at %s\nretry later", sentinelURL)
			}},
			wantContains: []string{"fleet unavailable at [redacted] retry later", "[redacted]"},
			wantAbsent:   []string{sentinelURL},
		},
		{
			name: "active workspace",
			deps: commandDeps{
				openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
					return &bootstrap.StoreHandle{Store: commandStore(t, "codex")}, nil
				},
				activeWorkspace: func(context.Context, store.Store) (string, error) {
					storeFailure := fmt.Errorf("GET %s: transport unavailable", sentinelURL)
					return "", fmt.Errorf("resolve active workspace: %w", storeFailure)
				},
			},
			wantContains: []string{"resolve active workspace: GET [redacted] transport unavailable", "[redacted]"},
			wantAbsent:   []string{sentinelURL},
		},
		{
			name: "no active workspace guidance",
			deps: commandDeps{
				openStore: func(context.Context) (*bootstrap.StoreHandle, error) {
					return &bootstrap.StoreHandle{Store: commandStore(t, "codex")}, nil
				},
				activeWorkspace: func(context.Context, store.Store) (string, error) {
					return "", errors.New("no active workspace: set LOOM_WORKSPACE or pass --workspace")
				},
			},
			wantContains: []string{"no active workspace: set LOOM_WORKSPACE or pass --workspace"},
			wantAbsent:   []string{"[redacted]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(bootstrap.EnvWorkspace, "ACME")
			stdout, err := executeCommand(t, tc.deps, "--json")
			if got := cli.CommandExitCode(err); got != 2 {
				t.Fatalf("exit code = %d error=%v, want 2", got, err)
			}
			got := decodeReport(t, stdout)
			if got.ErrorClass != runtimepreflight.ErrorClassResolutionFailed {
				t.Fatalf("result = %+v, want resolution failure", got.Result)
			}
			if strings.Contains(got.Message, "\n") {
				t.Fatalf("message = %q, want single-line underlying cause", got.Message)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got.Message, want) {
					t.Errorf("message = %q, want substring %q", got.Message, want)
				}
			}
			for _, unwanted := range tc.wantAbsent {
				if strings.Contains(got.Message, unwanted) {
					t.Errorf("message = %q, must not contain %q", got.Message, unwanted)
				}
			}
		})
	}
}

func TestCheckTargetNilStoreOpenerReturnsOperationalError(t *testing.T) {
	t.Setenv(bootstrap.EnvWorkspace, "ACME")
	result, workspace, err := checkTarget(context.Background(), options{}, commandDeps{})
	if err == nil {
		t.Fatal("checkTarget() error = nil, want missing dependency error")
	}
	if workspace != "ACME" || result.ErrorClass != runtimepreflight.ErrorClassResolutionFailed {
		t.Fatalf("checkTarget() = (%+v, %q, %v), want resolution failure for ACME", result, workspace, err)
	}
}

func TestCommandSeparatesStdoutAndStderr(t *testing.T) {
	t.Setenv(bootstrap.EnvWorkspace, "ACME")
	withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Installed: false, Message: "binary missing"}, true
	})
	deps := depsForStore(commandStore(t, "codex"))
	originalOpen := deps.openStore
	deps.openStore = func(ctx context.Context) (*bootstrap.StoreHandle, error) {
		slog.Info("store opened for preflight")
		log.Print("legacy store opened for preflight")
		return originalOpen(ctx)
	}

	stdout, stderr, err := executeCommandLifecycle(t, deps, "--json")
	if got := cli.CommandExitCode(err); got != 1 {
		t.Fatalf("exit code = %d error=%v, want 1", got, err)
	}
	if stderr != "preflight: local_backend_unavailable\n" {
		t.Fatalf("stderr = %q, want exactly one class line", stderr)
	}
	if !strings.Contains(stdout, `"error_class": "local_backend_unavailable"`) {
		t.Fatalf("stdout = %q, want canonical JSON report", stdout)
	}
	if strings.Contains(stderr, `"error_class"`) || strings.Contains(stderr, "Local task runner") {
		t.Fatalf("report leaked to stderr: %q", stderr)
	}
}

func TestCommandOverridesRootBackendResolvingPreRun(t *testing.T) {
	t.Setenv(bootstrap.EnvWorkspace, "")
	withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, true
	})
	rootPreRunCalled := false
	root := &cobra.Command{
		Use: "loom",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			rootPreRunCalled = true
			return errors.New("invalid global backend")
		},
	}
	root.PersistentFlags().String("backend", "", "test backend")
	root.AddGroup(&cobra.Group{ID: "workspace", Title: "Workspace Commands:"})
	root.AddCommand(newCommand(commandDeps{}))
	root.SetArgs([]string{"--backend", "definitely-invalid", "preflight", "--ai-backend", "codex", "--json"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute preflight through root: %v", err)
	}
	if rootPreRunCalled {
		t.Fatal("preflight inherited root backend-resolving PersistentPreRunE")
	}
}

func TestCommandHelpIncludesLongDescriptionAndExamples(t *testing.T) {
	cmd := newCommand(commandDeps{})
	if strings.TrimSpace(cmd.Long) == "" || strings.TrimSpace(cmd.Example) == "" {
		t.Fatalf("command help missing Long or Example: Long=%q Example=%q", cmd.Long, cmd.Example)
	}
}

func executeCommandLifecycle(t *testing.T, deps commandDeps, args ...string) (string, string, error) {
	t.Helper()
	previousLogger := slog.Default()
	previousLogLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
	slog.SetLogLoggerLevel(previousLogLevel)
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	previousStderr := os.Stderr
	os.Stderr = writeEnd
	t.Cleanup(func() {
		os.Stderr = previousStderr
		slog.SetDefault(previousLogger)
		slog.SetLogLoggerLevel(previousLogLevel)
		_ = writeEnd.Close()
		_ = readEnd.Close()
	})

	cmd := newCommand(deps)
	cmd.SetContext(context.Background())
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	runErr := cmd.PersistentPreRunE(cmd, nil)
	if runErr == nil {
		runErr = cmd.RunE(cmd, cmd.Flags().Args())
	}
	if runErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, runErr)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	os.Stderr = previousStderr
	stderr, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return stdout.String(), string(stderr), runErr
}

func TestJSONEnvelopeOmissionRules(t *testing.T) {
	value := report{
		SchemaVersion: 1,
		Kind:          reportKind,
		Result: runtimepreflight.Result{
			ErrorClass:  runtimepreflight.ErrorClassResolutionFailed,
			Message:     "failed",
			Remediation: []string{"retry"},
		},
	}
	var output bytes.Buffer
	if err := render(&output, value, true); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	for _, absent := range []string{"workspace", "agent", "backend", "backend_source", "health"} {
		if _, ok := fields[absent]; ok {
			t.Errorf("field %q must be omitted: %s", absent, output.String())
		}
	}
	for _, present := range []string{"schema_version", "kind", "ready", "error_class", "message", "remediation"} {
		if _, ok := fields[present]; !ok {
			t.Errorf("field %q must be present: %s", present, output.String())
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCommandWriterFailuresExitTwo(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	withHealth(t, func(string) (runtimepreflight.HealthStatus, bool) {
		return runtimepreflight.HealthStatus{Healthy: true, Installed: true}, true
	})
	for _, jsonOutput := range []bool{false, true} {
		name := "human"
		args := []string{"--ai-backend", "codex"}
		if jsonOutput {
			name = "json"
			args = append(args, "--json")
		}
		t.Run(name, func(t *testing.T) {
			cmd := newCommand(commandDeps{})
			cmd.SetContext(context.Background())
			cmd.SetOut(failingWriter{})
			if err := cmd.ParseFlags(args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			err := cmd.RunE(cmd, nil)
			if got := cli.CommandExitCode(err); got != 2 {
				t.Fatalf("exit code = %d error=%v, want 2", got, err)
			}
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("error = %v, want broken pipe", err)
			}
		})
	}
}
