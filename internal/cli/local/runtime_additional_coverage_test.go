package local

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRuntimeHelperAdditionalBranches(t *testing.T) {
	if runtimeSnapshot(nil) != nil {
		t.Fatal("runtimeSnapshot(nil) should be nil")
	}
	applyExecutableIdentity(nil, executableIdentity{Path: "ignored"})
	if runtimeMatchesExecutable(nil, executableIdentity{}) {
		t.Fatal("nil runtime should not match executable")
	}
	if !runtimeMatchesExecutable(&runtimeInfo{BinaryHash: "old"}, executableIdentity{}) {
		t.Fatal("empty executable hash should be treated as matching")
	}
	if runtimeMatchesFleetDBRedisSettings(nil, "hash") {
		t.Fatal("nil runtime should not match redis settings")
	}
	if got := executableHash(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("executableHash(missing) = %q, want empty", got)
	}

	dataDir := t.TempDir()
	if err := os.WriteFile(runtimePath(dataDir), []byte("{bad json"), 0600); err != nil {
		t.Fatalf("write bad runtime: %v", err)
	}
	if _, err := readRuntime(dataDir); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("readRuntime malformed err = %v", err)
	}

	info := &runtimeInfo{Status: "starting", PID: 123, URL: "http://127.0.0.1:1", StartedAt: time.Now().UTC()}
	if err := writeRuntime(dataDir, info); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
	if info.Version != 1 || info.DataDir != dataDir || info.UpdatedAt.IsZero() {
		t.Fatalf("writeRuntime did not stamp metadata: %+v", info)
	}
}

func TestBundledExecutableAndFrontendFallbacks(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	exeDir := filepath.Dir(exe)
	baseName := "loom-coverage-helper"
	plain := filepath.Join(exeDir, baseName)
	dashed := filepath.Join(exeDir, baseName+"-darwin")
	t.Cleanup(func() {
		_ = os.Remove(plain)
		_ = os.Remove(dashed)
		_ = os.RemoveAll(filepath.Join(exeDir, "webui"))
		_ = os.Remove(filepath.Join(exeDir, "fleet-db"))
	})

	if err := os.WriteFile(plain, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write plain helper: %v", err)
	}
	if got := bundledExecutable(baseName); got != plain {
		t.Fatalf("bundledExecutable plain = %q, want %q", got, plain)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatalf("remove plain helper: %v", err)
	}
	if err := os.WriteFile(dashed, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write dashed helper: %v", err)
	}
	if got := bundledExecutable(baseName); got != dashed {
		t.Fatalf("bundledExecutable dashed = %q, want %q", got, dashed)
	}

	webuiDir := filepath.Join(exeDir, "webui")
	if err := os.MkdirAll(webuiDir, 0755); err != nil {
		t.Fatalf("mkdir webui dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(webuiDir, "index.html"), []byte("<html></html>"), 0600); err != nil {
		t.Fatalf("write bundled frontend: %v", err)
	}
	t.Setenv("LOOM_FRONTEND_DIR", "")
	if got := bundledFrontendDir(); got != webuiDir {
		t.Fatalf("bundledFrontendDir = %q, want %q", got, webuiDir)
	}

	fleetDB := filepath.Join(exeDir, "fleet-db")
	mode := os.FileMode(0700)
	if runtime.GOOS == "windows" {
		mode = 0600
	}
	if err := os.WriteFile(fleetDB, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("write fleet-db helper: %v", err)
	}
	t.Setenv("FLEET_DB_BIN", "")
	env := localEnv(t.TempDir(), 19998)
	if !envContains(env, "FLEET_DB_BIN="+fleetDB) {
		t.Fatalf("localEnv did not include bundled fleet-db: %v", env)
	}
	if !envContains(env, "LOOM_FRONTEND_DIR="+webuiDir) {
		t.Fatalf("localEnv did not include bundled frontend: %v", env)
	}
}
