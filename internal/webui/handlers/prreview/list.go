package prreview

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type pullRequestsData struct {
	PullRequests []ops.GitPullRequest `json:"pull_requests"`
	Warnings     []string             `json:"warnings,omitempty"`
}

const (
	pullsListPerPage  = 100
	maxPullsListPages = 5
)

func (m *Module) listPullRequests(w http.ResponseWriter, r *http.Request) {
	ws := canonicalWorkspaceFromRequest(r)
	if ws == "" {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "canonical workspace is required", false)
		return
	}
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = "all"
	}

	if !m.connectorListAvailable() {
		var warnings []string
		if m != nil && m.dispatcher != nil && !m.githubTokenConfigured() {
			warnings = append(warnings, connectorUnavailableWarning)
		}
		m.ghListFallback(w, r.Context(), ws, state, warnings...)
		return
	}

	// The connector pulls API can't represent "merged" (no merged filter/field),
	// so serve that filter from gh directly instead of issuing N failing 422s
	// and only then falling back.
	if strings.EqualFold(state, "merged") {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	repos, err := m.workspaceRepos(r.Context(), ws)
	if err != nil || len(repos) == 0 {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	prs, warnings, attempted, failed := m.connectorListPullRequests(r, ws, state, repos)

	// Fall back to gh when the connector learned nothing: either no repo was
	// parseable or every repo errored.
	if len(prs) == 0 && (attempted == 0 || failed == attempted) {
		// When the connector was configured and actually tried but failed for
		// every repo, surface that (with the per-repo reasons) instead of
		// silently pretending gh is the intended source.
		notice := append([]string(nil), warnings...)
		if attempted > 0 {
			notice = append([]string{connectorUnavailableWarning}, notice...)
		}
		m.ghListFallback(w, r.Context(), ws, state, notice...)
		return
	}

	writeJSON(w, pullRequestsData{PullRequests: prs, Warnings: warnings})
}

// connectorListPullRequests lists PRs for every connector-eligible workspace
// repo, accumulating per-repo warnings instead of failing the whole list.
// attempted/failed let the caller distinguish "no repo was eligible" from
// "the connector tried and failed everywhere".
func (m *Module) connectorListPullRequests(r *http.Request, ws, state string, repos []*domain.Repo) (prs []ops.GitPullRequest, warnings []string, attempted, failed int) {
	prs = []ops.GitPullRequest{}
	for _, workspaceRepo := range repos {
		owner, repo, ok := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !ok {
			if strings.TrimSpace(workspaceRepo.RemoteURL) != "" {
				warnings = append(warnings, fmt.Sprintf(
					"%s: remote URL is not a supported GitHub URL", workspaceRepo.Name,
				))
			}
			continue
		}
		attempted++
		if err := m.ensureConnectorAndGrants(r.Context(), ws, owner, repo, prReadActions); err != nil {
			failed++
			warnings = append(warnings, repoWarning(owner, repo, err))
			continue
		}
		repoPRs, truncated, err := m.connectorListPullRequestsForRepo(
			r, ws, state, owner, repo, workspaceRepo.Name,
		)
		prs = append(prs, repoPRs...)
		if err != nil {
			failed++
			warnings = append(warnings, repoWarning(owner, repo, err))
			continue
		}
		if truncated {
			warnings = append(warnings, pullsListTruncationWarning(owner, repo))
		}
	}
	return prs, warnings, attempted, failed
}

func (m *Module) connectorListPullRequestsForRepo(
	r *http.Request,
	ws, state, owner, repo, sourceRepo string,
) (prs []ops.GitPullRequest, truncated bool, err error) {
	prs = []ops.GitPullRequest{}
	for page := 1; page <= maxPullsListPages; page++ {
		res, dispatchErr := m.dispatcher.Dispatch(r.Context(), connectorsmodule.DispatchCommand{
			WorkspaceKey: ws,
			RunID:        listRunID(r, owner, repo),
			BindingID:    bindingID,
			ConnectorID:  connectorID,
			Action:       connectorsmodule.ActionGitHubPullsList,
			Resource:     prResource(owner, repo),
			Args: map[string]any{
				"owner":   owner,
				"repo":    repo,
				"state":   connectorListState(state),
				"perPage": pullsListPerPage,
				"page":    page,
			},
			CallSeq: page - 1,
		})
		if dispatchErr != nil {
			return prs, false, dispatchErr
		}
		pagePRs := pullRequestsFromBody(owner, repo, sourceRepo, res.Body)
		prs = append(prs, pagePRs...)
		if len(pagePRs) < pullsListPerPage {
			return prs, false, nil
		}
	}
	return prs, true, nil
}

func pullsListTruncationWarning(owner, repo string) string {
	return fmt.Sprintf("%s/%s: pull request list truncated after %d entries", owner, repo, pullsListPerPage*maxPullsListPages)
}

func (m *Module) connectorListAvailable() bool {
	if m == nil || m.dispatcher == nil {
		return false
	}
	return m.githubTokenConfigured()
}

// connectorUnavailableWarning is surfaced (via the response warnings the PR
// list already renders) when the connector was configured but failed for every
// repo and we fell back to gh — so a broken connector isn't invisible.
const connectorUnavailableWarning = "GitHub connector unavailable — showing local pull requests instead"

func (m *Module) ghListFallback(w http.ResponseWriter, ctx context.Context, ws, state string, priorWarnings ...string) {
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
	warnings := append([]string{}, priorWarnings...)
	if res != nil {
		if res.PullRequests != nil {
			prs = res.PullRequests
		}
		warnings = append(warnings, res.Warnings...)
	}
	writeJSON(w, pullRequestsData{PullRequests: prs, Warnings: warnings})
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
	return "webui-review:" + userID + ":" + owner + "/" + repo + ":list:" + connectorsmodule.ActionGitHubPullsList
}

func pullRequestsFromBody(owner, repo, sourceRepo string, body map[string]any) []ops.GitPullRequest {
	prs := []ops.GitPullRequest{}
	if rawPulls, ok := body["pullRequests"].([]map[string]any); ok {
		for _, raw := range rawPulls {
			prs = append(prs, pullRequestFromSummary(owner, repo, sourceRepo, raw))
		}
		return prs
	}
	if rawPulls, ok := body["pullRequests"].([]any); ok {
		for _, entry := range rawPulls {
			raw, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			prs = append(prs, pullRequestFromSummary(owner, repo, sourceRepo, raw))
		}
	}
	return prs
}

func pullRequestFromSummary(owner, repo, sourceRepo string, body map[string]any) ops.GitPullRequest {
	number := intValue(body["number"])
	repoName := owner + "/" + repo
	// GitHub's REST list payload does not expose aggregate review decision,
	// so ReviewDecision intentionally remains empty on the connector path.
	return ops.GitPullRequest{
		Number:      number,
		Title:       stringValue(body["title"]),
		URL:         fmt.Sprintf("https://github.com/%s/pull/%d", repoName, number),
		State:       normalizePullState(stringValue(body["state"]), boolValue(body["merged"])),
		IsDraft:     boolValue(body["draft"]),
		HeadRefName: stringValue(body["headRef"]),
		BaseRefName: stringValue(body["baseRef"]),
		AuthorLogin: stringValue(body["authorLogin"]),
		UpdatedAt:   stringValue(body["updatedAt"]),
		RepoName:    repoName,
		SourceRepo:  sourceRepo,
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
