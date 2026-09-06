package subscription

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// OpenMutationSource captures one subscriber identity for the connection's
// lifetime. Subsequent reads never select a replacement, even with equal cursors.
func (m *MultiWorkspaceSubscriber) OpenMutationSource(ctx context.Context, workspace string) (realtime.MutationSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	entry := m.subscribers[workspace]
	m.mu.RUnlock()
	source := &boundMutationSource{manager: m, entry: entry, workspace: workspace}
	if err := source.check(ctx); err != nil {
		return nil, err
	}
	return source, nil
}

type boundMutationSource struct {
	manager    *MultiWorkspaceSubscriber
	entry      *subscriberEntry
	workspace  string
	identityMu sync.Mutex
	identity   string
	retired    bool
}

func (s *boundMutationSource) check(ctx context.Context) error {
	s.identityMu.Lock()
	retired := s.retired
	s.identityMu.Unlock()
	if retired {
		return backend.ErrMutationSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.manager.mu.RLock()
	valid := s.entry != nil && !s.manager.closed && s.manager.subscribers[s.workspace] == s.entry
	s.manager.mu.RUnlock()
	if !valid {
		return fmt.Errorf("workspace %q mutation source retired or unavailable", s.workspace)
	}
	return nil
}

func readBoundSource[T any](s *boundMutationSource, ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := s.check(ctx); err != nil {
		return zero, err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	result, err := fn(requestCtx)
	if err != nil {
		if errors.Is(err, backend.ErrMutationSourceChanged) {
			s.identityMu.Lock()
			s.retired = true
			s.identityMu.Unlock()
		}
		return zero, err
	}
	if err := s.check(requestCtx); err != nil {
		return zero, err
	}
	return result, nil
}

// ReadIssueRecovery is an optional capability on the exact source returned to
// SSE. It never selects a fresh entry by workspace or substitutes ordinary reads.
func (s *boundMutationSource) ReadIssueRecovery(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
	return readBoundSource(s, ctx, func(ctx context.Context) (backend.IssueRecoverySnapshot, error) {
		reader, ok := s.entry.sub.(backend.IssueRecoveryBackend)
		if !ok {
			return backend.IssueRecoverySnapshot{}, fmt.Errorf("workspace %q source does not support certified issue recovery", s.workspace)
		}
		result, err := reader.ReadIssueRecovery(ctx)
		if err != nil {
			return backend.IssueRecoverySnapshot{}, err
		}
		if err := s.checkIdentity(result.SourceIdentity, false); err != nil {
			return backend.IssueRecoverySnapshot{}, err
		}
		if result.Workspace != s.workspace {
			return backend.IssueRecoverySnapshot{}, fmt.Errorf("recovery workspace differs from captured source")
		}
		return result, nil
	})
}

func (s *boundMutationSource) ReadHead(ctx context.Context) (backend.MutationPage, error) {
	return readBoundSource(s, ctx, func(ctx context.Context) (backend.MutationPage, error) {
		page, err := s.entry.sub.GetMutationHead(ctx)
		if err != nil {
			return backend.MutationPage{}, err
		}
		if err := s.checkIdentity(page.SourceIdentity, true); err != nil {
			return backend.MutationPage{}, err
		}
		return page, nil
	})
}
func (s *boundMutationSource) ReadPage(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return readBoundSource(s, ctx, func(ctx context.Context) (backend.MutationPage, error) {
		page, err := s.entry.sub.GetMutationPageThrough(ctx, since, through, limit)
		if err != nil {
			return backend.MutationPage{}, err
		}
		if err := s.checkIdentity(page.SourceIdentity, false); err != nil {
			return backend.MutationPage{}, err
		}
		return page, nil
	})
}

func (s *boundMutationSource) checkIdentity(identity string, establish bool) error {
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	if s.retired || !backend.ValidSourceIdentity(identity) {
		s.retired = true
		return backend.ErrMutationSourceChanged
	}
	if s.identity == "" && establish {
		s.identity = identity
	}
	if s.identity != identity {
		s.retired = true
		return backend.ErrMutationSourceChanged
	}
	return nil
}
