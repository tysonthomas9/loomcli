package subscription

import (
	"context"
	"net/http"

	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Module registers the workspace-scoped SSE event stream routes on a
// [*http.ServeMux]. The module is only constructed when hub is non-nil.
//
// The SSE token exchange route is always registered. In open mode, when
// sseTokens is nil, it returns a cache-disabled "disabled" response so browsers
// do not emit noisy 404 console errors before connecting directly.
type Module struct {
	hub               *realtime.Hub
	getMutationsSince func(wsID string, since string) []realtime.MutationEvent
	workspaceFromCtx  func(context.Context) string
	activateWorkspace func(context.Context, string)
	sseTokens         *realtime.TokenStore // may be nil in open auth mode
}

// NewModule returns a Module. sseTokens may be nil in open auth mode.
func NewModule(
	hub *realtime.Hub,
	getMutationsSince func(string, string) []realtime.MutationEvent,
	workspaceFromCtx func(context.Context) string,
	activateWorkspace func(context.Context, string),
	sseTokens *realtime.TokenStore,
) *Module {
	return &Module{
		hub:               hub,
		getMutationsSince: getMutationsSince,
		workspaceFromCtx:  workspaceFromCtx,
		activateWorkspace: activateWorkspace,
		sseTokens:         sseTokens,
	}
}

// Register implements [Module] by registering workspace-scoped SSE routes.
func (m *Module) Register(mux *http.ServeMux) {
	// SSE event stream — uses mux.Handle because realtime.NewHandler returns http.Handler
	sseHandler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:               m.hub,
		GetMutationsSince: m.getMutationsSince,
		WorkspaceFromCtx:  m.workspaceFromCtx,
		TokenStore:        m.sseTokens,
		OnAuthenticated:   m.activateWorkspace,
	})
	mux.Handle("GET /api/workspaces/{ws}/events", sseHandler)

	mux.HandleFunc(
		"GET /api/workspaces/{ws}/events/token",
		HandleSSETokenWithActivation(m.sseTokens, m.workspaceFromCtx, m.activateWorkspace),
	)
}
