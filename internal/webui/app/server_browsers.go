package app

import (
	"fmt"
	"net"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/webui/modbuilder"
)

// buildBrowserModule assembles the durable-browser routes, the agent-session
// registry (bound at PTY spawn), and — in local open mode with a configured
// socket path — the local desktop operator bridge. It must run after the PTY
// manager exists and before routes are registered.
func (app *Server) buildBrowserModule() {
	if app.config.Store == nil {
		return
	}
	app.browsers = modbuilder.NewBrowserWiring(modbuilder.BrowserWiringDeps{
		Store:              app.config.Store,
		Backend:            app.config.BrowserBackend,
		Signer:             app.config.BrowserSigner,
		PermissionResolver: app.config.BrowserPermissionResolver,
		OperatorSocketPath: app.config.BrowserOperatorSocketPath,
		RemoteAuth:         app.config.ExtAuthURL != "",
		PTYMgr:             app.ptyMgr,
		Hub:                app.hub,
		AgentBaseURL:       app.agentBrowserURL,
		Logger:             app.config.Logger,
	})
	app.wsModules = append(app.wsModules, app.browsers.Module)
}

// agentBrowserURL is the loopback-reachable base URL for agent processes,
// which always run on this host.
func (app *Server) agentBrowserURL() string {
	host := strings.TrimSpace(app.config.BindAddress)
	if ip := net.ParseIP(host); host == "" || ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprint(app.actualPort))
}
