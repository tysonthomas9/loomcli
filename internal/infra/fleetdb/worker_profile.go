package fleetdb

import (
	"context"
	"net/url"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workerProfileStore struct{ client *Client }

var _ store.WorkerProfileStore = (*workerProfileStore)(nil)

func (s *workerProfileStore) Create(ctx context.Context, in store.WorkerProfileCreate) (*domain.WorkerProfile, error) {
	body := struct {
		ProfileID     string            `json:"profile_id"`
		Name          string            `json:"name,omitempty"`
		Role          string            `json:"role"`
		Backend       string            `json:"backend,omitempty"`
		RuntimePolicy map[string]string `json:"runtime_policy,omitempty"`
		Repos         []string          `json:"repos,omitempty"`
		MaxPriority   *int              `json:"max_priority,omitempty"`
		MaxParallel   int               `json:"max_parallel,omitempty"`
		ParentEpic    string            `json:"parent_epic,omitempty"`
		Labels        []string          `json:"labels,omitempty"`
		Capabilities  []string          `json:"capabilities,omitempty"`
		Enabled       *bool             `json:"enabled,omitempty"`
		Metadata      map[string]string `json:"metadata,omitempty"`
	}{
		ProfileID:     in.ProfileID,
		Name:          in.Name,
		Role:          in.Role,
		Backend:       in.Backend,
		RuntimePolicy: in.RuntimePolicy,
		Repos:         in.Repos,
		MaxPriority:   in.MaxPriority,
		MaxParallel:   in.MaxParallel,
		ParentEpic:    in.ParentEpic,
		Labels:        in.Labels,
		Capabilities:  in.Capabilities,
		Enabled:       in.Enabled,
		Metadata:      in.Metadata,
	}
	var out domain.WorkerProfile
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/worker-profiles", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workerProfileStore) Get(ctx context.Context, ws, profileID string) (*domain.WorkerProfile, error) {
	var out domain.WorkerProfile
	path := "/api/v1/" + pathEscape(ws) + "/worker-profiles/" + pathEscape(profileID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workerProfileStore) List(ctx context.Context, ws string, filter store.WorkerProfileFilter) ([]*domain.WorkerProfile, error) {
	q := url.Values{}
	if filter.Role != "" {
		q.Set("role", filter.Role)
	}
	if filter.Backend != "" {
		q.Set("backend", filter.Backend)
	}
	if filter.Enabled != nil {
		q.Set("enabled", strconv.FormatBool(*filter.Enabled))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/worker-profiles", q)
	var resp struct {
		WorkerProfiles []*domain.WorkerProfile `json:"worker_profiles"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.WorkerProfiles == nil {
		resp.WorkerProfiles = []*domain.WorkerProfile{}
	}
	return resp.WorkerProfiles, nil
}

func (s *workerProfileStore) Update(ctx context.Context, ws, profileID string, patch store.WorkerProfileUpdate) (*domain.WorkerProfile, error) {
	body := struct {
		Name               *string            `json:"name,omitempty"`
		Role               *string            `json:"role,omitempty"`
		Backend            *string            `json:"backend,omitempty"`
		RuntimePolicy      *map[string]string `json:"runtime_policy,omitempty"`
		Repos              *[]string          `json:"repos,omitempty"`
		MaxPriority        *int               `json:"max_priority,omitempty"`
		MaxParallel        *int               `json:"max_parallel,omitempty"`
		ClearMaxPriority   bool               `json:"clear_max_priority,omitempty"`
		ParentEpic         *string            `json:"parent_epic,omitempty"`
		ExpectedParentEpic *string            `json:"expected_parent_epic,omitempty"`
		Labels             *[]string          `json:"labels,omitempty"`
		Capabilities       *[]string          `json:"capabilities,omitempty"`
		Enabled            *bool              `json:"enabled,omitempty"`
		Metadata           *map[string]string `json:"metadata,omitempty"`
	}{
		Name:               patch.Name,
		Role:               patch.Role,
		Backend:            patch.Backend,
		RuntimePolicy:      patch.RuntimePolicy,
		Repos:              patch.Repos,
		MaxPriority:        patch.MaxPriority,
		MaxParallel:        patch.MaxParallel,
		ClearMaxPriority:   patch.ClearMaxPriority,
		ParentEpic:         patch.ParentEpic,
		ExpectedParentEpic: patch.ExpectedParentEpic,
		Labels:             patch.Labels,
		Capabilities:       patch.Capabilities,
		Enabled:            patch.Enabled,
		Metadata:           patch.Metadata,
	}
	var out domain.WorkerProfile
	path := "/api/v1/" + pathEscape(ws) + "/worker-profiles/" + pathEscape(profileID)
	if err := s.client.do(ctx, "PATCH", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *workerProfileStore) Delete(ctx context.Context, ws, profileID string) error {
	path := "/api/v1/" + pathEscape(ws) + "/worker-profiles/" + pathEscape(profileID)
	return s.client.do(ctx, "DELETE", path, nil, nil)
}
