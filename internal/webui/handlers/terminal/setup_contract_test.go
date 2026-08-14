package terminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
	loomapi "github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type terminalSetupStub struct {
	interaction.TerminalTabs
	workspace string
	request   interaction.TerminalSetupRequest
}

func (stub *terminalSetupStub) StartSetup(
	_ context.Context,
	workspace string,
	request interaction.TerminalSetupRequest,
) (*interaction.TerminalSetupResult, error) {
	stub.workspace = workspace
	stub.request = request
	return &interaction.TerminalSetupResult{
		SessionName: "TEST--lead-shell-setup-codex",
		Label:       "Codex Setup",
		Backend:     request.Backend,
		Action:      request.Action,
		Command:     "codex --version",
		Title:       "Test Codex",
		Message:     "Run the Codex version check.",
		Created:     true,
	}, nil
}

func TestTerminalSetupHTTPMapsGeneratedContractToInteraction(t *testing.T) {
	stub := &terminalSetupStub{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/TEST/terminal/setup",
		strings.NewReader(`{"backend":"codex","action":"test"}`),
	)
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "TEST"))
	recorder := httptest.NewRecorder()

	HandleStartTerminalSetup(stub).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if stub.workspace != "TEST" || stub.request != (interaction.TerminalSetupRequest{Backend: "codex", Action: "test"}) {
		t.Fatalf("owner request = (%q, %#v)", stub.workspace, stub.request)
	}
	var envelope struct {
		Success bool                         `json:"success"`
		Data    *loomapi.TerminalSetupResult `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !envelope.Success || envelope.Data == nil {
		t.Fatalf("response envelope = %#v", envelope)
	}
	if envelope.Data.Backend != loomapi.Codex || envelope.Data.Action != loomapi.TerminalSetupResultActionTest {
		t.Fatalf("generated setup result = %#v", envelope.Data)
	}
}
