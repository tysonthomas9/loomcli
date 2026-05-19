package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func useBootstrapTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir)
	t.Setenv(EnvWorkspace, "")
	t.Setenv(EnvFleetDBURL, "")
	return dir
}

func TestStateCacheLoadSaveMutateAndPaths(t *testing.T) {
	dir := useBootstrapTempDir(t)
	if LoomDir() != dir {
		t.Fatalf("LoomDir = %q, want %q", LoomDir(), dir)
	}
	if StateFilePath() != filepath.Join(dir, "state.json") {
		t.Fatalf("StateFilePath = %q", StateFilePath())
	}

	missing, err := LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache missing: %v", err)
	}
	if missing.Version != stateCacheVersion || len(missing.Workspaces) != 0 {
		t.Fatalf("missing cache = %+v", missing)
	}
	if err := SaveStateCache(nil); err == nil {
		t.Fatal("expected nil cache save error")
	}

	cache := &StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]WorkspaceLocalState{
			"WS": {Path: "/workspace", Repos: map[string]string{"app": "/workspace/app"}},
		},
	}
	if err := SaveStateCache(cache); err != nil {
		t.Fatalf("SaveStateCache: %v", err)
	}
	loaded, err := LoadStateCache()
	if err != nil {
		t.Fatalf("LoadStateCache saved: %v", err)
	}
	if loaded.Version != stateCacheVersion || loaded.LastWorkspace != "WS" {
		t.Fatalf("loaded cache = %+v", loaded)
	}
	if loaded.Workspaces["WS"].Repos["app"] != "/workspace/app" {
		t.Fatalf("loaded workspace = %+v", loaded.Workspaces["WS"])
	}

	if err := WithStateLock(func() error { return nil }); err != nil {
		t.Fatalf("WithStateLock: %v", err)
	}
	if err := MutateWorkspaceLocalState("", func(*WorkspaceLocalState) error { return nil }); err == nil {
		t.Fatal("expected empty workspace key error")
	}
	if err := MutateWorkspaceLocalState("WS", nil); err == nil {
		t.Fatal("expected nil mutate function error")
	}
	if err := MutateWorkspaceLocalState("WS", func(local *WorkspaceLocalState) error {
		local.Agents = map[string]AgentLocalState{"planner": {Worktree: "/workspace/worktrees/app/planner"}}
		return nil
	}); err != nil {
		t.Fatalf("MutateWorkspaceLocalState: %v", err)
	}
	loaded, err = LoadStateCache()
	if err != nil {
		t.Fatalf("reload after mutate: %v", err)
	}
	if loaded.Workspaces["WS"].Agents["planner"].Worktree == "" {
		t.Fatalf("mutated workspace = %+v", loaded.Workspaces["WS"])
	}

	if err := os.WriteFile(StateFilePath(), []byte("{bad-json"), 0600); err != nil {
		t.Fatalf("write malformed state: %v", err)
	}
	if _, err := LoadStateCache(); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("malformed cache err = %v", err)
	}
}

func TestModeAndActiveWorkspaceHelpers(t *testing.T) {
	useBootstrapTempDir(t)

	if ModeLocal.String() != "local" || ModeCloud.String() != "cloud" || Mode(99).String() != "mode(99)" {
		t.Fatalf("unexpected mode strings")
	}
	if DetectMode() != ModeLocal {
		t.Fatalf("DetectMode without URL should be local")
	}
	t.Setenv(EnvFleetDBURL, "http://127.0.0.1:8080")
	if DetectMode() != ModeCloud {
		t.Fatalf("DetectMode with URL should be cloud")
	}
	t.Setenv(EnvFleetDBURL, "")

	ctx := context.Background()
	if _, err := ResolveActiveWorkspaceKey(ctx, nil); !errors.Is(err, ErrNoActiveWorkspace) {
		t.Fatalf("ResolveActiveWorkspaceKey without env err = %v", err)
	}
	t.Setenv(EnvWorkspace, "WS")
	if key, err := ResolveActiveWorkspaceKey(ctx, nil); err != nil || key != "WS" {
		t.Fatalf("ResolveActiveWorkspaceKey no store key=%q err=%v", key, err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if key, err := ResolveActiveWorkspaceKey(ctx, st.Workspaces()); err != nil || key != "WS" {
		t.Fatalf("ResolveActiveWorkspaceKey store key=%q err=%v", key, err)
	}
	t.Setenv(EnvWorkspace, "MISSING")
	if _, err := ResolveActiveWorkspaceKey(ctx, st.Workspaces()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing active workspace err = %v", err)
	}

	if err := SetActiveWorkspaceKey(""); err == nil {
		t.Fatal("expected empty active workspace key error")
	}
	if err := SetActiveWorkspaceKey("WS"); err != nil {
		t.Fatalf("SetActiveWorkspaceKey: %v", err)
	}
	cache, err := LoadStateCache()
	if err != nil {
		t.Fatalf("load after set: %v", err)
	}
	if cache.LastWorkspace != "WS" {
		t.Fatalf("LastWorkspace after set = %q", cache.LastWorkspace)
	}
	if err := ClearActiveWorkspaceKey(); err != nil {
		t.Fatalf("ClearActiveWorkspaceKey: %v", err)
	}
	cache, err = LoadStateCache()
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if cache.LastWorkspace != "" {
		t.Fatalf("LastWorkspace after clear = %q", cache.LastWorkspace)
	}
}

func TestStoreHandleModeURLAndClose(t *testing.T) {
	t.Setenv(EnvFleetDBURL, "http://fleet.example.test")
	h := &StoreHandle{Store: memstore.New(), mode: ModeCloud}
	if h.Mode() != ModeCloud {
		t.Fatalf("Mode = %s", h.Mode())
	}
	if h.URL() != "http://fleet.example.test" {
		t.Fatalf("URL = %q", h.URL())
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if (*StoreHandle)(nil).URL() != "" {
		t.Fatal("nil handle URL should be empty")
	}
}
