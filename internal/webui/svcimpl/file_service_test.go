package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestFileServiceListReadAndWrite(t *testing.T) {
	root := resolvedTempDir(t)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "b.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	svc := NewFileService(fakeFileOps{wt: &ops.AgentWorktree{Name: "agent", Path: root}})
	tree, err := svc.ListDirectory(context.Background(), "WS", "agent", "")
	if err != nil {
		t.Fatalf("ListDirectory: %v", err)
	}
	if tree.Path != "." || len(tree.Entries) != 3 || tree.Entries[0].Name != "dir" {
		t.Fatalf("tree = %+v", tree)
	}
	text, err := svc.ReadFile(context.Background(), "WS", "agent", "a.txt")
	if err != nil {
		t.Fatalf("ReadFile text: %v", err)
	}
	if text.Content != "alpha" || text.Binary {
		t.Fatalf("text result = %+v", text)
	}
	bin, err := svc.ReadFile(context.Background(), "WS", "agent", "bin.dat")
	if err != nil {
		t.Fatalf("ReadFile binary: %v", err)
	}
	if !bin.Binary || bin.Content != "" {
		t.Fatalf("binary result = %+v", bin)
	}
	if err := svc.WriteFile(context.Background(), "WS", "agent", "dir/new.txt", "new content"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "dir", "new.txt"))
	if err != nil || string(data) != "new content" {
		t.Fatalf("written data = %q err=%v", string(data), err)
	}
}

func TestFileServiceValidationErrors(t *testing.T) {
	root := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write denied: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "file.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "real-parent"), 0o755); err != nil {
		t.Fatalf("mkdir real parent: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "real-parent"), filepath.Join(root, "link-parent")); err != nil {
		t.Fatalf("symlink parent: %v", err)
	}
	svc := NewFileService(fakeFileOps{wt: &ops.AgentWorktree{Name: "agent", Path: root}})

	cases := []struct {
		name string
		call func() error
		want string
	}{
		{"bad agent", func() error {
			_, err := svc.ListDirectory(context.Background(), "WS", "../bad", "")
			return err
		}, "invalid agent"},
		{"missing agent", func() error {
			_, err := NewFileService(fakeFileOps{err: errors.New("missing")}).ReadFile(context.Background(), "WS", "agent", "file.txt")
			return err
		}, "not found"},
		{"list file", func() error {
			_, err := svc.ListDirectory(context.Background(), "WS", "agent", "file.txt")
			return err
		}, "not a directory"},
		{"list symlink", func() error {
			_, err := svc.ListDirectory(context.Background(), "WS", "agent", "link.txt")
			return err
		}, "symlink"},
		{"missing read path", func() error {
			_, err := svc.ReadFile(context.Background(), "WS", "agent", "")
			return err
		}, "required"},
		{"denied read", func() error {
			_, err := svc.ReadFile(context.Background(), "WS", "agent", ".env")
			return err
		}, "denied"},
		{"read directory", func() error {
			_, err := svc.ReadFile(context.Background(), "WS", "agent", ".")
			return err
		}, "directory"},
		{"read missing", func() error {
			_, err := svc.ReadFile(context.Background(), "WS", "agent", "missing.txt")
			return err
		}, "not found"},
		{"read symlink", func() error {
			_, err := svc.ReadFile(context.Background(), "WS", "agent", "link.txt")
			return err
		}, "symlink"},
		{"write missing path", func() error {
			return svc.WriteFile(context.Background(), "WS", "agent", "", "x")
		}, "required"},
		{"write denied", func() error {
			return svc.WriteFile(context.Background(), "WS", "agent", "id_rsa", "x")
		}, "denied"},
		{"write missing parent", func() error {
			return svc.WriteFile(context.Background(), "WS", "agent", "missing/new.txt", "x")
		}, "parent directory"},
		{"write symlink parent", func() error {
			return svc.WriteFile(context.Background(), "WS", "agent", "link-parent/new.txt", "x")
		}, "symlink"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestFileServiceDirectValidationBranches(t *testing.T) {
	root := resolvedTempDir(t)
	large := filepath.Join(root, "large.txt")
	f, err := os.Create(large)
	if err != nil {
		t.Fatalf("create large: %v", err)
	}
	if err := f.Truncate(maxRequestBody + 1); err != nil {
		_ = f.Close()
		t.Fatalf("truncate large: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close large: %v", err)
	}
	if _, err := validateFilePath(large); err == nil || !strings.Contains(strings.ToLower(err.Error()), "too large") {
		t.Fatalf("large validate err = %v", err)
	}

	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "target-link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readFileContent(link, root); err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("readFileContent symlink err = %v", err)
	}
}

func TestFileServiceAdditionalErrorBranches(t *testing.T) {
	root := resolvedTempDir(t)
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-target"), filepath.Join(root, "dangling")); err != nil {
		t.Fatalf("symlink dangling: %v", err)
	}

	svc := NewFileService(fakeFileOps{wt: &ops.AgentWorktree{Name: "agent", Path: root}})
	if _, err := svc.ListDirectory(context.Background(), "WS", "agent", "missing"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("missing directory err = %v", err)
	}
	if _, err := svc.ListDirectory(context.Background(), "WS", "agent", "../outside"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("cleaned missing directory err = %v", err)
	}
	if _, err := svc.ReadFile(context.Background(), "WS", "agent", "../outside"); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("cleaned missing read err = %v", err)
	}
	if _, err := validateFilePath(filepath.Join(root, "dangling")); err == nil ||
		!strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("dangling symlink validate err = %v", err)
	}
}

type fakeFileOps struct {
	wt  *ops.AgentWorktree
	err error
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

func (f fakeFileOps) ResolveAgentWorktree(_, _ string) (*ops.AgentWorktree, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.wt, nil
}
