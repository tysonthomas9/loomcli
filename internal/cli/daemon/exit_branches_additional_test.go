package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

type daemonExitCode int

func expectDaemonExit(t *testing.T, want int, fn func()) {
	t.Helper()
	testingSetExitProcess(t, func(code int) {
		panic(daemonExitCode(code))
	})
	defer func() {
		t.Helper()
		got := recover()
		if got == nil {
			t.Fatalf("expected exitProcess(%d)", want)
		}
		code, ok := got.(daemonExitCode)
		if !ok {
			panic(got)
		}
		if int(code) != want {
			t.Fatalf("exit code = %d, want %d", code, want)
		}
	}()
	fn()
}

func TestDaemonExitBranchesWithInjectedExit(t *testing.T) {
	t.Run("control helpers exit on socket errors", func(t *testing.T) {
		restore := replaceDaemonControlHooks(t)
		defer restore()

		resolveControlSocketFromCwdFn = func() (string, error) { return "", errors.New("no socket") }
		expectDaemonExit(t, 1, func() { runDaemonAgentStop("nova", true, time.Second) })

		resolveControlSocketFromCwdFn = func() (string, error) { return "/sock", nil }
		sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
			return nil, errors.New("send failed")
		}
		expectDaemonExit(t, 1, func() { _ = requestYieldOrFallback("/sock", "nova") })

		sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
			return &DaemonControlResponse{Success: false, Error: "agent not found"}, nil
		}
		expectDaemonExit(t, 1, func() { _ = requestYieldOrFallback("/sock", "nova") })

		sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
			return nil, errors.New("stop failed")
		}
		expectDaemonExit(t, 1, func() { forceStopAgent("/sock", "nova") })

		sendDaemonControlRequestFullFn = func(string, DaemonControlRequest) (*DaemonControlResponse, error) {
			return &DaemonControlResponse{Success: false, Error: "permission denied"}, nil
		}
		expectDaemonExit(t, 1, func() { forceStopAgent("/sock", "nova") })
	})

	t.Run("agent start and restart exit on failures", func(t *testing.T) {
		restore := replaceDaemonControlHooks(t)
		defer restore()

		resolveControlSocketFromCwdFn = func() (string, error) { return "", errors.New("no cwd") }
		expectDaemonExit(t, 1, func() { runDaemonAgentStart(&cobra.Command{}, []string{"nova"}) })
		expectDaemonExit(t, 1, func() { runDaemonAgentRestart(&cobra.Command{}, []string{"nova"}) })

		resolveControlSocketFromCwdFn = func() (string, error) { return "/sock", nil }
		sendDaemonControlRequestFn = func(string, string, string) (*DaemonControlResponse, error) {
			return nil, errors.New("send failed")
		}
		expectDaemonExit(t, 1, func() { runDaemonAgentStart(&cobra.Command{}, []string{"nova"}) })
		expectDaemonExit(t, 1, func() { runDaemonAgentRestart(&cobra.Command{}, []string{"nova"}) })

		sendDaemonControlRequestFn = func(string, string, string) (*DaemonControlResponse, error) {
			return &DaemonControlResponse{Success: false, Error: "rejected"}, nil
		}
		expectDaemonExit(t, 1, func() { runDaemonAgentStart(&cobra.Command{}, []string{"nova"}) })
		expectDaemonExit(t, 1, func() { runDaemonAgentRestart(&cobra.Command{}, []string{"nova"}) })
	})

	t.Run("log helpers exit on missing state and logs", func(t *testing.T) {
		cfg := &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{LogDir: filepath.Join(t.TempDir(), "logs")}}
		expectDaemonExit(t, 1, func() { listAgentLogs(t.TempDir(), cfg, nil, errors.New("missing state")) })

		state := &DaemonState{Agents: []DaemonAgentStatus{{Worktree: "nova", Role: "task"}}}
		expectDaemonExit(t, 1, func() { _ = findAgent("missing", state, nil) })
		expectDaemonExit(t, 1, func() { _ = findAgent("missing", nil, errors.New("missing state")) })
		expectDaemonExit(t, 1, func() { showAgentLog(filepath.Join(t.TempDir(), "missing.log")) })

		dirAsLog := t.TempDir()
		expectDaemonExit(t, 1, func() { showAgentLog(dirAsLog) })
	})

	t.Run("runDaemon hook errors and dry run", func(t *testing.T) {
		restore := replaceDaemonRunHooks(t)
		defer restore()

		isolateProcessGroupFn = func() {}
		daemonGetwdFn = func() (string, error) { return "", errors.New("no cwd") }
		expectDaemonExit(t, 1, func() { runDaemon(&cobra.Command{}, nil) })

		daemonGetwdFn = func() (string, error) { return t.TempDir(), nil }
		loadDaemonConfigFn = func(string) (*cfgpkg.DaemonConfig, error) {
			return nil, errors.New("bad config")
		}
		expectDaemonExit(t, 1, func() { runDaemon(&cobra.Command{}, nil) })

		projectDir := t.TempDir()
		daemonGetwdFn = func() (string, error) { return projectDir, nil }
		cfg := &cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{
			PIDFile:   ".loom/daemon.pid",
			LogDir:    ".loom/logs",
			EventsDir: ".loom/events",
		}, Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "task"}}}
		loadDaemonConfigFn = func(string) (*cfgpkg.DaemonConfig, error) { return cfg, nil }
		var validated, printed bool
		validateDaemonPathsFn = func(string, string, string) { validated = true }
		printDryRunInfoFn = func(*cfgpkg.DaemonConfig, string, string, string) { printed = true }
		daemonDryRun = true
		t.Cleanup(func() { daemonDryRun = false })
		runDaemon(&cobra.Command{}, nil)
		if !validated || !printed {
			t.Fatalf("dry run validated=%t printed=%t", validated, printed)
		}
	})

	t.Run("initDaemonServices exits when store open fails", func(t *testing.T) {
		restore := replaceDaemonRunHooks(t)
		defer restore()

		setupSignalHandlerFn = func() chan struct{} { return make(chan struct{}) }
		cmdstoreOpenStoreFn = func(context.Context) (*bootstrap.StoreHandle, error) {
			return nil, errors.New("store offline")
		}
		cfg := &cfgpkg.DaemonConfig{}
		paths := daemonPaths{eventsDir: filepath.Join(t.TempDir(), "events")}
		expectDaemonExit(t, 1, func() {
			_, _ = initDaemonServices(cfg, t.TempDir(), paths)
		})
	})
}

func TestDaemonFileAndRuntimeAdditionalBranches(t *testing.T) {
	t.Run("file helpers exit on invalid paths and held locks", func(t *testing.T) {
		tmp := t.TempDir()
		parentFile := filepath.Join(tmp, "not-a-dir")
		if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
			t.Fatalf("write parent file: %v", err)
		}
		expectDaemonExit(t, 1, func() {
			prepareDaemonDirs(filepath.Join(parentFile, "daemon.pid"), filepath.Join(tmp, "logs"))
		})
		expectDaemonExit(t, 1, func() {
			initPIDFile(filepath.Join(parentFile, "daemon.pid"))
		})

		lockPath := filepath.Join(tmp, "daemon.lock")
		first := acquireDaemonLock(lockPath)
		defer func() { _ = first.Close() }()
		expectDaemonExit(t, 1, func() { _ = acquireDaemonLock(lockPath) })
	})

	t.Run("runDaemonStop uses changed timeout flag", func(t *testing.T) {
		restore := replaceDaemonControlHooks(t)
		defer restore()
		oldForce, oldTimeout := daemonStopForce, daemonStopTimeout
		t.Cleanup(func() {
			daemonStopForce, daemonStopTimeout = oldForce, oldTimeout
		})

		cmd := &cobra.Command{}
		cmd.Flags().Int("timeout", 0, "")
		if err := cmd.Flags().Set("timeout", "3"); err != nil {
			t.Fatalf("set timeout: %v", err)
		}
		daemonStopTimeout = 3
		var gotTimeout time.Duration
		runDaemonAgentStopFn = func(agentName string, force bool, timeout time.Duration) {
			if agentName != "nova" || force {
				t.Fatalf("unexpected stop args agent=%q force=%t", agentName, force)
			}
			gotTimeout = timeout
		}
		runDaemonStop(cmd, []string{"nova"})
		if gotTimeout != 3*time.Second {
			t.Fatalf("timeout = %v, want 3s", gotTimeout)
		}
	})

	t.Run("runDaemonStop daemon process force and graceful", func(t *testing.T) {
		restore := replaceDaemonControlHooks(t)
		defer restore()
		oldForce := daemonStopForce
		t.Cleanup(func() { daemonStopForce = oldForce })

		projectDir := t.TempDir()
		t.Chdir(projectDir)
		stateDir := filepath.Join(projectDir, ".loom")
		if err := os.MkdirAll(stateDir, 0755); err != nil {
			t.Fatalf("mkdir state dir: %v", err)
		}
		if err := writeStateFile(filepath.Join(stateDir, "daemon-agents.json"), time.Now(), nil, 1); err != nil {
			t.Fatalf("write state file: %v", err)
		}

		var gracefulPID, forcePID int
		stopDaemonGracefulFn = func(pid int) { gracefulPID = pid }
		stopDaemonForceFn = func(pid int) { forcePID = pid }

		daemonStopForce = false
		runDaemonStop(&cobra.Command{}, nil)
		if gracefulPID != os.Getpid() || forcePID != 0 {
			t.Fatalf("gracefulPID=%d forcePID=%d want graceful current pid", gracefulPID, forcePID)
		}

		daemonStopForce = true
		runDaemonStop(&cobra.Command{}, nil)
		if forcePID != os.Getpid() {
			t.Fatalf("forcePID=%d want current pid", forcePID)
		}
	})

	t.Run("otel exporter disabled and no-op providers", func(t *testing.T) {
		if got := initOTelExporter(&cfgpkg.DaemonConfig{}, nil); got != nil {
			t.Fatalf("disabled otel exporter = %#v, want nil", got)
		}
		traces, metrics := false, false
		bus := events.NewBus(t.TempDir())
		exp := initOTelExporter(&cfgpkg.DaemonConfig{Daemon: cfgpkg.DaemonSettings{OTel: &cfgpkg.OTelDaemonConfig{
			Enabled:     true,
			ServiceName: "loom-test",
			Traces:      &traces,
			Metrics:     &metrics,
		}}}, bus)
		if exp == nil {
			t.Fatal("expected no-op otel exporter")
		}
		stopOTelExporter(exp)
	})

	t.Run("load daemon logs config falls back to defaults", func(t *testing.T) {
		t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "missing-config"))
		cfg := loadDaemonLogsConfig(t.TempDir())
		if cfg.Daemon.PIDFile == "" || cfg.Daemon.LogDir == "" {
			t.Fatalf("fallback config = %+v", cfg.Daemon)
		}
	})

}

func replaceDaemonRunHooks(t *testing.T) func() {
	t.Helper()
	oldIsolate := isolateProcessGroupFn
	oldGetwd := daemonGetwdFn
	oldLoadConfig := loadDaemonConfigFn
	oldValidatePaths := validateDaemonPathsFn
	oldPrintDryRun := printDryRunInfoFn
	oldPrepareDirs := prepareDaemonDirsFn
	oldAcquireLock := acquireDaemonLockFn
	oldInitPID := initPIDFileFn
	oldInitServices := initDaemonServicesFn
	oldRunLoop := runDaemonMainLoopFn
	oldSetupSignal := setupSignalHandlerFn
	oldInitOTel := initOTelExporterFn
	oldOpenStore := cmdstoreOpenStoreFn
	oldNewDaemon := newDaemonFn
	oldDryRun := daemonDryRun
	return func() {
		isolateProcessGroupFn = oldIsolate
		daemonGetwdFn = oldGetwd
		loadDaemonConfigFn = oldLoadConfig
		validateDaemonPathsFn = oldValidatePaths
		printDryRunInfoFn = oldPrintDryRun
		prepareDaemonDirsFn = oldPrepareDirs
		acquireDaemonLockFn = oldAcquireLock
		initPIDFileFn = oldInitPID
		initDaemonServicesFn = oldInitServices
		runDaemonMainLoopFn = oldRunLoop
		setupSignalHandlerFn = oldSetupSignal
		initOTelExporterFn = oldInitOTel
		cmdstoreOpenStoreFn = oldOpenStore
		newDaemonFn = oldNewDaemon
		daemonDryRun = oldDryRun
	}
}
