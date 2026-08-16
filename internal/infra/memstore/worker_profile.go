package memstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workerProfileStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*execution.WorkerProfile
	services *agentServiceStore
}

func newWorkerProfileStore() *workerProfileStore {
	return &workerProfileStore{items: make(map[string]map[string]*execution.WorkerProfile)}
}

var _ execution.WorkerProfileStore = (*workerProfileStore)(nil)

func (s *workerProfileStore) Create(_ context.Context, in execution.WorkerProfileCreate) (*execution.WorkerProfile, error) {
	profileID := strings.TrimSpace(in.ProfileID)
	if in.WorkspaceKey == "" || profileID == "" || strings.TrimSpace(in.Role) == "" {
		return nil, fmt.Errorf("workspace_key + profile_id + role required: %w", persistence.ErrInvalid)
	}
	name := in.Name
	if strings.TrimSpace(name) == "" {
		name = profileID
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	now := time.Now().UTC()
	profile := &execution.WorkerProfile{
		WorkspaceKey:  in.WorkspaceKey,
		ProfileID:     profileID,
		Name:          name,
		Role:          in.Role,
		Backend:       in.Backend,
		RuntimePolicy: cloneMap(in.RuntimePolicy),
		Repos:         cloneStringSlice(in.Repos),
		MaxPriority:   clonePtr(in.MaxPriority),
		MaxParallel:   in.MaxParallel,
		ParentEpic:    in.ParentEpic,
		Labels:        cloneStringSlice(in.Labels),
		Capabilities:  cloneStringSlice(in.Capabilities),
		Enabled:       enabled,
		Metadata:      cloneMap(in.Metadata),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := validateWorkerProfileMem(profile); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[profile.WorkspaceKey] == nil {
		s.items[profile.WorkspaceKey] = make(map[string]*execution.WorkerProfile)
	}
	if _, ok := s.items[profile.WorkspaceKey][profile.ProfileID]; ok {
		return nil, fmt.Errorf("worker profile %q in workspace %q: %w", profile.ProfileID, profile.WorkspaceKey, persistence.ErrAlreadyExists)
	}
	s.items[profile.WorkspaceKey][profile.ProfileID] = profile
	return cloneWorkerProfile(profile), nil
}

func (s *workerProfileStore) Get(_ context.Context, ws, profileID string) (*execution.WorkerProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.items[ws][profileID]
	if !ok {
		return nil, fmt.Errorf("worker profile %q in workspace %q: %w", profileID, ws, persistence.ErrNotFound)
	}
	return cloneWorkerProfile(profile), nil
}

func (s *workerProfileStore) List(_ context.Context, ws string, filter execution.WorkerProfileFilter) ([]*execution.WorkerProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*execution.WorkerProfile, 0, len(s.items[ws]))
	for _, profile := range s.items[ws] {
		if workerProfileMatchesMem(profile, filter) {
			out = append(out, cloneWorkerProfile(profile))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *workerProfileStore) Update(_ context.Context, ws, profileID string, patch execution.WorkerProfileUpdate) (*execution.WorkerProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.items[ws][profileID]
	if !ok {
		return nil, fmt.Errorf("worker profile %q in workspace %q: %w", profileID, ws, persistence.ErrNotFound)
	}
	updated := cloneWorkerProfile(profile)
	if patch.ExpectedParentEpic != nil && updated.ParentEpic != *patch.ExpectedParentEpic {
		return nil, persistence.ErrConflict
	}
	applyWorkerProfileUpdateMem(updated, patch)
	updated.ProfileID = strings.TrimSpace(updated.ProfileID)
	if strings.TrimSpace(updated.Name) == "" {
		updated.Name = updated.ProfileID
	}
	updated.UpdatedAt = time.Now().UTC()
	if err := validateWorkerProfileMem(updated); err != nil {
		return nil, err
	}
	s.items[ws][profileID] = updated
	return cloneWorkerProfile(updated), nil
}

func (s *workerProfileStore) Delete(_ context.Context, ws, profileID string) error {
	if s.services != nil && s.services.hasProfile(ws, profileID) {
		return fmt.Errorf("worker profile %q in workspace %q is used by agent service: %w", profileID, ws, persistence.ErrInvalidTransition)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][profileID]; !ok {
		return fmt.Errorf("worker profile %q in workspace %q: %w", profileID, ws, persistence.ErrNotFound)
	}
	delete(s.items[ws], profileID)
	return nil
}

func (s *workerProfileStore) exists(ws, profileID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[ws][profileID]
	return ok
}

func validateWorkerProfileMem(profile *execution.WorkerProfile) error {
	if profile.WorkspaceKey == "" || profile.ProfileID == "" || strings.TrimSpace(profile.Role) == "" {
		return fmt.Errorf("workspace_key + profile_id + role required: %w", persistence.ErrInvalid)
	}
	if profile.MaxPriority != nil && (*profile.MaxPriority < 0 || *profile.MaxPriority > 4) {
		return fmt.Errorf("worker profile %q max_priority must be between 0 and 4: %w", profile.ProfileID, persistence.ErrInvalid)
	}
	if profile.MaxParallel < 0 {
		return fmt.Errorf("worker profile %q max_parallel must be non-negative: %w", profile.ProfileID, persistence.ErrInvalid)
	}
	if profile.CreatedAt.IsZero() || profile.UpdatedAt.IsZero() {
		return fmt.Errorf("worker profile %q timestamps required: %w", profile.ProfileID, persistence.ErrInvalid)
	}
	return nil
}

func cloneWorkerProfile(profile *execution.WorkerProfile) *execution.WorkerProfile {
	if profile == nil {
		return nil
	}
	out := *profile
	out.RuntimePolicy = cloneMap(profile.RuntimePolicy)
	out.Repos = cloneStringSlice(profile.Repos)
	out.MaxPriority = clonePtr(profile.MaxPriority)
	out.Labels = cloneStringSlice(profile.Labels)
	out.Capabilities = cloneStringSlice(profile.Capabilities)
	out.Metadata = cloneMap(profile.Metadata)
	return &out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func workerProfileMatchesMem(profile *execution.WorkerProfile, filter execution.WorkerProfileFilter) bool {
	return (filter.Role == "" || profile.Role == filter.Role) &&
		(filter.Backend == "" || profile.Backend == filter.Backend) &&
		(filter.Enabled == nil || profile.Enabled == *filter.Enabled)
}

func applyWorkerProfileUpdateMem(profile *execution.WorkerProfile, patch execution.WorkerProfileUpdate) {
	if patch.Name != nil {
		profile.Name = *patch.Name
	}
	if patch.Role != nil {
		profile.Role = *patch.Role
	}
	if patch.Backend != nil {
		profile.Backend = *patch.Backend
	}
	if patch.RuntimePolicy != nil {
		profile.RuntimePolicy = cloneMap(*patch.RuntimePolicy)
	}
	if patch.Repos != nil {
		profile.Repos = cloneStringSlice(*patch.Repos)
	}
	if patch.ClearMaxPriority {
		profile.MaxPriority = nil
	} else if patch.MaxPriority != nil {
		profile.MaxPriority = clonePtr(patch.MaxPriority)
	}
	if patch.ParentEpic != nil {
		profile.ParentEpic = *patch.ParentEpic
	}
	if patch.MaxParallel != nil {
		profile.MaxParallel = *patch.MaxParallel
	}
	if patch.Labels != nil {
		profile.Labels = cloneStringSlice(*patch.Labels)
	}
	if patch.Capabilities != nil {
		profile.Capabilities = cloneStringSlice(*patch.Capabilities)
	}
	if patch.Enabled != nil {
		profile.Enabled = *patch.Enabled
	}
	if patch.Metadata != nil {
		profile.Metadata = cloneMap(*patch.Metadata)
	}
}
