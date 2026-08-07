package fleetdb

import (
	"context"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/store"
)

type repoStore struct{ client *Client }

var _ store.RepoStore = (*repoStore)(nil)

// repoWire mirrors fleet-db's models.Repo JSON shape.
type repoWire struct {
	WorkspaceKey  string    `json:"workspace_key"`
	Name          string    `json:"name"`
	RemoteURL     string    `json:"remote_url"`
	Remote        string    `json:"remote,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Groups        []string  `json:"groups,omitempty"`
	SourceRepoID  string    `json:"source_repo_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (r repoWire) toDomain() *workspacemodule.Repository {
	return &workspacemodule.Repository{
		WorkspaceKey:  r.WorkspaceKey,
		Name:          r.Name,
		RemoteURL:     r.RemoteURL,
		Remote:        r.Remote,
		DefaultBranch: r.DefaultBranch,
		Groups:        r.Groups,
		SourceRepoID:  r.SourceRepoID,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func (s *repoStore) Create(ctx context.Context, in store.RepoCreate) (*workspacemodule.Repository, error) {
	body := struct {
		Name          string   `json:"name"`
		RemoteURL     string   `json:"remote_url"`
		Remote        string   `json:"remote,omitempty"`
		DefaultBranch string   `json:"default_branch,omitempty"`
		Groups        []string `json:"groups,omitempty"`
		SourceRepoID  string   `json:"source_repo_id,omitempty"`
	}{
		Name:          in.Name,
		RemoteURL:     in.RemoteURL,
		Remote:        in.Remote,
		DefaultBranch: in.DefaultBranch,
		Groups:        in.Groups,
		SourceRepoID:  in.SourceRepoID,
	}
	var resp repoWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/repos", body, &resp); err != nil {
		return nil, err
	}
	// FleetDB creates the first-class repository and its legacy workspace
	// admission mapping in one backend commit. A follow-up workspace PATCH
	// would expose a partially admitted repository between the two calls.
	return resp.toDomain(), nil
}

func (s *repoStore) Get(ctx context.Context, ws, name string) (*workspacemodule.Repository, error) {
	var resp repoWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) List(ctx context.Context, ws string) ([]*workspacemodule.Repository, error) {
	var resp struct {
		Repos []repoWire `json:"repos"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*workspacemodule.Repository, 0, len(resp.Repos))
	for _, r := range resp.Repos {
		out = append(out, r.toDomain())
	}
	return out, nil
}

func (s *repoStore) Update(ctx context.Context, ws, name string, patch store.RepoUpdate) (*workspacemodule.Repository, error) {
	body := struct {
		RemoteURL     *string   `json:"remote_url,omitempty"`
		Remote        *string   `json:"remote,omitempty"`
		DefaultBranch *string   `json:"default_branch,omitempty"`
		Groups        *[]string `json:"groups,omitempty"`
		SourceRepoID  *string   `json:"source_repo_id,omitempty"`
	}{
		RemoteURL:     patch.RemoteURL,
		Remote:        patch.Remote,
		DefaultBranch: patch.DefaultBranch,
		Groups:        patch.Groups,
		SourceRepoID:  patch.SourceRepoID,
	}
	var resp repoWire
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) Delete(ctx context.Context, ws, name string) error {
	// FleetDB's guarded repository command removes the first-class entity and
	// its legacy workspace admission mapping in one backend commit. A follow-up
	// workspace PATCH would reintroduce a race in which new work can be admitted
	// between the two calls.
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, nil)
}
