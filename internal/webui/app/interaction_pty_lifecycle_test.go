package app

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type interactionPTYTabStoreStub struct {
	value *tabmeta.TabMetadata
	err   error
}

func (stub *interactionPTYTabStoreStub) Get(
	context.Context,
	string,
	string,
) (*tabmeta.TabMetadata, error) {
	return stub.value, stub.err
}

type interactionPTYForceStub struct {
	commands []interaction.ForceInterruptCommand
	err      error
}

func (stub *interactionPTYForceStub) ForceInterrupt(
	_ context.Context,
	command interaction.ForceInterruptCommand,
) (interaction.ForceInterruptResult, error) {
	stub.commands = append(stub.commands, command)
	return interaction.ForceInterruptResult{Changed: true}, stub.err
}

func TestInteractionPTYBeforeKillUsesExactServerOwnedPlacement(t *testing.T) {
	tabs := &interactionPTYTabStoreStub{value: &tabmeta.TabMetadata{
		SessionName: "agent-tab", Workspace: "WS", Kind: "agent",
		AgentID: "agent-1", InteractionSessionID: "session-1",
		InteractionTerminalID: "terminal-1",
		InteractionLeaseID:    "lease-1", InteractionLeaseFencingToken: 7,
	}}
	force := &interactionPTYForceStub{}
	hook := NewInteractionPTYBeforeKill(tabs, force)
	key := terminal.SessionKey{Workspace: "WS", Name: "agent-tab"}
	if err := hook(t.Context(), key, terminal.ExitReasonShutdown); err != nil {
		t.Fatal(err)
	}
	if len(force.commands) != 1 {
		t.Fatalf("force commands = %+v", force.commands)
	}
	command := force.commands[0]
	if command.WorkspaceKey != key.Workspace ||
		command.SessionID != "session-1" ||
		command.AgentID != "agent-1" ||
		command.TerminalID != "terminal-1" ||
		command.ExpectedLeaseID != "lease-1" ||
		command.ExpectedLeaseFencingToken != 7 ||
		command.StreamRef != "terminal:WS/agent-tab" ||
		command.TerminalTab != key.Name ||
		command.Reason != "server PTY shutdown" {
		t.Fatalf("force command = %+v", command)
	}
}

func TestInteractionPTYBeforeKillFailsClosedForPartialCanonicalIdentity(t *testing.T) {
	tabs := &interactionPTYTabStoreStub{value: &tabmeta.TabMetadata{
		SessionName: "agent-tab", Workspace: "WS", Kind: "agent",
		AgentID: "agent-1", InteractionSessionID: "session-1",
		InteractionTerminalID: "terminal-1",
	}}
	force := &interactionPTYForceStub{}
	hook := NewInteractionPTYBeforeKill(tabs, force)
	if err := hook(
		t.Context(),
		terminal.SessionKey{Workspace: "WS", Name: "agent-tab"},
		terminal.ExitReasonKilled,
	); err == nil {
		t.Fatal("partial canonical identity was allowed")
	}
	if len(force.commands) != 0 {
		t.Fatalf("partial identity reached force command: %+v", force.commands)
	}
}
