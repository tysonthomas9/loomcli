//go:build windows

package rpc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPipeName_Deterministic(t *testing.T) {
	t.Parallel()

	socketPath := `C:\Users\test\project\.loom\loom.sock`
	result1 := pipeName(socketPath)
	result2 := pipeName(socketPath)

	if result1 != result2 {
		t.Errorf("pipeName() not deterministic: %q != %q", result1, result2)
	}
}

func TestPipeName_DifferentWorkspaces(t *testing.T) {
	t.Parallel()

	path1 := `C:\Users\test\project1\.loom\loom.sock`
	path2 := `C:\Users\test\project2\.loom\loom.sock`

	result1 := pipeName(path1)
	result2 := pipeName(path2)

	if result1 == result2 {
		t.Errorf("different workspaces should produce different pipe names: %q == %q", result1, result2)
	}
}

func TestPipeName_Format(t *testing.T) {
	t.Parallel()

	socketPath := `C:\Users\test\project\.loom\loom.sock`
	result := pipeName(socketPath)

	if !strings.HasPrefix(result, `\\.\pipe\loom-`) {
		t.Errorf("pipe name should start with \\\\.\\pipe\\loom-, got %q", result)
	}

	// Extract the hash portion
	hash := strings.TrimPrefix(result, `\\.\pipe\loom-`)
	if len(hash) != 16 {
		t.Errorf("pipe name hash should be 16 hex chars, got %d chars: %q", len(hash), hash)
	}

	// Verify it's valid hex
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("pipe name hash contains non-hex char: %c in %q", c, hash)
			break
		}
	}
}

func TestCurrentUserSDDL(t *testing.T) {
	t.Parallel()

	sddl := currentUserSDDL()

	// Should start with D:P(A;;GA;;;
	if !strings.HasPrefix(sddl, "D:P(A;;GA;;;") {
		t.Errorf("SDDL should start with D:P(A;;GA;;;, got %q", sddl)
	}

	// Should end with )
	if !strings.HasSuffix(sddl, ")") {
		t.Errorf("SDDL should end with ), got %q", sddl)
	}

	// Should contain a SID (S-1-5-...) or CO fallback
	inner := strings.TrimPrefix(sddl, "D:P(A;;GA;;;")
	inner = strings.TrimSuffix(inner, ")")
	if inner != "CO" && !strings.HasPrefix(inner, "S-1-") {
		t.Errorf("SDDL should contain SID (S-1-...) or CO fallback, got %q", inner)
	}
}

func TestEndpointInfoPipeRoundTrip(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "loom.sock")

	pipePath := `\\.\pipe\loom-abcdef0123456789`
	info := endpointInfo{
		Network: "pipe",
		Address: pipePath,
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal endpoint info: %v", err)
	}

	if err := os.WriteFile(socketPath, data, 0o600); err != nil {
		t.Fatalf("failed to write endpoint info: %v", err)
	}

	// Read it back
	readData, err := os.ReadFile(socketPath)
	if err != nil {
		t.Fatalf("failed to read endpoint info: %v", err)
	}

	var readInfo endpointInfo
	if err := json.Unmarshal(readData, &readInfo); err != nil {
		t.Fatalf("failed to unmarshal endpoint info: %v", err)
	}

	if readInfo.Network != "pipe" {
		t.Errorf("network should be 'pipe', got %q", readInfo.Network)
	}

	if readInfo.Address != pipePath {
		t.Errorf("address should be %q, got %q", pipePath, readInfo.Address)
	}
}

func TestDialRPC_TCPFallback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "loom.sock")

	// Write TCP endpoint info (simulating old daemon)
	info := endpointInfo{
		Network: "tcp",
		Address: "127.0.0.1:99999", // invalid port, will fail to connect
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal endpoint info: %v", err)
	}

	if err := os.WriteFile(socketPath, data, 0o600); err != nil {
		t.Fatalf("failed to write endpoint info: %v", err)
	}

	// Attempt to dial - should attempt TCP connection (and fail since nothing is listening)
	_, err = dialRPC(socketPath, 100*time.Millisecond)
	if err == nil {
		t.Error("dialRPC should fail when nothing is listening on the TCP port")
	}
	// The error should be a TCP dial error, not an unsupported network error
	if strings.Contains(err.Error(), "unsupported RPC network type") {
		t.Errorf("dialRPC should attempt TCP fallback, not reject the network type: %v", err)
	}
}

func TestDialRPC_UnsupportedNetwork(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "loom.sock")

	info := endpointInfo{
		Network: "udp",
		Address: "127.0.0.1:12345",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal endpoint info: %v", err)
	}

	if err := os.WriteFile(socketPath, data, 0o600); err != nil {
		t.Fatalf("failed to write endpoint info: %v", err)
	}

	_, err = dialRPC(socketPath, 100*time.Millisecond)
	if err == nil {
		t.Error("dialRPC should fail for unsupported network type")
	}
	if !strings.Contains(err.Error(), "unsupported RPC network type") {
		t.Errorf("expected unsupported network error, got: %v", err)
	}
}

func TestDialRPC_MissingAddress(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "loom.sock")

	info := endpointInfo{
		Network: "pipe",
		Address: "",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("failed to marshal endpoint info: %v", err)
	}

	if err := os.WriteFile(socketPath, data, 0o600); err != nil {
		t.Fatalf("failed to write endpoint info: %v", err)
	}

	_, err = dialRPC(socketPath, 100*time.Millisecond)
	if err == nil {
		t.Error("dialRPC should fail for empty address")
	}
	if !strings.Contains(err.Error(), "missing address") {
		t.Errorf("expected missing address error, got: %v", err)
	}
}

func TestDialRPC_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := dialRPC(`C:\nonexistent\loom.sock`, 100*time.Millisecond)
	if err == nil {
		t.Error("dialRPC should fail for nonexistent socket path")
	}
}

func TestEndpointExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	t.Run("exists", func(t *testing.T) {
		socketPath := filepath.Join(tmpDir, "exists.sock")
		if err := os.WriteFile(socketPath, []byte("{}"), 0o600); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		if !endpointExists(socketPath) {
			t.Error("endpointExists should return true for existing file")
		}
	})

	t.Run("not exists", func(t *testing.T) {
		socketPath := filepath.Join(tmpDir, "not_exists.sock")

		if endpointExists(socketPath) {
			t.Error("endpointExists should return false for nonexistent file")
		}
	})
}

func TestNormalizeWorkspacePath(t *testing.T) {
	t.Parallel()

	// Normalization should lowercase the result (Windows case-insensitivity)
	result := normalizeWorkspacePath(`C:\Users\TEST\Project`)
	if result != strings.ToLower(result) {
		t.Errorf("normalizeWorkspacePath should return lowercase, got %q", result)
	}
}
