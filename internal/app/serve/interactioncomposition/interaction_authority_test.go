package interactioncomposition

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	platformruntime "github.com/tysonthomas9/loomcli/internal/platform/runtime"
)

type interactionAuthorityValidatorStub struct {
	value *interaction.SessionAuthorityValidation
	err   error
	calls int
}

func (stub *interactionAuthorityValidatorStub) ValidateSessionAuthority(
	_ context.Context,
	_ interaction.SessionAuthorityProof,
) (*interaction.SessionAuthorityValidation, error) {
	stub.calls++
	return stub.value, stub.err
}

func TestInteractionSessionAuthorityIsDerivedFromValidatedLeaseAndConsumesToken(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	validator := &interactionAuthorityValidatorStub{value: &interaction.SessionAuthorityValidation{
		WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
		TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
		FencingToken: 7, ExpiresAt: now.Add(30 * time.Second),
	}}
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	resolver := newInteractionSessionAuthorityResolver(validator, issuer, func() time.Time { return now })
	token := interaction.NewLeaseToken([]byte("raw-session-token"))
	value, err := resolver.ResolveSessionAuthority(
		t.Context(),
		interaction.ActionOpenTerminal,
		interaction.SessionAuthorityProof{
			WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
			TerminalID: "terminal-1", NodeID: "node-1", LeaseID: "lease-1",
			FencingToken: 7, Token: token,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if value.Subject() != "session:session-1" ||
		value.Workspace() != "WS" ||
		value.Action() != interaction.ActionOpenTerminal ||
		value.SessionID() != "session-1" ||
		value.AgentID() != "agent-1" ||
		value.TerminalID() != "terminal-1" ||
		value.NodeID() != "node-1" ||
		value.LeaseID() != "lease-1" ||
		value.FencingToken() != 7 ||
		!value.ExpiresAt().Equal(now.Add(30*time.Second)) {
		t.Fatalf("session authority = %+v", value)
	}
	if got := token.Bytes(); len(got) != 0 {
		t.Fatalf("resolver retained %d raw token bytes", len(got))
	}
	credential := value.SessionOwner().ConsumeLeaseCredential()
	if string(credential) != "raw-session-token" {
		t.Fatalf("authority credential = %q", credential)
	}
	clear(credential)
	if replay := value.SessionOwner().ConsumeLeaseCredential(); len(replay) != 0 {
		t.Fatalf("authority credential replay returned %d bytes", len(replay))
	}
}

func TestInteractionSessionAuthorityFailsClosedBeforeIssuance(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for name, testCase := range map[string]struct {
		action    authority.Action
		terminal  string
		validator *interactionAuthorityValidatorStub
		want      error
	}{
		"operator action": {
			action:    interaction.ActionStartSession,
			validator: &interactionAuthorityValidatorStub{},
			want:      authority.ErrActionNotAllowed,
		},
		"missing terminal": {
			action:    interaction.ActionUpdateTerminal,
			validator: &interactionAuthorityValidatorStub{},
			want:      authority.ErrInvalidScope,
		},
		"expired durable lease": {
			action: interaction.ActionHeartbeatSession,
			validator: &interactionAuthorityValidatorStub{value: &interaction.SessionAuthorityValidation{
				WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
				NodeID: "node-1", LeaseID: "lease-1", FencingToken: 7, ExpiresAt: now,
			}},
			want: interaction.ErrNotOwner,
		},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := newInteractionSessionAuthorityResolver(
				testCase.validator,
				issuer,
				func() time.Time { return now },
			)
			token := interaction.NewLeaseToken([]byte("raw-token"))
			_, err := resolver.ResolveSessionAuthority(
				t.Context(),
				testCase.action,
				interaction.SessionAuthorityProof{
					WorkspaceKey: "WS", SessionID: "session-1", AgentID: "agent-1",
					TerminalID: testCase.terminal, NodeID: "node-1", LeaseID: "lease-1",
					FencingToken: 7, Token: token,
				},
			)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
			if len(token.Bytes()) != 0 {
				t.Fatal("failed authority derivation retained raw token")
			}
		})
	}
}

func TestInteractionRuntimeAuthorityProviderAllowsOnlyRegisteredRecoveryPair(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	provider := newInteractionRuntimeAuthorityProvider(issuer, func() time.Time { return now })
	value, err := provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.SessionRecoveryComponentID,
		"WS",
		interaction.ActionReconcileSessions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if value.Subject() != string(interaction.SessionRecoveryComponentID) ||
		value.Workspace() != "WS" ||
		value.Action() != interaction.ActionReconcileSessions ||
		value.Reason() != "registered Interaction session recovery pass" {
		t.Fatalf("runtime authority = %+v", value)
	}
	startRecovery, err := provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.SessionRecoveryComponentID,
		"WS",
		interaction.ActionRecoverStart,
	)
	if err != nil {
		t.Fatal(err)
	}
	if startRecovery.Subject() != string(interaction.SessionRecoveryComponentID) ||
		startRecovery.Workspace() != "WS" ||
		startRecovery.Action() != interaction.ActionRecoverStart ||
		startRecovery.Reason() != "registered Interaction session recovery pass" {
		t.Fatalf("start-recovery runtime authority = %+v", startRecovery)
	}
	inbox, err := provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.InboxDeliveryComponentID,
		"WS",
		interaction.ActionEnqueueInbox,
	)
	if err != nil {
		t.Fatal(err)
	}
	if inbox.Subject() != string(interaction.InboxDeliveryComponentID) ||
		inbox.Workspace() != "WS" ||
		inbox.Action() != interaction.ActionEnqueueInbox ||
		inbox.Reason() != "registered Interaction inbox delivery" {
		t.Fatalf("inbox runtime authority = %+v", inbox)
	}
	lifecycle, err := provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.TerminalLifecycleComponentID,
		"WS",
		interaction.ActionForceInterrupt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle.Subject() != string(interaction.TerminalLifecycleComponentID) ||
		lifecycle.Workspace() != "WS" ||
		lifecycle.Action() != interaction.ActionForceInterrupt ||
		lifecycle.Reason() != "registered Interaction terminal lifecycle interrupt" {
		t.Fatalf("terminal lifecycle runtime authority = %+v", lifecycle)
	}
	for _, action := range []authority.Action{
		interaction.ActionDeliverChatMessage,
		interaction.ActionDeliverAssignment,
	} {
		chat, err := provider.AuthorityForInteractionRuntime(
			t.Context(),
			interaction.ChatDeliveryComponentID,
			"WS",
			action,
		)
		if err != nil {
			t.Fatal(err)
		}
		if chat.Subject() != string(interaction.ChatDeliveryComponentID) ||
			chat.Workspace() != "WS" ||
			chat.Action() != action ||
			chat.Reason() != "registered Interaction chat delivery" {
			t.Fatalf("chat runtime authority = %+v", chat)
		}
	}
	_, err = provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.InboxDeliveryComponentID,
		"WS",
		interaction.ActionReconcileSessions,
	)
	if !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("mismatched registered pair error = %v", err)
	}
	_, err = provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.TerminalLifecycleComponentID,
		"WS",
		interaction.ActionReconcileSessions,
	)
	if !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("mismatched terminal lifecycle pair error = %v", err)
	}
	_, err = provider.AuthorityForInteractionRuntime(
		t.Context(),
		interaction.ChatDeliveryComponentID,
		"WS",
		interaction.ActionReadConversation,
	)
	if !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("mismatched chat delivery pair error = %v", err)
	}
	_, err = provider.AuthorityForInteractionRuntime(
		t.Context(),
		platformruntime.ComponentID("unknown"),
		"WS",
		interaction.ActionReconcileSessions,
	)
	if !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("unknown component error = %v", err)
	}
}
