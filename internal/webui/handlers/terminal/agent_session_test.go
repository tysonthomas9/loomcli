package terminal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type ensureAgentTerminalStub struct {
	interaction.TerminalTabs
	command interaction.EnsureAgentTerminalCommand
	result  *interaction.TabMetadata
	err     error
	calls   int
}

func (stub *ensureAgentTerminalStub) EnsureAgentTerminal(
	_ context.Context,
	command interaction.EnsureAgentTerminalCommand,
) (*interaction.TabMetadata, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func TestEnsureAgentTerminalHTTPDelegatesToInteraction(t *testing.T) {
	stub := &ensureAgentTerminalStub{result: &interaction.TabMetadata{
		Workspace: "WS", SessionName: "term_agent", AgentID: "reviewer",
		InteractionSessionID: "private-session", InteractionTerminalID: "private-terminal",
		InteractionLeaseID: "private-lease", InteractionLeaseFencingToken: 7,
		Launch: &interaction.LaunchSpec{Argv: []string{"codex", "private-argument"}},
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/agents/reviewer/terminal/session", nil)
	request.SetPathValue("name", "reviewer")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "WS"))
	recorder := httptest.NewRecorder()

	HandleEnsureAgentTerminalSession(stub).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if stub.calls != 1 || stub.command.WorkspaceKey != "WS" || stub.command.AgentID != "reviewer" {
		t.Fatalf("Interaction command = %#v, calls = %d", stub.command, stub.calls)
	}
	if !strings.Contains(recorder.Body.String(), `"session_name":"term_agent"`) {
		t.Fatalf("response body = %s", recorder.Body.String())
	}
	for _, private := range []string{"launch", "private-argument", "interaction_session_id", "private-session", "private-lease"} {
		if strings.Contains(recorder.Body.String(), private) {
			t.Fatalf("response leaked private owner field %q: %s", private, recorder.Body.String())
		}
	}
}

func TestEnsureAgentTerminalHTTPRejectsInvalidIdentityBeforeOwnerCall(t *testing.T) {
	stub := &ensureAgentTerminalStub{}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("name", "bad agent")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "WS"))
	recorder := httptest.NewRecorder()

	HandleEnsureAgentTerminalSession(stub).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || stub.calls != 0 {
		t.Fatalf("status = %d, calls = %d, body = %s", recorder.Code, stub.calls, recorder.Body.String())
	}
}

func TestEnsureAgentTerminalHTTPMapsOwnerAvailabilityFailure(t *testing.T) {
	stub := &ensureAgentTerminalStub{err: errors.Join(interaction.ErrUnavailable, errors.New("adapter detail"))}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.SetPathValue("name", "reviewer")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "WS"))
	recorder := httptest.NewRecorder()

	HandleEnsureAgentTerminalSession(stub).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "adapter detail") {
		t.Fatalf("response leaked owner diagnostic: %s", recorder.Body.String())
	}
}
