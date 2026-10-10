//go:build e2e

package serve

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A workspace registered on a local loom serve (embedded FleetDB over the
// in-process miniredis) is still listed after serve stops on SIGTERM and
// starts again on the same LOOM_CONFIG_DIR. Needs fleet-db (FLEET_DB_BIN or
// PATH).
func TestE2E_ServeRestartKeepsWorkspaceRegistrations(t *testing.T) {
	testServeRestartKeepsWorkspace(t, syscall.SIGTERM)
}

// The same holds when serve is SIGKILLed right after the create returns: no
// shutdown hook runs, so the create itself must have reached disk.
func TestE2E_ServeCrashKeepsWorkspaceRegistrations(t *testing.T) {
	testServeRestartKeepsWorkspace(t, syscall.SIGKILL)
}

func testServeRestartKeepsWorkspace(t *testing.T, sig syscall.Signal) {
	fleetDB := os.Getenv("FLEET_DB_BIN")
	if fleetDB == "" {
		var err error
		if fleetDB, err = exec.LookPath("fleet-db"); err != nil {
			t.Skip("fleet-db not found on PATH or FLEET_DB_BIN")
		}
	}
	dir := t.TempDir()
	loom := filepath.Join(dir, "loom")
	run(t, "", "go", "build", "-o", loom, "github.com/tysonthomas9/loomcli/cmd/loom")
	repo := filepath.Join(dir, "repo")
	run(t, "", "git", "init", "-q", repo)
	cfgDir := filepath.Join(dir, "cfg")

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	serve := func() *exec.Cmd {
		cmd := exec.Command(loom, "serve", "--port", fmt.Sprint(port))
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "LOOM_CONFIG_DIR="+cfgDir, "FLEET_DB_BIN="+fleetDB, "LOOM_FLEET_DB_URL=")
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		for i := 0; i < 80; i++ {
			if resp, err := http.Get(base + "/health"); err == nil {
				_ = resp.Body.Close()
				return cmd
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatal("loom serve did not become healthy")
		return nil
	}
	listed := func() string {
		resp, err := http.Get(base + "/api/workspaces")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	first := serve()
	resp, err := http.Post(base+"/api/workspaces", "application/json",
		strings.NewReader(fmt.Sprintf(`{"name":"restart-ws","type":"empty","repos":[%q]}`, repo)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create workspace: HTTP %d", resp.StatusCode)
	}
	if got := listed(); !strings.Contains(got, `"id":"RESTART-WS"`) {
		t.Fatalf("workspace not listed before the restart: %s", got)
	}

	if err := first.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	err = first.Wait()
	if sig == syscall.SIGTERM && err != nil {
		t.Fatalf("loom serve did not exit cleanly on SIGTERM: %v", err)
	}
	if ws, ok := first.ProcessState.Sys().(syscall.WaitStatus); sig == syscall.SIGKILL && !(ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL) {
		t.Fatalf("loom serve was not SIGKILLed: %v", err)
	}

	serve()
	if got := listed(); !strings.Contains(got, `"id":"RESTART-WS"`) {
		t.Fatalf("workspace lost across the serve restart: %s", got)
	}
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
