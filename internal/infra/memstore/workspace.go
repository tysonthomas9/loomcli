package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workspaceStore struct {
	mu    sync.RWMutex
	items map[string]*workspaceowner.Workspace // keyed by Workspace.Key
}

func newWorkspaceStore() *workspaceStore {
	return &workspaceStore{items: make(map[string]*workspaceowner.Workspace)}
}

// Compile-time check.
var _ workspaceowner.WorkspaceStore = (*workspaceStore)(nil)

func (s *workspaceStore) Create(_ context.Context, in workspaceowner.WorkspaceCreate) (*workspaceowner.Workspace, error) {
	if in.Key == "" {
		return nil, fmt.Errorf("workspace key required: %w", persistence.ErrInvalid)
	}
	if in.Name == "" {
		return nil, fmt.Errorf("workspace name required: %w", persistence.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[in.Key]; ok {
		return nil, fmt.Errorf("workspace %q: %w", in.Key, persistence.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	ws := &workspaceowner.Workspace{
		Key:           in.Key,
		Name:          in.Name,
		Description:   in.Description,
		DefaultBranch: in.DefaultBranch,
		DesignFormat:  in.DesignFormat,
		State:         workspaceowner.StateReady,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.items[in.Key] = ws
	return cloneWorkspace(ws), nil
}

func (s *workspaceStore) Get(_ context.Context, key string) (*workspaceowner.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ws, ok := s.items[key]
	if !ok {
		return nil, fmt.Errorf("workspace %q: %w", key, persistence.ErrNotFound)
	}
	return cloneWorkspace(ws), nil
}

func (s *workspaceStore) GetByName(_ context.Context, name string) (*workspaceowner.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ws := range s.items {
		if ws.Name == name {
			return cloneWorkspace(ws), nil
		}
	}
	return nil, fmt.Errorf("workspace name %q: %w", name, persistence.ErrNotFound)
}

func (s *workspaceStore) List(_ context.Context) ([]*workspaceowner.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*workspaceowner.Workspace, 0, len(s.items))
	for _, ws := range s.items {
		out = append(out, cloneWorkspace(ws))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *workspaceStore) Update(_ context.Context, key string, patch workspaceowner.WorkspaceUpdate) (*workspaceowner.Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.items[key]
	if !ok {
		return nil, fmt.Errorf("workspace %q: %w", key, persistence.ErrNotFound)
	}
	if patch.Name != nil {
		if *patch.Name == "" {
			return nil, fmt.Errorf("workspace name cannot be empty: %w", persistence.ErrInvalid)
		}
		ws.Name = *patch.Name
	}
	if patch.Description != nil {
		ws.Description = *patch.Description
	}
	if patch.DefaultBranch != nil {
		ws.DefaultBranch = *patch.DefaultBranch
	}
	if patch.DesignFormat != nil {
		ws.DesignFormat = *patch.DesignFormat
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
		return fmt.Errorf("workspace %q: %w", key, persistence.ErrNotFound)
	}
	delete(s.items, key)
	return nil
}

// cloneWorkspace returns a deep-ish copy so callers can't mutate
// internal state. Workspace currently has no slice/map fields so a
// shallow struct copy suffices; if fields are added (e.g., Tags []string),
// extend this helper.
func cloneWorkspace(ws *workspaceowner.Workspace) *workspaceowner.Workspace {
	out := *ws
	return &out
}
