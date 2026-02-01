package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultFindWebUIBinary_EnvVar(t *testing.T) {
	// Create a fake binary
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "beads-web-ui")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	SetupTestEnv(t, map[string]string{"BEADS_WEBUI_BIN": fakeBin})

	path, err := defaultFindWebUIBinary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != fakeBin {
		t.Errorf("got %q, want %q", path, fakeBin)
	}
}

func TestDefaultFindWebUIBinary_EnvVarNotFound(t *testing.T) {
	SetupTestEnv(t, map[string]string{"BEADS_WEBUI_BIN": "/nonexistent/beads-web-ui"})

	_, err := defaultFindWebUIBinary()
	if err == nil {
		t.Fatal("expected error when BEADS_WEBUI_BIN points to nonexistent file")
	}
}

func TestDefaultFindWebUIBinary_FallbackPath(t *testing.T) {
	// Unset the env var so it falls back to PATH lookup
	SetupTestEnv(t, map[string]string{"BEADS_WEBUI_BIN": ""})
	// Explicitly unset after SetupTestEnv sets it to ""
	os.Unsetenv("BEADS_WEBUI_BIN")

	// Create a temp dir with a fake binary and prepend to PATH
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "beads-web-ui")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	os.Setenv("PATH", tmpDir+":"+origPath)
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	path, err := defaultFindWebUIBinary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != fakeBin {
		t.Errorf("got %q, want %q", path, fakeBin)
	}
}

func TestDefaultFindWebUIBinary_NotFound(t *testing.T) {
	SetupTestEnv(t, map[string]string{"BEADS_WEBUI_BIN": ""})
	os.Unsetenv("BEADS_WEBUI_BIN")

	// Use empty PATH so binary won't be found
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "")
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	_, err := defaultFindWebUIBinary()
	if err == nil {
		t.Fatal("expected error when binary not found")
	}
}

func TestNewWebUIProcess_BinaryNotFound(t *testing.T) {
	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) {
		return "", fmt.Errorf("beads-web-ui binary not found")
	}
	t.Cleanup(func() { findWebUIBinary = orig })

	_, err := NewWebUIProcess(8080, "http://localhost:3000")
	if err == nil {
		t.Fatal("expected error when binary not found")
	}
}

func TestNewWebUIProcess_Success(t *testing.T) {
	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) {
		return "/usr/bin/beads-web-ui", nil
	}
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(8080, "http://localhost:3000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.port != 8080 {
		t.Errorf("port = %d, want 8080", p.port)
	}
	if p.corsOrigin != "http://localhost:3000" {
		t.Errorf("corsOrigin = %q, want %q", p.corsOrigin, "http://localhost:3000")
	}
	if p.binaryPath != "/usr/bin/beads-web-ui" {
		t.Errorf("binaryPath = %q, want %q", p.binaryPath, "/usr/bin/beads-web-ui")
	}
}

func TestWebUIProcess_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	script := filepath.Join(tmpDir, "fake-webui")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 60\n"), 0755); err != nil {
		t.Fatal(err)
	}

	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) { return script, nil }
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(8080, "")
	if err != nil {
		t.Fatalf("NewWebUIProcess: %v", err)
	}

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify process is running
	p.mu.Lock()
	pid := p.cmd.Process.Pid
	p.mu.Unlock()
	if pid == 0 {
		t.Fatal("expected non-zero PID")
	}

	// Stop should return without hanging
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5 seconds")
	}
}

func TestWebUIProcess_MonitorRestartsOnCrash(t *testing.T) {
	// Create a script that exits immediately (simulates crash)
	tmpDir := t.TempDir()
	script := filepath.Join(tmpDir, "crash-webui")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) { return script, nil }
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(8080, "")
	if err != nil {
		t.Fatalf("NewWebUIProcess: %v", err)
	}

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Let monitor attempt at least one restart (1s backoff)
	time.Sleep(1500 * time.Millisecond)

	// Stop should work cleanly even during backoff
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return within 5 seconds")
	}
}

func TestWebUIProcess_DoubleStop(t *testing.T) {
	tmpDir := t.TempDir()
	script := filepath.Join(tmpDir, "fake-webui")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 60\n"), 0755); err != nil {
		t.Fatal(err)
	}

	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) { return script, nil }
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(8080, "")
	if err != nil {
		t.Fatalf("NewWebUIProcess: %v", err)
	}

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Double Stop should not panic (sync.Once guards channel close)
	p.Stop()
	p.Stop() // should be safe
}

func TestWebUIProcess_StartFailure(t *testing.T) {
	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) { return "/nonexistent/binary", nil }
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(8080, "")
	if err != nil {
		t.Fatalf("NewWebUIProcess: %v", err)
	}

	err = p.Start()
	if err == nil {
		t.Fatal("expected error when binary doesn't exist")
	}
}

func TestWebUIProcess_CorsOmittedWhenEmpty(t *testing.T) {
	// Verify that when CorsOrigin is empty, -cors flag is not passed
	// We test this indirectly by checking the process starts without error
	tmpDir := t.TempDir()
	// Script that prints args and exits
	script := filepath.Join(tmpDir, "args-webui")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+filepath.Join(tmpDir, "args.txt")+"\nexec sleep 60\n"), 0755); err != nil {
		t.Fatal(err)
	}

	orig := findWebUIBinary
	findWebUIBinary = func() (string, error) { return script, nil }
	t.Cleanup(func() { findWebUIBinary = orig })

	p, err := NewWebUIProcess(9090, "")
	if err != nil {
		t.Fatalf("NewWebUIProcess: %v", err)
	}

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	// Give the script a moment to write args
	time.Sleep(200 * time.Millisecond)

	argsFile := filepath.Join(tmpDir, "args.txt")
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}

	args := string(data)
	if args != "-port 9090\n" {
		t.Errorf("args = %q, want %q", args, "-port 9090\n")
	}
}
