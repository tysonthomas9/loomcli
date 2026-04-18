package handlermux

import (
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
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
	CSPReport           http.HandlerFunc
	AuthConfig          http.HandlerFunc
	Stats               http.HandlerFunc
	Metrics             http.HandlerFunc // pre-built by caller (requires fleet types)
	DaemonStatus        http.HandlerFunc
	GetBackendConfig    http.HandlerFunc
	PatchBackendConfig  http.HandlerFunc
	GetTerminalConfig   http.HandlerFunc
	GetBackendsHealth   http.HandlerFunc // pre-built by caller (requires ops types), may be nil
	ListEditors         http.HandlerFunc
	OpenEditor          http.HandlerFunc
	NotifySessionChange http.HandlerFunc // pre-built by caller, may be nil
	DaemonSupervisor    http.HandlerFunc
	DaemonConfig        http.HandlerFunc

	// Closers for cleanup
	ClientErrLimiter Stopper
	CSPLimiter       Stopper
	AuthCfgLimiter   Stopper
}

// Stopper has a Stop method for cleanup.
type Stopper interface {
	Stop()
}

// HandlerDeps holds the dependencies for building top-level handlers.
type HandlerDeps struct {
	Pool               daemon.Pool
	Hub                *realtime.Hub // may be nil
	ExtAuthURL         string
	BackendsHealthH    http.HandlerFunc // pre-built; nil disables endpoint
	NotifyToken        string
	DaemonSupervisor   http.HandlerFunc    // pre-built; nil = disabled
	DaemonConfig       http.HandlerFunc    // pre-built; nil = disabled
	FleetTimeoutsFn    func() int64        // nil = no fleet
	ClaimMetrics       *fleet.ClaimMetrics // nil = no fleet
	TerminalGraceMS    int64               // 0 = disabled
	TerminalIdleMS     int64               // 0 = disabled
	TerminalMaxSession int                 // 0 = unknown
}

// BuildHandlers constructs all top-level HTTP handlers.
func BuildHandlers(deps HandlerDeps) *Handlers {
	clientErrLimiter := misc.NewClientErrorLimiter(rate.Limit(10.0/60.0), 10, 5*time.Minute, 10*time.Minute)
	cspLimiter := misc.NewCSPReportLimiter(rate.Limit(1.0), 20, 5*time.Minute, 10*time.Minute)
	authCfgLimiter := misc.NewAuthConfigLimiter(rate.Limit(5), 10, 5*time.Minute, 10*time.Minute)

	editorCache := misc.NewDefaultEditorCache()

	h := &Handlers{
		Health:             healthhandlers.HandleHealth(deps.Pool),
		APIHealth:          healthhandlers.HandleAPIHealth(deps.Pool),
		ClientErrors:       misc.HandleClientErrors(clientErrLimiter),
		CSPReport:          misc.HandleCSPReport(cspLimiter),
		AuthConfig:         misc.HandleAuthConfig(deps.ExtAuthURL, authCfgLimiter),
		Stats:              healthhandlers.HandleStats(deps.Pool),
		Metrics:            healthhandlers.HandleMetrics(deps.Hub, deps.FleetTimeoutsFn, deps.ClaimMetrics),
		DaemonStatus:       healthhandlers.HandleDaemonStatus(deps.Pool),
		GetBackendConfig:   hterminal.HandleGetBackendConfig(deps.Pool),
		PatchBackendConfig: hterminal.HandlePatchBackendConfig(deps.Pool),
		GetTerminalConfig: hterminal.HandleGetTerminalConfig(hterminal.TerminalLifecycleConfig{
			GracePeriodMS: deps.TerminalGraceMS,
			IdleTimeoutMS: deps.TerminalIdleMS,
			MaxSessions:   deps.TerminalMaxSession,
		}),
		ListEditors:      misc.HandleListEditors(editorCache),
		OpenEditor:       misc.HandleOpenEditorDefault(editorCache),
		DaemonSupervisor: deps.DaemonSupervisor,
		DaemonConfig:     deps.DaemonConfig,
		ClientErrLimiter: clientErrLimiter,
		CSPLimiter:       cspLimiter,
		AuthCfgLimiter:   authCfgLimiter,
	}

	h.GetBackendsHealth = deps.BackendsHealthH
	if deps.Hub != nil {
		h.NotifySessionChange = misc.HandleNotifySessionChange(deps.Hub, deps.NotifyToken)
	}

	return h
}
