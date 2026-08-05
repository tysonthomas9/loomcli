package realtime

import (
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// BackendMutationToPayload converts a backend.MutationData into the SSE
// MutationPayload wire format. The workspaceID parameter is injected onto
// the payload because backend.MutationData is workspace-agnostic — fleet-db
// emits per-workspace mutation streams keyed by URL path, so the wire-level
// workspace tag must come from the subscriber that produced the event.
//
// This function MUST produce a MutationPayload that, when JSON-marshaled,
// is byte-identical to MutationEventToPayload's output for the same logical
// event. Any drift breaks reconnection catch-up across RPC and backend streams.
func BackendMutationToPayload(m backend.MutationData, workspaceID string) *MutationPayload {
	return &MutationPayload{
		Cursor:      m.Cursor,
		Type:        m.Type,
		EntityType:  m.EntityType,
		EntityID:    m.EntityID,
		Action:      m.Action,
		IssueID:     m.IssueID,
		Title:       m.Title,
		Assignee:    mutationAssignee(m.Action, m.Assignee, m.Actor, m.OldStatus, m.NewStatus),
		Actor:       m.Actor,
		Timestamp:   m.Timestamp.UTC().Format(time.RFC3339Nano),
		OldStatus:   m.OldStatus,
		NewStatus:   m.NewStatus,
		ParentID:    m.ParentID,
		StepCount:   m.StepCount,
		SourceRepo:  m.SourceRepo,
		WorkspaceID: workspaceID,
	}
}

// mutationAssignee preserves explicit ownership retirement. Fleet's mutation
// projection otherwise represents both "not present" and "cleared" as the Go
// zero value. issue.assign is explicit by definition; a task-run transition
// out of in_progress is the atomic Work Item handoff and also clears ownership.
// Human status changes remain sparse because they may intentionally retain an
// assignee. Without this distinction, browser clients display a former task-run
// claimant until a full reload.
func mutationAssignee(action, assignee, actor, oldStatus, newStatus string) *string {
	if assignee == "" && action != "issue.assign" &&
		!(strings.HasPrefix(actor, "task-run:") && oldStatus == "in_progress" &&
			newStatus != "" && newStatus != "in_progress") {
		return nil
	}
	value := assignee
	return &value
}

// BackendMutationToEvent projects backend mutation data to the durable
// realtime catch-up event. Used by the catch-up path so that
// MultiWorkspaceSubscriber.GetMutationsSinceForWorkspace can return a single
// []MutationEvent. The Timestamp is preserved as-is; MutationEventToPayload
// formats it into RFC3339.
func BackendMutationToEvent(m backend.MutationData) MutationEvent {
	return MutationEvent{
		Cursor:     m.Cursor,
		Type:       m.Type,
		EntityType: m.EntityType,
		EntityID:   m.EntityID,
		Action:     m.Action,
		IssueID:    m.IssueID,
		Title:      m.Title,
		Assignee:   m.Assignee,
		Actor:      m.Actor,
		Timestamp:  m.Timestamp,
		OldStatus:  m.OldStatus,
		NewStatus:  m.NewStatus,
		ParentID:   m.ParentID,
		StepCount:  m.StepCount,
		SourceRepo: m.SourceRepo,
	}
}

// EventToMutationData is the inverse of BackendMutationToEvent.
func EventToMutationData(e MutationEvent) backend.MutationData {
	return backend.MutationData{
		Cursor:     e.Cursor,
		Type:       e.Type,
		EntityType: e.EntityType,
		EntityID:   e.EntityID,
		Action:     e.Action,
		IssueID:    e.IssueID,
		Title:      e.Title,
		Assignee:   e.Assignee,
		Actor:      e.Actor,
		Timestamp:  e.Timestamp,
		OldStatus:  e.OldStatus,
		NewStatus:  e.NewStatus,
		ParentID:   e.ParentID,
		StepCount:  e.StepCount,
		SourceRepo: e.SourceRepo,
	}
}
