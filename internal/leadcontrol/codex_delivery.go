package leadcontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type DeliveryState string

const (
	DeliveryStateNone        DeliveryState = "none"
	DeliveryStatePending     DeliveryState = "pending"
	DeliveryStateDelivered   DeliveryState = "delivered"
	DeliveryStateUnsupported DeliveryState = "unsupported"
)

type DeliveryResult struct {
	State     DeliveryState
	Reason    string
	Runtime   CodexRuntimeMetadata
	Thread    *CodexThread
	SessionID string
}

func DeliverCurrentAssignmentToCodex(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
	assignment, err := epicrunner.LoadLeadAssignmentContext(ctx, st, workspace, leadName)
	if err != nil || assignment == nil {
		return &DeliveryResult{State: DeliveryStateNone}, err
	}

	session, err := store.OrchestrationSessionFor(ctx, st, workspace, assignment.LeadName)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return pendingDelivery("", "lead has no orchestration session"), nil
	}
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID}
	if strings.TrimSpace(session.Metadata[MetadataDeliveryVersion]) == assignment.AssignmentVersion {
		result.State = DeliveryStateDelivered
		return result, nil
	}

	runtime := RuntimeMetadataFromSession(session)
	result.Runtime = runtime
	if !hasCodexRuntimeMetadata(session.Metadata) {
		return recordPendingDelivery(ctx, st, workspace, session.SessionID, result, "controlled Codex runtime is not ready"), nil
	}
	if codexRuntimeUnsupported(session.Metadata, runtime) {
		result.State = DeliveryStateUnsupported
		result.Reason = "lead session is not a controlled Codex runtime"
		return result, nil
	}
	if pendingReason := codexRuntimePendingReason(runtime); pendingReason != "" {
		return recordPendingDelivery(ctx, st, workspace, session.SessionID, result, pendingReason), nil
	}
	return deliverCodexAssignmentTurn(ctx, st, workspace, session.SessionID, assignment, runtime, result)
}

func deliverCodexAssignmentTurn(
	ctx context.Context,
	st store.Store,
	workspace string,
	sessionID string,
	assignment *epicrunner.LeadAssignmentContext,
	runtime CodexRuntimeMetadata,
	result *DeliveryResult,
) (*DeliveryResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := dialCodexAppServerClient(callCtx, runtime.Endpoint)
	if err != nil {
		result.Reason = err.Error()
		_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, result.Reason)
		return result, nil
	}
	defer func() { _ = client.Close("assignment delivery complete") }()

	thread, err := client.ReadThread(callCtx, runtime.ThreadID)
	if err != nil {
		result.Reason = err.Error()
		_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, result.Reason)
		return result, nil
	}
	result.Thread = thread
	runtime.Status = thread.Status.RuntimeStatus()
	_ = UpdateCodexRuntimeMetadata(ctx, st, workspace, sessionID, runtime)
	if !thread.Status.CanStartTurn() {
		result.Reason = fmt.Sprintf("codex thread is %s", runtime.Status)
		_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, result.Reason)
		return result, nil
	}

	message := formatCodexAssignmentTurn(assignment)
	if err := client.StartTurn(callCtx, runtime.ThreadID, message); err != nil {
		result.Reason = err.Error()
		_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, result.Reason)
		return result, nil
	}
	if err := MarkAssignmentDelivered(ctx, st, workspace, sessionID, assignment.EpicID, assignment.AssignmentVersion); err != nil {
		return nil, err
	}
	result.State = DeliveryStateDelivered
	result.Reason = ""
	return result, nil
}

func codexRuntimePendingReason(runtime CodexRuntimeMetadata) string {
	if runtime.Endpoint == "" || runtime.ThreadID == "" {
		return "controlled Codex runtime is not ready"
	}
	return ""
}

func codexRuntimeUnsupported(metadata map[string]string, runtime CodexRuntimeMetadata) bool {
	return strings.TrimSpace(metadata[MetadataRuntimeProvider]) != RuntimeProviderCodex || !runtime.Controlled
}

func hasCodexRuntimeMetadata(metadata map[string]string) bool {
	if len(metadata) == 0 {
		return false
	}
	for _, key := range []string{
		MetadataRuntimeProvider,
		MetadataRuntimeControlled,
		MetadataCodexEndpoint,
		MetadataCodexPID,
		MetadataCodexRuntimeHome,
		MetadataCodexSQLiteHome,
		MetadataCodexThreadID,
	} {
		if strings.TrimSpace(metadata[key]) != "" {
			return true
		}
	}
	return false
}

func recordPendingDelivery(ctx context.Context, st store.Store, workspace, sessionID string, result *DeliveryResult, reason string) *DeliveryResult {
	result.Reason = reason
	_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, reason)
	return result
}

func pendingDelivery(sessionID, reason string) *DeliveryResult {
	return &DeliveryResult{State: DeliveryStatePending, Reason: reason, SessionID: sessionID}
}

func formatCodexAssignmentTurn(assignment *epicrunner.LeadAssignmentContext) string {
	var b strings.Builder
	b.WriteString("Loom assigned this lead session an epic through backend state.\n\n")
	b.WriteString(epicrunner.FormatLeadAssignmentContext(assignment))
	b.WriteString("\n\nAcknowledge this backend assignment in the visible conversation, then start or resume the assigned epic using Loom's lead-mode conventions. Do not switch to a different epic unless the user explicitly asks.")
	return b.String()
}
