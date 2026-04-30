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

type workspaceStore struct {
	mu    sync.RWMutex
	items map[string]*domain.Workspace // keyed by Workspace.Key
}

func newWorkspaceStore() *workspaceStore {
	return &workspaceStore{items: make(map[string]*domain.Workspace)}
}

// Compile-time check.
var _ store.WorkspaceStore = (*workspaceStore)(nil)

func (s *workspaceStore) Create(_ context.Context, in store.WorkspaceCreate) (*domain.Workspace, error) {
	if in.Key == "" {
		return nil, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("workspace name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[in.Key]; ok {
		return nil, fmt.Errorf("workspace %q: %w", in.Key, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	ws := &domain.Workspace{
		Key:           in.Key,
		Name:          in.Name,
		Description:   in.Description,
		DefaultBranch: in.DefaultBranch,
		State:         domain.WorkspaceStateReady,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.items[in.Key] = ws
	return cloneWorkspace(ws), nil
}

func (s *workspaceStore) Get(_ context.Context, key string) (*domain.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, ok := s.items[key]
	if !ok {
		return nil, fmt.Errorf("workspace %q: %w", key, domain.ErrNotFound)
	}
	return cloneWorkspace(ws), nil
}

func (s *workspaceStore) GetByName(_ context.Context, name string) (*domain.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.items {
		if ws.Name == name {
			return cloneWorkspace(ws), nil
		}
	}
	return nil, fmt.Errorf("workspace name %q: %w", name, domain.ErrNotFound)
}

func (s *workspaceStore) List(_ context.Context) ([]*domain.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.Workspace, 0, len(s.items))
	for _, ws := range s.items {
		out = append(out, cloneWorkspace(ws))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *workspaceStore) Update(_ context.Context, key string, patch store.WorkspaceUpdate) (*domain.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.items[key]
	if !ok {
		return nil, fmt.Errorf("workspace %q: %w", key, domain.ErrNotFound)
	}
	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, fmt.Errorf("workspace name cannot be empty: %w", domain.ErrInvalid)
		}
		ws.Name = *patch.Name
	}
	if patch.Description != nil {
		ws.Description = *patch.Description
	}
	if patch.DefaultBranch != nil {
		ws.DefaultBranch = *patch.DefaultBranch
	}
	if patch.State != nil {
		ws.State = *patch.State
	}
	if patch.ErrorMessage != nil {
		ws.ErrorMessage = *patch.ErrorMessage
	}
	ws.UpdatedAt = time.Now().UTC()
	return cloneWorkspace(ws), nil
}

func (s *workspaceStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[key]; !ok {
		return fmt.Errorf("workspace %q: %w", key, domain.ErrNotFound)
	}
	delete(s.items, key)
	return nil
}

// cloneWorkspace returns a deep-ish copy so callers can't mutate
// internal state. Workspace currently has no slice/map fields so a
// shallow struct copy suffices; if fields are added (e.g., Tags []string),
// extend this helper.
func cloneWorkspace(ws *domain.Workspace) *domain.Workspace {
	out := *ws
	return &out
}
