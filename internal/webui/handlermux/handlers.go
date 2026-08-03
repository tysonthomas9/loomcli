package handlermux

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
	healthhandlers "github.com/tysonthomas9/loomcli/internal/webui/handlers/health"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	hterminal "github.com/tysonthomas9/loomcli/internal/webui/handlers/terminal"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// Handlers holds all pre-built top-level HTTP handlers.
type Handlers struct {
	Health              http.HandlerFunc
	APIHealth           http.HandlerFunc
	ClientErrors        http.HandlerFunc
	AuthConfig          http.HandlerFunc
	Metrics             http.HandlerFunc // pre-built by caller (requires fleet types)
	GetTerminalConfig   http.HandlerFunc
	GetBackendsHealth   http.HandlerFunc // pre-built by caller (requires ops types), may be nil
	ListEditors         http.HandlerFunc
	OpenEditor          http.HandlerFunc
	NotifySessionChange http.HandlerFunc // pre-built by caller, may be nil

	// Closers for cleanup
	ClientErrLimiter Stopper
	AuthCfgLimiter   Stopper
}

// Stopper has a Stop method for cleanup.
type Stopper interface {
	Stop()
}

// HandlerDeps holds the dependencies for building top-level handlers.
type HandlerDeps struct {
	Hub                *realtime.Hub // may be nil
	ExtAuthURL         string
	BackendsHealthH    http.HandlerFunc // pre-built; nil disables endpoint
	NotifyToken        string
	FleetTimeoutsFn    func() int64        // nil = no fleet
	ClaimMetrics       *fleet.ClaimMetrics // nil = no fleet
	TerminalGraceMS    int64               // 0 = disabled
	TerminalIdleMS     int64               // 0 = disabled
	TerminalMaxSession int                 // 0 = unknown
	// IssueBackendFn returns the active backend.IssueBackend. Threaded
	// into /api/config so clients can see which backend the server is
	// talking to without peeking at LOOM_ISSUE_BACKEND on the host. Nil
	// is safe — the response falls back to the env var, then empty.
	//
	// ctx carries the per-request workspace ID so cloud-mode wirings can
	// route to a per-workspace fleet-db backend; /api/config callers pass
	// context.Background() since the response is workspace-agnostic.
	IssueBackendFn func(ctx context.Context) backend.IssueBackend
}

// BuildHandlers constructs all top-level HTTP handlers.
func BuildHandlers(deps HandlerDeps) *Handlers {
	clientErrLimiter := misc.NewClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	authCfgLimiter := misc.NewAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)

	editorCache := misc.NewDefaultEditorCache()

	h := &Handlers{
		Health:       healthhandlers.HandleHealth(),
		APIHealth:    healthhandlers.HandleAPIHealth(),
		ClientErrors: misc.HandleClientErrors(clientErrLimiter),
		AuthConfig:   misc.HandleAuthConfig(deps.ExtAuthURL, authCfgLimiter, deps.IssueBackendFn),
		Metrics:      healthhandlers.HandleMetrics(deps.Hub, deps.FleetTimeoutsFn, deps.ClaimMetrics),
		GetTerminalConfig: hterminal.HandleGetTerminalConfig(hterminal.TerminalLifecycleConfig{
			GracePeriodMS: deps.TerminalGraceMS,
			IdleTimeoutMS: deps.TerminalIdleMS,
			MaxSessions:   deps.TerminalMaxSession,
		}),
		ListEditors:      misc.HandleListEditors(editorCache),
		OpenEditor:       misc.HandleOpenEditorDefault(editorCache),
		ClientErrLimiter: clientErrLimiter,
		AuthCfgLimiter:   authCfgLimiter,
	}

	h.GetBackendsHealth = deps.BackendsHealthH
	if deps.Hub != nil {
		h.NotifySessionChange = misc.HandleNotifySessionChange(deps.Hub, deps.NotifyToken)
	}

	return h
}
