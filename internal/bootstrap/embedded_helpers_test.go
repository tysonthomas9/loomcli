package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/localsettings"
)

func TestEmbeddedRuntimeMetadataHelpers(t *testing.T) {
	fleetDir := t.TempDir()
	if _, err := readEmbeddedRuntime(fleetDir); err == nil {
		t.Fatal("read missing runtime returned nil error")
	}
	if err := os.WriteFile(embeddedRuntimePath(fleetDir), []byte(`{"pid":1}`), 0600); err != nil {
		t.Fatalf("write invalid runtime: %v", err)
	}
	if _, err := readEmbeddedRuntime(fleetDir); err == nil || !strings.Contains(err.Error(), "missing url") {
		t.Fatalf("missing url err = %v", err)
	}
	if err := os.WriteFile(embeddedRuntimePath(fleetDir), []byte(`{`), 0600); err != nil {
		t.Fatalf("write malformed runtime: %v", err)
	}
	if _, err := readEmbeddedRuntime(fleetDir); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("parse err = %v", err)
	}
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: 1, URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	info, err := readEmbeddedRuntime(fleetDir)
	if err != nil || info.URL != "http://127.0.0.1:1" || info.StartedAt.IsZero() {
		t.Fatalf("runtime info = %+v err=%v", info, err)
	}
	removeEmbeddedRuntimeIfOwner(fleetDir, 2, info.URL)
	if _, err := os.Stat(embeddedRuntimePath(fleetDir)); err != nil {
		t.Fatalf("runtime removed by non-owner: %v", err)
	}
	removeEmbeddedRuntimeIfOwner(fleetDir, 1, info.URL)
	if _, err := os.Stat(embeddedRuntimePath(fleetDir)); !os.IsNotExist(err) {
		t.Fatalf("runtime still exists after owner remove: %v", err)
	}
}

func TestEmbeddedRedisConfigMatchingAndSnapshot(t *testing.T) {
	local := localsettings.RedisConfig{}
	if !embeddedRuntimeRedisMatches(&embeddedRuntimeInfo{}, local) {
		t.Fatal("local redis runtime should match empty local config")
	}
	if embeddedSnapshotPath(localsettings.RedisConfig{Enabled: true}, "/tmp/snapshot") != "" {
		t.Fatal("external redis should not use snapshot path")
	}
	if got := embeddedSnapshotPath(local, "/tmp/snapshot"); got != "/tmp/snapshot" {
		t.Fatalf("local snapshot path = %q", got)
	}
	external := localsettings.RedisConfig{Enabled: true, Addr: " 127.0.0.1:6379 ", DB: 3, TLS: true, Password: "secret"}
	info := &embeddedRuntimeInfo{
		RedisExternal: true,
		RedisAddr:     "127.0.0.1:6379",
		RedisDB:       3,
		RedisTLS:      true,
	}
	info.RedisConfigHash = localsettings.RuntimeHash(external)
	if !embeddedRuntimeRedisMatches(info, external) {
		t.Fatalf("external redis should match: %+v", info)
	}
	info.RedisDB = 4
	if embeddedRuntimeRedisMatches(info, external) {
		t.Fatalf("changed external redis DB should not match")
	}
}

func TestFleetDBBinaryValidationAndProbeFailures(t *testing.T) {
	dir := t.TempDir()
	if err := validateFleetDBBinaryPath(dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("dir validation err = %v", err)
	}
	missing := filepath.Join(dir, "missing")
	if err := validateFleetDBBinaryPath(missing); err == nil {
		t.Fatal("missing binary validation returned nil")
	}
	notFleet := filepath.Join(dir, "fleet-db")
	if err := os.WriteFile(notFleet, []byte("#!/bin/sh\necho hello\n"), 0755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	diag := probeFleetDBBinary(notFleet, []string{notFleet}, "install it")
	if diag.Err == nil || !strings.Contains(diag.Err.Error(), "did not look like fleet-db") {
		t.Fatalf("probe err = %v output=%q", diag.Err, diag.ProbeOutput)
	}
	helpExitError := filepath.Join(dir, "fleet-db-help-exit")
	if err := os.WriteFile(helpExitError, []byte("#!/bin/sh\necho 'Usage of fleet-db'; exit 2\n"), 0755); err != nil {
		t.Fatalf("write fake help binary: %v", err)
	}
	diag = probeFleetDBBinary(helpExitError, []string{helpExitError}, "install it")
	if diag.Err != nil || !diag.Runnable {
		t.Fatalf("probe help exit diag = %+v", diag)
	}
	if runtime.GOOS != "windows" {
		noExec := filepath.Join(dir, "no-exec")
		if err := os.WriteFile(noExec, []byte("fleet"), 0644); err != nil {
			t.Fatalf("write noexec: %v", err)
		}
		if err := validateFleetDBBinaryPath(noExec); err == nil || !strings.Contains(err.Error(), "not executable") {
			t.Fatalf("noexec err = %v", err)
		}
	}
}

func TestDiscoverFleetDBBinaryWithEnvOverride(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fleet-db")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'Usage of fleet-db'\n"), 0755); err != nil {
		t.Fatalf("write fake fleet-db: %v", err)
	}
	t.Setenv(EnvFleetDBBin, fake)

	got, err := DiscoverFleetDBBinary()
	if err != nil {
		t.Fatalf("DiscoverFleetDBBinary: %v", err)
	}
	if got != fake {
		t.Fatalf("DiscoverFleetDBBinary = %q, want %q", got, fake)
	}
	diag := DiagnoseFleetDBBinary()
	if !diag.Runnable || diag.Path != fake || len(diag.Checked) == 0 {
		t.Fatalf("diag = %+v, want runnable env override", diag)
	}
}

func TestEmbeddedFleetDBLifecycleAccessors(t *testing.T) {
	emb := &EmbeddedFleetDB{
		url:     "http://127.0.0.1:1234",
		waitErr: make(chan error, 1),
		done:    make(chan struct{}),
	}
	if emb.URL() != "http://127.0.0.1:1234" {
		t.Fatalf("URL = %q", emb.URL())
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	select {
	case <-emb.Done():
	case <-time.After(time.Second):
		t.Fatalf("Done was not closed by Stop")
	}

	reaper := &EmbeddedFleetDB{
		waitErr: make(chan error, 1),
		done:    make(chan struct{}),
		logger:  slog.Default(),
	}
	reaper.reapAndPublish()
	if _, ok := <-reaper.waitErr; ok {
		t.Fatalf("reapAndPublish nil cmd should close waitErr")
	}
}

func TestStartEmbeddedWithFakeFleetDBBinary(t *testing.T) {
	dataDir := t.TempDir()
	fakeFleetDB := buildFakeFleetDBBinary(t)
	t.Setenv(EnvFleetDBBin, fakeFleetDB)
	t.Setenv(EnvFleetRedisPoolSize, "")

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	emb, err := StartEmbedded(ctx, dataDir, logger)
	if err != nil {
		t.Fatalf("StartEmbedded: %v\nlogs:\n%s", err, logs.String())
	}
	if emb.URL() == "" {
		t.Fatal("embedded URL is empty")
	}
	info, err := readEmbeddedRuntime(filepath.Join(dataDir, "fleet-db"))
	if err != nil {
		t.Fatalf("read runtime metadata: %v", err)
	}
	if info.PID == 0 || info.URL != emb.URL() || info.RedisExternal {
		t.Fatalf("runtime metadata = %+v", info)
	}
	if info.SnapshotPath == "" || info.RedisAddr == "" {
		t.Fatalf("runtime metadata missing local redis fields: %+v", info)
	}
	if err := emb.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-emb.Done():
	case <-time.After(time.Second):
		t.Fatal("Done not closed after Stop")
	}
	if _, err := os.Stat(embeddedRuntimePath(filepath.Join(dataDir, "fleet-db"))); !os.IsNotExist(err) {
		t.Fatalf("runtime metadata should be removed after owner Stop, err=%v", err)
	}
}

func buildFakeFleetDBBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "fake_fleetdb.go")
	bin := filepath.Join(dir, "fleet-db")
	const program = `package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("Usage of fleet-db --auth-dev-mode --redis-addr")
		return
	}
	addr := os.Getenv("FLEET_SERVER_ADDR")
	if addr == "" {
		fmt.Fprintln(os.Stderr, "missing FLEET_SERVER_ADDR")
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: addr, Handler: mux}
	errs := make(chan error, 1)
	go func() {
		errs <- server.ListenAndServe()
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
`
	if err := os.WriteFile(src, []byte(program), 0600); err != nil {
		t.Fatalf("write fake fleet-db source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, src) //nolint:norawexec // test builds a fake fleet-db binary.
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake fleet-db: %v\n%s", err, out)
	}
	return bin
}

func TestEmbeddedRuntimeLockContention(t *testing.T) {
	fleetDir := t.TempDir()
	lock1, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err != nil {
		t.Fatalf("acquire lock1: %v", err)
	}
	defer lock1.Release()
	lock2, err := acquireEmbeddedRuntimeLock(fleetDir)
	if err == nil {
		_ = lock2.Release()
		t.Fatalf("second lock acquisition succeeded, want contention error")
	}
	if !errors.Is(err, ErrEmbeddedAlreadyRunning) {
		t.Fatalf("second lock error = %v, want ErrEmbeddedAlreadyRunning", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("release lock1: %v", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("second release should be nil: %v", err)
	}
}

func TestTailAndSlogWriters(t *testing.T) {
	tw := newTailWriter(5)
	if n, err := tw.Write([]byte("hello world")); err != nil || n != len("hello world") {
		t.Fatalf("tail write n=%d err=%v", n, err)
	}
	if got := tw.String(); got != "world" {
		t.Fatalf("tail = %q", got)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sw := newSlogWriter(logger, slog.LevelInfo, "test")
	if n, err := sw.Write([]byte("line one\npartial")); err != nil || n != len("line one\npartial") {
		t.Fatalf("slog write n=%d err=%v", n, err)
	}
	if !strings.Contains(buf.String(), "line one") || strings.Contains(buf.String(), "partial") {
		t.Fatalf("unexpected log buffer after partial write: %s", buf.String())
	}
	big := strings.Repeat("x", maxLineBuffer)
	if _, err := sw.Write([]byte(big)); err != nil {
		t.Fatalf("write big partial: %v", err)
	}
	if !strings.Contains(buf.String(), "truncated=true") {
		t.Fatalf("expected truncated log, got %s", buf.String())
	}
}

func TestWaitForEmbeddedRuntimeTimesOutWithLastError(t *testing.T) {
	fleetDir := t.TempDir()
	if err := writeEmbeddedRuntime(fleetDir, embeddedRuntimeInfo{PID: 999999999, URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatalf("write runtime: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := waitForEmbeddedRuntime(ctx, fleetDir, 20*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("wait err = %v", err)
	}
}
