package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type chatRuntimeStub struct {
	messages     []DeliverChatMessageCommand
	assignments  []DeliverAssignmentCommand
	queries      []ConversationQuery
	delivery     *ChatDelivery
	conversation *Conversation
	err          error
}

func (stub *chatRuntimeStub) DeliverChatMessage(
	_ context.Context,
	command DeliverChatMessageCommand,
) (*ChatDelivery, error) {
	stub.messages = append(stub.messages, command)
	return stub.delivery, stub.err
}

func (stub *chatRuntimeStub) DeliverAssignment(
	_ context.Context,
	command DeliverAssignmentCommand,
) (*ChatDelivery, error) {
	stub.assignments = append(stub.assignments, command)
	return stub.delivery, stub.err
}

func (stub *chatRuntimeStub) ReadConversation(
	_ context.Context,
	query ConversationQuery,
) (*Conversation, error) {
	stub.queries = append(stub.queries, query)
	return stub.conversation, stub.err
}

type chatHarness struct {
	service *ChatService
	runtime *chatRuntimeStub
	issuer  *authority.Issuer
	now     time.Time
}

func newChatHarness(t *testing.T) *chatHarness {
	t.Helper()
	harness := &chatHarness{
		now: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		runtime: &chatRuntimeStub{
			delivery: &ChatDelivery{State: ChatDeliveryPending},
			conversation: &Conversation{
				State: ConversationIdle,
				Messages: []ConversationMessage{{
					TurnID: "turn-1",
					ItemID: "item-1",
					Role:   "assistant",
					Text:   "Ready.",
				}},
			},
		},
	}
	issuer, err := authority.NewIssuerWithClock(func() time.Time {
		return harness.now
	})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(ChatOperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewChat(harness.runtime, admission)
	if err != nil {
		t.Fatal(err)
	}
	harness.issuer = issuer
	harness.service = service
	return harness
}

func (harness *chatHarness) operator(
	t *testing.T,
	action authority.Action,
) authority.OperatorAuthority {
	t.Helper()
	principal, err := harness.issuer.DeriveVerifiedPrincipal(
		authority.PrincipalClaims{
			Subject:   "operator:test",
			Class:     authority.ClassOperator,
			Workspace: testWorkspace,
			Actions:   []authority.Action{action},
			ExpiresAt: harness.now.Add(time.Hour),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := harness.issuer.IssueOperator(
		principal,
		testWorkspace,
		action,
	)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func (harness *chatHarness) system(
	t *testing.T,
	action authority.Action,
) authority.SystemAuthority {
	t.Helper()
	principal, err := harness.issuer.DeriveVerifiedPrincipal(
		authority.PrincipalClaims{
			Subject:   "serve-interaction-chat-delivery",
			Class:     authority.ClassSystem,
			Workspace: testWorkspace,
			Actions:   []authority.Action{action},
			ExpiresAt: harness.now.Add(time.Hour),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := harness.issuer.IssueSystem(
		principal,
		testWorkspace,
		action,
		"test Interaction chat delivery",
	)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func TestChatServiceRoutesTypedOperatorAndSystemDelivery(t *testing.T) {
	harness := newChatHarness(t)
	command := DeliverChatMessageCommand{
		WorkspaceKey: testWorkspace,
		AgentID:      testAgent,
		Body:         "hello",
		SourceKind:   "user_chat",
		DedupeKey:    "request-1",
	}
	result, err := harness.service.DeliverChatMessage(
		t.Context(),
		harness.operator(t, ActionDeliverChatMessage),
		command,
	)
	if err != nil || result.State != ChatDeliveryPending {
		t.Fatalf("DeliverChatMessage = %+v, %v", result, err)
	}
	if len(harness.runtime.messages) != 1 ||
		harness.runtime.messages[0] != command {
		t.Fatalf("runtime messages = %+v", harness.runtime.messages)
	}

	assignment := DeliverAssignmentCommand{
		WorkspaceKey: testWorkspace,
		AgentID:      testAgent,
	}
	result, err = harness.service.DeliverAssignmentAsSystem(
		t.Context(),
		harness.system(t, ActionDeliverAssignment),
		assignment,
	)
	if err != nil || result.State != ChatDeliveryPending {
		t.Fatalf("DeliverAssignmentAsSystem = %+v, %v", result, err)
	}
	if len(harness.runtime.assignments) != 1 ||
		harness.runtime.assignments[0] != assignment {
		t.Fatalf("runtime assignments = %+v", harness.runtime.assignments)
	}
}

func TestChatServiceAcceptsQueuedDelivery(t *testing.T) {
	harness := newChatHarness(t)
	harness.runtime.delivery = &ChatDelivery{
		State:          ChatDeliveryQueued,
		InboxMessageID: "inbox-1",
	}
	result, err := harness.service.DeliverChatMessage(
		t.Context(),
		harness.operator(t, ActionDeliverChatMessage),
		DeliverChatMessageCommand{
			WorkspaceKey: testWorkspace,
			AgentID:      testAgent,
			Body:         "hello",
			SourceKind:   "workflow",
			DedupeKey:    "request-1",
		},
	)
	if err != nil ||
		result.State != ChatDeliveryQueued ||
		result.InboxMessageID != "inbox-1" {
		t.Fatalf("DeliverChatMessage = %+v, %v", result, err)
	}
}

func TestChatServiceRejectsWrongActionAndInvalidIntentBeforeRuntime(t *testing.T) {
	harness := newChatHarness(t)
	command := DeliverChatMessageCommand{
		WorkspaceKey: testWorkspace,
		AgentID:      testAgent,
		Body:         "hello",
		SourceKind:   "user_chat",
		DedupeKey:    "request-1",
	}
	_, err := harness.service.DeliverChatMessage(
		t.Context(),
		harness.operator(t, ActionReadConversation),
		command,
	)
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong action error = %v", err)
	}
	command.DedupeKey = ""
	_, err = harness.service.DeliverChatMessage(
		t.Context(),
		harness.operator(t, ActionDeliverChatMessage),
		command,
	)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid intent error = %v", err)
	}
	if len(harness.runtime.messages) != 0 {
		t.Fatalf("runtime received invalid messages: %+v", harness.runtime.messages)
	}
}

func TestChatServiceReadsDefensivelyClonedConversation(t *testing.T) {
	harness := newChatHarness(t)
	conversation, err := harness.service.ReadConversation(
		t.Context(),
		harness.operator(t, ActionReadConversation),
		ConversationQuery{WorkspaceKey: testWorkspace, AgentID: testAgent},
	)
	if err != nil {
		t.Fatal(err)
	}
	conversation.Messages[0].Text = "mutated"
	if harness.runtime.conversation.Messages[0].Text != "Ready." {
		t.Fatal("conversation result aliases runtime state")
	}
	if len(harness.runtime.queries) != 1 ||
		harness.runtime.queries[0].AgentID != testAgent {
		t.Fatalf("queries = %+v", harness.runtime.queries)
	}
}

func TestChatServiceRejectsInvalidRuntimeResults(t *testing.T) {
	harness := newChatHarness(t)
	harness.runtime.delivery = &ChatDelivery{State: "mystery"}
	_, err := harness.service.DeliverAssignment(
		t.Context(),
		harness.operator(t, ActionDeliverAssignment),
		DeliverAssignmentCommand{
			WorkspaceKey: testWorkspace,
			AgentID:      testAgent,
		},
	)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("invalid delivery error = %v", err)
	}

	harness.runtime.conversation = &Conversation{
		State: ConversationIdle,
		Messages: []ConversationMessage{{
			TurnID: "turn-1",
			ItemID: "item-1",
			Role:   "tool",
			Text:   "secret",
		}},
	}
	_, err = harness.service.ReadConversation(
		t.Context(),
		harness.operator(t, ActionReadConversation),
		ConversationQuery{WorkspaceKey: testWorkspace, AgentID: testAgent},
	)
	if !errors.Is(err, ErrInvalidPersistedState) {
		t.Fatalf("invalid conversation error = %v", err)
	}
}

func TestNewChatFailsClosedWithoutCompleteComposition(t *testing.T) {
	harness := newChatHarness(t)
	if _, err := NewChat(nil, harness.service.admission); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf("nil runtime error = %v", err)
	}
	if _, err := NewChat(harness.runtime, nil); !errors.Is(
		err,
		ErrUnavailable,
	) {
		t.Fatalf("nil admission error = %v", err)
	}
}
