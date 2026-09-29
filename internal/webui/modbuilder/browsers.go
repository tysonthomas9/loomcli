package modbuilder

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// BrowserWiringDeps holds dependencies for the durable interactive-agent
// browser routes and their session bridges.
type BrowserWiringDeps struct {
	Store              store.Store
	Backend            browsers.Backend
	Signer             browsers.Minter
	PermissionResolver browsers.PermissionResolver
	// OperatorSocketPath enables the local desktop operator bridge; it is
	// never started when RemoteAuth is set.
	OperatorSocketPath string
	RemoteAuth         bool
	PTYMgr             *terminal.MultiPTYManager // may be nil
	Hub                *realtime.Hub             // may be nil
	// AgentBaseURL returns the loopback-reachable server URL handed to agent
	// processes; it is called at PTY spawn, after the server is listening.
	AgentBaseURL func() string
	Logger       *slog.Logger
}

// BrowserWiring owns the browser route module, the agent-session registry
// (bound at PTY spawn), and the optional local operator socket.
type BrowserWiring struct {
	Module         *browsers.Module
	agentSessions  *browserauth.AgentSessionRegistry
	operatorSocket *browserauth.OperatorSocketServer
	deps           BrowserWiringDeps
}

// NewBrowserWiring assembles the browser module. It must run after the PTY
// manager exists and before routes are registered.
func NewBrowserWiring(deps BrowserWiringDeps) *BrowserWiring {
	w := &BrowserWiring{
		agentSessions: browserauth.NewAgentSessionRegistry(),
		deps:          deps,
	}
	if deps.PTYMgr != nil {
		deps.PTYMgr.SetSpawnHooks(terminal.SpawnHooks{
			ExtraEnv: w.SpawnEnv,
			Ended: func(_ terminal.SessionKey, extra map[string]string) {
				w.agentSessions.RevokeToken(extra[browserauth.EnvAgentSessionToken])
			},
		})
	}

	var operatorSessions *browserauth.OperatorSessionRegistry
	if !deps.RemoteAuth && strings.TrimSpace(deps.OperatorSocketPath) != "" {
		operatorSessions = w.startOperatorBridge()
	}

	w.Module = browsers.NewModule(browsers.Config{
		Store:             deps.Store,
		Backend:           deps.Backend,
		Signer:            deps.Signer,
		AgentSessions:     w.agentSessions,
		OperatorSessions:  operatorSessions,
		RemoteAuth:        deps.RemoteAuth,
		ResolvePermission: deps.PermissionResolver,
		Notify:            w.notifier(),
		Logger:            deps.Logger,
	})
	return w
}

// RegisterAgentRoutes registers the agent-session routes, which authenticate
// in their own handlers. Safe on a nil receiver.
func (w *BrowserWiring) RegisterAgentRoutes(mux *http.ServeMux) {
	if w == nil {
		return
	}
	w.Module.RegisterAgentRoutes(mux)
}

// AgentSessions exposes the agent-session registry.
func (w *BrowserWiring) AgentSessions() *browserauth.AgentSessionRegistry {
	return w.agentSessions
}

// OperatorBridgeListening reports whether the local operator socket is up.
func (w *BrowserWiring) OperatorBridgeListening() bool {
	return w != nil && w.operatorSocket != nil
}

// Close stops the operator socket and revokes every local operator session.
// Agent bindings die with their PTYs. Safe on a nil receiver.
func (w *BrowserWiring) Close() {
	if w != nil && w.operatorSocket != nil {
		_ = w.operatorSocket.Close()
	}
}

// startOperatorBridge listens on the local operator socket. It fails closed:
// on error it returns nil and operator routes answer "bridge unavailable".
func (w *BrowserWiring) startOperatorBridge() *browserauth.OperatorSessionRegistry {
	logger := w.deps.Logger
	registry := browserauth.NewOperatorSessionRegistry()
	st := w.deps.Store
	sock, err := browserauth.ListenOperatorSocket(w.deps.OperatorSocketPath, registry,
		func(ctx context.Context, ws string) error {
			if strings.TrimSpace(ws) == "" {
				return fmt.Errorf("workspace is required")
			}
			_, err := st.Workspaces().Get(ctx, ws)
			return err
		}, logger)
	if err != nil {
		if logger != nil {
			logger.Error("local browser operator bridge disabled", "socket", w.deps.OperatorSocketPath, "err", err)
		}
		return nil
	}
	w.operatorSocket = sock
	if logger != nil {
		logger.Info("local browser operator bridge listening", "socket", sock.Path())
	}
	return registry
}

// notifier publishes post-commit browser changes over SSE. Only the owner
// name crosses SSE; clients refetch through their own authorized browser
// route.
func (w *BrowserWiring) notifier() browsers.Notifier {
	hub := w.deps.Hub
	if hub == nil {
		return nil
	}
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

// SpawnEnv binds a fresh agent-session bearer to an interactive agent's PTY
// at spawn. It runs under the PTY manager lock: memory only.
func (w *BrowserWiring) SpawnEnv(key terminal.SessionKey, launch *tabmeta.LaunchSpec) map[string]string {
	if launch == nil {
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
	token, _, err := w.agentSessions.Issue(key.Workspace, agent, orchestrator, key.Name)
	if err != nil {
		if w.deps.Logger != nil {
			w.deps.Logger.Warn("agent browser session binding failed", "workspace", key.Workspace, "agent", agent, "err", err)
		}
		return nil
	}
	baseURL := ""
	if w.deps.AgentBaseURL != nil {
		baseURL = w.deps.AgentBaseURL()
	}
	return map[string]string{
		browserauth.EnvAgentSessionToken: token,
		browserauth.EnvAgentBrowserURL:   baseURL,
	}
}
