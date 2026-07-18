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
//
// orchestrator_session_id was on this struct historically as a cache of
// the lead-to-orchestration AgentSession join. AgentSession is the
// single source of truth; readers use store.OrchestrationSessionIDFor.
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
	Execution        string    `json:"execution,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	// Derived, read-only liveness fields fleet-db computes from the
	// session+lease join; passed through to domain.Agent, never sent on writes.
	LiveStatus   string `json:"live_status,omitempty"`
	ActiveTaskID string `json:"active_task_id,omitempty"`
	ActivePhase  string `json:"active_phase,omitempty"`
	// LastErrorClass is fleet-db's derived error_class of the agent's most
	// recent terminal session when that run failed (idle agents only). Passed
	// through to domain.Agent so the UI can explain a stalled idle agent.
	LastErrorClass string `json:"last_error_class,omitempty"`
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
		Execution:        a.Execution,
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
		LiveStatus:       domain.AgentLiveStatus(a.LiveStatus),
		ActiveTaskID:     a.ActiveTaskID,
		ActivePhase:      a.ActivePhase,
		LastErrorClass:   a.LastErrorClass,
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
		Execution        string   `json:"execution,omitempty"`
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
		Execution:        in.Execution,
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
	if !agentUpdateHasFleetDBFields(patch) {
		return s.Get(ctx, ws, name)
	}
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
		Execution        *string   `json:"execution,omitempty"`
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
		Execution:        patch.Execution,
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

// agentUpdateHasFleetDBFields filters store.AgentUpdate down to the fields
// accepted by FleetDB's strict agent PATCH contract. Used to short-circuit
// PATCH requests that would carry only loomcli-local fields (none today; the
// last such field, OrchestratorSessionID, was removed when AgentSession
// became the single source of truth).
func agentUpdateHasFleetDBFields(patch store.AgentUpdate) bool {
	return patch.RoleName != nil ||
		patch.Auto != nil ||
		patch.Backend != nil ||
		patch.FallbackBackends != nil ||
		patch.Repos != nil ||
		patch.RepoGroups != nil ||
		patch.CrossRepo != nil ||
		patch.Parent != nil ||
		patch.State != nil ||
		patch.Mode != nil ||
		patch.TaskFilter != nil ||
		patch.MaxConcurrency != nil ||
		patch.BudgetPolicy != nil ||
		patch.DesiredState != nil
}

func (s *agentStore) Delete(ctx context.Context, ws, name string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/agents/"+pathEscape(name), nil, nil)
}
