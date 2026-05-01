package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock err = %v, want already running", err)
	}
}
