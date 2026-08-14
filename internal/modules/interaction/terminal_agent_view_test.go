package interaction

import (
	"errors"
	"io"
	"testing"
	"time"
)

type agentTerminalConnectionFake struct{ id, session string }

func (*agentTerminalConnectionFake) Read([]byte) (int, error)            { return 0, io.EOF }
func (*agentTerminalConnectionFake) Write(value []byte) (int, error)     { return len(value), nil }
func (fake *agentTerminalConnectionFake) ConnectionID() string           { return fake.id }
func (fake *agentTerminalConnectionFake) SessionName() string            { return fake.session }
func (*agentTerminalConnectionFake) Killed() <-chan struct{}             { return make(chan struct{}) }
func (*agentTerminalConnectionFake) Resize(string, uint16, uint16) error { return nil }

type agentTerminalRuntimeFake struct {
	session      string
	found        bool
	count        int
	max          int
	attachedName string
	detachedID   string
	err          error
}

func (fake *agentTerminalRuntimeFake) HasSession(name string) bool {
	return fake.found && name == fake.session
}
func (*agentTerminalRuntimeFake) PaneDead(string) bool           { return false }
func (*agentTerminalRuntimeFake) CapturePane(string, int) string { return "scrollback" }
func (fake *agentTerminalRuntimeFake) FindLatestAgentSession(string, string) (string, bool, error) {
	return fake.session, fake.found, fake.err
}
func (fake *agentTerminalRuntimeFake) SessionCount() int { return fake.count }
func (fake *agentTerminalRuntimeFake) MaxSessions() int  { return fake.max }
func (fake *agentTerminalRuntimeFake) Attach(session string, _, _ uint16) (AgentTerminalConnection, error) {
	fake.attachedName = session
	if fake.err != nil {
		return nil, fake.err
	}
	return &agentTerminalConnectionFake{id: "conn-1", session: session}, nil
}
func (fake *agentTerminalRuntimeFake) Detach(id string) error {
	fake.detachedID = id
	return fake.err
}

func TestAgentTerminalViewResolvesAndAttachesServerSelectedSession(t *testing.T) {
	runtime := &agentTerminalRuntimeFake{session: "tmux-owned-session", found: true, max: 4}
	service := NewTerminalTabs(nil, nil, zeroTime(), TerminalDependencies{LiveView: runtime})

	info, err := service.AgentTerminalInfo(t.Context(), "WS", "worker-1")
	if err != nil || info == nil || !info.Live || info.SessionName != "tmux-owned-session" {
		t.Fatalf("info = %#v, err = %v", info, err)
	}
	result, err := service.AttachAgentTerminal(t.Context(), AttachAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "worker-1", Columns: 100, Rows: 30,
	})
	if err != nil || result == nil || result.Connection == nil || result.Monitor == nil {
		t.Fatalf("attach = %#v, err = %v", result, err)
	}
	if runtime.attachedName != "tmux-owned-session" {
		t.Fatalf("adapter attached %q", runtime.attachedName)
	}
	if !result.Monitor.HasSession("tmux-owned-session") || result.Monitor.CapturePane("tmux-owned-session", 10) != "scrollback" {
		t.Fatal("process-neutral monitor did not delegate")
	}
	if err := service.DetachAgentTerminal(t.Context(), result.Connection.ConnectionID()); err != nil {
		t.Fatal(err)
	}
	if runtime.detachedID != "conn-1" {
		t.Fatalf("detached id = %q", runtime.detachedID)
	}
}

func TestAgentTerminalViewAcceptsCanonicalDottedAgentIdentifier(t *testing.T) {
	runtime := &agentTerminalRuntimeFake{session: "loom-ws-worker-agent.one-7", found: true, max: 4}
	service := NewTerminalTabs(nil, nil, zeroTime(), TerminalDependencies{LiveView: runtime})

	info, err := service.AgentTerminalInfo(t.Context(), "WS", "agent.one")
	if err != nil || info == nil || !info.Live {
		t.Fatalf("info = %#v, err = %v", info, err)
	}
}

func TestAgentTerminalViewReportsArchiveAndCapacity(t *testing.T) {
	archiveRuntime := &agentTerminalRuntimeFake{max: 4}
	archive := NewTerminalTabs(nil, nil, zeroTime(), TerminalDependencies{LiveView: archiveRuntime})
	info, err := archive.AgentTerminalInfo(t.Context(), "WS", "worker-1")
	if err != nil || info == nil || info.Live {
		t.Fatalf("archive info = %#v, err = %v", info, err)
	}
	if _, err := archive.AttachAgentTerminal(t.Context(), AttachAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "worker-1",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archive attach error = %v", err)
	}

	fullRuntime := &agentTerminalRuntimeFake{session: "tmux", found: true, count: 2, max: 2}
	full := NewTerminalTabs(nil, nil, zeroTime(), TerminalDependencies{LiveView: fullRuntime})
	if _, err := full.AttachAgentTerminal(t.Context(), AttachAgentTerminalCommand{
		WorkspaceKey: "WS", AgentID: "worker-1",
	}); !errors.Is(err, ErrTerminalCapacity) {
		t.Fatalf("capacity error = %v", err)
	}
}

func zeroTime() (value time.Time) { return value }
