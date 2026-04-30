package fleetdb

import (
	"context"
	"fmt"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// workspaceStore implements store.WorkspaceStore against fleet-db's
// /api/v1/admin/workspaces endpoints. Bound to a parent *Client which
// owns the HTTP transport + auth state.
type workspaceStore struct{ client *Client }

var _ store.WorkspaceStore = (*workspaceStore)(nil)

// workspaceWire is the JSON shape fleet-db emits for a workspace.
// Mirrors models.Workspace but lives here so loom doesn't import
// fleet-db packages directly.
type workspaceWire struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Repos       []string  `json:"repos,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Optional fields fleet-db may add (state, default_branch, etc.).
	// Decoded if present, ignored if absent.
	State         string `json:"state,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

func (w workspaceWire) toDomain() *domain.Workspace {
	return &domain.Workspace{
		Key:           w.Key,
		Name:          w.Name,
		Description:   w.Description,
		State:         domain.WorkspaceState(w.State),
		ErrorMessage:  w.ErrorMessage,
		DefaultBranch: w.DefaultBranch,
		CreatedAt:     w.CreatedAt,
		UpdatedAt:     w.UpdatedAt,
	}
}

func (s *workspaceStore) Create(ctx context.Context, in store.WorkspaceCreate) (*domain.Workspace, error) {
	body := struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}{
		Key:         in.Key,
		Name:        in.Name,
		Description: in.Description,
	}
	var resp workspaceWire
	if err := s.client.do(ctx, "POST", "/api/v1/admin/workspaces", body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *workspaceStore) Get(ctx context.Context, key string) (*domain.Workspace, error) {
	var resp workspaceWire
	if err := s.client.do(ctx, "GET", "/api/v1/admin/workspaces/"+pathEscape(key), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *workspaceStore) GetByName(ctx context.Context, name string) (*domain.Workspace, error) {
	// Fleet-db does not currently expose a name-lookup endpoint, so we
	// fall back to List + scan. List is cheap (workspace count is tiny);
	// upgrade to a dedicated endpoint if it ever becomes a hotspot.
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, ws := range all {
		if ws.Name == name {
			return ws, nil
		}
	}
	return nil, fmt.Errorf("fleetdb: workspace name %q: %w", name, domain.ErrNotFound)
}

func (s *workspaceStore) List(ctx context.Context) ([]*domain.Workspace, error) {
	var resp struct {
		Workspaces []workspaceWire `json:"workspaces"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/admin/workspaces", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.Workspace, 0, len(resp.Workspaces))
	for _, w := range resp.Workspaces {
		out = append(out, w.toDomain())
	}
	return out, nil
}

func (s *workspaceStore) Update(ctx context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	body := struct {
		Name *string `json:"name,omitempty"`
		// fleet-db's existing PATCH route does not yet accept Description / State / DefaultBranch / ErrorMessage.
		// Sending them is silently ignored; once fleet-db's PATCH route
		// grows those fields, the JSON-omitempty wire shape lets loom
		// adopt them without code changes here.
		Description   *string `json:"description,omitempty"`
		DefaultBranch *string `json:"default_branch,omitempty"`
		State         *string `json:"state,omitempty"`
		ErrorMessage  *string `json:"error_message,omitempty"`
	}{
		Name:          patch.Name,
		Description:   patch.Description,
		DefaultBranch: patch.DefaultBranch,
		ErrorMessage:  patch.ErrorMessage,
	}
	if patch.State != nil {
		s := string(*patch.State)
		body.State = &s
	}
	if err := s.client.do(ctx, "PATCH", "/api/v1/admin/workspaces/"+pathEscape(key), body, nil); err != nil {
		return nil, err
	}
	// fleet-db PATCH returns 204 No Content. Re-Get for the canonical view.
	return s.Get(ctx, key)
}

func (s *workspaceStore) Delete(ctx context.Context, key string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/admin/workspaces/"+pathEscape(key)+"?force=true", nil, nil)
}

