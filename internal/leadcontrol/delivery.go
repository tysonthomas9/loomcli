package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	Provider       string
	Runtime        CodexRuntimeMetadata
	HarnessRuntime HarnessRuntimeMetadata
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

const (
	assignmentInboxSourceKind      = "lead_assignment"
	assignmentInboxSourceRefPrefix = "lead-assignment://"
	leadMessageDrainInterval       = 2 * time.Second
)

// leadTurnDeliverer is the per-provider strategy for injecting a queued inbox
// message into a lead's live session. The codex implementation dials the
// app-server endpoint persisted in session metadata; the harness
// implementation requires the in-process conversation registry (delivery from
// other processes stays enqueue-only).
type leadTurnDeliverer interface {
	provider() string
	hasRuntimeMetadata(metadata map[string]string) bool
	notReadyReason() string
	// unsupportedReason returns a non-empty reason when the session's runtime
	// metadata identifies a runtime this deliverer cannot inject into.
	unsupportedReason(metadata map[string]string) string
	// pendingReason returns a non-empty reason when runtime metadata exists
	// but the runtime is not yet ready to accept a turn.
	pendingReason() string
	claimedBy(sessionID string) string
	// populate refreshes the deliverer's cached runtime view from the session
	// and mirrors it onto the result.
	populate(result *DeliveryResult, session *domain.AgentSession)
	deliveredThreadID() string
	deliverTurn(ctx context.Context, st store.Store, workspace, sessionID string,
		result *DeliveryResult, message, closeReason string) (*DeliveryResult, error)
}

// delivererForSession picks the delivery strategy from the session's runtime
// provider metadata. Sessions without provider metadata default to codex,
// preserving the pre-provider behavior (and its exact pending reasons).
func delivererForSession(session *domain.AgentSession) leadTurnDeliverer {
	provider := ""
	if session != nil {
		provider = strings.TrimSpace(session.Metadata[MetadataRuntimeProvider])
	}
	if provider != "" && !strings.EqualFold(provider, RuntimeProviderCodex) {
		return newHarnessTurnDeliverer(provider, session)
	}
	return newCodexTurnDeliverer(session)
}

// IsControlledLeadBackend reports whether leads on the given backend run under
// a controlled runtime that supports queued message delivery. Must stay in
// sync with the launch dispatch in backends.RunControlledLeadRuntime.
func IsControlledLeadBackend(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case RuntimeProviderCodex, "claude", "gemini", "opencode", "cursor":
		return true
	default:
		return false
	}
}

func DeliverCurrentAssignment(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
	assignment, err := epicrunner.LoadLeadAssignmentContext(ctx, st, workspace, leadName)
	if err != nil || assignment == nil {
		return &DeliveryResult{State: DeliveryStateNone}, err
	}

	session, err := store.OrchestrationSessionFor(ctx, st, workspace, assignment.LeadName)
	if err != nil {
		return nil, err
	}
	if session == nil {
		inbox, err := createLeadAssignmentInboxMessage(ctx, st, workspace, assignment, "")
		if err != nil {
			return nil, err
		}
		result := pendingDelivery("", "lead has no orchestration session")
		if inbox != nil {
			result.InboxMessageID = inbox.InboxMessageID
		}
		return result, nil
	}
	d := delivererForSession(session)
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID}
	d.populate(result, session)
	if strings.TrimSpace(session.Metadata[MetadataDeliveryVersion]) == assignment.AssignmentVersion {
		result.State = DeliveryStateDelivered
		return result, nil
	}

	if d.hasRuntimeMetadata(session.Metadata) {
		if reason := d.unsupportedReason(session.Metadata); reason != "" {
			result.State = DeliveryStateUnsupported
			result.Reason = reason
			return result, nil
		}
	}
	inbox, err := createLeadAssignmentInboxMessage(ctx, st, workspace, assignment, session.SessionID)
	if err != nil {
		return nil, err
	}
	if inbox != nil {
		result.InboxMessageID = inbox.InboxMessageID
	}
	if !d.hasRuntimeMetadata(session.Metadata) {
		return recordPendingDelivery(ctx, st, workspace, session.SessionID, result, d.notReadyReason()), nil
	}
	if pendingReason := d.pendingReason(); pendingReason != "" {
		return recordPendingDelivery(ctx, st, workspace, session.SessionID, result, pendingReason), nil
	}
	return deliverNextLeadInboxMessage(ctx, st, workspace, assignment.LeadName, session.SessionID, d, result)
}

func DeliverLeadMessage(ctx context.Context, st store.Store, workspace, leadName, message string) (*DeliveryResult, error) {
	return DeliverLeadMessageWithOptions(ctx, st, workspace, leadName, message, LeadMessageDeliveryOptions{})
}

func DeliverLeadMessageWithOptions(ctx context.Context, st store.Store, workspace, leadName, message string, opts LeadMessageDeliveryOptions) (*DeliveryResult, error) {
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
	d := delivererForSession(session)
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID}
	d.populate(result, session)
	if d.hasRuntimeMetadata(session.Metadata) {
		if reason := d.unsupportedReason(session.Metadata); reason != "" {
			result.State = DeliveryStateUnsupported
			result.Reason = reason
			return result, nil
		}
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
	d = delivererForSession(session)
	d.populate(result, session)
	if !d.hasRuntimeMetadata(session.Metadata) {
		result.Reason = d.notReadyReason()
		_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, session.SessionID, result.Reason)
		return result, nil
	}
	if reason := d.unsupportedReason(session.Metadata); reason != "" {
		result.State = DeliveryStateUnsupported
		result.Reason = reason
		return result, nil
	}
	if pendingReason := d.pendingReason(); pendingReason != "" {
		result.Reason = pendingReason
		_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, session.SessionID, result.Reason)
		return result, nil
	}
	return deliverNextLeadInboxMessage(ctx, st, workspace, leadName, session.SessionID, d, result)
}

func DeliverPendingLeadMessages(ctx context.Context, st store.Store, workspace, leadName string) (*DeliveryResult, error) {
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
	d := delivererForSession(session)
	if !hasQueuedLeadInboxMessages(ctx, st, workspace, leadName, session.SessionID) {
		result := &DeliveryResult{State: DeliveryStateNone, SessionID: session.SessionID}
		d.populate(result, session)
		return result, nil
	}
	result := &DeliveryResult{State: DeliveryStatePending, SessionID: session.SessionID}
	d.populate(result, session)
	if !d.hasRuntimeMetadata(session.Metadata) {
		result.Reason = d.notReadyReason()
		return result, nil
	}
	if reason := d.unsupportedReason(session.Metadata); reason != "" {
		result.State = DeliveryStateUnsupported
		result.Reason = reason
		return result, nil
	}
	if pendingReason := d.pendingReason(); pendingReason != "" {
		result.Reason = pendingReason
		return result, nil
	}
	return deliverNextLeadInboxMessage(ctx, st, workspace, leadName, session.SessionID, d, result)
}

func deliverNextLeadInboxMessage(
	ctx context.Context,
	st store.Store,
	workspace string,
	leadName string,
	sessionID string,
	d leadTurnDeliverer,
	result *DeliveryResult,
) (*DeliveryResult, error) {
	if st == nil || st.AgentInboxMessages() == nil {
		result.Reason = "agent inbox store is not configured"
		return result, nil
	}
	msg, err := st.AgentInboxMessages().ClaimNext(ctx, store.AgentInboxMessageClaim{
		WorkspaceKey:  workspace,
		TargetAgentID: leadName,
		SessionID:     sessionID,
		ClaimedBy:     d.claimedBy(sessionID),
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
	closeReason := "lead message delivery complete"
	if isAssignmentInboxMessage(msg) {
		closeReason = "assignment delivery complete"
	}
	delivered, err := d.deliverTurn(ctx, st, workspace, sessionID, result, msg.Body, closeReason)
	if err != nil {
		return nil, err
	}
	if delivered.State != DeliveryStateDelivered {
		if delivered.Reason != "" {
			if isAssignmentInboxMessage(msg) {
				_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, delivered.Reason)
			} else {
				_ = MarkLeadMessageDeliveryAttempt(ctx, st, workspace, sessionID, delivered.Reason)
			}
		}
		if _, err := st.AgentInboxMessages().Complete(ctx, workspace, msg.InboxMessageID, store.AgentInboxMessageComplete{
			Outcome:    "retry",
			ErrorClass: d.provider() + "_delivery_pending",
			Error:      delivered.Reason,
		}); err != nil {
			return nil, err
		}
		return delivered, nil
	}
	if _, err := st.AgentInboxMessages().Complete(ctx, workspace, msg.InboxMessageID, store.AgentInboxMessageComplete{
		Outcome:           "delivered",
		DeliveredThreadID: d.deliveredThreadID(),
	}); err != nil {
		return nil, err
	}
	if epicID, version, ok := assignmentFromInboxMessage(msg); ok {
		if err := MarkAssignmentDelivered(ctx, st, workspace, sessionID, epicID, version); err != nil {
			return nil, err
		}
	}
	return delivered, nil
}

// drainLeadMessageQueue ticks until ctx is done, delivering queued inbox
// messages to the lead's controlled runtime. Run by the lead runtime process
// (codex and harness alike) so messages enqueued by other processes land in
// the visible session.
func drainLeadMessageQueue(ctx context.Context, st store.Store, workspace, leadName string, logger *slog.Logger) {
	if st == nil || strings.TrimSpace(workspace) == "" || strings.TrimSpace(leadName) == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(leadMessageDrainInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := DeliverPendingLeadMessages(ctx, st, workspace, leadName)
			if err != nil {
				logger.Debug("lead message queue drain failed", "err", err)
				continue
			}
			if result != nil && result.State == DeliveryStateDelivered {
				logger.Debug("lead message queue drained", "lead", leadName, "session", result.SessionID)
			}
		}
	}
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
		DedupeKey:         leadInboxDedupeKey(workspace, leadName, message, opts),
	})
}

func createLeadAssignmentInboxMessage(ctx context.Context, st store.Store, workspace string, assignment *epicrunner.LeadAssignmentContext, sessionID string) (*domain.AgentInboxMessage, error) {
	if assignment == nil {
		return nil, nil
	}
	message := formatLeadAssignmentTurn(assignment)
	return agentinbox.Enqueue(ctx, st, workspace, assignment.LeadName, message, agentinbox.MessageOptions{
		SessionID:  sessionID,
		SourceKind: assignmentInboxSourceKind,
		SourceRef:  assignmentInboxSourceRef(assignment),
		DedupeKey:  leadAssignmentInboxDedupeKey(workspace, assignment),
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

func leadInboxDedupeKey(workspace, leadName, message string, opts LeadMessageDeliveryOptions) string {
	return agentinbox.ContentDedupeKey(
		"agent-message",
		workspace,
		leadName,
		opts.SourceKind,
		opts.SourceRef,
		opts.DriverRunID,
		opts.TaskRunID,
		opts.TriggerEventID,
		opts.TriggerDeliveryID,
		message,
	)
}

func leadAssignmentInboxDedupeKey(workspace string, assignment *epicrunner.LeadAssignmentContext) string {
	if assignment == nil {
		return ""
	}
	return agentinbox.ContentDedupeKey("lead-assignment", workspace, assignment.LeadName, assignment.EpicID, assignment.AssignmentVersion)
}

func assignmentInboxSourceRef(assignment *epicrunner.LeadAssignmentContext) string {
	if assignment == nil {
		return assignmentInboxSourceRefPrefix
	}
	return assignmentInboxSourceRefPrefix + strings.TrimSpace(assignment.EpicID) + "/" + strings.TrimSpace(assignment.AssignmentVersion)
}

func isAssignmentInboxMessage(msg *domain.AgentInboxMessage) bool {
	return msg != nil && strings.TrimSpace(msg.SourceKind) == assignmentInboxSourceKind
}

func assignmentFromInboxMessage(msg *domain.AgentInboxMessage) (string, string, bool) {
	if !isAssignmentInboxMessage(msg) {
		return "", "", false
	}
	ref := strings.TrimSpace(msg.SourceRef)
	if !strings.HasPrefix(ref, assignmentInboxSourceRefPrefix) {
		return "", "", false
	}
	payload := strings.TrimPrefix(ref, assignmentInboxSourceRefPrefix)
	slash := strings.LastIndex(payload, "/")
	if slash <= 0 || slash == len(payload)-1 {
		return "", "", false
	}
	return payload[:slash], payload[slash+1:], true
}

func recordPendingDelivery(ctx context.Context, st store.Store, workspace, sessionID string, result *DeliveryResult, reason string) *DeliveryResult {
	result.Reason = reason
	_ = MarkAssignmentDeliveryAttempt(ctx, st, workspace, sessionID, reason)
	return result
}

func pendingDelivery(sessionID, reason string) *DeliveryResult {
	return &DeliveryResult{State: DeliveryStatePending, Reason: reason, SessionID: sessionID}
}

func formatLeadAssignmentTurn(assignment *epicrunner.LeadAssignmentContext) string {
	var b strings.Builder
	b.WriteString("Loom assigned this lead session an epic through backend state.\n\n")
	b.WriteString(epicrunner.FormatLeadAssignmentContext(assignment))
	b.WriteString("\n\nAcknowledge this backend assignment in the visible conversation. The UI/backend has already queued the epic-runner workflow for this assignment, so monitor the assigned epic and in-progress child tasks instead of starting another `loom epic run` from this terminal. Do not switch to a different epic unless the user explicitly asks.")
	return b.String()
}
