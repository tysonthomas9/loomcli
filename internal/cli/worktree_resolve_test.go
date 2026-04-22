package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestGetDefaultResolver_RefreshesOnConfigChange verifies that editing the
// config file on disk causes the next GetDefaultResolver call to rebuild from
// the fresh config, rather than returning a stale cached instance.
//
// Regression test for loomcli-r3ddn.5: without mtime-based invalidation,
// /api/monitor/agents would return stale worktree lists after a user edited
// ~/.loom/config.yaml, until the loom serve process was restarted.
func TestGetDefaultResolver_RefreshesOnConfigChange(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	cfgPath := filepath.Join(configDir, "config.yaml")

	initialCfg := []byte(`version: 2
default_workspace: alpha
workspaces:
  alpha:
    path: /tmp/ws-alpha
    repos:
      - name: repo1
        path: /tmp/ws-alpha/repo1
`)
	if err := os.WriteFile(cfgPath, initialCfg, 0644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	old := TestingResetDefaultResolver()
	t.Cleanup(func() { TestingSetDefaultResolver(old) })

	first := GetDefaultResolver()
	if first.Mode != ModeWorkspace {
		t.Fatalf("expected workspace mode, got %d", first.Mode)
	}
	if first.Config == nil || len(first.Config.Workspaces["alpha"].Repos) != 1 {
		t.Fatalf("expected 1 repo in initial config, got %+v", first.Config)
	}

	same := GetDefaultResolver()
	if same != first {
		t.Errorf("expected same cached resolver when config unchanged, got different pointer")
	}

	updatedCfg := []byte(`version: 2
default_workspace: alpha
workspaces:
  alpha:
    path: /tmp/ws-alpha
    repos:
      - name: repo1
        path: /tmp/ws-alpha/repo1
      - name: repo2
        path: /tmp/ws-alpha/repo2
`)
	if err := os.WriteFile(cfgPath, updatedCfg, 0644); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	// Force a distinct mtime — some filesystems have coarse (1s) mtime
	// resolution so an immediate rewrite may produce the same timestamp.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(cfgPath, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	refreshed := GetDefaultResolver()
	if refreshed == first {
		t.Errorf("expected new resolver after config change, got same pointer")
	}
	if refreshed.Config == nil || len(refreshed.Config.Workspaces["alpha"].Repos) != 2 {
		t.Fatalf("expected 2 repos after config change, got %+v", refreshed.Config)
	}
}

// TestGetDefaultResolver_NoConfigFile verifies legacy-mode fallback when the
// config file does not exist, and that repeated calls reuse the cached
// resolver (both snapshots see the zero-time mtime and match as equal).
func TestGetDefaultResolver_NoConfigFile(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	old := TestingResetDefaultResolver()
	t.Cleanup(func() { TestingSetDefaultResolver(old) })

	first := GetDefaultResolver()
	if first.Mode != ModeLegacy {
		t.Fatalf("expected legacy mode with no config, got %d", first.Mode)
	}

	second := GetDefaultResolver()
	if second != first {
		t.Errorf("expected same cached legacy resolver, got different pointer")
	}
}

// TestTestingSetDefaultResolver_Persists verifies that an injected resolver
// is returned by the next GetDefaultResolver call rather than being
// immediately evicted by a stale-mtime mismatch.
func TestTestingSetDefaultResolver_Persists(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	cfgPath := filepath.Join(configDir, "config.yaml")

	cfg := []byte(`version: 2
default_workspace: alpha
workspaces:
  alpha:
    path: /tmp/ws-alpha
`)
	if err := os.WriteFile(cfgPath, cfg, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := TestingResetDefaultResolver()
	t.Cleanup(func() { TestingSetDefaultResolver(old) })

	injected := &Resolver{Mode: ModeLegacy}
	TestingSetDefaultResolver(injected)

	got := GetDefaultResolver()
	if got != injected {
		t.Errorf("expected injected resolver to persist, got different pointer")
	}
}

// TestTestingResetDefaultResolver_ClearsMtime verifies that the test-only
// reset hook also zeros the cached mtime so the next GetDefaultResolver call
// rebuilds even if the underlying config file mtime happens to match.
func TestTestingResetDefaultResolver_ClearsMtime(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	cfgPath := filepath.Join(configDir, "config.yaml")

	cfg := []byte(`version: 2
default_workspace: alpha
workspaces:
  alpha:
    path: /tmp/ws-alpha
`)
	if err := os.WriteFile(cfgPath, cfg, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	old := TestingResetDefaultResolver()
	t.Cleanup(func() { TestingSetDefaultResolver(old) })

	first := GetDefaultResolver()

	_ = TestingResetDefaultResolver()

	second := GetDefaultResolver()
	if second == first {
		t.Errorf("expected new resolver after TestingResetDefaultResolver, got same pointer")
	}
}
