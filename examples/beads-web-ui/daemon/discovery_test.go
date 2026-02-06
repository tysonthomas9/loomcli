package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeSocketPath(t *testing.T) {
	tests := []struct {
		name          string
		workspacePath string
		wantErr       error
	}{
		{
			name:          "valid workspace path",
			workspacePath: "/home/user/project",
			wantErr:       nil,
		},
		{
			name:          "empty workspace path",
			workspacePath: "",
			wantErr:       ErrInvalidSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			socketPath, err := ComputeSocketPath(tt.workspacePath)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("ComputeSocketPath() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ComputeSocketPath() unexpected error = %v", err)
				return
			}

			// Verify the socket path is non-empty
			if socketPath == "" {
				t.Error("ComputeSocketPath() returned empty path")
			}
		})
	}
}

func TestDiscoverSocketPath(t *testing.T) {
	tests := []struct {
		name          string
		workspacePath string
		wantErr       error
	}{
		{
			name:          "empty workspace path returns error",
			workspacePath: "",
			wantErr:       ErrInvalidSocketPath,
		},
		// Note: Can't test actual daemon discovery without a running daemon
		// so we only test the error cases here
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DiscoverSocketPath(tt.workspacePath)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("DiscoverSocketPath() error = %v, wantErr %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestDiscoverSocketPath_NoDaemon(t *testing.T) {
	// Create a temporary directory that definitely has no daemon
	tmpDir, err := os.MkdirTemp("", "beads-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// DiscoverSocketPath should return ErrDaemonNotRunning for a workspace with no daemon
	_, err = DiscoverSocketPath(tmpDir)
	if err != ErrDaemonNotRunning {
		t.Errorf("DiscoverSocketPath() for no-daemon dir error = %v, wantErr %v", err, ErrDaemonNotRunning)
	}
}

func TestFindBeadsDir(t *testing.T) {
	// Create a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "beads-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .beads directory
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.Mkdir(beadsDir, 0755); err != nil {
		t.Fatalf("failed to create .beads dir: %v", err)
	}

	// Create nested subdirectory
	nestedDir := filepath.Join(tmpDir, "src", "pkg")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	tests := []struct {
		name          string
		workspacePath string
		wantDir       string
		wantErr       error
	}{
		{
			name:          "find .beads at workspace root",
			workspacePath: tmpDir,
			wantDir:       beadsDir,
			wantErr:       nil,
		},
		{
			name:          "find .beads from nested directory",
			workspacePath: nestedDir,
			wantDir:       beadsDir,
			wantErr:       nil,
		},
		{
			name:          "empty path returns error",
			workspacePath: "",
			wantDir:       "",
			wantErr:       ErrInvalidSocketPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			foundDir, err := FindBeadsDir(tt.workspacePath)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("FindBeadsDir() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("FindBeadsDir() unexpected error = %v", err)
				return
			}

			if foundDir != tt.wantDir {
				t.Errorf("FindBeadsDir() = %v, want %v", foundDir, tt.wantDir)
			}
		})
	}
}

func TestFindBeadsDir_NotFound(t *testing.T) {
	// Create a temporary directory without .beads
	tmpDir, err := os.MkdirTemp("", "beads-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	_, err = FindBeadsDir(tmpDir)
	if err != ErrInvalidSocketPath {
		t.Errorf("FindBeadsDir() for no-.beads dir error = %v, wantErr %v", err, ErrInvalidSocketPath)
	}
}

func TestDiscoverSocketPathFromEnv(t *testing.T) {
	// Test with no BEADS_SOCKET set and no discoverable daemon
	// Clear any existing env var
	originalValue := os.Getenv("BEADS_SOCKET")
	os.Unsetenv("BEADS_SOCKET")
	defer func() {
		if originalValue != "" {
			os.Setenv("BEADS_SOCKET", originalValue)
		}
	}()

	// This will fail to discover since no daemon is running
	// The exact error depends on whether current dir has a daemon
	_, err := DiscoverSocketPathFromEnv()
	if err == nil {
		// If it succeeded, that's fine (maybe a daemon is actually running)
		// but we can't really test this case without mocking
		t.Skip("Skipping - daemon may be running")
	}
}

func TestDiscoverSocketPathFromEnv_WithEnvVar(t *testing.T) {
	// Create a temporary socket file to test BEADS_SOCKET env var
	tmpDir, err := os.MkdirTemp("", "beads-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	socketPath := filepath.Join(tmpDir, "test.sock")
	// Create the socket file (just a regular file for testing)
	if err := os.WriteFile(socketPath, []byte{}, 0600); err != nil {
		t.Fatalf("failed to create socket file: %v", err)
	}

	// Save and restore original value
	originalValue := os.Getenv("BEADS_SOCKET")
	defer func() {
		if originalValue != "" {
			os.Setenv("BEADS_SOCKET", originalValue)
		} else {
			os.Unsetenv("BEADS_SOCKET")
		}
	}()

	// Set the env var
	os.Setenv("BEADS_SOCKET", socketPath)

	discoveredPath, err := DiscoverSocketPathFromEnv()
	if err != nil {
		t.Errorf("DiscoverSocketPathFromEnv() unexpected error = %v", err)
		return
	}

	if discoveredPath != socketPath {
		t.Errorf("DiscoverSocketPathFromEnv() = %v, want %v", discoveredPath, socketPath)
	}
}

func TestDiscoverSocketPathFromEnv_NonexistentPath(t *testing.T) {
	// Save and restore original value
	originalValue := os.Getenv("BEADS_SOCKET")
	defer func() {
		if originalValue != "" {
			os.Setenv("BEADS_SOCKET", originalValue)
		} else {
			os.Unsetenv("BEADS_SOCKET")
		}
	}()

	// Set env var to non-existent path
	os.Setenv("BEADS_SOCKET", "/nonexistent/path/bd.sock")

	_, err := DiscoverSocketPathFromEnv()
	if err != ErrDaemonNotRunning {
		t.Errorf("DiscoverSocketPathFromEnv() with nonexistent path error = %v, wantErr %v", err, ErrDaemonNotRunning)
	}
}
