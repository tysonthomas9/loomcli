package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/entity"
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
)

// IssueBackendProvider returns the active backend.IssueBackend.
//
// It is injected by the cli/main wiring as a closure rather than a direct
// reference to avoid an import cycle (webui must not import internal/cli).
// Implementations may resolve the backend lazily; callers must handle a nil
// return by treating the backend as unavailable. The returned backend is
// expected to be safe for concurrent use across goroutines.
//
// The ctx carries the per-request workspace ID; cloud-mode providers use it
// to build a fleet-db backend scoped to the request's workspace. Local
// providers may ignore ctx.
type IssueBackendProvider func(ctx context.Context) backend.IssueBackend

type issueServiceImpl struct {
	pool            daemon.Pool
	multiPool       *daemon.MultiPool
	withWorkspaceFn func(ctx context.Context, wsID string) context.Context
	// backendFn returns the active IssueBackend used by methods that have
	// migrated off the direct rpc.Client path. The pool/multiPool fields
	// remain to back ListIssues/ListKanban and the cross-workspace MoveIssue
	// path which have not yet been migrated.
	backendFn IssueBackendProvider

	labelMutationMu sync.Mutex
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
// backend has been wired into the service. The ctx is forwarded to the
// provider so cloud-mode wirings can pick a per-workspace backend.
func (s *issueServiceImpl) resolveBackend(ctx context.Context) (backend.IssueBackend, *ServiceError) {
	if s.backendFn == nil {
		return nil, ErrUnavailable("issue backend not configured")
	}
	be := s.backendFn(ctx)
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
			return nil, ErrTimeout("timeout connecting to issue backend")
		}
		return nil, ErrUnavailable("issue backend unavailable")
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
	be, svcErr := s.resolveBackend(ctx)
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

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lineage := backend.TaskLineageSpec{InheritsFrom: params.InheritsFrom, IntegrationInputs: params.IntegrationInputs}
	if err := backend.ValidateTaskLineageInputs(ctx, lineage, params.SourceRepo, be.Get); err != nil {
		return nil, ErrValidation("invalid task code lineage: " + err.Error())
	}

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
	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	if patchHasLabelMutation(params) {
		s.labelMutationMu.Lock()
		defer s.labelMutationMu.Unlock()
	}

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

func patchHasLabelMutation(params PatchIssueParams) bool {
	return len(params.AddLabels) > 0 || len(params.RemoveLabels) > 0 || len(params.SetLabels) > 0
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
	be, svcErr := s.resolveBackend(ctx)
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
		// Idempotent close: "already closed" means the desired state is
		// true. Old fleet-db versions surface it as a conflict — treat it
		// as a quiet no-op success instead of an ERROR-level failure (new
		// fleet-db returns 200 and never reaches this branch).
		if backend.IsAlreadyClosedConflict(err) {
			slog.Info("close was a no-op: issue already closed", "issue_id", params.IssueID)
			result = &backend.CloseResult{Closed: &backend.IssueData{ID: params.IssueID}}
		} else {
			// "blocker" / cycle conflicts surface as KindConflict via the backend's
			// classifier; KindNotFound is mapped already. Fall through to translate.
			slog.Error("backend error in CloseIssue", "issue_id", params.IssueID, "err", err)
			return nil, translateBackendError(err)
		}
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
// issue without error.
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

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := ensureClaimable(ctx, be, params.IssueID); err != nil {
		return nil, err
	}

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

func ensureClaimable(ctx context.Context, be backend.IssueBackend, issueID string) *ServiceError {
	detail, err := be.Get(ctx, issueID)
	if err != nil {
		return translateBackendError(err)
	}
	if detail == nil {
		return ErrInternal("backend returned nil issue before claim", nil)
	}
	if blockerID, ok := firstOpenClaimBlocker(detail.Dependencies); ok {
		return ErrConflict(fmt.Sprintf("issue is blocked by open dependency %s", blockerID))
	}
	return nil
}

func firstOpenClaimBlocker(deps []backend.DependencyData) (string, bool) {
	for _, dep := range deps {
		if !entity.DependencyType(dep.Type).AffectsReadyWork() {
			continue
		}
		if strings.EqualFold(dep.Status, "closed") {
			continue
		}
		if dep.DependsOnID != "" {
			return dep.DependsOnID, true
		}
		return "unknown", true
	}
	return "", false
}

func (s *issueServiceImpl) DeleteIssue(ctx context.Context, issueID string) (json.RawMessage, error) {
	be, svcErr := s.resolveBackend(ctx)
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

	be, svcErr := s.resolveBackend(ctx)
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

	be, svcErr := s.resolveBackend(ctx)
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
	be, svcErr := s.resolveBackend(ctx)
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

// SearchIssues performs a full-text relevance-ranked search via the
// backend.IssueBackend.SearchIssues operation and returns the slim issue list
// marshaled into the same wire shape as the list endpoint (IssueData).
// Returns ErrValidation if the query is empty, ErrUnavailable if no backend is
// wired, and translates backend errors via translateBackendError.
func (s *issueServiceImpl) SearchIssues(ctx context.Context, params SearchIssuesParams) (json.RawMessage, error) {
	if strings.TrimSpace(params.Query) == "" {
		return nil, ErrValidation("search query is required")
	}
	if params.Limit < 0 {
		return nil, ErrValidation("limit must be non-negative")
	}

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	issues, err := be.SearchIssues(ctx, params.Query, params.Limit)
	if err != nil {
		slog.Error("backend error in SearchIssues", "query", params.Query, "err", err)
		return nil, translateBackendError(err)
	}

	wire := make([]map[string]any, 0, len(issues))
	for i := range issues {
		wire = append(wire, issueDataToWire(&issues[i]))
	}

	out, err := json.Marshal(wire)
	if err != nil {
		return nil, ErrInternal("failed to marshal search results", err)
	}
	return out, nil
}

// ReopenIssue transitions a closed issue back to open status. If
// ReopenIssueParams.Reason is non-empty, the backend records it as a comment on
// the issue (best-effort; status transition is the primary result).
// Returns ErrValidation for empty IssueID.
func (s *issueServiceImpl) ReopenIssue(ctx context.Context, params ReopenIssueParams) error {
	if strings.TrimSpace(params.IssueID) == "" {
		return ErrValidation("issue ID is required")
	}

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := be.Reopen(ctx, params.IssueID, backend.ReopenParams{Reason: params.Reason}); err != nil {
		slog.Error("backend error in ReopenIssue", "issue_id", params.IssueID, "err", err)
		return translateBackendError(err)
	}
	return nil
}

// ListComments returns all comments for an issue, ordered by creation time.
// Returns ErrValidation for empty IssueID.
func (s *issueServiceImpl) ListComments(ctx context.Context, issueID string) ([]*types.Comment, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, ErrValidation("issue ID is required")
	}

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	data, err := be.ListComments(ctx, issueID)
	if err != nil {
		slog.Error("backend error in ListComments", "issue_id", issueID, "err", err)
		return nil, translateBackendError(err)
	}

	comments := make([]*types.Comment, 0, len(data))
	for i := range data {
		comments = append(comments, commentDataToTypesComment(&data[i]))
	}
	return comments, nil
}

// ListDependencies returns the dependency list for an issue in the same wire
// shape used by IssueDetailData.Dependencies (depsToWire). Marshaled to
// json.RawMessage so handlers can forward it through the usual {success,data}
// envelope without re-marshaling.
func (s *issueServiceImpl) ListDependencies(ctx context.Context, issueID string) (json.RawMessage, error) {
	if strings.TrimSpace(issueID) == "" {
		return nil, ErrValidation("issue ID is required")
	}

	be, svcErr := s.resolveBackend(ctx)
	if svcErr != nil {
		return nil, svcErr
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// The IssueBackend interface does not expose a standalone ListDependencies;
	// it returns them embedded in IssueDetailData.Get. That's the canonical
	// source so every backend path reuses the same machinery.
	detail, err := be.Get(ctx, issueID)
	if err != nil {
		slog.Error("backend error in ListDependencies", "issue_id", issueID, "err", err)
		return nil, translateBackendError(err)
	}
	if detail == nil {
		return nil, ErrNotFound(fmt.Sprintf("issue not found: %s", issueID))
	}

	out, err := json.Marshal(depsToWire(detail.Dependencies, issueID))
	if err != nil {
		return nil, ErrInternal("failed to marshal dependencies", err)
	}
	return out, nil
}

func (s *issueServiceImpl) ListEvents(ctx context.Context, params EventListParams) ([]*types.Event, error) {
	be, svcErr := s.resolveBackend(ctx)
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
