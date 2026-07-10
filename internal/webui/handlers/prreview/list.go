package prreview

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type pullRequestsData struct {
	PullRequests []ops.GitPullRequest `json:"pull_requests"`
	Warnings     []string             `json:"warnings,omitempty"`
}

func (m *Module) listPullRequests(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = "all"
	}

	if !m.connectorListAvailable() {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	// The connector pulls API can't represent "merged" (no merged filter/field),
	// so serve that filter from gh directly instead of issuing N failing 422s
	// and only then falling back.
	if strings.EqualFold(state, "merged") {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	data, err := storeadapter.BuildWorkspaceDataForKey(r.Context(), m.store, ws)
	if err != nil || data == nil || len(data.Repos) == 0 {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	prs := []ops.GitPullRequest{}
	var warnings []string
	attempted := 0
	failed := 0
	for _, workspaceRepo := range data.Repos {
		owner, repo, ok := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !ok {
			continue
		}
		attempted++
		if err := m.ensureConnectorAndGrants(r.Context(), ws, owner, repo, prReviewActions); err != nil {
			failed++
			warnings = append(warnings, repoWarning(owner, repo, err))
			continue
		}
		res, err := m.dispatcher.Dispatch(r.Context(), connector.Request{
			WorkspaceKey: ws,
			RunID:        listRunID(r, owner, repo),
			BindingID:    bindingID,
			ConnectorID:  connectorID,
			Action:       providers.ActionGitHubPullsList,
			Resource:     prResource(owner, repo),
			Args: map[string]any{
				"owner": owner,
				"repo":  repo,
				"state": connectorListState(state),
			},
			CallSeq: 0,
		})
		if err != nil {
			failed++
			warnings = append(warnings, repoWarning(owner, repo, err))
			continue
		}
		prs = append(prs, pullRequestsFromBody(owner, repo, res.Body)...)
	}

	// Fall back to gh when the connector learned nothing: either no repo was
	// parseable (attempted == 0 — e.g. ssh-scheme remotes the parser rejects,
	// which the old gh-only route would have listed) or every repo errored.
	if len(prs) == 0 && (attempted == 0 || failed == attempted) {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	writeJSON(w, http.StatusOK, pullRequestsData{PullRequests: prs, Warnings: warnings})
}

func (m *Module) connectorListAvailable() bool {
	if m == nil || m.dispatcher == nil {
		return false
	}
	if strings.TrimSpace(os.Getenv(webuiGitHubTokenEnv)) == "" {
		return false
	}
	_, err := connector.NewVaultFromEnv()
	return err == nil
}

func (m *Module) ghListFallback(w http.ResponseWriter, ctx context.Context, ws, state string) {
	if m == nil || m.agentSvc == nil {
		writePRReviewError(w, errEgressUnavailable)
		return
	}
	res, err := m.agentSvc.ListPullRequests(ctx, ws, state)
	if err != nil {
		writePRReviewErrorCode(w, http.StatusBadGateway, "upstream_error", err.Error(), true)
		return
	}
	prs := []ops.GitPullRequest{}
	var warnings []string
	if res != nil {
		if res.PullRequests != nil {
			prs = res.PullRequests
		}
		warnings = res.Warnings
	}
	writeJSON(w, http.StatusOK, pullRequestsData{PullRequests: prs, Warnings: warnings})
}

func connectorListState(state string) string {
	if strings.EqualFold(state, "review") {
		return "open"
	}
	return state
}

func listRunID(r *http.Request, owner, repo string) string {
	userID := "unknown"
	if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && strings.TrimSpace(identity.UserID) != "" {
		userID = strings.TrimSpace(identity.UserID)
	}
	return "webui-review:" + userID + ":" + owner + "/" + repo + ":list:" + providers.ActionGitHubPullsList
}

func pullRequestsFromBody(owner, repo string, body map[string]any) []ops.GitPullRequest {
	prs := []ops.GitPullRequest{}
	if rawPulls, ok := body["pullRequests"].([]map[string]any); ok {
		for _, raw := range rawPulls {
			prs = append(prs, pullRequestFromSummary(owner, repo, raw))
		}
		return prs
	}
	if rawPulls, ok := body["pullRequests"].([]any); ok {
		for _, entry := range rawPulls {
			raw, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			prs = append(prs, pullRequestFromSummary(owner, repo, raw))
		}
	}
	return prs
}

func pullRequestFromSummary(owner, repo string, body map[string]any) ops.GitPullRequest {
	number := intValue(body["number"])
	repoName := owner + "/" + repo
	return ops.GitPullRequest{
		Number:      number,
		Title:       stringValue(body["title"]),
		URL:         fmt.Sprintf("https://github.com/%s/pull/%d", repoName, number),
		State:       normalizePullState(stringValue(body["state"]), boolValue(body["merged"])),
		IsDraft:     boolValue(body["draft"]),
		HeadRefName: stringValue(body["headRef"]),
		BaseRefName: stringValue(body["baseRef"]),
		RepoName:    repoName,
	}
}

// normalizePullState converts GitHub REST's lowercase pull state ("open" /
// "closed") into the UPPERCASE form the rest of loom speaks (the `gh` path
// emits OPEN/CLOSED/MERGED, and the frontend keys its open/merged filters off
// those exact strings — a lowercase state renders an empty PR list).
func normalizePullState(state string, merged bool) string {
	if merged {
		return "MERGED"
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "open":
		return "OPEN"
	case "closed":
		return "CLOSED"
	case "merged":
		return "MERGED"
	case "":
		return ""
	default:
		return strings.ToUpper(state)
	}
}

func repoWarning(owner, repo string, err error) string {
	return owner + "/" + repo + ": " + sanitizeWarning(err)
}

func sanitizeWarning(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	if len(msg) > 240 {
		msg = strings.TrimSpace(msg[:240])
	}
	return msg
}
