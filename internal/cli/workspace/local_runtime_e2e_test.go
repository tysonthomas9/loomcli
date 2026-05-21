//go:build e2e
// +build e2e

package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestE2E_WorkspaceOpsEnsureRuntimeKeepsRuntimeStoreAlive(t *testing.T) {
	loom := loomBinaryPath(t)
	fleetDB := fleetDBBinaryPath(t)
	dataDir := t.TempDir()
	workspaceKey := "RUNTIMEFIX"

	t.Cleanup(func() {
		_, _, _ = runLoomRuntimeCommand(t, loom, dataDir, fleetDB, "local", "--data-dir", dataDir, "stop")
	})

	stdout, stderr, err := runLoomRuntimeCommand(t, loom, dataDir, fleetDB, "workspace", "add", workspaceKey)
	if err != nil {
		t.Fatalf("workspace add failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	stdout, stderr, err = runLoomRuntimeCommand(t, loom, dataDir, fleetDB, "workspace", "ops", "ensure-runtime", workspaceKey, "--json", "--timeout", "20")
	if err != nil {
		t.Fatalf("workspace ops ensure-runtime failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}

	status := readLocalRuntimeStatus(t, loom, dataDir, fleetDB)
	if !status.Healthy {
		t.Fatalf("local runtime is not healthy after ensure-runtime: %+v", status)
	}
	if status.Runtime.URL == "" {
		t.Fatalf("local runtime URL is empty after ensure-runtime: %+v", status)
	}

	assertRuntimeListsWorkspaces(t, status.Runtime.URL, workspaceKey)
}

func loomBinaryPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("loom")
	if err != nil {
		t.Skip("loom binary not found on PATH; skipping local runtime E2E test")
	}
	return path
}

func fleetDBBinaryPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FLEET_DB_BIN"); path != "" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		t.Skipf("FLEET_DB_BIN is set but not usable: %s", path)
	}
	path, err := exec.LookPath("fleet-db")
	if err != nil {
		t.Skip("fleet-db binary not found on PATH or FLEET_DB_BIN; skipping local runtime E2E test")
	}
	return path
}

func runLoomRuntimeCommand(t *testing.T, loom, dataDir, fleetDB string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, loom, args...)
	cmd.Env = localRuntimeE2EEnv(dataDir, fleetDB)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("command timed out: loom %s", strings.Join(args, " "))
	}
	return stdout.String(), stderr.String(), err
}

func localRuntimeE2EEnv(dataDir, fleetDB string) []string {
	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "LOOM_CONFIG_DIR="),
			strings.HasPrefix(entry, "LOOM_DESKTOP_DATA_DIR="),
			strings.HasPrefix(entry, "LOOM_WORKSPACE_RUNTIME_DIR="),
			strings.HasPrefix(entry, "LOOM_FLEET_DB_URL="),
			strings.HasPrefix(entry, "LOOM_FLEET_URL="),
			strings.HasPrefix(entry, "LOOM_SERVER_URL="),
			strings.HasPrefix(entry, "FLEET_DB_BIN="):
			continue
		default:
			env = append(env, entry)
		}
	}
	return append(env,
		"LOOM_CONFIG_DIR="+dataDir,
		"LOOM_DESKTOP_DATA_DIR="+dataDir,
		"LOOM_WORKSPACE_RUNTIME_DIR="+dataDir,
		"LOOM_WORKSPACE=RUNTIMEFIX",
		"FLEET_DB_BIN="+fleetDB,
		// Keep the e2e isolated from the host's $HOME/.loom/fleet-db
		// registry so a desktop loom serve on the developer's machine
		// can't be joined accidentally and skew results.
		"LOOM_FLEET_DB_NO_DISCOVERY=1",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

type localRuntimeE2EStatus struct {
	Healthy bool `json:"healthy"`
	Runtime struct {
		URL string `json:"url"`
	} `json:"runtime"`
	Error string `json:"error,omitempty"`
}

func readLocalRuntimeStatus(t *testing.T, loom, dataDir, fleetDB string) localRuntimeE2EStatus {
	t.Helper()
	stdout, stderr, err := runLoomRuntimeCommand(t, loom, dataDir, fleetDB, "local", "--data-dir", dataDir, "status", "--json")
	if err != nil {
		t.Fatalf("local status failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var status localRuntimeE2EStatus
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("parse local status JSON: %v\nstdout:\n%s", err, stdout)
	}
	return status
}

func assertRuntimeListsWorkspaces(t *testing.T, runtimeURL, workspaceKey string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	url := strings.TrimRight(runtimeURL, "/") + "/api/workspaces"
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	var lastBody string
	var lastStatus int

	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:gosec // local loopback runtime from test process
		if err != nil {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		lastBody = string(body)
		if readErr != nil {
			lastErr = readErr
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if resp.StatusCode == http.StatusOK && strings.Contains(lastBody, workspaceKey) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("runtime did not list workspace %s at %s; last status=%d err=%v body=%s", workspaceKey, url, lastStatus, lastErr, lastBody)
}
