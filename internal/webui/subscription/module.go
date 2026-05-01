package subscription

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Module registers the workspace-scoped SSE event stream routes on a
// [*http.ServeMux]. The module is only constructed when hub is non-nil.
//
// The SSE token exchange route is conditional on sseTokens being non-nil
// (external auth mode only).
type Module struct {
	hub               *realtime.Hub
	getMutationsSince func(wsID string, since string) []rpc.MutationEvent
	workspaceFromCtx  func(context.Context) string
	sseTokens         *realtime.TokenStore // may be nil — token route skipped
}

// NewModule returns a Module. sseTokens may be nil — the token
// exchange route will simply not be registered.
func NewModule(hub *realtime.Hub, getMutationsSince func(string, string) []rpc.MutationEvent, workspaceFromCtx func(context.Context) string, sseTokens *realtime.TokenStore) *Module {
	return &Module{
		hub:               hub,
		getMutationsSince: getMutationsSince,
		workspaceFromCtx:  workspaceFromCtx,
		sseTokens:         sseTokens,
	}
}

// Register implements [Module] by registering 1–2 SSE routes.
func (m *Module) Register(mux *http.ServeMux) {
	// SSE event stream — uses mux.Handle because realtime.NewHandler returns http.Handler
	sseHandler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:               m.hub,
		GetMutationsSince: m.getMutationsSince,
		WorkspaceFromCtx:  m.workspaceFromCtx,
		TokenStore:        m.sseTokens,
	})
	mux.Handle("GET /api/workspaces/{ws}/events", sseHandler)

	// SSE token exchange — conditional on external auth mode
	if m.sseTokens != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/events/token", HandleSSEToken(m.sseTokens))
	}
}
