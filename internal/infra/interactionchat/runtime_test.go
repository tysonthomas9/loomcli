package interactionchat

import (
	"context"
	"errors"
	"testing"

	hwtranscript "github.com/olesho/harness-wrapper/pkg/transcript"

	"github.com/tysonthomas9/loomcli/internal/domain"
	leadcontrol "github.com/tysonthomas9/loomcli/internal/infra/interactionlead"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type inboxStub struct {
	commands []interaction.EnqueueInboxCommand
	steps    *[]string
}

func (stub *inboxStub) Enqueue(
	_ context.Context,
	command interaction.EnqueueInboxCommand,
) (*interaction.InboxMessage, error) {
	stub.commands = append(stub.commands, command)
	if stub.steps != nil {
		*stub.steps = append(*stub.steps, "enqueue")
	}
	return &interaction.InboxMessage{
		WorkspaceKey:  command.WorkspaceKey,
		MessageID:     command.MessageID,
		TargetAgentID: command.TargetAgentID,
		SessionID:     command.SessionID,
		Body:          command.Body,
		Status:        interaction.InboxQueued,
		SourceKind:    command.SourceKind,
		SourceRef:     command.SourceRef,
		DriverRunID:   command.DriverRunID,
		TaskRunID:     command.TaskRunID,
		DedupeKey:     command.DedupeKey,
	}, nil
}

type agentQueriesStub struct {
	agent    *agents.Agent
	agentErr error
	role     *agents.Role
	roleErr  error
	steps    *[]string
}

func (stub *agentQueriesStub) GetAgent(
	_ context.Context,
	_,
	_ string,
) (*agents.Agent, error) {
	if stub.steps != nil {
		*stub.steps = append(*stub.steps, "get-agent")
	}
	return stub.agent, stub.agentErr
}

func (stub *agentQueriesStub) GetRole(
	_ context.Context,
	_,
	_ string,
) (*agents.Role, error) {
	if stub.steps != nil {
		*stub.steps = append(*stub.steps, "get-role")
	}
	return stub.role, stub.roleErr
}

type codexReaderStub struct {
	thread *leadcontrol.CodexThread
	err    error
	closed bool
}

type harnessReaderStub struct {
	calls  int
	events []hwtranscript.Event
	err    error
}

func (stub *harnessReaderStub) Read(
	_ context.Context,
	_,
	_ string,
) ([]hwtranscript.Event, error) {
	stub.calls++
	return stub.events, stub.err
}

func (stub *codexReaderStub) ReadThreadWithTurns(
	context.Context,
	string,
) (*leadcontrol.CodexThread, error) {
	return stub.thread, stub.err
}

func (stub *codexReaderStub) Close(string) error {
	stub.closed = true
	return nil
}

func TestRuntimeReadsAndRedactsCodexConversation(t *testing.T) {
	st := memstore.New()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:   leadcontrol.RuntimeProviderCodex,
		leadcontrol.MetadataCodexEndpoint:     "ws://codex.test",
		leadcontrol.MetadataCodexThreadID:     "thread-1",
		leadcontrol.MetadataRuntimeControlled: "true",
	})
	reader := &codexReaderStub{
		thread: &leadcontrol.CodexThread{
			Status: leadcontrol.CodexThreadStatus{Type: "idle"},
			Turns: []leadcontrol.CodexTurn{{
				ID: "turn-1",
				Items: []leadcontrol.CodexTurnItem{
					{
						Type: "userMessage",
						ID:   "prompt",
						Content: []leadcontrol.CodexContentBlock{{
							Type: "text",
							Text: reviewerPromptMarker + "\nReview.",
						}},
					},
					{
						Type: "agentMessage",
						ID:   "answer",
						Text: "token=ghp_abcdefghijklmnopqrstuvwxyz123456",
					},
				},
			}},
		},
	}
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.dialCodex = func(
		context.Context,
		string,
	) (codexThreadReader, error) {
		return reader, nil
	}
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.State != interaction.ConversationIdle ||
		len(conversation.Messages) != 1 ||
		conversation.Messages[0].Role != "assistant" ||
		conversation.Messages[0].Text ==
			"token=ghp_abcdefghijklmnopqrstuvwxyz123456" ||
		!reader.closed {
		t.Fatalf("conversation = %+v closed=%t", conversation, reader.closed)
	}
}

func TestRuntimeMapsUnavailableAndUnsupportedProviderStates(t *testing.T) {
	st := memstore.New()
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "missing",
		},
	)
	if err != nil ||
		conversation.State != interaction.ConversationStarting {
		t.Fatalf("missing conversation = %+v, %v", conversation, err)
	}

	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider: "cursor",
	})
	conversation, err = runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil ||
		conversation.State != interaction.ConversationUnsupported {
		t.Fatalf("unsupported conversation = %+v, %v", conversation, err)
	}
}

func TestRuntimeReadsProviderNeutralHarnessConversation(t *testing.T) {
	st := memstore.New()
	seedRuntimeSession(t, st, map[string]string{
		leadcontrol.MetadataRuntimeProvider:   "claude",
		leadcontrol.MetadataHarnessSessionID:  "11111111-2222-4333-8444-555555555555",
		leadcontrol.MetadataRuntimeStatus:     leadcontrol.RuntimeStatusIdle,
		leadcontrol.MetadataRuntimeControlled: "true",
		leadcontrol.MetadataHarnessStartedAt:  "2026-07-30T12:00:00Z",
	})
	reader := &harnessReaderStub{
		events: []hwtranscript.Event{
			{
				Seq:  1,
				Role: hwtranscript.RoleUser,
				Type: hwtranscript.EventText,
				Text: reviewerPromptMarker + "\nReview.",
				UUID: "prompt-1",
			},
			{
				Seq:  2,
				Role: hwtranscript.RoleAssistant,
				Type: hwtranscript.EventText,
				Text: "LGTM.",
				UUID: "answer-1",
			},
			{
				Seq:  3,
				Role: hwtranscript.RoleTool,
				Type: hwtranscript.EventText,
				Text: "hidden tool result",
				UUID: "tool-1",
			},
		},
	}
	runtime := newTestRuntime(st, &inboxStub{}, &agentQueriesStub{})
	runtime.harnesses = map[string]harnessTranscriptReader{
		"claude": reader,
	}
	runtime.worktreeFor = func(
		workspace,
		agentID string,
	) (string, bool) {
		return "/tmp/reviewer-worktree", workspace == "WS" &&
			agentID == "reviewer"
	}
	conversation, err := runtime.ReadConversation(
		t.Context(),
		interaction.ConversationQuery{
			WorkspaceKey: "WS",
			AgentID:      "reviewer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.State != interaction.ConversationIdle ||
		len(conversation.Messages) != 1 ||
		conversation.Messages[0].Role != "assistant" ||
		conversation.Messages[0].Text != "LGTM." ||
		reader.calls != 1 {
		t.Fatalf(
			"conversation = %+v calls=%d",
			conversation,
			reader.calls,
		)
	}
}

func TestRuntimeQueuesGenericMessageAfterAgentValidation(t *testing.T) {
	steps := []string{}
	inbox := &inboxStub{steps: &steps}
	queries := &agentQueriesStub{
		agent: &agents.Agent{
			WorkspaceKey: "WS",
			AgentID:      "worker-1",
			Behavior: agents.BehaviorReference{
				DriverID:        "task-runner",
				DriverVersionID: "v1",
			},
		},
		steps: &steps,
	}
	runtime := newTestRuntime(memstore.New(), inbox, queries)
	delivery, err := runtime.DeliverChatMessage(
		t.Context(),
		interaction.DeliverChatMessageCommand{
			WorkspaceKey: "WS",
			AgentID:      "worker-1",
			Body:         "task finished",
			SourceKind:   "workflow",
			SourceRef:    "driver-run://run-1",
			DriverRunID:  "run-1",
			TaskRunID:    "task-run-1",
			DedupeKey:    "message-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.State != interaction.ChatDeliveryQueued ||
		delivery.InboxMessageID == "" ||
		delivery.Reason !=
			"agent message queued; no runtime delivery adapter is configured" {
		t.Fatalf("delivery = %+v", delivery)
	}
	if len(inbox.commands) != 1 {
		t.Fatalf("inbox commands = %+v, want one", inbox.commands)
	}
	command := inbox.commands[0]
	if command.TargetAgentID != "worker-1" ||
		command.Body != "task finished" ||
		command.SourceRef != "driver-run://run-1" ||
		command.DriverRunID != "run-1" ||
		command.TaskRunID != "task-run-1" ||
		command.DedupeKey != "message-1" {
		t.Fatalf("inbox command = %+v", command)
	}
	if got, want := steps, []string{"get-agent", "enqueue"}; !equalStrings(
		got,
		want,
	) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestRuntimeKeepsControlledLeadMessagePending(t *testing.T) {
	steps := []string{}
	inbox := &inboxStub{steps: &steps}
	queries := &agentQueriesStub{
		agent: &agents.Agent{
			WorkspaceKey: "WS",
			AgentID:      "lead-1",
			Behavior: agents.BehaviorReference{
				RoleName: "lead",
			},
		},
		role: &agents.Role{
			WorkspaceKey: "WS",
			Name:         "lead",
			Backend:      "codex",
		},
		steps: &steps,
	}
	runtime := newTestRuntime(memstore.New(), inbox, queries)
	delivery, err := runtime.DeliverChatMessage(
		t.Context(),
		interaction.DeliverChatMessageCommand{
			WorkspaceKey: "WS",
			AgentID:      "lead-1",
			Body:         "new assignment",
			SourceKind:   "workflow",
			DriverRunID:  "run-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.State != interaction.ChatDeliveryPending ||
		delivery.InboxMessageID == "" {
		t.Fatalf("delivery = %+v, want pending controlled lead", delivery)
	}
	if got, want := steps, []string{
		"get-agent",
		"get-role",
		"enqueue",
	}; !equalStrings(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestRuntimeRejectsMissingTargetBeforeInboxMutation(t *testing.T) {
	inbox := &inboxStub{}
	runtime := newTestRuntime(
		memstore.New(),
		inbox,
		&agentQueriesStub{agentErr: agents.ErrNotFound},
	)
	_, err := runtime.DeliverChatMessage(
		t.Context(),
		interaction.DeliverChatMessageCommand{
			WorkspaceKey: "WS",
			AgentID:      "missing",
			Body:         "hello",
		},
	)
	if !errors.Is(err, interaction.ErrNotFound) {
		t.Fatalf("error = %v, want Interaction not found", err)
	}
	if len(inbox.commands) != 0 {
		t.Fatalf("inbox commands = %+v, want none", inbox.commands)
	}
}

func TestRuntimeValidatesLeadBeforeAssignmentInboxMutation(t *testing.T) {
	st := memstore.New()
	if _, err := st.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: "lead"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorkerProfiles().Create(t.Context(), store.WorkerProfileCreate{
		WorkspaceKey: "WS", ProfileID: "lead-profile", Role: "lead", ParentEpic: "EPIC-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "lead-1", Kind: domain.AgentServiceKindLead,
		RoleName: "lead", ProfileName: "lead-profile",
	}); err != nil {
		t.Fatal(err)
	}
	steps := []string{}
	inbox := &inboxStub{steps: &steps}
	runtime := newTestRuntime(st, inbox, &agentQueriesStub{
		agent: &agents.Agent{
			WorkspaceKey: "WS",
			AgentID:      "lead-1",
			Behavior: agents.BehaviorReference{
				RoleName: "lead",
			},
		},
		role: &agents.Role{
			WorkspaceKey: "WS",
			Name:         "lead",
			Backend:      "codex",
		},
		steps: &steps,
	})
	delivery, err := runtime.DeliverAssignment(
		t.Context(),
		interaction.DeliverAssignmentCommand{
			WorkspaceKey: "WS",
			AgentID:      "lead-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.State != interaction.ChatDeliveryPending ||
		delivery.InboxMessageID == "" ||
		len(inbox.commands) != 1 {
		t.Fatalf(
			"delivery = %+v inbox commands = %+v",
			delivery,
			inbox.commands,
		)
	}
	if inbox.commands[0].SourceKind != "lead_assignment" ||
		inbox.commands[0].TargetAgentID != "lead-1" {
		t.Fatalf("assignment inbox command = %+v", inbox.commands[0])
	}
	if got, want := steps, []string{
		"get-agent",
		"get-role",
		"enqueue",
	}; !equalStrings(got, want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
}

func TestNewFailsClosedWithoutOwnedDependencies(t *testing.T) {
	queries := &agentQueriesStub{}
	inbox := &inboxStub{}
	if _, err := New(LeadRuntimeDependencies{}, inbox, queries); !errors.Is(
		err,
		interaction.ErrUnavailable,
	) {
		t.Fatalf("nil Store error = %v", err)
	}
	if _, err := New(testLeadRuntime(memstore.New()), nil, queries); !errors.Is(
		err,
		interaction.ErrUnavailable,
	) {
		t.Fatalf("nil inbox error = %v", err)
	}
	if _, err := New(testLeadRuntime(memstore.New()), inbox, nil); !errors.Is(
		err,
		interaction.ErrUnavailable,
	) {
		t.Fatalf("nil Agents queries error = %v", err)
	}
}

func newTestRuntime(
	st store.Store,
	inbox interaction.InboxEnqueuer,
	queries AgentQueries,
) *Runtime {
	return newRuntime(testLeadRuntime(st), inbox, queries)
}

func testLeadRuntime(st store.Store) LeadRuntimeDependencies {
	return LeadRuntimeDependencies{
		DeliverMessage: func(
			ctx context.Context,
			workspace, agentID, body string,
			options leadcontrol.LeadMessageDeliveryOptions,
			inbox interaction.InboxEnqueuer,
		) (*leadcontrol.DeliveryResult, error) {
			return leadcontrol.DeliverLeadMessageWithOptions(
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
		) (*leadcontrol.DeliveryResult, error) {
			return leadcontrol.DeliverCurrentAssignment(
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
			return store.OrchestrationSessionFor(
				ctx,
				st,
				workspace,
				agentID,
			)
		},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func seedRuntimeSession(
	t *testing.T,
	st store.Store,
	metadata map[string]string,
) {
	t.Helper()
	_, err := st.AgentSessions().Create(
		t.Context(),
		store.AgentSessionCreate{
			WorkspaceKey: "WS",
			SessionID:    "session-1",
			AgentID:      "reviewer",
			Kind:         domain.AgentSessionKindOrchestration,
			Status:       domain.AgentSessionRunning,
			Metadata:     metadata,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}
