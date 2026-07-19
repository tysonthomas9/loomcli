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

type sessionEvalStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.SessionEval
}

func newSessionEvalStore() *sessionEvalStore {
	return &sessionEvalStore{items: make(map[string]map[string]*domain.SessionEval)}
}

var _ store.SessionEvalStore = (*sessionEvalStore)(nil)

func (s *sessionEvalStore) Create(_ context.Context, in *domain.SessionEval) (*domain.SessionEval, error) {
	if in == nil || in.WorkspaceKey == "" || in.EvalID == "" || in.SessionID == "" || in.AgentID == "" {
		return nil, fmt.Errorf("workspace_key + eval_id + session_id + agent_id required: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[in.WorkspaceKey] == nil {
		s.items[in.WorkspaceKey] = make(map[string]*domain.SessionEval)
	}
	if _, ok := s.items[in.WorkspaceKey][in.EvalID]; ok {
		return nil, fmt.Errorf("session eval %q in workspace %q: %w", in.EvalID, in.WorkspaceKey, domain.ErrConflict)
	}
	now := time.Now().UTC()
	eval := cloneSessionEval(in)
	if eval.CreatedAt.IsZero() {
		eval.CreatedAt = now
	}
	if eval.UpdatedAt.IsZero() {
		eval.UpdatedAt = eval.CreatedAt
	}
	s.items[in.WorkspaceKey][in.EvalID] = eval
	return cloneSessionEval(eval), nil
}

func (s *sessionEvalStore) Get(_ context.Context, ws, evalID string) (*domain.SessionEval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	eval, ok := s.items[ws][evalID]
	if !ok {
		return nil, fmt.Errorf("session eval %q in workspace %q: %w", evalID, ws, domain.ErrNotFound)
	}
	return cloneSessionEval(eval), nil
}

func (s *sessionEvalStore) Delete(_ context.Context, ws, evalID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[ws][evalID]; !ok {
		return fmt.Errorf("session eval %q in workspace %q: %w", evalID, ws, domain.ErrNotFound)
	}
	delete(s.items[ws], evalID)
	return nil
}

func (s *sessionEvalStore) List(_ context.Context, ws string, filter store.SessionEvalFilter) ([]*domain.SessionEval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	evals := s.items[ws]
	out := make([]*domain.SessionEval, 0, len(evals))
	for _, eval := range evals {
		if !sessionEvalMatches(eval, filter) {
			continue
		}
		out = append(out, cloneSessionEval(eval))
	}
	// fleet-db sorts generic session-evals by created_at descending after
	// filtering, with eval_id as the stable tie-breaker.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].EvalID < out[j].EvalID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func sessionEvalMatches(eval *domain.SessionEval, filter store.SessionEvalFilter) bool {
	if eval == nil {
		return false
	}
	if filter.SessionID != "" && eval.SessionID != filter.SessionID {
		return false
	}
	if filter.TaskID != "" && eval.TaskID != filter.TaskID {
		return false
	}
	if filter.AgentID != "" && eval.AgentID != filter.AgentID {
		return false
	}
	if filter.JudgePromptVersion != "" && eval.JudgePromptVersion != filter.JudgePromptVersion {
		return false
	}
	if filter.Since != nil && eval.CreatedAt.Before(*filter.Since) {
		return false
	}
	if filter.Until != nil && eval.CreatedAt.After(*filter.Until) {
		return false
	}
	return true
}

func cloneSessionEval(in *domain.SessionEval) *domain.SessionEval {
	if in == nil {
		return nil
	}
	out := *in
	out.ErrorTaxonomyTags = append([]string(nil), in.ErrorTaxonomyTags...)
	out.ImprovementCategories.Harness = append([]string(nil), in.ImprovementCategories.Harness...)
	out.ImprovementCategories.Linter = append([]string(nil), in.ImprovementCategories.Linter...)
	out.ImprovementCategories.Prompt = append([]string(nil), in.ImprovementCategories.Prompt...)
	out.ImprovementCategories.Skill = append([]string(nil), in.ImprovementCategories.Skill...)
	return &out
}
