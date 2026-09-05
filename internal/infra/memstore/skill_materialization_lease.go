package memstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type skillMaterializationLeaseStore struct {
	mu     sync.Mutex
	files  *workspaceFileStore
	leases map[string]*domain.SkillMaterializationLease
	next   uint64
	now    func() time.Time
}

func newSkillMaterializationLeaseStore(files *workspaceFileStore) *skillMaterializationLeaseStore {
	return &skillMaterializationLeaseStore{
		files: files, leases: make(map[string]*domain.SkillMaterializationLease),
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (s *skillMaterializationLeaseStore) Acquire(ctx context.Context, in store.SkillMaterializationLeaseAcquire) (*domain.SkillMaterializationLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, revision := range in.TreeRevisions {
		if _, err := s.files.GetTree(ctx, in.WorkspaceKey, revision); err != nil {
			return nil, err
		}
	}
	now := s.now()
	key := materializationLeaseKey(in.WorkspaceKey, in.TargetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.leases[key]; current != nil && current.ExpiresAt.After(now) {
		return nil, &domain.SkillMaterializationLeaseConflictError{
			Holder: current.Holder, ExpiresAt: current.ExpiresAt,
		}
	}
	s.next++
	lease := &domain.SkillMaterializationLease{
		Token:     fmt.Sprintf("mem-skill-materialization-lease-%d", s.next),
		TargetKey: in.TargetKey, Holder: in.Holder, ExpiresAt: now.Add(in.TTL),
		TreeRevisions: append([]string{}, in.TreeRevisions...),
	}
	s.leases[key] = lease
	return cloneSkillMaterializationLease(lease), nil
}

func (s *skillMaterializationLeaseStore) Renew(ctx context.Context, workspace, targetKey, token string, ttl time.Duration) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	now := s.now()
	key := materializationLeaseKey(workspace, targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	lease := s.leases[key]
	if lease == nil || !lease.ExpiresAt.After(now) || lease.Token != token {
		return time.Time{}, domain.ErrSkillMaterializationLeaseTokenMismatch
	}
	lease.ExpiresAt = now.Add(ttl)
	return lease.ExpiresAt, nil
}

func (s *skillMaterializationLeaseStore) Release(ctx context.Context, workspace, targetKey, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := materializationLeaseKey(workspace, targetKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	lease := s.leases[key]
	if lease == nil {
		return nil
	}
	if lease.Token != token {
		return domain.ErrSkillMaterializationLeaseTokenMismatch
	}
	delete(s.leases, key)
	return nil
}

func materializationLeaseKey(workspace, targetKey string) string {
	return workspace + "\x00" + targetKey
}

func cloneSkillMaterializationLease(lease *domain.SkillMaterializationLease) *domain.SkillMaterializationLease {
	if lease == nil {
		return nil
	}
	out := *lease
	out.TreeRevisions = append([]string{}, lease.TreeRevisions...)
	return &out
}

var _ store.SkillMaterializationLeaseStore = (*skillMaterializationLeaseStore)(nil)
