package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckRuntimeHealthUsesAPIHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
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

func TestLocalEnvSetsWorkspaceRuntimeDir(t *testing.T) {
	env := localEnv("/tmp/loom-data", 12345)

	if !containsEnv(env, "LOOM_WORKSPACE_RUNTIME_DIR=/tmp/loom-data") {
		t.Fatalf("localEnv() missing LOOM_WORKSPACE_RUNTIME_DIR")
	}
}

func TestLocalEnvMarksDesktopRuntimeMode(t *testing.T) {
	env := localEnv("/tmp/loom-data", 12345)

	if !containsEnv(env, "LOOM_LOCAL_RUNTIME=desktop") {
		t.Fatalf("localEnv() missing desktop runtime mode")
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

func TestStopRuntimeProcessesStopsServiceAndServePIDs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX sleep process")
	}

	service := startSleepProcess(t)
	serve := startSleepProcess(t)
	info := &runtimeInfo{
		PID:      service.Process.Pid,
		ServePID: serve.Process.Pid,
	}

	if err := stopRuntimeProcesses(info, 3*time.Second); err != nil {
		t.Fatalf("stopRuntimeProcesses() error = %v", err)
	}
	if processRunning(service.Process.Pid) {
		t.Fatalf("service pid %d still running", service.Process.Pid)
	}
	if processRunning(serve.Process.Pid) {
		t.Fatalf("serve pid %d still running", serve.Process.Pid)
	}
}

func TestReuseRunningRuntimeIgnoresReusedPIDFromDifferentExecutable(t *testing.T) {
	if runtime.GOOS == "windows" || !processExecutableInspectionSupported {
		t.Skip("requires POSIX process executable inspection")
	}

	unrelated := startSleepProcess(t)
	dataDir := t.TempDir()
	info := &runtimeInfo{
		PID:        unrelated.Process.Pid,
		Executable: "/Applications/Loom.app/Contents/MacOS/loom",
		Status:     "running",
	}
	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("writeRuntime() error = %v", err)
	}

	original := processExecutablePathFn
	processExecutablePathFn = func(pid int) (string, error) {
		if pid != unrelated.Process.Pid {
			t.Fatalf("processExecutablePathFn pid = %d, want %d", pid, unrelated.Process.Pid)
		}
		return "/bin/sleep", nil
	}
	t.Cleanup(func() { processExecutablePathFn = original })

	result, err := reuseRunningRuntime(dataDir, true)
	if err != nil {
		t.Fatalf("reuseRunningRuntime() error = %v", err)
	}
	if result != nil {
		t.Fatalf("reuseRunningRuntime() result = %#v, want nil so caller starts a fresh runtime", result)
	}
	if !processRunning(unrelated.Process.Pid) {
		t.Fatalf("unrelated reused pid %d was stopped", unrelated.Process.Pid)
	}
}

func TestStopRuntimeProcessesSkipsPIDWhenExecutableCannotBeVerified(t *testing.T) {
	if runtime.GOOS == "windows" || !processExecutableInspectionSupported {
		t.Skip("requires POSIX process executable inspection")
	}

	unrelated := startSleepProcess(t)
	info := &runtimeInfo{
		PID:        unrelated.Process.Pid,
		Executable: "/Applications/Loom.app/Contents/MacOS/loom",
	}
	original := processExecutablePathFn
	processExecutablePathFn = func(int) (string, error) {
		return "", os.ErrPermission
	}
	t.Cleanup(func() { processExecutablePathFn = original })

	if err := stopRuntimeProcesses(info, 50*time.Millisecond); err != nil {
		t.Fatalf("stopRuntimeProcesses() error = %v", err)
	}
	if !processRunning(unrelated.Process.Pid) {
		t.Fatalf("unverified pid %d was stopped", unrelated.Process.Pid)
	}
}

func startSleepProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30") //nolint:norawexec // intentional child process for cleanup assertions.
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep process: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})
	return cmd
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
