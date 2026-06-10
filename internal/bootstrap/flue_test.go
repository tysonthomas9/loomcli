package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFlueRuntime(t *testing.T, dataDir string, info flueRuntimeInfo) {
	t.Helper()
	if err := os.MkdirAll(flueDir(dataDir), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flueRuntimePath(dataDir), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestReuseFlueRuntime_NoFile(t *testing.T) {
	t.Parallel()
	url, ok, err := ReuseFlueRuntime(context.Background(), t.TempDir())
	if err != nil || ok || url != "" {
		t.Fatalf("got url=%q ok=%v err=%v, want absent", url, ok, err)
	}
}

func TestReuseFlueRuntime_HealthyProcess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dataDir := t.TempDir()
	// Use our own PID — definitely running.
	writeFlueRuntime(t, dataDir, flueRuntimeInfo{PID: os.Getpid(), URL: srv.URL, StartedAt: time.Now()})

	url, ok, err := ReuseFlueRuntime(context.Background(), dataDir)
	if err != nil || !ok || url != srv.URL {
		t.Fatalf("got url=%q ok=%v err=%v, want reuse of %s", url, ok, err, srv.URL)
	}
}

func TestReuseFlueRuntime_DeadPIDRemovesFile(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writeFlueRuntime(t, dataDir, flueRuntimeInfo{PID: 1 << 30, URL: "http://127.0.0.1:1", StartedAt: time.Now()})

	_, ok, err := ReuseFlueRuntime(context.Background(), dataDir)
	if ok || err == nil {
		t.Fatalf("got ok=%v err=%v, want stale error", ok, err)
	}
	if _, statErr := os.Stat(flueRuntimePath(dataDir)); !os.IsNotExist(statErr) {
		t.Fatal("stale runtime.json should be removed")
	}
}

func TestRemoveFlueRuntimeIfOwner(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	writeFlueRuntime(t, dataDir, flueRuntimeInfo{PID: 42, URL: "http://x"})

	removeFlueRuntimeIfOwner(dataDir, 43, "http://x") // wrong pid → keep
	if _, err := os.Stat(flueRuntimePath(dataDir)); err != nil {
		t.Fatal("runtime.json should remain for non-owner")
	}
	removeFlueRuntimeIfOwner(dataDir, 42, "http://x") // owner → remove
	if _, err := os.Stat(flueRuntimePath(dataDir)); !os.IsNotExist(err) {
		t.Fatal("runtime.json should be removed by owner")
	}
}

func TestStartFlue_RequiresProjectOrDist(t *testing.T) {
	t.Parallel()
	_, err := StartFlue(context.Background(), t.TempDir(), FlueConfig{})
	if err == nil {
		t.Fatal("want config error")
	}
}

func TestDiscoverFlueBinary_EnvOverride(t *testing.T) {
	// Not parallel: mutates env.
	bin := filepath.Join(t.TempDir(), "flue")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv(EnvFlueBin, bin)
	got, err := discoverFlueBinary("")
	if err != nil || got != bin {
		t.Fatalf("got %q err=%v", got, err)
	}
	t.Setenv(EnvFlueBin, filepath.Join(t.TempDir(), "missing"))
	if _, err := discoverFlueBinary(""); err == nil {
		t.Fatal("want error for missing env binary")
	}
}
