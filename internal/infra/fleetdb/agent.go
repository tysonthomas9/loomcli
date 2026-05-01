package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentStore struct{ client *Client }

var _ store.AgentStore = (*agentStore)(nil)

// agentWire mirrors fleet-db's models.Agent JSON shape.
type agentWire struct {
	WorkspaceKey     string    `json:"workspace_key"`
	Name             string    `json:"name"`
	RoleName         string    `json:"role_name"`
	Auto             bool      `json:"auto,omitempty"`
	Backend          string    `json:"backend,omitempty"`
	FallbackBackends []string  `json:"fallback_backends,omitempty"`
	Repos            []string  `json:"repos,omitempty"`
	RepoGroups       []string  `json:"repo_groups,omitempty"`
	CrossRepo        bool      `json:"cross_repo,omitempty"`
	Parent           string    `json:"parent,omitempty"`
	State            string    `json:"state"`
	Mode             string    `json:"mode,omitempty"`
	TaskFilter       string    `json:"task_filter,omitempty"`
	MaxConcurrency   int       `json:"max_concurrency,omitempty"`
	BudgetPolicy     string    `json:"budget_policy,omitempty"`
	DesiredState     string    `json:"desired_state,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (a agentWire) toDomain() *domain.Agent {
	return &domain.Agent{
		WorkspaceKey:     a.WorkspaceKey,
		Name:             a.Name,
		RoleName:         a.RoleName,
		Auto:             a.Auto,
		Backend:          a.Backend,
		FallbackBackends: a.FallbackBackends,
		Repos:            a.Repos,
		RepoGroups:       a.RepoGroups,
		CrossRepo:        a.CrossRepo,
		Parent:           a.Parent,
		State:            domain.AgentState(a.State),
		Mode:             domain.AgentMode(a.Mode),
		TaskFilter:       a.TaskFilter,
		MaxConcurrency:   a.MaxConcurrency,
		BudgetPolicy:     a.BudgetPolicy,
		DesiredState:     domain.AgentDesiredState(a.DesiredState),
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
	}
}

func (s *agentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	body := struct {
		Name             string   `json:"name"`
		RoleName         string   `json:"role_name"`
		Auto             bool     `json:"auto,omitempty"`
		Backend          string   `json:"backend,omitempty"`
		FallbackBackends []string `json:"fallback_backends,omitempty"`
		Repos            []string `json:"repos,omitempty"`
		RepoGroups       []string `json:"repo_groups,omitempty"`
		CrossRepo        bool     `json:"cross_repo,omitempty"`
		Parent           string   `json:"parent,omitempty"`
		Mode             string   `json:"mode,omitempty"`
		TaskFilter       string   `json:"task_filter,omitempty"`
		MaxConcurrency   int      `json:"max_concurrency,omitempty"`
		BudgetPolicy     string   `json:"budget_policy,omitempty"`
		DesiredState     string   `json:"desired_state,omitempty"`
	}{
		Name:             in.Name,
		RoleName:         in.RoleName,
		Auto:             in.Auto,
		Backend:          in.Backend,
		FallbackBackends: in.FallbackBackends,
		Repos:            in.Repos,
		RepoGroups:       in.RepoGroups,
		CrossRepo:        in.CrossRepo,
		Parent:           in.Parent,
		Mode:             string(in.Mode),
		TaskFilter:       in.TaskFilter,
		MaxConcurrency:   in.MaxConcurrency,
		BudgetPolicy:     in.BudgetPolicy,
		DesiredState:     string(in.DesiredState),
	}
	var resp agentWire
	if err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/agents", body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *agentStore) Get(ctx context.Context, ws, name string) (*domain.Agent, error) {
	var resp agentWire
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agents/"+pathEscape(name), nil, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *agentStore) List(ctx context.Context, ws string) ([]*domain.Agent, error) {
	var resp struct {
		Agents []agentWire `json:"agents"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/agents", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.Agent, 0, len(resp.Agents))
	for _, a := range resp.Agents {
		out = append(out, a.toDomain())
	}
	return out, nil
}

func (s *agentStore) Update(ctx context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	body := struct {
		RoleName         *string   `json:"role_name,omitempty"`
		Auto             *bool     `json:"auto,omitempty"`
		Backend          *string   `json:"backend,omitempty"`
		FallbackBackends *[]string `json:"fallback_backends,omitempty"`
		Repos            *[]string `json:"repos,omitempty"`
		RepoGroups       *[]string `json:"repo_groups,omitempty"`
		CrossRepo        *bool     `json:"cross_repo,omitempty"`
		Parent           *string   `json:"parent,omitempty"`
		State            *string   `json:"state,omitempty"`
		Mode             *string   `json:"mode,omitempty"`
		TaskFilter       *string   `json:"task_filter,omitempty"`
		MaxConcurrency   *int      `json:"max_concurrency,omitempty"`
		BudgetPolicy     *string   `json:"budget_policy,omitempty"`
		DesiredState     *string   `json:"desired_state,omitempty"`
	}{
		RoleName:         patch.RoleName,
		Auto:             patch.Auto,
		Backend:          patch.Backend,
		FallbackBackends: patch.FallbackBackends,
		Repos:            patch.Repos,
		RepoGroups:       patch.RepoGroups,
		CrossRepo:        patch.CrossRepo,
		Parent:           patch.Parent,
		TaskFilter:       patch.TaskFilter,
		MaxConcurrency:   patch.MaxConcurrency,
		BudgetPolicy:     patch.BudgetPolicy,
	}
	if patch.State != nil {
		s := string(*patch.State)
		body.State = &s
	}
	if patch.Mode != nil {
		s := string(*patch.Mode)
		body.Mode = &s
	}
	if patch.DesiredState != nil {
		s := string(*patch.DesiredState)
		body.DesiredState = &s
	}
	var resp agentWire
	if err := s.client.do(ctx, "PATCH", "/api/v1/"+pathEscape(ws)+"/agents/"+pathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp.toDomain(), nil
}

func (s *agentStore) Delete(ctx context.Context, ws, name string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/agents/"+pathEscape(name), nil, nil)
}
