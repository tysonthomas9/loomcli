package config

import (
	"context"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// seedDaemonConfigStore returns a memstore holding one workspace with a role
// and an agent, so a loaded DaemonConfig is distinguishable from the defaults.
func seedDaemonConfigStore(t *testing.T, ctx context.Context, wsKey string) store.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:           wsKey,
		Name:          wsKey,
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Roles().Create(ctx, store.RoleCreate{
		WorkspaceKey: wsKey,
		Name:         "worker",
		Kind:         "worker",
		PromptFile:   "worker.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: wsKey,
		Name:         "agent1",
		RoleName:     "worker",
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return st
}

// prepareDaemonConfigEnv points config at a temp dir, sets the active
// workspace, and makes any accidental fleet-db dial fail loudly.
func prepareDaemonConfigEnv(t *testing.T, wsKey string) {
	t.Helper()
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", wsKey)
	// Unreachable port: if the store-injecting path ever fell through to
	// bootstrap.OpenStore, the test would fail instead of silently dialing.
	t.Setenv("LOOM_FLEET_DB_URL", "http://127.0.0.1:1")
}

func TestLoadDaemonConfigWithStore_MatchesPrimingHelper(t *testing.T) {
	const wsKey = "WS1"
	ctx := context.Background()
	prepareDaemonConfigEnv(t, wsKey)
	st := seedDaemonConfigStore(t, ctx, wsKey)
	projectDir := t.TempDir()

	got, err := LoadDaemonConfigWithStore(ctx, st, projectDir)
	if err != nil {
		t.Fatalf("LoadDaemonConfigWithStore() error = %v", err)
	}

	want, err := TestingPrimeDaemonConfigCacheFromStore(ctx, st, wsKey, projectDir)
	if err != nil {
		t.Fatalf("TestingPrimeDaemonConfigCacheFromStore() error = %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadDaemonConfigWithStore() = %#v, want %#v", got, want)
	}
	if len(got.Agents) != 1 || got.Agents[0].Worktree != "agent1" {
		t.Errorf("agents = %#v, want the seeded agent1", got.Agents)
	}
	if _, ok := got.Roles["worker"]; !ok {
		t.Errorf("roles = %#v, want the seeded worker role", got.Roles)
	}
}

func TestLoadDaemonConfig_HonoursPrimedCacheAfterDelegation(t *testing.T) {
	const wsKey = "WS1"
	ctx := context.Background()
	prepareDaemonConfigEnv(t, wsKey)
	st := seedDaemonConfigStore(t, ctx, wsKey)
	projectDir := t.TempDir()

	want, err := TestingPrimeDaemonConfigCacheFromStore(ctx, st, wsKey, projectDir)
	if err != nil {
		t.Fatalf("TestingPrimeDaemonConfigCacheFromStore() error = %v", err)
	}

	// LoadDaemonConfig now delegates to LoadDaemonConfigWithStore with a nil
	// store; the primed-cache prologue must still short-circuit before any open.
	got, err := LoadDaemonConfig(projectDir)
	if err != nil {
		t.Fatalf("LoadDaemonConfig() error = %v", err)
	}
	if got != want {
		t.Fatalf("LoadDaemonConfig() = %p, want primed pointer %p", got, want)
	}
}

func TestLoadDaemonConfigWithStore_NoActiveWorkspaceReturnsDefaults(t *testing.T) {
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_WORKSPACE", "")

	got, err := LoadDaemonConfigWithStore(context.Background(), memstore.New(), t.TempDir())
	if err != nil {
		t.Fatalf("LoadDaemonConfigWithStore() error = %v", err)
	}
	if !reflect.DeepEqual(got, newDefaultDaemonConfig()) {
		t.Errorf("LoadDaemonConfigWithStore() = %#v, want built-in defaults", got)
	}
}
