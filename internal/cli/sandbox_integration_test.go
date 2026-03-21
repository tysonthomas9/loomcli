//go:build integration

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests for OpenShell sandbox execution.
// These tests require Docker and OpenShell to be installed and a gateway running.
// Skip with: go test -short
// Run with:  go test -run TestSandboxIntegration -v -timeout 300s

func skipIfNoOpenShell(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if _, err := exec.LookPath("openshell"); err != nil {
		t.Skip("openshell not found on PATH — skipping integration test")
	}
	// Verify gateway is running
	out, err := exec.Command("openshell", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Connected") {
		t.Skip("openshell gateway not running — skipping integration test")
	}
}

func TestSandboxIntegration_UploadAndRun(t *testing.T) {
	skipIfNoOpenShell(t)

	// Create a simple test script to upload and execute
	tmpDir := t.TempDir()
	testScript := filepath.Join(tmpDir, "hello.sh")
	if err := os.WriteFile(testScript, []byte("#!/bin/sh\necho UPLOAD_WORKS\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create sandbox, upload script, run it
	cmd := exec.Command("openshell", "sandbox", "create",
		"--name", "test-integration-upload",
		"--no-keep",
		"--upload", testScript+":/sandbox/bin",
		"--", "sh", "-c", "chmod +x /sandbox/bin/hello.sh && /sandbox/bin/hello.sh")
	out, err := cmd.CombinedOutput()
	output := string(out)

	defer exec.Command("openshell", "sandbox", "delete", "test-integration-upload").Run()

	if err != nil {
		t.Fatalf("sandbox create failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(output, "UPLOAD_WORKS") {
		t.Errorf("expected 'UPLOAD_WORKS' in output, got: %s", output)
	}
}

func TestSandboxIntegration_GitClone(t *testing.T) {
	skipIfNoOpenShell(t)

	// Test that git clone works inside sandbox with SSL workaround
	cmd := exec.Command("openshell", "sandbox", "create",
		"--name", "test-integration-git",
		"--no-keep",
		"--", "sh", "-c",
		"export GIT_SSL_NO_VERIFY=1 && git clone --depth 1 https://github.com/NVIDIA/OpenShell.git /tmp/test-repo 2>&1 && echo CLONE_OK || echo CLONE_FAILED")
	out, err := cmd.CombinedOutput()
	output := string(out)

	defer exec.Command("openshell", "sandbox", "delete", "test-integration-git").Run()

	if err != nil {
		t.Fatalf("sandbox create failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(output, "CLONE_OK") {
		t.Errorf("git clone failed inside sandbox. output: %s", output)
	}
}

func TestSandboxIntegration_UploadPathFormat(t *testing.T) {
	skipIfNoOpenShell(t)

	// Verify that --upload <file>:<dir> places the file at <dir>/<filename>
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "myfile.txt")
	if err := os.WriteFile(testFile, []byte("test-content"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("openshell", "sandbox", "create",
		"--name", "test-integration-upload-fmt",
		"--no-keep",
		"--upload", testFile+":/sandbox/data",
		"--", "sh", "-c",
		"cat /sandbox/data/myfile.txt && echo PATH_OK || echo PATH_FAILED")
	out, err := cmd.CombinedOutput()
	output := string(out)

	defer exec.Command("openshell", "sandbox", "delete", "test-integration-upload-fmt").Run()

	if err != nil {
		t.Fatalf("sandbox create failed: %v\noutput: %s", err, output)
	}

	if !strings.Contains(output, "test-content") || !strings.Contains(output, "PATH_OK") {
		t.Errorf("upload path format incorrect — file not found at expected path. output: %s", output)
	}
}

func TestSandboxIntegration_SandboxCleanup(t *testing.T) {
	skipIfNoOpenShell(t)

	name := "test-integration-cleanup"

	// Create a sandbox (without --no-keep so it persists)
	cmd := exec.Command("openshell", "sandbox", "create",
		"--name", name,
		"--", "echo", "done")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sandbox create failed: %v\noutput: %s", err, string(out))
	}

	// Verify it exists
	listOut, _ := exec.Command("openshell", "sandbox", "list", "--names").Output()
	if !strings.Contains(string(listOut), name) {
		t.Fatalf("sandbox %s not found in list after creation", name)
	}

	// Delete it
	if out, err := exec.Command("openshell", "sandbox", "delete", name).CombinedOutput(); err != nil {
		t.Fatalf("sandbox delete failed: %v\noutput: %s", err, string(out))
	}

	// Verify it's gone
	listOut2, _ := exec.Command("openshell", "sandbox", "list", "--names").Output()
	if strings.Contains(string(listOut2), name) {
		t.Errorf("sandbox %s still exists after deletion", name)
	}
}
