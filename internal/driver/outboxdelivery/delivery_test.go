package outboxdelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const outboxTestWorkspace = "WS"

func createOutboxRow(t *testing.T, ctx context.Context, st store.Store, in store.OutboxCreate) *domain.OutboxRecord {
	t.Helper()
	if in.WorkspaceKey == "" {
		in.WorkspaceKey = outboxTestWorkspace
	}
	row, err := st.Outbox().Create(ctx, in)
	if err != nil {
		t.Fatalf("Outbox Create: %v", err)
	}
	return row
}

func getOutboxRow(t *testing.T, ctx context.Context, st store.Store, outboxID string) *domain.OutboxRecord {
	t.Helper()
	row, err := st.Outbox().Get(ctx, outboxTestWorkspace, outboxID)
	if err != nil {
		t.Fatalf("Outbox Get %q: %v", outboxID, err)
	}
	return row
}

func TestOutboxDispatcherRunOnce(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		create        store.OutboxCreate
		assignment    *interaction.ChatDelivery
		assignmentErr error
		message       AgentMessageDeliveryResult
		messageErr    error
		wantDelivered int
		wantStatus    domain.OutboxStatus
		wantAttempt   int
		wantInboxID   string
		wantRetryAt   bool
		wantLastError string
	}{
		{
			name:          "lead assignment delivered",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadAssignment, TargetAgent: "lead-1"},
			assignment:    &interaction.ChatDelivery{State: interaction.ChatDeliveryDelivered},
			wantDelivered: 1,
			wantStatus:    domain.OutboxStatusDelivered,
			wantAttempt:   1,
		},
		{
			name:          "lead assignment pending with inbox message counts as delivered",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadAssignment, TargetAgent: "lead-1"},
			assignment:    &interaction.ChatDelivery{State: interaction.ChatDeliveryPending, InboxMessageID: "inbox-1"},
			wantDelivered: 1,
			wantStatus:    domain.OutboxStatusDelivered,
			wantAttempt:   1,
			wantInboxID:   "inbox-1",
		},
		{
			name:          "lead assignment unsupported is terminal",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadAssignment, TargetAgent: "lead-1"},
			assignment:    &interaction.ChatDelivery{State: interaction.ChatDeliveryUnsupported, Reason: "runtime cannot accept turns"},
			wantStatus:    domain.OutboxStatusUnsupported,
			wantAttempt:   1,
			wantLastError: "runtime cannot accept turns",
		},
		{
			name:          "lead assignment pending stays pending with retry",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadAssignment, TargetAgent: "lead-1"},
			assignment:    &interaction.ChatDelivery{State: interaction.ChatDeliveryPending, Reason: "runtime not ready"},
			wantStatus:    domain.OutboxStatusPending,
			wantAttempt:   1,
			wantRetryAt:   true,
			wantLastError: "runtime not ready",
		},
		{
			name:          "lead assignment transient error stays pending",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadAssignment, TargetAgent: "lead-1"},
			assignmentErr: errors.New("store hiccup"),
			wantStatus:    domain.OutboxStatusPending,
			wantAttempt:   1,
			wantRetryAt:   true,
			wantLastError: "store hiccup",
		},
		{
			name:          "lead task message queued is delivered",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadTaskMessage, TargetAgent: "lead-1", Body: "task done"},
			message:       AgentMessageDeliveryResult{State: "queued", InboxMessageID: "inbox-2"},
			wantDelivered: 1,
			wantStatus:    domain.OutboxStatusDelivered,
			wantAttempt:   1,
			wantInboxID:   "inbox-2",
		},
		{
			name:          "lead task message error stays pending",
			create:        store.OutboxCreate{Kind: domain.OutboxKindLeadTaskMessage, TargetAgent: "lead-1", Body: "task done"},
			messageErr:    errors.New("agent missing"),
			wantStatus:    domain.OutboxStatusPending,
			wantAttempt:   1,
			wantRetryAt:   true,
			wantLastError: "agent missing",
		},
		{
			name:          "unknown kind is terminal unsupported",
			create:        store.OutboxCreate{Kind: domain.OutboxKind("mystery"), TargetAgent: "lead-1"},
			wantStatus:    domain.OutboxStatusUnsupported,
			wantAttempt:   1,
			wantLastError: `unknown outbox kind "mystery"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := memstore.New()
			row := createOutboxRow(t, ctx, st, tc.create)
			d := &OutboxDispatcher{
				Store:        st,
				WorkspaceKey: outboxTestWorkspace,
				Now:          func() time.Time { return now },
				deliverAssignment: func(
					context.Context,
					interaction.ChatMessenger,
					string,
					string,
				) (*interaction.ChatDelivery, error) {
					return tc.assignment, tc.assignmentErr
				},
				deliverTaskMessage: func(
					context.Context,
					interaction.ChatMessenger,
					string,
					string,
					string,
					string,
					AgentMessageDeliveryOptions,
				) (AgentMessageDeliveryResult, error) {
					return tc.message, tc.messageErr
				},
			}
			delivered, err := d.RunOnce(ctx)
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if delivered != tc.wantDelivered {
				t.Fatalf("delivered = %d, want %d", delivered, tc.wantDelivered)
			}
			got := getOutboxRow(t, ctx, st, row.OutboxID)
			if got.Status != tc.wantStatus || got.Attempt != tc.wantAttempt {
				t.Fatalf("row status/attempt = %s/%d, want %s/%d", got.Status, got.Attempt, tc.wantStatus, tc.wantAttempt)
			}
			if got.InboxMessageID != tc.wantInboxID {
				t.Fatalf("row inbox message = %q, want %q", got.InboxMessageID, tc.wantInboxID)
			}
			if (got.NextRetryAt != nil) != tc.wantRetryAt {
				t.Fatalf("row next retry at = %v, want set=%v", got.NextRetryAt, tc.wantRetryAt)
			}
			if tc.wantRetryAt && !got.NextRetryAt.After(now) {
				t.Fatalf("row next retry at = %v, want after %v", got.NextRetryAt, now)
			}
			if got.LastError != tc.wantLastError {
				t.Fatalf("row last error = %q, want %q", got.LastError, tc.wantLastError)
			}
		})
	}
}

func TestOutboxDispatcherBackoffGrowsAndSkipsNotDueRows(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	row := createOutboxRow(t, ctx, st, store.OutboxCreate{
		Kind:        domain.OutboxKindLeadAssignment,
		TargetAgent: "lead-1",
	})
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	attempts := 0
	d := &OutboxDispatcher{
		Store:        st,
		WorkspaceKey: outboxTestWorkspace,
		Now:          func() time.Time { return now },
		deliverAssignment: func(
			context.Context,
			interaction.ChatMessenger,
			string,
			string,
		) (*interaction.ChatDelivery, error) {
			attempts++
			return &interaction.ChatDelivery{
				State:  interaction.ChatDeliveryPending,
				Reason: "runtime not ready",
			}, nil
		},
	}

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce #1: %v", err)
	}
	first := getOutboxRow(t, ctx, st, row.OutboxID)
	if attempts != 1 || first.Attempt != 1 || first.NextRetryAt == nil {
		t.Fatalf("after first pass attempts/row = %d/%+v, want one recorded attempt with retry time", attempts, first)
	}
	firstDelay := first.NextRetryAt.Sub(now)

	// Not due yet: the row's NextRetryAt is in the future, so the next pass
	// must not attempt delivery.
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce #2: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d after not-due pass, want 1 (future rows are skipped)", attempts)
	}

	// Past the retry time the row is due again; the backoff delay grows.
	now = first.NextRetryAt.Add(time.Millisecond)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce #3: %v", err)
	}
	second := getOutboxRow(t, ctx, st, row.OutboxID)
	if attempts != 2 || second.Attempt != 2 || second.NextRetryAt == nil {
		t.Fatalf("after due pass attempts/row = %d/%+v, want second recorded attempt", attempts, second)
	}
	if secondDelay := second.NextRetryAt.Sub(now); secondDelay <= firstDelay {
		t.Fatalf("backoff delay = %v after %v, want growth", secondDelay, firstDelay)
	}
}

func TestOutboxRetryDelayIsCapped(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 2 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second},
		{60, 30 * time.Second},
	}
	for _, tc := range cases {
		if got := outboxRetryDelay(tc.attempt); got != tc.want {
			t.Fatalf("outboxRetryDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestOutboxDispatcherForwardsDedupeKeyToInbox(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	row := createOutboxRow(t, ctx, st, store.OutboxCreate{
		Kind:        domain.OutboxKindLeadTaskMessage,
		EpicID:      "EPIC-1",
		DriverRunID: "run-1",
		TaskRunID:   "task-run-1",
		TargetAgent: "worker-bot",
		Body:        "task completed",
		DedupeKey:   "lead-task-message:EPIC-1:task-run-1:completed",
	})
	inbox := testInteractionInbox{store: st}
	messenger := &testInteractionChatMessenger{inbox: inbox}
	d := &OutboxDispatcher{
		Store:        st,
		WorkspaceKey: outboxTestWorkspace,
		Chat:         messenger,
	}
	delivered, err := d.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if delivered != 1 {
		t.Fatalf("delivered = %d, want 1", delivered)
	}
	got := getOutboxRow(t, ctx, st, row.OutboxID)
	if got.Status != domain.OutboxStatusDelivered || got.InboxMessageID == "" {
		t.Fatalf("row = %+v, want delivered with inbox message id", got)
	}
	messages, err := st.AgentInboxMessages().List(ctx, outboxTestWorkspace, store.AgentInboxMessageFilter{
		TargetAgentID: "worker-bot",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("List inbox messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("inbox messages = %d, want 1", len(messages))
	}
	if messages[0].DedupeKey != row.DedupeKey {
		t.Fatalf("inbox dedupe key = %q, want outbox row key %q", messages[0].DedupeKey, row.DedupeKey)
	}
	if messages[0].TaskRunID != "task-run-1" || messages[0].DriverRunID != "run-1" {
		t.Fatalf("inbox provenance = %+v, want task-run-1/run-1", messages[0])
	}
	if len(messenger.messages) != 1 ||
		messenger.messages[0].SourceRef != "driver-run://run-1" ||
		messenger.messages[0].DedupeKey != row.DedupeKey {
		t.Fatalf("Interaction chat commands = %+v", messenger.messages)
	}

	// Redelivery with the same dedupe key returns the same inbox message
	// instead of enqueueing a duplicate.
	again, err := DeliverAgentMessageForDriverWithOptions(
		ctx,
		messenger,
		outboxTestWorkspace,
		"run-1",
		"worker-bot",
		"task completed",
		AgentMessageDeliveryOptions{
			TaskRunID: "task-run-1",
			DedupeKey: row.DedupeKey,
		},
	)
	if err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	if again.InboxMessageID != got.InboxMessageID {
		t.Fatalf("redelivered inbox id = %q, want original %q", again.InboxMessageID, got.InboxMessageID)
	}
	messages, err = st.AgentInboxMessages().List(ctx, outboxTestWorkspace, store.AgentInboxMessageFilter{
		TargetAgentID: "worker-bot",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("List inbox messages after redelivery: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("inbox messages after redelivery = %d, want 1 (deduped)", len(messages))
	}
}

type testInteractionInbox struct {
	store store.Store
}

type testInteractionChatMessenger struct {
	inbox    interaction.InboxEnqueuer
	messages []interaction.DeliverChatMessageCommand
}

func (messenger *testInteractionChatMessenger) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	messenger.messages = append(messenger.messages, command)
	message, err := agentinbox.Enqueue(
		ctx,
		messenger.inbox,
		command.WorkspaceKey,
		command.AgentID,
		command.Body,
		agentinbox.MessageOptions{
			SourceKind:        command.SourceKind,
			SourceRef:         command.SourceRef,
			DriverRunID:       command.DriverRunID,
			TaskRunID:         command.TaskRunID,
			TriggerEventID:    command.TriggerEventID,
			TriggerDeliveryID: command.TriggerDeliveryID,
			DedupeKey:         command.DedupeKey,
		},
	)
	if err != nil {
		return nil, err
	}
	return &interaction.ChatDelivery{
		State:          interaction.ChatDeliveryPending,
		SessionID:      message.SessionID,
		InboxMessageID: message.InboxMessageID,
	}, nil
}

func (*testInteractionChatMessenger) DeliverAssignment(
	context.Context,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryUnsupported,
	}, nil
}

func (inbox testInteractionInbox) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := inbox.store.AgentInboxMessages().Create(ctx, store.AgentInboxMessageCreate{
		WorkspaceKey: command.WorkspaceKey, TargetAgentID: command.TargetAgentID,
		SessionID: command.SessionID, Body: command.Body,
		SourceKind: command.SourceKind, SourceRef: command.SourceRef,
		DriverRunID: command.DriverRunID, TaskRunID: command.TaskRunID,
		TriggerEventID: command.TriggerEventID, TriggerDeliveryID: command.TriggerDeliveryID,
		DedupeKey: command.DedupeKey,
	})
	if err != nil {
		return nil, err
	}
	return &interaction.InboxMessage{
		WorkspaceKey: message.WorkspaceKey, MessageID: message.InboxMessageID,
		Cursor: message.Cursor, TargetAgentID: message.TargetAgentID,
		SessionID: message.SessionID, Body: message.Body,
		Status:     interaction.InboxStatus(message.Status),
		SourceKind: message.SourceKind, SourceRef: message.SourceRef,
		DriverRunID: message.DriverRunID, TaskRunID: message.TaskRunID,
		TriggerEventID: message.TriggerEventID, TriggerDeliveryID: message.TriggerDeliveryID,
		DedupeKey: message.DedupeKey, Attempt: message.Attempt,
		ClaimedBy: message.ClaimedBy, ClaimExpiresAt: message.ClaimExpiresAt,
		ErrorClass: message.ErrorClass, DeliveredThreadID: message.DeliveredThreadID,
		DeliveredAt: message.DeliveredAt, CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}, nil
}

func TestOutboxDispatcherTerminalRowsAreNotRetried(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	createOutboxRow(t, ctx, st, store.OutboxCreate{
		Kind:        domain.OutboxKindLeadAssignment,
		TargetAgent: "lead-1",
	})
	attempts := 0
	d := &OutboxDispatcher{
		Store:        st,
		WorkspaceKey: outboxTestWorkspace,
		deliverAssignment: func(
			context.Context,
			interaction.ChatMessenger,
			string,
			string,
		) (*interaction.ChatDelivery, error) {
			attempts++
			return &interaction.ChatDelivery{
				State:  interaction.ChatDeliveryUnsupported,
				Reason: "no adapter",
			}, nil
		},
	}
	for pass := 0; pass < 3; pass++ {
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce pass %d: %v", pass, err)
		}
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (unsupported is terminal)", attempts)
	}
}

func TestOutboxDispatcherUnscopedResolvesEveryWorkspace(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	for _, workspace := range []string{"WS-A", "WS-B"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{
			Key: workspace, Name: workspace,
		}); err != nil {
			t.Fatalf("create workspace %q: %v", workspace, err)
		}
	}
	dispatcher := &OutboxDispatcher{Store: st}
	got, err := dispatcher.workspaceKeys(ctx)
	if err != nil {
		t.Fatalf("workspaceKeys: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("workspaceKeys = %v, want two workspaces", got)
	}
	seen := map[string]bool{}
	for _, workspace := range got {
		seen[workspace] = true
	}
	if !seen["WS-A"] || !seen["WS-B"] {
		t.Fatalf("workspaceKeys = %v, want WS-A and WS-B", got)
	}
}
