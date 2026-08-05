package daemon

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
)

func TestValidateIPCSessionAcceptsExactActiveCredential(t *testing.T) {
	d := newTestIPCDaemon(&mockIPCBackend{})
	d.sup.WorkspaceID = "WS"
	d.sup.Agents = []*supervisor.AgentProcess{activeIPCTestAgent()}

	resp, ok := d.validateIPCSession(validIPCRequest())
	if !ok {
		t.Fatalf("validateIPCSession failed: %+v", resp)
	}
}

func TestValidateIPCSessionRejectsMissingCredential(t *testing.T) {
	d := newTestIPCDaemon(&mockIPCBackend{})
	d.sup.WorkspaceID = "WS"
	d.sup.Agents = []*supervisor.AgentProcess{activeIPCTestAgent()}

	resp, ok := d.validateIPCSession(AgentIPCRequest{AgentName: "planner"})
	if ok {
		t.Fatal("validateIPCSession accepted missing credential")
	}
	if resp.Kind != string(backend.KindValidation) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindValidation, resp)
	}
}

func TestValidateIPCSessionRejectsStaleOrForeignCredential(t *testing.T) {
	d := newTestIPCDaemon(&mockIPCBackend{})
	d.sup.WorkspaceID = "WS"
	d.sup.Agents = []*supervisor.AgentProcess{activeIPCTestAgent()}

	tests := []struct {
		name string
		req  AgentIPCRequest
	}{
		{name: "stale session", req: AgentIPCRequest{AgentName: "planner", SessionID: "old", AuthToken: "token-1"}},
		{name: "wrong token", req: AgentIPCRequest{AgentName: "planner", SessionID: "session-1", AuthToken: "wrong"}},
		{name: "foreign agent", req: AgentIPCRequest{AgentName: "coder", SessionID: "session-1", AuthToken: "token-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp, ok := d.validateIPCSession(test.req)
			if ok {
				t.Fatal("validateIPCSession accepted stale or foreign credential")
			}
			if resp.Kind != string(backend.KindConflict) {
				t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
			}
		})
	}
}

func TestValidateIPCSessionFailsClosedAfterFinalizationRevokesToken(t *testing.T) {
	d := newTestIPCDaemon(&mockIPCBackend{})
	d.sup.WorkspaceID = "WS"
	agent := activeIPCTestAgent()
	d.sup.Agents = []*supervisor.AgentProcess{agent}
	agent.Mu.Lock()
	agent.AgentSessionID = ""
	agent.AgentIPCAuthToken = ""
	agent.Mu.Unlock()

	resp, ok := d.validateIPCSession(validIPCRequest())
	if ok {
		t.Fatal("validateIPCSession accepted a revoked credential")
	}
	if resp.Kind != string(backend.KindConflict) {
		t.Fatalf("response kind = %q, want %q: %+v", resp.Kind, backend.KindConflict, resp)
	}
}

func activeIPCTestAgent() *supervisor.AgentProcess {
	return &supervisor.AgentProcess{
		Entry:             config.AgentEntry{Worktree: "planner"},
		AgentSessionID:    "session-1",
		AgentIPCAuthToken: "token-1",
	}
}

func validIPCRequest() AgentIPCRequest {
	return AgentIPCRequest{
		AgentName: "planner",
		SessionID: "session-1",
		AuthToken: "token-1",
	}
}
