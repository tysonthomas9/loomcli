package cmdstore

// Traced wrappers for the core entity stores (workspaces, repos, agents,
// roles), mirroring the per-entity files in internal/store. Shared span
// helpers live in store_tracing.go.

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// --- WorkspaceStore ---

type tracedWorkspaceStore struct{ inner store.WorkspaceStore }

func (t *tracedWorkspaceStore) Create(ctx context.Context, in store.WorkspaceCreate) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Create", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.Key),
	)
}

func (t *tracedWorkspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Get", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Get(ctx, key)
	},
		attribute.String("loom.workspace", key),
	)
}

// GetWorkspaceScoped delegates to the inner store's workspace-scoped fetch when
// supported (the HTTP/fleetdb store). Implements store.ScopedWorkspaceGetter so
// the assertion still succeeds through the tracing wrapper.
func (t *tracedWorkspaceStore) GetWorkspaceScoped(ctx context.Context, key string) (*domain.Workspace, error) {
	sg, ok := t.inner.(store.ScopedWorkspaceGetter)
	if !ok {
		return nil, errors.New("workspace store does not support scoped get")
	}
	return traced(ctx, "Workspaces", "GetWorkspaceScoped", func(ctx context.Context) (*domain.Workspace, error) {
		return sg.GetWorkspaceScoped(ctx, key)
	},
		attribute.String("loom.workspace", key),
	)
}

func (t *tracedWorkspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "GetByName", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.GetByName(ctx, name)
	})
}

func (t *tracedWorkspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	return tracedList(ctx, "Workspaces", "List", func(ctx context.Context) ([]*domain.Workspace, error) {
		return t.inner.List(ctx)
	})
}

func (t *tracedWorkspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	return traced(ctx, "Workspaces", "Update", func(ctx context.Context) (*domain.Workspace, error) {
		return t.inner.Update(ctx, key, patch)
	},
		attribute.String("loom.workspace", key),
	)
}

func (t *tracedWorkspaceStore) Delete(ctx context.Context, key string) error {
	return tracedErr(ctx, "Workspaces", func(ctx context.Context) error {
		return t.inner.Delete(ctx, key)
	},
		attribute.String("loom.workspace", key),
	)
}

// --- RepoStore ---

type tracedRepoStore struct{ inner store.RepoStore }

func (t *tracedRepoStore) Create(ctx context.Context, in store.RepoCreate) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Create", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
	)
}

func (t *tracedRepoStore) Get(ctx context.Context, ws, name string) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Get", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) List(ctx context.Context, ws string) ([]*domain.Repo, error) {
	return tracedList(ctx, "Repos", "List", func(ctx context.Context) ([]*domain.Repo, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) Update(ctx context.Context, ws, name string, patch store.RepoUpdate) (*domain.Repo, error) {
	return traced(ctx, "Repos", "Update", func(ctx context.Context) (*domain.Repo, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRepoStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Repos", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
	)
}

// --- AgentStore ---

type tracedAgentStore struct{ inner store.AgentStore }

func (t *tracedAgentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Create", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.agent", in.Name),
	)
}

func (t *tracedAgentStore) Get(ctx context.Context, ws, name string) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Get", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

func (t *tracedAgentStore) List(ctx context.Context, ws string) ([]*domain.Agent, error) {
	return tracedList(ctx, "Agents", "List", func(ctx context.Context) ([]*domain.Agent, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedAgentStore) Update(ctx context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	return traced(ctx, "Agents", "Update", func(ctx context.Context) (*domain.Agent, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

func (t *tracedAgentStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Agents", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.agent", name),
	)
}

// --- RoleStore ---

type tracedRoleStore struct{ inner store.RoleStore }

func (t *tracedRoleStore) Create(ctx context.Context, in store.RoleCreate) (*domain.Role, error) {
	return traced(ctx, "Roles", "Create", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Create(ctx, in)
	},
		attribute.String("loom.workspace", in.WorkspaceKey),
		attribute.String("loom.role", in.Name),
	)
}

func (t *tracedRoleStore) Get(ctx context.Context, ws, name string) (*domain.Role, error) {
	return traced(ctx, "Roles", "Get", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Get(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}

func (t *tracedRoleStore) List(ctx context.Context, ws string) ([]*domain.Role, error) {
	return tracedList(ctx, "Roles", "List", func(ctx context.Context) ([]*domain.Role, error) {
		return t.inner.List(ctx, ws)
	},
		attribute.String("loom.workspace", ws),
	)
}

func (t *tracedRoleStore) Update(ctx context.Context, ws, name string, patch store.RoleUpdate) (*domain.Role, error) {
	return traced(ctx, "Roles", "Update", func(ctx context.Context) (*domain.Role, error) {
		return t.inner.Update(ctx, ws, name, patch)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}

func (t *tracedRoleStore) Delete(ctx context.Context, ws, name string) error {
	return tracedErr(ctx, "Roles", func(ctx context.Context) error {
		return t.inner.Delete(ctx, ws, name)
	},
		attribute.String("loom.workspace", ws),
		attribute.String("loom.role", name),
	)
}
