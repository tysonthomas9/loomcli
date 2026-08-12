//go:build workitems_e2e

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

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workItemsFleetRuntime struct {
	URL    string
	APIKey string
}

func TestE2E_WorkItemsConformance_AllAdapters(t *testing.T) {
	fleetDBBin := fleetDBBinaryForWorkItemsConformance(t)
	loomBin := loomBinaryForWorkItemsConformance(t)

	t.Run("fleetdb-local", func(t *testing.T) {
		configDir := t.TempDir()
		workspace := "CONTRACTLOCAL"
		setWorkItemsModeEnv(t, map[string]string{
			"FLEET_DB_BIN": fleetDBBin, "LOOM_CONFIG_DIR": configDir,
			"LOOM_WORKSPACE": workspace, "LOOM_AGENT_NAME": "contract-local",
			"LOOM_ISSUE_BACKEND": "fleetdb", "LOOM_FLEET_DB_URL": "",
			"LOOM_FLEET_URL": "", "LOOM_SERVER_URL": "",
		})
		createLocalWorkspace(t, configDir, workspace)
		runWorkItemsConformance(t, workItemsSuiteConfig{
			NewAPI: func(t testing.TB) workitems.API {
				t.Helper()
				api, err := workitems.New(newFleetDBWorkItemsAdapter())
				if err != nil {
					t.Fatalf("compose local FleetDB Work Items: %v", err)
				}
				return api
			},
		})
	})

	remote := startFleetDBForWorkItemsConformance(t)

	t.Run("fleetdb-cloud", func(t *testing.T) {
		workspace := "CONTRACTCLOUD"
		createRemoteWorkspace(t, remote, workspace)
		setWorkItemsModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR": t.TempDir(), "LOOM_WORKSPACE": workspace,
			"LOOM_AGENT_NAME": "contract-cloud", "LOOM_ISSUE_BACKEND": "fleetdb",
			"LOOM_FLEET_DB_URL": remote.URL, "LOOM_FLEET_DB_API_KEY": remote.APIKey,
			"LOOM_FLEET_URL": "", "LOOM_SERVER_URL": "",
		})
		runWorkItemsConformance(t, workItemsSuiteConfig{
			NewAPI: func(t testing.TB) workitems.API {
				t.Helper()
				api, err := workitems.New(newFleetDBWorkItemsAdapter())
				if err != nil {
					t.Fatalf("compose cloud FleetDB Work Items: %v", err)
				}
				return api
			},
		})
	})

	t.Run("fleet-direct", func(t *testing.T) {
		workspace := "CONTRACTFLEET"
		createRemoteWorkspace(t, remote, workspace)
		setWorkItemsModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR": t.TempDir(), "LOOM_WORKSPACE": workspace,
			"LOOM_AGENT_NAME": "contract-fleet", "LOOM_ISSUE_BACKEND": "fleet",
			"LOOM_FLEET_URL": remote.URL, "LOOM_FLEET_API_KEY": remote.APIKey,
			"LOOM_FLEET_DB_URL": "", "LOOM_SERVER_URL": "",
		})
		runWorkItemsConformance(t, workItemsSuiteConfig{
			NewAPI: func(t testing.TB) workitems.API {
				t.Helper()
				adapter, err := createFleetWorkItemStore()
				if err != nil {
					t.Fatalf("create Fleet Work Items adapter: %v", err)
				}
				api, err := workitems.New(adapter)
				if err != nil {
					t.Fatalf("compose Fleet Work Items: %v", err)
				}
				return api
			},
		})
	})

	t.Run("api-server", func(t *testing.T) {
		workspace := "CONTRACTAPI"
		configDir := t.TempDir()
		createRemoteWorkspace(t, remote, workspace)
		serverURL := startLoomAPIServerForWorkItemsConformance(t, loomBin, configDir, remote, workspace)
		setWorkItemsModeEnv(t, map[string]string{
			"LOOM_CONFIG_DIR": configDir, "LOOM_WORKSPACE": workspace,
			"LOOM_AGENT_NAME": "contract-api", "LOOM_ISSUE_BACKEND": "api",
			"LOOM_SERVER_URL": serverURL, "LOOM_FLEET_DB_URL": "", "LOOM_FLEET_URL": "",
		})
		runWorkItemsConformance(t, workItemsSuiteConfig{
			NewAPI: func(t testing.TB) workitems.API {
				t.Helper()
				api, err := createAPIWorkItems()
				if err != nil {
					t.Fatalf("create API Work Items adapter: %v", err)
				}
				return api
			},
		})
	})
}

func fleetDBBinaryForWorkItemsConformance(t *testing.T) string {
	t.Helper()
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err == nil {
		return diag.Path
	}
	t.Skip("fleet-db binary unavailable; set FLEET_DB_BIN or install fleet-db on PATH")
	return ""
}

func loomBinaryForWorkItemsConformance(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("LOOM_BIN"); value != "" {
		if _, err := os.Stat(value); err != nil {
			t.Skipf("LOOM_BIN is set but unusable: %v", err)
		}
		return value
	}
	path, err := exec.LookPath("loom")
	if err != nil {
		t.Skip("loom binary unavailable; set LOOM_BIN or install loom on PATH")
	}
	return path
}

func startFleetDBForWorkItemsConformance(t *testing.T) workItemsFleetRuntime {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	dataDir := t.TempDir()
	embedded, err := bootstrap.StartEmbedded(ctx, dataDir, slog.Default())
	if err != nil {
		t.Fatalf("start FleetDB: %v", err)
	}
	t.Cleanup(func() {
		if err := embedded.Stop(); err != nil {
			t.Logf("stop FleetDB: %v", err)
		}
	})
	apiKey, err := authority.ReadLocalFleetDBServiceCredential(filepath.Join(dataDir, "fleet-db", "auth"))
	if err != nil {
		t.Fatalf("read embedded FleetDB service credential: %v", err)
	}
	return workItemsFleetRuntime{URL: embedded.URL(), APIKey: apiKey}
}

func createLocalWorkspace(t *testing.T, configDir, workspace string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handle, err := bootstrap.OpenStore(ctx, configDir, slog.Default())
	if err != nil {
		t.Fatalf("open local FleetDB store: %v", err)
	}
	defer handle.Close()
	createWorkspace(t, ctx, handle.Store.Workspaces(), workspace)
	createRepository(t, ctx, handle.Store.Repos(), workspace)
}

func createRemoteWorkspace(t *testing.T, runtime workItemsFleetRuntime, workspace string) {
	t.Helper()
	client, err := fleetdb.New(fleetdb.Config{BaseURL: runtime.URL, APIKey: runtime.APIKey, Actor: "workitems-conformance"})
	if err != nil {
		t.Fatalf("FleetDB client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Logf("close FleetDB client: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	createWorkspace(t, ctx, client.Workspaces(), workspace)
	createRepository(t, ctx, client.Repos(), workspace)
}

func createWorkspace(t *testing.T, ctx context.Context, workspaces store.WorkspaceStore, key string) {
	t.Helper()
	_, err := workspaces.Create(ctx, store.WorkspaceCreate{Key: key, Name: key})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create workspace %s: %v", key, err)
	}
}

func createRepository(t *testing.T, ctx context.Context, repos store.RepoStore, workspace string) {
	t.Helper()
	_, err := repos.Create(ctx, store.RepoCreate{
		WorkspaceKey: workspace,
		Name:         "contract-repo",
		SourceRepoID: "contract-repo",
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("create repository in %s: %v", workspace, err)
	}
}

func startLoomAPIServerForWorkItemsConformance(
	t *testing.T,
	loomBin, configDir string,
	fleetRuntime workItemsFleetRuntime,
	workspace string,
) string {
	t.Helper()
	port := freeLoopbackPort(t)
	serverURL := "http://127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(loomBin, "serve", "--bind", "127.0.0.1", "--port", strconv.Itoa(port))
	cmd.Env = workItemsModeEnv(map[string]string{
		"LOOM_CONFIG_DIR": configDir, "LOOM_WORKSPACE": workspace,
		"LOOM_AGENT_NAME": "contract-api-server", "LOOM_FLEET_DB_URL": fleetRuntime.URL,
		"LOOM_FLEET_DB_API_KEY": fleetRuntime.APIKey, "LOOM_ISSUE_BACKEND": "",
		"LOOM_FLEET_URL": "", "LOOM_SERVER_URL": "",
	})
	var stderr bytes.Buffer
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start loom serve: %v", err)
	}
	t.Cleanup(func() { stopWorkItemsConformanceProcess(t, cmd, stderr.String()) })
	waitForWorkItemsConformanceServer(t, serverURL, &stderr)
	return serverURL
}

func waitForWorkItemsConformanceServer(t *testing.T, serverURL string, stderr *bytes.Buffer) {
	t.Helper()
	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(serverURL + "/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("loom serve did not become healthy at %s; output:\n%s", serverURL, stderr.String())
}

func stopWorkItemsConformanceProcess(t *testing.T, cmd *exec.Cmd, output string) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func setWorkItemsModeEnv(t *testing.T, values map[string]string) {
	t.Helper()
	for _, key := range []string{
		"FLEET_DB_BIN", "LOOM_CONFIG_DIR", "LOOM_WORKSPACE", "LOOM_AGENT_NAME",
		"LOOM_ISSUE_BACKEND", "LOOM_FLEET_DB_URL", "LOOM_FLEET_DB_API_KEY",
		"LOOM_FLEET_URL", "LOOM_FLEET_API_KEY", "LOOM_SERVER_URL",
	} {
		t.Setenv(key, values[key])
	}
	ResetDefaultWorkItems()
}

func workItemsModeEnv(values map[string]string) []string {
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
