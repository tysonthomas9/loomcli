package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

func TestLocalDaemonRunnableWorkspaceErrorAndAgentBranches(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dataDir)
	t.Setenv("LOOM_WORKSPACE", "WS")

	oldHasRepos := localDaemonWorkspaceHasRepos
	oldLoadConfig := localDaemonLoadConfig
	t.Cleanup(func() {
		localDaemonWorkspaceHasRepos = oldHasRepos
		localDaemonLoadConfig = oldLoadConfig
	})

	repoErr := errors.New("repo list failed")
	localDaemonWorkspaceHasRepos = func(string, string) (bool, error) { return false, repoErr }
	if key, runnable, err := localDaemonRunnableWorkspace(dataDir); key != "WS" || runnable || !errors.Is(err, repoErr) {
		t.Fatalf("repo error branch key=%q runnable=%t err=%v", key, runnable, err)
	}

	localDaemonWorkspaceHasRepos = func(string, string) (bool, error) { return false, nil }
	localDaemonLoadConfig = func(string, string) (*cfgpkg.DaemonConfig, error) {
		return &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "task"}}}, nil
	}
	if key, runnable, err := localDaemonRunnableWorkspace(dataDir); key != "WS" || !runnable || err != nil {
		t.Fatalf("agent branch key=%q runnable=%t err=%v", key, runnable, err)
	}

	cfgErr := errors.New("config failed")
	localDaemonLoadConfig = func(string, string) (*cfgpkg.DaemonConfig, error) { return nil, cfgErr }
	if key, runnable, err := localDaemonRunnableWorkspace(dataDir); key != "WS" || runnable || !errors.Is(err, cfgErr) {
		t.Fatalf("config error key=%q runnable=%t err=%v", key, runnable, err)
	}
}

func TestLocalDaemonLogAndSleepHelpers(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	appendLocalDaemonLog(dataDir, "hello daemon")
	data, err := os.ReadFile(daemonLogPath(dataDir))
	if err != nil {
		t.Fatalf("read daemon log: %v", err)
	}
	if !strings.Contains(string(data), "hello daemon") {
		t.Fatalf("daemon log = %q", string(data))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepOrDone(ctx, time.Hour) {
		t.Fatal("sleepOrDone should return false after context cancel")
	}
	if !sleepOrDone(context.Background(), time.Millisecond) {
		t.Fatal("sleepOrDone should return true after timer fires")
	}
}

func TestLaunchAgentConfigPathAndWriteHelpers(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("PATH", "")
	if got := launchAgentPathEnv(); !strings.Contains(got, "/usr/bin") {
		t.Fatalf("launchAgentPathEnv default = %q", got)
	}
	t.Setenv("PATH", "/custom/bin")
	if got := launchAgentPathEnv(); got != "/custom/bin" {
		t.Fatalf("launchAgentPathEnv env = %q", got)
	}

	cfg, err := buildLaunchAgentConfig(dataDir, 18444)
	if err != nil {
		t.Fatalf("buildLaunchAgentConfig: %v", err)
	}
	if cfg.Label != localLaunchAgentLabel || cfg.DataDir != dataDir || cfg.Port != 18444 {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.StdoutPath != serviceLogPath(dataDir) || cfg.StderrPath != serviceLogPath(dataDir) {
		t.Fatalf("log paths = %+v", cfg)
	}

	path := filepath.Join(t.TempDir(), "nested", "com.loom.local.plist")
	if err := writeLaunchAgentFile(path, "<plist/>"); err != nil {
		t.Fatalf("writeLaunchAgentFile: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launch agent file: %v", err)
	}
	if string(written) != "<plist/>" {
		t.Fatalf("launch agent file = %q", string(written))
	}
}

func TestRuntimePathResolutionAndExecutableHelpers(t *testing.T) {
	flagDir := filepath.Join(t.TempDir(), "flag")
	if got, err := resolveDataDir(flagDir); err != nil || got != flagDir {
		t.Fatalf("resolveDataDir flag got=%q err=%v", got, err)
	}
	envDir := filepath.Join(t.TempDir(), "env")
	t.Setenv("LOOM_DESKTOP_DATA_DIR", envDir)
	if got, err := DefaultDataDir(); err != nil || got != envDir {
		t.Fatalf("DefaultDataDir got=%q err=%v", got, err)
	}
	t.Setenv("LOOM_DESKTOP_DATA_DIR", "")
	t.Setenv("LOOM_CONFIG_DIR", envDir)
	if got, err := resolveDataDir(""); err != nil || got != envDir {
		t.Fatalf("resolveDataDir env got=%q err=%v", got, err)
	}

	if daemonLogPath(envDir) != filepath.Join(envDir, logsDirName, "loom-daemon.log") {
		t.Fatalf("daemonLogPath mismatch")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if isExecutableFile(missing) {
		t.Fatal("missing file should not be executable")
	}
	dir := t.TempDir()
	if isExecutableFile(dir) {
		t.Fatal("directory should not be executable file")
	}
	exe := filepath.Join(t.TempDir(), "tool")
	mode := os.FileMode(0600)
	if runtime.GOOS != "windows" {
		mode = 0700
	}
	if err := os.WriteFile(exe, []byte("x"), mode); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if !isExecutableFile(exe) {
		t.Fatal("executable file not detected")
	}

	frontend := t.TempDir()
	if bundledFrontendDir() != "" {
		t.Setenv("LOOM_FRONTEND_DIR", "")
	}
	if err := os.WriteFile(filepath.Join(frontend, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	t.Setenv("LOOM_FRONTEND_DIR", frontend)
	if got := bundledFrontendDir(); got != frontend {
		t.Fatalf("bundledFrontendDir = %q", got)
	}
}

func TestServeProcessLifecycleAndHealthHelpers(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}

	shortExe := writeLocalFakeExecutable(t, "loom-short", "exit 0")
	cfg := &localServiceConfig{
		dataDir:  dataDir,
		bindAddr: "127.0.0.1",
		port:     18765,
		exe:      shortExe,
		url:      "http://127.0.0.1:18765",
	}
	info := newRuntimeInfo(cfg)
	logFile, err := os.OpenFile(serveLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open serve log: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd, err := startServeProcess(context.Background(), cfg, logFile, info)
	if err != nil {
		t.Fatalf("startServeProcess: %v", err)
	}
	if info.ServePID == 0 {
		t.Fatal("startServeProcess did not record serve PID")
	}
	if err := waitServeExit(context.Background(), cmd, dataDir, info); err != nil {
		t.Fatalf("waitServeExit: %v", err)
	}
	written, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("readRuntime: %v", err)
	}
	if written.Status != "stopped" || written.Error != "" {
		t.Fatalf("runtime after wait = %+v", written)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("unexpected health path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	longExe := writeLocalFakeExecutable(t, "loom-long", "sleep 5")
	cfg.exe = longExe
	cfg.url = server.URL
	info = newRuntimeInfo(cfg)
	cmd, err = startServeProcess(context.Background(), cfg, logFile, info)
	if err != nil {
		t.Fatalf("start long serve process: %v", err)
	}
	if err := awaitServeHealthy(context.Background(), cfg, info, cmd); err != nil {
		t.Fatalf("awaitServeHealthy: %v", err)
	}
	if info.Status != "running" || info.Error != "" {
		t.Fatalf("runtime after healthy = %+v", info)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func writeLocalFakeExecutable(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	return path
}

func TestInstallUninstallServiceCommandsSurfaceUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("install-service touches launchctl on darwin")
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runInstallService(cmd, nil); err == nil || !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("runInstallService err = %v", err)
	}
	if err := runUninstallService(cmd, nil); err == nil || !strings.Contains(err.Error(), "only supported on macOS") {
		t.Fatalf("runUninstallService err = %v", err)
	}
}

func TestLocalStatusLogsPauseAndEnsureRuntimeBranches(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	oldDataDirFlag, oldJSONFlag := dataDirFlag, jsonFlag
	t.Cleanup(func() {
		dataDirFlag, jsonFlag = oldDataDirFlag, oldJSONFlag
	})
	dataDirFlag = dataDir

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Fatalf("unexpected health path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := writeRuntime(dataDir, &runtimeInfo{
		Status:   "running",
		PID:      os.Getpid(),
		ServePID: os.Getpid(),
		DataDir:  dataDir,
		URL:      server.URL,
		Port:     18555,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus text: %v", err)
	}
	if !strings.Contains(out.String(), "healthy: true") || !strings.Contains(out.String(), "status: running") {
		t.Fatalf("status output = %q", out.String())
	}

	out.Reset()
	jsonFlag = true
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus json: %v", err)
	}
	var snapshot RuntimeStatusSnapshot
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode status json: %v", err)
	}
	if !snapshot.Healthy || snapshot.Runtime == nil || snapshot.Runtime.URL != server.URL {
		t.Fatalf("json status = %+v", snapshot)
	}
	jsonFlag = false

	if err := updatePauseState(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	paused, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read paused runtime: %v", err)
	}
	if !paused.ClaimsPaused || paused.Status != "draining" {
		t.Fatalf("paused runtime = %+v", paused)
	}
	if err := updatePauseState(false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	resumed, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read resumed runtime: %v", err)
	}
	if resumed.ClaimsPaused || resumed.Status != "running" {
		t.Fatalf("resumed runtime = %+v", resumed)
	}

	out.Reset()
	if err := runLogs(cmd, nil); err != nil {
		t.Fatalf("runLogs: %v", err)
	}
	if !strings.Contains(out.String(), serviceLogPath(dataDir)) || !strings.Contains(out.String(), serveLogPath(dataDir)) {
		t.Fatalf("logs output = %q", out.String())
	}

	oldRead, oldRestart := readRuntimeStatusFn, restartRuntimeFn
	t.Cleanup(func() {
		readRuntimeStatusFn = oldRead
		restartRuntimeFn = oldRestart
	})

	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Healthy: true, Runtime: runtimeSnapshot(&runtimeInfo{PID: os.Getpid(), URL: server.URL})}, nil
	}
	healthy, err := EnsureRuntimeStarted(context.Background(), dataDir, 0)
	if err != nil {
		t.Fatalf("EnsureRuntimeStarted healthy: %v", err)
	}
	if !healthy.Healthy {
		t.Fatalf("healthy snapshot = %+v", healthy)
	}

	calls := 0
	restarted := false
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		calls++
		if calls == 1 {
			return &RuntimeStatusSnapshot{Runtime: runtimeSnapshot(&runtimeInfo{PID: os.Getpid(), URL: server.URL})}, nil
		}
		return &RuntimeStatusSnapshot{Healthy: true, Runtime: runtimeSnapshot(&runtimeInfo{PID: os.Getpid(), URL: server.URL})}, nil
	}
	restartRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		restarted = true
		return &RuntimeStartResult{PID: os.Getpid(), URL: server.URL}, nil
	}
	afterRestart, err := EnsureRuntimeStarted(context.Background(), dataDir, 18888)
	if err != nil {
		t.Fatalf("EnsureRuntimeStarted restart: %v", err)
	}
	if !restarted || !afterRestart.Healthy || calls < 2 {
		t.Fatalf("restart branch restarted=%t calls=%d status=%+v", restarted, calls, afterRestart)
	}
}

func TestLocalStatusStopAndTimeoutBranches(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}

	if status, err := ReadRuntimeStatus(context.Background(), filepath.Join(dataDir, "missing")); err == nil || status == nil || status.Error == "" {
		t.Fatalf("ReadRuntimeStatus missing status=%+v err=%v", status, err)
	}

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()
	if err := writeRuntime(dataDir, &runtimeInfo{
		Status:  "running",
		PID:     os.Getpid(),
		DataDir: dataDir,
		URL:     unhealthy.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	status, err := ReadRuntimeStatus(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("ReadRuntimeStatus unhealthy should not fail on readable runtime: %v", err)
	}
	if status.Healthy || !strings.Contains(status.Error, "/api/health returned 503") {
		t.Fatalf("unhealthy status = %+v", status)
	}

	oldDataDirFlag := dataDirFlag
	t.Cleanup(func() { dataDirFlag = oldDataDirFlag })
	dataDirFlag = dataDir
	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: 0, DataDir: dataDir}); err != nil {
		t.Fatalf("write stopped runtime: %v", err)
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStop(cmd, nil); err != nil {
		t.Fatalf("runStop not running: %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Fatalf("runStop output = %q", out.String())
	}
	stopped, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read stopped runtime: %v", err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("stopped runtime status = %q", stopped.Status)
	}

	if err := stopRuntimeProcess(-1, time.Millisecond); err == nil {
		t.Fatal("stopRuntimeProcess(-1) err = nil")
	}

	oldRead, oldRestart := readRuntimeStatusFn, restartRuntimeFn
	t.Cleanup(func() {
		readRuntimeStatusFn = oldRead
		restartRuntimeFn = oldRestart
	})
	restartCalls := 0
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 123, URL: "http://127.0.0.1:1"}}, nil
	}
	restartRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		restartCalls++
		return &RuntimeStartResult{PID: 123}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := EnsureRuntimeStarted(ctx, dataDir, 12345); err == nil {
		t.Fatal("EnsureRuntimeStarted timeout err = nil")
	}
	if restartCalls != 1 {
		t.Fatalf("restart calls = %d, want 1", restartCalls)
	}
}

func TestLocalStartRuntimeReusesMatchingLiveRuntime(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	redisHash, err := currentFleetDBRedisHash(dataDir)
	if err != nil {
		t.Fatalf("currentFleetDBRedisHash: %v", err)
	}
	info := &runtimeInfo{
		Status:           "running",
		PID:              os.Getpid(),
		DataDir:          dataDir,
		URL:              "http://127.0.0.1:19999",
		FleetDBRedisHash: redisHash,
	}
	applyExecutableIdentity(info, currentExecutableIdentity())
	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}

	result, err := StartRuntime(dataDir, 12345)
	if err != nil {
		t.Fatalf("StartRuntime: %v", err)
	}
	if result == nil || !result.AlreadyRunning || result.PID != os.Getpid() || result.URL != info.URL {
		t.Fatalf("StartRuntime result = %+v", result)
	}

	oldDataDirFlag := dataDirFlag
	t.Cleanup(func() { dataDirFlag = oldDataDirFlag })
	dataDirFlag = dataDir
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStart(cmd, nil); err != nil {
		t.Fatalf("runStart: %v", err)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Fatalf("runStart output = %q", out.String())
	}
}

func TestInstallUninstallLocalLaunchAgentWithFakeLaunchctl(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchagent install is macOS-only")
	}
	home := t.TempDir()
	fakeBin := t.TempDir()
	fakeLaunchctl := filepath.Join(fakeBin, "launchctl")
	if err := os.WriteFile(fakeLaunchctl, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write fake launchctl: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", fakeBin)

	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	oldDataDirFlag, oldPortFlag := dataDirFlag, portFlag
	t.Cleanup(func() {
		dataDirFlag, portFlag = oldDataDirFlag, oldPortFlag
	})
	dataDirFlag = dataDir
	portFlag = 19090

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runInstallService(cmd, nil); err != nil {
		t.Fatalf("runInstallService: %v", err)
	}
	plist, err := launchAgentPlistPath()
	if err != nil {
		t.Fatalf("launchAgentPlistPath: %v", err)
	}
	if _, err := os.Stat(plist); err != nil {
		t.Fatalf("installed plist stat: %v", err)
	}
	if !strings.Contains(out.String(), "installed") {
		t.Fatalf("install output = %q", out.String())
	}

	out.Reset()
	if err := runUninstallService(cmd, nil); err != nil {
		t.Fatalf("runUninstallService: %v", err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatalf("plist after uninstall stat err = %v", err)
	}
	if !strings.Contains(out.String(), "uninstalled") {
		t.Fatalf("uninstall output = %q", out.String())
	}
}

func TestPrepareLocalServiceConfigAndSpawnDetachedService(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", os.Getenv("LOOM_CONFIG_DIR"))
	t.Setenv("LOOM_DESKTOP_DATA_DIR", os.Getenv("LOOM_DESKTOP_DATA_DIR"))
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"))
	t.Setenv("FLEET_DB_BIN", os.Getenv("FLEET_DB_BIN"))
	oldDataDirFlag, oldBindFlag, oldPortFlag := dataDirFlag, bindFlag, portFlag
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag = oldDataDirFlag, oldBindFlag, oldPortFlag
	})
	dataDirFlag = dataDir
	bindFlag = ""
	portFlag = 19876

	cfg, err := prepareLocalServiceConfig()
	if err != nil {
		t.Fatalf("prepareLocalServiceConfig: %v", err)
	}
	if cfg.dataDir != dataDir || cfg.bindAddr != "127.0.0.1" || cfg.port != 19876 || cfg.url != "http://127.0.0.1:19876" {
		t.Fatalf("config = %+v", cfg)
	}
	if os.Getenv("LOOM_CONFIG_DIR") != dataDir || os.Getenv("LOOM_DESKTOP_DATA_DIR") != dataDir || os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR") != dataDir {
		t.Fatalf("runtime env not set for dataDir %q", dataDir)
	}

	exe := writeLocalFakeExecutable(t, "loom-detached", "exit 0")
	result, err := spawnDetachedService(exe, dataDir, 19877)
	if err != nil {
		t.Fatalf("spawnDetachedService: %v", err)
	}
	if result == nil || result.PID <= 0 {
		t.Fatalf("spawnDetachedService result = %+v", result)
	}
}

func TestPrepareLocalServiceConfigPicksEphemeralPort(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("FLEET_DB_BIN", os.Getenv("FLEET_DB_BIN"))
	oldDataDirFlag, oldBindFlag, oldPortFlag := dataDirFlag, bindFlag, portFlag
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag = oldDataDirFlag, oldBindFlag, oldPortFlag
	})
	dataDirFlag = dataDir
	bindFlag = ""
	portFlag = 0

	cfg, err := prepareLocalServiceConfig()
	if err != nil {
		t.Fatalf("prepareLocalServiceConfig: %v", err)
	}
	if cfg.port == 0 {
		t.Fatalf("expected picked port, got %+v", cfg)
	}
	if cfg.url != "http://127.0.0.1:"+strconv.Itoa(cfg.port) {
		t.Fatalf("url = %q for port %d", cfg.url, cfg.port)
	}
}

func TestServeProcessFailureBranches(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	logFile, err := os.OpenFile(serveLogPath(dataDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open serve log: %v", err)
	}
	defer func() { _ = logFile.Close() }()

	missingCfg := &localServiceConfig{
		dataDir:  dataDir,
		bindAddr: "127.0.0.1",
		port:     19901,
		exe:      filepath.Join(t.TempDir(), "missing-loom"),
		url:      "http://127.0.0.1:19901",
	}
	info := newRuntimeInfo(missingCfg)
	if cmd, err := startServeProcess(context.Background(), missingCfg, logFile, info); err == nil || cmd != nil {
		t.Fatalf("startServeProcess missing exe cmd=%v err=%v", cmd, err)
	}
	written, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read missing-exe runtime: %v", err)
	}
	if written.Status != "failed" || written.Error == "" {
		t.Fatalf("runtime after missing exe = %+v", written)
	}

	exitExe := writeLocalFakeExecutable(t, "loom-exit", "exit 7")
	exitCfg := &localServiceConfig{dataDir: dataDir, bindAddr: "127.0.0.1", port: 19902, exe: exitExe, url: "http://127.0.0.1:19902"}
	info = newRuntimeInfo(exitCfg)
	cmd, err := startServeProcess(context.Background(), exitCfg, logFile, info)
	if err != nil {
		t.Fatalf("start exit process: %v", err)
	}
	if err := waitServeExit(context.Background(), cmd, dataDir, info); err == nil {
		t.Fatal("waitServeExit exit 7 err = nil")
	}
	written, err = readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read failed runtime: %v", err)
	}
	if written.Status != "failed" || written.Error == "" {
		t.Fatalf("runtime after failed serve = %+v", written)
	}

	longExe := writeLocalFakeExecutable(t, "loom-long-unhealthy", "sleep 60")
	unhealthyCfg := &localServiceConfig{dataDir: dataDir, bindAddr: "127.0.0.1", port: 19903, exe: longExe, url: "http://127.0.0.1:19903"}
	info = newRuntimeInfo(unhealthyCfg)
	cmd, err = startServeProcess(context.Background(), unhealthyCfg, logFile, info)
	if err != nil {
		t.Fatalf("start long process: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitServeHealthy(ctx, unhealthyCfg, info, cmd); err == nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("awaitServeHealthy canceled err = nil")
	}
	_ = cmd.Wait()
	written, err = readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read unhealthy runtime: %v", err)
	}
	if written.Status != "failed" || written.Error == "" {
		t.Fatalf("runtime after unhealthy serve = %+v", written)
	}
}

func TestRunLocalDaemonOnceSuccessAndCancellation(t *testing.T) {
	t.Run("success passes daemon env", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := ensureRuntimeDirs(dataDir); err != nil {
			t.Fatalf("ensureRuntimeDirs: %v", err)
		}
		outPath := filepath.Join(dataDir, "daemon-env.txt")
		exe := writeLocalFakeExecutable(t, "loom-daemon-success",
			`printf '%s|%s|%s|%s\n' "$1" "$LOOM_WORKSPACE" "$LOOM_WEBUI_URL" "$(pwd)" > "`+outPath+`"`)

		if err := runLocalDaemonOnce(context.Background(), dataDir, exe, 19991, "WS-LOCAL"); err != nil {
			t.Fatalf("runLocalDaemonOnce: %v", err)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read daemon env: %v", err)
		}
		got := strings.TrimSpace(string(data))
		wantDir, err := filepath.EvalSymlinks(dataDir)
		if err != nil {
			t.Fatalf("eval symlinks: %v", err)
		}
		want := "daemon|WS-LOCAL|http://127.0.0.1:19991|" + wantDir
		if got != want {
			t.Fatalf("daemon env = %q, want %q", got, want)
		}
	})

	t.Run("context cancellation terminates child", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := ensureRuntimeDirs(dataDir); err != nil {
			t.Fatalf("ensureRuntimeDirs: %v", err)
		}
		exe := writeLocalFakeExecutable(t, "loom-daemon-sleep", "sleep 60")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := runLocalDaemonOnce(ctx, dataDir, exe, 19992, "WS-LOCAL")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runLocalDaemonOnce err = %v, want context.Canceled", err)
		}
	})
}
