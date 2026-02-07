package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/sqlite"
)

// TestAuthTokenValidRequest verifies that a client connected via TryConnect
// (which auto-loads the token file) can successfully perform operations.
func TestAuthTokenValidRequest(t *testing.T) {
	_, client, cleanup := setupTestServer(t)
	defer cleanup()

	// TryConnect auto-loads the auth token from the token file next to the socket.
	// A simple ping should succeed.
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping with valid auth token failed: %v", err)
	}

	// Also verify a data operation works
	args := &CreateArgs{
		Title:     "Auth test issue",
		IssueType: "task",
		Priority:  2,
	}
	resp, err := client.Create(args)
	if err != nil {
		t.Fatalf("Create with valid auth token failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("Create returned error: %s", resp.Error)
	}
}

// TestAuthTokenInvalidRequest verifies that a client with a corrupted
// auth token is rejected by the server.
func TestAuthTokenInvalidRequest(t *testing.T) {
	_, client, cleanup := setupTestServer(t)
	defer cleanup()

	// Corrupt the client's auth token
	client.SetAuthToken("invalid_token_that_does_not_match_server_secret_at_all_padding")

	_, err := client.Execute(OpPing, nil)
	if err == nil {
		t.Fatal("expected error with invalid auth token, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "authentication failed") {
		t.Errorf("expected 'authentication failed' in error, got: %q", errMsg)
	}
}

// TestAuthTokenMissingRequest verifies that a raw connection without any
// auth token is rejected with a helpful error message.
func TestAuthTokenMissingRequest(t *testing.T) {
	srv, _, cleanup := setupTestServer(t)
	defer cleanup()

	// Open a raw connection (bypassing TryConnect's token loading)
	conn := dialTestConn(t, srv.socketPath)
	defer conn.Close()

	// Send a request with no auth token
	req := Request{
		Operation: OpPing,
		AuthToken: "", // explicitly empty
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(append(data, '\n')); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.Success {
		t.Fatal("expected failure response for missing token, got success")
	}

	// Error should mention authentication and the env var workaround
	if !strings.Contains(resp.Error, "authentication required") {
		t.Errorf("error should mention 'authentication required', got: %q", resp.Error)
	}
	if !strings.Contains(resp.Error, "BEADS_RPC_NO_AUTH") {
		t.Errorf("error should mention BEADS_RPC_NO_AUTH, got: %q", resp.Error)
	}
}

// TestAuthTokenDisabled verifies that when BEADS_RPC_NO_AUTH=1 is set,
// the server does not create a token file and allows unauthenticated requests.
func TestAuthTokenDisabled(t *testing.T) {
	t.Setenv("BEADS_RPC_NO_AUTH", "1")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	dbPath := filepath.Join(beadsDir, "test.db")
	ctx := context.Background()

	store, err := sqlite.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)
	srv := NewServer(socketPath, store, tmpDir, dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	// Token file should NOT exist
	tokenPath := tokenFilePath(socketPath)
	if _, err := os.Stat(tokenPath); err == nil {
		t.Error("token file should not exist when BEADS_RPC_NO_AUTH=1")
	}

	// Server's authToken should be empty
	if srv.authToken != "" {
		t.Errorf("server authToken should be empty, got %q", srv.authToken)
	}

	// A raw connection without any token should work
	conn := dialTestConn(t, socketPath)
	defer conn.Close()

	req := Request{
		Operation: OpPing,
		AuthToken: "", // no token
	}
	data, _ := json.Marshal(req)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	conn.Write(append(data, '\n'))

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("ping should succeed with auth disabled, got error: %s", resp.Error)
	}
}

// TestAuthTokenFileCreatedOnStart verifies that the rpc-token file is
// created when the server starts and has secure permissions.
func TestAuthTokenFileCreatedOnStart(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	dbPath := filepath.Join(beadsDir, "test.db")
	ctx := context.Background()

	store, err := sqlite.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	socketPath := newTestSocketPath(t)
	srv := NewServer(socketPath, store, tmpDir, dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)
	defer srv.Stop()

	tokenPath := tokenFilePath(socketPath)

	// Token file should exist
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file should exist after server start: %v", err)
	}

	// Verify permissions are 0600
	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("token file permissions = %o, want 0600", perm)
		}
	}

	// Token file should contain a valid token
	token, err := readTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("failed to read token file: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64", len(token))
	}

	// Server's in-memory token should match the file
	if srv.authToken != token {
		t.Error("server in-memory token does not match token file")
	}
}

// TestAuthTokenFileCleanedOnStop verifies that the rpc-token file is
// removed when the server stops.
func TestAuthTokenFileCleanedOnStop(t *testing.T) {
	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	dbPath := filepath.Join(beadsDir, "test.db")
	ctx := context.Background()

	store, err := sqlite.New(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	// Note: don't defer store.Close() here because srv.Stop() closes it

	socketPath := newTestSocketPath(t)
	srv := NewServer(socketPath, store, tmpDir, dbPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	tokenPath := tokenFilePath(socketPath)

	// Verify token file exists before stop
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("token file should exist before stop: %v", err)
	}

	// Stop the server
	if err := srv.Stop(); err != nil {
		t.Fatalf("srv.Stop() error: %v", err)
	}

	// Token file should be removed after stop
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Error("token file should be removed after server stop")
	}
}
