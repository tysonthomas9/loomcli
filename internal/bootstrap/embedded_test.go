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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	fleetDir := filepath.Join(dataDir, "fleet-db")
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{
		PID: os.Getpid(),
		URL: srv.URL,
	}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}

	h, err := OpenStore(context.Background(), dataDir, nil)
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
}
