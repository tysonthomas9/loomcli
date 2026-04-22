package config

import (
	"sync"
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

func TestConfigCache_ExpiresOnStaleMarker(t *testing.T) {
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

	// Force the cache to appear stale relative to the real file mtime.
	expireCachedConfigForTest()

	got2, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() after expire error = %v", err)
	}
	if got1 == got2 {
		t.Error("LoadConfigCached() returned same pointer after expire hook — cache not refreshed")
	}
}

// A concurrent reader and writer must not deadlock. The prior mutex-based
// cache took an in-process write lock inside LoadConfigCached while waiting
// for the config file flock; InvalidateConfigCache wanted the same write
// lock from inside the flock. That AB-BA deadlocked the server. The
// 5 s deadline catches a regression of that lock inversion.
func TestLoadConfigCached_ConcurrentReaderAndWriterNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	// Seed a config so LoadConfig has something to parse.
	seed := &LoomConfig{Workspaces: map[string]WorkspaceConfig{"seed": {Path: "/tmp/seed"}}}
	if err := SaveConfig(seed); err != nil {
		t.Fatalf("SaveConfig(seed) error = %v", err)
	}
	InvalidateConfigCache()

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer: acquire flock via WithConfigLock, save (which invalidates the
	// cache). This is the path SaveConfigUnlocked takes during workspace
	// creation.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			err := WithConfigLock(func() error {
				return SaveConfigUnlocked(&LoomConfig{
					Workspaces: map[string]WorkspaceConfig{
						"w": {Path: "/tmp/w"},
					},
				})
			})
			if err != nil {
				t.Errorf("writer: WithConfigLock error = %v", err)
				return
			}
		}
	}()

	// Reader: spin calling LoadConfigCached. Before the redesign this would
	// race the writer on the cache mutex while holding it across LoadConfig's
	// flock acquisition.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if _, err := LoadConfigCached(); err != nil {
				t.Errorf("reader: LoadConfigCached error = %v", err)
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed cleanly.
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent reader+writer deadlocked after 5s — cache lock inversion regressed")
	}
}
