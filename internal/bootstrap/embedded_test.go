package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestDiagnoseFleetDBBinaryEnvMissingReportsRemediation(t *testing.T) {
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))

	diag := DiagnoseFleetDBBinary()
	if diag.Err == nil {
		t.Fatal("expected missing binary error")
	}
	if !strings.Contains(diag.Err.Error(), EnvFleetDBBin) {
		t.Fatalf("error %q does not mention %s", diag.Err, EnvFleetDBBin)
	}
	if diag.Remediation == "" {
		t.Fatal("expected remediation")
	}
}

func TestDiagnoseFleetDBBinaryEnvNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit check is Unix-specific")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-db")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fleet-db\n"), 0644); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv(EnvFleetDBBin, path)

	diag := DiagnoseFleetDBBinary()
	if diag.Err == nil || !strings.Contains(diag.Err.Error(), "not executable") {
		t.Fatalf("diag err = %v, want not executable", diag.Err)
	}
}

func TestDiagnoseFleetDBBinaryEnvRunnable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-db")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'fleet-db test server help'\n"), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv(EnvFleetDBBin, path)

	diag := DiagnoseFleetDBBinary()
	if diag.Err != nil {
		t.Fatalf("DiagnoseFleetDBBinary returned error: %v", diag.Err)
	}
	if !diag.Runnable {
		t.Fatal("expected Runnable=true")
	}
	if diag.Path != path {
		t.Fatalf("Path = %q, want %q", diag.Path, path)
	}
}

func TestDiagnoseFleetDBBinaryProbeFailureBranches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-db")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho broken >&2\nexit 2\n"), 0755); err != nil {
		t.Fatalf("write failing binary: %v", err)
	}
	t.Setenv(EnvFleetDBBin, path)

	diag := DiagnoseFleetDBBinary()
	if diag.Err == nil || !strings.Contains(diag.Err.Error(), "not runnable") {
		t.Fatalf("failing binary diag err = %v, want not runnable", diag.Err)
	}
	if diag.Runnable {
		t.Fatal("failing binary should not be runnable")
	}
	if _, err := DiscoverFleetDBBinary(); err == nil || !strings.Contains(err.Error(), "not runnable") {
		t.Fatalf("DiscoverFleetDBBinary failing binary err = %v", err)
	}

	if _, err := StartEmbedded(context.Background(), t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "not runnable") {
		t.Fatalf("StartEmbedded failing binary err = %v", err)
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\necho totally unrelated help\n"), 0755); err != nil {
		t.Fatalf("write non-fleet binary: %v", err)
	}
	diag = DiagnoseFleetDBBinary()
	if diag.Err == nil || !strings.Contains(diag.Err.Error(), "did not look like fleet-db") {
		t.Fatalf("non-fleet diag err = %v, want probe output error", diag.Err)
	}
}

func TestDiagnoseFleetDBBinaryPathAndBundledFallbacks(t *testing.T) {
	pathDir := t.TempDir()
	pathBin := filepath.Join(pathDir, "fleet-db")
	if err := os.WriteFile(pathBin, []byte("#!/bin/sh\necho 'fleet-db path help'\n"), 0755); err != nil {
		t.Fatalf("write PATH fake binary: %v", err)
	}
	t.Setenv(EnvFleetDBBin, "")
	t.Setenv("PATH", pathDir)

	diag := DiagnoseFleetDBBinary()
	if diag.Err != nil || !diag.Runnable || diag.Path != pathBin {
		t.Fatalf("PATH diagnostic = %+v err=%v", diag, diag.Err)
	}
	if len(diag.Checked) == 0 || !strings.HasPrefix(diag.Checked[0], "PATH:") {
		t.Fatalf("PATH diagnostic checked = %v", diag.Checked)
	}

	loomDir := t.TempDir()
	t.Setenv("PATH", "")
	t.Setenv("LOOM_CONFIG_DIR", loomDir)
	bundled := filepath.Join(loomDir, "bin", "fleet-db")
	if err := os.MkdirAll(filepath.Dir(bundled), 0755); err != nil {
		t.Fatalf("mkdir bundled bin: %v", err)
	}
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\necho 'fleet-db bundled help'\n"), 0755); err != nil {
		t.Fatalf("write bundled fake binary: %v", err)
	}

	diag = DiagnoseFleetDBBinary()
	if diag.Err != nil || !diag.Runnable || diag.Path != bundled {
		t.Fatalf("bundled diagnostic = %+v err=%v", diag, diag.Err)
	}
	if !containsChecked(diag.Checked, bundled) {
		t.Fatalf("bundled diagnostic checked = %v, want %q", diag.Checked, bundled)
	}
}

func containsChecked(checked []string, want string) bool {
	for _, got := range checked {
		if got == want {
			return true
		}
	}
	return false
}

func TestIsSignalledExit(t *testing.T) {
	if isSignalledExit(errors.New("plain error")) {
		t.Fatal("plain error reported as signaled exit")
	}
	cmd := exec.Command("sh", "-c", "kill -TERM $$") //nolint:gosec //nolint:norawexec // fixed shell snippet for signal-exit behavior
	err := cmd.Run()
	if err == nil {
		t.Fatal("self-terminating command returned nil error")
	}
	if !isSignalledExit(err) {
		t.Fatalf("isSignalledExit(%T %[1]v) = false, want true", err)
	}
}

func TestAppendEmbeddedFleetDBEnvDefaultsAddsWhenMissing(t *testing.T) {
	env := appendEmbeddedFleetDBEnvDefaults([]string{"EXISTING=value"})

	if !envHas(env, EnvFleetRedisPoolSize+"="+defaultEmbeddedFleetRedisPoolSize) {
		t.Fatalf("missing default %s in env %v", EnvFleetRedisPoolSize, env)
	}
	if !envHas(env, EnvFleetRedisMinIdleConns+"="+defaultEmbeddedFleetRedisMinIdleConns) {
		t.Fatalf("missing default %s in env %v", EnvFleetRedisMinIdleConns, env)
	}
}

func TestAppendEmbeddedFleetDBEnvDefaultsPreservesConfiguredValues(t *testing.T) {
	env := appendEmbeddedFleetDBEnvDefaults([]string{
		EnvFleetRedisPoolSize + "=7",
		EnvFleetRedisMinIdleConns + "=2",
	})

	if !envHas(env, EnvFleetRedisPoolSize+"=7") {
		t.Fatalf("expected configured pool size to be preserved in env %v", env)
	}
	if !envHas(env, EnvFleetRedisMinIdleConns+"=2") {
		t.Fatalf("expected configured min idle conns to be preserved in env %v", env)
	}
}

func TestAppendEmbeddedFleetDBEnvDefaultsReplacesEmptyValues(t *testing.T) {
	env := appendEmbeddedFleetDBEnvDefaults([]string{
		EnvFleetRedisPoolSize + "=",
		EnvFleetRedisMinIdleConns + "=   ",
	})

	if !envHas(env, EnvFleetRedisPoolSize+"="+defaultEmbeddedFleetRedisPoolSize) {
		t.Fatalf("expected empty pool size to receive default in env %v", env)
	}
	if !envHas(env, EnvFleetRedisMinIdleConns+"="+defaultEmbeddedFleetRedisMinIdleConns) {
		t.Fatalf("expected empty min idle conns to receive default in env %v", env)
	}
}

func envHas(env []string, want string) bool {
	for _, got := range env {
		if got == want {
			return true
		}
	}
	return false
}

func TestEmbeddedRuntimeLockFailsFastWhenHeld(t *testing.T) {
	fleetDir := t.TempDir()
	first, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first.Release()

	second, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err == nil {
		_ = second.Release()
		t.Fatal("second lock acquired while first lock was held")
	}
	if !errors.Is(err, ErrEmbeddedAlreadyRunning) {
		t.Fatalf("second lock err = %v, want ErrEmbeddedAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock err = %v, want already running", err)
	}
}

func TestReuseEmbeddedRuntimeHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fleetDir := t.TempDir()
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	url, ok, err := reuseEmbeddedRuntime(context.Background(), fleetDir, nil, time.Second)
	if err != nil {
		t.Fatalf("reuse runtime: %v", err)
	}
	if !ok {
		t.Fatal("expected reusable runtime")
	}
	if url != srv.URL {
		t.Fatalf("url = %q, want %q", url, srv.URL)
	}
}

func TestReuseEmbeddedRuntimeRemovesStalePID(t *testing.T) {
	fleetDir := t.TempDir()
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: 999999999,
		URL: "http://127.0.0.1:1",
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	_, ok, err := reuseEmbeddedRuntime(context.Background(), fleetDir, nil, time.Second)
	if ok {
		t.Fatal("stale runtime should not be reusable")
	}
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v, want not running", err)
	}
	if _, statErr := os.Stat(embeddedRuntimePath(fleetDir)); !os.IsNotExist(statErr) {
		t.Fatalf("runtime file still exists after stale pid cleanup: %v", statErr)
	}
}

func TestReuseEmbeddedRuntimeRejectsUnhealthyProcess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	fleetDir := t.TempDir()
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	_, ok, err := reuseEmbeddedRuntime(context.Background(), fleetDir, nil, 100*time.Millisecond)
	if ok {
		t.Fatal("unhealthy runtime should not be reusable")
	}
	if err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("err = %v, want not healthy", err)
	}
}

func TestOpenStoreLocalReusesHealthyEmbeddedRuntime(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	h, err := OpenStore(context.Background(), dataDir, nil)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer h.Close()
	if h.URL() != srv.URL {
		t.Fatalf("URL = %q, want %q", h.URL(), srv.URL)
	}
	if h.embedded != nil {
		t.Fatal("OpenStore started a new embedded process instead of reusing runtime")
	}
}

func TestOpenStoreCloudUsesConfiguredURL(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "http://127.0.0.1:65530")
	t.Setenv(EnvFleetDBAPIKey, "test-key")
	t.Setenv(EnvFleetDBActor, "cloud-actor")

	h, err := OpenStore(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("OpenStore cloud: %v", err)
	}
	defer h.Close()
	if h.Mode() != ModeCloud {
		t.Fatalf("Mode = %s, want cloud", h.Mode())
	}
	if h.URL() != "http://127.0.0.1:65530" {
		t.Fatalf("URL = %q, want configured cloud URL", h.URL())
	}
	if h.embedded != nil {
		t.Fatal("cloud OpenStore should not attach an embedded runtime")
	}
	if got := resolveActor(); got != "cloud-actor" {
		t.Fatalf("resolveActor = %q, want cloud-actor", got)
	}
}

func TestEmbeddedRuntimeFileErrorBranches(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0600); err != nil {
		t.Fatalf("write not-dir: %v", err)
	}
	if _, err := acquireEmbeddedRuntimeLock(notDir); err == nil || !strings.Contains(err.Error(), "open runtime lock") {
		t.Fatalf("acquire lock under file err = %v", err)
	}
	if err := writeEmbeddedRuntime(notDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: "http://127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "mkdir runtime dir") {
		t.Fatalf("write runtime mkdir err = %v", err)
	}

	fleetDir := filepath.Join(dir, "fleet-db")
	if err := os.MkdirAll(fleetDir, 0755); err != nil {
		t.Fatalf("mkdir fleet dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(fleetDir, "runtime.json.tmp"), 0755); err != nil {
		t.Fatalf("mkdir tmp path: %v", err)
	}
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: "http://127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "write embedded runtime tmp") {
		t.Fatalf("write runtime tmp err = %v", err)
	}
	if err := os.Remove(filepath.Join(fleetDir, "runtime.json.tmp")); err != nil {
		t.Fatalf("remove tmp dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(fleetDir, "runtime.json"), 0755); err != nil {
		t.Fatalf("mkdir runtime path: %v", err)
	}
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: "http://127.0.0.1"}); err == nil || !strings.Contains(err.Error(), "rename embedded runtime") {
		t.Fatalf("write runtime rename err = %v", err)
	}

	if err := os.Remove(filepath.Join(fleetDir, "runtime.json")); err != nil {
		t.Fatalf("remove runtime dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fleetDir, "runtime.json"), []byte(`{bad`), 0600); err != nil {
		t.Fatalf("write bad runtime: %v", err)
	}
	if _, err := readEmbeddedRuntime(fleetDir); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("read bad runtime err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(fleetDir, "runtime.json"), []byte(`{"pid":1}`), 0600); err != nil {
		t.Fatalf("write missing url runtime: %v", err)
	}
	if _, err := readEmbeddedRuntime(fleetDir); err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Fatalf("read missing url err = %v", err)
	}
}

func TestEmbeddedRuntimeRedisConfigBranches(t *testing.T) {
	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := localsettings.Save(dataDir, localsettings.Settings{FleetDBRedis: localsettings.RedisConfig{
		Enabled:  true,
		Addr:     "127.0.0.1:6379",
		Password: "secret",
		DB:       2,
		TLS:      true,
	}}); err != nil {
		t.Fatalf("save local settings: %v", err)
	}
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID:             os.Getpid(),
		URL:             "http://127.0.0.1:1",
		RedisExternal:   true,
		RedisAddr:       "127.0.0.1:6379",
		RedisDB:         2,
		RedisTLS:        true,
		RedisConfigHash: localsettings.RuntimeHash(localsettings.RedisConfig{Enabled: true, Addr: "127.0.0.1:6379", Password: "different", DB: 2, TLS: true}),
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	_, ok, err := reuseEmbeddedRuntime(context.Background(), fleetDir, nil, 100*time.Millisecond)
	if ok || err == nil || !strings.Contains(err.Error(), "redis settings changed") {
		t.Fatalf("reuse mismatch ok=%t err=%v", ok, err)
	}

	cfg, err := desiredEmbeddedRedisConfig(dataDir)
	if err != nil {
		t.Fatalf("desired redis config: %v", err)
	}
	if embeddedSnapshotPath(cfg, "snapshot.json") != "" {
		t.Fatal("external redis should suppress embedded snapshot path")
	}
	info := &embeddedRuntimeInfo{
		RedisExternal:   true,
		RedisAddr:       "127.0.0.1:6379",
		RedisDB:         2,
		RedisTLS:        true,
		RedisConfigHash: localsettings.RuntimeHash(cfg),
	}
	if !embeddedRuntimeRedisMatches(info, cfg) {
		t.Fatalf("redis settings should match: info=%+v cfg=%+v", info, cfg)
	}

	badDataDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(badDataDir, []byte("x"), 0600); err != nil {
		t.Fatalf("write bad data dir file: %v", err)
	}
	if _, err := desiredEmbeddedRedisConfig(badDataDir); err == nil {
		t.Fatal("desired config load error = nil")
	}
}

func TestEmbeddedRuntimeWaitBranches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fleetDir := t.TempDir()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: srv.URL})
	}()
	url, err := waitForEmbeddedRuntime(context.Background(), fleetDir, time.Second, nil)
	if err != nil {
		t.Fatalf("waitForEmbeddedRuntime: %v", err)
	}
	if url != srv.URL {
		t.Fatalf("url = %q, want %q", url, srv.URL)
	}
}
