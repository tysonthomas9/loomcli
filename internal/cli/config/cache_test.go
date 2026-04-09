package config

import (
	"testing"
	"time"
)

func TestLoadConfigCached_ReturnsCachedValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// First call populates cache.
	got1, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() error = %v", err)
	}
	if got1 == nil || len(got1.Workspaces) != 1 {
		t.Fatalf("LoadConfigCached() = %v, want 1 workspace", got1)
	}

	// Second call within TTL should return the same pointer (cached).
	got2, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() second call error = %v", err)
	}
	if got1 != got2 {
		t.Error("LoadConfigCached() returned different pointer within TTL — cache miss")
	}
}

func TestSaveConfig_InvalidatesCache(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	// Save initial config and populate cache.
	cfg1 := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
		},
	}
	if err := SaveConfig(cfg1); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	got1, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() error = %v", err)
	}
	if len(got1.Workspaces) != 1 {
		t.Fatalf("got %d workspaces, want 1", len(got1.Workspaces))
	}

	// Save updated config — should invalidate cache.
	cfg2 := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
			"ws2": {Path: "/tmp/ws2"},
		},
	}
	if err := SaveConfig(cfg2); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	// Next LoadConfigCached should return the updated config, not the stale cache.
	got2, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() after SaveConfig error = %v", err)
	}
	if len(got2.Workspaces) != 2 {
		t.Errorf("LoadConfigCached() after SaveConfig got %d workspaces, want 2", len(got2.Workspaces))
	}
}

func TestInvalidateConfigCache_ForcesReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got1, _ := LoadConfigCached()

	// Manually invalidate.
	InvalidateConfigCache()

	// Should reload from disk (different pointer).
	got2, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() after invalidate error = %v", err)
	}
	if got1 == got2 {
		t.Error("LoadConfigCached() returned same pointer after InvalidateConfigCache — cache not cleared")
	}
}

func TestConfigCache_ExpiresByTTL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {Path: "/tmp/ws1"},
		},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	got1, _ := LoadConfigCached()

	// Force cache to expire by setting expires to the past.
	configCache.Lock()
	configCache.expires = time.Now().Add(-1 * time.Second)
	configCache.Unlock()

	got2, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() after TTL error = %v", err)
	}
	if got1 == got2 {
		t.Error("LoadConfigCached() returned same pointer after TTL expiry — cache not refreshed")
	}
}
