package prreview

import (
	"fmt"
	"net/http"
	"strings"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type pullRequestDetail = loomapi.PullRequestDetail
type pullRequestDiff = loomapi.PullRequestDiff
type pullRequestDiffFile = loomapi.PullRequestDiffFile
type pullRequestReviewRequest = loomapi.PullRequestReviewRequest
type pullRequestReviewRequestEvent = loomapi.PullRequestReviewRequestEvent

const (
	pullRequestReviewApprove        pullRequestReviewRequestEvent = "approve"
	pullRequestReviewComment        pullRequestReviewRequestEvent = "comment"
	pullRequestReviewRequestChanges pullRequestReviewRequestEvent = "request_changes"
)

type pullRequestReviewResult = loomapi.PullRequestReviewResult
type reviewerEnsureResult = loomapi.ReviewerEnsureResult
type reviewerMessageRequest = loomapi.ReviewerMessageRequest
type reviewerMessageResult = loomapi.ReviewerMessageResult

// resolveAuthorizedPR parses the {owner}/{repo}/{number} path and
// canonicalizes owner/repo through the workspace membership check, writing
// the HTTP error itself on failure. Every PR-scoped handler starts here.
func (m *Module) resolveAuthorizedPR(w http.ResponseWriter, r *http.Request) (string, pullRequestPath, bool) {
	ws := canonicalWorkspaceFromRequest(r)
	if ws == "" {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "canonical workspace is required", false)
		return "", pullRequestPath{}, false
	}
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return ws, params, false
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return ws, params, false
	}
	params.owner, params.repo = canonOwner, canonRepo
	return ws, params, true
}

func canonicalWorkspaceFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(middleware.WorkspaceFromContext(r.Context()))
}

func (m *Module) getPullRequest(w http.ResponseWriter, r *http.Request) {
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}
	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReadActions); err != nil {
		writePRReviewError(w, err)
		return
	}
	res, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey: ws,
		RunID:        syntheticRunID(r, params, connectorsmodule.ActionGitHubPullRequestRead),
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       connectorsmodule.ActionGitHubPullRequestRead,
		Resource:     prResource(params.owner, params.repo),
		Args:         pullRequestArgs(params),
		CallSeq:      0,
	})
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	writeJSON(w, pullRequestDetailFromBody(res.Body))
}

func (m *Module) getPullRequestDiff(w http.ResponseWriter, r *http.Request) {
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}
	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReadActions); err != nil {
		writePRReviewError(w, err)
		return
	}
	runID := syntheticRunID(r, params, connectorsmodule.ActionGitHubCompareRead)
	detail, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey: ws,
		RunID:        runID,
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       connectorsmodule.ActionGitHubPullRequestRead,
		Resource:     prResource(params.owner, params.repo),
		Args:         pullRequestArgs(params),
		CallSeq:      0,
	})
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	baseRef := stringValue(detail.Body["baseRef"])
	headSha := stringValue(detail.Body["headSha"])
	res, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey: ws,
		RunID:        runID,
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       connectorsmodule.ActionGitHubCompareRead,
		Resource:     prResource(params.owner, params.repo),
		Args: map[string]any{
			"owner": params.owner,
			"repo":  params.repo,
			"base":  baseRef,
			"head":  headSha,
		},
		Preconditions: connectorsmodule.DispatchPreconditions{ExpectedHeadSha: headSha},
		CallSeq:       1,
	})
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	writeJSON(w, pullRequestDiffFromBody(res.Body))
}

func (m *Module) postReview(w http.ResponseWriter, r *http.Request) {
	ws, params, ok := m.resolveAuthorizedPR(w, r)
	if !ok {
		return
	}

	req, event, expectedHeadSha, ok := decodeReviewRequest(w, r)
	if !ok {
		return
	}

	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReviewSubmissionActions); err != nil {
		writePRReviewError(w, err)
		return
	}
	args := pullRequestArgs(params)
	args["event"] = event
	if req.Body != nil {
		args["body"] = *req.Body
	}
	runID, err := reviewSubmissionRunID(r, params)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusInternalServerError, "internal", "failed to prepare the review request", false)
		return
	}
	res, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey:  ws,
		RunID:         runID,
		BindingID:     bindingID,
		ConnectorID:   connectorID,
		Action:        connectorsmodule.ActionGitHubReviewPost,
		Resource:      prResource(params.owner, params.repo),
		Args:          args,
		Preconditions: connectorsmodule.DispatchPreconditions{ExpectedHeadSha: expectedHeadSha},
		CallSeq:       0,
	})
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	writeJSON(w, pullRequestReviewResultFromBody(res.Body))
}

// authorizeRepo enforces workspace membership and returns the CANONICAL
// owner/repo (as registered) for the caller to use everywhere downstream —
// grant resource/pattern/id and the dispatch resource — so casing can never
// decouple the seeded grant from the dispatched resource.
func (m *Module) authorizeRepo(w http.ResponseWriter, r *http.Request, ws, owner, repo string) (canonOwner, canonRepo string, ok bool) {
	canonOwner, canonRepo, found, err := m.workspaceHasRepo(r.Context(), ws, owner, repo)
	if err != nil {
		writePRReviewError(w, err)
		return "", "", false
	}
	if !found {
		writePRReviewErrorCode(w, http.StatusNotFound, "repo_not_registered",
			fmt.Sprintf("repository %s/%s is not registered in workspace %s", owner, repo, ws), false)
		return "", "", false
	}
	return canonOwner, canonRepo, true
}

func pullRequestArgs(params pullRequestPath) map[string]any {
	return map[string]any{
		"owner":  params.owner,
		"repo":   params.repo,
		"number": params.number,
	}
}

// decodeReviewRequest parses and validates a review POST body, writing the
// HTTP error itself and returning ok=false on failure.
func decodeReviewRequest(w http.ResponseWriter, r *http.Request) (req pullRequestReviewRequest, event, expectedHeadSHA string, ok bool) {
	if err := handler.DecodeOneJSON(w, r, &req, handler.JSONDecodeOptions{DisallowUnknownFields: true}); err != nil {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid review request body", false)
		return req, "", "", false
	}
	expectedHeadSHA = strings.TrimSpace(req.ExpectedHeadSha)
	if expectedHeadSHA == "" {
		writePRReviewErrorCode(w, http.StatusPreconditionRequired, "precondition_required", "expected_head_sha is required", false)
		return req, "", "", false
	}
	event, ok = githubReviewEvent(req.Event)
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid review event", false)
		return req, "", "", false
	}
	return req, event, expectedHeadSHA, true
}

// reviewSubmissionRunID builds the dispatch run id for a review POST. Unlike
// the read paths — where a deterministic runID intentionally collapses the
// audit trail of a polled endpoint — every review submission needs a UNIQUE
// identity: CallID and the provider Idempotency-Key derive from
// runID#action#seq, so a deterministic runID would make a retry after a
// stale-head refresh, or a second decision on the same PR, collide with the
// first submission.
func reviewSubmissionRunID(r *http.Request, params pullRequestPath) (string, error) {
	nonce, err := randomHex(8)
	if err != nil {
		return "", fmt.Errorf("review run id nonce: %w", err)
	}
	return syntheticRunID(r, params, connectorsmodule.ActionGitHubReviewPost) + ":" + nonce, nil
}

func syntheticRunID(r *http.Request, params pullRequestPath, action string) string {
	userID := "unknown"
	if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && strings.TrimSpace(identity.UserID) != "" {
		userID = strings.TrimSpace(identity.UserID)
	}
	return "webui-review:" + userID + ":" + params.owner + "/" + params.repo + "#" + fmt.Sprint(params.number) + ":" + action
}

func pullRequestDetailFromBody(body map[string]any) pullRequestDetail {
	return pullRequestDetail{
		Number:      intValue(body["number"]),
		State:       stringValue(body["state"]),
		Title:       stringValue(body["title"]),
		IsDraft:     boolValue(body["draft"]),
		HeadRefName: stringValue(body["headRef"]),
		BaseRefName: stringValue(body["baseRef"]),
		HeadSha:     stringValue(body["headSha"]),
		Merged:      boolValue(body["merged"]),
	}
}

func pullRequestDiffFromBody(body map[string]any) pullRequestDiff {
	files := []pullRequestDiffFile{}
	if rawFiles, ok := body["files"].([]map[string]any); ok {
		for _, raw := range rawFiles {
			files = append(files, pullRequestDiffFileFromBody(raw))
		}
	} else if rawFiles, ok := body["files"].([]any); ok {
		for _, entry := range rawFiles {
			raw, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			files = append(files, pullRequestDiffFileFromBody(raw))
		}
	}
	return pullRequestDiff{
		Files: files,
		Diff:  stringValue(body["diff"]),
	}
}

func pullRequestDiffFileFromBody(body map[string]any) pullRequestDiffFile {
	path := stringValue(body["path"])
	if path == "" {
		path = stringValue(body["filename"])
	}
	return pullRequestDiffFile{
		Path:      path,
		Status:    stringValue(body["status"]),
		Additions: intValue(body["additions"]),
		Deletions: intValue(body["deletions"]),
		Patch:     stringValue(body["patch"]),
	}
}

func githubReviewEvent(event pullRequestReviewRequestEvent) (string, bool) {
	switch event {
	case pullRequestReviewApprove:
		return "APPROVE", true
	case pullRequestReviewRequestChanges:
		return "REQUEST_CHANGES", true
	case pullRequestReviewComment:
		return "COMMENT", true
	default:
		return "", false
	}
}

func pullRequestReviewResultFromBody(body map[string]any) pullRequestReviewResult {
	return pullRequestReviewResult{
		ReviewId: intPtrFromValue(body["id"]),
		State:    stringPtrFromValue(body["state"]),
	}
}

func stringPtrFromValue(v any) *string {
	s := stringValue(v)
	if s == "" {
		return nil
	}
	return &s
}

func intPtrFromValue(v any) *int {
	i := intValue(v)
	if i == 0 {
		return nil
	}
	return &i
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case jsonNumber:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

type jsonNumber interface {
	Int64() (int64, error)
}
