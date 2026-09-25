package prreview

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/prref"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

type pullRequestsData struct {
	PullRequests []ops.GitPullRequest `json:"pull_requests"`
	Warnings     []string             `json:"warnings,omitempty"`
	// DeliveryGroups is a page of durable groups from FleetDB (additive).
	DeliveryGroups []deliveryGroupView `json:"delivery_groups,omitempty"`
	// DeliveryGroupsCount is the page length only — never a workspace total.
	DeliveryGroupsCount int `json:"delivery_groups_count,omitempty"`
	// DeliveryGroupsHasMore is true when another group page may exist.
	DeliveryGroupsHasMore bool `json:"delivery_groups_has_more,omitempty"`
	// DeliveryGroupsNextCursor is the opaque cursor for the next group page.
	DeliveryGroupsNextCursor string `json:"delivery_groups_next_cursor,omitempty"`
	// StandaloneContinuation reports per-repo discovery bounds so clients can
	// fetch beyond the connector's five-page cap without treating absence as
	// completeness.
	StandaloneContinuation *standaloneContinuation `json:"standalone_continuation,omitempty"`
}

// standaloneContinuation is the honest bounded-discovery contract for
// connector list pages (maxPullsListPages × pullsListPerPage per repo).
type standaloneContinuation struct {
	Repos    []standaloneRepoContinuation `json:"repos"`
	HasMore  bool                         `json:"has_more"`
	Complete bool                         `json:"complete"`
}

type standaloneRepoContinuation struct {
	Repo           string `json:"repo"`
	SourceRepo     string `json:"source_repo,omitempty"`
	Fetched        int    `json:"fetched"`
	PageSize       int    `json:"page_size"`
	MaxPages       int    `json:"max_pages"`
	NextPage       int    `json:"next_page,omitempty"`
	HasMore        bool   `json:"has_more"`
	PartialError   string `json:"partial_error,omitempty"`
	ContinuationOf string `json:"continuation_hint,omitempty"`
}

const (
	pullsListPerPage  = 100
	maxPullsListPages = 5
)

func (m *Module) listPullRequests(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
		state = "all"
	}

	// Optional single-repo standalone continuation: fetch one additional
	// bounded window starting at standalone_page for standalone_repo.
	contRepo := strings.TrimSpace(r.URL.Query().Get("standalone_repo"))
	contPage := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("standalone_page")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "standalone_page must be a positive integer", false)
			return
		}
		contPage = n
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

	data, err := storeadapter.BuildWorkspaceDataForKey(r.Context(), m.store, ws)
	if err != nil || data == nil || len(data.Repos) == 0 {
		m.ghListFallback(w, r.Context(), ws, state)
		return
	}

	var (
		prs               []ops.GitPullRequest
		warnings          []string
		attempted, failed int
		continuation      *standaloneContinuation
	)
	if contRepo != "" {
		if contPage < 1 {
			contPage = 1
		}
		prs, warnings, continuation, attempted, failed = m.connectorContinueStandalone(r, ws, state, data.Repos, contRepo, contPage)
	} else {
		prs, warnings, continuation, attempted, failed = m.connectorListPullRequests(r, ws, state, data.Repos)
	}

	// Fall back to gh when the connector learned nothing: either no repo was
	// parseable or every repo errored. When durable delivery groups are
	// available, still return them with explicit warnings rather than failing
	// the whole Pull Requests surface on a GitHub outage.
	if len(prs) == 0 && (attempted == 0 || failed == attempted) && contRepo == "" {
		notice := append([]string(nil), warnings...)
		if attempted > 0 {
			notice = append([]string{connectorUnavailableWarning}, notice...)
		}
		if m.deliveryGroups != nil {
			out := pullRequestsData{
				PullRequests:           []ops.GitPullRequest{},
				Warnings:               notice,
				StandaloneContinuation: continuation,
			}
			if continuation != nil {
				continuation.Complete = false
			}
			m.attachDeliveryGroupsPage(r, ws, &out)
			writeJSON(w, out)
			return
		}
		m.ghListFallback(w, r.Context(), ws, state, notice...)
		return
	}

	m.observeListedPullRequests(ws, prs)
	grouped, groupWarnings, _ := m.loadActiveGroupedPRKeys(r, ws)
	warnings = append(warnings, groupWarnings...)
	standalone := filterStandalonePullRequests(prs, grouped)

	out := pullRequestsData{
		PullRequests:           standalone,
		Warnings:               warnings,
		StandaloneContinuation: continuation,
	}
	m.attachDeliveryGroupsPage(r, ws, &out)
	writeJSON(w, out)
}

// connectorListPullRequests lists PRs for every connector-eligible workspace
// repo, accumulating per-repo warnings instead of failing the whole list.
// attempted/failed let the caller distinguish "no repo was eligible" from
// "the connector tried and failed everywhere".
func (m *Module) connectorListPullRequests(r *http.Request, ws, state string, repos []ops.WorkspaceRepo) (
	prs []ops.GitPullRequest,
	warnings []string,
	continuation *standaloneContinuation,
	attempted, failed int,
) {
	prs = []ops.GitPullRequest{}
	cont := &standaloneContinuation{Repos: []standaloneRepoContinuation{}, Complete: true}
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
			msg := repoWarning(owner, repo, err)
			warnings = append(warnings, msg)
			cont.Repos = append(cont.Repos, standaloneRepoContinuation{
				Repo: owner + "/" + repo, SourceRepo: workspaceRepo.Name,
				PageSize: pullsListPerPage, MaxPages: maxPullsListPages,
				PartialError: msg, HasMore: false,
			})
			cont.Complete = false
			continue
		}
		repoPRs, truncated, err := m.connectorListPullRequestsForRepo(
			r, ws, state, owner, repo, workspaceRepo.Name, 1, maxPullsListPages,
		)
		prs = append(prs, repoPRs...)
		repoCont := standaloneRepoContinuation{
			Repo:       owner + "/" + repo,
			SourceRepo: workspaceRepo.Name,
			Fetched:    len(repoPRs),
			PageSize:   pullsListPerPage,
			MaxPages:   maxPullsListPages,
		}
		if err != nil {
			failed++
			msg := repoWarning(owner, repo, err)
			warnings = append(warnings, msg)
			repoCont.PartialError = msg
			cont.Complete = false
		}
		if truncated {
			warnings = append(warnings, pullsListTruncationWarning(owner, repo))
			repoCont.HasMore = true
			repoCont.NextPage = maxPullsListPages + 1
			repoCont.ContinuationOf = fmt.Sprintf(
				"GET .../pull-requests?standalone_repo=%s/%s&standalone_page=%d",
				owner, repo, maxPullsListPages+1,
			)
			cont.HasMore = true
			cont.Complete = false
		}
		cont.Repos = append(cont.Repos, repoCont)
	}
	return prs, warnings, cont, attempted, failed
}

// connectorContinueStandalone fetches one bounded window for a single repo
// starting at startPage (inclusive), up to maxPullsListPages pages.
func (m *Module) connectorContinueStandalone(
	r *http.Request, ws, state string, repos []ops.WorkspaceRepo, wantRepo string, startPage int,
) (prs []ops.GitPullRequest, warnings []string, continuation *standaloneContinuation, attempted, failed int) {
	prs = []ops.GitPullRequest{}
	cont := &standaloneContinuation{Repos: []standaloneRepoContinuation{}, Complete: true}
	wantRepo = strings.ToLower(strings.TrimSpace(wantRepo))
	for _, workspaceRepo := range repos {
		owner, repo, ok := parseGitHubOwnerRepo(workspaceRepo.RemoteURL)
		if !ok || strings.ToLower(owner+"/"+repo) != wantRepo {
			continue
		}
		attempted++
		if err := m.ensureConnectorAndGrants(r.Context(), ws, owner, repo, prReadActions); err != nil {
			failed++
			msg := repoWarning(owner, repo, err)
			warnings = append(warnings, msg)
			cont.Repos = append(cont.Repos, standaloneRepoContinuation{
				Repo: owner + "/" + repo, SourceRepo: workspaceRepo.Name,
				PageSize: pullsListPerPage, MaxPages: maxPullsListPages,
				PartialError: msg,
			})
			cont.Complete = false
			return prs, warnings, cont, attempted, failed
		}
		repoPRs, truncated, err := m.connectorListPullRequestsForRepo(
			r, ws, state, owner, repo, workspaceRepo.Name, startPage, maxPullsListPages,
		)
		prs = append(prs, repoPRs...)
		repoCont := standaloneRepoContinuation{
			Repo: owner + "/" + repo, SourceRepo: workspaceRepo.Name,
			Fetched: len(repoPRs), PageSize: pullsListPerPage, MaxPages: maxPullsListPages,
		}
		if err != nil {
			failed++
			msg := repoWarning(owner, repo, err)
			warnings = append(warnings, msg)
			repoCont.PartialError = msg
			cont.Complete = false
		}
		if truncated {
			warnings = append(warnings, pullsListTruncationWarning(owner, repo))
			repoCont.HasMore = true
			repoCont.NextPage = startPage + maxPullsListPages
			repoCont.ContinuationOf = fmt.Sprintf(
				"GET .../pull-requests?standalone_repo=%s/%s&standalone_page=%d",
				owner, repo, startPage+maxPullsListPages,
			)
			cont.HasMore = true
			cont.Complete = false
		}
		cont.Repos = append(cont.Repos, repoCont)
		return prs, warnings, cont, attempted, failed
	}
	warnings = append(warnings, wantRepo+": standalone_repo is not a registered GitHub repository")
	cont.Complete = false
	return prs, warnings, cont, attempted, failed
}

func (m *Module) connectorListPullRequestsForRepo(
	r *http.Request,
	ws, state, owner, repo, sourceRepo string,
	startPage, pageBudget int,
) (prs []ops.GitPullRequest, truncated bool, err error) {
	if startPage < 1 {
		startPage = 1
	}
	if pageBudget < 1 {
		pageBudget = maxPullsListPages
	}
	prs = []ops.GitPullRequest{}
	endPage := startPage + pageBudget - 1
	for page := startPage; page <= endPage; page++ {
		res, dispatchErr := m.dispatcher.Dispatch(r.Context(), connector.Request{
			WorkspaceKey: ws,
			RunID:        listRunID(r, owner, repo),
			BindingID:    bindingID,
			ConnectorID:  connectorID,
			Action:       providers.ActionGitHubPullsList,
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

func (m *Module) attachDeliveryGroupsPage(r *http.Request, ws string, out *pullRequestsData) {
	if m == nil || m.deliveryGroups == nil || out == nil {
		return
	}
	limit := defaultDeliveryGroupLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("delivery_groups_limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= maxDeliveryGroupListLimit {
			limit = n
		}
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("delivery_groups_cursor"))
	page, err := m.deliveryGroups.List(r.Context(), ws, store.DeliveryGroupListOpts{
		State:  "active",
		Limit:  limit,
		Cursor: cursor,
	})
	if err != nil {
		out.Warnings = append(out.Warnings, "delivery groups unavailable: "+sanitizeWarning(err))
		return
	}
	views, warnings := m.decorateDeliveryGroups(r, ws, page.Groups, false)
	out.DeliveryGroups = views
	out.DeliveryGroupsCount = page.Count
	out.DeliveryGroupsHasMore = page.HasMore
	out.DeliveryGroupsNextCursor = page.NextCursor
	out.Warnings = append(out.Warnings, warnings...)
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
	m.observeListedPullRequests(ws, prs)
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
	return "webui-review:" + userID + ":" + owner + "/" + repo + ":list:" + providers.ActionGitHubPullsList
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
	// Identity is the base repo, never a fork head. GitHub's html_url names the
	// base repo's current owner/repo (it follows renames and transfers), so
	// derive pr_key from it — as the gh fallback does — keeping url and pr_key
	// in agreement. Fall back to the registered remote when it is absent.
	htmlURL := stringValue(body["htmlUrl"])
	prKey := prref.Format(owner, repo, number)
	if ref, ok := prref.FromURL(htmlURL); ok && ref.Number == number {
		prKey = ref.Key()
	} else {
		htmlURL = fmt.Sprintf("https://github.com/%s/pull/%d", repoName, number)
	}
	// GitHub's REST list payload does not expose aggregate review decision,
	// so ReviewDecision intentionally remains empty on the connector path.
	return ops.GitPullRequest{
		Number:      number,
		PRKey:       prKey,
		NodeID:      stringValue(body["nodeId"]),
		HeadSHA:     stringValue(body["headSha"]),
		Title:       stringValue(body["title"]),
		URL:         htmlURL,
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
