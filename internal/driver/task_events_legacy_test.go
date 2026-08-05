package driver

// Legacy TaskRun event/outbox helpers remain test-only while the
// characterization suite proves parity with Execution-owned convergence.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Server-side TaskRunEvent emission + lead-outbox creation for TaskRun
// lifecycle transitions. loom serve is the publisher: every transition
// appends a journal event (best-effort — watch reconciliation snapshots
// cover a dropped append), and terminal transitions under a lead-bound
// epic create an outbox notification row. This replaces epic-runner's
// workflow-side formatTaskCompleteLeadMessage + enqueue loop; per §9.4
// the policy and message content live server-side.

// taskRunEventContext carries hook-site extras that are not derivable
// from the run row itself.
type taskRunEventContext struct {
	EpicID string
}

// appendTaskRunEvent records one lifecycle event in the append-only
// journal with the deterministic EventID taskRunID#attempt#type. It is
// best-effort: failures are logged and never fail the transition.
func appendTaskRunEvent(ctx context.Context, s store.Store, run *domain.TaskRun, typ domain.TaskRunEventType, completion taskExecCompletion, evctx taskRunEventContext) {
	if s == nil || run == nil {
		return
	}
	attempt := taskRunAttempt(run)
	in := store.TaskRunEventAppend{
		WorkspaceKey:   run.WorkspaceKey,
		EventID:        domain.TaskRunEventID(run.TaskRunID, attempt, typ),
		EpicID:         evctx.EpicID,
		DriverRunID:    run.DriverRunID,
		TaskID:         run.TaskID,
		TaskRunID:      run.TaskRunID,
		Type:           typ,
		Status:         run.Status,
		SchedulerState: run.RuntimeMetadata["scheduler_state"],
		Attempt:        attempt,
		ErrorClass:     firstNonEmpty(completion.ErrorClass, run.ErrorClass),
		ErrorMessage:   firstNonEmpty(completion.ErrorMessage, run.ErrorMessage),
		LogsRef:        run.LogsRef,
		ArtifactsRef:   run.ArtifactsRef,
		OccurredAt:     time.Now().UTC(),
	}
	if typ == domain.TaskRunEventRequeued {
		in.NextEligibleAt = run.NextEligibleAt
	}
	if _, err := s.TaskRunEvents().Append(ctx, in); err != nil {
		slog.WarnContext(ctx, "append task run event failed",
			"taskRunID", run.TaskRunID, "type", string(typ), "error", err)
	}
}

// emitTerminalTaskRunEvents appends the terminal journal event for a
// finished run and, on completed/retry-exhausted transitions under a lead-bound
// epic, creates the lead-notification outbox row.
func emitTerminalTaskRunEvents(ctx context.Context, s store.Store, final *domain.TaskRun, completion taskExecCompletion, evctx taskRunEventContext) {
	if s == nil || final == nil {
		return
	}
	typ := terminalTaskRunEventType(completion)
	appendTaskRunEvent(ctx, s, final, typ, completion, evctx)
	if typ == domain.TaskRunEventCompleted || taskRunBlockedByRetryExhaustion(final) {
		createLeadTaskOutbox(ctx, s, final, evctx.EpicID)
	}
}

// terminalTaskRunEventType maps a terminal completion to its journal
// event type.
func terminalTaskRunEventType(completion taskExecCompletion) domain.TaskRunEventType {
	switch completion.Status {
	case domain.TaskRunCompleted:
		return domain.TaskRunEventCompleted
	case domain.TaskRunCancelled:
		return domain.TaskRunEventCancelled
	default:
		return domain.TaskRunEventFailed
	}
}

func taskRunBlockedByRetryExhaustion(run *domain.TaskRun) bool {
	return run != nil && run.RuntimeMetadata["scheduler_state"] == "blocked"
}

// taskRunEpicID resolves the epic a run belongs to via its parent driver
// run. Best-effort: lookup failures are logged and yield "".
func taskRunEpicID(ctx context.Context, s store.Store, run *domain.TaskRun) string {
	if s == nil || run == nil || run.DriverRunID == "" {
		return ""
	}
	parent, err := s.DriverRuns().Get(ctx, run.WorkspaceKey, run.DriverRunID)
	if err != nil {
		slog.WarnContext(ctx, "resolve task run epic failed",
			"taskRunID", run.TaskRunID, "driverRunID", run.DriverRunID, "error", err)
		return ""
	}
	return parent.EpicID
}

// createLeadTaskOutbox creates the lead-notification outbox row for a
// terminal completed or retry-exhausted run under a lead-bound epic. The lead is
// resolved at row-creation time; with no lead bound, no row is created.
// DedupeKey keeps dispatcher redelivery exactly-once when the completion
// path re-runs. Best-effort: failures are logged, never returned.
func createLeadTaskOutbox(ctx context.Context, s store.Store, run *domain.TaskRun, epicID string) {
	if run == nil || epicID == "" {
		return
	}
	lead, err := resolveEpicLead(ctx, s, run.WorkspaceKey, epicID)
	if err != nil {
		slog.WarnContext(ctx, "resolve epic lead for outbox failed",
			"taskRunID", run.TaskRunID, "epicID", epicID, "error", err)
		return
	}
	if lead == "" {
		return
	}
	_, err = s.Outbox().Create(ctx, store.OutboxCreate{
		WorkspaceKey: run.WorkspaceKey,
		Kind:         domain.OutboxKindLeadTaskMessage,
		EpicID:       epicID,
		DriverRunID:  run.DriverRunID,
		TaskRunID:    run.TaskRunID,
		TargetAgent:  lead,
		Body:         buildLeadTaskMessage(epicID, run.TaskID, "", run.TaskRunID, run.LogsRef, run.ArtifactsRef, run.Status),
		DedupeKey:    "lead-task-message:" + epicID + ":" + run.TaskRunID + ":" + string(run.Status),
	})
	if err != nil {
		slog.WarnContext(ctx, "create lead task outbox failed",
			"taskRunID", run.TaskRunID, "epicID", epicID, "error", err)
	}
}

// resolveEpicLead finds the lead/orchestrator agent bound to an epic —
// the server-side port of epic-runner's findConflictingLeadOwner +
// isLeadRole scan. Agents().List returns name-sorted agents, so ties
// resolve deterministically. Returns "" when no lead is bound.
func resolveEpicLead(ctx context.Context, s store.Store, workspaceKey, epicID string) (string, error) {
	if epicID == "" {
		return "", nil
	}
	agents, err := s.Agents().List(ctx, workspaceKey)
	if err != nil {
		return "", fmt.Errorf("list agents for epic lead: %w", err)
	}
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		if isLeadRole(agent.RoleName) && strings.TrimSpace(agent.Parent) == epicID {
			return agent.Name, nil
		}
	}
	return "", nil
}

// isLeadRole reports whether a role name marks an epic lead — the Go
// port of epic-runner's isLeadRole.
func isLeadRole(roleName string) bool {
	switch strings.ToLower(strings.TrimSpace(roleName)) {
	case "lead", "orchestrator":
		return true
	default:
		return false
	}
}

// buildLeadTaskMessage is the Go port of epic-runner's
// formatTaskCompleteLeadMessage, including the "Do not start another
// epic runner" guardrail. A failed status swaps the headline and the
// acknowledgement subject; the rest of the template is shared.
func buildLeadTaskMessage(epicID, taskID, title, taskRunID, logsRef, artifactsRef string, status domain.TaskRunStatus) string {
	headline := "Loom completed a child task under the active epic-runner workflow."
	subject := "completion"
	if status != domain.TaskRunCompleted {
		headline = "Loom blocked a child task under the active epic-runner workflow; retries are exhausted and the run needs review."
		subject = "blocked task"
	}
	taskLine := taskID
	if strings.TrimSpace(title) != "" {
		taskLine += " - " + strings.TrimSpace(title)
	}
	lines := []string{
		headline,
		"",
		"epic: " + epicID,
		"task: " + taskLine,
		"task_run: " + taskRunID,
	}
	if logsRef != "" {
		lines = append(lines, "logs: "+logsRef)
	}
	if artifactsRef != "" {
		lines = append(lines, "artifacts: "+artifactsRef)
	}
	lines = append(lines, "",
		"Acknowledge this "+subject+" in the visible conversation, update your epic status summary, and continue monitoring the remaining child tasks. Do not start another epic runner.")
	return strings.Join(lines, "\n")
}
