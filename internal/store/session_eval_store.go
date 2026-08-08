package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type SessionEvalFilter struct {
	SessionID          string
	TaskID             string
	AgentID            string
	JudgePromptVersion string
	Since              *time.Time
	Until              *time.Time
	Limit              int
}

type SessionEvalStore interface {
	Create(ctx context.Context, in *domain.SessionEval) (*domain.SessionEval, error)
	Get(ctx context.Context, workspaceKey, evalID string) (*domain.SessionEval, error)
	Delete(ctx context.Context, workspaceKey, evalID string) error
	List(ctx context.Context, workspaceKey string, filter SessionEvalFilter) ([]*domain.SessionEval, error)
}
