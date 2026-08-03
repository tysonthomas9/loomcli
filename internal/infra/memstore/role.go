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

type roleStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.Role // wsKey → name → Role
	services *agentServiceStore
}

func newRoleStore() *roleStore {
	return &roleStore{items: make(map[string]map[string]*domain.Role)}
}

var _ store.RoleStore = (*roleStore)(nil)

func (s *roleStore) Create(_ context.Context, in store.RoleCreate) (*domain.Role, error) {
	if in.WorkspaceKey == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Role)
	}
	if _, ok := s.items[in.WorkspaceKey][in.Name]; ok {
		return nil, fmt.Errorf("role %q in workspace %q: %w", in.Name, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	r := &domain.Role{
		WorkspaceKey:   in.WorkspaceKey,
		Name:           in.Name,
		Kind:           domain.RoleKind(in.Kind),
		Description:    in.Description,
		Prompt:         in.Prompt,
		PromptFile:     in.PromptFile,
		Model:          in.Model,
		TaskFilter:     in.TaskFilter,
		Executor:       in.Executor,
		Backend:        in.Backend,
		Effort:         in.Effort,
		PathPatterns:   append([]string(nil), in.PathPatterns...),
		Skills:         append([]string(nil), in.Skills...),
		InputPolicy:    in.InputPolicy.Clone(),
		MaxPriority:    clonePtr(in.MaxPriority),
		MaxConcurrency: clonePtr(in.MaxConcurrency),
		ReadOnly:       in.ReadOnly,
		AllowedTools:   append([]string(nil), in.AllowedTools...),
		DeniedTools:    append([]string(nil), in.DeniedTools...),
		MaxBudgetUSD:   clonePtr(in.MaxBudgetUSD),
		MaxRunDuration: clonePtr(in.MaxRunDuration),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	s.items[in.WorkspaceKey][in.Name] = r
	return cloneRole(r), nil
}

func (s *roleStore) Get(_ context.Context, ws, name string) (*domain.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("role %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return cloneRole(r), nil
}

func (s *roleStore) List(_ context.Context, ws string) ([]*domain.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wsRoles := s.items[ws]
	out := make([]*domain.Role, 0, len(wsRoles))
	for _, r := range wsRoles {
		out = append(out, cloneRole(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

//nolint:funlen // Patch application mirrors the store.RoleUpdate surface area.
func (s *roleStore) Update(_ context.Context, ws, name string, patch store.RoleUpdate) (*domain.Role, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("role %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	if patch.Description != nil {
		r.Description = *patch.Description
	}
	if patch.Kind != nil {
		r.Kind = domain.RoleKind(*patch.Kind)
	}
	if patch.Prompt != nil {
		r.Prompt = *patch.Prompt
	}
	if patch.PromptFile != nil {
		r.PromptFile = *patch.PromptFile
	}
	if patch.Model != nil {
		r.Model = *patch.Model
	}
	if patch.TaskFilter != nil {
		r.TaskFilter = *patch.TaskFilter
	}
	if patch.Executor != nil {
		r.Executor = *patch.Executor
	}
	if patch.Backend != nil {
		r.Backend = *patch.Backend
	}
	if patch.Effort != nil {
		r.Effort = *patch.Effort
	}
	if patch.PathPatterns != nil {
		r.PathPatterns = append([]string(nil), (*patch.PathPatterns)...)
	}
	if patch.Skills != nil {
		r.Skills = append([]string(nil), (*patch.Skills)...)
	}
	if patch.InputPolicy != nil {
		// Deep-copied on the way in as well as on the way out: the caller keeps
		// a reference to the Kinds map it built, and a shared map would let it
		// flip a role's disposition after the store accepted the patch.
		r.InputPolicy = (*patch.InputPolicy).Clone()
	}
	if patch.MaxPriority != nil {
		r.MaxPriority = clonePtr(*patch.MaxPriority)
	}
	if patch.MaxConcurrency != nil {
		r.MaxConcurrency = clonePtr(*patch.MaxConcurrency)
	}
	if patch.ReadOnly != nil {
		r.ReadOnly = *patch.ReadOnly
	}
	if patch.AllowedTools != nil {
		r.AllowedTools = append([]string(nil), (*patch.AllowedTools)...)
	}
	if patch.DeniedTools != nil {
		r.DeniedTools = append([]string(nil), (*patch.DeniedTools)...)
	}
	if patch.MaxBudgetUSD != nil {
		r.MaxBudgetUSD = clonePtr(*patch.MaxBudgetUSD)
	}
	if patch.MaxRunDuration != nil {
		r.MaxRunDuration = clonePtr(*patch.MaxRunDuration)
	}
	r.UpdatedAt = time.Now().UTC()
	return cloneRole(r), nil
}

func (s *roleStore) Delete(_ context.Context, ws, name string) error {
	if s.services != nil && s.services.hasRole(ws, name) {
		return fmt.Errorf("role %q in workspace %q is used by agent service: %w", name, ws, domain.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][name]; !ok {
		return fmt.Errorf("role %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], name)
	return nil
}

func (s *roleStore) exists(ws, name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][name]
	return ok
}

func cloneRole(r *domain.Role) *domain.Role {
	out := *r
	out.PathPatterns = append([]string(nil), r.PathPatterns...)
	out.Skills = append([]string(nil), r.Skills...)
	out.AllowedTools = append([]string(nil), r.AllowedTools...)
	out.DeniedTools = append([]string(nil), r.DeniedTools...)
	out.InputPolicy = r.InputPolicy.Clone()
	out.MaxPriority = clonePtr(r.MaxPriority)
	out.MaxConcurrency = clonePtr(r.MaxConcurrency)
	out.MaxBudgetUSD = clonePtr(r.MaxBudgetUSD)
	out.MaxRunDuration = clonePtr(r.MaxRunDuration)
	return &out
}
