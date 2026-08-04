package config

import (
	"testing"
	"time"
)

func TestLoadConfigCached_ReturnsCachedValue(t *testing.T) {
	InvalidateConfigCache()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	want := &LoomConfig{Workspaces: map[string]WorkspaceConfig{"WS1": {Path: "/tmp/ws1"}}}

	configCache.Lock()
	configCache.cfg = want
	configCache.err = nil
	configCache.expires = time.Now().Add(time.Minute)
	configCache.dir = dir
	configCache.Unlock()

	got, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached() error = %v", err)
	}
	if got != want {
		t.Fatalf("LoadConfigCached() returned %p, want cached pointer %p", got, want)
	}
}

func TestLoadConfigCached_IgnoresCachedValueForDifferentConfigDir(t *testing.T) {
	InvalidateConfigCache()
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:1")

	oldDir := t.TempDir()
	newDir := t.TempDir()
	want := &LoomConfig{Workspaces: map[string]WorkspaceConfig{"OLD": {Path: "/tmp/old"}}}

	configCache.Lock()
	configCache.cfg = want
	configCache.err = nil
	configCache.expires = time.Now().Add(time.Minute)
	configCache.dir = oldDir
	configCache.Unlock()

	t.Setenv("LOOM_CONFIG_DIR", newDir)
	got, err := LoadConfigCached()
	if err == nil && got == want {
		t.Fatal("LoadConfigCached() returned cached config from a different LOOM_CONFIG_DIR")
	}
}

func TestInvalidateConfigCache_ClearsCachedValue(t *testing.T) {
	configCache.Lock()
	configCache.cfg = &LoomConfig{}
	configCache.err = nil
	configCache.expires = time.Now().Add(time.Minute)
	configCache.dir = t.TempDir()
	configCache.Unlock()

	InvalidateConfigCache()

	configCache.RLock()
	defer configCache.RUnlock()
	if configCache.cfg != nil {
		t.Fatalf("configCache.cfg = %v, want nil", configCache.cfg)
	}
	if !configCache.expires.IsZero() {
		t.Fatalf("configCache.expires = %v, want zero", configCache.expires)
	}
	if configCache.dir != "" {
		t.Fatalf("configCache.dir = %q, want empty", configCache.dir)
	}
}
