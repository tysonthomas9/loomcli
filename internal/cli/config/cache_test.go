package config

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLoadConfigCached_ReturnsCachedValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	// Save a v2-tagged config so autoMigrateFile takes the early-return path
	// during LoadConfigCached. Otherwise the auto-migration on the first load
	// triggers an in-flight InvalidateConfigCache that LoadConfigCached
	// (correctly) refuses to seal under, producing a fresh pointer on the
	// next call.
	cfg := &LoomConfig{
		Version: CurrentConfigVersion,
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

// TestLoadConfigCached_NoDeadlockWithWriter is the primary regression guard
// for the workspace-create-vs-monitor-poll AB-BA deadlock documented in
// loomcli-rc1s2. It drives 4 writers (mirroring CreateWorkspace ->
// finalizeWorkspace: WithConfigLock { LoadConfigUnlocked -> mutate ->
// SaveConfigUnlocked }) and 8 readers (mirroring monitor/HandleAgents ->
// NewResolver -> LoadConfigCached) concurrently for ~2s. If any lock holder
// re-introduces the cycle (e.g., taking a cache-level lock across the config
// flock), this test hangs — the 10s hard deadline fails it.
func TestLoadConfigCached_NoDeadlockWithWriter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	InvalidateConfigCache()

	// Seed the file at CurrentConfigVersion so the writer path doesn't keep
	// re-triggering autoMigrateFile's InvalidateConfigCache side-effect (see
	// config_migrate.go:182) — migration churn would add noise without
	// testing the deadlock.
	seed := &LoomConfig{
		Version:    CurrentConfigVersion,
		Workspaces: map[string]WorkspaceConfig{"seed": {Path: "/tmp/seed"}},
	}
	if err := SaveConfig(seed); err != nil {
		t.Fatalf("SaveConfig(seed) error = %v", err)
	}
	InvalidateConfigCache()

	const numWriters = 4
	const numReaders = 8
	const runFor = 2 * time.Second
	const hardTimeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), runFor)
	defer cancel()

	var (
		wg    sync.WaitGroup
		errMu sync.Mutex
		errs  []error
	)
	recordErr := func(err error) {
		errMu.Lock()
		errs = append(errs, err)
		errMu.Unlock()
	}

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			iter := 0
			for ctx.Err() == nil {
				err := WithConfigLock(func() error {
					cfg, err := LoadConfigUnlocked()
					if err != nil {
						return fmt.Errorf("writer %d: LoadConfigUnlocked: %w", idx, err)
					}
					if cfg == nil {
						cfg = &LoomConfig{
							Version:    CurrentConfigVersion,
							Workspaces: map[string]WorkspaceConfig{},
						}
					}
					if cfg.Workspaces == nil {
						cfg.Workspaces = map[string]WorkspaceConfig{}
					}
					name := fmt.Sprintf("w%d-%d", idx, iter)
					cfg.Workspaces[name] = WorkspaceConfig{Path: "/tmp/" + name}
					if err := SaveConfigUnlocked(cfg); err != nil {
						return fmt.Errorf("writer %d: SaveConfigUnlocked: %w", idx, err)
					}
					return nil
				})
				if err != nil {
					recordErr(err)
					return
				}
				iter++
			}
		}(i)
	}

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for ctx.Err() == nil {
				if _, err := LoadConfigCached(); err != nil {
					recordErr(fmt.Errorf("reader %d: LoadConfigCached: %w", idx, err))
					return
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Clean finish.
	case <-time.After(hardTimeout):
		// Do NOT wg.Wait() here — workers are likely stuck on a lock.
		// Letting the go test -timeout mechanism produce a goroutine dump
		// gives the best diagnostic for a deadlock regression.
		t.Fatalf("deadlock: workers did not finish within %s (lock-order invariant regressed — see cache.go header)", hardTimeout)
	}

	errMu.Lock()
	defer errMu.Unlock()
	for _, err := range errs {
		t.Errorf("worker error: %v", err)
	}
}
