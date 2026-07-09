package prreview

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func (m *Module) getPullRequest(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return
	}
	params.owner, params.repo = canonOwner, canonRepo
	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReviewActions); err != nil {
		writePRReviewError(w, err)
		return
	}
	res, err := m.dispatcher.Dispatch(r.Context(), connector.Request{
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
	writeJSON(w, http.StatusOK, pullRequestDetailFromBody(res.Body))
}

func (m *Module) getPullRequestDiff(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	params, ok := parsePullRequestPath(r.PathValue("owner"), r.PathValue("repo"), r.PathValue("number"))
	if !ok {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "invalid pull request path", false)
		return
	}
	canonOwner, canonRepo, ok := m.authorizeRepo(w, r, ws, params.owner, params.repo)
	if !ok {
		return
	}
	params.owner, params.repo = canonOwner, canonRepo
	if err := m.ensureConnectorAndGrants(r.Context(), ws, params.owner, params.repo, prReviewActions); err != nil {
		writePRReviewError(w, err)
		return
	}
	runID := syntheticRunID(r, params, providers.ActionGitHubCompareRead)
	detail, err := m.dispatcher.Dispatch(r.Context(), connector.Request{
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
	res, err := m.dispatcher.Dispatch(r.Context(), connector.Request{
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
	writeJSON(w, http.StatusOK, pullRequestDiffFromBody(res.Body))
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
