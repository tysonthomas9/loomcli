package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type repoStore struct{ client *Client }

var _ store.RepoStore = (*repoStore)(nil)

// repoWire mirrors fleet-db's models.Repo JSON shape.
type repoWire struct {
	WorkspaceKey            string                         `json:"workspace_key"`
	Name                    string                         `json:"name"`
	RemoteURL               string                         `json:"remote_url"`
	Remote                  string                         `json:"remote,omitempty"`
	DefaultBranch           string                         `json:"default_branch,omitempty"`
	Groups                  []string                       `json:"groups,omitempty"`
	SourceRepoID            string                         `json:"source_repo_id,omitempty"`
	TaskDeliveryRequirement domain.TaskDeliveryRequirement `json:"task_delivery_requirement,omitempty"`
	CreatedAt               time.Time                      `json:"created_at"`
	UpdatedAt               time.Time                      `json:"updated_at"`
}

func (r repoWire) toDomain() *domain.Repo {
	return &domain.Repo{
		WorkspaceKey:            r.WorkspaceKey,
		Name:                    r.Name,
		RemoteURL:               r.RemoteURL,
		Remote:                  r.Remote,
		DefaultBranch:           r.DefaultBranch,
		Groups:                  r.Groups,
		SourceRepoID:            r.SourceRepoID,
		TaskDeliveryRequirement: r.TaskDeliveryRequirement,
		CreatedAt:               r.CreatedAt,
		UpdatedAt:               r.UpdatedAt,
	}
}

func (s *repoStore) Create(ctx context.Context, in store.RepoCreate) (*domain.Repo, error) {
	body := struct {
		Name                    string                         `json:"name"`
		RemoteURL               string                         `json:"remote_url"`
		Remote                  string                         `json:"remote,omitempty"`
		DefaultBranch           string                         `json:"default_branch,omitempty"`
		Groups                  []string                       `json:"groups,omitempty"`
		SourceRepoID            string                         `json:"source_repo_id,omitempty"`
		TaskDeliveryRequirement domain.TaskDeliveryRequirement `json:"task_delivery_requirement,omitempty"`
	}{
		Name:                    in.Name,
		RemoteURL:               in.RemoteURL,
		Remote:                  in.Remote,
		DefaultBranch:           in.DefaultBranch,
		Groups:                  in.Groups,
		SourceRepoID:            in.SourceRepoID,
		TaskDeliveryRequirement: in.TaskDeliveryRequirement,
	}
	var resp repoWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/repos", body, &resp); err != nil {
		return nil, err
	}
	if err := s.client.do(ctx, "PATCH", "/api/v1/admin/workspaces/"+pathEscape(in.WorkspaceKey), struct {
		AddRepos []string `json:"add_repos,omitempty"`
	}{AddRepos: []string{in.Name}}, nil); err != nil {
		_ = s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/repos/"+pathEscape(in.Name), nil, nil)
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) Get(ctx context.Context, ws, name string) (*domain.Repo, error) {
	var resp repoWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) List(ctx context.Context, ws string) ([]*domain.Repo, error) {
	var resp struct {
		Repos []repoWire `json:"repos"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/repos", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.Repo, 0, len(resp.Repos))
	for _, r := range resp.Repos {
		out = append(out, r.toDomain())
	}
	return out, nil
}

func (s *repoStore) Update(ctx context.Context, ws, name string, patch store.RepoUpdate) (*domain.Repo, error) {
	body := struct {
		RemoteURL               *string                         `json:"remote_url,omitempty"`
		Remote                  *string                         `json:"remote,omitempty"`
		DefaultBranch           *string                         `json:"default_branch,omitempty"`
		Groups                  *[]string                       `json:"groups,omitempty"`
		SourceRepoID            *string                         `json:"source_repo_id,omitempty"`
		TaskDeliveryRequirement *domain.TaskDeliveryRequirement `json:"task_delivery_requirement,omitempty"`
	}{
		RemoteURL:               patch.RemoteURL,
		Remote:                  patch.Remote,
		DefaultBranch:           patch.DefaultBranch,
		Groups:                  patch.Groups,
		SourceRepoID:            patch.SourceRepoID,
		TaskDeliveryRequirement: patch.TaskDeliveryRequirement,
	}
	var resp repoWire
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *repoStore) Delete(ctx context.Context, ws, name string) error {
	if err := s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/repos/"+pathEscape(name), nil, nil); err != nil {
		return err
	}
	return s.client.do(ctx, "PATCH", "/api/v1/admin/workspaces/"+pathEscape(ws), struct {
		DelRepos []string `json:"del_repos,omitempty"`
	}{DelRepos: []string{name}}, nil)
}
