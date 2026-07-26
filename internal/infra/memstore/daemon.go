package memstore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type daemonStore struct {
	mu    sync.RWMutex
	items map[string]*domain.DaemonProfile // wsKey → profile
}

func newDaemonStore() *daemonStore {
	return &daemonStore{items: make(map[string]*domain.DaemonProfile)}
}

var _ store.DaemonProfileStore = (*daemonStore)(nil)

// Get returns the profile for ws, or a default-valued profile if no
// explicit settings have been written. To match the contract, this
// returns ErrNotFound only when callers would otherwise observe stale
// state — the in-memory implementation always has a workspace context
// implicit in its keys, so we synthesize defaults instead. Production
// fleet-db Get must verify the workspace exists.
func (s *daemonStore) Get(_ context.Context, ws string) (*domain.DaemonProfile, error) {
	if ws == "" {
		return nil, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.items[ws]; ok {
		return cloneDaemonProfile(p), nil
	}
	return defaultDaemonProfile(ws), nil
}

func (s *daemonStore) Upsert(_ context.Context, profile *domain.DaemonProfile) (*domain.DaemonProfile, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile required: %w", domain.ErrInvalid)
	}
	if profile.WorkspaceKey == "" {
		return nil, fmt.Errorf("workspace_key required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := cloneDaemonProfile(profile)
	out.UpdatedAt = time.Now().UTC()
	s.items[profile.WorkspaceKey] = out
	return cloneDaemonProfile(out), nil
}

func defaultDaemonProfile(ws string) *domain.DaemonProfile {
	return &domain.DaemonProfile{
		WorkspaceKey: ws,
		IssueBackend: "fleetdb",
	}
}

func cloneDaemonProfile(p *domain.DaemonProfile) *domain.DaemonProfile {
	out := *p
	if p.OTel != nil {
		ot := *p.OTel
		out.OTel = &ot
	}
	out.RestartPolicy = cloneRestartPolicy(p.RestartPolicy)
	out.MaxAgents = clonePtr(p.MaxAgents)
	out.StartupTimeout = clonePtr(p.StartupTimeout)
	return &out
}

func cloneRestartPolicy(rp domain.RestartPolicy) domain.RestartPolicy {
	return domain.RestartPolicy{
		MaxRetries:       clonePtr(rp.MaxRetries),
		BackoffInitial:   clonePtr(rp.BackoffInitial),
		BackoffMax:       clonePtr(rp.BackoffMax),
		OutputTimeout:    clonePtr(rp.OutputTimeout),
		RateLimitBackoff: clonePtr(rp.RateLimitBackoff),
		RateLimitMaxWait: clonePtr(rp.RateLimitMaxWait),
		RateLimitNoCount: clonePtr(rp.RateLimitNoCount),
		TimeoutBackoff:   clonePtr(rp.TimeoutBackoff),
		NoWorkBackoff:    clonePtr(rp.NoWorkBackoff),
		NoWorkBackoffMax: clonePtr(rp.NoWorkBackoffMax),
		IdlePollInterval: clonePtr(rp.IdlePollInterval),
		YieldTimeout:     clonePtr(rp.YieldTimeout),
		SigtermTimeout:   clonePtr(rp.SigtermTimeout),
	}
}
