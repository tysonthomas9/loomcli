package driver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveProcessNodePath(t *testing.T) {
	t.Run("explicit override wins", func(t *testing.T) {
		got := resolveProcessNodePath(" /operator/node ", filepath.Join(t.TempDir(), "loom"))
		if got != "/operator/node" {
			t.Fatalf("resolveProcessNodePath() = %q, want explicit override", got)
		}
	})

	t.Run("packaged resource wins before legacy sibling and PATH", func(t *testing.T) {
		dir := t.TempDir()
		executable := filepath.Join(dir, "Contents", "MacOS", "loom")
		node := filepath.Join(dir, "Contents", "Resources", "runtime", "node")
		if err := os.MkdirAll(filepath.Dir(node), 0o755); err != nil {
			t.Fatalf("create packaged runtime dir: %v", err)
		}
		if err := os.WriteFile(node, []byte("packaged node"), 0o755); err != nil {
			t.Fatalf("write packaged node: %v", err)
		}
		legacyNode := filepath.Join(filepath.Dir(executable), "node")
		if err := os.MkdirAll(filepath.Dir(legacyNode), 0o755); err != nil {
			t.Fatalf("create executable dir: %v", err)
		}
		if err := os.WriteFile(legacyNode, []byte("legacy node"), 0o755); err != nil {
			t.Fatalf("write legacy node: %v", err)
		}
		got := resolveProcessNodePath("", executable)
		if got != node {
			t.Fatalf("resolveProcessNodePath() = %q, want packaged resource %q", got, node)
		}
	})

	t.Run("legacy packaged sibling remains compatible", func(t *testing.T) {
		dir := t.TempDir()
		node := filepath.Join(dir, "node")
		if err := os.WriteFile(node, []byte("legacy packaged node"), 0o755); err != nil {
			t.Fatalf("write legacy packaged node: %v", err)
		}
		got := resolveProcessNodePath("", filepath.Join(dir, "loom"))
		if got != node {
			t.Fatalf("resolveProcessNodePath() = %q, want legacy sibling %q", got, node)
		}
	})

	t.Run("non executable sibling falls back to PATH", func(t *testing.T) {
		dir := t.TempDir()
		node := filepath.Join(dir, "node")
		mode := os.FileMode(0o644)
		if runtime.GOOS == "windows" {
			mode = 0o755
		}
		if err := os.WriteFile(node, []byte("not a runtime"), mode); err != nil {
			t.Fatalf("write node candidate: %v", err)
		}
		if runtime.GOOS == "windows" {
			if err := os.Remove(node); err != nil {
				t.Fatalf("remove Windows candidate: %v", err)
			}
		}
		if got := resolveProcessNodePath("", filepath.Join(dir, "loom")); got != "node" {
			t.Fatalf("resolveProcessNodePath() = %q, want PATH fallback", got)
		}
	})
}
