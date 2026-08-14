package app

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type interactionPTYTabStoreStub struct {
	value *interaction.TabMetadata
	err   error
}

func (stub *interactionPTYTabStoreStub) Get(
	context.Context,
	string,
	string,
) (*interaction.TabMetadata, error) {
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
	tabs := &interactionPTYTabStoreStub{value: &interaction.TabMetadata{
		SessionName: "agent-tab", Workspace: "WS", Kind: "agent",
		AgentID: "agent-1", InteractionSessionID: "session-1",
		InteractionTerminalID: "terminal-1",
		InteractionLeaseID:    "lease-1", InteractionLeaseFencingToken: 7,
	}}
	force := &interactionPTYForceStub{}
	hook := NewInteractionPTYBeforeKill(tabs, force)
	key := interaction.TerminalKey{WorkspaceKey: "WS", TerminalID: "agent-tab"}
	if err := hook(t.Context(), key, "shutdown"); err != nil {
		t.Fatal(err)
	}
	if len(force.commands) != 1 {
		t.Fatalf("force commands = %+v", force.commands)
	}
	command := force.commands[0]
	if command.WorkspaceKey != key.WorkspaceKey ||
		command.SessionID != "session-1" ||
		command.AgentID != "agent-1" ||
		command.TerminalID != "terminal-1" ||
		command.ExpectedLeaseID != "lease-1" ||
		command.ExpectedLeaseFencingToken != 7 ||
		command.StreamRef != "terminal:WS/agent-tab" ||
		command.TerminalTab != key.TerminalID ||
		command.Reason != "server PTY shutdown" {
		t.Fatalf("force command = %+v", command)
	}
}

func TestInteractionPTYBeforeKillFailsClosedForPartialCanonicalIdentity(t *testing.T) {
	tabs := &interactionPTYTabStoreStub{value: &interaction.TabMetadata{
		SessionName: "agent-tab", Workspace: "WS", Kind: "agent",
		AgentID: "agent-1", InteractionSessionID: "session-1",
		InteractionTerminalID: "terminal-1",
	}}
	force := &interactionPTYForceStub{}
	hook := NewInteractionPTYBeforeKill(tabs, force)
	if err := hook(
		t.Context(),
		interaction.TerminalKey{WorkspaceKey: "WS", TerminalID: "agent-tab"},
		"killed",
	); err == nil {
		t.Fatal("partial canonical identity was allowed")
	}
	if len(force.commands) != 0 {
		t.Fatalf("partial identity reached force command: %+v", force.commands)
	}
}
