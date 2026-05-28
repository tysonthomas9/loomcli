package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
)

func TestCheckRuntimeHealthRequiresAPIAndStoreReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/workspaces":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"workspaces":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	if err := checkRuntimeHealth(context.Background(), server.URL); err != nil {
		t.Fatalf("checkRuntimeHealth() error = %v", err)
	}
}

func TestCheckRuntimeHealthReportsStatusCode(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	err := checkRuntimeHealth(context.Background(), server.URL)
	if err == nil {
		t.Fatal("checkRuntimeHealth() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "/api/health returned 404") {
		t.Fatalf("checkRuntimeHealth() error = %q, want /api/health 404", err)
	}
}

func TestCheckRuntimeHealthReportsStoreFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.WriteHeader(http.StatusOK)
		case "/api/workspaces":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"failed to list workspaces from store"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	err := checkRuntimeHealth(context.Background(), server.URL)
	if err == nil {
		t.Fatal("checkRuntimeHealth() error = nil, want store readiness error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "/api/workspaces returned 500") {
		t.Fatalf("checkRuntimeHealth() error = %q, want /api/workspaces 500", msg)
	}
	if !strings.Contains(msg, "failed to list workspaces") {
		t.Fatalf("checkRuntimeHealth() error = %q, want store body", msg)
	}
}

func TestLocalEnvSetsWorkspaceRuntimeDir(t *testing.T) {
	env := localEnv("/tmp/loom-data", 12345)

	if !containsEnv(env, "LOOM_WORKSPACE_RUNTIME_DIR=/tmp/loom-data") {
		t.Fatalf("localEnv() missing LOOM_WORKSPACE_RUNTIME_DIR")
	}
}

func TestLocalEnvPrependsExecutableDirToPath(t *testing.T) {
	env := localEnv("/tmp/loom-data", 12345)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	pathPrefix := "PATH=" + filepath.Dir(exe) + string(os.PathListSeparator)
	if !containsEnvPrefix(env, pathPrefix) {
		t.Fatalf("localEnv() missing PATH prefix %q", pathPrefix)
	}
}

func TestDesktopRuntimePathIncludesMacCLILocations(t *testing.T) {
	got := desktopRuntimePath("/Applications/Loom.app/Contents/MacOS", "/usr/bin:/bin")
	for _, want := range []string{
		"/Applications/Loom.app/Contents/MacOS",
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
		"/usr/sbin",
		"/sbin",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("desktopRuntimePath() = %q, missing %q", got, want)
		}
	}
}

func TestDesktopRuntimePathDeduplicatesEntries(t *testing.T) {
	got := desktopRuntimePath("/usr/bin", "/usr/bin:/bin")
	if strings.Count(got, "/usr/bin") != 1 {
		t.Fatalf("desktopRuntimePath() = %q, want one /usr/bin", got)
	}
}

func TestLocalDaemonWorkspaceKeyUsesDesktopState(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "DESKTOP-QA",
	}); err != nil {
		t.Fatalf("SaveStateCache() error = %v", err)
	}

	workspaceKey, err := localDaemonWorkspaceKey()
	if err != nil {
		t.Fatalf("localDaemonWorkspaceKey() error = %v", err)
	}
	if workspaceKey != "DESKTOP-QA" {
		t.Fatalf("localDaemonWorkspaceKey() = %q, want DESKTOP-QA", workspaceKey)
	}
}

func TestLocalEnvForcesLocalFleetDBBackend(t *testing.T) {
	env := localEnv("/tmp/loom-data", 12345)

	for _, want := range []string{
		"LOOM_ISSUE_BACKEND=fleetdb",
		"LOOM_SERVER_URL=",
		"LOOM_FLEET_DB_URL=",
		"LOOM_FLEET_URL=",
	} {
		if !containsEnv(env, want) {
			t.Fatalf("localEnv() missing %s", want)
		}
	}
}

func TestLocalEnvOverridesConflictingParentEnv(t *testing.T) {
	t.Setenv("LOOM_FLEET_DB_URL", "http://stale-fleet-db")
	t.Setenv("LOOM_SERVER_URL", "http://stale-server")

	env := localEnv("/tmp/loom-data", 12345)

	if got := countEnvPrefix(env, "LOOM_FLEET_DB_URL="); got != 1 {
		t.Fatalf("LOOM_FLEET_DB_URL entries = %d, want 1", got)
	}
	if !containsEnv(env, "LOOM_FLEET_DB_URL=") {
		t.Fatalf("localEnv() did not clear LOOM_FLEET_DB_URL: %v", env)
	}
	if got := countEnvPrefix(env, "LOOM_SERVER_URL="); got != 1 {
		t.Fatalf("LOOM_SERVER_URL entries = %d, want 1", got)
	}
	if !containsEnv(env, "LOOM_SERVER_URL=") {
		t.Fatalf("localEnv() did not clear LOOM_SERVER_URL: %v", env)
	}
}

func TestRuntimeMatchesExecutableRequiresStoredBinaryHash(t *testing.T) {
	identity := executableIdentity{
		Path:  "/Applications/Loom.app/Contents/MacOS/loom",
		Hash:  "current-hash",
		Build: "current-build",
	}

	tests := []struct {
		name string
		info *runtimeInfo
		want bool
	}{
		{
			name: "matching hash",
			info: &runtimeInfo{BinaryHash: "current-hash"},
			want: true,
		},
		{
			name: "different hash",
			info: &runtimeInfo{BinaryHash: "old-hash"},
			want: false,
		},
		{
			name: "missing hash from older runtime",
			info: &runtimeInfo{},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeMatchesExecutable(tt.info, identity); got != tt.want {
				t.Fatalf("runtimeMatchesExecutable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRuntimeMatchesExecutableAllowsUnknownCurrentHash(t *testing.T) {
	if !runtimeMatchesExecutable(&runtimeInfo{}, executableIdentity{}) {
		t.Fatal("runtimeMatchesExecutable() should not force restart when current hash cannot be computed")
	}
}

func TestApplyExecutableIdentityPersistsBuildFingerprint(t *testing.T) {
	info := &runtimeInfo{}
	applyExecutableIdentity(info, executableIdentity{
		Path:  "/Applications/Loom.app/Contents/MacOS/loom",
		Hash:  "abc123",
		Build: "git123",
	})

	if info.Executable != "/Applications/Loom.app/Contents/MacOS/loom" {
		t.Fatalf("Executable = %q", info.Executable)
	}
	if info.BinaryHash != "abc123" {
		t.Fatalf("BinaryHash = %q", info.BinaryHash)
	}
	if info.Build != "git123" {
		t.Fatalf("Build = %q", info.Build)
	}
}

func TestRuntimeMatchesFleetDBRedisSettings(t *testing.T) {
	if !runtimeMatchesFleetDBRedisSettings(&runtimeInfo{FleetDBRedisHash: "same"}, "same") {
		t.Fatal("runtimeMatchesFleetDBRedisSettings() should match equal hashes")
	}
	if runtimeMatchesFleetDBRedisSettings(&runtimeInfo{FleetDBRedisHash: "old"}, "new") {
		t.Fatal("runtimeMatchesFleetDBRedisSettings() should reject changed settings")
	}
	if runtimeMatchesFleetDBRedisSettings(&runtimeInfo{}, "new") {
		t.Fatal("runtimeMatchesFleetDBRedisSettings() should reject missing hash when settings are enabled")
	}
	if !runtimeMatchesFleetDBRedisSettings(&runtimeInfo{}, "") {
		t.Fatal("runtimeMatchesFleetDBRedisSettings() should allow missing hash when settings are disabled")
	}
}

func TestEnsureRuntimeStartedRestartsUnhealthyRecordedRuntime(t *testing.T) {
	originalRead := readRuntimeStatusFn
	originalRestart := restartRuntimeFn
	t.Cleanup(func() {
		readRuntimeStatusFn = originalRead
		restartRuntimeFn = originalRestart
	})

	reads := 0
	readRuntimeStatusFn = func(context.Context, string) (*RuntimeStatusSnapshot, error) {
		reads++
		if reads == 1 {
			return &RuntimeStatusSnapshot{
				Runtime: &RuntimeSnapshot{
					PID: 123,
					URL: "http://127.0.0.1:9",
				},
				Healthy: false,
				Error:   "health check timed out",
			}, nil
		}
		return &RuntimeStatusSnapshot{
			Runtime: &RuntimeSnapshot{
				PID: 456,
				URL: "http://127.0.0.1:4321",
			},
			Healthy: true,
		}, nil
	}

	restarted := false
	restartRuntimeFn = func(dataDir string, port int) (*RuntimeStartResult, error) {
		restarted = true
		if dataDir != "/tmp/loom-data" {
			t.Fatalf("restart dataDir = %q, want /tmp/loom-data", dataDir)
		}
		if port != 4321 {
			t.Fatalf("restart port = %d, want 4321", port)
		}
		return &RuntimeStartResult{PID: 456}, nil
	}

	status, err := EnsureRuntimeStarted(context.Background(), "/tmp/loom-data", 4321)
	if err != nil {
		t.Fatalf("EnsureRuntimeStarted returned error: %v", err)
	}
	if !restarted {
		t.Fatal("EnsureRuntimeStarted did not restart unhealthy recorded runtime")
	}
	if status == nil || !status.Healthy || status.Runtime == nil || status.Runtime.PID != 456 {
		t.Fatalf("status = %#v, want healthy restarted runtime", status)
	}
}

func TestCleanupOrphanedEmbeddedLockStopsOrphanedFleetModeServe(t *testing.T) {
	originalProcessRunning := processRunningFn
	originalInspect := inspectProcessFn
	originalStop := stopRuntimeProcessFn
	t.Cleanup(func() {
		processRunningFn = originalProcessRunning
		inspectProcessFn = originalInspect
		stopRuntimeProcessFn = originalStop
	})

	dataDir := t.TempDir()
	if err := writeRuntime(dataDir, &runtimeInfo{Status: "failed", PID: 100, ServePID: 101}); err != nil {
		t.Fatalf("writeRuntime() error = %v", err)
	}
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := os.MkdirAll(fleetDir, 0755); err != nil {
		t.Fatalf("mkdir fleet dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fleetDir, "embedded.lock"), []byte("42\n"), 0600); err != nil {
		t.Fatalf("write embedded lock: %v", err)
	}

	processRunningFn = func(pid int) bool {
		return pid == 42
	}
	inspectProcessFn = func(pid int) (processDetails, error) {
		if pid != 42 {
			t.Fatalf("inspect pid = %d, want 42", pid)
		}
		return processDetails{
			PPID:    1,
			Command: "/Applications/Loom.app/Contents/MacOS/loom serve --bind 127.0.0.1 --port 54893 --fleet-mode",
		}, nil
	}
	stoppedPID := 0
	stopRuntimeProcessFn = func(pid int, _ time.Duration) error {
		stoppedPID = pid
		return nil
	}

	if err := cleanupOrphanedEmbeddedLock(dataDir, time.Second); err != nil {
		t.Fatalf("cleanupOrphanedEmbeddedLock() error = %v", err)
	}
	if stoppedPID != 42 {
		t.Fatalf("stopped pid = %d, want 42", stoppedPID)
	}
}

func TestCleanupOrphanedEmbeddedLockLeavesNonOrphanedServe(t *testing.T) {
	originalProcessRunning := processRunningFn
	originalInspect := inspectProcessFn
	originalStop := stopRuntimeProcessFn
	t.Cleanup(func() {
		processRunningFn = originalProcessRunning
		inspectProcessFn = originalInspect
		stopRuntimeProcessFn = originalStop
	})

	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := os.MkdirAll(fleetDir, 0755); err != nil {
		t.Fatalf("mkdir fleet dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fleetDir, "embedded.lock"), []byte("42\n"), 0600); err != nil {
		t.Fatalf("write embedded lock: %v", err)
	}

	processRunningFn = func(pid int) bool { return pid == 42 }
	inspectProcessFn = func(int) (processDetails, error) {
		return processDetails{
			PPID:    os.Getpid(),
			Command: "/Applications/Loom.app/Contents/MacOS/loom serve --fleet-mode",
		}, nil
	}
	stopRuntimeProcessFn = func(pid int, _ time.Duration) error {
		t.Fatalf("stopRuntimeProcessFn called for pid %d", pid)
		return nil
	}

	if err := cleanupOrphanedEmbeddedLock(dataDir, time.Second); err != nil {
		t.Fatalf("cleanupOrphanedEmbeddedLock() error = %v", err)
	}
}

// TestWaitForWorkspaceReadyReturnsOnSuccess verifies the happy path: when the
// workspace readyz endpoint responds 200, WaitForWorkspaceReady returns
// nil promptly. The request path is also asserted so a future refactor that
// changes the URL pattern (e.g. drops the {workspace} segment) is caught here.
func TestWaitForWorkspaceReadyReturnsOnSuccess(t *testing.T) {
	var observedPath atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedPath.Store(r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	if err := WaitForWorkspaceReady(ctx, server.URL, "LOOM"); err != nil {
		t.Fatalf("WaitForWorkspaceReady() error = %v", err)
	}

	got, _ := observedPath.Load().(string)
	if got != "/api/workspaces/LOOM/readyz" {
		t.Fatalf("server saw path = %q, want %q", got, "/api/workspaces/LOOM/readyz")
	}
}

// TestWaitForWorkspaceReadyIncludesReasonOnTimeout verifies the timeout
// failure mode preserves the JSON-decoded reason from the last 503. The
// loop used to swallow the upstream reason and surface only
// "context deadline exceeded", which made desktop ensure-runtime failures
// undebuggable.
func TestWaitForWorkspaceReadyIncludesReasonOnTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"ready":false,"mode":"fleet","workspace":"LOOM","reason":"workspace not registered: LOOM"}`))
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	t.Cleanup(cancel)

	err := WaitForWorkspaceReady(ctx, server.URL, "LOOM")
	if err == nil {
		t.Fatal("WaitForWorkspaceReady() error = nil, want timeout error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LOOM") {
		t.Errorf("error %q missing workspace key %q", msg, "LOOM")
	}
	if !strings.Contains(msg, "workspace not registered") {
		t.Errorf("error %q missing decoded reason %q", msg, "workspace not registered")
	}
}

func containsEnv(env []string, needle string) bool {
	for _, entry := range env {
		if entry == needle {
			return true
		}
	}
	return false
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func countEnvPrefix(env []string, prefix string) int {
	count := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}
	return count
}
