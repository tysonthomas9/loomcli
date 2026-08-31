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
	WorkspaceKey     string             `json:"workspace_key"`
	Name             string             `json:"name"`
	RoleName         string             `json:"role_name"`
	Auto             bool               `json:"auto,omitempty"`
	Backend          string             `json:"backend,omitempty"`
	FallbackBackends []string           `json:"fallback_backends,omitempty"`
	Repos            []string           `json:"repos,omitempty"`
	RepoGroups       []string           `json:"repo_groups,omitempty"`
	CrossRepo        bool               `json:"cross_repo,omitempty"`
	Parent           string             `json:"parent,omitempty"`
	State            string             `json:"state"`
	Mode             string             `json:"mode,omitempty"`
	TaskFilter       string             `json:"task_filter,omitempty"`
	MaxConcurrency   int                `json:"max_concurrency,omitempty"`
	BudgetPolicy     string             `json:"budget_policy,omitempty"`
	DesiredState     string             `json:"desired_state,omitempty"`
	DrainNodeID      string             `json:"drain_node_id,omitempty"`
	DrainExpiresAt   *time.Time         `json:"drain_expires_at,omitempty"`
	Hooks            *domain.AgentHooks `json:"hooks,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
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
		Repos:            coalesceStrings(a.Repos),
		RepoGroups:       coalesceStrings(a.RepoGroups),
		CrossRepo:        a.CrossRepo,
		Parent:           a.Parent,
		State:            domain.AgentState(a.State),
		Mode:             domain.AgentMode(a.Mode),
		TaskFilter:       a.TaskFilter,
		MaxConcurrency:   a.MaxConcurrency,
		BudgetPolicy:     a.BudgetPolicy,
		DesiredState:     domain.AgentDesiredState(a.DesiredState),
		DrainNodeID:      a.DrainNodeID,
		DrainExpiresAt:   a.DrainExpiresAt,
		Hooks:            a.Hooks.Clone(),
		CreatedAt:        a.CreatedAt,
		UpdatedAt:        a.UpdatedAt,
		LiveStatus:       domain.AgentLiveStatus(a.LiveStatus),
		ActiveTaskID:     a.ActiveTaskID,
		ActivePhase:      a.ActivePhase,
		LastErrorClass:   a.LastErrorClass,
	}
}

// coalesceStrings returns a non-nil slice so agent Repos/RepoGroups serialize as
// [] rather than null. fleet-db omits empty repos/repo_groups on the wire, which
// decodes to a nil slice here; the web response contract (and the UI) require an
// array. See domain.Agent.Repos.
func coalesceStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (s *agentStore) Create(ctx context.Context, in store.AgentCreate) (*domain.Agent, error) {
	body := struct {
		Name             string             `json:"name"`
		RoleName         string             `json:"role_name"`
		Auto             bool               `json:"auto,omitempty"`
		Backend          string             `json:"backend,omitempty"`
		FallbackBackends []string           `json:"fallback_backends,omitempty"`
		Repos            []string           `json:"repos,omitempty"`
		RepoGroups       []string           `json:"repo_groups,omitempty"`
		CrossRepo        bool               `json:"cross_repo,omitempty"`
		Parent           string             `json:"parent,omitempty"`
		Mode             string             `json:"mode,omitempty"`
		TaskFilter       string             `json:"task_filter,omitempty"`
		MaxConcurrency   int                `json:"max_concurrency,omitempty"`
		BudgetPolicy     string             `json:"budget_policy,omitempty"`
		DesiredState     string             `json:"desired_state,omitempty"`
		DrainNodeID      string             `json:"drain_node_id,omitempty"`
		DrainExpiresAt   *time.Time         `json:"drain_expires_at,omitempty"`
		Hooks            *domain.AgentHooks `json:"hooks,omitempty"`
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
		DrainNodeID:      in.DrainNodeID,
		DrainExpiresAt:   in.DrainExpiresAt,
		Hooks:            in.Hooks.Clone(),
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

// agentUpdateWire is the PATCH body for an agent update, mirroring fleet-db's
// strict update contract. Split out of Update so the request construction is
// one named thing rather than a 50-line anonymous struct literal.
type agentUpdateWire struct {
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
	// Drain metadata. fleet-db derives a clear from any desired_state that is
	// not "draining", so these only ever need sending on a drain stamp.
	DrainNodeID    *string    `json:"drain_node_id,omitempty"`
	DrainExpiresAt *time.Time `json:"drain_expires_at,omitempty"`
	// A non-nil empty object is the explicit clear marker; omitempty only
	// drops a nil pointer, so {} still reaches fleet-db.
	Hooks *domain.AgentHooks `json:"hooks,omitempty"`
}

// newAgentUpdateWire projects the store-level patch onto the wire body,
// stringifying the typed enums that fleet-db carries as plain strings.
func newAgentUpdateWire(patch store.AgentUpdate) agentUpdateWire {
	body := agentUpdateWire{
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
		DrainNodeID:      patch.DrainNodeID,
		DrainExpiresAt:   patch.DrainExpiresAt,
		Hooks:            patch.Hooks.Clone(),
	}
	if patch.State != nil {
		v := string(*patch.State)
		body.State = &v
	}
	if patch.Mode != nil {
		v := string(*patch.Mode)
		body.Mode = &v
	}
	if patch.DesiredState != nil {
		v := string(*patch.DesiredState)
		body.DesiredState = &v
	}
	return body
}

func (s *agentStore) Update(ctx context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	if !agentUpdateHasFleetDBFields(patch) {
		return s.Get(ctx, ws, name)
	}
	body := newAgentUpdateWire(patch)
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
		patch.DesiredState != nil ||
		patch.DrainNodeID != nil ||
		patch.DrainExpiresAt != nil ||
		patch.Hooks != nil
}

func (s *agentStore) Delete(ctx context.Context, ws, name string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/agents/"+pathEscape(name), nil, nil)
}
