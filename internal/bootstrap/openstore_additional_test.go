package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
)

func TestOpenStoreAdditionalErrorBranches(t *testing.T) {
	logger := slog.Default()
	if _, err := openCloudStore(fleetdb.Config{BaseURL: "://bad-url"}, logger); err == nil {
		t.Fatal("openCloudStore accepted malformed URL")
	}

	handle, ok, err := tryReuseLocalStore(context.Background(), t.TempDir(), fleetdb.Config{}, logger)
	if handle != nil || ok || err != nil {
		t.Fatalf("tryReuseLocalStore missing runtime = handle:%v ok:%v err:%v", handle, ok, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = waitAndOpenLocalStore(ctx, t.TempDir(), fleetdb.Config{}, logger, ErrEmbeddedAlreadyRunning)
	if err == nil || !strings.Contains(err.Error(), "existing runtime did not become healthy") {
		t.Fatalf("waitAndOpenLocalStore canceled err = %v", err)
	}

	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBBin, filepath.Join(t.TempDir(), "missing-fleet-db"))
	if _, err := OpenStore(context.Background(), t.TempDir(), logger); err == nil || !strings.Contains(err.Error(), "openstore: local") {
		t.Fatalf("OpenStore local missing binary err = %v", err)
	}
}

func TestOpenStoreLocalStartsEmbeddedRuntimeWhenFleetDBBinaryAvailable(t *testing.T) {
	if os.Getenv(EnvFleetDBBin) == "" {
		t.Skip("requires FLEET_DB_BIN")
	}
	t.Setenv(EnvFleetDBURL, "")
	t.Setenv(EnvFleetDBActor, "bootstrap-test")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	handle, err := OpenStore(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("OpenStore local embedded: %v", err)
	}
	if handle.Mode() != ModeLocal || handle.embedded == nil || handle.URL() == "" {
		t.Fatalf("handle mode=%s embedded=%t url=%q", handle.Mode(), handle.embedded != nil, handle.URL())
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("Close embedded handle: %v", err)
	}
}

func TestStartEmbeddedWithFakeFleetDBProcess(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "fleet-db")
	script := `#!/usr/bin/env python3
import http.server
import os
import signal
import sys

if "--help" in sys.argv:
    print("usage of fleet-db --auth-dev-mode --redis-addr")
    sys.exit(0)

addr = os.environ["FLEET_SERVER_ADDR"]
host, port = addr.rsplit(":", 1)

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        return

server = http.server.HTTPServer((host, int(port)), Handler)
signal.signal(signal.SIGINT, lambda signum, frame: sys.exit(0))
try:
    server.serve_forever()
finally:
    server.server_close()
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake fleet-db: %v", err)
	}
	t.Setenv(EnvFleetDBBin, binPath)

	dataDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	emb, err := StartEmbedded(ctx, dataDir, nil)
	if err != nil {
		t.Fatalf("StartEmbedded fake process: %v", err)
	}
	if emb.URL() == "" {
		t.Fatal("embedded URL is empty")
	}
	if _, err := readEmbeddedRuntime(filepath.Join(dataDir, "fleet-db")); err != nil {
		t.Fatalf("read embedded runtime metadata: %v", err)
	}
	if emb.Done() == nil {
		t.Fatal("Done channel is nil")
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("Stop fake process: %v", err)
	}
	select {
	case <-emb.Done():
	default:
		t.Fatal("Done channel was not closed by Stop")
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("second Stop fake process: %v", err)
	}
	if _, err := os.Stat(embeddedRuntimePath(filepath.Join(dataDir, "fleet-db"))); !os.IsNotExist(err) {
		t.Fatalf("runtime metadata still exists after Stop: %v", err)
	}
}

func TestEmbeddedRuntimeLockAndWritersAdditionalBranches(t *testing.T) {
	fleetDir := t.TempDir()
	lock, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err != nil {
		t.Fatalf("acquireEmbeddedRuntimeLock: %v", err)
	}
	if lock.path != filepath.Join(fleetDir, "embedded.lock") {
		t.Fatalf("lock path = %q", lock.path)
	}
	data, err := os.ReadFile(lock.path)
	if err != nil {
		t.Fatalf("read lock pid: %v", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatalf("lock file did not record pid: %q", data)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if err := ((*embeddedRuntimeLock)(nil)).Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}

	env := withDefaultEnv([]string{"A=1"}, "B", "2")
	if !containsEnv(env, "B=2") {
		t.Fatalf("withDefaultEnv did not append default: %v", env)
	}
	env = withDefaultEnv([]string{"B=  "}, "B", "3")
	if !containsEnv(env, "B=3") {
		t.Fatalf("withDefaultEnv did not replace blank value: %v", env)
	}
	env = withDefaultEnv([]string{"B=4"}, "B", "5")
	if !containsEnv(env, "B=4") || containsEnv(env, "B=5") {
		t.Fatalf("withDefaultEnv replaced non-empty value: %v", env)
	}
	defaults := appendEmbeddedFleetDBEnvDefaults([]string{EnvFleetRedisPoolSize + "="})
	if !containsEnv(defaults, EnvFleetRedisPoolSize+"="+defaultEmbeddedFleetRedisPoolSize) ||
		!containsEnv(defaults, EnvFleetRedisMinIdleConns+"="+defaultEmbeddedFleetRedisMinIdleConns) {
		t.Fatalf("appendEmbeddedFleetDBEnvDefaults = %v", defaults)
	}

	tail := newTailWriter(5)
	if n, err := tail.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("tail.Write n=%d err=%v", n, err)
	}
	if got := tail.String(); got != "bcdef" {
		t.Fatalf("tail.String = %q, want bcdef", got)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	writer := newSlogWriter(logger, slog.LevelInfo, "fleet-db")
	if n, err := writer.Write([]byte("line one\npartial")); n != len("line one\npartial") || err != nil {
		t.Fatalf("slog writer first write n=%d err=%v", n, err)
	}
	if !strings.Contains(logs.String(), "line one") || strings.Contains(logs.String(), "partial") {
		t.Fatalf("unexpected slog output after partial write: %q", logs.String())
	}
	if _, err := writer.Write([]byte(strings.Repeat("x", maxLineBuffer))); err != nil {
		t.Fatalf("slog writer large write: %v", err)
	}
	if !strings.Contains(logs.String(), "truncated=true") {
		t.Fatalf("large partial line was not logged as truncated: %q", logs.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := waitForEmbeddedRuntime(ctx, t.TempDir(), time.Millisecond, logger); err == nil {
		t.Fatal("waitForEmbeddedRuntime returned nil error for missing runtime and expired context")
	}

	if _, _, err := reuseEmbeddedRuntime(context.Background(), t.TempDir(), logger, time.Millisecond); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reuseEmbeddedRuntime missing file err = %v", err)
	}
}

func TestEmbeddedRuntimeMetadataReuseBranches(t *testing.T) {
	fleetDir := filepath.Join(t.TempDir(), "fleet-db")
	removeEmbeddedRuntimeIfOwner(fleetDir, 12345, "http://127.0.0.1:1")

	info := embeddedRuntimeInfo{PID: 999999, URL: "http://127.0.0.1:1"}
	if err := writeEmbeddedRuntime(fleetDir, info); err != nil {
		t.Fatalf("writeEmbeddedRuntime: %v", err)
	}
	read, err := readEmbeddedRuntime(fleetDir)
	if err != nil {
		t.Fatalf("readEmbeddedRuntime: %v", err)
	}
	if read.StartedAt.IsZero() {
		t.Fatalf("StartedAt was not defaulted: %+v", read)
	}
	removeEmbeddedRuntimeIfOwner(fleetDir, os.Getpid(), "http://different")
	if _, err := os.Stat(embeddedRuntimePath(fleetDir)); err != nil {
		t.Fatalf("non-owner remove deleted runtime: %v", err)
	}
	if _, ok, err := reuseEmbeddedRuntime(context.Background(), fleetDir, slog.Default(), time.Millisecond); ok || err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("reuse stale runtime ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(embeddedRuntimePath(fleetDir)); !os.IsNotExist(err) {
		t.Fatalf("stale runtime metadata was not removed: %v", err)
	}

	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: os.Getpid(), URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("rewrite runtime: %v", err)
	}
	removeEmbeddedRuntimeIfOwner(fleetDir, os.Getpid(), "http://127.0.0.1:1")
	if _, err := os.Stat(embeddedRuntimePath(fleetDir)); !os.IsNotExist(err) {
		t.Fatalf("owner remove left runtime metadata: %v", err)
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
