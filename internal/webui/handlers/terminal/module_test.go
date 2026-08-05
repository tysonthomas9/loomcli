package terminal

import (
	"net/http"
	"testing"
	"time"

	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// TestModule_Register_TerminalWSRoute_WithMultiPTYManager verifies that
// Module.Register wires GET /api/workspaces/{ws}/terminal/ws when ptyMgr is
// a non-nil *terminal.MultiPTYManager assigned to the PTYSource interface.
// This guards against a typed-nil interface regression at the module wiring
// boundary (modbuilder.TerminalModuleDeps.PTYMgr → Module.ptyMgr).
func TestModule_Register_TerminalWSRoute_WithMultiPTYManager(t *testing.T) {
	mm := webuterminal.NewMultiPTYManager("bash", 0)
	t.Cleanup(func() { _ = mm.Close() })

	// Assign through the PTYSource interface — this is how modbuilder
	// composes the dependency.
	var src webuterminal.PTYSource = mm

	mod := NewModule(
		nil, // termSvc
		nil, // agentSvc
		src, // ptyMgr
		nil, // agentTmuxMgr
		nil, // termAuth
		nil, // allowedOrigins
		"",  // loomServerURL
		nil, // store
		nil, // tabMetaStore
		nil, // hub
		time.Time{},
		InteractionDependencies{},
	)

	mux := http.NewServeMux()
	mod.Register(mux)

	// The terminal WS route should be registered because ptyMgr is non-nil.
	req, err := http.NewRequest(http.MethodGet, "/api/workspaces/ws1/terminal/ws", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	_, pattern := mux.Handler(req)
	if pattern == "" {
		t.Error("expected /api/workspaces/{ws}/terminal/ws to be registered when ptyMgr is non-nil, got empty pattern")
	}
}

// TestModule_Register_TerminalWSRoute_NilPTYMgr verifies that when ptyMgr is
// a nil interface, Module.Register does NOT register the terminal WS route.
// This is the skip-registration path documented on NewModule.
func TestModule_Register_TerminalWSRoute_NilPTYMgr(t *testing.T) {
	mod := NewModule(
		nil, nil, nil, nil, nil, nil, "", nil, nil, nil, time.Time{}, InteractionDependencies{},
	)

	mux := http.NewServeMux()
	mod.Register(mux)

	// Handler lookup for an unregistered route returns the default 404
	// handler with an empty pattern.
	req, _ := http.NewRequest(http.MethodGet, "/api/workspaces/ws1/terminal/ws", nil)
	_, pattern := mux.Handler(req)
	if pattern != "" {
		t.Errorf("expected no registration for nil ptyMgr, got pattern %q", pattern)
	}
}
