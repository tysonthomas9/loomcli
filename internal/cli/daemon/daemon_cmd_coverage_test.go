package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPrintDryRunInfo_DefaultValues(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "alpha", Role: "plan", Auto: true},
			{Worktree: "beta", Role: "task", Auto: false},
		},
	}

	// Should not panic - exercises all default branches
	printDryRunInfo(config, "/tmp/pid", "/tmp/logs", "/tmp/state")
}

func TestPrintDryRunInfo_CustomValues(t *testing.T) {
	maxRetries := 5
	backoffInitial := 10
	backoffMax := 600
	maxAgents := 50

	config := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries:     &maxRetries,
				BackoffInitial: &backoffInitial,
				BackoffMax:     &backoffMax,
			},
			MaxAgents: &maxAgents,
		},
		Agents: []AgentEntry{
			{Worktree: "gamma", Role: "task", Auto: true},
		},
	}

	// Should not panic - exercises all custom value branches
	printDryRunInfo(config, "/var/run/loom.pid", "/var/log/loom", "/var/run/loom.state")
}

func TestPrintDryRunInfo_NoAgents(t *testing.T) {
	config := &DaemonConfig{
		Agents: []AgentEntry{},
	}

	// Should not panic even with no agents
	printDryRunInfo(config, "/tmp/pid", "/tmp/logs", "/tmp/state")
}

func TestRunDaemonDryRunUsesStartupHooks(t *testing.T) {
	restore := replaceDaemonStartupHooks(t)
	defer restore()

	projectDir := t.TempDir()
	cfg := testDaemonConfig(projectDir)
	daemonDryRun = true

	var isolated, loaded, validated, printed bool
	isolateProcessGroupFn = func() { isolated = true }
	daemonGetwdFn = func() (string, error) { return projectDir, nil }
	loadDaemonConfigFn = func(dir string) (*DaemonConfig, error) {
		loaded = dir == projectDir
		return cfg, nil
	}
	validateDaemonPathsFn = func(dir, pidFile, logDir string) {
		validated = dir == projectDir && pidFile != "" && logDir != ""
	}
	printDryRunInfoFn = func(got *DaemonConfig, pidFile, logDir, stateFile string) {
		printed = got == cfg && pidFile != "" && logDir != "" && stateFile != ""
	}
	prepareDaemonDirsFn = func(string, string) {
		t.Fatal("prepareDaemonDirs should not run for dry-run")
	}
	runDaemon(&cobra.Command{}, nil)

	if !isolated || !loaded || !validated || !printed {
		t.Fatalf("hooks isolated=%t loaded=%t validated=%t printed=%t", isolated, loaded, validated, printed)
	}
}

func TestRunDaemonStartsMainLoopThroughHooks(t *testing.T) {
	restore := replaceDaemonStartupHooks(t)
	defer restore()

	projectDir := t.TempDir()
	cfg := testDaemonConfig(projectDir)
	daemonDryRun = false

	var prepared, pidInitialized, servicesInitialized, mainLoopRan bool
	isolateProcessGroupFn = func() {}
	daemonGetwdFn = func() (string, error) { return projectDir, nil }
	loadDaemonConfigFn = func(string) (*DaemonConfig, error) { return cfg, nil }
	validateDaemonPathsFn = func(string, string, string) {}
	prepareDaemonDirsFn = func(pidFile, logDir string) {
		prepared = pidFile != "" && logDir != ""
		if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
			t.Fatalf("mkdir pid dir: %v", err)
		}
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			t.Fatalf("mkdir log dir: %v", err)
		}
	}
	acquireDaemonLockFn = func(lockFilePath string) *os.File {
		lock, err := os.OpenFile(lockFilePath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open lock: %v", err)
		}
		return lock
	}
	initPIDFileFn = func(pidFilePath string) {
		pidInitialized = pidFilePath != ""
		if err := os.WriteFile(pidFilePath, []byte("123\n"), 0o600); err != nil {
			t.Fatalf("write pid: %v", err)
		}
	}
	initDaemonServicesFn = func(got *DaemonConfig, gotProject string, paths daemonPaths) (chan struct{}, *Daemon) {
		servicesInitialized = got == cfg && gotProject == projectDir && paths.eventsDir != ""
		return make(chan struct{}), &Daemon{}
	}
	runDaemonMainLoopFn = func(got *DaemonConfig, gotProject string, paths daemonPaths, shutdown chan struct{}, d *Daemon, lock *os.File) {
		mainLoopRan = got == cfg && gotProject == projectDir && shutdown != nil && d != nil && lock != nil
		if err := os.WriteFile(paths.stateFile, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write state: %v", err)
		}
	}

	runDaemon(&cobra.Command{}, nil)

	if !prepared || !pidInitialized || !servicesInitialized || !mainLoopRan {
		t.Fatalf("startup prepared=%t pid=%t services=%t mainLoop=%t", prepared, pidInitialized, servicesInitialized, mainLoopRan)
	}
}

func TestInitDaemonServicesUsesStoreAndDaemonHooks(t *testing.T) {
	restore := replaceDaemonStartupHooks(t)
	defer restore()

	cfg := testDaemonConfig(t.TempDir())
	paths := resolveDaemonPaths(t.TempDir(), cfg)
	var signaled, openedStore, createdDaemon bool

	setupSignalHandlerFn = func() chan struct{} {
		signaled = true
		return make(chan struct{})
	}
	cmdstoreOpenStoreFn = func(ctx context.Context) (*bootstrap.StoreHandle, error) {
		if ctx == nil {
			t.Fatal("cmdstoreOpenStoreFn received nil context")
		}
		openedStore = true
		return nil, nil
	}
	newDaemonFn = func(got *DaemonConfig, projectDir string, eventBus events.Emitter, issueBackend backend.IssueBackend, st store.Store) (*Daemon, error) {
		createdDaemon = got == cfg && projectDir != "" && eventBus != nil && issueBackend != nil && st == nil
		return &Daemon{}, nil
	}

	shutdown, d := initDaemonServices(cfg, t.TempDir(), paths)
	if shutdown == nil || d == nil {
		t.Fatalf("initDaemonServices returned shutdown=%v daemon=%v", shutdown, d)
	}
	if !signaled || !openedStore || !createdDaemon {
		t.Fatalf("hooks signaled=%t openedStore=%t createdDaemon=%t", signaled, openedStore, createdDaemon)
	}
}

func testDaemonConfig(projectDir string) *DaemonConfig {
	return &DaemonConfig{
		Daemon: DaemonSettings{
			PIDFile:   filepath.Join(projectDir, ".loom", "daemon.pid"),
			LogDir:    filepath.Join(projectDir, ".loom", "logs"),
			EventsDir: filepath.Join(projectDir, ".loom", "events"),
		},
		Roles:  map[string]RoleConfig{},
		Agents: []AgentEntry{},
	}
}

func replaceDaemonStartupHooks(t *testing.T) func() {
	t.Helper()
	oldDryRun := daemonDryRun
	oldIsolate := isolateProcessGroupFn
	oldGetwd := daemonGetwdFn
	oldLoad := loadDaemonConfigFn
	oldValidate := validateDaemonPathsFn
	oldPrintDryRun := printDryRunInfoFn
	oldPrepare := prepareDaemonDirsFn
	oldAcquire := acquireDaemonLockFn
	oldInitPID := initPIDFileFn
	oldInitServices := initDaemonServicesFn
	oldMainLoop := runDaemonMainLoopFn
	oldSetupSignal := setupSignalHandlerFn
	oldInitOTel := initOTelExporterFn
	oldOpenStore := cmdstoreOpenStoreFn
	oldNewDaemon := newDaemonFn
	return func() {
		daemonDryRun = oldDryRun
		isolateProcessGroupFn = oldIsolate
		daemonGetwdFn = oldGetwd
		loadDaemonConfigFn = oldLoad
		validateDaemonPathsFn = oldValidate
		printDryRunInfoFn = oldPrintDryRun
		prepareDaemonDirsFn = oldPrepare
		acquireDaemonLockFn = oldAcquire
		initPIDFileFn = oldInitPID
		initDaemonServicesFn = oldInitServices
		runDaemonMainLoopFn = oldMainLoop
		setupSignalHandlerFn = oldSetupSignal
		initOTelExporterFn = oldInitOTel
		cmdstoreOpenStoreFn = oldOpenStore
		newDaemonFn = oldNewDaemon
	}
}
