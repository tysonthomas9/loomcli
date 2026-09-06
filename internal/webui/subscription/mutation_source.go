package subscription

import (
	"context"
	"fmt"

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
	manager   *MultiWorkspaceSubscriber
	entry     *subscriberEntry
	workspace string
}

func (s *boundMutationSource) check(ctx context.Context) error {
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
		if result.Workspace != s.workspace {
			return backend.IssueRecoverySnapshot{}, fmt.Errorf("recovery workspace differs from captured source")
		}
		return result, nil
	})
}

func (s *boundMutationSource) ReadHead(ctx context.Context) (backend.MutationPage, error) {
	return readBoundSource(s, ctx, func(ctx context.Context) (backend.MutationPage, error) { return s.entry.sub.GetMutationHead(ctx) })
}
func (s *boundMutationSource) ReadPage(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	return readBoundSource(s, ctx, func(ctx context.Context) (backend.MutationPage, error) {
		return s.entry.sub.GetMutationPageThrough(ctx, since, through, limit)
	})
}
