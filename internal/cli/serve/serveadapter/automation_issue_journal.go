package serveadapter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/systemeventing"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// automationIssueJournalEmitter is the composition adapter from the legacy
// journal poller's narrow producer contract to the named systemeventing
// workflow. It has no TriggerRoute or trigger persistence access.
type automationIssueJournalEmitter struct {
	emitter systemeventing.IssueJournalEmitter
	awaits  *trigger.AwaitMatcher
}

var _ trigger.InternalEventEmitter = (*automationIssueJournalEmitter)(nil)

// NewAutomationIssueJournalEmitter returns the narrow journal producer used
// by the legacy polling loop while Automation owns admission and policy.
func NewAutomationIssueJournalEmitter(
	emitter systemeventing.IssueJournalEmitter,
	awaits *trigger.AwaitMatcher,
) trigger.InternalEventEmitter {
	if emitter == nil {
		return nil
	}
	return &automationIssueJournalEmitter{emitter: emitter, awaits: awaits}
}

func (emitter *automationIssueJournalEmitter) Emit(
	ctx context.Context,
	workspace string,
	event trigger.InternalEvent,
) (*trigger.InternalEmitResult, error) {
	if emitter == nil || emitter.emitter == nil {
		return nil, systemeventing.ErrUnavailable
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || event.Origin != domain.TriggerEventOriginSystem {
		return nil, fmt.Errorf("issue journal emitter requires a system event and workspace: %w", domain.ErrInvalid)
	}
	result, err := emitter.emitter.EmitIssueJournal(ctx, workspace, event.ActorRef, systemeventing.EmitRequest{
		WorkspaceKey:  workspace,
		SourceEventID: event.EventID,
		EventType:     event.EventType,
		SourceRef:     systemeventing.IssueJournalBridgeComponentID,
		SubjectRef:    event.SubjectRef,
		ParentEventID: event.ParentEventID,
		EpicID:        event.EpicID,
		Payload:       event.Payload,
		SubjectAttrs:  event.SubjectAttrs,
	})
	if err != nil {
		if errors.Is(err, automation.ErrNotFound) || errors.Is(err, automation.ErrNoMatchingBinding) {
			return nil, errors.Join(domain.ErrNotFound, err)
		}
		return nil, err
	}
	if result == nil {
		return nil, automation.ErrInvalidPersistedState
	}
	mapped := &trigger.InternalEmitResult{
		Dropped:    result.Dropped,
		DropReason: result.DropReason,
		EventType:  result.EventType,
		RouteKey:   result.RouteKey,
		Origin:     result.Origin,
		HopDepth:   result.HopDepth,
	}
	emitter.notifyAwait(ctx, workspace, result, event.Payload)
	return mapped, nil
}

func (emitter *automationIssueJournalEmitter) notifyAwait(
	ctx context.Context,
	workspace string,
	result *automation.AdmissionResult,
	payload []byte,
) {
	if result.Dropped || result.Event == nil || emitter.awaits == nil {
		return
	}
	if _, err := emitter.awaits.Dispatch(ctx, workspace, trigger.AwaitDispatchEvent{
		EventID:    result.Event.SourceEventID,
		EventType:  result.Event.EventType,
		SourceKind: result.Event.SourceKind,
		Origin:     result.Event.Origin,
		SubjectRef: result.Event.SubjectRef,
		ActorRef:   result.Event.ActorRef,
		Payload:    payload,
	}); err != nil {
		// Await notification is best effort after durable admission, matching
		// the existing workflow-originated event lane.
		slog.WarnContext(ctx, "issue journal Automation await dispatch failed",
			"workspace", workspace, "event_id", result.Event.SourceEventID, "error", err)
	}
}
