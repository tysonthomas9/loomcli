package terminal

import (
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/interaction"
)

type moduleTerminalTabsStub struct{ interaction.TerminalTabs }

func TestModuleRegistersTerminalWebSocketFromInteractionService(t *testing.T) {
	module := NewModule(
		&moduleTerminalTabsStub{}, nil, nil, nil, "", nil, nil,
		time.Time{}, InteractionDependencies{},
	)
	mux := http.NewServeMux()
	module.Register(mux)

	request, err := http.NewRequest(http.MethodGet, "/api/workspaces/ws1/terminal/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, pattern := mux.Handler(request)
	if pattern == "" {
		t.Fatal("terminal WebSocket route was not registered from the Interaction service")
	}
}

func TestModuleSkipsTerminalWebSocketWithoutInteractionService(t *testing.T) {
	module := NewModule(
		nil, nil, nil, nil, "", nil, nil,
		time.Time{}, InteractionDependencies{},
	)
	mux := http.NewServeMux()
	module.Register(mux)
	request, _ := http.NewRequest(http.MethodGet, "/api/workspaces/ws1/terminal/ws", nil)
	_, pattern := mux.Handler(request)
	if pattern != "" {
		t.Fatalf("unexpected terminal route pattern %q", pattern)
	}
}
