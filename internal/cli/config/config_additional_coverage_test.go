package config

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTestingPrimeConfigCacheWorkspaceByIDAndProjectionFallbacks(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	InvalidateConfigCache()

	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "WS",
		Name:          "api",
		Remote:        "origin",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		LastWorkspace: "WS",
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: "/workspace"},
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	cfg, err := TestingPrimeConfigCacheFromStore(ctx, st)
	if err != nil {
		t.Fatalf("TestingPrimeConfigCacheFromStore: %v", err)
	}
	cached, err := LoadConfigCached()
	if err != nil {
		t.Fatalf("LoadConfigCached: %v", err)
	}
	if cached != cfg {
		t.Fatalf("cached config pointer = %p, want %p", cached, cfg)
	}

	name, ws, ok := WorkspaceByID(cfg, "WS")
	if !ok || name != "WS" || ws.Path != "/workspace" {
		t.Fatalf("WorkspaceByID = %q/%+v/%t, want WS workspace", name, ws, ok)
	}
	if _, _, ok := WorkspaceByID(nil, "WS"); ok {
		t.Fatal("WorkspaceByID nil cfg returned ok")
	}
	if _, _, ok := WorkspaceByID(cfg, ""); ok {
		t.Fatal("WorkspaceByID empty id returned ok")
	}
	if _, _, ok := WorkspaceByID(cfg, "missing"); ok {
		t.Fatal("WorkspaceByID missing id returned ok")
	}

	repo := repoConfigFromStore(&domain.Repo{Name: "web", Groups: []string{"frontend"}}, bootstrap.WorkspaceLocalState{})
	if repo.Path != "web" || repo.SourceRepoID != "web" || len(repo.Groups) != 1 {
		t.Fatalf("repo fallback projection = %+v", repo)
	}
	if got := roleConfigFromDomain(nil); got.Description != "" {
		t.Fatalf("nil role projection = %+v, want zero value", got)
	}
	if got := agentEntryFromDomain(nil); got.Worktree != "" {
		t.Fatalf("nil agent projection = %+v, want zero value", got)
	}
	if got := daemonSettingsFromDomain(nil); got != nil {
		t.Fatalf("nil daemon profile projection = %+v, want nil", got)
	}
	if got := otelFromDomain(nil); got != nil {
		t.Fatalf("nil otel projection = %+v, want nil", got)
	}
}

func TestLoadDaemonConfigFromStoreErrorBranches(t *testing.T) {
	ctx := context.Background()
	errBoom := errors.New("boom")

	t.Run("daemon get error", func(t *testing.T) {
		st := &daemonGetErrorStore{Store: memstore.New(), err: errBoom}
		_, err := loadDaemonConfigFromStore(ctx, st, "WS", nil, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "get daemon profile") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want daemon profile error", err)
		}
	})

	t.Run("role list error", func(t *testing.T) {
		st := &roleListErrorStore{Store: memstore.New(), err: errBoom}
		_, err := loadDaemonConfigFromStore(ctx, st, "WS", nil, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "list roles") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want role list error", err)
		}
	})

	t.Run("agent list error", func(t *testing.T) {
		st := &agentListErrorStore{Store: memstore.New(), err: errBoom}
		_, err := loadDaemonConfigFromStore(ctx, st, "WS", nil, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "list agents") || !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want agent list error", err)
		}
	})

	t.Run("defaults issue backend", func(t *testing.T) {
		st := memstore.New()
		cfg, err := loadDaemonConfigFromStore(ctx, st, "WS", nil, t.TempDir())
		if err != nil {
			t.Fatalf("loadDaemonConfigFromStore: %v", err)
		}
		if cfg.Daemon.IssueBackend != "fleetdb" {
			t.Fatalf("IssueBackend = %q, want fleetdb", cfg.Daemon.IssueBackend)
		}
	})
}

type daemonGetErrorStore struct {
	*memstore.Store
	err error
}

func (s *daemonGetErrorStore) Daemon() store.DaemonProfileStore {
	return daemonGetErrorProfileStore{err: s.err}
}

type daemonGetErrorProfileStore struct {
	err error
}

func (s daemonGetErrorProfileStore) Get(context.Context, string) (*domain.DaemonProfile, error) {
	return nil, s.err
}

func (s daemonGetErrorProfileStore) Upsert(context.Context, *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	return nil, s.err
}

type roleListErrorStore struct {
	*memstore.Store
	err error
}

func (s *roleListErrorStore) Roles() store.RoleStore {
	return roleListErrorRoleStore{RoleStore: s.Store.Roles(), err: s.err}
}

type roleListErrorRoleStore struct {
	store.RoleStore
	err error
}

func (s roleListErrorRoleStore) List(context.Context, string) ([]*domain.Role, error) {
	return nil, s.err
}

type agentListErrorStore struct {
	*memstore.Store
	err error
}

func (s *agentListErrorStore) Agents() store.AgentStore {
	return agentListErrorAgentStore{AgentStore: s.Store.Agents(), err: s.err}
}

type agentListErrorAgentStore struct {
	store.AgentStore
	err error
}

func (s agentListErrorAgentStore) List(context.Context, string) ([]*domain.Agent, error) {
	return nil, s.err
}
