package loomgittest

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

func (s *Store) ClaimLease(_ context.Context, scope, owner string, ttl time.Duration) (loomgit.Lease, error) {
	if scope == "" || owner == "" || ttl <= 0 {
		return loomgit.Lease{}, errors.New("scope, owner and positive TTL are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	l, ok := s.leases[scope]
	if ok && l.ExpiresAt.After(now) {
		return loomgit.Lease{}, journal.ErrLeaseHeld
	}
	l = loomgit.Lease{Scope: scope, Owner: owner, Fence: l.Fence + 1, ExpiresAt: now.Add(ttl)}
	s.leases[scope] = l
	return l, nil
}
func (s *Store) RenewLease(_ context.Context, prior loomgit.Lease, ttl time.Duration) (loomgit.Lease, error) {
	if ttl <= 0 {
		return loomgit.Lease{}, errors.New("positive TTL is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	l, ok := s.leases[prior.Scope]
	if !ok || l.Owner != prior.Owner || l.Fence != prior.Fence || !l.ExpiresAt.After(now) {
		return loomgit.Lease{}, journal.ErrStale
	}
	l.ExpiresAt = now.Add(ttl)
	s.leases[l.Scope] = l
	return l, nil
}
func (s *Store) ReleaseLease(_ context.Context, prior loomgit.Lease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[prior.Scope]
	if !ok || l.Owner != prior.Owner || l.Fence != prior.Fence || !l.ExpiresAt.After(time.Now().UTC()) {
		return journal.ErrStale
	}
	l.ExpiresAt = time.Time{}
	s.leases[l.Scope] = l
	return nil
}
