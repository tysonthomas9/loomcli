package agentcoord

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type interactiveRuntimePTYFake struct {
	live   map[interaction.TerminalKey]bool
	closed map[interaction.TerminalKey]bool
	killed []interaction.TerminalKey
}

func (fake *interactiveRuntimePTYFake) IsLive(key interaction.TerminalKey) bool {
	return fake.live[key]
}

func (fake *interactiveRuntimePTYFake) IsClosed(key interaction.TerminalKey) bool {
	return fake.closed[key]
}

func (fake *interactiveRuntimePTYFake) Kill(key interaction.TerminalKey) error {
	fake.killed = append(fake.killed, key)
	return nil
}

type interactiveRuntimeTabSourceFake struct {
	tabs []InteractiveRuntimeTab
}

func (fake interactiveRuntimeTabSourceFake) ListInteractiveRuntimeTabs(context.Context, string) ([]InteractiveRuntimeTab, error) {
	return append([]InteractiveRuntimeTab(nil), fake.tabs...), nil
}

func TestInteractiveRuntimeControllerBindsPTYsThroughServerTabMetadata(t *testing.T) {
	keyA := interaction.TerminalKey{WorkspaceKey: "TEST2", TerminalID: "term_a"}
	keyB := interaction.TerminalKey{WorkspaceKey: "TEST2", TerminalID: "term_b"}
	ptys := &interactiveRuntimePTYFake{live: map[interaction.TerminalKey]bool{keyA: true, keyB: true}}
	controller := NewInteractiveRuntimeController(interactiveRuntimeTabSourceFake{tabs: []InteractiveRuntimeTab{
		{SessionName: keyA.TerminalID, Kind: "agent", AgentID: "agent-a", PTYAlive: true},
		{SessionName: keyB.TerminalID, Kind: "agent", AgentID: "agent-b", PTYAlive: true},
	}}, ptys)

	owned, err := controller.OwnedAgentSessions(t.Context(), "TEST2", "agent-a")
	if err != nil {
		t.Fatalf("OwnedAgentSessions: %v", err)
	}
	if len(owned) != 1 || owned[0].Key != keyA || !owned[0].Live {
		t.Fatalf("agent-a owned sessions = %+v, want only %+v", owned, keyA)
	}
}

func TestCanonicalInteractiveRuntimeCannotKillAnotherAgentsPTY(t *testing.T) {
	keyA := interaction.TerminalKey{WorkspaceKey: "TEST2", TerminalID: "term_a"}
	keyB := interaction.TerminalKey{WorkspaceKey: "TEST2", TerminalID: "term_b"}
	ptys := &interactiveRuntimePTYFake{live: map[interaction.TerminalKey]bool{keyA: true, keyB: true}}
	controller := NewInteractiveRuntimeController(interactiveRuntimeTabSourceFake{tabs: []InteractiveRuntimeTab{
		{SessionName: keyA.TerminalID, Kind: "agent", AgentID: "agent-a", PTYAlive: true},
		{SessionName: keyB.TerminalID, Kind: "agent", AgentID: "agent-b", PTYAlive: true},
	}}, ptys)
	runtime := NewCanonicalInteractiveAgentRuntime(controller)

	if err := runtime.StopAgent(t.Context(), "TEST2", "agent-a"); err != nil {
		t.Fatalf("StopAgent: %v", err)
	}
	if len(ptys.killed) != 1 || ptys.killed[0] != keyA {
		t.Fatalf("killed = %+v, want only %+v", ptys.killed, keyA)
	}
}
