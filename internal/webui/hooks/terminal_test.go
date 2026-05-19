package hooks

import (
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

func TestTerminalHookLifecycleWithNilManager(t *testing.T) {
	h := NewTerminalHook(nil, nil)
	if h.Name() != "terminal" {
		t.Fatalf("Name = %q", h.Name())
	}
	if h.Critical() {
		t.Fatal("terminal hook should not be critical")
	}

	ctx := &coordinator.RegistrationContext{WorkspaceID: "WS", WorkspacePath: t.TempDir()}
	if err := h.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}
	if _, ok := ctx.Resources()[coordinator.ResourceKeyTerminal]; !ok {
		t.Fatal("terminal resource was not provided")
	}

	h.OnDeregister(coordinator.DeregistrationContext{WorkspaceID: ""})
	h.OnRollback(coordinator.DeregistrationContext{WorkspaceID: "WS"})
}

func TestTerminalHookUsesProvidedLogger(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	h := NewTerminalHook(nil, logger)
	if h.logger != logger {
		t.Fatal("provided logger was not retained")
	}
}
