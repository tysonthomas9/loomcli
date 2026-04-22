package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// writeConfig writes the given YAML under $LOOM_CONFIG_DIR/config.yaml for
// a PatchRepoDefaultBranch test to read and mutate.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestPatchRepoDefaultBranch_Success(t *testing.T) {
	path := writeConfig(t, `version: 1
default_workspace: alpha
workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: backend
        path: ./backend
        default_branch: main
        remote: origin
        groups: [svc]
      - name: frontend
        path: ./frontend
        default_branch: main
`)

	svc := &workspaceServiceImpl{} // configFn nil → refresh returns nil; that's fine

	_, err := svc.PatchRepoDefaultBranch(context.Background(), "ws-1", "backend", "develop")
	if err != nil {
		t.Fatalf("PatchRepoDefaultBranch: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var got loomConfigForRepoBranch
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("parse saved config: %v", err)
	}
	ws, ok := got.Workspaces["alpha"]
	if !ok {
		t.Fatal("workspace alpha missing from saved config")
	}
	if len(ws.Repos) != 2 {
		t.Fatalf("repos: got %d, want 2", len(ws.Repos))
	}

	var backend, frontend repoForBranch
	for _, r := range ws.Repos {
		switch r.Name {
		case "backend":
			backend = r
		case "frontend":
			frontend = r
		}
	}

	if backend.DefaultBranch != "develop" {
		t.Errorf("backend.default_branch = %q, want develop", backend.DefaultBranch)
	}
	if backend.Path != "./backend" {
		t.Errorf("backend.path lost round-trip: %q", backend.Path)
	}
	if backend.Remote != "origin" {
		t.Errorf("backend.remote lost round-trip: %q", backend.Remote)
	}
	if len(backend.Groups) != 1 || backend.Groups[0] != "svc" {
		t.Errorf("backend.groups lost round-trip: %v", backend.Groups)
	}

	// Sibling repo must not be touched.
	if frontend.DefaultBranch != "main" {
		t.Errorf("frontend.default_branch = %q, want main (unmodified)", frontend.DefaultBranch)
	}

	// Top-level fields round-trip.
	if got.Version != 1 {
		t.Errorf("version lost round-trip: %d", got.Version)
	}
	if got.DefaultWorkspace != "alpha" {
		t.Errorf("default_workspace lost round-trip: %q", got.DefaultWorkspace)
	}
}

func TestPatchRepoDefaultBranch_EmptyBranch(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: backend
        path: ./backend
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.PatchRepoDefaultBranch(context.Background(), "ws-1", "backend", "")
	if err == nil {
		t.Fatal("want error for empty branch, got nil")
	}
	se, ok := err.(*ServiceError)
	if !ok || se.Kind != KindValidation {
		t.Errorf("want validation error, got %#v", err)
	}
}

func TestPatchRepoDefaultBranch_UnknownWorkspace(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: backend
        path: ./backend
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.PatchRepoDefaultBranch(context.Background(), "nope", "backend", "main")
	if err == nil {
		t.Fatal("want error for unknown workspace, got nil")
	}
	se, ok := err.(*ServiceError)
	if !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found error, got %#v", err)
	}
}

func TestPatchRepoDefaultBranch_UnknownRepo(t *testing.T) {
	writeConfig(t, `workspaces:
  alpha:
    id: ws-1
    path: /tmp/alpha
    repos:
      - name: backend
        path: ./backend
`)
	svc := &workspaceServiceImpl{}
	_, err := svc.PatchRepoDefaultBranch(context.Background(), "ws-1", "missing", "main")
	if err == nil {
		t.Fatal("want error for unknown repo, got nil")
	}
	se, ok := err.(*ServiceError)
	if !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found error, got %#v", err)
	}
}

func TestPatchRepoDefaultBranch_PreservesSiblingWorkspaceWithEmptyRepos(t *testing.T) {
	// Regression: loomWorkspaceForRepoBranch.Repos must not be ,omitempty, or
	// sibling workspaces with empty repos lose the `repos:` key on save.
	path := writeConfig(t, `workspaces:
  alpha:
    id: ws-alpha
    path: /tmp/alpha
    repos:
      - name: backend
        path: ./backend
  beta:
    id: ws-beta
    path: /tmp/beta
    repos: []
`)

	svc := &workspaceServiceImpl{}
	if _, err := svc.PatchRepoDefaultBranch(context.Background(), "ws-alpha", "backend", "develop"); err != nil {
		t.Fatalf("PatchRepoDefaultBranch: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	// beta must still carry the `repos:` key (even if null/empty) to match what
	// config.LoadConfig-then-save would produce.
	if !bytes.Contains(raw, []byte("beta:")) {
		t.Fatal("beta workspace lost on save")
	}
	if !bytes.Contains(raw, []byte("repos:")) {
		t.Fatalf("repos key dropped on save:\n%s", raw)
	}
}

func TestPatchRepoDefaultBranch_NoConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir) // no config.yaml written
	svc := &workspaceServiceImpl{}
	_, err := svc.PatchRepoDefaultBranch(context.Background(), "ws-1", "backend", "main")
	if err == nil {
		t.Fatal("want error when config is missing, got nil")
	}
	se, ok := err.(*ServiceError)
	if !ok || se.Kind != KindNotFound {
		t.Errorf("want not-found error, got %#v", err)
	}
}
