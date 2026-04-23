package subscription

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// knownIssueState is a lightweight snapshot of an issue used by pollDBChanges
// to determine what kind of granular mutation to emit.
type knownIssueState struct {
	Title    string
	Assignee string
	Status   string
	Priority int
}

// changedIssue is a lightweight struct for issues returned by fetchChangedIssues.
type changedIssue struct {
	ID         string
	Title      string
	Assignee   string
	Status     string
	Priority   int
	SourceRepo string
}

// granularMutationThreshold is the max number of changed issues to emit
// individually. Beyond this, fall back to MutationRefresh.
const granularMutationThreshold = 100

// emitGranularMutations broadcasts individual mutation events for each changed issue,
// comparing against knownIssues to determine the mutation type (create/status/update).
// rawIssues is keyed by issue ID and carries the raw JSON payload to embed in each
// mutation event so frontend consumers can wholesale-replace their stored issue.
func (s *DaemonSubscriber) emitGranularMutations(changed []changedIssue, rawIssues map[string]json.RawMessage, now time.Time, totalCount int64) {
	known := s.snapshotKnownIssues()

	mutationCount := 0
	for _, issue := range changed {
		if issue.ID == "" {
			continue
		}
		payload := s.buildMutationPayload(issue, rawIssues[issue.ID], now, known)
		s.hub.Broadcast(payload)
		mutationCount++
	}

	s.updateKnownIssues(changed, now, totalCount)

	if mutationCount > 0 {
		slog.Info("external DB change: broadcast granular mutations", "count", mutationCount, "clients", s.hub.ClientCount())
	}
}

// snapshotKnownIssues returns a copy of the knownIssues map to avoid holding the
// lock during broadcast.
func (s *DaemonSubscriber) snapshotKnownIssues() map[string]knownIssueState {
	s.mu.Lock()
	known := make(map[string]knownIssueState, len(s.knownIssues))
	for k, v := range s.knownIssues {
		known[k] = v
	}
	s.mu.Unlock()
	return known
}

// buildMutationPayload creates a MutationPayload for a changed issue, classifying
// the mutation type by comparing against previously known state. rawIssue, if
// non-nil, is embedded so frontend consumers can wholesale-replace their stored
// issue without hand-mapping fields.
func (s *DaemonSubscriber) buildMutationPayload(issue changedIssue, rawIssue json.RawMessage, now time.Time, known map[string]knownIssueState) *realtime.MutationPayload {
	payload := &realtime.MutationPayload{
		IssueID:     issue.ID,
		Title:       issue.Title,
		Assignee:    issue.Assignee,
		Timestamp:   now.UTC().Format(time.RFC3339),
		SourceRepo:  issue.SourceRepo,
		WorkspaceID: s.workspaceID,
		Priority:    &issue.Priority,
		Issue:       rawIssue,
	}
	prev, existed := known[issue.ID]
	switch {
	case !existed:
		payload.Type = rpc.MutationCreate
		payload.NewStatus = issue.Status
	case prev.Status != issue.Status:
		payload.Type = rpc.MutationStatus
		payload.OldStatus = prev.Status
		payload.NewStatus = issue.Status
	default:
		payload.Type = rpc.MutationUpdate
	}
	return payload
}

// updateKnownIssues merges changed issues into the known-issue snapshot and
// advances the poll cursor.
func (s *DaemonSubscriber) updateKnownIssues(changed []changedIssue, now time.Time, totalCount int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.knownIssues == nil {
		s.knownIssues = make(map[string]knownIssueState, len(changed))
	}
	for _, issue := range changed {
		if issue.ID == "" {
			continue
		}
		s.knownIssues[issue.ID] = knownIssueState{
			Title:    issue.Title,
			Assignee: issue.Assignee,
			Status:   issue.Status,
			Priority: issue.Priority,
		}
	}
	s.lastKnownCount = totalCount
	s.lastPollTime = now
}

// fetchChangedIssues calls List with UpdatedAfter to get issues changed since lastPollTime.
//
// Returns (issues, rawIssues, clientHealthy):
//   - clientHealthy == false iff the List RPC hit a transport error and the
//     connection's read buffer may be poisoned. The caller must Discard.
//   - (nil, nil, true) means "List succeeded logically but body was empty or
//     unparseable" — the caller may fall back to a blanket refresh.
//   - (issues, rawIssues, true) on the happy path. rawIssues is keyed by issue
//     ID and carries the per-issue JSON so SSE can embed it unchanged.
//
// Uses Lightweight: true to skip description/design/acceptance_criteria/notes,
// keeping broadcast payload sizes small (matching loadKnownIssues).
//
// Note the distinction: returning nil does NOT imply an unhealthy connection.
// Callers must check clientHealthy separately (ref: loomcli-67meg).
func (s *DaemonSubscriber) fetchChangedIssues(client *rpc.Client, since time.Time) ([]changedIssue, map[string]json.RawMessage, bool) {
	resp, err := client.List(&rpc.ListArgs{
		UpdatedAfter: since.UTC().Format(time.RFC3339),
		Lightweight:  true,
	})
	if err != nil {
		if resp == nil {
			slog.Error("external poll: list changed issues transport error", "err", err)
			return nil, nil, false
		}
		// resp != nil — Success=false. Logical failure, connection intact.
		slog.Error("external poll: list changed issues failed", "err", resp.Error)
		return nil, nil, true
	}
	if !resp.Success {
		slog.Error("external poll: list changed issues failed", "err", resp.Error)
		return nil, nil, true
	}

	issues, rawIssues := parseChangedIssues(resp.Data)
	return issues, rawIssues, true
}

// parseChangedIssues parses a List RPC response into changedIssue structs and
// a parallel ID-keyed map of raw per-issue JSON. The raw map is what SSE
// consumers embed so the frontend can wholesale-replace its stored issue.
func parseChangedIssues(data json.RawMessage) ([]changedIssue, map[string]json.RawMessage) {
	// Decode the array as raw JSON first so we can retain the per-element bytes
	// without a second Marshal round-trip.
	var rawElems []json.RawMessage
	if err := json.Unmarshal(data, &rawElems); err != nil {
		slog.Error("external poll: parse changed issues error", "err", err)
		return nil, nil
	}
	result := make([]changedIssue, 0, len(rawElems))
	rawByID := make(map[string]json.RawMessage, len(rawElems))
	for _, elem := range rawElems {
		var r struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Assignee   string `json:"assignee"`
			Status     string `json:"status"`
			Priority   int    `json:"priority"`
			SourceRepo string `json:"source_repo"`
		}
		if err := json.Unmarshal(elem, &r); err != nil {
			slog.Error("external poll: parse changed issue element error", "err", err)
			continue
		}
		result = append(result, changedIssue{
			ID:         r.ID,
			Title:      r.Title,
			Assignee:   r.Assignee,
			Status:     r.Status,
			Priority:   r.Priority,
			SourceRepo: r.SourceRepo,
		})
		if r.ID != "" {
			rawByID[r.ID] = elem
		}
	}
	return result, rawByID
}

// broadcastRefresh sends a MutationRefresh event and updates poll state.
func (s *DaemonSubscriber) broadcastRefresh(now time.Time, totalCount int64) {
	s.hub.Broadcast(&realtime.MutationPayload{
		Type:        rpc.MutationRefresh,
		IssueID:     "",
		Timestamp:   now.UTC().Format(time.RFC3339),
		WorkspaceID: s.workspaceID,
	})
	s.mu.Lock()
	s.lastKnownCount = totalCount
	s.lastPollTime = now
	s.mu.Unlock()
	slog.Info("external DB change detected, broadcast refresh", "clients", s.hub.ClientCount())
}

// loadKnownIssues builds or rebuilds the knownIssues map from the current database state.
// Best-effort: if the List RPC logically fails, knownIssues is left unchanged.
//
// Returns clientHealthy: false only when the List RPC hit a transport error
// (resp == nil && err != nil), indicating the caller must Discard the
// connection. A Success=false or parse error keeps clientHealthy = true
// because the response frame was fully received (ref: loomcli-67meg).
//
// Note: Limit 500 matches other callers in the codebase. For workspaces with >500 issues,
// issues beyond this snapshot may produce spurious MutationCreate events, but the frontend
// dedup logic in useMutationHandler handles this gracefully (treating duplicate creates as updates).
func (s *DaemonSubscriber) loadKnownIssues(client *rpc.Client) bool {
	resp, err := client.List(&rpc.ListArgs{Limit: 500, Lightweight: true})
	if err != nil {
		if resp == nil {
			return false
		}
		// Success=false — logical failure, connection intact.
		return true
	}
	if !resp.Success {
		return true
	}
	issues, _ := parseChangedIssues(resp.Data)
	if issues == nil {
		return true
	}
	known := make(map[string]knownIssueState, len(issues))
	for _, issue := range issues {
		if issue.ID == "" {
			continue
		}
		known[issue.ID] = knownIssueState{
			Title:    issue.Title,
			Assignee: issue.Assignee,
			Status:   issue.Status,
			Priority: issue.Priority,
		}
	}
	s.mu.Lock()
	s.knownIssues = known
	s.mu.Unlock()
	return true
}
