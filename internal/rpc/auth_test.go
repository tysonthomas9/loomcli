package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		socketPath string
		want       string
	}{
		{
			name:       "standard beads path",
			socketPath: "/home/user/project/.beads/bd.sock",
			want:       "/home/user/project/.beads/rpc-token",
		},
		{
			name:       "tmp path",
			socketPath: "/tmp/beads-abc123/bd.sock",
			want:       "/tmp/beads-abc123/rpc-token",
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

func TestLoadAuthToken(t *testing.T) {
	t.Parallel()

	t.Run("file does not exist", func(t *testing.T) {
		t.Parallel()
		token := loadAuthToken("/nonexistent/path/bd.sock")
		if token != "" {
			t.Errorf("loadAuthToken() = %q, want empty string for missing file", token)
		}
	})

	t.Run("file exists with token", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		socketPath := filepath.Join(dir, "bd.sock")
		tokenPath := filepath.Join(dir, tokenFileName)

		if err := os.WriteFile(tokenPath, []byte("secret-token-123"), 0600); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		token := loadAuthToken(socketPath)
		if token != "secret-token-123" {
			t.Errorf("loadAuthToken() = %q, want %q", token, "secret-token-123")
		}
	})

	t.Run("file with whitespace trimmed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		socketPath := filepath.Join(dir, "bd.sock")
		tokenPath := filepath.Join(dir, tokenFileName)

		if err := os.WriteFile(tokenPath, []byte("  my-token  \n"), 0600); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		token := loadAuthToken(socketPath)
		if token != "my-token" {
			t.Errorf("loadAuthToken() = %q, want %q", token, "my-token")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		socketPath := filepath.Join(dir, "bd.sock")
		tokenPath := filepath.Join(dir, tokenFileName)

		if err := os.WriteFile(tokenPath, []byte(""), 0600); err != nil {
			t.Fatalf("WriteFile() error: %v", err)
		}

		token := loadAuthToken(socketPath)
		if token != "" {
			t.Errorf("loadAuthToken() = %q, want empty string for empty file", token)
		}
	})
}
