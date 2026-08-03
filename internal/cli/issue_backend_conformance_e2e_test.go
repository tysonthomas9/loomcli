//go:build issuebackend_e2e
// +build issuebackend_e2e

package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/backendtest"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type issueBackendFleetRuntime struct {
	URL    string
	APIKey string
}

func TestE2E_IssueBackendConformance_AllModes(t *testing.T) {
	fleetDBBin := fleetDBBinaryForIssueBackendConformance(t)
	loomBin := loomBinaryForIssueBackendConformance(t)

	t.Run("fleetdb-local", func(t *testing.T) {
		configDir := t.TempDir()
		workspace := "CONTRACTLOCAL"
		setBackendModeEnv(t, map[string]string{
			"FLEET_DB_BIN":       fleetDBBin,
			"LOOM_CONFIG_DIR":    configDir,
			"LOOM_WORKSPACE":     workspace,
			"LOOM_AGENT_NAME":    "contract-local",
			"LOOM_ISSUE_BACKEND": "fleetdb",
			"LOOM_FLEET_DB_URL":  "",
			"LOOM_FLEET_URL":     "",
			"LOOM_SERVER_URL":    "",
		})
		createLocalWorkspace(t, configDir, workspace)
		backendtest.RunIssueBackendConformance(t, backendtest.IssueBackendSuiteConfig{
			NewBackend: func(testing.TB) backend.IssueBackend {
				return newFleetDBIssueBackend()
			},
		})
	})

	remoteURL := startFleetDBForIssueBackendConformance(t)

	t.Run("fleetdb-cloud", func(t *testing.T) {
		workspace := "CONTRACTCLOUD"
		createRemoteWorkspace(t, remoteURL, workspace)
		setBackendModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR":       t.TempDir(),
			"LOOM_WORKSPACE":        workspace,
			"LOOM_AGENT_NAME":       "contract-cloud",
			"LOOM_ISSUE_BACKEND":    "fleetdb",
			"LOOM_FLEET_DB_URL":     remoteURL.URL,
			"LOOM_FLEET_DB_API_KEY": remoteURL.APIKey,
			"LOOM_FLEET_URL":        "",
			"LOOM_SERVER_URL":       "",
		})
		backendtest.RunIssueBackendConformance(t, backendtest.IssueBackendSuiteConfig{
			NewBackend: func(testing.TB) backend.IssueBackend {
				return newFleetDBIssueBackend()
			},
		})
	})

	t.Run("fleet-direct", func(t *testing.T) {
		workspace := "CONTRACTFLEET"
		createRemoteWorkspace(t, remoteURL, workspace)
		setBackendModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR":    t.TempDir(),
			"LOOM_WORKSPACE":     workspace,
			"LOOM_AGENT_NAME":    "contract-fleet",
			"LOOM_ISSUE_BACKEND": "fleet",
			"LOOM_FLEET_URL":     remoteURL.URL,
			"LOOM_FLEET_API_KEY": remoteURL.APIKey,
			"LOOM_FLEET_DB_URL":  "",
			"LOOM_SERVER_URL":    "",
		})
		backendtest.RunIssueBackendConformance(t, backendtest.IssueBackendSuiteConfig{
			NewBackend: func(t testing.TB) backend.IssueBackend {
				t.Helper()
				ib, err := createFleetIssueBackend()
				if err != nil {
					t.Fatalf("create fleet backend: %v", err)
				}
				return ib
			},
		})
	})

	t.Run("api-server", func(t *testing.T) {
		workspace := "CONTRACTAPI"
		configDir := t.TempDir()
		createRemoteWorkspace(t, remoteURL, workspace)
		serverURL := startLoomAPIServerForIssueBackendConformance(t, loomBin, configDir, remoteURL, workspace)
		setBackendModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR":    configDir,
			"LOOM_WORKSPACE":     workspace,
			"LOOM_AGENT_NAME":    "contract-api",
			"LOOM_ISSUE_BACKEND": "api",
			"LOOM_SERVER_URL":    serverURL,
			"LOOM_FLEET_DB_URL":  "",
			"LOOM_FLEET_URL":     "",
		})
		backendtest.RunIssueBackendConformance(t, backendtest.IssueBackendSuiteConfig{
			NewBackend: func(t testing.TB) backend.IssueBackend {
				t.Helper()
				ib, err := createAPIIssueBackend()
				if err != nil {
					t.Fatalf("create api backend: %v", err)
				}
				return ib
			},
		})
	})
}

func fleetDBBinaryForIssueBackendConformance(t *testing.T) string {
	t.Helper()
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err == nil {
		return diag.Path
	}
	t.Skip("fleet-db binary unavailable; set FLEET_DB_BIN or install fleet-db on PATH")
	return ""
}

func loomBinaryForIssueBackendConformance(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("LOOM_BIN"); v != "" {
		if _, err := os.Stat(v); err != nil {
			t.Skipf("LOOM_BIN is set but unusable: %v", err)
		}
		return v
	}
	path, err := exec.LookPath("loom")
	if err != nil {
		t.Skip("loom binary unavailable; set LOOM_BIN or install loom on PATH")
	}
	return path
}

func startFleetDBForIssueBackendConformance(t *testing.T) issueBackendFleetRuntime {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	dataDir := t.TempDir()
	emb, err := bootstrap.StartEmbedded(ctx, dataDir, slog.Default())
	if err != nil {
		t.Fatalf("start fleet-db: %v", err)
	}
	t.Cleanup(func() {
		if err := emb.Stop(); err != nil {
			t.Logf("stop fleet-db: %v", err)
		}
	})
	apiKey, err := authority.ReadLocalFleetDBServiceCredential(filepath.Join(dataDir, "fleet-db", "auth"))
	if err != nil {
		t.Fatalf("read embedded FleetDB service credential: %v", err)
	}
	return issueBackendFleetRuntime{URL: emb.URL(), APIKey: apiKey}
}

func createLocalWorkspace(t *testing.T, configDir, workspace string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handle, err := bootstrap.OpenStore(ctx, configDir, slog.Default())
	if err != nil {
		t.Fatalf("open local fleet-db store: %v", err)
	}
	defer handle.Close()
	createWorkspace(t, ctx, handle.Store.Workspaces(), workspace)
}

func createRemoteWorkspace(t *testing.T, runtime issueBackendFleetRuntime, workspace string) {
	t.Helper()
	client, err := fleetdb.New(fleetdb.Config{BaseURL: runtime.URL, APIKey: runtime.APIKey, Actor: "backend-conformance"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close fleetdb client: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	createWorkspace(t, ctx, client.Workspaces(), workspace)
}

func createWorkspace(t *testing.T, ctx context.Context, workspaces store.WorkspaceStore, key string) {
	t.Helper()
	_, err := workspaces.Create(ctx, store.WorkspaceCreate{
		Key:  key,
		Name: key,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create workspace %s: %v", key, err)
	}
}

func startLoomAPIServerForIssueBackendConformance(t *testing.T, loomBin, configDir string, fleetRuntime issueBackendFleetRuntime, workspace string) string {
	t.Helper()
	port := freeLoopbackPort(t)
	serverURL := "http://127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(loomBin, "serve", "--bind", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Env = backendModeEnv(map[string]string{
		"LOOM_CONFIG_DIR":       configDir,
		"LOOM_WORKSPACE":        workspace,
		"LOOM_AGENT_NAME":       "contract-api-server",
		"LOOM_FLEET_DB_URL":     fleetRuntime.URL,
		"LOOM_FLEET_DB_API_KEY": fleetRuntime.APIKey,
		"LOOM_ISSUE_BACKEND":    "",
		"LOOM_FLEET_URL":        "",
		"LOOM_SERVER_URL":       "",
	})
	var stderr bytes.Buffer
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start loom serve: %v", err)
	}
	t.Cleanup(func() {
		stopIssueBackendConformanceProcess(t, cmd, stderr.String())
	})
	waitForIssueBackendConformanceServer(t, serverURL, &stderr)
	return serverURL
}

func waitForIssueBackendConformanceServer(t *testing.T, serverURL string, stderr *bytes.Buffer) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(serverURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("loom serve did not become healthy at %s; output:\n%s", serverURL, stderr.String())
}

func stopIssueBackendConformanceProcess(t *testing.T, cmd *exec.Cmd, output string) {
	t.Helper()
	if cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Logf("killed loom serve after timeout; output:\n%s", output)
	}
}

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func setBackendModeEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{
		"FLEET_DB_BIN",
		"LOOM_CONFIG_DIR",
		"LOOM_WORKSPACE",
		"LOOM_AGENT_NAME",
		"LOOM_ISSUE_BACKEND",
		"LOOM_FLEET_DB_URL",
		"LOOM_FLEET_DB_API_KEY",
		"LOOM_FLEET_URL",
		"LOOM_FLEET_API_KEY",
		"LOOM_SERVER_URL",
	} {
		t.Setenv(key, values[key])
	}
	ResetDefaultIssueBackend()
}

func backendModeEnv(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if _, ok := values[key]; ok {
			continue
		}
		env = append(env, entry)
	}
	for key, value := range values {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}
