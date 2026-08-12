package leadcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
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
	// DedupeKey, when set, overrides the content-derived inbox dedupe key so
	// callers with a durable identity (e.g. outbox rows) stay idempotent
	// across redelivery.
	DedupeKey string
}

type RuntimeStore interface {
	store.OrchestrationSessionStore
	AgentInboxMessages() store.AgentInboxMessageStore
	AgentServices() store.AgentServiceStore
	WorkerProfiles() store.WorkerProfileStore
	Roles() store.RoleStore
}

// RuntimeDependencies names the exact persistence ports required by lead
// delivery. Composition must supply each port explicitly; the provider never
// receives the process-wide Store aggregate.
type RuntimeDependencies struct {
	Sessions       store.OrchestrationSessionStore
	InboxMessages  store.AgentInboxMessageStore
	AgentServices  store.AgentServiceStore
	WorkerProfiles store.WorkerProfileStore
	Roles          store.RoleStore
}

type runtimeDependencies struct {
	RuntimeDependencies
}

func (dependencies runtimeDependencies) AgentSessions() store.AgentSessionStore {
	return dependencies.Sessions.AgentSessions()
}

func (dependencies runtimeDependencies) AgentInboxMessages() store.AgentInboxMessageStore {
	return dependencies.InboxMessages
}

func (dependencies runtimeDependencies) AgentServices() store.AgentServiceStore {
	return dependencies.RuntimeDependencies.AgentServices
}

func (dependencies runtimeDependencies) WorkerProfiles() store.WorkerProfileStore {
	return dependencies.RuntimeDependencies.WorkerProfiles
}

func (dependencies runtimeDependencies) Roles() store.RoleStore {
	return dependencies.RuntimeDependencies.Roles
}

// InteractionChatDependencies is the provider-specific lead runtime surface
// consumed by Interaction's infrastructure adapter. It contains operations,
// not the process-wide persistence aggregate.
type InteractionChatDependencies struct {
	DeliverMessage func(
		context.Context,
		string,
		string,
		string,
		LeadMessageDeliveryOptions,
		interaction.InboxEnqueuer,
	) (*DeliveryResult, error)
	DeliverAssignment func(
		context.Context,
		string,
		string,
		interaction.InboxEnqueuer,
	) (*DeliveryResult, error)
	FindSession func(
		context.Context,
		string,
		string,
	) (*domain.AgentSession, error)
}

// NewInteractionChatDependencies binds the exact provider persistence ports
// to Interaction's operation-shaped adapter.
func NewInteractionChatDependencies(dependencies RuntimeDependencies) (InteractionChatDependencies, error) {
	if dependencies.Sessions == nil || dependencies.InboxMessages == nil || dependencies.AgentServices == nil ||
		dependencies.WorkerProfiles == nil || dependencies.Roles == nil {
		return InteractionChatDependencies{}, fmt.Errorf("compose lead delivery: all runtime persistence ports are required")
	}
	st := runtimeDependencies{RuntimeDependencies: dependencies}
	return InteractionChatDependencies{
		DeliverMessage: func(
			ctx context.Context,
			workspace, agentID, body string,
			options LeadMessageDeliveryOptions,
			inbox interaction.InboxEnqueuer,
		) (*DeliveryResult, error) {
			return DeliverLeadMessageWithOptions(
				ctx,
				st,
				workspace,
				agentID,
				body,
				options,
				inbox,
			)
		},
		DeliverAssignment: func(
			ctx context.Context,
			workspace, agentID string,
			inbox interaction.InboxEnqueuer,
		) (*DeliveryResult, error) {
			return DeliverCurrentAssignment(
				ctx,
				st,
				workspace,
				agentID,
				inbox,
			)
		},
		FindSession: func(
			ctx context.Context,
			workspace, agentID string,
		) (*domain.AgentSession, error) {
			return store.OrchestrationSessionFor(ctx, st, workspace, agentID)
		},
	}, nil
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
	// populate refreshes the deliverer's cached runtime view from the session
	// and mirrors it onto the result.
	populate(result *DeliveryResult, session *domain.AgentSession)
	deliveredThreadID() string
	deliverTurn(ctx context.Context, st RuntimeStore, runtime SessionRuntime, workspace, sessionID string,
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

func DeliverCurrentAssignment(
	ctx context.Context,
	st RuntimeStore,
	workspace,
	leadName string,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	return deliverCurrentAssignmentOwned(ctx, st, nil, workspace, leadName, inbox...)
}

//nolint:funlen // Assignment lookup, durable inbox publication, and runtime delivery are one ordered orchestration transition.
func deliverCurrentAssignmentOwned(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace, leadName string,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	enqueuer := firstInboxEnqueuer(runtime, inbox)
	assignment, err := epicrunner.LoadLeadAssignmentContext(
		ctx,
		epicrunner.NewStoreLeadAssignmentSource(st),
		workspace,
		leadName,
	)
	if err != nil || assignment == nil {
		return &DeliveryResult{State: DeliveryStateNone}, err
	}

	session, err := store.OrchestrationSessionFor(ctx, st, workspace, assignment.LeadName)
	if err != nil {
		return nil, err
	}
	if session == nil {
		inbox, err := createLeadAssignmentInboxMessage(ctx, enqueuer, workspace, assignment, "")
		if err != nil {
			return nil, err
		}
		result := pendingDelivery("", "lead has no orchestration session")
		if inbox != nil {
			result.InboxMessageID = inbox.MessageID
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
	inboxMessage, err := createLeadAssignmentInboxMessage(
		ctx, enqueuer, workspace, assignment, session.SessionID,
	)
	if err != nil {
		if runtime == nil {
			return nil, err
		}
	} else if inboxMessage != nil {
		result.InboxMessageID = inboxMessage.MessageID
	}
	if runtime == nil {
		result.Reason = "awaiting session-owned inbox claim"
		return result, nil
	}
	if !d.hasRuntimeMetadata(session.Metadata) {
		return recordPendingDelivery(ctx, runtime, workspace, session.SessionID, result, d.notReadyReason()), nil
	}
	if pendingReason := d.pendingReason(); pendingReason != "" {
		return recordPendingDelivery(ctx, runtime, workspace, session.SessionID, result, pendingReason), nil
	}
	return deliverNextLeadInboxMessage(
		ctx, st, runtime, workspace, assignment.LeadName, session.SessionID, d, result,
	)
}

func DeliverLeadMessage(
	ctx context.Context,
	st RuntimeStore,
	workspace,
	leadName,
	message string,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	return DeliverLeadMessageWithOptions(
		ctx, st, workspace, leadName, message, LeadMessageDeliveryOptions{}, inbox...,
	)
}

func DeliverLeadMessageWithOptions(
	ctx context.Context,
	st RuntimeStore,
	workspace,
	leadName,
	message string,
	opts LeadMessageDeliveryOptions,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	return deliverLeadMessageWithOptionsOwned(
		ctx, st, nil, workspace, leadName, message, opts, inbox...,
	)
}

//nolint:unparam // Tests inject fixed workspace/inbox coordinates through this owner-runtime seam.
func deliverLeadMessageOwned(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace, leadName, message string,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	return deliverLeadMessageWithOptionsOwned(
		ctx, st, runtime, workspace, leadName, message, LeadMessageDeliveryOptions{}, inbox...,
	)
}

//nolint:funlen // Message validation, durable enqueue, and controlled-runtime delivery share one ordered ownership boundary.
func deliverLeadMessageWithOptionsOwned(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace, leadName, message string,
	opts LeadMessageDeliveryOptions,
	inbox ...interaction.InboxEnqueuer,
) (*DeliveryResult, error) {
	enqueuer := firstInboxEnqueuer(runtime, inbox)
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
		return enqueueLeadMessageWithoutSession(
			ctx, enqueuer, workspace, leadName, message, opts,
		)
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
	inboxMessage, err := createLeadInboxMessage(
		ctx, enqueuer, workspace, leadName, session.SessionID, message, opts,
	)
	if err != nil {
		if runtime == nil {
			return nil, err
		}
	} else if inboxMessage != nil {
		result.InboxMessageID = inboxMessage.MessageID
	}
	if runtime == nil {
		result.Reason = "awaiting session-owned inbox claim"
		return result, nil
	}
	session, err = st.AgentSessions().Get(ctx, workspace, session.SessionID)
	if err != nil {
		return nil, err
	}
	d = delivererForSession(session)
	d.populate(result, session)
	if blocked := leadMessageDeliveryBlock(ctx, runtime, workspace, session, d, result); blocked != nil {
		return blocked, nil
	}
	return deliverNextLeadInboxMessage(
		ctx, st, runtime, workspace, leadName, session.SessionID, d, result,
	)
}

// enqueueLeadMessageWithoutSession queues a lead message when the lead has no
// orchestration session yet, reporting the enqueue as a pending delivery.
func enqueueLeadMessageWithoutSession(
	ctx context.Context,
	enqueuer interaction.InboxEnqueuer,
	workspace,
	leadName,
	message string,
	opts LeadMessageDeliveryOptions,
) (*DeliveryResult, error) {
	inbox, err := createLeadInboxMessage(
		ctx, enqueuer, workspace, leadName, "", message, opts,
	)
	if err != nil {
		return nil, err
	}
	result := pendingDelivery("", "lead has no orchestration session")
	if inbox != nil {
		result.InboxMessageID = inbox.MessageID
	}
	return result, nil
}

// leadMessageDeliveryBlock applies the runtime readiness gates for direct lead
// message delivery, returning the (mutated) result when delivery cannot
// proceed and nil when the runtime is ready for a turn.
func leadMessageDeliveryBlock(
	ctx context.Context,
	runtime SessionRuntime,
	workspace string,
	session *domain.AgentSession,
	d leadTurnDeliverer,
	result *DeliveryResult,
) *DeliveryResult {
	if !d.hasRuntimeMetadata(session.Metadata) {
		result.Reason = d.notReadyReason()
		_ = MarkLeadMessageDeliveryAttempt(ctx, runtime, workspace, session.SessionID, result.Reason)
		return result
	}
	if reason := d.unsupportedReason(session.Metadata); reason != "" {
		result.State = DeliveryStateUnsupported
		result.Reason = reason
		return result
	}
	if pendingReason := d.pendingReason(); pendingReason != "" {
		result.Reason = pendingReason
		_ = MarkLeadMessageDeliveryAttempt(ctx, runtime, workspace, session.SessionID, result.Reason)
		return result
	}
	return nil
}

func DeliverPendingLeadMessages(ctx context.Context, st RuntimeStore, workspace, leadName string) (*DeliveryResult, error) {
	return deliverPendingLeadMessagesOwned(ctx, st, nil, workspace, leadName)
}

func deliverPendingLeadMessagesOwned(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace, leadName string,
) (*DeliveryResult, error) {
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
	if runtime == nil {
		result.Reason = "session authority is required to claim inbox messages"
		return result, nil
	}
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
	return deliverNextLeadInboxMessage(ctx, st, runtime, workspace, leadName, session.SessionID, d, result)
}

func deliverNextLeadInboxMessage(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace string,
	leadName string,
	sessionID string,
	d leadTurnDeliverer,
	result *DeliveryResult,
) (*DeliveryResult, error) {
	if runtime == nil {
		result.Reason = "session authority is required to claim inbox messages"
		return result, nil
	}
	msg, err := runtime.ClaimNextInbox(ctx, interaction.ClaimInboxCommand{
		WorkspaceKey: workspace,
		AgentID:      leadName,
		SessionID:    sessionID,
		LeaseTTL:     2 * time.Minute,
	})
	if errors.Is(err, domain.ErrNotFound) {
		result.State = DeliveryStateNone
		result.Reason = ""
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.InboxMessageID = msg.MessageID
	closeReason := "lead message delivery complete"
	if isAssignmentInboxMessage(msg) {
		closeReason = "assignment delivery complete"
	}
	delivered, err := d.deliverTurn(ctx, st, runtime, workspace, sessionID, result, msg.Body, closeReason)
	if err != nil {
		return nil, err
	}
	if delivered.State != DeliveryStateDelivered {
		return completeLeadInboxRetry(ctx, runtime, workspace, sessionID, d, msg, delivered)
	}
	return completeLeadInboxDelivered(ctx, runtime, workspace, sessionID, d, msg, delivered)
}

// completeLeadInboxRetry records the delivery attempt and returns the claimed
// inbox message to the queue for retry after a non-delivered turn.
func completeLeadInboxRetry(
	ctx context.Context,
	runtime SessionRuntime,
	workspace string,
	sessionID string,
	d leadTurnDeliverer,
	msg *interaction.InboxMessage,
	delivered *DeliveryResult,
) (*DeliveryResult, error) {
	if delivered.Reason != "" {
		if isAssignmentInboxMessage(msg) {
			_ = MarkAssignmentDeliveryAttempt(ctx, runtime, workspace, sessionID, delivered.Reason)
		} else {
			_ = MarkLeadMessageDeliveryAttempt(ctx, runtime, workspace, sessionID, delivered.Reason)
		}
	}
	if err := runtime.CompleteInbox(ctx, interaction.CompleteInboxCommand{
		WorkspaceKey: workspace,
		SessionID:    sessionID,
		MessageID:    msg.MessageID,
		Attempt:      msg.Attempt,
		Status:       interaction.InboxQueued,
		ErrorClass:   d.provider() + "_delivery_pending",
	}); err != nil {
		return nil, err
	}
	return delivered, nil
}

// completeLeadInboxDelivered finalizes a delivered inbox message and, for
// assignment messages, marks the assignment delivered on the session.
func completeLeadInboxDelivered(
	ctx context.Context,
	runtime SessionRuntime,
	workspace string,
	sessionID string,
	d leadTurnDeliverer,
	msg *interaction.InboxMessage,
	delivered *DeliveryResult,
) (*DeliveryResult, error) {
	if err := runtime.CompleteInbox(ctx, interaction.CompleteInboxCommand{
		WorkspaceKey:      workspace,
		SessionID:         sessionID,
		MessageID:         msg.MessageID,
		Attempt:           msg.Attempt,
		Status:            interaction.InboxDelivered,
		DeliveredThreadID: d.deliveredThreadID(),
	}); err != nil {
		return nil, err
	}
	if epicID, version, ok := assignmentFromInboxMessage(msg); ok {
		if err := MarkAssignmentDelivered(ctx, runtime, workspace, sessionID, epicID, version); err != nil {
			return nil, err
		}
	}
	return delivered, nil
}

// drainLeadMessageQueue ticks until ctx is done, delivering queued inbox
// messages to the lead's controlled runtime. Run by the lead runtime process
// (codex and harness alike) so messages enqueued by other processes land in
// the visible session.
func drainLeadMessageQueue(
	ctx context.Context,
	st RuntimeStore,
	runtime SessionRuntime,
	workspace, leadName string,
	logger *slog.Logger,
) {
	if st == nil || runtime == nil ||
		strings.TrimSpace(workspace) == "" || strings.TrimSpace(leadName) == "" {
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
			result, err := deliverPendingLeadMessagesOwned(ctx, st, runtime, workspace, leadName)
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

func createLeadInboxMessage(
	ctx context.Context,
	enqueuer interaction.InboxEnqueuer,
	workspace,
	leadName,
	sessionID,
	message string,
	opts LeadMessageDeliveryOptions,
) (*interaction.InboxMessage, error) {
	if opts.SourceKind == "" {
		opts.SourceKind = "workflow"
	}
	if opts.SourceRef == "" && opts.DriverRunID != "" {
		opts.SourceRef = "driver-run://" + opts.DriverRunID
	}
	dedupeKey := strings.TrimSpace(opts.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = leadInboxDedupeKey(workspace, leadName, message, opts)
	}
	return interaction.EnqueueGenerated(ctx, enqueuer, interaction.EnqueueInboxCommand{
		WorkspaceKey:      workspace,
		TargetAgentID:     leadName,
		Body:              message,
		SessionID:         sessionID,
		SourceKind:        opts.SourceKind,
		SourceRef:         opts.SourceRef,
		DriverRunID:       opts.DriverRunID,
		TaskRunID:         opts.TaskRunID,
		TriggerEventID:    opts.TriggerEventID,
		TriggerDeliveryID: opts.TriggerDeliveryID,
		DedupeKey:         dedupeKey,
	})
}

func createLeadAssignmentInboxMessage(
	ctx context.Context,
	enqueuer interaction.InboxEnqueuer,
	workspace string,
	assignment *epicrunner.LeadAssignmentContext,
	sessionID string,
) (*interaction.InboxMessage, error) {
	if assignment == nil {
		return nil, nil
	}
	message := formatLeadAssignmentTurn(assignment)
	return interaction.EnqueueGenerated(ctx, enqueuer, interaction.EnqueueInboxCommand{
		WorkspaceKey:  workspace,
		TargetAgentID: assignment.LeadName,
		Body:          message,
		SessionID:     sessionID,
		SourceKind:    assignmentInboxSourceKind,
		SourceRef:     assignmentInboxSourceRef(assignment),
		DedupeKey:     leadAssignmentInboxDedupeKey(workspace, assignment),
	})
}

func firstInboxEnqueuer(
	runtime SessionRuntime,
	values []interaction.InboxEnqueuer,
) interaction.InboxEnqueuer {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	value, _ := runtime.(interaction.InboxEnqueuer)
	return value
}

func hasQueuedLeadInboxMessages(ctx context.Context, st RuntimeStore, workspace, leadName, sessionID string) bool {
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
	return interaction.ContentDedupeKey(
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
	return interaction.ContentDedupeKey("lead-assignment", workspace, assignment.LeadName, assignment.EpicID, assignment.AssignmentVersion)
}

func assignmentInboxSourceRef(assignment *epicrunner.LeadAssignmentContext) string {
	if assignment == nil {
		return assignmentInboxSourceRefPrefix
	}
	return assignmentInboxSourceRefPrefix + strings.TrimSpace(assignment.EpicID) + "/" + strings.TrimSpace(assignment.AssignmentVersion)
}

func isAssignmentInboxMessage(msg *interaction.InboxMessage) bool {
	return msg != nil && strings.TrimSpace(msg.SourceKind) == assignmentInboxSourceKind
}

func assignmentFromInboxMessage(msg *interaction.InboxMessage) (string, string, bool) {
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

func recordPendingDelivery(
	ctx context.Context,
	runtime SessionRuntime,
	workspace, sessionID string,
	result *DeliveryResult,
	reason string,
) *DeliveryResult {
	result.Reason = reason
	_ = MarkAssignmentDeliveryAttempt(ctx, runtime, workspace, sessionID, reason)
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
