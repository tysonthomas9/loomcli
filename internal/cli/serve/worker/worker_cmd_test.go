package worker

import (
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
)

// ---------------------------------------------------------------------------
// isUUIDFormat tests (loomcli-n28bt.10)
// ---------------------------------------------------------------------------

func TestIsUUIDFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid UUID v4",
			input: "550e8400-e29b-41d4-a716-446655440000",
			want:  true,
		},
		{
			name:  "valid UUID nil",
			input: "00000000-0000-0000-0000-000000000000",
			want:  true,
		},
		{
			name:  "workspace name",
			input: "my-workspace",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "partial UUID",
			input: "550e8400-e29b-41d4",
			want:  false,
		},
		{
			name:  "UUID without dashes",
			input: "550e8400e29b41d4a716446655440000",
			want:  true, // uuid.Parse accepts this form
		},
		{
			name:  "just numbers",
			input: "12345",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUUIDFormat(tt.input)
			if got != tt.want {
				t.Errorf("isUUIDFormat(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveWorkerWorkspace tests (loomcli-n28bt.10)
// ---------------------------------------------------------------------------

func TestResolveWorkerWorkspace_AlreadyUUID(t *testing.T) {
	// When workspace is already a valid UUID, it should be returned as-is
	// without attempting to load config.
	uuid := "550e8400-e29b-41d4-a716-446655440000"
	got := resolveWorkerWorkspace(uuid)
	if got != uuid {
		t.Errorf("resolveWorkerWorkspace(%q) = %q, want %q", uuid, got, uuid)
	}
}

func TestResolveWorkerWorkspace_NonUUIDFallsBack(t *testing.T) {
	// When workspace is not a UUID and config is unavailable, the original
	// value should be returned as-is (graceful fallback).
	name := "my-workspace"
	got := resolveWorkerWorkspace(name)
	// Without a valid config file, it should fall back to returning the name.
	// We can't easily assert what it returns beyond "no panic", but since
	// LoadConfig will fail in a test env without config, we expect the name back.
	if got == "" {
		t.Error("resolveWorkerWorkspace returned empty string, want non-empty fallback")
	}
}

func TestValidateWorkerFlagsSuccessReturnsTokenAndWorktree(t *testing.T) {
	oldControl, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControl, oldWorkspace, oldAgent, oldBackend
	})
	workerControlPlane = "http://control.test"
	workerWorkspace = "WS"
	workerAgent = "nova"
	workerBackend = ""
	t.Setenv("LOOM_WORKER_TOKEN", "worker-token")

	token, worktreePath := validateWorkerFlags()
	if token != "worker-token" {
		t.Fatalf("token = %q, want worker-token", token)
	}
	if worktreePath == "" {
		t.Fatal("worktree path was empty")
	}
}

func TestValidateWorkerFlagsExitBranches(t *testing.T) {
	oldControl, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	oldExit := workerExitFn
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControl, oldWorkspace, oldAgent, oldBackend
		workerExitFn = oldExit
	})

	tests := []struct {
		name      string
		control   string
		workspace string
		agent     string
		backend   string
	}{
		{name: "missing control", workspace: "WS", agent: "nova"},
		{name: "missing workspace", control: "http://control.test", agent: "nova"},
		{name: "missing agent", control: "http://control.test", workspace: "WS"},
		{name: "invalid backend", control: "http://control.test", workspace: "WS", agent: "nova", backend: "invalid-backend"},
	}
	type exitCalled int
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workerControlPlane = tt.control
			workerWorkspace = tt.workspace
			workerAgent = tt.agent
			workerBackend = tt.backend
			workerExitFn = func(code int) { panic(exitCalled(code)) }
			defer func() {
				got := recover()
				if got != exitCalled(1) {
					t.Fatalf("recover = %#v, want exitCalled(1)", got)
				}
			}()
			validateWorkerFlags()
		})
	}
}

func TestRunWorkerUsesLifecycleHooks(t *testing.T) {
	oldControl, oldWorkspace, oldAgent, oldBackend := workerControlPlane, workerWorkspace, workerAgent, workerBackend
	oldInterval, oldMaxTasks, oldIdleTimeout, oldParent := workerInterval, workerMaxTasks, workerIdleTimeout, workerParentID
	oldValidate := validateWorkerFlagsFn
	oldResolve := resolveWorkerWorkspaceFn
	oldPrint := printWorkerBannerFn
	oldRegister := registerAndGetTokenFn
	oldSetup := setupWorkerInterfacesFn
	oldShutdown := setupWorkerShutdownFn
	oldAcquire := acquireWorkerLockFn
	oldRelease := releaseWorkerLockFn
	oldRunLoop := runAutoModeLoopFn
	oldCleanup := cleanupWorkerResourcesFn
	oldDeregister := deregisterWorkerFn
	oldExit := workerExitFn
	t.Cleanup(func() {
		workerControlPlane, workerWorkspace, workerAgent, workerBackend = oldControl, oldWorkspace, oldAgent, oldBackend
		workerInterval, workerMaxTasks, workerIdleTimeout, workerParentID = oldInterval, oldMaxTasks, oldIdleTimeout, oldParent
		validateWorkerFlagsFn = oldValidate
		resolveWorkerWorkspaceFn = oldResolve
		printWorkerBannerFn = oldPrint
		registerAndGetTokenFn = oldRegister
		setupWorkerInterfacesFn = oldSetup
		setupWorkerShutdownFn = oldShutdown
		acquireWorkerLockFn = oldAcquire
		releaseWorkerLockFn = oldRelease
		runAutoModeLoopFn = oldRunLoop
		cleanupWorkerResourcesFn = oldCleanup
		deregisterWorkerFn = oldDeregister
		workerExitFn = oldExit
	})

	workerControlPlane = "http://control.test"
	workerWorkspace = "WS"
	workerAgent = "nova"
	workerBackend = "codex"
	workerInterval = 7
	workerMaxTasks = 3
	workerIdleTimeout = 11
	workerParentID = "EPIC-1"

	shutdown := make(chan struct{})
	var printed, acquired, released, ran, cleaned bool
	validateWorkerFlagsFn = func() (string, string) { return "bootstrap-token", "/repo" }
	resolveWorkerWorkspaceFn = func(workspace string) string {
		if workspace != "WS" {
			t.Fatalf("resolve workspace arg = %q", workspace)
		}
		return "WS-UUID"
	}
	printWorkerBannerFn = func(worktreePath string) {
		if worktreePath != "/repo" {
			t.Fatalf("print banner path = %q", worktreePath)
		}
		printed = true
	}
	registerAndGetTokenFn = func(token string) (*workerRegistration, string) {
		if token != "bootstrap-token" {
			t.Fatalf("register token = %q", token)
		}
		return &workerRegistration{WorkerID: "worker-1", Token: "issued-token"}, "issued-token"
	}
	setupWorkerInterfacesFn = func(reg *workerRegistration, authToken string) (*cli.HTTPLockBridge, *automode.HTTPEventEmitter, *LogForwarder) {
		if reg.WorkerID != "worker-1" || authToken != "issued-token" {
			t.Fatalf("setup args reg=%+v auth=%q", reg, authToken)
		}
		return nil, nil, nil
	}
	setupWorkerShutdownFn = func() chan struct{} { return shutdown }
	acquireWorkerLockFn = func(worktreePath, command, agentName string) error {
		if worktreePath != "/repo" || command != "worker" || agentName != "nova" {
			t.Fatalf("acquire args path=%q command=%q agent=%q", worktreePath, command, agentName)
		}
		acquired = true
		return nil
	}
	releaseWorkerLockFn = func(worktreePath string) error {
		if worktreePath != "/repo" {
			t.Fatalf("release path = %q", worktreePath)
		}
		released = true
		return nil
	}
	runAutoModeLoopFn = func(opts automode.AutoModeOptions, gotShutdown chan struct{}) {
		if gotShutdown != shutdown {
			t.Fatal("run loop got different shutdown channel")
		}
		if opts.Interval != 7 || opts.MaxTasks != 3 || opts.IdleTimeout != 11 || opts.ParentID != "EPIC-1" ||
			opts.AgentType != "task" || opts.AgentName != "nova" || opts.WorktreePath != "/repo" {
			t.Fatalf("auto mode opts = %+v", opts)
		}
		ran = true
	}
	cleanupWorkerResourcesFn = func(_ *LogForwarder, _ *automode.HTTPEventEmitter, authToken, workerID string) {
		if authToken != "issued-token" || workerID != "worker-1" {
			t.Fatalf("cleanup auth=%q worker=%q", authToken, workerID)
		}
		cleaned = true
	}
	deregisterWorkerFn = func(string, string, string) {
		t.Fatal("deregister should not run on successful lock acquisition")
	}
	workerExitFn = func(code int) {
		t.Fatalf("workerExitFn called with %d", code)
	}

	runWorker(nil, nil)
	if !printed || !acquired || !released || !ran || !cleaned {
		t.Fatalf("printed=%t acquired=%t released=%t ran=%t cleaned=%t", printed, acquired, released, ran, cleaned)
	}
	if workerWorkspace != "WS-UUID" {
		t.Fatalf("workerWorkspace = %q, want resolved UUID", workerWorkspace)
	}
}

func TestRunWorkerDeregistersWhenLockFails(t *testing.T) {
	type exitCalled int
	oldValidate := validateWorkerFlagsFn
	oldResolve := resolveWorkerWorkspaceFn
	oldPrint := printWorkerBannerFn
	oldRegister := registerAndGetTokenFn
	oldSetup := setupWorkerInterfacesFn
	oldShutdown := setupWorkerShutdownFn
	oldAcquire := acquireWorkerLockFn
	oldRelease := releaseWorkerLockFn
	oldDeregister := deregisterWorkerFn
	oldExit := workerExitFn
	oldControl, oldWorkspace, oldAgent := workerControlPlane, workerWorkspace, workerAgent
	t.Cleanup(func() {
		validateWorkerFlagsFn = oldValidate
		resolveWorkerWorkspaceFn = oldResolve
		printWorkerBannerFn = oldPrint
		registerAndGetTokenFn = oldRegister
		setupWorkerInterfacesFn = oldSetup
		setupWorkerShutdownFn = oldShutdown
		acquireWorkerLockFn = oldAcquire
		releaseWorkerLockFn = oldRelease
		deregisterWorkerFn = oldDeregister
		workerExitFn = oldExit
		workerControlPlane, workerWorkspace, workerAgent = oldControl, oldWorkspace, oldAgent
	})
	workerControlPlane = "http://control.test"
	workerWorkspace = "WS"
	workerAgent = "nova"
	validateWorkerFlagsFn = func() (string, string) { return "bootstrap-token", "/repo" }
	resolveWorkerWorkspaceFn = func(workspace string) string { return workspace }
	printWorkerBannerFn = func(string) {}
	registerAndGetTokenFn = func(string) (*workerRegistration, string) {
		return &workerRegistration{WorkerID: "worker-1"}, "bootstrap-token"
	}
	setupWorkerInterfacesFn = func(*workerRegistration, string) (*cli.HTTPLockBridge, *automode.HTTPEventEmitter, *LogForwarder) {
		return nil, nil, nil
	}
	setupWorkerShutdownFn = func() chan struct{} { return make(chan struct{}) }
	acquireWorkerLockFn = func(string, string, string) error { return errors.New("locked") }
	releaseWorkerLockFn = func(string) error {
		t.Fatal("release should not run when lock acquisition fails")
		return nil
	}
	deregistered := false
	deregisterWorkerFn = func(controlPlane, authToken, workerID string) {
		if controlPlane != "http://control.test" || authToken != "bootstrap-token" || workerID != "worker-1" {
			t.Fatalf("deregister args control=%q auth=%q worker=%q", controlPlane, authToken, workerID)
		}
		deregistered = true
	}
	workerExitFn = func(code int) { panic(exitCalled(code)) }

	defer func() {
		got := recover()
		if got != exitCalled(1) {
			t.Fatalf("recover = %#v, want exitCalled(1)", got)
		}
		if !deregistered {
			t.Fatal("worker did not deregister after lock failure")
		}
	}()
	runWorker(nil, nil)
}
