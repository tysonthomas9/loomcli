package app

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/browsers"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

// Launch-env keys written by the server into agent terminal tab metadata
// (handlers/terminal agentLaunchEnv). Clients cannot set tab launch specs, so
// their presence identifies a server-built interactive agent launch.
const (
	launchEnvAgentName        = "LOOM_AGENT_NAME"
	launchEnvAgentTerminalID  = "LOOM_AGENT_TERMINAL_ID"
	launchEnvOrchestratorSess = "LOOM_ORCHESTRATOR_SESSION_ID"
)

// buildBrowserModule assembles the durable-browser routes, the agent-session
// registry (bound at PTY spawn), and — in local open mode with a configured
// socket path — the local desktop operator bridge. It must run after the PTY
// manager exists and before routes are registered.
func (app *Server) buildBrowserModule() {
	if app.config.Store == nil {
		return
	}
	logger := app.config.Logger
	remote := app.config.ExtAuthURL != ""

	app.agentBrowserSessions = browserauth.NewAgentSessionRegistry()
	if app.ptyMgr != nil {
		app.ptyMgr.SetSpawnHooks(terminal.SpawnHooks{
			ExtraEnv: app.agentBrowserSpawnEnv,
			Ended: func(_ terminal.SessionKey, extra map[string]string) {
				app.agentBrowserSessions.RevokeToken(extra[browserauth.EnvAgentSessionToken])
			},
		})
	}

	var operatorSessions *browserauth.OperatorSessionRegistry
	if !remote && strings.TrimSpace(app.config.BrowserOperatorSocketPath) != "" {
		operatorSessions = app.startBrowserOperatorBridge()
	}

	app.browserModule = browsers.NewModule(browsers.Config{
		Store:             app.config.Store,
		Backend:           app.config.BrowserBackend,
		Signer:            app.config.BrowserSigner,
		AgentSessions:     app.agentBrowserSessions,
		OperatorSessions:  operatorSessions,
		RemoteAuth:        remote,
		ResolvePermission: app.config.BrowserPermissionResolver,
		Notify:            app.browserNotifier(),
		Logger:            logger,
	})
	app.wsModules = append(app.wsModules, app.browserModule)
}

// startBrowserOperatorBridge listens on the local operator socket. It fails
// closed: on error it returns nil and operator routes answer "bridge
// unavailable".
func (app *Server) startBrowserOperatorBridge() *browserauth.OperatorSessionRegistry {
	logger := app.config.Logger
	registry := browserauth.NewOperatorSessionRegistry()
	st := app.config.Store
	sock, err := browserauth.ListenOperatorSocket(app.config.BrowserOperatorSocketPath, registry,
		func(ctx context.Context, ws string) error {
			if strings.TrimSpace(ws) == "" {
				return fmt.Errorf("workspace is required")
			}
			_, err := st.Workspaces().Get(ctx, ws)
			return err
		}, logger)
	if err != nil {
		if logger != nil {
			logger.Error("local browser operator bridge disabled", "socket", app.config.BrowserOperatorSocketPath, "err", err)
		}
		return nil
	}
	app.browserOperatorSocket = sock
	if logger != nil {
		logger.Info("local browser operator bridge listening", "socket", sock.Path())
	}
	return registry
}

// browserNotifier publishes post-commit browser changes over SSE. Only the
// owner name crosses SSE; clients refetch through their own authorized
// browser route.
func (app *Server) browserNotifier() browsers.Notifier {
	if app.hub == nil {
		return nil
	}
	hub := app.hub
	return func(ws, owner, action string) {
		typ := "update"
		if action == "browser.create" {
			typ = "create"
		}
		hub.Broadcast(&realtime.MutationPayload{
			Type:        typ,
			EntityType:  "browser",
			EntityID:    owner,
			Action:      action,
			WorkspaceID: ws,
			Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
}

// agentBrowserSpawnEnv binds a fresh agent-session bearer to an interactive
// agent's PTY at spawn. It runs under the PTY manager lock: memory only.
func (app *Server) agentBrowserSpawnEnv(key terminal.SessionKey, launch *tabmeta.LaunchSpec) map[string]string {
	if launch == nil || app.agentBrowserSessions == nil {
		return nil
	}
	agent := strings.TrimSpace(launch.Env[launchEnvAgentName])
	orchestrator := strings.TrimSpace(launch.Env[launchEnvOrchestratorSess])
	terminalID := strings.TrimSpace(launch.Env[launchEnvAgentTerminalID])
	// Only interactive agents get an orchestrator session id, and the
	// terminal id must be the session being spawned.
	if agent == "" || orchestrator == "" || terminalID == "" || terminalID != key.Name || key.Workspace == "" {
		return nil
	}
	token, _, err := app.agentBrowserSessions.Issue(key.Workspace, agent, orchestrator, key.Name)
	if err != nil {
		if app.config.Logger != nil {
			app.config.Logger.Warn("agent browser session binding failed", "workspace", key.Workspace, "agent", agent, "err", err)
		}
		return nil
	}
	return map[string]string{
		browserauth.EnvAgentSessionToken: token,
		browserauth.EnvAgentBrowserURL:   app.agentBrowserURL(),
	}
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

// closeBrowserBridge stops the operator socket and revokes every local
// operator session. Agent bindings die with their PTYs.
func (app *Server) closeBrowserBridge() {
	if app.browserOperatorSocket != nil {
		_ = app.browserOperatorSocket.Close()
	}
}
