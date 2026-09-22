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

type repoStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.Repo // wsKey → name → Repo
}

func newRepoStore() *repoStore {
	return &repoStore{items: make(map[string]map[string]*domain.Repo)}
}

var _ store.RepoStore = (*repoStore)(nil)

func (s *repoStore) Create(_ context.Context, in store.RepoCreate) (*domain.Repo, error) {
	if in.WorkspaceKey == "" || in.Name == "" {
		return nil, fmt.Errorf("workspace_key + name required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.Repo)
	}
	if _, ok := s.items[in.WorkspaceKey][in.Name]; ok {
		return nil, fmt.Errorf("repo %q in workspace %q: %w", in.Name, in.WorkspaceKey, domain.ErrAlreadyExists)
	}
	now := time.Now().UTC()
	source := in.SourceRepoID
	if source == "" {
		source = in.Name
	}
	r := &domain.Repo{
		WorkspaceKey:            in.WorkspaceKey,
		Name:                    in.Name,
		RemoteURL:               in.RemoteURL,
		Remote:                  in.Remote,
		DefaultBranch:           in.DefaultBranch,
		Groups:                  append([]string(nil), in.Groups...),
		SourceRepoID:            source,
		TaskDeliveryRequirement: in.TaskDeliveryRequirement,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	s.items[in.WorkspaceKey][in.Name] = r
	return cloneRepo(r), nil
}

func (s *repoStore) Get(_ context.Context, ws, name string) (*domain.Repo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("repo %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	return cloneRepo(r), nil
}

func (s *repoStore) List(_ context.Context, ws string) ([]*domain.Repo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wsRepos := s.items[ws]
	out := make([]*domain.Repo, 0, len(wsRepos))
	for _, r := range wsRepos {
		out = append(out, cloneRepo(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *repoStore) Update(_ context.Context, ws, name string, patch store.RepoUpdate) (*domain.Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.items[ws][name]
	if !ok {
		return nil, fmt.Errorf("repo %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	if patch.RemoteURL != nil {
		r.RemoteURL = *patch.RemoteURL
	}
	if patch.Remote != nil {
		r.Remote = *patch.Remote
	}
	if patch.DefaultBranch != nil {
		r.DefaultBranch = *patch.DefaultBranch
	}
	if patch.Groups != nil {
		r.Groups = append([]string(nil), (*patch.Groups)...)
	}
	if patch.SourceRepoID != nil {
		r.SourceRepoID = *patch.SourceRepoID
	}
	if patch.TaskDeliveryRequirement != nil {
		r.TaskDeliveryRequirement = *patch.TaskDeliveryRequirement
	}
	r.UpdatedAt = time.Now().UTC()
	return cloneRepo(r), nil
}

func (s *repoStore) Delete(_ context.Context, ws, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][name]; !ok {
		return fmt.Errorf("repo %q in workspace %q: %w", name, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], name)
	return nil
}

func cloneRepo(r *domain.Repo) *domain.Repo {
	out := *r
	out.Groups = append([]string(nil), r.Groups...)
	return &out
}
