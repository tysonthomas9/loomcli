// Package fleet implements backend.IssueBackend as an HTTP REST client against
// the fleet server's workspace-scoped API endpoints. It translates IssueBackend
// method calls into HTTP requests, parses the JSON response envelopes, and
// converts server-side types to backend wire types.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

// maxResponseBody limits response body reads to 50MB to prevent OOM.
const maxResponseBody = 50 << 20

// FleetBackend implements backend.IssueBackend by forwarding calls to a fleet
// server's REST API. It is safe for concurrent use.
type FleetBackend struct {
	client           *http.Client
	baseWorkspaceURL string // e.g., "http://host/api/workspaces/ws1"

	mu        sync.RWMutex
	authToken string
	apiKey    string
}

// Compile-time interface check.
var _ backend.IssueBackend = (*FleetBackend)(nil)

// apiResponse is the generic JSON envelope returned by fleet server endpoints.
type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// New creates a FleetBackend with the given configuration.
func New(cfg Config) (*FleetBackend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("fleet.New: BaseURL is required")
	}
	if cfg.WorkspaceID == "" {
		return nil, fmt.Errorf("fleet.New: WorkspaceID is required")
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &FleetBackend{
		client:           httpClient,
		baseWorkspaceURL: baseURL + "/api/workspaces/" + url.PathEscape(cfg.WorkspaceID),
		authToken:        cfg.AuthToken,
		apiKey:           cfg.APIKey,
	}, nil
}

// SetAuthToken updates the bearer token for subsequent requests.
// Safe to call concurrently with IssueBackend method calls.
func (b *FleetBackend) SetAuthToken(token string) {
	b.mu.Lock()
	b.authToken = token
	b.mu.Unlock()
}

// SetAPIKey updates the API key for subsequent requests.
// Safe to call concurrently with IssueBackend method calls.
func (b *FleetBackend) SetAPIKey(key string) {
	b.mu.Lock()
	b.apiKey = key
	b.mu.Unlock()
}

func (b *FleetBackend) BackendName() string { return "fleet" }

// doRequest executes an HTTP request and parses the JSON response envelope.
func (b *FleetBackend) doRequest(ctx context.Context, method, path string, body interface{}) (*apiResponse, int, error) {
	fullURL := b.baseWorkspaceURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Set auth headers.
	b.mu.RLock()
	token := b.authToken
	key := b.apiKey
	b.mu.RUnlock()

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("X-Fleet-API-Key", key)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("fleet server returned non-JSON response (HTTP %d)", resp.StatusCode)
	}

	return &apiResp, resp.StatusCode, nil
}

// exec is a convenience wrapper around doRequest that classifies errors.
func (b *FleetBackend) exec(ctx context.Context, op, method, path string, body interface{}) (*apiResponse, error) {
	apiResp, statusCode, err := b.doRequest(ctx, method, path, body)
	if err != nil {
		return nil, classifyTransportError(op, err)
	}
	if cerr := classifyHTTPError(op, statusCode, *apiResp); cerr != nil {
		return nil, cerr
	}
	return apiResp, nil
}

// hasData returns true if the response Data field is present and non-null.
func hasData(resp *apiResponse) bool {
	return resp != nil && resp.Data != nil && string(resp.Data) != "null"
}

// --- Query operations ---

func (b *FleetBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	resp, err := b.exec(ctx, "Get", "GET", "/issues/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrNotFound("Get", "issue not found")
	}
	var details types.IssueDetails
	if err := json.Unmarshal(resp.Data, &details); err != nil {
		return nil, backend.ErrInternal("Get", "unmarshal response", err)
	}
	result := detailsToDetailData(&details)
	return &result, nil
}

func (b *FleetBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	if err := checkFleetUnsupportedFilters(opts); err != nil {
		return nil, err
	}
	path := "/issues?" + listOptsToQuery(opts)
	resp, err := b.exec(ctx, "List", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []*types.IssueWithCounts
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("List", "unmarshal response", err)
	}
	return issuesWithCountsToData(issues), nil
}

func (b *FleetBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	path := "/ready?" + readyOptsToQuery(opts)
	resp, err := b.exec(ctx, "Ready", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	// The ready endpoint returns ReadyIssueWithParent which embeds *types.Issue.
	var issues []*readyIssueWithParent
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("Ready", "unmarshal response", err)
	}
	return readyIssuesToData(issues), nil
}

func (b *FleetBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	path := "/blocked?" + blockedOptsToQuery(opts)
	resp, err := b.exec(ctx, "Blocked", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []*types.BlockedIssue
	if err := json.Unmarshal(resp.Data, &issues); err != nil {
		return nil, backend.ErrInternal("Blocked", "unmarshal response", err)
	}
	return blockedIssuesToData(issues), nil
}

// Stats builds StatsData from the fleet server's count endpoint with
// group_by=status. ReadyIssues, EpicsEligibleForClosure, and AverageLeadTime
// are unavailable until fleet-db adds server-side stats aggregation (fleet-08yg).
func (b *FleetBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	resp, err := b.exec(ctx, "Stats", "GET", "/issues/count?group_by=status", nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("Stats", "nil response from CountIssues", nil)
	}
	var countResp countIssuesResponse
	if err := json.Unmarshal(resp.Data, &countResp); err != nil {
		return nil, backend.ErrInternal("Stats", "unmarshal response", err)
	}
	groups := countResp.Groups
	return &backend.StatsData{
		TotalIssues:      int(countResp.Total),
		OpenIssues:       int(groups[string(types.StatusOpen)]),
		InProgressIssues: int(groups[string(types.StatusInProgress)]),
		ClosedIssues:     int(groups[string(types.StatusClosed)]),
		BlockedIssues:    int(groups[string(types.StatusBlocked)]),
		DeferredIssues:   int(groups[string(types.StatusDeferred)]),
		TombstoneIssues:  int(groups[string(types.StatusTombstone)]),
		PinnedIssues:     int(groups[string(types.StatusPinned)]),
		// ReadyIssues, EpicsEligibleForClosure, AverageLeadTime: 0 (fleet-08yg).
		// StatusReview and StatusHooked counts are included in TotalIssues but have
		// no dedicated StatsData field; they are silently omitted from per-status counts.
	}, nil
}

func (b *FleetBackend) Count(_ context.Context, _ backend.CountOpts) (int, error) {
	return 0, backend.ErrNotImplemented("Count", "fleet server has no count endpoint")
}

// GetChildren returns the direct children of the given issue (typically an epic)
// by calling the fleet-db list endpoint with a parent filter.
func (b *FleetBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	if id == "" {
		return nil, backend.ErrValidation("GetChildren", "id must not be empty")
	}
	path := "/issues?parent_id=" + url.QueryEscape(id)
	resp, callErr := b.exec(ctx, "GetChildren", "GET", path, nil)
	if callErr != nil {
		return nil, callErr
	}
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []*types.IssueWithCounts
	if jsonErr := json.Unmarshal(resp.Data, &issues); jsonErr != nil {
		return nil, backend.ErrInternal("GetChildren", "unmarshal response", jsonErr)
	}
	return issuesWithCountsToData(issues), nil
}

// --- Mutation operations ---

func (b *FleetBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	resp, err := b.exec(ctx, "Create", "POST", "/issues", params)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("Create", "empty response from server", nil)
	}
	var issue types.Issue
	if err := json.Unmarshal(resp.Data, &issue); err != nil {
		return nil, backend.ErrInternal("Create", "unmarshal response", err)
	}
	result := issueToData(&issue)
	return &result, nil
}

func (b *FleetBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	if params.Claim {
		return backend.ErrValidation("Update", "Claim field is not supported in FleetBackend.Update; use ClaimIssue instead")
	}
	// Map UpdateParams to the PatchIssueRequest format the server expects.
	req := updateParamsToPatchRequest(params)
	_, err := b.exec(ctx, "Update", "PATCH", "/issues/"+url.PathEscape(id), req)
	return err
}

// ClaimIssue atomically claims an issue via the fleet claim endpoint.
// The lockTTL parameter is accepted but ignored — fleet server manages
// claim TTL via server-side configuration.
func (b *FleetBackend) ClaimIssue(ctx context.Context, id string, _ time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	type claimReq struct {
		IssueID string `json:"issue_id"`
	}
	// The fleet claim endpoint uses "payload" instead of "data", but we only
	// care about success/error for ClaimIssue (which returns only error).
	_, err := b.exec(ctx, "ClaimIssue", "POST", "/fleet/claim", claimReq{IssueID: id})
	return err
}

// DeferIssue defers an issue via PATCH with status="deferred" and optional
// defer_until. A zero until means status-only defer with no end date. The
// fleet server has no dedicated defer route; PATCH /issues/{id} is used.
func (b *FleetBackend) DeferIssue(ctx context.Context, id string, until time.Time) error {
	if id == "" {
		return backend.ErrValidation("DeferIssue", "id must not be empty")
	}
	req := map[string]interface{}{
		"status": "deferred",
	}
	if !until.IsZero() {
		req["defer_until"] = until.Format(time.RFC3339)
	}
	_, callErr := b.exec(ctx, "DeferIssue", "PATCH", "/issues/"+url.PathEscape(id), req)
	return callErr
}

// UndeferIssue restores a deferred issue to "open" status and clears the
// defer_until field by sending an empty string (matching bd undefer behavior).
func (b *FleetBackend) UndeferIssue(ctx context.Context, id string) error {
	if id == "" {
		return backend.ErrValidation("UndeferIssue", "id must not be empty")
	}
	req := map[string]interface{}{
		"status":      "open",
		"defer_until": "",
	}
	_, callErr := b.exec(ctx, "UndeferIssue", "PATCH", "/issues/"+url.PathEscape(id), req)
	return callErr
}

func (b *FleetBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	type closeReq struct {
		Reason      string `json:"reason,omitempty"`
		Session     string `json:"session,omitempty"`
		SuggestNext bool   `json:"suggest_next,omitempty"`
		Force       bool   `json:"force,omitempty"`
	}
	req := closeReq{
		Reason:      params.Reason,
		Session:     params.Session,
		SuggestNext: params.SuggestNext,
		Force:       params.Force,
	}
	resp, err := b.exec(ctx, "Close", "POST", "/issues/"+url.PathEscape(id)+"/close", req)
	if err != nil {
		return nil, err
	}
	// The close endpoint returns a close result with closed issue and unblocked issues.
	// TODO(fleet-q6ox): fleet-db does not yet return unblocked issues on close;
	// Unblocked will be empty until fleet-db adds unblocked-on-close support.
	if !hasData(resp) {
		return nil, backend.ErrInternal("Close", "empty response from server", nil)
	}
	var cr closeResultJSON
	if err := json.Unmarshal(resp.Data, &cr); err != nil {
		return nil, backend.ErrInternal("Close", "unmarshal response", err)
	}
	return closeResultJSONToData(&cr), nil
}

func (b *FleetBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	if id == "" {
		return backend.ErrValidation("Reopen", "id must not be empty")
	}
	// Reopen is done via PATCH with status="open".
	req := map[string]interface{}{
		"status": "open",
	}
	_, err := b.exec(ctx, "Reopen", "PATCH", "/issues/"+url.PathEscape(id), req)
	if err != nil {
		return err
	}
	// Record reason as a comment per the IssueBackend interface contract.
	// Best-effort: the status transition already succeeded.
	if params.Reason != "" {
		type commentReq struct {
			Text string `json:"text"`
		}
		_, _ = b.exec(ctx, "Reopen", "POST", "/issues/"+url.PathEscape(id)+"/comments", commentReq{Text: params.Reason})
	}
	return nil
}

func (b *FleetBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	if len(params.IDs) == 0 {
		return backend.ErrValidation("Delete", "IDs must not be empty")
	}
	// The server's DELETE endpoint handles single issue. Delete each one.
	for _, id := range params.IDs {
		_, err := b.exec(ctx, "Delete", "DELETE", "/issues/"+url.PathEscape(id), nil)
		if err != nil {
			if params.Force && backend.IsKind(err, backend.KindNotFound) {
				continue
			}
			return err
		}
	}
	return nil
}

// --- Dependency operations ---

func (b *FleetBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	type depReq struct {
		DependsOnID string `json:"depends_on_id"`
		DepType     string `json:"dep_type,omitempty"`
	}
	req := depReq{
		DependsOnID: params.ToID,
		DepType:     params.DepType,
	}
	_, err := b.exec(ctx, "AddDependency", "POST", "/issues/"+url.PathEscape(params.FromID)+"/dependencies", req)
	return err
}

func (b *FleetBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	_, err := b.exec(ctx, "RemoveDependency", "DELETE", "/issues/"+url.PathEscape(params.FromID)+"/dependencies/"+url.PathEscape(params.ToID), nil)
	return err
}

// --- Label operations ---

func (b *FleetBackend) AddLabel(ctx context.Context, id string, label string) error {
	req := map[string]interface{}{
		"add_labels": []string{label},
	}
	_, err := b.exec(ctx, "AddLabel", "PATCH", "/issues/"+url.PathEscape(id), req)
	return err
}

func (b *FleetBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	req := map[string]interface{}{
		"remove_labels": []string{label},
	}
	_, err := b.exec(ctx, "RemoveLabel", "PATCH", "/issues/"+url.PathEscape(id), req)
	return err
}

// --- Comment operations ---

func (b *FleetBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	// ListComments extracts comments from the Get response, which includes
	// IssueDetails.Comments.
	detail, err := b.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if detail.Comments == nil {
		return []backend.CommentData{}, nil
	}
	return detail.Comments, nil
}

func (b *FleetBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	type commentReq struct {
		Text string `json:"text"`
	}
	resp, err := b.exec(ctx, "AddComment", "POST", "/issues/"+url.PathEscape(params.IssueID)+"/comments", commentReq{Text: params.Text})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("AddComment", "empty response from server", nil)
	}
	var comment types.Comment
	if err := json.Unmarshal(resp.Data, &comment); err != nil {
		return nil, backend.ErrInternal("AddComment", "unmarshal response", err)
	}
	result := commentToData(&comment)
	return &result, nil
}

// --- Event operations ---

func (b *FleetBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	path := "/issues/" + url.PathEscape(id) + "/events"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "ListEvents", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.EventData{}, nil
	}
	var events []*types.Event
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, backend.ErrInternal("ListEvents", "unmarshal response", err)
	}
	result := make([]backend.EventData, 0, len(events))
	for _, e := range events {
		if e != nil {
			result = append(result, eventToData(e))
		}
	}
	return result, nil
}

// --- Batch operations ---

func (b *FleetBackend) Batch(_ context.Context, _ []backend.BatchOp) ([]backend.BatchResult, error) {
	return nil, backend.ErrNotImplemented("Batch", "fleet server has no batch endpoint")
}

// --- Mutation polling ---

func (b *FleetBackend) GetMutations(_ context.Context, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("GetMutations", "fleet mode uses SSE for real-time updates")
}

func (b *FleetBackend) WaitForMutations(_ context.Context, _ int64, _ int64) ([]backend.MutationData, error) {
	return nil, backend.ErrNotImplemented("WaitForMutations", "fleet mode uses SSE for real-time updates")
}
