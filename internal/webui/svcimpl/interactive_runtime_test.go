package svcimpl

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type interactiveRuntimePTYFake struct {
	live   map[terminal.SessionKey]bool
	closed map[terminal.SessionKey]bool
	killed []terminal.SessionKey
}

func (fake *interactiveRuntimePTYFake) HasSession(key terminal.SessionKey) bool {
	return fake.live[key]
}

func (fake *interactiveRuntimePTYFake) SessionClosed(key terminal.SessionKey) bool {
	return fake.closed[key]
}

func (fake *interactiveRuntimePTYFake) Kill(key terminal.SessionKey) error {
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
	keyA := terminal.SessionKey{Workspace: "TEST2", Name: "term_a"}
	keyB := terminal.SessionKey{Workspace: "TEST2", Name: "term_b"}
	ptys := &interactiveRuntimePTYFake{live: map[terminal.SessionKey]bool{keyA: true, keyB: true}}
	controller := NewInteractiveRuntimeController(interactiveRuntimeTabSourceFake{tabs: []InteractiveRuntimeTab{
		{SessionName: keyA.Name, Kind: "agent", AgentID: "agent-a", PTYAlive: true},
		{SessionName: keyB.Name, Kind: "agent", AgentID: "agent-b", PTYAlive: true},
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
	keyA := terminal.SessionKey{Workspace: "TEST2", Name: "term_a"}
	keyB := terminal.SessionKey{Workspace: "TEST2", Name: "term_b"}
	ptys := &interactiveRuntimePTYFake{live: map[terminal.SessionKey]bool{keyA: true, keyB: true}}
	controller := NewInteractiveRuntimeController(interactiveRuntimeTabSourceFake{tabs: []InteractiveRuntimeTab{
		{SessionName: keyA.Name, Kind: "agent", AgentID: "agent-a", PTYAlive: true},
		{SessionName: keyB.Name, Kind: "agent", AgentID: "agent-b", PTYAlive: true},
	}}, ptys)
	runtime := NewCanonicalInteractiveAgentRuntime(controller)

	if err := runtime.StopAgent(t.Context(), "TEST2", "agent-a"); err != nil {
		t.Fatalf("StopAgent: %v", err)
	}
	if len(ptys.killed) != 1 || ptys.killed[0] != keyA {
		t.Fatalf("killed = %+v, want only %+v", ptys.killed, keyA)
	}
}
