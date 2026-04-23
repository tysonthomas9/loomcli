// Package fleet implements backend.IssueBackend as an HTTP REST client against
// the fleet server's workspace-scoped API endpoints. It translates IssueBackend
// method calls into HTTP requests, parses the JSON response envelopes, and
// converts server-side types to backend wire types.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	baseWorkspaceURL string // e.g., "http://host/api/v1/ws1"

	mu        sync.RWMutex
	authToken string
	apiKey    string
	actor     string
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
		baseWorkspaceURL: baseURL + "/api/v1/" + url.PathEscape(cfg.WorkspaceID),
		authToken:        cfg.AuthToken,
		apiKey:           cfg.APIKey,
		actor:            cfg.Actor,
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
	actor := b.actor
	b.mu.RUnlock()

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("X-Fleet-API-Key", key)
	}
	if actor != "" {
		req.Header.Set("X-Actor", actor)
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

	apiResp, err := parseFleetResponse(respBody, resp.StatusCode)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return apiResp, resp.StatusCode, nil
}

// parseFleetResponse turns a fleet-db response body into the apiResponse
// envelope downstream code expects. fleet-db speaks two dialects:
//   - Legacy/wrapper: {"success":true,"data":...}  or  {"success":false,"error":"..."}
//   - Native:        the raw entity (e.g. an Issue) on 2xx, or
//                    {"error":{"code":"...","message":"..."}} on 4xx
//
// Both are valid; we synthesize a uniform apiResponse so callers don't have
// to care which dialect fired.
func parseFleetResponse(body []byte, statusCode int) (*apiResponse, error) {
	// Try envelope first.
	var env apiResponse
	envErr := json.Unmarshal(body, &env)
	hasEnvelopeFields := envErr == nil && (env.Success || env.Error != "" || env.Data != nil)
	if hasEnvelopeFields {
		return &env, nil
	}

	// Native dialect. On 2xx the whole body IS the data; on non-2xx try
	// to extract a structured error message before falling back to the raw
	// body string.
	if statusCode >= 200 && statusCode < 300 {
		// Empty body (e.g. 204 No Content) → success with no data.
		if len(bytes.TrimSpace(body)) == 0 {
			return &apiResponse{Success: true}, nil
		}
		return &apiResponse{Success: true, Data: body}, nil
	}

	// Native error shape: {"error":{"code":"...","message":"..."}}.
	var errEnv struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errEnv) == nil && errEnv.Error.Message != "" {
		return &apiResponse{Success: false, Error: errEnv.Error.Message}, nil
	}

	// Last resort: surface the raw body as the error string. If even THAT
	// failed to parse as JSON, return the parse error so callers can
	// distinguish "non-JSON response" from "unknown error".
	if envErr != nil {
		return nil, fmt.Errorf("fleet server returned non-JSON response (HTTP %d)", statusCode)
	}
	return &apiResponse{Success: false, Error: string(body)}, nil
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

// unmarshalIssueList unmarshals a []*types.IssueWithCounts response and
// converts to []backend.IssueData. Used by List, GetChildren, and SearchIssues.
//
// fleet-db speaks two list dialects:
//   - Bare array: [{...}, {...}]                 (legacy / wrapper-envelope path)
//   - Wrapped:   {"issues": [{...}, {...}]}      (native v1 list responses)
//
// Try the bare array first; on a JSON unmarshal type mismatch, fall back
// to the wrapper. Anything else is a real parse failure.
func unmarshalIssueList(resp *apiResponse, op string) ([]backend.IssueData, error) {
	if !hasData(resp) {
		return []backend.IssueData{}, nil
	}
	var issues []*types.IssueWithCounts
	err := json.Unmarshal(resp.Data, &issues)
	if err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) {
			var wrapper struct {
				Issues []*types.IssueWithCounts `json:"issues"`
			}
			if werr := json.Unmarshal(resp.Data, &wrapper); werr == nil {
				return issuesWithCountsToData(wrapper.Issues), nil
			}
		}
		return nil, backend.ErrInternal(op, "unmarshal response", err)
	}
	return issuesWithCountsToData(issues), nil
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
	return unmarshalIssueList(resp, "List")
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

// Count returns the number of issues matching opts via fleet-db's
// /issues/count endpoint. It wires a subset of backend.CountOpts into the
// endpoint's supported query params (status/type/assignee/label/repo) and
// returns the Total field from the response.
//
// Grouping is rejected: when opts.GroupBy is non-empty, the caller wants a
// breakdown that cannot fit into a single int return value. The grouped
// variant is exposed through Stats(), which calls /issues/count?group_by=status
// directly. Future work (fleet-08yg) may expand Stats to cover more groupings
// without changing Count's signature.
func (b *FleetBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	if opts.GroupBy != "" {
		return 0, backend.ErrNotImplemented("Count",
			"group_by is not supported by Count (use Stats for grouped status counts)")
	}
	path := "/issues/count"
	if q := countOptsToQuery(opts); q != "" {
		path += "?" + q
	}
	resp, err := b.exec(ctx, "Count", "GET", path, nil)
	if err != nil {
		return 0, err
	}
	if !hasData(resp) {
		return 0, backend.ErrInternal("Count", "empty response from server", nil)
	}
	var countResp countIssuesResponse
	if err := json.Unmarshal(resp.Data, &countResp); err != nil {
		return 0, backend.ErrInternal("Count", "unmarshal response", err)
	}
	return int(countResp.Total), nil
}

// GetChildren returns the direct children of the given issue (typically an epic)
// by calling the fleet-db list endpoint with a parent filter.
func (b *FleetBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	if id == "" {
		return nil, backend.ErrValidation("GetChildren", "id must not be empty")
	}
	path := "/issues?parent_id=" + url.QueryEscape(id)
	resp, err := b.exec(ctx, "GetChildren", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "GetChildren")
}

// SearchIssues performs a full-text search via the fleet-db list endpoint with
// the query parameter set. Future fleet-db work can route this to a dedicated
// FT.SEARCH endpoint.
// Note: fleet-db uses "query" (not "q") for its search param — see
// internal/backend/fleet/params.go addListSearchFilters.
func (b *FleetBackend) SearchIssues(ctx context.Context, query string, limit int) ([]backend.IssueData, error) {
	if query == "" {
		return nil, backend.ErrValidation("SearchIssues", "query must not be empty")
	}
	if limit < 0 {
		return nil, backend.ErrValidation("SearchIssues", "limit must not be negative")
	}
	path := "/issues?query=" + url.QueryEscape(query)
	if limit > 0 {
		path += "&limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "SearchIssues", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return unmarshalIssueList(resp, "SearchIssues")
}

// --- Mutation operations ---

func (b *FleetBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	body := createParamsToBody(params)
	resp, err := b.exec(ctx, "Create", "POST", "/issues", body)
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
//
// fleet-db's claim endpoint is per-issue: POST /issues/{id}/claim with an
// empty body. Earlier drafts of this client used a workspace-level
// /fleet/claim endpoint with the issue ID in the body, which fleet-db never
// shipped. Using the per-issue route also lets us share auth/authz with
// other per-issue routes.
func (b *FleetBackend) ClaimIssue(ctx context.Context, id string, _ time.Duration) error {
	if id == "" {
		return backend.ErrValidation("ClaimIssue", "id must not be empty")
	}
	_, err := b.exec(ctx, "ClaimIssue", "POST", "/issues/"+url.PathEscape(id)+"/claim", map[string]interface{}{})
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
	// Request body: fleet-db names the content field "body". Earlier
	// drafts of this client sent "text" (beads' dialect); fleet-db's
	// strict JSON validation (disallowUnknownFields) rejected it with
	// 400 "unknown field text". Response body: fleet-db returns a
	// "body" field + string ID; loom's canonical types.Comment has
	// "text" + int64 ID. Unmarshal into a local struct that mirrors
	// fleet-db's wire shape, then project to types.Comment.
	type commentReq struct {
		Body string `json:"body"`
	}
	resp, err := b.exec(ctx, "AddComment", "POST", "/issues/"+url.PathEscape(params.IssueID)+"/comments", commentReq{Body: params.Text})
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, backend.ErrInternal("AddComment", "empty response from server", nil)
	}
	var wire fleetCommentWire
	if err := json.Unmarshal(resp.Data, &wire); err != nil {
		return nil, backend.ErrInternal("AddComment", "unmarshal response", err)
	}
	comment := wire.toTypesComment()
	result := commentToData(&comment)
	return &result, nil
}

// fleetCommentWire mirrors fleet-db's comment response shape so the
// unmarshal path doesn't collide with types.Comment's beads-dialect
// field tags. Fleet-db can emit ID as either string (per-issue sequence
// like "2") or number (legacy envelope wrappers); json.RawMessage
// tolerates both and the toTypesComment projection normalizes to int64.
// Field rename: body → Text.
type fleetCommentWire struct {
	ID        json.RawMessage `json:"id"`
	IssueID   string          `json:"issue_id"`
	Author    string          `json:"author"`
	Body      string          `json:"body"`
	Text      string          `json:"text,omitempty"` // legacy alias; some envelopes still emit "text"
	CreatedAt time.Time       `json:"created_at"`
}

// toTypesComment projects the fleet-db wire shape back into loom's
// canonical types.Comment so downstream helpers (commentToData, service
// handlers, FE JSON) don't have to know about the dialect gap.
func (w fleetCommentWire) toTypesComment() types.Comment {
	var id int64
	if len(w.ID) > 0 {
		raw := strings.Trim(strings.TrimSpace(string(w.ID)), `"`)
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			id = n
		}
	}
	text := w.Body
	if text == "" {
		text = w.Text
	}
	return types.Comment{
		ID:        id,
		IssueID:   w.IssueID,
		Author:    w.Author,
		Text:      text,
		CreatedAt: w.CreatedAt,
	}
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

// Batch executes a heterogeneous set of BatchOps against fleet-db by grouping
// ops by operation type and fanning out to the specialized or per-issue
// endpoints:
//
//   - "create" ops are aggregated into a single POST /issues/batch call
//     (all-or-nothing per fleet-db's contract).
//   - "close" ops are aggregated into a single POST /issues/batch/close call
//     (best-effort; already-closed issues are silently skipped by fleet-db).
//   - "update" and "delete" ops are dispatched one at a time via the per-issue
//     endpoints. This costs N round-trips per group but lets each op report an
//     independent outcome in its BatchResult slot.
//
// Semantic caveats versus beads:
//   - Fleet-db does NOT provide a polymorphic batch endpoint, so this method
//     is NOT transactionally atomic across operation types: a failure inside
//     the create batch does not roll back earlier close/update/delete calls
//     (or vice versa). Within a single create batch, fleet-db does roll back
//     on validation failure, so callers expecting "all-or-nothing" for pure
//     create batches still get that semantic. This is acceptable for P2
//     parity goals; a polymorphic batch endpoint can be added to fleet-db
//     later (see fleet-08yg follow-ups).
//   - Result ordering preserves the caller's input order regardless of which
//     sub-call actually executed each op.
//
// The method-level error is reserved for transport/marshaling failures that
// prevent issuing any request; per-op failures surface in each BatchResult.
func (b *FleetBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	if len(ops) == 0 {
		return []backend.BatchResult{}, nil
	}
	results := make([]backend.BatchResult, len(ops))

	// First pass: collect indices grouped by op type so we can fan out in bulk.
	var (
		createIdx []int
		closeIdx  []int
	)
	for i, op := range ops {
		switch strings.ToLower(op.Operation) {
		case "create":
			createIdx = append(createIdx, i)
		case "close":
			closeIdx = append(closeIdx, i)
		}
	}

	// Batch-create: aggregate all "create" ops into one call. Fleet-db is
	// all-or-nothing here, so on failure every create slot gets the same error.
	if len(createIdx) > 0 {
		b.runBatchCreates(ctx, ops, createIdx, results)
	}

	// Batch-close: fleet-db returns 204 No Content, so we only need to know
	// whether the call succeeded to populate per-op results.
	if len(closeIdx) > 0 {
		b.runBatchCloses(ctx, ops, closeIdx, results)
	}

	// Fan-out: update/delete/unknown ops run one at a time.
	for i, op := range ops {
		switch strings.ToLower(op.Operation) {
		case "create", "close":
			// handled above
		case "update":
			results[i] = b.runSingleUpdate(ctx, op)
		case "delete":
			results[i] = b.runSingleDelete(ctx, op)
		default:
			results[i] = backend.BatchResult{
				Success: false,
				Error: fmt.Sprintf("unsupported batch operation %q (supported: create, update, close, delete)",
					op.Operation),
			}
		}
	}
	return results, nil
}

// runBatchCreates aggregates CreateParams from the given indices and fires one
// POST /issues/batch. Results are written back into `results` at the original
// indices.
func (b *FleetBackend) runBatchCreates(ctx context.Context, ops []backend.BatchOp, idx []int, results []backend.BatchResult) {
	type batchCreateReq struct {
		Issues []backend.CreateParams `json:"issues"`
	}
	type batchCreateResp struct {
		Issues []types.Issue `json:"issues"`
		Count  int           `json:"count"`
	}
	req := batchCreateReq{Issues: make([]backend.CreateParams, 0, len(idx))}
	for _, i := range idx {
		var p backend.CreateParams
		if err := json.Unmarshal(ops[i].Args, &p); err != nil {
			results[i] = backend.BatchResult{Success: false, Error: "unmarshal create args: " + err.Error()}
			continue
		}
		req.Issues = append(req.Issues, p)
	}
	// If every op had a marshal error we have nothing to send.
	if len(req.Issues) == 0 {
		return
	}
	resp, err := b.exec(ctx, "Batch", "POST", "/issues/batch", req)
	if err != nil {
		errMsg := err.Error()
		for _, i := range idx {
			if results[i].Error != "" {
				continue // marshal error already recorded
			}
			results[i] = backend.BatchResult{Success: false, Error: errMsg}
		}
		return
	}
	// Parse response and map issues back to input slots in order.
	var parsed batchCreateResp
	if hasData(resp) {
		if uerr := json.Unmarshal(resp.Data, &parsed); uerr != nil {
			for _, i := range idx {
				if results[i].Error != "" {
					continue
				}
				results[i] = backend.BatchResult{Success: false, Error: "unmarshal batch response: " + uerr.Error()}
			}
			return
		}
	}
	// The response's issues slice is positionally aligned with req.Issues,
	// which we built by walking idx in order and skipping marshal-failures.
	respIdx := 0
	for _, i := range idx {
		if results[i].Error != "" {
			continue // marshal error; skip
		}
		if respIdx < len(parsed.Issues) {
			issueData := issueToData(&parsed.Issues[respIdx])
			respIdx++
			raw, merr := json.Marshal(issueData)
			if merr != nil {
				results[i] = backend.BatchResult{Success: false, Error: "marshal result: " + merr.Error()}
				continue
			}
			results[i] = backend.BatchResult{Success: true, Data: raw}
		} else {
			results[i] = backend.BatchResult{Success: true}
		}
	}
}

// runBatchCloses aggregates issue IDs from the given indices and fires one
// POST /issues/batch/close. Fleet-db returns 204 with no body on success; our
// apiResponse envelope may therefore be empty. Any non-error response counts
// as success for every slot.
func (b *FleetBackend) runBatchCloses(ctx context.Context, ops []backend.BatchOp, idx []int, results []backend.BatchResult) {
	type closeArg struct {
		ID     string `json:"id"`
		Reason string `json:"reason,omitempty"`
	}
	type batchCloseReq struct {
		IssueIDs []string `json:"issue_ids"`
		Reason   string   `json:"reason,omitempty"`
	}
	req := batchCloseReq{IssueIDs: make([]string, 0, len(idx))}
	for _, i := range idx {
		var arg closeArg
		if err := json.Unmarshal(ops[i].Args, &arg); err != nil {
			results[i] = backend.BatchResult{Success: false, Error: "unmarshal close args: " + err.Error()}
			continue
		}
		if arg.ID == "" {
			results[i] = backend.BatchResult{Success: false, Error: "close op missing id"}
			continue
		}
		req.IssueIDs = append(req.IssueIDs, arg.ID)
		if req.Reason == "" && arg.Reason != "" {
			// Fleet-db applies one reason to the whole batch; record the first
			// non-empty reason we see. Callers needing per-op reasons must use
			// single-close.
			req.Reason = arg.Reason
		}
	}
	if len(req.IssueIDs) == 0 {
		return
	}
	_, err := b.exec(ctx, "Batch", "POST", "/issues/batch/close", req)
	for _, i := range idx {
		if results[i].Error != "" {
			continue // already recorded local error
		}
		if err != nil {
			results[i] = backend.BatchResult{Success: false, Error: err.Error()}
		} else {
			results[i] = backend.BatchResult{Success: true}
		}
	}
}

// runSingleUpdate executes a single update op via Update() and wraps the
// outcome in a BatchResult.
func (b *FleetBackend) runSingleUpdate(ctx context.Context, op backend.BatchOp) backend.BatchResult {
	// Support two shapes for args:
	//   1. Nested: {"id": "...", "params": {<UpdateParams fields>}}
	//   2. Flat:   {"id": "...", <UpdateParams fields at top level>}
	// The beads backend accepts both (callers in the codebase use the flat
	// form for historical reasons); we unmarshal into the nested struct first
	// and fall back to flat if "params" is absent.
	var nested struct {
		ID     string                `json:"id"`
		Params *backend.UpdateParams `json:"params"`
	}
	if err := json.Unmarshal(op.Args, &nested); err != nil {
		return backend.BatchResult{Success: false, Error: "unmarshal update args: " + err.Error()}
	}
	if nested.ID == "" {
		return backend.BatchResult{Success: false, Error: "update op missing id"}
	}
	var params backend.UpdateParams
	if nested.Params != nil {
		params = *nested.Params
	} else {
		// Flat shape: parse top-level into UpdateParams. The "id" field in the
		// JSON is simply ignored because UpdateParams has no ID field.
		if err := json.Unmarshal(op.Args, &params); err != nil {
			return backend.BatchResult{Success: false, Error: "unmarshal update args (flat): " + err.Error()}
		}
	}
	if err := b.Update(ctx, nested.ID, params); err != nil {
		return backend.BatchResult{Success: false, Error: err.Error()}
	}
	return backend.BatchResult{Success: true}
}

// runSingleDelete executes a single delete op via Delete() and wraps the
// outcome in a BatchResult.
func (b *FleetBackend) runSingleDelete(ctx context.Context, op backend.BatchOp) backend.BatchResult {
	var arg struct {
		ID      string   `json:"id"`
		IDs     []string `json:"ids"`
		Force   bool     `json:"force"`
		Cascade bool     `json:"cascade"`
		Reason  string   `json:"reason"`
	}
	if err := json.Unmarshal(op.Args, &arg); err != nil {
		return backend.BatchResult{Success: false, Error: "unmarshal delete args: " + err.Error()}
	}
	ids := arg.IDs
	if len(ids) == 0 && arg.ID != "" {
		ids = []string{arg.ID}
	}
	if len(ids) == 0 {
		return backend.BatchResult{Success: false, Error: "delete op missing id/ids"}
	}
	if err := b.Delete(ctx, backend.DeleteParams{
		IDs: ids, Force: arg.Force, Cascade: arg.Cascade, Reason: arg.Reason,
	}); err != nil {
		return backend.BatchResult{Success: false, Error: err.Error()}
	}
	return backend.BatchResult{Success: true}
}

// --- Mutation polling ---

// GetMutations returns mutation events from fleet-db's cursor-based events
// stream. The caller passes sinceMs as a millisecond epoch, but fleet-db's
// cursor is a Redis Stream ID of the form "<ms>-<seq>"; we pass the integer
// as-is because fleet's handler accepts any numeric prefix and treats it as a
// millisecond timestamp when a dash is absent. Callers that hold a real
// cursor from a previous response should thread it back via the sinceMs
// parameter (the backend interface uses int64, so until a generic cursor API
// lands — cf. fleet-0qcs — we cannot round-trip the "<ms>-<seq>" form and will
// re-receive events emitted within the same millisecond).
func (b *FleetBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	path := "/events/mutations?since=" + strconv.FormatInt(sinceMs, 10)
	resp, err := b.exec(ctx, "GetMutations", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.MutationData{}, nil
	}
	var fresp fleetMutationsResponse
	if err := json.Unmarshal(resp.Data, &fresp); err != nil {
		return nil, backend.ErrInternal("GetMutations", "unmarshal response", err)
	}
	return fleetEventsToMutationData(fresp.Events), nil
}

// WaitForMutations long-polls fleet-db's events endpoint. Fleet-db's contract
// is that `timeout` must be 0 (non-blocking) or 1000-120000 (ms); we forward
// timeoutMs unchanged and let the server return a 400 for out-of-range values
// so callers see a classified validation error rather than silent truncation.
// On timeout, fleet-db returns an empty events array (not an error).
func (b *FleetBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	path := fmt.Sprintf("/events/mutations?since=%d&timeout=%d", sinceMs, timeoutMs)
	resp, err := b.exec(ctx, "WaitForMutations", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.MutationData{}, nil
	}
	var fresp fleetMutationsResponse
	if err := json.Unmarshal(resp.Data, &fresp); err != nil {
		return nil, backend.ErrInternal("WaitForMutations", "unmarshal response", err)
	}
	return fleetEventsToMutationData(fresp.Events), nil
}
