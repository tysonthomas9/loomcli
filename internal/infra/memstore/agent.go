package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type agentStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Agent // wsKey → name → Agent
}

func newAgentStore() *agentStore {
	return &agentStore{items: make(map[string]map[string]*domain.Agent)}
}

var _ store.AgentStore = (*agentStore)(nil)

func (s *agentStore) Create(_ context.Context, in store.AgentCreate) (*domain.Agent, error) {
	if in.WorkspaceKey == "" || in.Name == "" || in.RoleName == "" {
		return nil, fmt.Errorf("workspace_key + name + role_name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Agent)
	}
	if _, ok := s.items[in.WorkspaceKey][in.Name]; ok {
		return nil, fmt.Errorf("agent %q in workspace %q: %w", in.Name, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	a := &domain.Agent{
		WorkspaceKey:     in.WorkspaceKey,
		Name:             in.Name,
		RoleName:         in.RoleName,
		Auto:             in.Auto,
		Backend:          in.Backend,
		FallbackBackends: append([]string(nil), in.FallbackBackends...),
		Repos:            append([]string(nil), in.Repos...),
		RepoGroups:       append([]string(nil), in.RepoGroups...),
		CrossRepo:        in.CrossRepo,
		Parent:           in.Parent,
		State:            domain.AgentStateIdle,
		Mode:             in.Mode,
		TaskFilter:       in.TaskFilter,
		MaxConcurrency:   in.MaxConcurrency,
		BudgetPolicy:     in.BudgetPolicy,
		DesiredState:     in.DesiredState,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if !in.Hooks.IsEmpty() {
		a.Hooks = in.Hooks.Clone()
	}
	s.items[in.WorkspaceKey][in.Name] = a
	return cloneAgent(a), nil
}

func (s *agentStore) Get(_ context.Context, ws, name string) (*domain.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("agent %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return cloneAgent(a), nil
}

func (s *agentStore) List(_ context.Context, ws string) ([]*domain.Agent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wsAgents := s.items[ws]
	out := make([]*domain.Agent, 0, len(wsAgents))
	for _, a := range wsAgents {
		out = append(out, cloneAgent(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *agentStore) Update(_ context.Context, ws, name string, patch store.AgentUpdate) (*domain.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("agent %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	applyAgentPatch(a, patch)
	a.UpdatedAt = time.Now().UTC()
	return cloneAgent(a), nil
}

func applyAgentPatch(a *domain.Agent, patch store.AgentUpdate) {
	if patch.RoleName != nil {
		a.RoleName = *patch.RoleName
	}
	if patch.Auto != nil {
		a.Auto = *patch.Auto
	}
	if patch.Backend != nil {
		a.Backend = *patch.Backend
	}
	applyAgentRoutingPatch(a, patch)
	applyAgentRuntimePatch(a, patch)
}

func applyAgentRoutingPatch(a *domain.Agent, patch store.AgentUpdate) {
	if patch.FallbackBackends != nil {
		a.FallbackBackends = append([]string(nil), (*patch.FallbackBackends)...)
	}
	if patch.Repos != nil {
		a.Repos = append([]string(nil), (*patch.Repos)...)
	}
	if patch.RepoGroups != nil {
		a.RepoGroups = append([]string(nil), (*patch.RepoGroups)...)
	}
	if patch.CrossRepo != nil {
		a.CrossRepo = *patch.CrossRepo
	}
	if patch.TaskFilter != nil {
		a.TaskFilter = *patch.TaskFilter
	}
	if patch.MaxConcurrency != nil {
		a.MaxConcurrency = *patch.MaxConcurrency
	}
	if patch.BudgetPolicy != nil {
		a.BudgetPolicy = *patch.BudgetPolicy
	}
}

func applyAgentRuntimePatch(a *domain.Agent, patch store.AgentUpdate) {
	if patch.Parent != nil {
		a.Parent = *patch.Parent
	}
	if patch.State != nil {
		a.State = *patch.State
	}
	if patch.Mode != nil {
		a.Mode = *patch.Mode
	}
	if patch.DesiredState != nil {
		a.DesiredState = *patch.DesiredState
	}
	if patch.Hooks != nil {
		// A non-nil empty pipeline is the explicit clear marker.
		if patch.Hooks.IsEmpty() {
			a.Hooks = nil
		} else {
			a.Hooks = patch.Hooks.Clone()
		}
	}
}

func (s *agentStore) Delete(_ context.Context, ws, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][name]; !ok {
		return fmt.Errorf("agent %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], name)
	return nil
}

func cloneAgent(a *domain.Agent) *domain.Agent {
	out := *a
	out.FallbackBackends = append([]string(nil), a.FallbackBackends...)
	out.Repos = append([]string(nil), a.Repos...)
	out.RepoGroups = append([]string(nil), a.RepoGroups...)
	out.Hooks = a.Hooks.Clone()
	return &out
}
