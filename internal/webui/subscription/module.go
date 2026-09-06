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
	hub                *realtime.Hub
	openMutationSource func(context.Context, string) (realtime.MutationSource, error)
	workspaceFromCtx   func(context.Context) string
	activateWorkspace  func(context.Context, string) (string, error)
	sseTokens          *realtime.TokenStore // may be nil in open auth mode
}

// NewModule returns a Module. sseTokens may be nil in open auth mode.
func NewModule(
	hub *realtime.Hub,
	openMutationSource func(context.Context, string) (realtime.MutationSource, error),
	workspaceFromCtx func(context.Context) string,
	activateWorkspace func(context.Context, string) (string, error),
	sseTokens *realtime.TokenStore,
) *Module {
	return &Module{
		hub:                hub,
		openMutationSource: openMutationSource,
		workspaceFromCtx:   workspaceFromCtx,
		activateWorkspace:  activateWorkspace,
		sseTokens:          sseTokens,
	}
}

// Register implements [Module] by registering workspace-scoped SSE routes.
func (m *Module) Register(mux *http.ServeMux) {
	var recovery *realtime.RecoveryRegistry
	if m.sseTokens != nil && m.openMutationSource != nil {
		recovery = realtime.NewRecoveryRegistry()
	}
	// SSE event stream — uses mux.Handle because realtime.NewHandler returns http.Handler
	sseHandler := realtime.NewHandler(realtime.HandlerConfig{
		Hub:                m.hub,
		OpenMutationSource: m.openMutationSource,
		WorkspaceFromCtx:   m.workspaceFromCtx,
		TokenStore:         m.sseTokens,
		RecoveryRegistry:   recovery,
		OnAuthenticated:    m.activateWorkspace,
	})
	mux.Handle("GET /api/workspaces/{ws}/events", sseHandler)
	mux.HandleFunc("POST /api/workspaces/{ws}/events/recovery/issues", handleIssueRecovery(recovery, m.workspaceFromCtx))

	mux.HandleFunc(
		"GET /api/workspaces/{ws}/events/token",
		handleSSEToken(m.sseTokens, m.workspaceFromCtx),
	)
}
