package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
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

func TestEmbeddedFleetDBSecurityProfileAndChildEnvSanitization(t *testing.T) {
	args := strings.Join(embeddedFleetDBArgs(), " ")
	for _, want := range []string{
		"--backend=redis",
		"--auth-enabled=true",
		"--auth-dev-mode=false",
		"--authz-enabled=true",
		"--auth-bootstrap-admin-actor=" + embeddedFleetDBServiceActor,
		"--rpc-enabled=false",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("embedded args %q missing %q", args, want)
		}
	}
	if strings.Contains(args, "bootstrap-admin-key") {
		t.Fatalf("embedded args expose bootstrap credential: %q", args)
	}

	env := []string{
		"FLEET_CONFIG=/tmp/insecure.json",
		"FLEET_AUTH_BOOTSTRAP_ADMIN_KEY=old",
		"FLEET_AUTH_BOOTSTRAP_ADMIN_KEY=older",
		"FLEET_WORKFLOW_CATALOG_LIFECYCLE_ENABLED=false",
		embeddedFleetDBArtifactBackendEnv + "=http",
		embeddedFleetDBArtifactDirEnv + "=/tmp/wrong",
		"UNCHANGED=value",
	}
	env = withoutEnvKey(env, "FLEET_CONFIG")
	env = withEnvValue(env, "FLEET_AUTH_BOOTSTRAP_ADMIN_KEY", "new-secret")
	env = withEnvValue(env, "FLEET_WORKFLOW_CATALOG_LIFECYCLE_ENABLED", "true")
	env = withEnvValue(env, embeddedFleetDBArtifactBackendEnv, "local")
	env = withEnvValue(env, embeddedFleetDBArtifactDirEnv, "/private/runtime/artifacts")
	if envKeyCount(env, "FLEET_CONFIG") != 0 {
		t.Fatalf("FLEET_CONFIG survived child env sanitization: %v", env)
	}
	if envKeyCount(env, "FLEET_AUTH_BOOTSTRAP_ADMIN_KEY") != 1 || !envHas(env, "FLEET_AUTH_BOOTSTRAP_ADMIN_KEY=new-secret") {
		t.Fatalf("bootstrap key was not replaced exactly once: %v", env)
	}
	if envKeyCount(env, "FLEET_WORKFLOW_CATALOG_LIFECYCLE_ENABLED") != 1 || !envHas(env, "FLEET_WORKFLOW_CATALOG_LIFECYCLE_ENABLED=true") {
		t.Fatalf("lifecycle capability was not forced on: %v", env)
	}
	if envKeyCount(env, embeddedFleetDBArtifactBackendEnv) != 1 || !envHas(env, embeddedFleetDBArtifactBackendEnv+"=local") {
		t.Fatalf("embedded artifact backend was not forced local: %v", env)
	}
	if envKeyCount(env, embeddedFleetDBArtifactDirEnv) != 1 || !envHas(env, embeddedFleetDBArtifactDirEnv+"=/private/runtime/artifacts") {
		t.Fatalf("embedded artifact directory was not replaced exactly once: %v", env)
	}
	if !envHas(env, "UNCHANGED=value") {
		t.Fatalf("unrelated child env was not preserved: %v", env)
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

func envKeyCount(env []string, key string) int {
	prefix := key + "="
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	return count
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
	t.Setenv(EnvFleetDBAPIKey, "ambient-key-must-not-win")
	dataDir := t.TempDir()
	serviceCredential, err := authority.LoadOrCreateLocalFleetDBServiceCredential(embeddedFleetDBAuthDir(dataDir))
	if err != nil {
		t.Fatalf("create service credential: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case fleetdb.CapabilitiesAPIPath:
			if got := r.Header.Get("X-API-Key"); got != serviceCredential {
				t.Errorf("capability API key = %q, want persisted local service credential", got)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api_revision":"v1","capabilities":["workflow_catalog.version_lifecycle.v1"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	h, err := OpenStoreWithOptions(context.Background(), dataDir, nil, OpenStoreOptions{
		RequiredFleetDBCapabilities: []string{fleetdb.WorkflowCatalogVersionLifecycleCapability},
	})
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
	if h.FleetDBClientAPIKey() != serviceCredential {
		t.Fatal("StoreHandle did not retain the persisted local service credential for in-process client composition")
	}
	if got := os.Getenv(EnvFleetDBAPIKey); got != "ambient-key-must-not-win" {
		t.Fatalf("%s was mutated while opening local Store", EnvFleetDBAPIKey)
	}
}

func TestOpenStoreLocalReuseFailsClosedWhenServiceCredentialIsMissing(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: srv.URL}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	handle, err := OpenStore(context.Background(), dataDir, nil)
	if handle != nil {
		_ = handle.Close()
		t.Fatal("OpenStore returned a handle without a persisted service credential")
	}
	if err == nil || !strings.Contains(err.Error(), "reused service credential") {
		t.Fatalf("OpenStore error = %v, want reused service credential failure", err)
	}
	if _, statErr := os.Lstat(embeddedFleetDBAuthDir(dataDir)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("reuse path created credential state: %v", statErr)
	}
}
