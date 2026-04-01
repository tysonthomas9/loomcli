package webui

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/rpc"
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
func (s *DaemonSubscriber) emitGranularMutations(changed []changedIssue, now time.Time, totalCount int64) {
	// Copy knownIssues map contents (not just the pointer) while holding the lock
	// to avoid a race with concurrent loadKnownIssues replacing the map.
	s.mu.Lock()
	known := make(map[string]knownIssueState, len(s.knownIssues))
	for k, v := range s.knownIssues {
		known[k] = v
	}
	s.mu.Unlock()

	mutationCount := 0
	for _, issue := range changed {
		if issue.ID == "" {
			continue
		}

		payload := &MutationPayload{
			IssueID:     issue.ID,
			Title:       issue.Title,
			Assignee:    issue.Assignee,
			Timestamp:   now.UTC().Format(time.RFC3339),
			SourceRepo:  issue.SourceRepo,
			WorkspaceID: s.workspaceID,
			Priority:    &issue.Priority,
		}

		prev, existed := known[issue.ID]
		if !existed {
			payload.Type = rpc.MutationCreate
			payload.NewStatus = issue.Status
		} else if prev.Status != issue.Status {
			payload.Type = rpc.MutationStatus
			payload.OldStatus = prev.Status
			payload.NewStatus = issue.Status
		} else {
			payload.Type = rpc.MutationUpdate
		}

		s.hub.Broadcast(payload)
		mutationCount++
	}

	// Update known issues with new state
	s.mu.Lock()
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
	s.mu.Unlock()

	if mutationCount > 0 {
		slog.Info("external DB change: broadcast granular mutations", "count", mutationCount, "clients", s.hub.ClientCount())
	}
}

// fetchChangedIssues calls List with UpdatedAfter to get issues changed since lastPollTime.
// Returns nil if the RPC call fails or the response cannot be parsed.
func (s *DaemonSubscriber) fetchChangedIssues(client *rpc.Client, since time.Time) []changedIssue {
	resp, err := client.List(&rpc.ListArgs{
		UpdatedAfter: since.UTC().Format(time.RFC3339),
	})
	if err != nil {
		slog.Error("external poll: list changed issues error", "err", err)
		return nil
	}
	if !resp.Success {
		slog.Error("external poll: list changed issues failed", "err", resp.Error)
		return nil
	}

	return parseChangedIssues(resp.Data)
}

// parseChangedIssues parses a List RPC response into changedIssue structs.
func parseChangedIssues(data json.RawMessage) []changedIssue {
	// List returns []*types.IssueWithCounts; we parse only the fields we need.
	var raw []struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		Assignee   string `json:"assignee"`
		Status     string `json:"status"`
		Priority   int    `json:"priority"`
		SourceRepo string `json:"source_repo"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		slog.Error("external poll: parse changed issues error", "err", err)
		return nil
	}
	result := make([]changedIssue, 0, len(raw))
	for _, r := range raw {
		result = append(result, changedIssue{
			ID:         r.ID,
			Title:      r.Title,
			Assignee:   r.Assignee,
			Status:     r.Status,
			Priority:   r.Priority,
			SourceRepo: r.SourceRepo,
		})
	}
	return result
}

// broadcastRefresh sends a MutationRefresh event and updates poll state.
func (s *DaemonSubscriber) broadcastRefresh(now time.Time, totalCount int64) {
	s.hub.Broadcast(&MutationPayload{
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
// Best-effort: if the List RPC fails, knownIssues is left unchanged.
// Note: Limit 500 matches other callers in the codebase. For workspaces with >500 issues,
// issues beyond this snapshot may produce spurious MutationCreate events, but the frontend
// dedup logic in useMutationHandler handles this gracefully (treating duplicate creates as updates).
func (s *DaemonSubscriber) loadKnownIssues(client *rpc.Client) {
	resp, err := client.List(&rpc.ListArgs{Limit: 500})
	if err != nil || !resp.Success {
		return
	}
	issues := parseChangedIssues(resp.Data)
	if issues == nil {
		return
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
}
