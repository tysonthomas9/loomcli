//go:build ignore

package cli

import (
	"testing"
)

func TestResolveActiveWorkspace_WithDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "myws",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: "/tmp/myws",
				Repos: []RepoConfig{
					{Name: "api", Path: "/tmp/myws/api"},
				},
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		t.Fatalf("ResolveActiveWorkspace() error = %v", err)
	}
	if ws == nil {
		t.Fatal("ResolveActiveWorkspace() returned nil")
	}
	if ws.Path != "/tmp/myws" {
		t.Errorf("workspace.Path = %q, want %q", ws.Path, "/tmp/myws")
	}
}

func TestResolveActiveWorkspace_DefaultNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "nonexistent",
		Workspaces: map[string]WorkspaceConfig{
			"myws": {
				Path: "/tmp/myws",
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	_, err := ResolveActiveWorkspace()
	if err == nil {
		t.Error("ResolveActiveWorkspace() should error when default workspace not found")
	}
}

func TestResolveActiveWorkspace_NoDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		DefaultWorkspace: "",
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path: "/tmp/ws1",
			},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		t.Fatalf("ResolveActiveWorkspace() error = %v", err)
	}
	if ws == nil {
		t.Fatal("ResolveActiveWorkspace() should return first workspace when no default")
	}
}

func TestResolveActiveWorkspace_Empty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		t.Fatalf("ResolveActiveWorkspace() error = %v", err)
	}
	if ws != nil {
		t.Error("ResolveActiveWorkspace() should return nil for empty workspaces")
	}
}

func TestIsWorkspaceMode_True(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if !IsWorkspaceMode() {
		t.Error("IsWorkspaceMode() should return true when workspaces exist")
	}
}

func TestIsWorkspaceMode_False(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)

	// No config file exists
	if IsWorkspaceMode() {
		t.Error("IsWorkspaceMode() should return false when no config exists")
	}
}
