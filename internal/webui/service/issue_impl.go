package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/types"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// validIssueTypes defines the valid issue types for validation.
var validIssueTypes = map[string]bool{
	"bug":     true,
	"feature": true,
	"task":    true,
	"epic":    true,
	"chore":   true,
}

// Limits for create request validation.
const (
	maxLabels        = 50
	maxDependencies  = 100
	maxCommentLength = 64 * 1024 // 64KB
	maxListLimit     = 1000
	parentBatchSize  = 1000
)

type issueServiceImpl struct {
	pool            daemon.Pool
	multiPool       *daemon.MultiPool
	withWorkspaceFn func(ctx context.Context, wsID string) context.Context
}

// NewIssueService creates a new IssueService implementation.
// withWorkspaceFn injects the workspace ID into the context for MultiPool routing
// (avoids import cycle with the webui package where the context key is defined).
func NewIssueService(pool daemon.Pool, multiPool *daemon.MultiPool, withWorkspaceFn func(ctx context.Context, wsID string) context.Context) IssueService {
	return &issueServiceImpl{pool: pool, multiPool: multiPool, withWorkspaceFn: withWorkspaceFn}
}

func (s *issueServiceImpl) acquireClient(ctx context.Context) (*rpc.Client, error) {
	if s.pool == nil {
		return nil, ErrUnavailable("connection pool not initialized")
	}
	client, err := s.pool.Get(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to daemon")
		}
		return nil, ErrUnavailable("daemon not available")
	}
	return client, nil
}

func (s *issueServiceImpl) GetIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	resp, err := client.Show(&rpc.ShowArgs{ID: issueID})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
		}
		slog.Error("RPC error in GetIssue", "issue_id", issueID, "err", err)
		return nil, ErrInternal("internal server error", err)
	}

	return resp.Data, nil
}

func (s *issueServiceImpl) CreateIssue(ctx context.Context, params CreateIssueParams) (json.RawMessage, error) {
	if err := validateCreateParams(&params); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	createArgs := toCreateArgs(&params)
	resp, err := client.Create(createArgs)
	if err != nil {
		slog.Error("RPC error in CreateIssue", "err", err)
		return nil, ErrInternal("failed to create issue", err)
	}

	if !resp.Success {
		return nil, ErrInternal(resp.Error, nil)
	}

	return resp.Data, nil
}

func (s *issueServiceImpl) PatchIssue(ctx context.Context, params PatchIssueParams) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(client)

	updateArgs := patchParamsToUpdateArgs(&params)
	resp, err := client.Update(updateArgs)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return ErrNotFound(fmt.Sprintf("issue not found: %s", params.IssueID))
		}
		slog.Error("RPC error in PatchIssue", "issue_id", params.IssueID, "err", err)
		return ErrInternal("internal server error", err)
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "not found") {
			return ErrNotFound(resp.Error)
		}
		if strings.Contains(resp.Error, "cannot update template") {
			return ErrConflict(resp.Error)
		}
		return ErrInternal(resp.Error, nil)
	}

	return nil
}

func (s *issueServiceImpl) CloseIssue(ctx context.Context, params CloseIssueParams) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	args := &rpc.CloseArgs{
		ID:          params.IssueID,
		Reason:      params.Reason,
		Session:     params.Session,
		SuggestNext: params.SuggestNext,
		Force:       params.Force,
	}

	resp, err := client.CloseIssue(args)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", params.IssueID))
		}
		if strings.Contains(err.Error(), "blocker") {
			return nil, ErrConflict(err.Error())
		}
		slog.Error("RPC error in CloseIssue", "issue_id", params.IssueID, "err", err)
		return nil, ErrInternal("internal server error", err)
	}

	if !resp.Success {
		return nil, ErrInternal(resp.Error, nil)
	}

	return resp.Data, nil
}

func (s *issueServiceImpl) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	resp, err := client.Delete(&rpc.DeleteArgs{
		IDs:   []string{issueID},
		Force: true,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
		}
		slog.Error("RPC error in DeleteIssue", "issue_id", issueID, "err", err)
		return nil, ErrInternal("internal server error", err)
	}

	if !resp.Success {
		return nil, ErrInternal(resp.Error, nil)
	}

	return resp.Data, nil
}

func (s *issueServiceImpl) AddComment(ctx context.Context, params AddCommentParams) (*types.Comment, error) {
	text := strings.TrimSpace(params.Text)
	if text == "" {
		return nil, ErrValidation("comment text is required")
	}
	if len(text) > maxCommentLength {
		return nil, ErrValidation(fmt.Sprintf("comment text too long (%d bytes, max %d)", len(text), maxCommentLength))
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	author := params.Author
	if author == "" {
		author = "web-ui"
	}

	resp, err := client.AddComment(&rpc.CommentAddArgs{
		ID:     params.IssueID,
		Author: author,
		Text:   text,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound("issue not found")
		}
		slog.Error("RPC error in AddComment", "err", err)
		return nil, ErrInternal("internal server error", err)
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "not found") {
			return nil, ErrNotFound(resp.Error)
		}
		return nil, ErrInternal(resp.Error, nil)
	}

	var comment types.Comment
	if err := json.Unmarshal(resp.Data, &comment); err != nil {
		return nil, ErrInternal(fmt.Sprintf("failed to parse comment: %v", err), err)
	}

	return &comment, nil
}

func (s *issueServiceImpl) AddDependency(ctx context.Context, params AddDependencyParams) error {
	if params.DependsOnID == "" {
		return ErrValidation("depends_on_id is required")
	}
	if params.IssueID == params.DependsOnID {
		return ErrValidation("cannot add self-dependency")
	}

	depType := params.DepType
	if depType == "" {
		depType = "blocks"
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(client)

	resp, err := client.AddDependency(&rpc.DepAddArgs{
		FromID:  params.IssueID,
		ToID:    params.DependsOnID,
		DepType: depType,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return ErrNotFound("dependency target not found")
		}
		if strings.Contains(err.Error(), "cycle") || strings.Contains(err.Error(), "already exists") {
			return ErrConflict(err.Error())
		}
		slog.Error("RPC error in AddDependency", "err", err)
		return ErrInternal("internal server error", err)
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "not found") {
			return ErrNotFound(resp.Error)
		}
		if strings.Contains(resp.Error, "cycle") || strings.Contains(resp.Error, "already exists") {
			return ErrConflict(resp.Error)
		}
		return ErrInternal(resp.Error, nil)
	}

	return nil
}

func (s *issueServiceImpl) RemoveDependency(ctx context.Context, params RemoveDependencyParams) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return err
	}
	defer s.pool.Put(client)

	resp, err := client.RemoveDependency(&rpc.DepRemoveArgs{
		FromID: params.IssueID,
		ToID:   params.DepID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return ErrNotFound("dependency not found")
		}
		slog.Error("RPC error in RemoveDependency", "err", err)
		return ErrInternal("internal server error", err)
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "not found") {
			return ErrNotFound(resp.Error)
		}
		return ErrInternal(resp.Error, nil)
	}

	return nil
}

func (s *issueServiceImpl) ListEvents(ctx context.Context, params EventListParams) ([]*types.Event, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := s.acquireClient(ctx)
	if err != nil {
		return nil, err
	}
	defer s.pool.Put(client)

	resp, err := client.ListEvents(&rpc.EventListArgs{
		ID:    params.IssueID,
		Limit: params.Limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNotFound("issue not found")
		}
		slog.Error("RPC error in ListEvents", "err", err)
		return nil, ErrInternal("internal server error", err)
	}

	if !resp.Success {
		if strings.Contains(resp.Error, "not found") {
			return nil, ErrNotFound(resp.Error)
		}
		return nil, ErrInternal(resp.Error, nil)
	}

	var events []*types.Event
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, ErrInternal("failed to parse events", err)
	}
	if events == nil {
		events = []*types.Event{}
	}
	return events, nil
}
