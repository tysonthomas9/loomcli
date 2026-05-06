package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
