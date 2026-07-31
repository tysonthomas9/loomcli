package interactioncomposition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type interactionChatCommandsStub struct {
	messageAuth       authority.SystemAuthority
	messageCommand    interaction.DeliverChatMessageCommand
	assignmentAuth    authority.SystemAuthority
	assignmentCommand interaction.DeliverAssignmentCommand
}

type interactionChatRuntimeStub struct{}

func (interactionChatRuntimeStub) DeliverChatMessage(
	context.Context,
	interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryDelivered,
	}, nil
}

func (interactionChatRuntimeStub) DeliverAssignment(
	context.Context,
	interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryPending,
	}, nil
}

func (interactionChatRuntimeStub) ReadConversation(
	context.Context,
	interaction.ConversationQuery,
) (*interaction.Conversation, error) {
	return &interaction.Conversation{
		State:    interaction.ConversationIdle,
		Messages: []interaction.ConversationMessage{},
	}, nil
}

func (stub *interactionChatCommandsStub) DeliverChatMessageAsSystem(
	_ context.Context,
	auth authority.SystemAuthority,
	command interaction.DeliverChatMessageCommand,
) (*interaction.ChatDelivery, error) {
	stub.messageAuth = auth
	stub.messageCommand = command
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryDelivered,
	}, nil
}

func (stub *interactionChatCommandsStub) DeliverAssignmentAsSystem(
	_ context.Context,
	auth authority.SystemAuthority,
	command interaction.DeliverAssignmentCommand,
) (*interaction.ChatDelivery, error) {
	stub.assignmentAuth = auth
	stub.assignmentCommand = command
	return &interaction.ChatDelivery{
		State: interaction.ChatDeliveryPending,
	}, nil
}

func TestInteractionChatMessengerDerivesExactSystemAuthority(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	commands := &interactionChatCommandsStub{}
	messenger := newInteractionChatMessenger(
		commands,
		newInteractionRuntimeAuthorityProvider(
			issuer,
			func() time.Time { return now },
		),
	)
	message := interaction.DeliverChatMessageCommand{
		WorkspaceKey: "WS",
		AgentID:      "reviewer",
		Body:         "Please re-check the failure.",
		SourceKind:   "pr_review",
		SourceRef:    "octocat/hello#7",
		DedupeKey:    "pr-review-message-1",
	}
	delivery, err := messenger.DeliverChatMessage(
		t.Context(),
		message,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.State != interaction.ChatDeliveryDelivered ||
		commands.messageCommand != message {
		t.Fatalf(
			"message delivery = %+v command = %+v",
			delivery,
			commands.messageCommand,
		)
	}
	assertInteractionChatAuthority(
		t,
		commands.messageAuth,
		interaction.ActionDeliverChatMessage,
	)

	assignment := interaction.DeliverAssignmentCommand{
		WorkspaceKey: "WS",
		AgentID:      "lead",
	}
	delivery, err = messenger.DeliverAssignment(
		t.Context(),
		assignment,
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.State != interaction.ChatDeliveryPending ||
		commands.assignmentCommand != assignment {
		t.Fatalf(
			"assignment delivery = %+v command = %+v",
			delivery,
			commands.assignmentCommand,
		)
	}
	assertInteractionChatAuthority(
		t,
		commands.assignmentAuth,
		interaction.ActionDeliverAssignment,
	)
}

func TestComposeInteractionChatPublishesOnlyOwnedSurfacesOnce(
	t *testing.T,
) {
	persistence := &interactionPersistenceStub{}
	capability, err := NewInteractionCapability(
		InteractionConfig{WorkspaceKey: "WS"},
		InteractionDependencies{
			Sessions:         persistence,
			Terminals:        &interactionTerminalStoreStub{persistence},
			Inbox:            &interactionInboxStoreStub{},
			Activity:         &interactionActivityStub{},
			SessionAuthority: &interactionAuthorityValidatorStub{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if capability.ChatAPI() != nil || capability.ChatMessenger() != nil {
		t.Fatal("base Interaction unexpectedly exposed an uncomposed chat runtime")
	}
	if err := ComposeInteractionChat(
		capability,
		interactionChatRuntimeStub{},
	); err != nil {
		t.Fatal(err)
	}
	if capability.ChatAPI() == nil || capability.ChatMessenger() == nil {
		t.Fatal("composed Interaction omitted chat API or messenger")
	}
	if err := ComposeInteractionChat(
		capability,
		interactionChatRuntimeStub{},
	); !errors.Is(err, interaction.ErrConflict) {
		t.Fatalf("duplicate chat composition error = %v", err)
	}
}

func assertInteractionChatAuthority(
	t *testing.T,
	value authority.SystemAuthority,
	action authority.Action,
) {
	t.Helper()
	if value.Subject() != string(interaction.ChatDeliveryComponentID) ||
		value.Workspace() != "WS" ||
		value.Action() != action ||
		value.Reason() != "registered Interaction chat delivery" {
		t.Fatalf("chat authority = %+v", value)
	}
}
