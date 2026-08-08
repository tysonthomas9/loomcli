package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type sessionEvalStore struct{ client *Client }

var _ store.SessionEvalStore = (*sessionEvalStore)(nil)

func (s *sessionEvalStore) Create(ctx context.Context, in *domain.SessionEval) (*domain.SessionEval, error) {
	if in == nil {
		return nil, fmt.Errorf("session eval is required: %w", domain.ErrInvalid)
	}
	var out domain.SessionEval
	err := s.client.do(ctx, "POST", "/api/v1/"+pathEscape(in.WorkspaceKey)+"/session-evals", in, &out)
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return nil, fmt.Errorf("session eval %q in workspace %q: %w", in.EvalID, in.WorkspaceKey, domain.ErrConflict)
		}
		return nil, err
	}
	return &out, nil
}

func (s *sessionEvalStore) Get(ctx context.Context, ws, evalID string) (*domain.SessionEval, error) {
	var out domain.SessionEval
	if err := s.client.do(ctx, "GET", "/api/v1/"+pathEscape(ws)+"/session-evals/"+pathEscape(evalID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *sessionEvalStore) Delete(ctx context.Context, ws, evalID string) error {
	return s.client.do(ctx, "DELETE", "/api/v1/"+pathEscape(ws)+"/session-evals/"+pathEscape(evalID), nil, nil)
}

func (s *sessionEvalStore) List(ctx context.Context, ws string, filter store.SessionEvalFilter) ([]*domain.SessionEval, error) {
	q := url.Values{}
	if filter.SessionID != "" {
		q.Set("session_id", filter.SessionID)
	}
	if filter.TaskID != "" {
		q.Set("task_id", filter.TaskID)
	}
	if filter.AgentID != "" {
		q.Set("agent_id", filter.AgentID)
	}
	if filter.JudgePromptVersion != "" {
		q.Set("judge_prompt_version", filter.JudgePromptVersion)
	}
	if filter.Since != nil {
		q.Set("since", filter.Since.UTC().Format(time.RFC3339))
	}
	if filter.Until != nil {
		q.Set("until", filter.Until.UTC().Format(time.RFC3339))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	var resp struct {
		SessionEvals []*domain.SessionEval `json:"session_evals"`
	}
	if err := s.client.do(ctx, "GET", withQuery("/api/v1/"+pathEscape(ws)+"/session-evals", q), nil, &resp); err != nil {
		return nil, err
	}
	if resp.SessionEvals == nil {
		resp.SessionEvals = []*domain.SessionEval{}
	}
	return resp.SessionEvals, nil
}
