package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

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

func TestReadRuntimeStatusUsesRuntimeSnapshotAndHealth(t *testing.T) {
	dataDir := t.TempDir()
	info := &runtimeInfo{
		Status:           "running",
		PID:              os.Getpid(),
		ServePID:         os.Getpid(),
		URL:              "http://runtime.test",
		Port:             18444,
		ClaimsPaused:     true,
		StartedAt:        time.Now().Add(-time.Hour),
		Executable:       "/tmp/loom",
		BinaryHash:       "hash",
		Build:            "build",
		FleetDBRedisHash: "redis",
	}
	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
	withDefaultHTTPClient(t, fakeRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "http://runtime.test/api/health" {
			t.Fatalf("health URL = %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
		}, nil
	}))

	status, err := ReadRuntimeStatus(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("ReadRuntimeStatus() error = %v", err)
	}
	if !status.Healthy || status.Runtime == nil || status.Runtime.URL != "http://runtime.test" || !status.Runtime.ClaimsPaused {
		t.Fatalf("status = %#v", status)
	}
}

func TestReadRuntimeStatusReportsReadAndHealthErrors(t *testing.T) {
	if status, err := ReadRuntimeStatus(context.Background(), t.TempDir()); err == nil || status == nil || status.Error == "" {
		t.Fatalf("missing runtime status=%#v err=%v, want error snapshot", status, err)
	}

	dataDir := t.TempDir()
	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: os.Getpid(), URL: "http://runtime.test"}); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
	withDefaultHTTPClient(t, fakeRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	}))
	status, err := ReadRuntimeStatus(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("ReadRuntimeStatus() read error = %v", err)
	}
	if status.Healthy || !strings.Contains(status.Error, "offline") {
		t.Fatalf("status = %#v, want unhealthy offline", status)
	}
}

func TestPrepareLocalServiceConfigResolvesEnvironment(t *testing.T) {
	oldDataDir, oldBind, oldPort := dataDirFlag, bindFlag, portFlag
	dataDir := t.TempDir()
	dataDirFlag, bindFlag, portFlag = dataDir, "", 18444
	t.Cleanup(func() {
		dataDirFlag, bindFlag, portFlag = oldDataDir, oldBind, oldPort
	})

	cfg, err := prepareLocalServiceConfig()
	if err != nil {
		t.Fatalf("prepareLocalServiceConfig() error = %v", err)
	}
	if cfg.dataDir != dataDir || cfg.bindAddr != "127.0.0.1" || cfg.port != 18444 || cfg.url != "http://127.0.0.1:18444" {
		t.Fatalf("config = %#v", cfg)
	}
	for _, env := range []string{"LOOM_CONFIG_DIR", "LOOM_DESKTOP_DATA_DIR", "LOOM_WORKSPACE_RUNTIME_DIR"} {
		if got := os.Getenv(env); got != dataDir {
			t.Fatalf("%s = %q, want %q", env, got, dataDir)
		}
	}
	info := newRuntimeInfo(cfg)
	if info.Status != "starting" || info.PID != os.Getpid() || info.URL != cfg.url || info.FleetDBRedisHash != cfg.redisHash {
		t.Fatalf("newRuntimeInfo = %#v", info)
	}
}

func TestLocalCommandsStatusLogsStopAndPauseState(t *testing.T) {
	oldDataDir, oldJSON := dataDirFlag, jsonFlag
	dataDir := t.TempDir()
	dataDirFlag, jsonFlag = dataDir, false
	t.Cleanup(func() {
		dataDirFlag, jsonFlag = oldDataDir, oldJSON
	})
	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: -1, URL: "http://runtime.test", Port: 18444}); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
	withDefaultHTTPClient(t, fakeRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	}))

	var out bytes.Buffer
	cmd := commandWithOutput(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(out.String(), "status: running") || !strings.Contains(out.String(), "healthy: false") {
		t.Fatalf("runStatus output = %q", out.String())
	}

	jsonFlag = true
	out.Reset()
	if err := runStatus(commandWithOutput(&out), nil); err != nil {
		t.Fatalf("runStatus(json) error = %v", err)
	}
	if !strings.Contains(out.String(), `"healthy": false`) {
		t.Fatalf("runStatus json output = %q", out.String())
	}

	jsonFlag = false
	out.Reset()
	if err := runLogs(commandWithOutput(&out), nil); err != nil {
		t.Fatalf("runLogs() error = %v", err)
	}
	if !strings.Contains(out.String(), "loom-local-service.log") || !strings.Contains(out.String(), "loom-serve.log") {
		t.Fatalf("runLogs output = %q", out.String())
	}

	out.Reset()
	if err := runStop(commandWithOutput(&out), nil); err != nil {
		t.Fatalf("runStop() error = %v", err)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Fatalf("runStop output = %q", out.String())
	}

	if err := writeRuntime(dataDir, &runtimeInfo{Status: "running", PID: os.Getpid()}); err != nil {
		t.Fatalf("writeRuntime current pid: %v", err)
	}
	if err := updatePauseState(true); err != nil {
		t.Fatalf("updatePauseState(true): %v", err)
	}
	paused, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read paused runtime: %v", err)
	}
	if !paused.ClaimsPaused || paused.Status != "draining" {
		t.Fatalf("paused runtime = %#v", paused)
	}
	if err := updatePauseState(false); err != nil {
		t.Fatalf("updatePauseState(false): %v", err)
	}
	resumed, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read resumed runtime: %v", err)
	}
	if resumed.ClaimsPaused || resumed.Status != "running" {
		t.Fatalf("resumed runtime = %#v", resumed)
	}
}

func TestWaitServeExitAndJSONHelpers(t *testing.T) {
	cmd := exec.Command("true") //nolint:norawexec // test needs a completed process for waitServeExit.
	if err := cmd.Start(); err != nil {
		t.Fatalf("start true: %v", err)
	}
	dataDir := t.TempDir()
	info := &runtimeInfo{Status: "running"}
	if err := waitServeExit(context.Background(), cmd, dataDir, info); err != nil {
		t.Fatalf("waitServeExit() error = %v", err)
	}
	written, err := readRuntime(dataDir)
	if err != nil {
		t.Fatalf("read runtime: %v", err)
	}
	if written.Status != "stopped" || written.Error != "" {
		t.Fatalf("written runtime = %#v", written)
	}
	var out bytes.Buffer
	if err := writeJSON(&out, map[string]string{"ok": "yes"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if !strings.Contains(out.String(), "\n  \"ok\": \"yes\"\n") {
		t.Fatalf("writeJSON output = %q", out.String())
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

type fakeRoundTrip func(*http.Request) (*http.Response, error)

func (f fakeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func withDefaultHTTPClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: rt}
	t.Cleanup(func() { http.DefaultClient = oldClient })
}

func commandWithOutput(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}
