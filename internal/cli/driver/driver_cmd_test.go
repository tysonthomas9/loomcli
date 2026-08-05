//nolint:revive // Tests use the established command package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentinbox"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestParseDriverRunPayload(t *testing.T) {
	payload, err := parseDriverRunPayload([]string{"provider=flue-daytona", "note=hello=world"}, "TEST-1")
	if err != nil {
		t.Fatalf("parseDriverRunPayload: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if got["provider"] != "flue-daytona" || got["note"] != "hello=world" || got["epicId"] != "TEST-1" {
		t.Fatalf("payload = %+v, want parsed key-values", got)
	}
}

func TestParseDriverRunPayloadRejectsMissingKey(t *testing.T) {
	if _, err := parseDriverRunPayload([]string{"=value"}, ""); err == nil {
		t.Fatal("parseDriverRunPayload accepted empty key")
	}
	if _, err := parseDriverRunPayload([]string{"missing-equals"}, ""); err == nil {
		t.Fatal("parseDriverRunPayload accepted missing equals")
	}
}

func TestDriverCommandContainsSubcommands(t *testing.T) {
	if driverCmd.Commands() == nil {
		t.Fatal("driver command has no subcommands")
	}
	for _, name := range []string{"register", "run", "exec-task", "work-task-run", "claim-ready", "epic-get", "epic-snapshot", "list-agents", "agent-orchestration-session", "update-agent-parent", "deliver-lead-assignment", "deliver-agent-message", "active-task-runs", "complete-task", "release-task", "recover-stale-tasks"} {
		found := false
		for _, cmd := range driverCmd.Commands() {
			if cmd.Name() == name {
				found = true
				if (name == "exec-task" || name == "work-task-run" || name == "claim-ready" || name == "epic-get" || name == "epic-snapshot" || name == "list-agents" || name == "agent-orchestration-session" || name == "update-agent-parent" || name == "deliver-lead-assignment" || name == "deliver-agent-message" || name == "active-task-runs" || name == "complete-task" || name == "release-task" || name == "recover-stale-tasks") && !cmd.Hidden {
					t.Fatalf("%s command should stay hidden", name)
				}
				break
			}
		}
		if !found {
			t.Fatalf("driver command missing %q subcommand", name)
		}
	}
	for _, cmd := range driverCmd.Commands() {
		if cmd.Name() == "deliver-lead-message" {
			t.Fatal("driver command should not register deliver-lead-message compatibility alias")
		}
	}
	for _, flag := range []string{"legacy-task-close", "session", "actor", "force"} {
		if driverCompleteTaskCmd.Flags().Lookup(flag) != nil {
			t.Fatalf("complete-task command should not register removed compatibility flag %q", flag)
		}
	}
	if driverCompleteTaskCmd.Flags().Lookup("lease-token") != nil {
		t.Fatal("complete-task command should not expose a caller-controlled lease-token flag")
	}
	if driverExecTaskCmd.Flags().Lookup("defer-completion") == nil {
		t.Fatal("exec-task command missing defer-completion flag")
	}
}

func TestTaskRunAPIBaseURL(t *testing.T) {
	t.Setenv("LOOM_TASK_RUN_API_URL", " https://tasks.example.test ")
	t.Setenv("LOOM_DRIVER_API_URL", "https://driver.example.test")
	if got := taskRunAPIBaseURL(); got != "https://tasks.example.test" {
		t.Fatalf("taskRunAPIBaseURL with explicit task URL = %q", got)
	}

	t.Setenv("LOOM_TASK_RUN_API_URL", "")
	if got := taskRunAPIBaseURL(); got != "https://driver.example.test" {
		t.Fatalf("taskRunAPIBaseURL fallback = %q, want driver API URL", got)
	}
}

func TestDriverRegisterTrustDefaultsUntrusted(t *testing.T) {
	origTrusted := driverRegisterTrusted
	origUntrusted := driverRegisterUntrusted
	t.Cleanup(func() {
		driverRegisterTrusted = origTrusted
		driverRegisterUntrusted = origUntrusted
	})

	driverRegisterTrusted = false
	driverRegisterUntrusted = false
	if trust, err := driverRegisterTrust(); err != nil || trust != domain.DriverTrustUntrusted {
		t.Fatalf("driverRegisterTrust without flag = %q err=%v, want untrusted", trust, err)
	}

	driverRegisterTrusted = true
	driverRegisterUntrusted = true
	if _, err := driverRegisterTrust(); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("driverRegisterTrust with both flags err = %v, want ErrInvalid", err)
	}

	driverRegisterTrusted = true
	driverRegisterUntrusted = false
	if trust, err := driverRegisterTrust(); err != nil || trust != domain.DriverTrustTrusted {
		t.Fatalf("driverRegisterTrust trusted = %q err=%v", trust, err)
	}

	driverRegisterTrusted = false
	driverRegisterUntrusted = true
	if trust, err := driverRegisterTrust(); err != nil || trust != domain.DriverTrustUntrusted {
		t.Fatalf("driverRegisterTrust untrusted = %q err=%v", trust, err)
	}
}

func TestDeliverAgentMessageForDriverQueuesGenericMessage(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	messenger := driverCommandChatMessenger{
		inbox: driverCommandInbox{store: st},
	}
	result, err := deliverAgentMessageForDriver(
		ctx,
		messenger,
		"WS",
		"run-1",
		"worker-1",
		" task finished ",
	)
	if err != nil {
		t.Fatalf("deliver generic agent message: %v", err)
	}
	if result.State != string(interaction.ChatDeliveryPending) ||
		result.AgentName != "worker-1" ||
		result.InboxMessageID == "" {
		t.Fatalf("result = %#v, want queued inbox message", result)
	}
	msgs, err := st.AgentInboxMessages().List(ctx, "WS", store.AgentInboxMessageFilter{
		TargetAgentID: "worker-1",
		Status:        domain.AgentInboxMessageQueued,
	})
	if err != nil {
		t.Fatalf("list inbox messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("queued inbox messages = %#v, want one", msgs)
	}
	if msgs[0].Body != "task finished" || msgs[0].SourceKind != "workflow" || msgs[0].SourceRef != "driver-run://run-1" || msgs[0].DriverRunID != "run-1" {
		t.Fatalf("queued inbox message = %#v", msgs[0])
	}
}

type driverCommandChatMessenger struct {
	inbox interaction.InboxEnqueuer
}

func (messenger driverCommandChatMessenger) DeliverChatMessage(
	ctx context.Context,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	message, err := agentinbox.Enqueue(
		ctx,
		messenger.inbox,
		command.WorkspaceKey,
		command.AgentID,
		command.Body,
		agentinbox.MessageOptions{
			SourceKind:  command.SourceKind,
			SourceRef:   command.SourceRef,
			DriverRunID: command.DriverRunID,
			TaskRunID:   command.TaskRunID,
			DedupeKey:   command.DedupeKey,
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

func (driverCommandChatMessenger) DeliverAssignment(
	context.Context,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryUnsupported,
	}, nil
}

type driverCommandInbox struct {
	store store.Store
}

func (inbox driverCommandInbox) Enqueue(
	ctx context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	message, err := inbox.store.AgentInboxMessages().Create(
		ctx,
		store.AgentInboxMessageCreate{
			WorkspaceKey:  command.WorkspaceKey,
			TargetAgentID: command.TargetAgentID,
			SessionID:     command.SessionID,
			Body:          command.Body,
			SourceKind:    command.SourceKind,
			SourceRef:     command.SourceRef,
			DriverRunID:   command.DriverRunID,
			TaskRunID:     command.TaskRunID,
			DedupeKey:     command.DedupeKey,
		},
	)
	if err != nil {
		return nil, err
	}
	return &interaction.InboxMessage{
		WorkspaceKey:  message.WorkspaceKey,
		MessageID:     message.InboxMessageID,
		Cursor:        message.Cursor,
		TargetAgentID: message.TargetAgentID,
		SessionID:     message.SessionID,
		Body:          message.Body,
		Status:        interaction.InboxStatus(message.Status),
		SourceKind:    message.SourceKind,
		SourceRef:     message.SourceRef,
		DriverRunID:   message.DriverRunID,
		TaskRunID:     message.TaskRunID,
		DedupeKey:     message.DedupeKey,
		CreatedAt:     message.CreatedAt,
		UpdatedAt:     message.UpdatedAt,
	}, nil
}

func TestResolveDriverCompleteTaskLeaseToken(t *testing.T) {
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "task-token")
	t.Setenv("LOOM_RUNNER_LEASE_TOKEN", "runner-token")

	if got := resolveDriverCompleteTaskLeaseToken(); got != "task-token" {
		t.Fatalf("resolve token from env = %q, want task-token", got)
	}
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "")
	if got := resolveDriverCompleteTaskLeaseToken(); got != "runner-token" {
		t.Fatalf("resolve runner token fallback = %q, want runner-token", got)
	}
}

func TestParseDriverRecoverStaleBefore(t *testing.T) {
	got, err := parseDriverRecoverStaleBefore("2026-06-06T12:34:56Z")
	if err != nil {
		t.Fatalf("parse stale before: %v", err)
	}
	want := time.Date(2026, 6, 6, 12, 34, 56, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("stale before = %s, want %s", got, want)
	}
	zero, err := parseDriverRecoverStaleBefore(" ")
	if err != nil {
		t.Fatalf("parse empty stale before: %v", err)
	}
	if !zero.IsZero() {
		t.Fatalf("empty stale before = %s, want zero", zero)
	}
	if _, err := parseDriverRecoverStaleBefore("not-a-time"); err == nil {
		t.Fatal("parse invalid stale before succeeded")
	}
}
