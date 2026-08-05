package driver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadcontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	AgentMessageDeliveryStateDelivered = string(leadcontrol.DeliveryStateDelivered)
	// AgentMessageDeliveryStateUnsupported is a terminal unsupported delivery.
	AgentMessageDeliveryStateUnsupported = string(leadcontrol.DeliveryStateUnsupported)
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
func DeliverAgentMessageForDriver(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string) (AgentMessageDeliveryResult, error) {
	return DeliverAgentMessageForDriverWithOptions(ctx, st, workspace, driverRunID, agentName, message, AgentMessageDeliveryOptions{})
}

// DeliverLeadAssignmentForDriver delivers the lead's current assignment and
// converts the lead-control result into the driver transport's stable shape.
func DeliverLeadAssignmentForDriver(ctx context.Context, st store.Store, workspace, leadName string) (AgentMessageDeliveryResult, error) {
	delivery, err := leadcontrol.DeliverCurrentAssignment(ctx, st, workspace, leadName)
	if err != nil {
		return AgentMessageDeliveryResult{}, err
	}
	return NewAgentMessageDeliveryResult(leadName, delivery), nil
}

// DeliverAgentMessageForDriverWithOptions is DeliverAgentMessageForDriver
// with explicit delivery options; the outbox dispatcher uses it to forward
// the row's DedupeKey into the agent inbox.
func DeliverAgentMessageForDriverWithOptions(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string, opts AgentMessageDeliveryOptions) (AgentMessageDeliveryResult, error) {
	agent, err := st.Agents().Get(ctx, workspace, agentName)
	if err != nil {
		return AgentMessageDeliveryResult{}, fmt.Errorf("get target agent: %w", err)
	}
	if isControlledLeadAgent(agent) {
		delivery, err := leadcontrol.DeliverLeadMessageWithOptions(ctx, st, workspace, agentName, message, leadcontrol.LeadMessageDeliveryOptions{
			SourceKind:  "workflow",
			DriverRunID: driverRunID,
			TaskRunID:   opts.TaskRunID,
			DedupeKey:   opts.DedupeKey,
		})
		if err != nil {
			return AgentMessageDeliveryResult{}, err
		}
		return NewAgentMessageDeliveryResult(agentName, delivery), nil
	}
	msg, err := agentinbox.Enqueue(ctx, st, workspace, agentName, message, agentinbox.MessageOptions{
		SourceKind:  "workflow",
		SourceRef:   "driver-run://" + strings.TrimSpace(driverRunID),
		DriverRunID: driverRunID,
		TaskRunID:   opts.TaskRunID,
		DedupeKey:   opts.DedupeKey,
	})
	if err != nil {
		return AgentMessageDeliveryResult{}, err
	}
	return AgentMessageDeliveryResult{
		AgentName:      agentName,
		State:          "queued",
		Reason:         "agent message queued; no runtime delivery adapter is configured",
		SessionID:      msg.SessionID,
		InboxMessageID: msg.InboxMessageID,
	}, nil
}

func isControlledLeadAgent(agent *domain.Agent) bool {
	if agent == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(agent.RoleName), "lead") && leadcontrol.IsControlledLeadBackend(agent.Backend)
}

// NewAgentMessageDeliveryResult converts a lead-control delivery into the
// driver-facing result shape.
func NewAgentMessageDeliveryResult(agentName string, delivery *leadcontrol.DeliveryResult) AgentMessageDeliveryResult {
	result := AgentMessageDeliveryResult{
		AgentName: agentName,
		State:     string(leadcontrol.DeliveryStateNone),
	}
	if delivery != nil {
		result.State = string(delivery.State)
		result.Reason = delivery.Reason
		result.SessionID = delivery.SessionID
		result.InboxMessageID = delivery.InboxMessageID
		result.RuntimeProvider = delivery.Provider
		if delivery.Provider != "" && delivery.Provider != leadcontrol.RuntimeProviderCodex {
			result.RuntimeStatus = delivery.HarnessRuntime.Status
			result.Controlled = delivery.HarnessRuntime.Controlled
		} else {
			result.RuntimeStatus = delivery.Runtime.Status
			result.Controlled = delivery.Runtime.Controlled
		}
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
// Like StaleTaskSweeper it is always-on policy: loom serve runs it whenever
// it has a store, independent of LOOM_DRIVER_EXECUTOR.
type OutboxDispatcher struct {
	Store store.Store
	// WorkspaceKey scopes dispatch to one workspace. Empty dispatches every
	// workspace returned by Store.Workspaces().List.
	WorkspaceKey string
	// BatchLimit bounds ListDue per workspace per pass. Zero or negative
	// falls back to defaultOutboxBatchLimit.
	BatchLimit int
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time

	// deliverAssignment and deliverTaskMessage are delivery seams for tests;
	// nil uses leadcontrol.DeliverCurrentAssignment and
	// DeliverAgentMessageForDriverWithOptions respectively.
	deliverAssignment  func(ctx context.Context, st store.Store, workspace, leadName string) (*leadcontrol.DeliveryResult, error)
	deliverTaskMessage func(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string, opts AgentMessageDeliveryOptions) (AgentMessageDeliveryResult, error)
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
	if d == nil || d.Store == nil {
		return 0, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaces, err := d.workspaceKeys(ctx)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, ws := range workspaces {
		rows, err := d.Store.Outbox().ListDue(ctx, ws, store.OutboxDueFilter{
			Now:   d.now(),
			Limit: d.batchLimit(),
		})
		if err != nil {
			return delivered, fmt.Errorf("list due outbox rows in workspace %q: %w", ws, err)
		}
		for _, row := range rows {
			if row == nil {
				continue
			}
			update := d.attemptDelivery(ctx, row)
			if _, err := d.Store.Outbox().MarkResult(ctx, ws, row.OutboxID, update); err != nil {
				return delivered, fmt.Errorf("mark outbox result for %q: %w", row.OutboxID, err)
			}
			if update.Status == domain.OutboxStatusDelivered {
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
func (d *OutboxDispatcher) attemptDelivery(ctx context.Context, row *domain.OutboxRecord) store.OutboxDeliveryUpdate {
	attempt := row.Attempt + 1
	result, err := d.deliverRow(ctx, row)
	if err != nil {
		return d.retryUpdate(attempt, err.Error())
	}
	switch {
	case result.State == string(leadcontrol.DeliveryStateDelivered),
		result.State == "queued",
		result.InboxMessageID != "":
		return store.OutboxDeliveryUpdate{
			Status:         domain.OutboxStatusDelivered,
			Attempt:        attempt,
			InboxMessageID: result.InboxMessageID,
		}
	case result.State == string(leadcontrol.DeliveryStateUnsupported):
		return store.OutboxDeliveryUpdate{
			Status:    domain.OutboxStatusUnsupported,
			Attempt:   attempt,
			LastError: result.Reason,
		}
	default:
		reason := result.Reason
		if reason == "" {
			reason = "delivery state " + result.State
		}
		return d.retryUpdate(attempt, reason)
	}
}

// deliverRow routes one row to its kind-specific delivery path.
func (d *OutboxDispatcher) deliverRow(ctx context.Context, row *domain.OutboxRecord) (outboxAttempt, error) {
	switch row.Kind {
	case domain.OutboxKindLeadAssignment:
		delivery, err := d.assignmentDeliverer()(ctx, d.Store, row.WorkspaceKey, row.TargetAgent)
		if err != nil {
			return outboxAttempt{}, err
		}
		if delivery == nil {
			return outboxAttempt{State: string(leadcontrol.DeliveryStateNone)}, nil
		}
		return outboxAttempt{
			State:          string(delivery.State),
			Reason:         delivery.Reason,
			InboxMessageID: delivery.InboxMessageID,
		}, nil
	case domain.OutboxKindLeadTaskMessage:
		result, err := d.taskMessageDeliverer()(ctx, d.Store, row.WorkspaceKey, row.DriverRunID, row.TargetAgent, row.Body, AgentMessageDeliveryOptions{
			TaskRunID: row.TaskRunID,
			DedupeKey: row.DedupeKey,
		})
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
			State:  string(leadcontrol.DeliveryStateUnsupported),
			Reason: fmt.Sprintf("unknown outbox kind %q", row.Kind),
		}, nil
	}
}

// retryUpdate keeps a row pending with the next capped-backoff retry time.
func (d *OutboxDispatcher) retryUpdate(attempt int, lastError string) store.OutboxDeliveryUpdate {
	next := d.now().Add(outboxRetryDelay(attempt))
	return store.OutboxDeliveryUpdate{
		Status:      domain.OutboxStatusPending,
		Attempt:     attempt,
		NextRetryAt: &next,
		LastError:   lastError,
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
// every known workspace when unscoped (mirrors StaleTaskSweeper).
func (d *OutboxDispatcher) workspaceKeys(ctx context.Context) ([]string, error) {
	return resolveSweepWorkspaces(ctx, d.Store, d.WorkspaceKey, "outbox dispatch")
}

func (d *OutboxDispatcher) assignmentDeliverer() func(ctx context.Context, st store.Store, workspace, leadName string) (*leadcontrol.DeliveryResult, error) {
	if d.deliverAssignment != nil {
		return d.deliverAssignment
	}
	return leadcontrol.DeliverCurrentAssignment
}

func (d *OutboxDispatcher) taskMessageDeliverer() func(ctx context.Context, st store.Store, workspace, driverRunID, agentName, message string, opts AgentMessageDeliveryOptions) (AgentMessageDeliveryResult, error) {
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
