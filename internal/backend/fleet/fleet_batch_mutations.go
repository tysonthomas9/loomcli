package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/types"
)

type fleetCommentWire struct {
	ID        json.RawMessage `json:"id"`
	IssueID   string          `json:"issue_id"`
	Author    string          `json:"author"`
	Body      string          `json:"body"`
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
	return types.Comment{
		ID:        id,
		IssueID:   w.IssueID,
		Author:    w.Author,
		Text:      w.Body,
		CreatedAt: w.CreatedAt,
	}
}

// --- Event operations ---

type fleetEventWire struct {
	ID        string                `json:"id"`
	Timestamp time.Time             `json:"timestamp"`
	Actor     string                `json:"actor"`
	Action    string                `json:"action"`
	Category  string                `json:"category"`
	Summary   string                `json:"summary"`
	Changes   []backend.FieldChange `json:"changes"`
	Metadata  map[string]string     `json:"metadata"`
}

type fleetEventHistory struct {
	History     []fleetEventWire `json:"history"`
	Cursor      string           `json:"cursor"`
	HasMore     bool             `json:"has_more"`
	TotalEvents int              `json:"total_events"`
}

func (b *FleetBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	history, err := b.ListEventHistory(ctx, id, backend.EventHistoryParams{Limit: limit})
	if err != nil {
		return nil, err
	}
	if history == nil {
		return []backend.EventData{}, nil
	}
	return history.Events, nil
}

// ListEventHistory returns the newest event tail when Since is nil and one
// oldest-first fleet-db page when Since is non-nil. Tail responses carry no
// cursor; HasMore reports whether their oldest events were trimmed.
func (b *FleetBackend) ListEventHistory(
	ctx context.Context,
	id string,
	params backend.EventHistoryParams,
) (*backend.EventHistoryData, error) {
	const (
		historyPageLimit    = 200
		defaultHistoryLimit = 50
	)
	limit := params.Limit
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if params.Since != nil {
		if limit > historyPageLimit {
			limit = historyPageLimit
		}
		return b.eventHistoryForwardPage(ctx, id, *params.Since, limit)
	}
	return b.eventHistoryTail(ctx, id, limit, historyPageLimit)
}

// eventHistoryForwardPage serves exactly one oldest-first fleet-db page,
// passing the paging metadata through verbatim.
func (b *FleetBackend) eventHistoryForwardPage(ctx context.Context, id, since string, limit int) (*backend.EventHistoryData, error) {
	history, err := b.listEventHistoryPage(ctx, id, since, limit)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return &backend.EventHistoryData{Events: []backend.EventData{}}, nil
	}
	return &backend.EventHistoryData{
		Events:      eventDataFromHistory(history.History, id),
		Cursor:      history.Cursor,
		HasMore:     history.HasMore,
		TotalEvents: history.TotalEvents,
	}, nil
}

// eventHistoryTail aggregates the most recent limit events. fleet-db's history
// endpoint pages forward from the start of the issue stream, so follow its
// cursor to the end and retain only the requested tail. A tail response
// carries no cursor; has_more reports whether older events were trimmed.
func (b *FleetBackend) eventHistoryTail(ctx context.Context, id string, limit, pageLimit int) (*backend.EventHistoryData, error) {
	result := make([]backend.EventData, 0, limit)
	totalEvents := 0
	cursor := ""
	for {
		history, err := b.listEventHistoryPage(ctx, id, cursor, pageLimit)
		if err != nil {
			return nil, err
		}
		if history == nil {
			return &backend.EventHistoryData{Events: []backend.EventData{}}, nil
		}
		result = append(result, eventDataFromHistory(history.History, id)...)
		totalEvents = history.TotalEvents
		if !history.HasMore {
			break
		}
		if history.Cursor == "" || history.Cursor == cursor {
			return nil, backend.ErrInternal(
				"ListEvents",
				"history response has_more without a new cursor",
				nil,
			)
		}
		cursor = history.Cursor
	}

	if len(result) > limit {
		result = result[len(result)-limit:]
	}
	return &backend.EventHistoryData{
		Events:      result,
		HasMore:     totalEvents > len(result),
		TotalEvents: totalEvents,
	}, nil
}

func (b *FleetBackend) listEventHistoryPage(
	ctx context.Context,
	id string,
	cursor string,
	limit int,
) (*fleetEventHistory, error) {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if cursor != "" {
		query.Set("since", cursor)
	}
	path := "/issues/" + url.PathEscape(id) + "/history?" + query.Encode()
	resp, err := b.exec(ctx, "ListEvents", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return nil, nil
	}

	var history fleetEventHistory
	if err := json.Unmarshal(resp.Data, &history); err != nil {
		return nil, backend.ErrInternal("ListEvents", "unmarshal response", err)
	}
	return &history, nil
}

func eventDataFromHistory(history []fleetEventWire, issueID string) []backend.EventData {
	result := make([]backend.EventData, 0, len(history))
	for _, event := range history {
		result = append(result, backend.EventData{
			ID:        event.ID,
			IssueID:   issueID,
			Kind:      event.Action,
			Actor:     event.Actor,
			Category:  event.Category,
			Summary:   event.Summary,
			Changes:   event.Changes,
			Metadata:  event.Metadata,
			CreatedAt: event.Timestamp,
		})
	}
	return result
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
// Semantic caveats:
//   - Fleet-db does NOT provide a polymorphic batch endpoint, so this method
//     is NOT transactionally atomic across operation types: a failure inside
//     the create batch does not roll back earlier close/update/delete calls
//     (or vice versa). Within a single create batch, fleet-db does roll back
//     on validation failure, so callers expecting "all-or-nothing" for pure
//     create batches still get that semantic. This is acceptable for P2
//     parity goals; a polymorphic batch endpoint can be added to fleet-db later.
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
	req := fleetBatchCreateReq{Issues: make([]fleetBatchCreateIssueReq, 0, len(idx))}
	for _, i := range idx {
		issueReq, err := batchCreateIssueReq(ops[i])
		if err != nil {
			results[i] = backend.BatchResult{Success: false, Error: "unmarshal create args: " + err.Error()}
			continue
		}
		req.Issues = append(req.Issues, issueReq)
	}
	// If every op had a marshal error we have nothing to send.
	if len(req.Issues) == 0 {
		return
	}
	resp, err := b.exec(ctx, "Batch", "POST", "/issues/batch", req)
	if err != nil {
		failPendingBatchCreates(idx, results, err.Error())
		return
	}
	// Parse response and map issues back to input slots in order.
	var parsed fleetBatchCreateResp
	if hasData(resp) {
		if uerr := json.Unmarshal(resp.Data, &parsed); uerr != nil {
			failPendingBatchCreates(idx, results, "unmarshal batch response: "+uerr.Error())
			return
		}
	}
	assignBatchCreateResults(idx, results, parsed.Issues)
}

type fleetBatchCreateIssueReq struct {
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Priority    int      `json:"priority"`
	Type        string   `json:"type"`
	Assignee    string   `json:"assignee,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	Repo        string   `json:"repo,omitempty"`
	Design      string   `json:"design,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	DueAt       string   `json:"due_at,omitempty"`
	DeferUntil  string   `json:"defer_until,omitempty"`
}

type fleetBatchCreateReq struct {
	Issues []fleetBatchCreateIssueReq `json:"issues"`
}

type fleetBatchCreateResp struct {
	Issues []fleetIssueWire `json:"issues"`
	Count  int              `json:"count"`
}

func batchCreateIssueReq(op backend.BatchOp) (fleetBatchCreateIssueReq, error) {
	var p backend.CreateParams
	if err := json.Unmarshal(op.Args, &p); err != nil {
		return fleetBatchCreateIssueReq{}, err
	}
	return fleetBatchCreateIssueReq{
		Title:       p.Title,
		Description: p.Description,
		Priority:    p.Priority,
		Type:        p.IssueType,
		Assignee:    p.Assignee,
		Owner:       p.Owner,
		Labels:      p.Labels,
		ParentID:    p.Parent,
		Repo:        p.SourceRepo,
		Design:      p.Design,
		Notes:       p.Notes,
		DueAt:       p.DueAt,
		DeferUntil:  p.DeferUntil,
	}, nil
}

func failPendingBatchCreates(idx []int, results []backend.BatchResult, errMsg string) {
	for _, i := range idx {
		if results[i].Error != "" {
			continue // unmarshal error already recorded
		}
		results[i] = backend.BatchResult{Success: false, Error: errMsg}
	}
}

func assignBatchCreateResults(idx []int, results []backend.BatchResult, issues []fleetIssueWire) {
	// The response's issues slice is positionally aligned with req.Issues,
	// which we built by walking idx in order and skipping marshal-failures.
	respIdx := 0
	for _, i := range idx {
		if results[i].Error != "" {
			continue // marshal error; skip
		}
		if respIdx < len(issues) {
			issue := issues[respIdx].toIssue()
			issueData := issueToData(&issue)
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
	// Support both fleet batch update shapes:
	//   1. Nested: {"id": "...", "params": {<UpdateParams fields>}}
	//   2. Flat:   {"id": "...", <UpdateParams fields at top level>}
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

// fleetCursorZero is the literal fleet-db `since` value that requests the
// full event history. fleet-db's `since` validator accepts "0" or a Redis
// Stream ID of the form "<ms>-<seq>"; zero is the only special-case form.
const fleetCursorZero = "0"
const fleetOpaqueCursorPrefix = "c1."

// formatFleetCursor renders an int64 millisecond epoch into the Redis-stream
// ID shape that fleet-db's `since` validator accepts. Zero stays "0"; any
// positive value gets a "-0" suffix to satisfy "<ms>-<seq>" parsing without
// claiming a specific sequence position. Callers re-receive events from the
// same millisecond as the cursor — see GetMutations doc for the dedupe trade.
func formatFleetCursor(sinceMs int64) string {
	if sinceMs <= 0 {
		return fleetCursorZero
	}
	return strconv.FormatInt(sinceMs, 10) + "-0"
}

func normalizeFleetCursor(cursor string) string {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" || cursor == fleetCursorZero {
		return fleetCursorZero
	}
	if strings.HasPrefix(cursor, fleetOpaqueCursorPrefix) {
		if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, fleetOpaqueCursorPrefix)); err == nil {
			cursor = string(decoded)
		}
	}
	if isFleetStreamID(cursor) {
		return cursor
	}
	if _, err := strconv.ParseInt(cursor, 10, 64); err == nil {
		return cursor + "-0"
	}
	return fleetCursorZero
}

func normalizeFleetCursorForV2(cursor string) string {
	cursor = normalizeFleetCursor(cursor)
	if cursor == fleetCursorZero {
		return fleetCursorZero
	}
	return fleetOpaqueCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(cursor))
}

func isFleetStreamID(cursor string) bool {
	parts := strings.Split(cursor, "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return false
	}
	return true
}

// --- Mutation polling ---

func (b *FleetBackend) getMutationsAfter(ctx context.Context, op string, since string, timeoutMs int64) ([]backend.MutationData, error) {
	params := url.Values{}
	params.Set("since", normalizeFleetCursorForV2(since))
	if timeoutMs > 0 {
		params.Set("timeout", strconv.FormatInt(timeoutMs, 10))
	}
	rawURL := b.baseWorkspaceV2 + "/events/mutations?" + params.Encode()
	resp, err := b.execURL(ctx, op, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.MutationData{}, nil
	}
	var fresp fleetMutationsResponse
	if err := json.Unmarshal(resp.Data, &fresp); err != nil {
		return nil, backend.ErrInternal(op, "unmarshal response", err)
	}
	return fleetEventsToMutationData(fresp.Events), nil
}

// GetMutationsAfter returns mutation events after an opaque fleet-db cursor.
func (b *FleetBackend) GetMutationsAfter(ctx context.Context, since string) ([]backend.MutationData, error) {
	return b.getMutationsAfter(ctx, "GetMutationsAfter", since, 0)
}

// WaitForMutationsAfter long-polls mutation events after an opaque fleet-db cursor.
func (b *FleetBackend) WaitForMutationsAfter(ctx context.Context, since string, timeoutMs int64) ([]backend.MutationData, error) {
	return b.getMutationsAfter(ctx, "WaitForMutationsAfter", since, timeoutMs)
}

// GetMutations returns mutation events from fleet-db's cursor-based events
// stream. fleet-db's `since` parameter validates strictly: it must be the
// literal string "0" OR a Redis Stream ID of the form "<ms>-<seq>".
// Caller passes a millisecond epoch (int64) per the IssueBackend interface;
// for any value > 0 we synthesize the lowest-sequence stream ID
// "<ms>-0" so the validator passes. Trade-off: events that landed in the
// same millisecond as the cursor get re-delivered (caller must dedupe).
// Once a generic cursor API lands (fleet-0qcs) we can round-trip the full
// "<ms>-<seq>" form.
func (b *FleetBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	path := "/events/mutations?since=" + formatFleetCursor(sinceMs)
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
	path := fmt.Sprintf("/events/mutations?since=%s&timeout=%d", formatFleetCursor(sinceMs), timeoutMs)
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
