package runtimepreflight

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type targetStoreStub struct {
	agent     *domain.Agent
	agentErr  error
	profile   *domain.DaemonProfile
	daemonErr error
}

func (s targetStoreStub) Agents() store.AgentStore {
	return agentStoreStub{agent: s.agent, err: s.agentErr}
}

func (s targetStoreStub) Daemon() store.DaemonProfileStore {
	return daemonStoreStub{profile: s.profile, err: s.daemonErr}
}

type agentStoreStub struct {
	agent *domain.Agent
	err   error
}

func (s agentStoreStub) Create(context.Context, store.AgentCreate) (*domain.Agent, error) {
	return nil, errors.New("unexpected agent create")
}

func (s agentStoreStub) Get(context.Context, string, string) (*domain.Agent, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.agent == nil {
		return nil, domain.ErrNotFound
	}
	return s.agent, nil
}

func (s agentStoreStub) List(context.Context, string) ([]*domain.Agent, error) {
	return nil, errors.New("unexpected agent list")
}

func (s agentStoreStub) Update(context.Context, string, string, store.AgentUpdate) (*domain.Agent, error) {
	return nil, errors.New("unexpected agent update")
}

func (s agentStoreStub) Delete(context.Context, string, string) error {
	return errors.New("unexpected agent delete")
}

type daemonStoreStub struct {
	profile *domain.DaemonProfile
	err     error
}

func (s daemonStoreStub) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return s.profile, s.err
}

func (s daemonStoreStub) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return nil, errors.New("unexpected daemon upsert")
}

func healthyProbe(string) (HealthStatus, bool) {
	return HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"}, true
}

func checkWithProbe(
	t *testing.T,
	ctx context.Context,
	st targetStore,
	req Request,
	probe func(string) (HealthStatus, bool),
) (Result, error) {
	t.Helper()
	restore := SetHealthCheckerForTest(probe)
	t.Cleanup(restore)
	return CheckLocalTaskRunner(ctx, st, req)
}

func TestCheckLocalTaskRunnerResolutionPrecedence(t *testing.T) {
	t.Setenv("LOOM_BACKEND", "claude")
	cases := []struct {
		name        string
		store       targetStore
		request     Request
		wantBackend string
		wantSource  BackendSource
	}{
		{
			name:        "explicit override without workspace",
			request:     Request{BackendOverride: " gemini "},
			wantBackend: "gemini",
			wantSource:  BackendSourceOverride,
		},
		{
			name: "explicit override still validates required agent",
			store: targetStoreStub{
				agent:   &domain.Agent{Name: "worker-a", Backend: "claude"},
				profile: &domain.DaemonProfile{AgentBackend: "opencode"},
			},
			request:     Request{WorkspaceKey: "ACME", AgentName: "worker-a", AgentRequired: true, BackendOverride: "gemini"},
			wantBackend: "gemini",
			wantSource:  BackendSourceOverride,
		},
		{
			name:        "agent backend",
			store:       targetStoreStub{agent: &domain.Agent{Backend: "cursor"}, profile: &domain.DaemonProfile{AgentBackend: "gemini"}},
			request:     Request{WorkspaceKey: "ACME", AgentName: "worker-a", AgentRequired: true},
			wantBackend: "cursor",
			wantSource:  BackendSourceAgent,
		},
		{
			name:        "blank agent backend falls through",
			store:       targetStoreStub{agent: &domain.Agent{Backend: "  "}, profile: &domain.DaemonProfile{AgentBackend: "gemini"}},
			request:     Request{WorkspaceKey: "ACME", AgentName: "worker-a", AgentRequired: true},
			wantBackend: "gemini",
			wantSource:  BackendSourceWorkspace,
		},
		{
			name:        "optional missing agent falls through",
			store:       targetStoreStub{profile: &domain.DaemonProfile{AgentBackend: "opencode"}},
			request:     Request{WorkspaceKey: "ACME", AgentName: "worker-a"},
			wantBackend: "opencode",
			wantSource:  BackendSourceWorkspace,
		},
		{
			name:        "workspace backend",
			store:       targetStoreStub{profile: &domain.DaemonProfile{AgentBackend: "claude"}},
			request:     Request{WorkspaceKey: "ACME"},
			wantBackend: "claude",
			wantSource:  BackendSourceWorkspace,
		},
		{
			name:        "codex default ignores LOOM_BACKEND",
			store:       targetStoreStub{profile: &domain.DaemonProfile{AgentBackend: "  "}},
			request:     Request{WorkspaceKey: "ACME"},
			wantBackend: "codex",
			wantSource:  BackendSourceDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var checked string
			result, err := checkWithProbe(t, context.Background(), tc.store, tc.request, func(name string) (HealthStatus, bool) {
				checked = name
				return healthyProbe(name)
			})
			if err != nil {
				t.Fatalf("CheckLocalTaskRunner() error = %v", err)
			}
			if result.Backend != tc.wantBackend || result.BackendSource != tc.wantSource {
				t.Fatalf("resolution = (%q, %q), want (%q, %q)", result.Backend, result.BackendSource, tc.wantBackend, tc.wantSource)
			}
			if checked != tc.wantBackend {
				t.Fatalf("health checked backend %q, want %q", checked, tc.wantBackend)
			}
			if !result.Ready {
				t.Fatalf("result = %+v, want ready", result)
			}
		})
	}
}

func TestCheckLocalTaskRunnerClassification(t *testing.T) {
	cases := []struct {
		name       string
		backend    string
		status     HealthStatus
		probeOK    bool
		wantReady  bool
		wantClass  ErrorClass
		wantHealth bool
	}{
		{"unknown backend", "made-up", HealthStatus{}, false, false, ErrorClassUnavailable, false},
		{"missing health capability", "codex", HealthStatus{}, false, false, ErrorClassUnavailable, false},
		{"healthy unsupported backend", "echo", HealthStatus{Healthy: true, Installed: true, APIKeySet: true}, true, false, ErrorClassUnsupported, true},
		{"unhealthy unsupported backend", "echo", HealthStatus{Installed: true, APIKeySet: true}, true, false, ErrorClassUnsupported, true},
		{"binary missing", "codex", HealthStatus{Installed: false}, true, false, ErrorClassUnavailable, true},
		{"healthy but binary missing", "codex", HealthStatus{Healthy: true, Installed: false, APIKeySet: true}, true, false, ErrorClassUnavailable, true},
		{"authentication missing", "codex", HealthStatus{Installed: true, APIKeySet: false}, true, false, ErrorClassAuthMissing, true},
		{"installed authenticated unhealthy", "gemini", HealthStatus{Installed: true, APIKeySet: true}, true, false, ErrorClassUnhealthy, true},
		{"healthy", "claude", HealthStatus{Healthy: true, Installed: true, APIKeySet: true}, true, true, "", true},
		{"healthy without API key signal", "opencode", HealthStatus{Healthy: true, Installed: true, APIKeySet: false}, true, true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := checkWithProbe(t, context.Background(), nil, Request{BackendOverride: tc.backend}, func(string) (HealthStatus, bool) {
				return tc.status, tc.probeOK
			})
			if err != nil {
				t.Fatalf("CheckLocalTaskRunner() error = %v", err)
			}
			if result.Ready != tc.wantReady || result.ErrorClass != tc.wantClass {
				t.Fatalf("verdict = ready:%v class:%q, want ready:%v class:%q", result.Ready, result.ErrorClass, tc.wantReady, tc.wantClass)
			}
			if (result.Health != nil) != tc.wantHealth {
				t.Fatalf("health = %#v, want present %v", result.Health, tc.wantHealth)
			}
			if tc.wantHealth && *result.Health != boundedHealthStatus(tc.status) {
				t.Fatalf("health = %+v, want canonical projection %+v", *result.Health, tc.status)
			}
			if tc.wantReady && (len(result.Remediation) != 0 || result.ErrorClass != "") {
				t.Fatalf("ready result = %+v, want no class or remediation", result)
			}
			if !tc.wantReady && (result.Message == "" || len(result.Remediation) == 0) {
				t.Fatalf("not-ready result = %+v, want message and remediation", result)
			}
		})
	}
}

func TestCheckLocalTaskRunnerResolutionFailures(t *testing.T) {
	storeFailure := errors.New("fleet unavailable")
	cases := []struct {
		name        string
		store       targetStore
		request     Request
		wantMessage string
	}{
		{"agent not found", targetStoreStub{}, Request{WorkspaceKey: "ACME", AgentName: "missing", AgentRequired: true}, `agent "missing" was not found in workspace "ACME"`},
		{"agent read failure", targetStoreStub{agentErr: storeFailure}, Request{WorkspaceKey: "ACME", AgentName: "worker-a", AgentRequired: true}, `backend configuration for agent "worker-a" in workspace "ACME" could not be read`},
		{"daemon profile read failure", targetStoreStub{daemonErr: storeFailure}, Request{WorkspaceKey: "ACME"}, `backend configuration for workspace "ACME" could not be read`},
		{"agent missing workspace", nil, Request{AgentName: "worker-a", AgentRequired: true, BackendOverride: "codex"}, `agent "worker-a" requires an active workspace`},
		{"workspace missing", nil, Request{}, `an active workspace is required when no backend override is provided`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			result, err := checkWithProbe(t, context.Background(), tc.store, tc.request, func(string) (HealthStatus, bool) {
				called = true
				return healthyProbe("")
			})
			if err == nil {
				t.Fatal("CheckLocalTaskRunner() error = nil, want operational failure")
			}
			if called {
				t.Fatal("health probe ran after resolution failure")
			}
			if result.ErrorClass != ErrorClassResolutionFailed || result.Message != tc.wantMessage {
				t.Fatalf("result = %+v, want resolution failure message %q", result, tc.wantMessage)
			}
			if result.Backend != "" || result.BackendSource != "" || result.Health != nil {
				t.Fatalf("result = %+v, want unresolved backend and nil health", result)
			}
		})
	}
}

func TestCheckLocalTaskRunnerKeepsStoreDetailOutOfAuthoredFields(t *testing.T) {
	const sentinelURL = "http://127.0.0.1:8280/fleetdb"
	storeFailure := errors.New(sentinelURL)
	cases := []struct {
		name    string
		store   targetStore
		request Request
	}{
		{
			name:    "agent store",
			store:   targetStoreStub{agentErr: storeFailure},
			request: Request{WorkspaceKey: "ACME", AgentName: "worker-a", AgentRequired: true},
		},
		{
			name:    "daemon profile store",
			store:   targetStoreStub{daemonErr: storeFailure},
			request: Request{WorkspaceKey: "ACME"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := SetHealthCheckerForTest(healthyProbe)
			t.Cleanup(restore)
			result, err := CheckLocalTaskRunner(context.Background(), tc.store, tc.request)
			if !errors.Is(err, storeFailure) || !strings.Contains(err.Error(), sentinelURL) {
				t.Fatalf("CheckLocalTaskRunner() error = %v, want wrapped sentinel store failure", err)
			}
			authored := result.Message + " " + strings.Join(result.Remediation, " ")
			if strings.Contains(authored, sentinelURL) {
				t.Fatalf("Loom-authored fields leaked store detail: %+v", result)
			}
			if result.ErrorClass != ErrorClassResolutionFailed {
				t.Fatalf("error class = %q, want %q", result.ErrorClass, ErrorClassResolutionFailed)
			}
		})
	}
}

func TestCheckLocalTaskRunnerContextCanceledBeforeProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	result, err := checkWithProbe(t, ctx, nil, Request{BackendOverride: "codex"}, func(string) (HealthStatus, bool) {
		called = true
		return healthyProbe("")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckLocalTaskRunner() error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("health probe ran with canceled context")
	}
	if result.ErrorClass != ErrorClassResolutionFailed || result.Health != nil {
		t.Fatalf("result = %+v, want operational resolution failure with nil health", result)
	}
	if result.Backend != "codex" || result.BackendSource != BackendSourceOverride {
		t.Fatalf("result = %+v, want resolved override retained", result)
	}
}

func TestCheckLocalTaskRunnerKeepsAdapterDetailOutOfAuthoredFields(t *testing.T) {
	const adapterDetail = "Authorization: Bearer secret-value"
	result, err := checkWithProbe(t, context.Background(), nil, Request{BackendOverride: "codex"}, func(string) (HealthStatus, bool) {
		return HealthStatus{Installed: true, APIKeySet: true, Message: adapterDetail}, true
	})
	if err != nil {
		t.Fatalf("CheckLocalTaskRunner() error = %v", err)
	}
	if result.Health == nil || result.Health.Message != adapterDetail {
		t.Fatalf("health = %#v, want capped passthrough detail", result.Health)
	}
	if strings.Contains(result.Message, "secret-value") || strings.Contains(strings.Join(result.Remediation, " "), "secret-value") {
		t.Fatalf("Loom-authored fields leaked adapter detail: %+v", result)
	}
}

func TestRequireLocalTaskRunnerReturnsTypedVerdict(t *testing.T) {
	restore := SetHealthCheckerForTest(func(string) (HealthStatus, bool) {
		return HealthStatus{Installed: false, Message: "binary missing"}, true
	})
	t.Cleanup(restore)
	err := RequireLocalTaskRunner(context.Background(), nil, Request{BackendOverride: "codex"})
	var notReady *NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("RequireLocalTaskRunner() error = %T %v, want *NotReadyError", err, err)
	}
	if notReady.PreflightClass() != string(ErrorClassUnavailable) || notReady.Result.ErrorClass != ErrorClassUnavailable {
		t.Fatalf("typed error = %+v, want unavailable result", notReady)
	}
	want := "backend codex CLI is not installed (local_backend_unavailable); next: install the codex CLI"
	if err.Error() != want {
		t.Fatalf("NotReadyError.Error() = %q, want %q", err, want)
	}
}

func TestRequireLocalTaskRunnerReturnsOperationalErrorUnchanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RequireLocalTaskRunner(ctx, nil, Request{BackendOverride: "codex"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RequireLocalTaskRunner() error = %v, want context.Canceled", err)
	}
	var notReady *NotReadyError
	if errors.As(err, &notReady) {
		t.Fatalf("operational error was converted to NotReadyError: %+v", notReady)
	}
}

func TestNewLocalTaskRunnerCheckerResolvesConcreteWorkerOnce(t *testing.T) {
	restore := SetHealthCheckerForTest(healthyProbe)
	t.Cleanup(restore)

	t.Run("agent override", func(t *testing.T) {
		agent := &domain.Agent{Backend: "claude"}
		checker := NewLocalTaskRunnerChecker(targetStoreStub{
			agent:   agent,
			profile: &domain.DaemonProfile{AgentBackend: "claude"},
		})
		agent.Backend = "cursor"
		backend, err := checker(context.Background(), "WS", "worker-a")
		if err != nil || backend != "cursor" {
			t.Fatalf("checker = %q, %v; want cursor, nil", backend, err)
		}
	})

	t.Run("missing agent falls through", func(t *testing.T) {
		checker := NewLocalTaskRunnerChecker(targetStoreStub{
			profile: &domain.DaemonProfile{AgentBackend: "gemini"},
		})
		backend, err := checker(context.Background(), "WS", "missing-worker")
		if err != nil || backend != "gemini" {
			t.Fatalf("checker = %q, %v; want gemini, nil", backend, err)
		}
	})
}

func TestNewLocalTaskRunnerCheckerAdmissionClassification(t *testing.T) {
	cases := []struct {
		name      string
		status    HealthStatus
		wantClass ErrorClass
	}{
		{
			name:   "healthy",
			status: HealthStatus{Healthy: true, Installed: true, APIKeySet: true, Message: "ready"},
		},
		{
			name:      "not installed",
			status:    HealthStatus{Installed: false, Message: "binary missing"},
			wantClass: ErrorClassUnavailable,
		},
		{
			name:      "not authenticated",
			status:    HealthStatus{Installed: true, APIKeySet: false, Message: "auth missing"},
			wantClass: ErrorClassAuthMissing,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restore := SetHealthCheckerForTest(func(string) (HealthStatus, bool) {
				return tc.status, true
			})
			t.Cleanup(restore)

			checker := NewLocalTaskRunnerChecker(targetStoreStub{
				profile: &domain.DaemonProfile{AgentBackend: "codex"},
			})
			backend, err := checker(context.Background(), "WS", "worker-a")
			if backend != "codex" {
				t.Fatalf("checker backend = %q, want codex", backend)
			}
			if tc.wantClass == "" {
				if err != nil {
					t.Fatalf("checker error = %v, want nil", err)
				}
				return
			}
			var notReady *NotReadyError
			if !errors.As(err, &notReady) || notReady.Result.ErrorClass != tc.wantClass {
				t.Fatalf("checker error = %v, want typed class %q", err, tc.wantClass)
			}
		})
	}
}

func TestNewLocalTaskRunnerCheckerSkipsVersionProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixture is a /bin/sh script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "version-probed")
	binary := filepath.Join(dir, "gemini")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n: > \"$LOOM_TEST_VERSION_MARKER\"\necho 9.9.9\n"), 0o755); err != nil {
		t.Fatalf("write fake gemini: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("LOOM_TEST_VERSION_MARKER", marker)

	checker := NewLocalTaskRunnerChecker(targetStoreStub{
		profile: &domain.DaemonProfile{AgentBackend: "gemini"},
	})
	backend, err := checker(context.Background(), "WS", "worker-a")
	if err != nil || backend != "gemini" {
		t.Fatalf("checker = %q, %v; want gemini, nil", backend, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("launch checker spawned --version; marker stat error = %v", err)
	}
}

func TestCheckLocalTaskRunnerForAdmissionContextCanceledDuringProbe(t *testing.T) {
	started := make(chan struct{})
	healthCheckerMu.Lock()
	previous := admissionHealthChecker
	admissionHealthChecker = func(ctx context.Context, _ string) (HealthStatus, bool) {
		close(started)
		<-ctx.Done()
		return HealthStatus{Healthy: true, Installed: true, APIKeySet: true}, true
	}
	healthCheckerMu.Unlock()
	t.Cleanup(func() {
		healthCheckerMu.Lock()
		admissionHealthChecker = previous
		healthCheckerMu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	result, err := CheckLocalTaskRunnerForAdmission(ctx, nil, Request{BackendOverride: "codex"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckLocalTaskRunnerForAdmission() error = %v, want context.Canceled", err)
	}
	if result.ErrorClass != ErrorClassResolutionFailed || result.Ready || result.Health != nil {
		t.Fatalf("result = %+v, want evaluation failure without a verdict", result)
	}
}

func TestResultJSONOmissionRules(t *testing.T) {
	cases := []struct {
		name    string
		result  Result
		present []string
		absent  []string
	}{
		{
			name:    "ready",
			result:  Result{Backend: "codex", BackendSource: BackendSourceDefault, Ready: true, Health: &HealthStatus{}, Message: "ready"},
			present: []string{"backend", "backend_source", "ready", "health", "message"},
			absent:  []string{"error_class", "remediation"},
		},
		{
			name:    "resolution failed before selection",
			result:  Result{ErrorClass: ErrorClassResolutionFailed, Message: "failed", Remediation: []string{"retry"}},
			present: []string{"ready", "error_class", "message", "remediation"},
			absent:  []string{"backend", "backend_source", "health"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.result)
			if err != nil {
				t.Fatalf("marshal Result: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatalf("unmarshal Result: %v", err)
			}
			for _, name := range tc.present {
				if _, ok := fields[name]; !ok {
					t.Errorf("field %q absent from %s", name, data)
				}
			}
			for _, name := range tc.absent {
				if _, ok := fields[name]; ok {
					t.Errorf("field %q present in %s", name, data)
				}
			}
		})
	}
}

func TestBoundedHealthStatus(t *testing.T) {
	status := boundedHealthStatus(HealthStatus{Version: strings.Repeat("v", 5000), Message: strings.Repeat("m", 5000)})
	if len([]rune(status.Version)) != 4096 || len([]rune(status.Message)) != 4096 {
		t.Fatalf("bounded lengths = (%d, %d), want (4096, 4096)", len([]rune(status.Version)), len([]rune(status.Message)))
	}
	if !strings.HasSuffix(status.Version, "…") {
		t.Fatalf("bounded version does not end in ellipsis: %q", status.Version[len(status.Version)-8:])
	}
}
