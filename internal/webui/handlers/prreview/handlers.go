package prreview

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

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
		RunID:        syntheticRunID(r, params, providers.ActionGitHubPullRequestRead),
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       providers.ActionGitHubPullRequestRead,
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
	runID := syntheticRunID(r, params, providers.ActionGitHubCompareRead)
	detail, err := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
		WorkspaceKey: ws,
		RunID:        runID,
		BindingID:    bindingID,
		ConnectorID:  connectorID,
		Action:       providers.ActionGitHubPullRequestRead,
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
		Action:       providers.ActionGitHubCompareRead,
		Resource:     prResource(params.owner, params.repo),
		Args: map[string]any{
			"owner": params.owner,
			"repo":  params.repo,
			"base":  baseRef,
			"head":  headSha,
		},
		Preconditions: providers.Preconditions{ExpectedHeadSha: headSha},
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
		Action:        providers.ActionGitHubReviewPost,
		Resource:      prResource(params.owner, params.repo),
		Args:          args,
		Preconditions: providers.Preconditions{ExpectedHeadSha: expectedHeadSha},
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
func decodeReviewRequest(w http.ResponseWriter, r *http.Request) (req gen.PullRequestReviewRequest, event, expectedHeadSha string, ok bool) {
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid review request body", false)
		return req, "", "", false
	}
	expectedHeadSha = strings.TrimSpace(req.ExpectedHeadSha)
	if expectedHeadSha == "" {
		writePRReviewErrorCode(w, http.StatusPreconditionRequired, "precondition_required", "expected_head_sha is required", false)
		return req, "", "", false
	}
	event, ok = githubReviewEvent(req.Event)
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid review event", false)
		return req, "", "", false
	}
	return req, event, expectedHeadSha, true
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
	return syntheticRunID(r, params, providers.ActionGitHubReviewPost) + ":" + nonce, nil
}

func syntheticRunID(r *http.Request, params pullRequestPath, action string) string {
	userID := "unknown"
	if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && strings.TrimSpace(identity.UserID) != "" {
		userID = strings.TrimSpace(identity.UserID)
	}
	return "webui-review:" + userID + ":" + params.owner + "/" + params.repo + "#" + fmt.Sprint(params.number) + ":" + action
}

func pullRequestDetailFromBody(body map[string]any) gen.PullRequestDetail {
	return gen.PullRequestDetail{
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

func pullRequestDiffFromBody(body map[string]any) gen.PullRequestDiff {
	files := []gen.PullRequestDiffFile{}
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
	return gen.PullRequestDiff{
		Files: files,
		Diff:  stringValue(body["diff"]),
	}
}

func pullRequestDiffFileFromBody(body map[string]any) gen.PullRequestDiffFile {
	path := stringValue(body["path"])
	if path == "" {
		path = stringValue(body["filename"])
	}
	return gen.PullRequestDiffFile{
		Path:      path,
		Status:    stringValue(body["status"]),
		Additions: intValue(body["additions"]),
		Deletions: intValue(body["deletions"]),
		Patch:     stringValue(body["patch"]),
	}
}

func githubReviewEvent(event gen.PullRequestReviewRequestEvent) (string, bool) {
	switch event {
	case gen.PullRequestReviewRequestEventApprove:
		return "APPROVE", true
	case gen.PullRequestReviewRequestEventRequestChanges:
		return "REQUEST_CHANGES", true
	case gen.PullRequestReviewRequestEventComment:
		return "COMMENT", true
	default:
		return "", false
	}
}

func pullRequestReviewResultFromBody(body map[string]any) gen.PullRequestReviewResult {
	return gen.PullRequestReviewResult{
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
