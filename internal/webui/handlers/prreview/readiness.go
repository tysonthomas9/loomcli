package prreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/connector/providers"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/prreadiness"
	"github.com/tysonthomas9/loomcli/internal/prref"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// Readiness read bounds. GitHub GraphQL allows 5,000 points/hour and costs a
// readiness query about 1 point, so one query per repository per page poll
// (30 s) stays far below budget; readinessMinRefetch additionally coalesces
// bursts (several tabs, list+detail loads) into one read per repository.
const (
	maxReadinessKeys         = 100
	maxPreviewKeys           = 50
	readinessRepoConcurrency = 4
	readinessRepoTimeout     = 8 * time.Second
	readinessRequestTimeout  = 12 * time.Second
	readinessMinRefetch      = 15 * time.Second
	// readinessDefaultRateLimitWait applies when GitHub rate-limits without
	// saying for how long.
	readinessDefaultRateLimitWait = 60 * time.Second
)

// readinessComputingBackoff re-reads PRs GitHub is still computing
// (mergeable UNKNOWN) inside the repository deadline.
var readinessComputingBackoff = []time.Duration{time.Second, 2 * time.Second}

// readinessRepoError is a structured per-repository read failure. PR rows
// named in PRKeys keep their last-known snapshot, shown as history.
type readinessRepoError struct {
	Repo              string                `json:"repo"`
	SourceRepo        string                `json:"source_repo,omitempty"`
	Code              prreadiness.ErrorCode `json:"code"`
	Retryable         bool                  `json:"retryable"`
	RetryAfterSeconds int                   `json:"retry_after_s,omitempty"`
	Message           string                `json:"message,omitempty"`
	PRKeys            []string              `json:"pr_keys"`
}

type readinessListData struct {
	ServerNow         time.Time            `json:"server_now"`
	FreshForSeconds   int                  `json:"fresh_for_s"`
	StaleAfterSeconds int                  `json:"stale_after_s"`
	PullRequests      []prreadiness.View   `json:"pull_requests"`
	RepoErrors        []readinessRepoError `json:"repo_errors"`
}

type readinessPreviewData struct {
	ServerNow         time.Time            `json:"server_now"`
	FreshForSeconds   int                  `json:"fresh_for_s"`
	StaleAfterSeconds int                  `json:"stale_after_s"`
	Preview           prreadiness.Preview  `json:"preview"`
	RepoErrors        []readinessRepoError `json:"repo_errors"`
}

// getPullRequestReadiness serves GET .../pull-requests/readiness?pr=<key>...
// It re-reads GitHub for requested PRs whose snapshot is missing, invalidated
// or older than readinessMinRefetch (force=1 always re-reads), then returns
// every requested PR's view. A failing repository never fails the request:
// its rows keep their last-known snapshot and it is listed in repo_errors.
func (m *Module) getPullRequestReadiness(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	refs, ok := parseReadinessKeys(w, r, maxReadinessKeys, false)
	if !ok {
		return
	}
	force := parseBoolQuery(r.URL.Query().Get("force"))
	repoErrs, err := m.refreshReadiness(r, ws, refs, force)
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	now := m.readinessClock()
	views := make([]prreadiness.View, 0, len(refs))
	for _, ref := range refs {
		views = append(views, m.readiness.view(ws, ref.Key(), now))
	}
	writeJSON(w, readinessListData{
		ServerNow:         now.UTC(),
		FreshForSeconds:   int(prreadiness.FreshFor / time.Second),
		StaleAfterSeconds: int(prreadiness.StaleAfter / time.Second),
		PullRequests:      views,
		RepoErrors:        repoErrs,
	})
}

// getPullRequestReadinessPreview serves the read-only ordered preview:
// GET .../pull-requests/readiness/preview?pr=<first>&pr=<second>...
// Opening a preview always re-reads every member; nothing is merged.
func (m *Module) getPullRequestReadinessPreview(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	refs, ok := parseReadinessKeys(w, r, maxPreviewKeys, true)
	if !ok {
		return
	}
	repoErrs, err := m.refreshReadiness(r, ws, refs, true)
	if err != nil {
		writePRReviewError(w, err)
		return
	}
	now := m.readinessClock()
	views := make([]prreadiness.View, 0, len(refs))
	for _, ref := range refs {
		views = append(views, m.readiness.view(ws, ref.Key(), now))
	}
	writeJSON(w, readinessPreviewData{
		ServerNow:         now.UTC(),
		FreshForSeconds:   int(prreadiness.FreshFor / time.Second),
		StaleAfterSeconds: int(prreadiness.StaleAfter / time.Second),
		Preview:           prreadiness.BuildPreview(views),
		RepoErrors:        repoErrs,
	})
}

// parseReadinessKeys reads the repeated pr query parameter. Duplicates are
// dropped for the readiness list and rejected for an ordered preview.
func parseReadinessKeys(w http.ResponseWriter, r *http.Request, limit int, rejectDuplicates bool) ([]prref.Ref, bool) {
	raw := r.URL.Query()["pr"]
	if len(raw) == 0 {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "at least one pr query parameter is required", false)
		return nil, false
	}
	if len(raw) > limit {
		writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", fmt.Sprintf("at most %d pr keys per request", limit), false)
		return nil, false
	}
	refs := make([]prref.Ref, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, key := range raw {
		ref, err := prref.Parse(key)
		if err != nil {
			writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", err.Error(), false)
			return nil, false
		}
		if seen[ref.Key()] {
			if rejectDuplicates {
				writePRReviewErrorCode(w, http.StatusBadRequest, "invalid", "duplicate pr key "+ref.Key(), false)
				return nil, false
			}
			continue
		}
		seen[ref.Key()] = true
		refs = append(refs, ref)
	}
	return refs, true
}

func parseBoolQuery(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

func (m *Module) readinessClock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// readinessRepo is one registered repository and the requested PRs in it.
type readinessRepo struct {
	owner, repo, sourceRepo string
	refs                    []prref.Ref
}

func (g readinessRepo) slug() string { return strings.ToLower(g.owner + "/" + g.repo) }

// only narrows g to the given PR numbers.
func (g readinessRepo) only(numbers []int) readinessRepo {
	keep := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		keep[n] = true
	}
	out := g
	out.refs = nil
	for _, ref := range g.refs {
		if keep[ref.Number] {
			out.refs = append(out.refs, ref)
		}
	}
	return out
}

func (g readinessRepo) keys() []string {
	keys := make([]string, 0, len(g.refs))
	for _, ref := range g.refs {
		keys = append(keys, ref.Key())
	}
	return keys
}

// refreshReadiness re-reads the requested PRs that need it, repository by
// repository (bounded concurrency and deadlines), and returns one structured
// error per failed repository. Only a failure to resolve the workspace
// itself is returned as err.
func (m *Module) refreshReadiness(r *http.Request, ws string, refs []prref.Ref, force bool) ([]readinessRepoError, error) {
	groups, unregistered, err := m.groupReadinessRefs(r.Context(), ws, refs)
	if err != nil {
		return nil, err
	}
	now := m.readinessClock()
	repoErrs := make([]readinessRepoError, 0)
	for _, g := range unregistered {
		repoErrs = append(repoErrs, m.failRepo(ws, g, prreadiness.ErrRepoUnregistered, false, 0,
			"repository is not registered in this workspace", now))
	}
	if !m.connectorListAvailable() {
		for _, g := range groups {
			repoErrs = append(repoErrs, m.failRepo(ws, g, prreadiness.ErrConnectorUnavailable, false, 0,
				"GitHub connector is not configured", now))
		}
		return sortRepoErrors(repoErrs), nil
	}

	repoErrs = append(repoErrs, m.readReposConcurrently(r, ws, groups, force, now)...)
	return sortRepoErrors(repoErrs), nil
}

// readReposConcurrently reads the groups that need it with bounded
// concurrency under the whole-request deadline, skipping repositories still
// in rate-limit backoff, and returns their errors.
func (m *Module) readReposConcurrently(r *http.Request, ws string, groups []readinessRepo, force bool, now time.Time) []readinessRepoError {
	var repoErrs []readinessRepoError
	ctx, cancel := context.WithTimeout(r.Context(), readinessRequestTimeout)
	defer cancel()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, readinessRepoConcurrency)
	)
	for _, g := range groups {
		if !force && !m.groupNeedsRead(ws, g, now) {
			continue
		}
		if until := m.readiness.backoffUntil(ws, g.slug()); until.After(now) {
			repoErr := m.failRepo(ws, g, prreadiness.ErrRateLimited, true, until.Sub(now),
				"GitHub rate limit; retrying after "+until.UTC().Format(time.RFC3339), now)
			mu.Lock()
			repoErrs = append(repoErrs, repoErr)
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(g readinessRepo) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				mu.Lock()
				repoErrs = append(repoErrs, m.failRepo(ws, g, prreadiness.ErrTimeout, true, 0,
					"readiness request deadline exceeded", m.readinessClock()))
				mu.Unlock()
				return
			}
			if repoErr := m.readRepoReadiness(ctx, r, ws, g); repoErr != nil {
				mu.Lock()
				repoErrs = append(repoErrs, *repoErr)
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	return repoErrs
}

func (m *Module) groupNeedsRead(ws string, g readinessRepo, now time.Time) bool {
	for _, ref := range g.refs {
		if m.readiness.needsRead(ws, ref.Key(), now) {
			return true
		}
	}
	return false
}

// groupReadinessRefs groups refs by registered repository, canonicalizing
// owner/repo to the registered spelling used for grants and dispatch.
func (m *Module) groupReadinessRefs(ctx context.Context, ws string, refs []prref.Ref) (groups, unregistered []readinessRepo, err error) {
	data, err := storeadapter.BuildWorkspaceDataForKey(ctx, m.store, ws)
	if err != nil {
		return nil, nil, err
	}
	registered := map[string]ops.WorkspaceRepo{}
	if data != nil {
		for _, repo := range data.Repos {
			owner, name, ok := parseGitHubOwnerRepo(repo.RemoteURL)
			if ok {
				registered[strings.ToLower(owner+"/"+name)] = repo
			}
		}
	}
	byRepo := map[string]*readinessRepo{}
	order := []string{}
	for _, ref := range refs {
		slug := ref.Owner + "/" + ref.Repo
		g, ok := byRepo[slug]
		if !ok {
			g = &readinessRepo{owner: ref.Owner, repo: ref.Repo}
			if wr, found := registered[slug]; found {
				g.owner, g.repo, _ = parseGitHubOwnerRepo(wr.RemoteURL)
				g.sourceRepo = wr.Name
			}
			byRepo[slug] = g
			order = append(order, slug)
		}
		g.refs = append(g.refs, ref)
	}
	for _, slug := range order {
		if _, found := registered[slug]; found {
			groups = append(groups, *byRepo[slug])
		} else {
			unregistered = append(unregistered, *byRepo[slug])
		}
	}
	return groups, unregistered, nil
}

// readRepoReadiness reads one repository in chunks and stores the snapshots.
// PRs GitHub is still computing are re-read with backoff inside the
// repository deadline. It returns the repository's error, if any.
func (m *Module) readRepoReadiness(parent context.Context, r *http.Request, ws string, g readinessRepo) *readinessRepoError {
	ctx, cancel := context.WithTimeout(parent, readinessRepoTimeout)
	defer cancel()
	if err := m.ensureConnectorAndGrants(ctx, ws, g.owner, g.repo, prReadActions); err != nil {
		return m.failRepoErr(ctx, ws, g, err)
	}
	numbers := make([]int, 0, len(g.refs))
	for _, ref := range g.refs {
		numbers = append(numbers, ref.Number)
	}
	callSeq := 0
	pending := numbers
	for attempt := 0; ; attempt++ {
		computing, err := m.readReadinessNumbers(ctx, r, ws, g, pending, &callSeq)
		if err != nil {
			// A failed re-read only concerns the PRs it re-read; the
			// others already hold this request's snapshot.
			return m.failRepoErr(ctx, ws, g.only(pending), err)
		}
		if len(computing) == 0 || attempt >= len(m.computingBackoff()) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil // computing PRs already hold their waiting snapshot
		case <-time.After(m.computingBackoff()[attempt]):
		}
		pending = computing
	}
}

func (m *Module) computingBackoff() []time.Duration {
	if m.readinessBackoff != nil {
		return m.readinessBackoff
	}
	return readinessComputingBackoff
}

// readReadinessNumbers dispatches the readiness action for numbers in
// chunks, applies every returned snapshot, records per-PR misses, and
// returns the numbers GitHub is still computing.
func (m *Module) readReadinessNumbers(ctx context.Context, r *http.Request, ws string, g readinessRepo, numbers []int, callSeq *int) ([]int, error) {
	var computing []int
	for start := 0; start < len(numbers); start += providers.MaxReadinessNumbers {
		chunk := numbers[start:min(start+providers.MaxReadinessNumbers, len(numbers))]
		args := make([]any, 0, len(chunk))
		for _, n := range chunk {
			args = append(args, n)
		}
		readStarted := m.readinessClock()
		res, err := m.dispatcher.Dispatch(ctx, connector.Request{
			WorkspaceKey: ws,
			RunID:        readinessRunID(r, g.owner, g.repo),
			BindingID:    bindingID,
			ConnectorID:  connectorID,
			Action:       providers.ActionGitHubPullRequestReadinessRead,
			Resource:     prResource(g.owner, g.repo),
			Args:         map[string]any{"owner": g.owner, "repo": g.repo, "numbers": args},
			CallSeq:      *callSeq,
		})
		*callSeq++
		if err != nil {
			return nil, err
		}
		observedAt := m.readinessClock()
		pulls, missing, err := decodeReadinessBody(res.Body)
		if err != nil {
			return nil, err
		}
		for _, pr := range pulls {
			key := prref.Format(g.owner, g.repo, pr.Number)
			snap := prreadiness.SnapshotFromGitHub(key, pr, observedAt)
			m.readiness.apply(ws, snap, readStarted)
			if snap.Verdict == prreadiness.VerdictWaiting && len(snap.Reasons) > 0 &&
				snap.Reasons[0] == prreadiness.ReasonGitHubComputing {
				computing = append(computing, pr.Number)
			}
		}
		for _, miss := range missing {
			code := prreadiness.ErrUpstream
			if miss.Code == string(prreadiness.ErrNotFound) {
				code = prreadiness.ErrNotFound
			}
			m.readiness.recordError(ws, prref.Format(g.owner, g.repo, miss.Number), code, 0, observedAt)
		}
	}
	return computing, nil
}

type readinessMiss struct {
	Number int    `json:"number"`
	Code   string `json:"code"`
}

// decodeReadinessBody converts the connector's camelCase body into typed
// reads via a JSON round trip (the body is an in-memory map).
func decodeReadinessBody(body map[string]any) ([]prreadiness.GitHubPR, []readinessMiss, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("encode readiness body: %w", err)
	}
	var decoded struct {
		PullRequests []prreadiness.GitHubPR `json:"pullRequests"`
		Missing      []readinessMiss        `json:"missing"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, nil, &providers.UpstreamError{
			Action:  providers.ActionGitHubPullRequestReadinessRead,
			Class:   providers.ClassServerError,
			Summary: "invalid readiness body",
		}
	}
	return decoded.PullRequests, decoded.Missing, nil
}

func readinessRunID(r *http.Request, owner, repo string) string {
	userID := "unknown"
	if identity, ok := middleware.UserIdentityFromContext(r.Context()); ok && strings.TrimSpace(identity.UserID) != "" {
		userID = strings.TrimSpace(identity.UserID)
	}
	return "webui-review:" + userID + ":" + owner + "/" + repo + ":readiness:" + providers.ActionGitHubPullRequestReadinessRead
}

// failRepoErr classifies err (using ctx to recognize the repository
// deadline) and records it for every PR in g.
func (m *Module) failRepoErr(ctx context.Context, ws string, g readinessRepo, err error) *readinessRepoError {
	code, retryable, retryAfter := classifyReadinessError(ctx, err)
	now := m.readinessClock()
	if code == prreadiness.ErrRateLimited {
		if retryAfter <= 0 {
			retryAfter = readinessDefaultRateLimitWait
		}
		m.readiness.setBackoff(ws, g.slug(), now.Add(retryAfter))
	}
	repoErr := m.failRepo(ws, g, code, retryable, retryAfter, sanitizeWarning(err), now)
	return &repoErr
}

func (m *Module) failRepo(ws string, g readinessRepo, code prreadiness.ErrorCode, retryable bool, retryAfter time.Duration, message string, now time.Time) readinessRepoError {
	keys := g.keys()
	for _, key := range keys {
		m.readiness.recordError(ws, key, code, retryAfter, now)
	}
	return readinessRepoError{
		Repo:              g.owner + "/" + g.repo,
		SourceRepo:        g.sourceRepo,
		Code:              code,
		Retryable:         retryable,
		RetryAfterSeconds: ceilSeconds(retryAfter),
		Message:           message,
		PRKeys:            keys,
	}
}

// classifyReadinessError maps a dispatch error to a readiness error code.
func classifyReadinessError(ctx context.Context, err error) (code prreadiness.ErrorCode, retryable bool, retryAfter time.Duration) {
	var (
		rl *providers.RateLimited
		up *providers.UpstreamError
	)
	switch {
	case errors.As(err, &rl):
		return prreadiness.ErrRateLimited, true, rl.RetryAfter
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return prreadiness.ErrTimeout, true, 0
	case errors.Is(err, errEgressUnavailable):
		return prreadiness.ErrConnectorUnavailable, false, 0
	case errors.Is(err, domain.ErrGrantDenied):
		return prreadiness.ErrForbidden, false, 0
	case errors.As(err, &up) && (up.Status == http.StatusUnauthorized || up.Status == http.StatusForbidden):
		return prreadiness.ErrForbidden, false, 0
	case errors.As(err, &up) && up.Status == http.StatusNotFound:
		return prreadiness.ErrNotFound, false, 0
	case errors.Is(err, domain.ErrNotFound):
		return prreadiness.ErrNotFound, false, 0
	default:
		return prreadiness.ErrUpstream, providers.Retryable(err), 0
	}
}

func sortRepoErrors(errs []readinessRepoError) []readinessRepoError {
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Repo < errs[j].Repo })
	return errs
}

// observeListedPullRequests invalidates cached readiness for PRs whose head
// SHA or base branch differs in a fresher list read.
func (m *Module) observeListedPullRequests(ws string, prs []ops.GitPullRequest) {
	if m == nil {
		return
	}
	now := m.readinessClock()
	for _, pr := range prs {
		m.readiness.observeRefs(ws, pr.PRKey, pr.HeadSHA, pr.BaseRefName, now)
	}
}
