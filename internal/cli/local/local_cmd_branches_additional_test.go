package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestLocalCommandClosuresAndStatusErrorJSON(t *testing.T) {
	dataDir := t.TempDir()
	if err := ensureRuntimeDirs(dataDir); err != nil {
		t.Fatalf("ensureRuntimeDirs: %v", err)
	}
	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: 0, DataDir: dataDir}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	oldDataDir, oldPort, oldStart, oldJSON := dataDirFlag, portFlag, startRuntimeFn, jsonFlag
	t.Cleanup(func() {
		dataDirFlag, portFlag, startRuntimeFn, jsonFlag = oldDataDir, oldPort, oldStart, oldJSON
	})
	dataDirFlag = dataDir
	portFlag = 19090

	if err := drainCmd.RunE(&cobra.Command{}, nil); err != nil {
		t.Fatalf("drain RunE: %v", err)
	}
	info, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read drained runtime: %v", err)
	}
	if !info.ClaimsPaused || info.Status != "draining" {
		t.Fatalf("drained info = %+v", info)
	}
	if err := resumeCmd.RunE(&cobra.Command{}, nil); err != nil {
		t.Fatalf("resume RunE: %v", err)
	}
	info, err = readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read resumed runtime: %v", err)
	}
	if info.ClaimsPaused || info.Status != "draining" {
		t.Fatalf("resume with stopped pid should clear pause but preserve status: %+v", info)
	}

	startRuntimeFn = func(gotDir string, gotPort int) (*RuntimeStartResult, error) {
		if gotDir != dataDir || gotPort != 19090 {
			t.Fatalf("start args dir=%q port=%d", gotDir, gotPort)
		}
		return &RuntimeStartResult{PID: 7777, URL: "http://127.0.0.1:19090"}, nil
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := restartCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("restart RunE: %v", err)
	}
	if !strings.Contains(out.String(), "Started Loom local service") {
		t.Fatalf("restart output = %q", out.String())
	}

	jsonFlag = true
	dataDirFlag = t.TempDir()
	out.Reset()
	cmd.SetContext(context.Background())
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus json missing runtime: %v", err)
	}
	var status RuntimeStatusSnapshot
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatalf("decode status json: %v; output=%s", err, out.String())
	}
	if status.Error == "" {
		t.Fatalf("missing runtime JSON status lacks error: %+v", status)
	}
}

func TestPrepareLocalServiceConfigExecutableError(t *testing.T) {
	oldDataDir, oldBind, oldPort, oldExecutable := dataDirFlag, bindFlag, portFlag, osExecutableFn
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag, osExecutableFn = oldDataDir, oldBind, oldPort, oldExecutable
	})
	dataDirFlag = t.TempDir()
	bindFlag = "127.0.0.1"
	portFlag = 18888
	osExecutableFn = func() (string, error) { return "", errors.New("no executable") }

	cfg, err := prepareLocalServiceConfig()
	if err == nil || cfg != nil || !strings.Contains(err.Error(), "resolve loom executable") {
		t.Fatalf("prepareLocalServiceConfig cfg=%+v err=%v, want executable error", cfg, err)
	}
}

func TestPrepareLocalServiceConfigEarlyErrorBranches(t *testing.T) {
	oldDataDir, oldBind, oldPort := dataDirFlag, bindFlag, portFlag
	oldEnsureDirs, oldExecutable, oldRedisHash := ensureRuntimeDirsFn, osExecutableFn, currentFleetDBRedisHashFn
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag = oldDataDir, oldBind, oldPort
		ensureRuntimeDirsFn, osExecutableFn, currentFleetDBRedisHashFn = oldEnsureDirs, oldExecutable, oldRedisHash
	})

	blockedDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockedDir, []byte("file"), 0600); err != nil {
		t.Fatalf("write blocked dir: %v", err)
	}
	dataDirFlag = blockedDir
	bindFlag = "127.0.0.1"
	portFlag = 18080

	if cfg, err := prepareLocalServiceConfig(); err == nil || cfg != nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("prepare ensure dirs cfg=%+v err=%v", cfg, err)
	}

	dataDirFlag = t.TempDir()
	osExecutableFn = func() (string, error) { return "/bin/loom-test", nil }
	currentFleetDBRedisHashFn = func(string) (string, error) { return "", errors.New("redis hash failed") }
	if cfg, err := prepareLocalServiceConfig(); err == nil || cfg != nil || !strings.Contains(err.Error(), "load FleetDB Redis settings") {
		t.Fatalf("prepare redis hash cfg=%+v err=%v", cfg, err)
	}
}

func TestRestartCommandStopsBeforeStartAndPropagatesStopError(t *testing.T) {
	dataDir := t.TempDir()
	if err := writeRuntime(dataDir, &runtimeInfo{PID: os.Getpid(), Status: "running", DataDir: dataDir}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	oldDataDir, oldPort := dataDirFlag, portFlag
	oldStart, oldStop := startRuntimeFn, stopRuntimeProcessFn
	t.Cleanup(func() {
		dataDirFlag, portFlag = oldDataDir, oldPort
		startRuntimeFn, stopRuntimeProcessFn = oldStart, oldStop
	})
	dataDirFlag = dataDir
	portFlag = 19991

	stopRuntimeProcessFn = func(pid int, timeout time.Duration) error {
		if pid != os.Getpid() || timeout != 15*time.Second {
			t.Fatalf("stop args pid=%d timeout=%v", pid, timeout)
		}
		return errors.New("stop before restart failed")
	}
	startRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		t.Fatal("start should not run after stop failure")
		return nil, nil
	}
	if err := restartCmd.RunE(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "stop before restart failed") {
		t.Fatalf("restart stop err = %v", err)
	}
}

func TestPrepareLocalServiceConfigSuccessAndRuntimeInfo(t *testing.T) {
	oldDataDir, oldBind, oldPort, oldExecutable, oldRedisHash := dataDirFlag, bindFlag, portFlag, osExecutableFn, currentFleetDBRedisHashFn
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag, osExecutableFn, currentFleetDBRedisHashFn = oldDataDir, oldBind, oldPort, oldExecutable, oldRedisHash
	})
	dataDirFlag = t.TempDir()
	bindFlag = ""
	portFlag = 19999
	osExecutableFn = func() (string, error) { return "/bin/loom-test", nil }
	currentFleetDBRedisHashFn = func(gotDir string) (string, error) {
		if gotDir != dataDirFlag {
			t.Fatalf("redis hash dir = %q, want %q", gotDir, dataDirFlag)
		}
		return "redis-hash", nil
	}

	cfg, err := prepareLocalServiceConfig()
	if err != nil {
		t.Fatalf("prepareLocalServiceConfig: %v", err)
	}
	if cfg.bindAddr != "127.0.0.1" || cfg.port != 19999 || cfg.exe != "/bin/loom-test" ||
		cfg.redisHash != "redis-hash" || cfg.url != "http://127.0.0.1:19999" {
		t.Fatalf("cfg = %+v", cfg)
	}
	info := newRuntimeInfo(cfg)
	if info.PID != os.Getpid() || info.DataDir != dataDirFlag || info.FleetDBRedisHash != "redis-hash" || info.Status != "starting" {
		t.Fatalf("runtime info = %+v", info)
	}
}

func TestStartRuntimeReuseAndSpawnHookBranches(t *testing.T) {
	oldExecutable := osExecutableFn
	oldRedisHash := currentFleetDBRedisHashFn
	oldEnsureDirs := ensureRuntimeDirsFn
	oldSpawn := spawnDetachedServiceFn
	oldStop := stopRuntimeProcessFn
	t.Cleanup(func() {
		osExecutableFn = oldExecutable
		currentFleetDBRedisHashFn = oldRedisHash
		ensureRuntimeDirsFn = oldEnsureDirs
		spawnDetachedServiceFn = oldSpawn
		stopRuntimeProcessFn = oldStop
	})

	dataDir := t.TempDir()
	currentFleetDBRedisHashFn = func(string) (string, error) { return "hash", nil }
	osExecutableFn = func() (string, error) { return "/bin/loom-test", nil }
	var ensured, spawned bool
	ensureRuntimeDirsFn = func(gotDir string) error {
		if gotDir != dataDir {
			t.Fatalf("ensure dir = %q, want %q", gotDir, dataDir)
		}
		ensured = true
		return ensureRuntimeDirs(gotDir)
	}
	spawnDetachedServiceFn = func(exe, gotDir string, port int) (*RuntimeStartResult, error) {
		if exe != "/bin/loom-test" || gotDir != dataDir || port != 19191 {
			t.Fatalf("spawn args exe=%q dir=%q port=%d", exe, gotDir, port)
		}
		spawned = true
		return &RuntimeStartResult{PID: 4242, URL: "http://127.0.0.1:19191"}, nil
	}

	started, err := StartRuntime(dataDir, 19191)
	if err != nil {
		t.Fatalf("StartRuntime: %v", err)
	}
	if !ensured || !spawned || started.PID != 4242 {
		t.Fatalf("started=%+v ensured=%t spawned=%t", started, ensured, spawned)
	}

	info := &runtimeInfo{Status: "running", PID: os.Getpid(), URL: "http://127.0.0.1:19191", DataDir: dataDir}
	applyExecutableIdentity(info, currentExecutableIdentity())
	info.FleetDBRedisHash = "hash"
	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	spawned = false
	reused, err := StartRuntime(dataDir, 0)
	if err != nil {
		t.Fatalf("StartRuntime reuse: %v", err)
	}
	if reused == nil || !reused.AlreadyRunning || reused.PID != os.Getpid() || spawned {
		t.Fatalf("reuse result=%+v spawned=%t", reused, spawned)
	}

	var stoppedPID int
	stopRuntimeProcessFn = func(pid int, timeout time.Duration) error {
		stoppedPID = pid
		if timeout != 15*time.Second {
			t.Fatalf("stop timeout = %v", timeout)
		}
		return nil
	}
	spawnDetachedServiceFn = func(exe, gotDir string, port int) (*RuntimeStartResult, error) {
		spawned = true
		return &RuntimeStartResult{PID: 5252}, nil
	}
	restarted, err := RestartRuntime(dataDir, 20202)
	if err != nil {
		t.Fatalf("RestartRuntime: %v", err)
	}
	if stoppedPID != os.Getpid() || !spawned || restarted.PID != 5252 {
		t.Fatalf("restart stoppedPID=%d spawned=%t result=%+v", stoppedPID, spawned, restarted)
	}

	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("rewrite runtime: %v", err)
	}
	stopRuntimeProcessFn = func(int, time.Duration) error { return errors.New("no stop") }
	if _, err := RestartRuntime(dataDir, 0); err == nil || !strings.Contains(err.Error(), "stop stale local runtime") {
		t.Fatalf("RestartRuntime stop error = %v", err)
	}
}

func TestStartRuntimeErrorBranches(t *testing.T) {
	oldExecutable := osExecutableFn
	oldRedisHash := currentFleetDBRedisHashFn
	oldEnsureDirs := ensureRuntimeDirsFn
	oldSpawn := spawnDetachedServiceFn
	t.Cleanup(func() {
		osExecutableFn = oldExecutable
		currentFleetDBRedisHashFn = oldRedisHash
		ensureRuntimeDirsFn = oldEnsureDirs
		spawnDetachedServiceFn = oldSpawn
	})

	dataDir := t.TempDir()
	currentFleetDBRedisHashFn = func(string) (string, error) { return "", errors.New("hash failed") }
	if _, err := StartRuntime(dataDir, 0); err == nil || !strings.Contains(err.Error(), "load FleetDB Redis settings") {
		t.Fatalf("StartRuntime redis settings err = %v", err)
	}

	currentFleetDBRedisHashFn = func(string) (string, error) { return "hash", nil }
	ensureRuntimeDirsFn = func(string) error { return errors.New("mkdir failed") }
	if _, err := StartRuntime(dataDir, 0); err == nil || !strings.Contains(err.Error(), "mkdir failed") {
		t.Fatalf("StartRuntime ensure dirs err = %v", err)
	}

	ensureRuntimeDirsFn = ensureRuntimeDirs
	osExecutableFn = func() (string, error) { return "", errors.New("no executable") }
	if _, err := StartRuntime(dataDir, 0); err == nil || !strings.Contains(err.Error(), "resolve loom executable") {
		t.Fatalf("StartRuntime executable err = %v", err)
	}

	osExecutableFn = func() (string, error) { return "/bin/loom-test", nil }
	spawnDetachedServiceFn = func(string, string, int) (*RuntimeStartResult, error) {
		return nil, errors.New("spawn failed")
	}
	if _, err := StartRuntime(dataDir, 0); err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("StartRuntime spawn err = %v", err)
	}
}

func TestEnsureRuntimeStartedBranches(t *testing.T) {
	oldRead := readRuntimeStatusFn
	oldStart := startRuntimeFn
	oldRestart := restartRuntimeFn
	t.Cleanup(func() {
		readRuntimeStatusFn = oldRead
		startRuntimeFn = oldStart
		restartRuntimeFn = oldRestart
	})

	dataDir := t.TempDir()
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 1}, Healthy: true}, nil
	}
	startRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		t.Fatal("start should not be called for healthy runtime")
		return nil, nil
	}
	status, err := EnsureRuntimeStarted(context.Background(), dataDir, 0)
	if err != nil || status == nil || !status.Healthy {
		t.Fatalf("healthy EnsureRuntimeStarted status=%+v err=%v", status, err)
	}

	restartCalled := false
	reads := 0
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		reads++
		if reads == 1 {
			return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 99}}, nil
		}
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 99}, Healthy: true}, nil
	}
	restartRuntimeFn = func(gotDir string, gotPort int) (*RuntimeStartResult, error) {
		if gotDir != dataDir || gotPort != 12345 {
			t.Fatalf("restart args dir=%q port=%d", gotDir, gotPort)
		}
		restartCalled = true
		return &RuntimeStartResult{PID: 100}, nil
	}
	status, err = EnsureRuntimeStarted(context.Background(), dataDir, 12345)
	if err != nil || !restartCalled || !status.Healthy {
		t.Fatalf("restart EnsureRuntimeStarted status=%+v err=%v restart=%t", status, err, restartCalled)
	}

	startCalled := false
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		if startCalled {
			return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 101}, Healthy: true}, nil
		}
		return nil, errors.New("missing runtime")
	}
	startRuntimeFn = func(gotDir string, gotPort int) (*RuntimeStartResult, error) {
		if gotDir != dataDir || gotPort != 2222 {
			t.Fatalf("start args dir=%q port=%d", gotDir, gotPort)
		}
		startCalled = true
		return &RuntimeStartResult{PID: 101}, nil
	}
	status, err = EnsureRuntimeStarted(context.Background(), dataDir, 2222)
	if err != nil || !startCalled || !status.Healthy {
		t.Fatalf("start EnsureRuntimeStarted status=%+v err=%v start=%t", status, err, startCalled)
	}
}

func TestEnsureRuntimeStartedContextBranches(t *testing.T) {
	oldRead := readRuntimeStatusFn
	oldStart := startRuntimeFn
	t.Cleanup(func() {
		readRuntimeStatusFn = oldRead
		startRuntimeFn = oldStart
	})

	dataDir := t.TempDir()
	startRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		return &RuntimeStartResult{PID: 123}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 123}}, nil
	}
	status, err := EnsureRuntimeStarted(ctx, dataDir, 0)
	if err == nil || !errors.Is(err, context.Canceled) || status == nil {
		t.Fatalf("EnsureRuntimeStarted canceled status=%+v err=%v", status, err)
	}

	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 123}}, errors.New("status still unavailable")
	}
	status, err = EnsureRuntimeStarted(ctx, dataDir, 0)
	if err == nil || !strings.Contains(err.Error(), "status still unavailable") || status == nil {
		t.Fatalf("EnsureRuntimeStarted canceled with read err status=%+v err=%v", status, err)
	}
}

func TestRunStartStopLogsAdditionalBranches(t *testing.T) {
	oldDataDir, oldPort, oldStart, oldStop := dataDirFlag, portFlag, startRuntimeFn, stopRuntimeProcessFn
	t.Cleanup(func() {
		dataDirFlag, portFlag, startRuntimeFn, stopRuntimeProcessFn = oldDataDir, oldPort, oldStart, oldStop
	})

	dataDir := t.TempDir()
	dataDirFlag = dataDir
	portFlag = 18888
	startRuntimeFn = func(string, int) (*RuntimeStartResult, error) {
		return &RuntimeStartResult{PID: 123, URL: "http://127.0.0.1:18888", AlreadyRunning: true}, nil
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStart(cmd, nil); err != nil {
		t.Fatalf("runStart already running: %v", err)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Fatalf("runStart output = %q", out.String())
	}

	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: 999999, DataDir: dataDir}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	out.Reset()
	if err := runStop(cmd, nil); err != nil {
		t.Fatalf("runStop stale process: %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Fatalf("runStop output = %q", out.String())
	}

	out.Reset()
	if err := runLogs(cmd, nil); err != nil {
		t.Fatalf("runLogs: %v", err)
	}
	if !strings.Contains(out.String(), "service:") || !strings.Contains(out.String(), "serve:") {
		t.Fatalf("runLogs output = %q", out.String())
	}
}

func TestLocalRuntimeCommandErrorBranches(t *testing.T) {
	oldDataDir, oldPort := dataDirFlag, portFlag
	oldStart, oldRestart, oldStop := startRuntimeFn, restartRuntimeFn, stopRuntimeProcessFn
	oldRead := readRuntimeStatusFn
	oldEnsureDirs := ensureRuntimeDirsFn
	t.Cleanup(func() {
		dataDirFlag, portFlag = oldDataDir, oldPort
		startRuntimeFn, restartRuntimeFn, stopRuntimeProcessFn = oldStart, oldRestart, oldStop
		readRuntimeStatusFn = oldRead
		ensureRuntimeDirsFn = oldEnsureDirs
	})

	cmd := &cobra.Command{}
	dataDir := t.TempDir()
	dataDirFlag = dataDir
	portFlag = 17777

	startRuntimeFn = func(gotDir string, gotPort int) (*RuntimeStartResult, error) {
		if gotDir != dataDir || gotPort != 17777 {
			t.Fatalf("runStart args dir=%q port=%d", gotDir, gotPort)
		}
		return nil, errors.New("start failed")
	}
	if err := runStart(cmd, nil); err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Fatalf("runStart err = %v", err)
	}

	restartRuntimeFn = func(gotDir string, gotPort int) (*RuntimeStartResult, error) {
		if gotDir != dataDir || gotPort != 17777 {
			t.Fatalf("EnsureRuntimeStarted restart args dir=%q port=%d", gotDir, gotPort)
		}
		return nil, errors.New("restart failed")
	}
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		return &RuntimeStatusSnapshot{Runtime: &RuntimeSnapshot{PID: 99}}, nil
	}
	if _, err := EnsureRuntimeStarted(context.Background(), dataDir, 17777); err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("EnsureRuntimeStarted restart err = %v", err)
	}

	if err := runStop(cmd, nil); err == nil || !strings.Contains(err.Error(), "read runtime") {
		t.Fatalf("runStop missing runtime err = %v", err)
	}
	if err := updatePauseState(true); err == nil || !strings.Contains(err.Error(), "read runtime") {
		t.Fatalf("updatePauseState missing runtime err = %v", err)
	}

	badDir := string([]byte{'b', 'a', 'd', 0, 'd', 'i', 'r'})
	dataDirFlag = badDir
	if err := runLogs(cmd, nil); err == nil {
		t.Fatal("runLogs invalid data dir err = nil")
	}
	if err := runInstallService(cmd, nil); err == nil {
		t.Fatal("runInstallService invalid data dir err = nil")
	}

	dataDirFlag = dataDir
	if err := writeRuntime(dataDir, &runtimeInfo{PID: os.Getpid(), Status: "running"}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	stopRuntimeProcessFn = func(pid int, timeout time.Duration) error {
		if pid != os.Getpid() || timeout != 15*time.Second {
			t.Fatalf("stop args pid=%d timeout=%v", pid, timeout)
		}
		return errors.New("stop failed")
	}
	if err := runStop(cmd, nil); err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("runStop stop err = %v", err)
	}
}
