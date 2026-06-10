package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/domain"
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
	State          DeliveryState
	Reason         string
	Runtime        CodexRuntimeMetadata
	Thread         *CodexThread
	SessionID      string
	InboxMessageID string
}

type LeadMessageDeliveryOptions struct {
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
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

func DeliverLeadMessageToCodex(ctx context.Context, st store.Store, workspace, leadName, message string) (*DeliveryResult, error) {
	return DeliverLeadMessageToCodexWithOptions(ctx, st, workspace, leadName, message, LeadMessageDeliveryOptions{})
}

func DeliverLeadMessageToCodexWithOptions(ctx context.Context, st store.Store, workspace, leadName, message string, opts LeadMessageDeliveryOptions) (*DeliveryResult, error) {
	leadName = strings.TrimSpace(leadName)
	message = strings.TrimSpace(message)
	if leadName == "" {
		return nil, fmt.Errorf("lead agent required")
	}
	if message == "" {
		return nil, fmt.Errorf("lead message required")
	}

	session, err := store.OrchestrationSessionFor(ctx, st, workspace, leadName)
	if err != nil {
		return nil, err
	}
	if session == nil {
		inbox, err := createLeadInboxMessage(ctx, st, workspace, leadName, "", message, opts)
		if err != nil {
			return nil, err
		}
		result := pendingDelivery("", "lead has no orchestration session")
		if inbox != nil {
			result.InboxMessageID = inbox.InboxMessageID
		}
		return result, nil
	}
	runtime := RuntimeMetadataFromSession(session)
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID, Runtime: runtime}
	if hasCodexRuntimeMetadata(session.Metadata) && codexRuntimeUnsupported(session.Metadata, runtime) {
		result.State = DeliveryStateUnsupported
		result.Reason = "lead session is not a controlled Codex runtime"
		return result, nil
	}
	inbox, err := createLeadInboxMessage(ctx, st, workspace, leadName, session.SessionID, message, opts)
	if err != nil {
		return nil, err
	}
	if inbox != nil {
		result.InboxMessageID = inbox.InboxMessageID
	}
	session, err = st.AgentSessions().Get(ctx, workspace, session.SessionID)
	if err != nil {
		return nil, err
	}
	runtime = RuntimeMetadataFromSession(session)
	result.Runtime = runtime
	if !hasCodexRuntimeMetadata(session.Metadata) {
		result.Reason = "controlled Codex runtime is not ready"
		_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, session.SessionID, result.Reason)
		return result, nil
	}
	if codexRuntimeUnsupported(session.Metadata, runtime) {
		result.State = DeliveryStateUnsupported
		result.Reason = "lead session is not a controlled Codex runtime"
		return result, nil
	}
	if pendingReason := codexRuntimePendingReason(runtime); pendingReason != "" {
		result.Reason = pendingReason
		_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, session.SessionID, result.Reason)
		return result, nil
	}
	return deliverNextCodexInboxMessage(ctx, st, workspace, leadName, session.SessionID, runtime, result)
}

func DeliverPendingLeadMessagesToCodex(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
	leadName = strings.TrimSpace(leadName)
	if leadName == "" {
		return nil, fmt.Errorf("lead agent required")
	}
	session, err := store.OrchestrationSessionFor(ctx, st, workspace, leadName)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return pendingDelivery("", "lead has no orchestration session"), nil
	}
	if !hasQueuedLeadInboxMessages(ctx, st, workspace, leadName, session.SessionID) {
		return &DeliveryResult{State: DeliveryStateNone, SessionID: session.SessionID, Runtime: RuntimeMetadataFromSession(session)}, nil
	}
	runtime := RuntimeMetadataFromSession(session)
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID, Runtime: runtime}
	if !hasCodexRuntimeMetadata(session.Metadata) {
		result.Reason = "controlled Codex runtime is not ready"
		return result, nil
	}
	if codexRuntimeUnsupported(session.Metadata, runtime) {
		result.State = DeliveryStateUnsupported
		result.Reason = "lead session is not a controlled Codex runtime"
		return result, nil
	}
	if pendingReason := codexRuntimePendingReason(runtime); pendingReason != "" {
		result.Reason = pendingReason
		return result, nil
	}
	return deliverNextCodexInboxMessage(ctx, st, workspace, leadName, session.SessionID, runtime, result)
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
	message := formatCodexAssignmentTurn(assignment)
	delivered, err := deliverCodexLeadTurn(ctx, st, workspace, sessionID, runtime, result, message, "assignment delivery complete")
	if err != nil {
		return nil, err
	}
	if delivered.State != DeliveryStateDelivered {
		if delivered.Reason != "" {
			_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, delivered.Reason)
		}
		return delivered, nil
	}
	if err := MarkAssignmentDelivered(ctx, st, workspace, sessionID, assignment.EpicID, assignment.AssignmentVersion); err != nil {
		return nil, err
	}
	delivered.Reason = ""
	return delivered, nil
}

func deliverNextCodexInboxMessage(
	ctx context.Context,
	st store.Store,
	workspace string,
	leadName string,
	sessionID string,
	runtime CodexRuntimeMetadata,
	result *DeliveryResult,
) (*DeliveryResult, error) {
	if st == nil || st.AgentInboxMessages() == nil {
		result.Reason = "agent inbox store is not configured"
		return result, nil
	}
	claimedBy := "codex:" + sessionID
	if runtime.ThreadID != "" {
		claimedBy += ":" + runtime.ThreadID
	}
	msg, err := st.AgentInboxMessages().ClaimNext(ctx, store.AgentInboxMessageClaim{
		WorkspaceKey:  workspace,
		TargetAgentID: leadName,
		SessionID:     sessionID,
		ClaimedBy:     claimedBy,
		LeaseTTL:      2 * time.Minute,
	})
	if errors.Is(err, domain.ErrNotFound) {
		result.State = DeliveryStateNone
		result.Reason = ""
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.InboxMessageID = msg.InboxMessageID
	delivered, err := deliverCodexLeadTurn(ctx, st, workspace, sessionID, runtime, result, msg.Body, "lead message delivery complete")
	if err != nil {
		return nil, err
	}
	if delivered.State != DeliveryStateDelivered {
		if delivered.Reason != "" {
			_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, sessionID, delivered.Reason)
		}
		if _, err := st.AgentInboxMessages().Complete(ctx, workspace, msg.InboxMessageID, store.AgentInboxMessageComplete{
			Outcome:    "retry",
			ErrorClass: "codex_delivery_pending",
			Error:      delivered.Reason,
		}); err != nil {
			return nil, err
		}
		return delivered, nil
	}
	if _, err := st.AgentInboxMessages().Complete(ctx, workspace, msg.InboxMessageID, store.AgentInboxMessageComplete{
		Outcome:           "delivered",
		DeliveredThreadID: runtime.ThreadID,
	}); err != nil {
		return nil, err
	}
	return delivered, nil
}

func deliverCodexLeadTurn(
	ctx context.Context,
	st store.Store,
	workspace string,
	sessionID string,
	runtime CodexRuntimeMetadata,
	result *DeliveryResult,
	message string,
	closeReason string,
) (*DeliveryResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := dialCodexAppServerClient(callCtx, runtime.Endpoint)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	defer func() { _ = client.Close(closeReason) }()

	thread, err := client.ReadThread(callCtx, runtime.ThreadID)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.Thread = thread
	runtime.Status = thread.Status.RuntimeStatus()
	result.Runtime = runtime
	_ = UpdateCodexRuntimeMetadata(ctx, st, workspace, sessionID, runtime)
	if !thread.Status.CanStartTurn() {
		result.Reason = fmt.Sprintf("codex thread is %s", runtime.Status)
		return result, nil
	}

	if err := client.StartTurn(callCtx, runtime.ThreadID, message); err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.State = DeliveryStateDelivered
	result.Reason = ""
	return result, nil
}

func createLeadInboxMessage(ctx context.Context, st store.Store, workspace, leadName, sessionID, message string, opts LeadMessageDeliveryOptions) (*domain.AgentInboxMessage, error) {
	if opts.SourceKind == "" {
		opts.SourceKind = "workflow"
	}
	if opts.SourceRef == "" && opts.DriverRunID != "" {
		opts.SourceRef = "driver-run://" + opts.DriverRunID
	}
	return agentinbox.Enqueue(ctx, st, workspace, leadName, message, agentinbox.MessageOptions{
		SessionID:         sessionID,
		SourceKind:        opts.SourceKind,
		SourceRef:         opts.SourceRef,
		DriverRunID:       opts.DriverRunID,
		TaskRunID:         opts.TaskRunID,
		TriggerEventID:    opts.TriggerEventID,
		TriggerDeliveryID: opts.TriggerDeliveryID,
		DedupeKey:         leadInboxDedupeKey(sessionID, message),
	})
}

func hasQueuedLeadInboxMessages(ctx context.Context, st store.Store, workspace, leadName, sessionID string) bool {
	if st == nil || st.AgentInboxMessages() == nil {
		return false
	}
	items, err := st.AgentInboxMessages().List(ctx, workspace, store.AgentInboxMessageFilter{
		TargetAgentID: leadName,
		Status:        domain.AgentInboxMessageQueued,
		Limit:         100,
	})
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.SessionID == "" || item.SessionID == sessionID {
			return true
		}
	}
	return false
}

func leadInboxDedupeKey(sessionID, message string) string {
	return agentinbox.ContentDedupeKey("agent-message:"+strings.TrimSpace(sessionID), sessionID, message)
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
	b.WriteString("\n\nAcknowledge this backend assignment in the visible conversation. The UI/backend has already queued the epic-runner workflow for this assignment, so monitor the assigned epic and in-progress child tasks instead of starting another `loom epic run` from this terminal. Do not switch to a different epic unless the user explicitly asks.")
	return b.String()
}
