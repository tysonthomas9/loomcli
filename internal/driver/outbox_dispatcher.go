package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type (
	// ChatMessenger is the Interaction port consumed by agent-message delivery.
	ChatMessenger = interaction.ChatMessenger
	// ChatDelivery is Interaction's stable delivery result.
	ChatDelivery = interaction.ChatDelivery
)

// AgentMessageDeliveryResult reports how a workflow-originated message reached
// an agent: live delivery into a controlled lead runtime, or queued into the
// agent inbox.
type AgentMessageDeliveryResult struct {
	AgentName       string `json:"agentName"`
	State           string `json:"state"`
	Reason          string `json:"reason,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	InboxMessageID  string `json:"inboxMessageId,omitempty"`
	RuntimeProvider string `json:"runtimeProvider,omitempty"`
	RuntimeStatus   string `json:"runtimeStatus,omitempty"`
	Controlled      bool   `json:"controlled,omitempty"`
}

const (
	// AgentMessageDeliveryStateDelivered is a terminal successful delivery.
	AgentMessageDeliveryStateDelivered = string(interaction.ChatDeliveryDelivered)
	// AgentMessageDeliveryStateUnsupported is a terminal unsupported delivery.
	AgentMessageDeliveryStateUnsupported = string(interaction.ChatDeliveryUnsupported)
)

// AgentMessageDeliveryOptions carries optional delivery metadata for
// DeliverAgentMessageForDriverWithOptions. DedupeKey, when set, overrides the
// inbox-side dedupe key so redelivery (e.g. the outbox dispatcher retrying)
// never enqueues the same message twice.
type AgentMessageDeliveryOptions struct {
	TaskRunID string
	DedupeKey string
}

// DeliverAgentMessageForDriver routes a driver-run message to an agent:
// controlled lead agents get live lead-control delivery, everything else is
// queued into the agent inbox. Shared by the driver CLI deliver-agent-message
// subcommand and the driver-op HTTP API.
func DeliverAgentMessageForDriver(
	ctx context.Context,
	messenger interaction.ChatMessenger,
	workspace,
	driverRunID,
	agentName,
	message string,
) (AgentMessageDeliveryResult, error) {
	return DeliverAgentMessageForDriverWithOptions(
		ctx, messenger, workspace, driverRunID, agentName, message,
		AgentMessageDeliveryOptions{},
	)
}

// DeliverLeadAssignmentForDriver delivers the lead's current assignment and
// converts the Interaction result into the driver transport's stable shape.
func DeliverLeadAssignmentForDriver(
	ctx context.Context,
	messenger interaction.ChatMessenger,
	workspace,
	leadName string,
) (AgentMessageDeliveryResult, error) {
	if messenger == nil {
		return AgentMessageDeliveryResult{}, fmt.Errorf(
			"interaction chat messenger is required: %w",
			interaction.ErrUnavailable,
		)
	}
	delivery, err := messenger.DeliverAssignment(
		ctx,
		interaction.DeliverAssignmentCommand{
			WorkspaceKey: strings.TrimSpace(workspace),
			AgentID:      strings.TrimSpace(leadName),
		},
	)
	if err != nil {
		return AgentMessageDeliveryResult{}, err
	}
	return NewAgentMessageDeliveryResult(leadName, delivery), nil
}

// DeliverAgentMessageForDriverWithOptions is DeliverAgentMessageForDriver
// with explicit delivery options; the outbox dispatcher uses it to forward
// the row's DedupeKey into the agent inbox.
func DeliverAgentMessageForDriverWithOptions(
	ctx context.Context,
	messenger interaction.ChatMessenger,
	workspace,
	driverRunID,
	agentName,
	message string,
	opts AgentMessageDeliveryOptions,
) (AgentMessageDeliveryResult, error) {
	workspace = strings.TrimSpace(workspace)
	driverRunID = strings.TrimSpace(driverRunID)
	agentName = strings.TrimSpace(agentName)
	message = strings.TrimSpace(message)
	dedupeKey := strings.TrimSpace(opts.DedupeKey)
	if dedupeKey == "" {
		dedupeKey = interaction.ContentDedupeKey(
			"driver-message",
			workspace,
			driverRunID,
			strings.TrimSpace(opts.TaskRunID),
			agentName,
			message,
		)
	}
	if messenger == nil {
		return AgentMessageDeliveryResult{}, fmt.Errorf(
			"interaction chat messenger is required: %w",
			interaction.ErrUnavailable,
		)
	}
	delivery, err := messenger.DeliverChatMessage(
		ctx,
		interaction.DeliverChatMessageCommand{
			WorkspaceKey: workspace,
			AgentID:      agentName,
			Body:         message,
			SourceKind:   "workflow",
			SourceRef:    "driver-run://" + driverRunID,
			DriverRunID:  driverRunID,
			TaskRunID:    strings.TrimSpace(opts.TaskRunID),
			DedupeKey:    dedupeKey,
		},
	)
	if err != nil {
		return AgentMessageDeliveryResult{}, err
	}
	return NewAgentMessageDeliveryResult(agentName, delivery), nil
}

// NewAgentMessageDeliveryResult converts an Interaction chat delivery into the
// driver-facing result shape.
func NewAgentMessageDeliveryResult(
	agentName string,
	delivery *interaction.ChatDelivery,
) AgentMessageDeliveryResult {
	result := AgentMessageDeliveryResult{
		AgentName: agentName,
		State:     string(interaction.ChatDeliveryNone),
	}
	if delivery != nil {
		result.State = string(delivery.State)
		result.Reason = delivery.Reason
		result.SessionID = delivery.SessionID
		result.InboxMessageID = delivery.InboxMessageID
		result.RuntimeProvider = delivery.Provider
		result.RuntimeStatus = delivery.RuntimeStatus
		result.Controlled = delivery.Controlled
	}
	return result
}

const (
	// defaultOutboxBatchLimit caps how many due rows one RunOnce pass
	// drains per workspace.
	defaultOutboxBatchLimit = 50

	// maxOutboxRetryDelay caps the exponential retry backoff. Per the
	// always-on dispatcher decision, lead deliveries retry unbounded with
	// this capped backoff.
	maxOutboxRetryDelay = 30 * time.Second
)

// OutboxDispatcher is the server-side delivery loop for OutboxRecords. It
// replaces epic-runner's workflow-side startLeadDeliveryRetry and
// startLeadMessageDeliveryRetry loops: task_events.go (and the
// deliver-lead-assignment driver op) create rows, and this dispatcher drains
// due rows, attempts delivery, and records the outcome with retry backoff.
// Like owner-fenced stale TaskRun recovery, it is always-on policy: loom serve
// runs it whenever
// it has a store, independent of LOOM_DRIVER_EXECUTOR.
type OutboxDispatcher struct {
	Delivery    execution.OutboxDeliveryAPI
	Authorities execution.SystemAuthorityResolver
	Workspaces  WorkspaceLister
	Chat        interaction.ChatMessenger
	// WorkspaceKey scopes dispatch to one workspace. Empty dispatches every
	// workspace returned by Store.Workspaces().List.
	WorkspaceKey string
	// BatchLimit bounds ListDue per workspace per pass. Zero or negative
	// falls back to defaultOutboxBatchLimit.
	BatchLimit int
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time

	// deliverAssignment and deliverTaskMessage are delivery seams for tests;
	// nil enters Interaction through ChatMessenger.
	deliverAssignment func(
		context.Context,
		interaction.ChatMessenger,
		string,
		string,
	) (*interaction.ChatDelivery, error)
	deliverTaskMessage func(
		context.Context,
		interaction.ChatMessenger,
		string,
		string,
		string,
		string,
		AgentMessageDeliveryOptions,
	) (AgentMessageDeliveryResult, error)
}

// WorkspaceLister is the read-only workspace directory consumed when the
// dispatcher is not scoped to one workspace.
type WorkspaceLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

// outboxAttempt is the provider-agnostic view of one delivery attempt that
// the dispatcher maps onto an OutboxDeliveryUpdate.
type outboxAttempt struct {
	State          string
	Reason         string
	InboxMessageID string
}

// RunOnce drains one batch of due outbox rows per target workspace and
// returns how many rows reached delivered status.
func (d *OutboxDispatcher) RunOnce(ctx context.Context) (int, error) {
	if d == nil || d.Delivery == nil || d.Authorities == nil {
		return 0, fmt.Errorf("execution outbox delivery API required: %w", persistence.ErrInvalid)
	}
	workspaces, err := d.workspaceKeys(ctx)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, ws := range workspaces {
		auth, err := d.Authorities.ResolveExecutionSystemAuthority(
			ctx, ws, execution.ActionListDueOutboxDeliveries, string(execution.OutboxDeliveryComponentID),
		)
		if err != nil {
			return delivered, fmt.Errorf("resolve outbox delivery authority in workspace %q: %w", ws, err)
		}
		rows, err := d.Delivery.ListDueOutboxDeliveries(ctx, auth, execution.ListDueOutboxDeliveriesCommand{
			WorkspaceKey: ws,
			Now:          d.now(),
			Limit:        d.batchLimit(),
		})
		if err != nil {
			return delivered, fmt.Errorf("list due outbox rows in workspace %q: %w", ws, err)
		}
		for _, row := range rows {
			update := d.attemptDelivery(ctx, row)
			recordAuth, err := d.Authorities.ResolveExecutionSystemAuthority(
				ctx, ws, execution.ActionRecordOutboxDeliveryResult, string(execution.OutboxDeliveryComponentID),
			)
			if err != nil {
				return delivered, fmt.Errorf("resolve outbox result authority in workspace %q: %w", ws, err)
			}
			if _, err := d.Delivery.RecordOutboxDeliveryResult(ctx, recordAuth, update); err != nil {
				return delivered, fmt.Errorf("mark outbox result for %q: %w", row.OutboxID, err)
			}
			if update.Status == execution.OutboxDeliveryStatusDelivered {
				delivered++
			}
		}
	}
	return delivered, nil
}

// attemptDelivery performs one delivery attempt for a row and maps the
// outcome onto the stored result: delivered/queued (or any inbox message
// created) is delivered, unsupported is terminal, and everything else stays
// pending with capped exponential backoff.
func (d *OutboxDispatcher) attemptDelivery(ctx context.Context, row execution.OutboxDelivery) execution.RecordOutboxDeliveryResultCommand {
	attempt := row.Attempt + 1
	result, err := d.deliverRow(ctx, row)
	if err != nil {
		return d.retryUpdate(row, attempt, err.Error())
	}
	switch {
	case result.State == string(interaction.ChatDeliveryDelivered),
		result.State == "queued",
		result.InboxMessageID != "":
		return execution.RecordOutboxDeliveryResultCommand{
			WorkspaceKey: row.WorkspaceKey, OutboxID: row.OutboxID,
			Status:         execution.OutboxDeliveryStatusDelivered,
			Attempt:        attempt,
			InboxMessageID: result.InboxMessageID,
		}
	case result.State == string(interaction.ChatDeliveryUnsupported):
		return execution.RecordOutboxDeliveryResultCommand{
			WorkspaceKey: row.WorkspaceKey, OutboxID: row.OutboxID,
			Status:    execution.OutboxDeliveryStatusUnsupported,
			Attempt:   attempt,
			LastError: result.Reason,
		}
	default:
		reason := result.Reason
		if reason == "" {
			reason = "delivery state " + result.State
		}
		return d.retryUpdate(row, attempt, reason)
	}
}

// deliverRow routes one row to its kind-specific delivery path.
func (d *OutboxDispatcher) deliverRow(ctx context.Context, row execution.OutboxDelivery) (outboxAttempt, error) {
	switch row.Kind {
	case execution.OutboxKindLeadAssignment:
		delivery, err := d.assignmentDeliverer()(
			ctx,
			d.Chat,
			row.WorkspaceKey,
			row.TargetAgent,
		)
		if err != nil {
			return outboxAttempt{}, err
		}
		if delivery == nil {
			return outboxAttempt{
				State: string(interaction.ChatDeliveryNone),
			}, nil
		}
		return outboxAttempt{
			State:          string(delivery.State),
			Reason:         delivery.Reason,
			InboxMessageID: delivery.InboxMessageID,
		}, nil
	case execution.OutboxKindLeadTaskMessage:
		result, err := d.taskMessageDeliverer()(
			ctx,
			d.Chat,
			row.WorkspaceKey,
			row.DriverRunID,
			row.TargetAgent,
			row.Body,
			AgentMessageDeliveryOptions{
				TaskRunID: row.TaskRunID,
				DedupeKey: row.DedupeKey,
			},
		)
		if err != nil {
			return outboxAttempt{}, err
		}
		return outboxAttempt{
			State:          result.State,
			Reason:         result.Reason,
			InboxMessageID: result.InboxMessageID,
		}, nil
	default:
		// Unknown kinds are terminal: retrying can never succeed.
		return outboxAttempt{
			State:  string(interaction.ChatDeliveryUnsupported),
			Reason: fmt.Sprintf("unknown outbox kind %q", row.Kind),
		}, nil
	}
}

// retryUpdate keeps a row pending with the next capped-backoff retry time.
func (d *OutboxDispatcher) retryUpdate(row execution.OutboxDelivery, attempt int, lastError string) execution.RecordOutboxDeliveryResultCommand {
	next := d.now().Add(outboxRetryDelay(attempt))
	return execution.RecordOutboxDeliveryResultCommand{
		WorkspaceKey: row.WorkspaceKey,
		OutboxID:     row.OutboxID,
		Status:       execution.OutboxDeliveryStatusPending,
		Attempt:      attempt,
		NextRetryAt:  &next,
		LastError:    lastError,
	}
}

// outboxRetryDelay is min(maxOutboxRetryDelay, 1s<<attempt) where attempt is
// the just-recorded attempt count (first retry waits 2s, then 4s, 8s, 16s,
// capped at 30s).
func outboxRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 5 {
		return maxOutboxRetryDelay
	}
	delay := time.Second << attempt
	if delay > maxOutboxRetryDelay {
		return maxOutboxRetryDelay
	}
	return delay
}

// workspaceKeys resolves the dispatch targets: the configured workspace, or
// every known workspace when unscoped (mirrors Executor.RecoverStaleOnce).
func (d *OutboxDispatcher) workspaceKeys(ctx context.Context) ([]string, error) {
	if d.WorkspaceKey != "" {
		return []string{d.WorkspaceKey}, nil
	}
	if d.Workspaces == nil {
		return nil, fmt.Errorf("workspace lister required: %w", persistence.ErrInvalid)
	}
	keys, err := d.Workspaces.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for outbox dispatch: %w", err)
	}
	return append([]string(nil), keys...), nil
}

func (d *OutboxDispatcher) assignmentDeliverer() func(
	context.Context,
	interaction.ChatMessenger,
	string,
	string,
) (*interaction.ChatDelivery, error) {
	if d.deliverAssignment != nil {
		return d.deliverAssignment
	}
	return func(
		ctx context.Context,
		messenger interaction.ChatMessenger,
		workspace,
		leadName string,
	) (*interaction.ChatDelivery, error) {
		if messenger == nil {
			return nil, interaction.ErrUnavailable
		}
		return messenger.DeliverAssignment(
			ctx,
			interaction.DeliverAssignmentCommand{
				WorkspaceKey: workspace,
				AgentID:      leadName,
			},
		)
	}
}

func (d *OutboxDispatcher) taskMessageDeliverer() func(
	context.Context,
	interaction.ChatMessenger,
	string,
	string,
	string,
	string,
	AgentMessageDeliveryOptions,
) (AgentMessageDeliveryResult, error) {
	if d.deliverTaskMessage != nil {
		return d.deliverTaskMessage
	}
	return DeliverAgentMessageForDriverWithOptions
}

func (d *OutboxDispatcher) batchLimit() int {
	if d.BatchLimit > 0 {
		return d.BatchLimit
	}
	return defaultOutboxBatchLimit
}

func (d *OutboxDispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}
