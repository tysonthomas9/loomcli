package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
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

// IssueBackendProvider returns the active backend.IssueBackend.
//
// It is injected by the cli/main wiring as a closure rather than a direct
// reference to avoid an import cycle (webui must not import internal/cli).
// Implementations may resolve the backend lazily; callers must handle a nil
// return by treating the backend as unavailable. The returned backend is
// expected to be safe for concurrent use across goroutines.
type IssueBackendProvider func() backend.IssueBackend

type issueServiceImpl struct {
	pool            daemon.Pool
	multiPool       *daemon.MultiPool
	withWorkspaceFn func(ctx context.Context, wsID string) context.Context
	// backendFn returns the active IssueBackend used by methods that have
	// migrated off the direct rpc.Client path. The pool/multiPool fields
	// remain to back ListIssues/ListKanban and the cross-workspace MoveIssue
	// path which have not yet been migrated.
	backendFn IssueBackendProvider
}

// NewIssueService creates a new IssueService implementation backed by the
// daemon connection pool only. Methods that have been migrated to use
// backend.IssueBackend will fall back to returning ErrUnavailable when
// invoked through this constructor.
//
// withWorkspaceFn injects the workspace ID into the context for MultiPool
// routing (avoids import cycle with the webui package where the context key
// is defined).
//
// Prefer NewIssueServiceWithBackend when an IssueBackend is available; this
// constructor is retained for tests and call sites that have not yet been
// updated to thread the backend through.
func NewIssueService(pool daemon.Pool, multiPool *daemon.MultiPool, withWorkspaceFn func(ctx context.Context, wsID string) context.Context) IssueService {
	return &issueServiceImpl{pool: pool, multiPool: multiPool, withWorkspaceFn: withWorkspaceFn}
}

// NewIssueServiceWithBackend creates a new IssueService implementation that
// dispatches CRUD operations through the supplied IssueBackend provider while
// retaining the daemon connection pools for the not-yet-migrated paths
// (ListIssues / ListKanban / MoveIssue).
//
// backendFn is a closure rather than a direct backend instance so the cli
// package can resolve the backend lazily without webui taking an import on
// internal/cli. backendFn may be nil; methods that need the backend then
// behave as if the backend were unavailable.
func NewIssueServiceWithBackend(pool daemon.Pool, multiPool *daemon.MultiPool, withWorkspaceFn func(ctx context.Context, wsID string) context.Context, backendFn IssueBackendProvider) IssueService {
	return &issueServiceImpl{
		pool:            pool,
		multiPool:       multiPool,
		withWorkspaceFn: withWorkspaceFn,
		backendFn:       backendFn,
	}
}

// resolveBackend returns the current IssueBackend or a service error if no
// backend has been wired into the service.
func (s *issueServiceImpl) resolveBackend() (backend.IssueBackend, *ServiceError) {
	if s.backendFn == nil {
		return nil, ErrUnavailable("issue backend not configured")
	}
	be := s.backendFn()
	if be == nil {
		return nil, ErrUnavailable("issue backend not available")
	}
	return be, nil
}

// translateBackendError converts a *backend.BackendError into a *ServiceError
// so handler error mapping continues to work uniformly across migrated and
// non-migrated paths.
func translateBackendError(err error) *ServiceError {
	if err == nil {
		return nil
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		// Non-backend error (e.g., context.Canceled returned directly).
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrTimeout("operation timed out")
		}
		return ErrInternal(err.Error(), err)
	}
	switch be.Kind {
	case backend.KindNotFound:
		return ErrNotFound(be.Message)
	case backend.KindValidation:
		return ErrValidation(be.Message)
	case backend.KindConflict:
		return ErrConflict(be.Message)
	case backend.KindUnavailable:
		return ErrUnavailable(be.Message)
	case backend.KindTimeout:
		return ErrTimeout(be.Message)
	case backend.KindCanceled:
		return ErrTimeout(be.Message)
	case backend.KindNotImplemented:
		return ErrNotImplemented(be.Message)
	default:
		return ErrInternal(be.Message, be.Cause)
	}
}

func (s *issueServiceImpl) acquireClient(ctx context.Context) (*rpc.Client, error) {
	if s.pool == nil {
		return nil, ErrUnavailable("connection pool not initialized")
	}
	client, err := s.pool.Get(ctx)
	if err != nil {
		if errors.Is(err, daemon.ErrDaemonStarting) {
			return nil, ErrStarting("workspace is loading")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("timeout connecting to daemon")
		}
		return nil, ErrUnavailable("daemon not available")
	}
	return client, nil
}

// releaseClient returns the connection to the pool when *ok is true, or
// closes (Discards) it when *ok is false. Use the conditional defer pattern:
//
//	rpcOK := false
//	defer s.releaseClient(client, &rpcOK)
//	... resp, err := client.Foo(...)
//	if err != nil { return err }
//	rpcOK = true
//
// On RPC error, the connection's read buffer may retain stale bytes that
// would corrupt the next borrower. Discarding closes the connection so a
// fresh one is opened next time.
func (s *issueServiceImpl) releaseClient(client *rpc.Client, ok *bool) {
	if *ok {
		s.pool.Put(client)
	} else {
		s.pool.Discard(client)
	}
}

func (s *issueServiceImpl) GetIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	detail, err := be.Get(ctx, issueID)
	if err != nil {
		slog.Error("backend error in GetIssue", "issue_id", issueID, "err", err)
		return nil, translateBackendError(err)
	}
	if detail == nil {
		return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
	}

	out, err := json.Marshal(issueDetailDataToWire(detail))
	if err != nil {
		return nil, ErrInternal("failed to marshal issue", err)
	}
	return out, nil
}

func (s *issueServiceImpl) CreateIssue(ctx context.Context, params CreateIssueParams) (json.RawMessage, error) {
	if err := validateCreateParams(&params); err != nil {
		return nil, err
	}

	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	created, err := be.Create(ctx, createParamsToBackend(&params))
	if err != nil {
		slog.Error("backend error in CreateIssue", "err", err)
		return nil, translateBackendError(err)
	}
	if created == nil {
		return nil, ErrInternal("backend returned nil issue after create", nil)
	}

	// backend.IssueData is the slim projection; the previous direct-RPC
	// path returned the full types.Issue (with description / design /
	// acceptance_criteria / notes / external_ref / created_by / etc.) and
	// the FE relies on those fields for the create-then-read-back roundtrip.
	// Fetch the full detail post-create to preserve that contract.
	detail, getErr := be.Get(ctx, created.ID)
	if getErr == nil && detail != nil {
		out, err := json.Marshal(issueDetailDataToWire(detail))
		if err != nil {
			return nil, ErrInternal("failed to marshal created issue", err)
		}
		return out, nil
	}
	if getErr != nil {
		slog.Warn("CreateIssue: post-create Get failed; returning slim projection",
			"issue_id", created.ID, "err", getErr)
	}

	// Fall back to the slim projection if the follow-up Get fails — the
	// create itself succeeded so we still return success rather than
	// surface the read failure as a create failure.
	out, err := json.Marshal(issueDataToWire(created))
	if err != nil {
		return nil, ErrInternal("failed to marshal created issue", err)
	}
	return out, nil
}

func (s *issueServiceImpl) PatchIssue(ctx context.Context, params PatchIssueParams) error {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if err := be.Update(ctx, params.IssueID, patchParamsToBackendUpdate(&params)); err != nil {
		// Special-case "cannot update template" — backend classifies as
		// internal but the prior service contract maps to ErrConflict.
		if isTemplateUpdateError(err) {
			return ErrConflict(err.Error())
		}
		slog.Error("backend error in PatchIssue", "issue_id", params.IssueID, "err", err)
		return translateBackendError(err)
	}
	return nil
}

// isTemplateUpdateError preserves the existing handler contract that surfaces
// "cannot update template" daemon errors as 409 Conflict rather than 500.
func isTemplateUpdateError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "cannot update template")
}

func (s *issueServiceImpl) CloseIssue(ctx context.Context, params CloseIssueParams) (json.RawMessage, error) {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := be.Close(ctx, params.IssueID, backend.CloseParams{
		Reason:      params.Reason,
		Session:     params.Session,
		SuggestNext: params.SuggestNext,
		Force:       params.Force,
	})
	if err != nil {
		// "blocker" / cycle conflicts surface as KindConflict via the backend's
		// classifier; KindNotFound is mapped already. Fall through to translate.
		slog.Error("backend error in CloseIssue", "issue_id", params.IssueID, "err", err)
		return nil, translateBackendError(err)
	}
	if result == nil {
		return nil, ErrInternal("backend returned nil close result", nil)
	}

	// Re-marshal the typed CloseResult back to a json.RawMessage so the
	// existing handler envelope (which forwards opaque JSON) keeps working.
	out, err := json.Marshal(closeResultToWire(result))
	if err != nil {
		return nil, ErrInternal("failed to marshal close result", err)
	}
	return out, nil
}

// ClaimIssue atomically claims an issue by ID for the server-side actor.
// Returns 409-equivalent ErrConflict if the issue is already claimed by a
// different agent. Re-claim by the same actor is idempotent and returns the
// issue without error (beads ClaimIssue SQL treats self-reclaim as success).
//
// Implementation note: backend.IssueBackend.ClaimIssue performs only the
// atomic claim. The previous direct-RPC path also forced status to
// "in_progress" in the same Update call. To preserve that observable
// behavior (and the response payload the FE expects), we follow ClaimIssue
// with an Update(status=in_progress) and then a Get to return the canonical
// post-claim issue body.
func (s *issueServiceImpl) ClaimIssue(ctx context.Context, params ClaimIssueParams) (json.RawMessage, error) {
	if strings.TrimSpace(params.IssueID) == "" {
		return nil, ErrValidation("issue ID is required")
	}

	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := be.ClaimIssue(ctx, params.IssueID, 0); err != nil {
		slog.Error("backend error in ClaimIssue", "issue_id", params.IssueID, "err", err)
		return nil, translateBackendError(err)
	}

	// Mirror the prior behavior of forcing status=in_progress on claim.
	// Errors here are non-fatal for the claim itself (the claim already
	// succeeded), but we surface them so the caller can see the inconsistent
	// state rather than silently swallowing.
	inProgress := "in_progress"
	if err := be.Update(ctx, params.IssueID, backend.UpdateParams{Status: &inProgress}); err != nil {
		slog.Warn("ClaimIssue: post-claim status update failed", "issue_id", params.IssueID, "err", err)
		// Fall through and still return the (now-claimed) issue.
	}

	detail, err := be.Get(ctx, params.IssueID)
	if err != nil {
		slog.Error("backend error in ClaimIssue.Get", "issue_id", params.IssueID, "err", err)
		return nil, translateBackendError(err)
	}
	if detail == nil {
		return nil, ErrInternal("backend returned nil issue after claim", nil)
	}

	out, err := json.Marshal(issueDetailDataToWire(detail))
	if err != nil {
		return nil, ErrInternal("failed to marshal issue", err)
	}
	return out, nil
}

func (s *issueServiceImpl) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := be.Delete(ctx, backend.DeleteParams{
		IDs:   []string{issueID},
		Force: true,
	}); err != nil {
		slog.Error("backend error in DeleteIssue", "issue_id", issueID, "err", err)
		return nil, translateBackendError(err)
	}

	// The previous RPC returned an opaque payload that callers didn't
	// inspect; emit a minimal envelope to preserve the success/data shape
	// the handler forwards as `data`.
	out, err := json.Marshal(map[string]any{
		"deleted_count": 1,
		"deleted_ids":   []string{issueID},
	})
	if err != nil {
		return nil, ErrInternal("failed to marshal delete result", err)
	}
	return out, nil
}

func (s *issueServiceImpl) AddComment(ctx context.Context, params AddCommentParams) (*types.Comment, error) {
	text := strings.TrimSpace(params.Text)
	if text == "" {
		return nil, ErrValidation("comment text is required")
	}
	if len(text) > maxCommentLength {
		return nil, ErrValidation(fmt.Sprintf("comment text too long (%d bytes, max %d)", len(text), maxCommentLength))
	}

	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	author := params.Author
	if author == "" {
		author = "web-ui"
	}

	data, err := be.AddComment(ctx, backend.CommentAddParams{
		IssueID: params.IssueID,
		Author:  author,
		Text:    text,
	})
	if err != nil {
		slog.Error("backend error in AddComment", "err", err)
		return nil, translateBackendError(err)
	}
	if data == nil {
		return nil, ErrInternal("backend returned nil comment", nil)
	}
	return commentDataToTypesComment(data), nil
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

	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := be.AddDependency(ctx, backend.DepAddParams{
		FromID:  params.IssueID,
		ToID:    params.DependsOnID,
		DepType: depType,
	}); err != nil {
		slog.Error("backend error in AddDependency", "err", err)
		return translateBackendError(err)
	}
	return nil
}

func (s *issueServiceImpl) RemoveDependency(ctx context.Context, params RemoveDependencyParams) error {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := be.RemoveDependency(ctx, backend.DepRemoveParams{
		FromID: params.IssueID,
		ToID:   params.DepID,
	}); err != nil {
		slog.Error("backend error in RemoveDependency", "err", err)
		return translateBackendError(err)
	}
	return nil
}

func (s *issueServiceImpl) ListEvents(ctx context.Context, params EventListParams) ([]*types.Event, error) {
	be, svcErr := s.resolveBackend()
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	data, err := be.ListEvents(ctx, params.IssueID, params.Limit)
	if err != nil {
		slog.Error("backend error in ListEvents", "err", err)
		return nil, translateBackendError(err)
	}

	events := make([]*types.Event, 0, len(data))
	for _, e := range data {
		events = append(events, eventDataToTypesEvent(e))
	}
	return events, nil
}
