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
	"github.com/tysonthomas9/loomcli/internal/types"
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
	withWorkspaceFn func(ctx context.Context, wsID string) context.Context
	backendFn       IssueBackendProvider

	labelMutationMu sync.Mutex
}

// NewIssueServiceWithBackend creates a service that dispatches every issue
// operation through the supplied IssueBackend provider.
//
// backendFn is a closure rather than a direct backend instance so the cli
// package can resolve the backend lazily without webui taking an import on
// internal/cli. backendFn may be nil; methods that need the backend then
// behave as if the backend were unavailable.
func NewIssueServiceWithBackend(withWorkspaceFn func(ctx context.Context, wsID string) context.Context, backendFn IssueBackendProvider) IssueService {
	return &issueServiceImpl{
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

	repositoryAdmission, err := resolveCreateRepositoryAdmission(be, params)
	if err != nil {
		return nil, err
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

	canonical, admitted, err := applyCreatedIssueRepositoryAdmission(ctx, repositoryAdmission, created)
	if err != nil {
		return nil, err
	}
	return marshalCreatedIssueResponse(ctx, be, created, canonical, admitted)
}

func resolveCreateRepositoryAdmission(
	be backend.IssueBackend,
	params CreateIssueParams,
) (backend.RepositoryRequirementBackend, error) {
	// An otherwise-runnable non-epic card with no repository must pass the Work
	// Items owner's atomic admission command before this request reports success.
	if !createNeedsRepositoryAdmission(params) {
		return nil, nil
	}
	repositoryAdmission, ok := be.(backend.RepositoryRequirementBackend)
	if !ok {
		// Fail before minting the issue; an error after Create would leave the
		// caller uncertain whether an unkeyed retry could create a duplicate.
		return nil, ErrNotImplemented("issue backend does not support atomic repository-required admission")
	}
	return repositoryAdmission, nil
}

func applyCreatedIssueRepositoryAdmission(
	ctx context.Context,
	repositoryAdmission backend.RepositoryRequirementBackend,
	created *backend.IssueData,
) (*backend.IssueData, bool, error) {
	if repositoryAdmission == nil {
		return created, false, nil
	}
	result, err := admitCreatedIssueRepository(ctx, repositoryAdmission, created.ID)
	if err != nil {
		slog.Error("backend error in CreateIssue repository admission", "issue_id", created.ID, "err", err)
		return nil, false, translateBackendError(err)
	}
	if result == nil || result.Issue == nil {
		return nil, false, ErrInternal("repository admission returned no canonical issue", nil)
	}
	if result.Issue.ID != created.ID {
		return nil, false, ErrInternal("repository admission returned a different issue", nil)
	}
	return result.Issue, true, nil
}

func marshalCreatedIssueResponse(
	ctx context.Context,
	be backend.IssueBackend,
	created, canonical *backend.IssueData,
	admitted bool,
) (json.RawMessage, error) {

	// backend.IssueData is the slim projection; the previous direct-RPC
	// path returned the full types.Issue (with description / design /
	// acceptance_criteria / notes / external_ref / created_by / etc.) and
	// the FE relies on those fields for the create-then-read-back roundtrip.
	// Fetch the full detail post-create to preserve that contract.
	detail, getErr := be.Get(ctx, created.ID)
	if getErr == nil && detail != nil {
		if admitted {
			mergeCanonicalCreatedIssue(detail, canonical)
		}
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
	out, err := json.Marshal(issueDataToWire(canonical))
	if err != nil {
		return nil, ErrInternal("failed to marshal created issue", err)
	}
	return out, nil
}

func mergeCanonicalCreatedIssue(detail *backend.IssueDetailData, canonical *backend.IssueData) {
	// The atomic command result owns the admission generation. Preserve the
	// relationship projections from Get while replacing every canonical field.
	dependencyCount, dependentCount := detail.DependencyCount, detail.DependentCount
	blockedByCount, blockedBy := detail.BlockedByCount, detail.BlockedBy
	detail.IssueData = *canonical
	detail.DependencyCount, detail.DependentCount = dependencyCount, dependentCount
	detail.BlockedByCount, detail.BlockedBy = blockedByCount, blockedBy
}

// admitCreatedIssueRepository makes one bounded replay of the Fleet-owned
// command when its result is ambiguous because of a transport failure or
// timeout. The command is explicitly replay-safe: if the first call committed
// and only its response was lost, the replay returns the same canonical
// blocked Issue; if it did not commit, the replay may apply it. A persistent
// failure remains fail-closed and the durable issue-journal lane will retry
// admission for the already-created card.
func admitCreatedIssueRepository(
	ctx context.Context,
	repositoryAdmission backend.RepositoryRequirementBackend,
	issueID string,
) (*backend.RepositoryRequirementResult, error) {
	result, err := repositoryAdmission.BlockRepositoryRequired(ctx, issueID)
	if err == nil || ctx.Err() != nil ||
		(!backend.IsKind(err, backend.KindUnavailable) && !backend.IsKind(err, backend.KindTimeout)) {
		return result, err
	}
	slog.Warn("CreateIssue: repository admission result ambiguous; replaying atomic command",
		"issue_id", issueID, "err", err)
	return repositoryAdmission.BlockRepositoryRequired(ctx, issueID)
}

// createNeedsRepositoryAdmission identifies create requests that can enter
// the ready queue without a deterministic checkout. The atomic command still
// rechecks all of these facts, including repository cardinality and ownership,
// at commit time; this predicate only avoids a needless command for epics,
// deferred cards, and cards with an explicit repository.
func createNeedsRepositoryAdmission(params CreateIssueParams) bool {
	status := strings.ToLower(strings.TrimSpace(params.Status))
	return (status == "" || status == string(types.StatusOpen)) &&
		!strings.EqualFold(strings.TrimSpace(params.IssueType), "epic") &&
		strings.TrimSpace(params.SourceRepo) == ""
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
