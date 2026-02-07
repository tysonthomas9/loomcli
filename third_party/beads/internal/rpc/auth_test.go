package rpc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token1, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken() returned error: %v", err)
	}

	// Token should be 64 hex characters (32 bytes encoded)
	if len(token1) != 64 {
		t.Errorf("expected token length 64, got %d", len(token1))
	}

	// Verify all characters are valid hex
	for i, c := range token1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("token char at index %d is not hex: %c", i, c)
		}
	}

	// Two calls should produce different tokens
	token2, err := generateToken()
	if err != nil {
		t.Fatalf("second generateToken() returned error: %v", err)
	}

	if token1 == token2 {
		t.Error("two calls to generateToken() produced the same token")
	}
}

func TestTokenFilePath(t *testing.T) {
	tests := []struct {
		name       string
		socketPath string
		want       string
	}{
		{
			name:       "beads socket in .beads dir",
			socketPath: filepath.Join("/home/user/project", ".beads", "bd.sock"),
			want:       filepath.Join("/home/user/project", ".beads", "rpc-token"),
		},
		{
			name:       "socket in tmp dir",
			socketPath: filepath.Join("/tmp", "beads-hash", "rpc.sock"),
			want:       filepath.Join("/tmp", "beads-hash", "rpc-token"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenFilePath(tt.socketPath)
			if got != tt.want {
				t.Errorf("tokenFilePath(%q) = %q, want %q", tt.socketPath, got, tt.want)
			}
		})
	}
}

func TestWriteReadTokenFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rpc-token")

	token := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	if err := writeTokenFile(path, token); err != nil {
		t.Fatalf("writeTokenFile() error: %v", err)
	}

	got, err := readTokenFile(path)
	if err != nil {
		t.Fatalf("readTokenFile() error: %v", err)
	}

	if got != token {
		t.Errorf("readTokenFile() = %q, want %q", got, token)
	}
}

func TestWriteTokenFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions not reliable on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rpc-token")

	token := "deadbeef01234567deadbeef01234567deadbeef01234567deadbeef01234567"

	if err := writeTokenFile(path, token); err != nil {
		t.Fatalf("writeTokenFile() error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat() error: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("token file permissions = %o, want 0600", perm)
	}
}

func TestValidateAuthToken_Disabled(t *testing.T) {
	// When server token is empty, auth is disabled - all requests should pass
	req := &Request{
		Operation: OpPing,
		AuthToken: "",
	}

	if err := validateAuthToken("", req); err != nil {
		t.Errorf("validateAuthToken with disabled auth returned error: %v", err)
	}

	// Even a request with a token should pass when auth is disabled
	req.AuthToken = "some-token"
	if err := validateAuthToken("", req); err != nil {
		t.Errorf("validateAuthToken with disabled auth (token present) returned error: %v", err)
	}
}

func TestValidateAuthToken_ValidToken(t *testing.T) {
	serverToken := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	req := &Request{
		Operation: OpPing,
		AuthToken: serverToken,
	}

	if err := validateAuthToken(serverToken, req); err != nil {
		t.Errorf("validateAuthToken with matching token returned error: %v", err)
	}
}

func TestValidateAuthToken_InvalidToken(t *testing.T) {
	serverToken := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	req := &Request{
		Operation: OpPing,
		AuthToken: "wrong_token_wrong_token_wrong_token_wrong_token_wrong_token_0000",
	}

	err := validateAuthToken(serverToken, req)
	if err == nil {
		t.Fatal("validateAuthToken with wrong token should return error")
	}

	if got := err.Error(); got != "authentication failed: invalid auth token" {
		t.Errorf("unexpected error message: %q", got)
	}
}

func TestValidateAuthToken_MissingToken(t *testing.T) {
	serverToken := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	req := &Request{
		Operation: OpPing,
		AuthToken: "",
	}

	err := validateAuthToken(serverToken, req)
	if err == nil {
		t.Fatal("validateAuthToken with missing token should return error")
	}

	// Error message should be helpful for users
	errMsg := err.Error()
	if got := errMsg; got == "" {
		t.Fatal("error message should not be empty")
	}

	if !strings.Contains(errMsg, "authentication required") {
		t.Errorf("error should mention 'authentication required', got: %q", errMsg)
	}

	if !strings.Contains(errMsg, "BEADS_RPC_NO_AUTH") {
		t.Errorf("error should mention BEADS_RPC_NO_AUTH env var, got: %q", errMsg)
	}
}
