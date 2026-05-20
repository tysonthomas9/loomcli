package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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

func TestWaitForWorkspaceReadyReturnsOnSuccess(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		if gotPath != "/api/workspaces/LOOM%2FQA/runtime-ready" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ready":true}`))
	}))
	t.Cleanup(server.Close)

	if err := WaitForWorkspaceReady(context.Background(), server.URL, "LOOM/QA"); err != nil {
		t.Fatalf("WaitForWorkspaceReady() error = %v", err)
	}
}

func TestWaitForWorkspaceReadyIncludesReasonOnTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(runtimeReadyResponse{
			Ready:  false,
			Reason: "workspace not registered",
		})
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err := WaitForWorkspaceReady(ctx, server.URL, "LOOM")
	if err == nil {
		t.Fatal("WaitForWorkspaceReady() error = nil, want timeout")
	}
	for _, want := range []string{`workspace "LOOM" runtime not ready`, "workspace not registered"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("WaitForWorkspaceReady() error = %q, want %q", err.Error(), want)
		}
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
