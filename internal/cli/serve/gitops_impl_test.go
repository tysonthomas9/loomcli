//go:build ignore

package serve

import (
	"testing"
)

// --- resolveWorkspaceConfigName tests ---

func TestResolveWorkspaceConfigName_DirectNameMatch(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "dev" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want %q", got, "dev")
	}
}

func TestResolveWorkspaceConfigName_UUIDMatch(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"staging": {ID: "uuid-staging-123", Path: "/tmp/staging"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "uuid-staging-123")
	if got != "staging" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'uuid-staging-123') = %q, want %q", got, "staging")
	}
}

func TestResolveWorkspaceConfigName_EmptyString(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, '') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_UnknownID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}

	got := resolveWorkspaceConfigName(cfg, "nonexistent-id")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'nonexistent-id') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilConfig(t *testing.T) {
	got := resolveWorkspaceConfigName(nil, "anything")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(nil, 'anything') = %q, want empty", got)
	}
}

func TestResolveWorkspaceConfigName_NilWorkspacesMap(t *testing.T) {
	cfg := &LoomConfig{}

	got := resolveWorkspaceConfigName(cfg, "dev")
	if got != "" {
		t.Errorf("resolveWorkspaceConfigName(cfg, 'dev') = %q, want empty", got)
	}
}

// --- scopeResolverToWorkspace tests ---

func TestScopeResolverToWorkspace_EmptyWorkspaceID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Workspace should remain unchanged (no-op).
	if resolver.WorkspaceName() != "dev" {
		t.Errorf("workspace = %q, want %q (should be unchanged)", resolver.WorkspaceName(), "dev")
	}
}

func TestScopeResolverToWorkspace_LegacyMode(t *testing.T) {
	resolver := &Resolver{
		mode: ModeLegacy,
	}

	err := scopeResolverToWorkspace(resolver, "some-ws")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Legacy mode: workspace should remain empty (no-op).
	if resolver.WorkspaceName() != "" {
		t.Errorf("workspace = %q, want empty (legacy mode no-op)", resolver.WorkspaceName())
	}
}

func TestScopeResolverToWorkspace_ValidWorkspaceName(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging", Path: "/tmp/staging"},
		},
	}
	resolver := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_ValidUUID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev":     {ID: "uuid-dev", Path: "/tmp/dev"},
			"staging": {ID: "uuid-staging-456", Path: "/tmp/staging"},
		},
	}
	resolver := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "uuid-staging-456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolver.WorkspaceName() != "staging" {
		t.Errorf("workspace = %q, want %q", resolver.WorkspaceName(), "staging")
	}
}

func TestScopeResolverToWorkspace_UnknownWorkspaceID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"dev": {ID: "uuid-dev", Path: "/tmp/dev"},
		},
	}
	resolver := &Resolver{
		mode:      ModeWorkspace,
		config:    cfg,
		workspace: "dev",
	}

	err := scopeResolverToWorkspace(resolver, "nonexistent-ws")
	if err == nil {
		t.Fatal("expected error for unknown workspace ID, got nil")
	}
}
