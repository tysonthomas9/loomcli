// Package browsers serves durable interactive-agent browser identities.
//
// Two principals reach FleetDB through this package, each on its own routes:
//
//   - Agent session (/api/agent/browsers...): the caller presents the
//     agent-session bearer Loom injected into that agent's PTY. The owner is
//     the binding's agent; nothing in the request can choose another owner.
//   - Workspace operator (/api/workspaces/{ws}/agents/{name}/browsers...):
//     the human using the Browser pane. Remote mode requires a validated
//     UserIdentity plus the browser permission resolver; local desktop mode
//     requires a live local operator session obtained over the per-user Unix
//     socket through the Tauri shell. Host, Origin, FileAccess, X-Actor and
//     LOOM_AGENT_NAME never authorize anything here.
//
// Every path verifies its principal and the interactive-agent target before
// minting a delegation, and mints only the operation it is about to perform.
package browsers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/browserauth"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// AgentRoutePrefix is the agent-session route family. It authenticates in
// its own handlers (see middleware.hasOwnAuthPrefix).
const AgentRoutePrefix = "/api/agent/browsers"

// Error codes returned in {"error","code"} bodies.
const (
	CodeAgentSessionRequired    = "browser_agent_session_required"
	CodeOperatorSessionRequired = "browser_operator_session_required"
	CodeOperatorSessionExpired  = "browser_operator_session_expired"
	CodeOperatorIdentityMissing = "browser_operator_identity_required"
	CodeForbidden               = "browser_forbidden"
	CodeAgentNotFound           = "browser_agent_not_found"
	CodeNotFound                = "browser_not_found"
	CodeInvalid                 = "browser_invalid"
	CodeRequestConflict         = "browser_request_conflict"
	CodeUnavailable             = "browser_unavailable"
	CodeBridgeUnavailable       = "browser_operator_bridge_unavailable"
)

// Backend is the FleetDB browser API (infra/fleetdb.BrowserClient).
type Backend interface {
	Create(ctx context.Context, ws, delegation string, req domain.BrowserCreate) (*domain.Browser, error)
	List(ctx context.Context, ws, delegation string) ([]domain.Browser, error)
	Get(ctx context.Context, ws, delegation, id string) (*domain.Browser, error)
	Select(ctx context.Context, ws, delegation, id string) (*domain.Browser, error)
}

// Minter signs delegations (browserauth.Signer).
type Minter interface {
	Mint(workspace, owner string, principal browserauth.Principal, operations ...string) (string, error)
}

// PermissionResolver authorizes a validated remote user for one browser
// operation on one interactive-agent target. A nil error allows. It is
// deliberately separate from the file-browser WorkspaceRoleResolver:
// FileAccess permission is not browser permission.
type PermissionResolver func(ctx context.Context, workspace string, identity middleware.UserIdentity, targetAgent, operation string) error

// Notifier publishes a post-commit change for owner in workspace.
type Notifier func(workspace, owner, action string)

// Config wires the module.
type Config struct {
	Store             store.Store
	Backend           Backend
	Signer            Minter
	AgentSessions     *browserauth.AgentSessionRegistry
	OperatorSessions  *browserauth.OperatorSessionRegistry
	RemoteAuth        bool
	ResolvePermission PermissionResolver
	Notify            Notifier
	Logger            *slog.Logger
}

// Module registers browser routes.
type Module struct {
	cfg Config
	log *slog.Logger
}

// NewModule returns a module. Missing dependencies disable the relevant
// routes' happy path (they answer 503), never the authorization checks.
func NewModule(cfg Config) *Module {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Module{cfg: cfg, log: log}
}

// Register mounts the workspace-scoped operator routes on the workspace mux.
func (m *Module) Register(mux *http.ServeMux) {
	if m == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/browsers", m.operatorList)
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/browsers/{id}", m.operatorGet)
	mux.HandleFunc("POST /api/workspaces/{ws}/agents/{name}/browsers/{id}/select", m.operatorSelect)
}

// RegisterAgentRoutes mounts the agent-session routes on the global mux.
func (m *Module) RegisterAgentRoutes(mux *http.ServeMux) {
	if m == nil {
		return
	}
	mux.HandleFunc("POST "+AgentRoutePrefix, m.agentCreate)
	mux.HandleFunc("GET "+AgentRoutePrefix, m.agentList)
	mux.HandleFunc("GET "+AgentRoutePrefix+"/{id}", m.agentGet)
	mux.HandleFunc("POST "+AgentRoutePrefix+"/{id}/select", m.agentSelect)
}

// ---------------------------------------------------------------------------
// Agent-session routes
// ---------------------------------------------------------------------------

// agentState is the `loom browser state --json` shape.
type agentState struct {
	Workspace    string           `json:"workspace"`
	OwnerAgentID string           `json:"owner_agent_id"`
	Browsers     []domain.Browser `json:"browsers"`
}

type agentCreateRequest struct {
	Name      string `json:"name"`
	RequestID string `json:"request_id"`
	// OwnerAgentID is accepted only so a conflicting claim can be rejected
	// with 403; it never selects the owner.
	OwnerAgentID string `json:"owner_agent_id,omitempty"`
}

func (m *Module) agentCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
	var req agentCreateRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid browser create request")
		return
	}
	binding, ok := m.agentPrincipal(w, r, req.OwnerAgentID)
	if !ok {
		return
	}
	create := domain.BrowserCreate{Name: req.Name, RequestID: req.RequestID}
	if err := create.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, strings.TrimPrefix(err.Error(), domain.ErrInvalid.Error()+"\n"))
		return
	}
	delegation, ok := m.mintAgent(w, binding, domain.BrowserOpCreate)
	if !ok {
		return
	}
	created, err := m.cfg.Backend.Create(r.Context(), binding.Workspace, delegation, create)
	if err != nil {
		m.writeBackendError(w, err, "create")
		return
	}
	// Publish only after FleetDB committed; a notify failure never rolls the
	// record back — clients recover by refetching.
	m.notify(binding.Workspace, binding.AgentName, "browser.create")
	handler.WriteJSON(w, http.StatusCreated, created)
}

func (m *Module) agentList(w http.ResponseWriter, r *http.Request) {
	binding, ok := m.agentPrincipal(w, r, r.URL.Query().Get("owner_agent_id"))
	if !ok {
		return
	}
	delegation, ok := m.mintAgent(w, binding, domain.BrowserOpList)
	if !ok {
		return
	}
	list, err := m.cfg.Backend.List(r.Context(), binding.Workspace, delegation)
	if err != nil {
		m.writeBackendError(w, err, "list")
		return
	}
	handler.WriteJSON(w, http.StatusOK, agentState{Workspace: binding.Workspace, OwnerAgentID: binding.AgentName, Browsers: nonNil(list)})
}

func (m *Module) agentGet(w http.ResponseWriter, r *http.Request) {
	binding, ok := m.agentPrincipal(w, r, r.URL.Query().Get("owner_agent_id"))
	if !ok {
		return
	}
	delegation, ok := m.mintAgent(w, binding, domain.BrowserOpGet)
	if !ok {
		return
	}
	b, err := m.cfg.Backend.Get(r.Context(), binding.Workspace, delegation, r.PathValue("id"))
	if err != nil {
		m.writeBackendError(w, err, "get")
		return
	}
	handler.WriteJSON(w, http.StatusOK, b)
}

func (m *Module) agentSelect(w http.ResponseWriter, r *http.Request) {
	binding, ok := m.agentPrincipal(w, r, r.URL.Query().Get("owner_agent_id"))
	if !ok {
		return
	}
	delegation, ok := m.mintAgent(w, binding, domain.BrowserOpSelect)
	if !ok {
		return
	}
	b, err := m.cfg.Backend.Select(r.Context(), binding.Workspace, delegation, r.PathValue("id"))
	if err != nil {
		m.writeBackendError(w, err, "select")
		return
	}
	m.notify(binding.Workspace, binding.AgentName, "browser.select")
	handler.WriteJSON(w, http.StatusOK, b)
}

// agentPrincipal resolves the agent-session binding and revalidates its owner
// as a current interactive agent. claimedOwner, when present, must equal the
// binding's agent (403 otherwise); it is never used as authority.
func (m *Module) agentPrincipal(w http.ResponseWriter, r *http.Request, claimedOwner string) (browserauth.AgentBinding, bool) {
	if m.cfg.AgentSessions == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "agent browser sessions are not enabled on this Loom server")
		return browserauth.AgentBinding{}, false
	}
	token := strings.TrimSpace(r.Header.Get(browserauth.AgentSessionHeader))
	if token == "" {
		writeError(w, http.StatusUnauthorized, CodeAgentSessionRequired, "an active Loom agent session is required; run this command inside a Loom-launched interactive agent terminal")
		return browserauth.AgentBinding{}, false
	}
	binding, err := m.cfg.AgentSessions.Resolve(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, CodeAgentSessionRequired, "the agent session is not active")
		return browserauth.AgentBinding{}, false
	}
	if claimedOwner = strings.TrimSpace(claimedOwner); claimedOwner != "" && claimedOwner != binding.AgentName {
		m.log.Warn("agent browser request named a different owner", "binding_agent", binding.AgentName, "workspace", binding.Workspace)
		writeError(w, http.StatusForbidden, CodeForbidden, "an agent session can act only for its own agent")
		return browserauth.AgentBinding{}, false
	}
	if ws := strings.TrimSpace(r.URL.Query().Get("workspace")); ws != "" && ws != binding.Workspace {
		writeError(w, http.StatusForbidden, CodeForbidden, "an agent session can act only in its own workspace")
		return browserauth.AgentBinding{}, false
	}
	if !m.validateTarget(w, r.Context(), binding.Workspace, binding.AgentName) {
		return browserauth.AgentBinding{}, false
	}
	return binding, true
}

func (m *Module) mintAgent(w http.ResponseWriter, b browserauth.AgentBinding, op string) (string, bool) {
	if !m.backendReady(w) {
		return "", false
	}
	token, err := m.cfg.Signer.Mint(b.Workspace, b.AgentName, browserauth.Principal{
		Kind:      domain.BrowserPrincipalAgentSession,
		Subject:   "agent:" + b.AgentName,
		SessionID: b.ID,
	}, op)
	if err != nil {
		m.log.Error("browser delegation mint failed", "principal", "agent_session", "op", op, "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "browser delegation signer unavailable")
		return "", false
	}
	return token, true
}

// ---------------------------------------------------------------------------
// Operator routes
// ---------------------------------------------------------------------------

type operatorList struct {
	Browsers []domain.Browser `json:"browsers"`
}

func (m *Module) operatorList(w http.ResponseWriter, r *http.Request) {
	ws, _, delegation, ok := m.operatorAuthorize(w, r, domain.BrowserOpList)
	if !ok {
		return
	}
	list, err := m.cfg.Backend.List(r.Context(), ws, delegation)
	if err != nil {
		m.writeBackendError(w, err, "list")
		return
	}
	handler.WriteJSON(w, http.StatusOK, operatorList{Browsers: nonNil(list)})
}

func (m *Module) operatorGet(w http.ResponseWriter, r *http.Request) {
	ws, _, delegation, ok := m.operatorAuthorize(w, r, domain.BrowserOpGet)
	if !ok {
		return
	}
	b, err := m.cfg.Backend.Get(r.Context(), ws, delegation, r.PathValue("id"))
	if err != nil {
		m.writeBackendError(w, err, "get")
		return
	}
	handler.WriteJSON(w, http.StatusOK, b)
}

func (m *Module) operatorSelect(w http.ResponseWriter, r *http.Request) {
	ws, target, delegation, ok := m.operatorAuthorize(w, r, domain.BrowserOpSelect)
	if !ok {
		return
	}
	b, err := m.cfg.Backend.Select(r.Context(), ws, delegation, r.PathValue("id"))
	if err != nil {
		m.writeBackendError(w, err, "select")
		return
	}
	m.notify(ws, target, "browser.select")
	handler.WriteJSON(w, http.StatusOK, b)
}

// operatorAuthorize runs the full operator check in order — principal, then
// permission, then target — and mints a single-operation delegation. Nothing
// reaches FleetDB's browser API unless every step passed.
func (m *Module) operatorAuthorize(w http.ResponseWriter, r *http.Request, op string) (ws, target, delegation string, ok bool) {
	ws = middleware.WorkspaceFromContext(r.Context())
	if ws == "" {
		ws = r.PathValue("ws")
	}
	target = r.PathValue("name")
	principal, ok := m.operatorPrincipal(w, r, ws, target, op)
	if !ok {
		return "", "", "", false
	}
	if !m.validateTarget(w, r.Context(), ws, target) {
		return "", "", "", false
	}
	if !m.backendReady(w) {
		return "", "", "", false
	}
	token, err := m.cfg.Signer.Mint(ws, target, principal, op)
	if err != nil {
		m.log.Error("browser delegation mint failed", "principal", "workspace_operator", "op", op, "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "browser delegation signer unavailable")
		return "", "", "", false
	}
	return ws, target, token, true
}

func (m *Module) operatorPrincipal(w http.ResponseWriter, r *http.Request, ws, target, op string) (browserauth.Principal, bool) {
	if m.cfg.RemoteAuth {
		identity, ok := middleware.UserIdentityFromContext(r.Context())
		if !ok || strings.TrimSpace(identity.UserID) == "" {
			writeError(w, http.StatusUnauthorized, CodeOperatorIdentityMissing, "a signed-in user is required")
			return browserauth.Principal{}, false
		}
		if m.cfg.ResolvePermission == nil {
			writeError(w, http.StatusForbidden, CodeForbidden, "browser operator permission is not configured on this Loom server")
			return browserauth.Principal{}, false
		}
		if err := m.cfg.ResolvePermission(r.Context(), ws, identity, target, op); err != nil {
			m.log.Info("browser operator permission denied", "user_id", identity.UserID, "workspace", ws, "target", target, "op", op)
			writeError(w, http.StatusForbidden, CodeForbidden, "you do not have browser access to this agent")
			return browserauth.Principal{}, false
		}
		return browserauth.Principal{
			Kind:     domain.BrowserPrincipalWorkspaceOperator,
			Subject:  "user:" + strings.TrimSpace(identity.UserID),
			AuthMode: domain.BrowserAuthModeRemoteUser,
		}, true
	}

	// Local desktop mode. Only a native-IPC session counts; a regular browser
	// tab (even with a forged loopback Host/Origin and FileAccess) has none.
	if m.cfg.OperatorSessions == nil {
		writeError(w, http.StatusServiceUnavailable, CodeBridgeUnavailable, "the Loom Desktop browser bridge is not running; open this workspace in Loom Desktop")
		return browserauth.Principal{}, false
	}
	token := strings.TrimSpace(r.Header.Get(browserauth.OperatorSessionHeader))
	if token == "" {
		writeError(w, http.StatusUnauthorized, CodeOperatorSessionRequired, "open this workspace in Loom Desktop to view agent browsers")
		return browserauth.Principal{}, false
	}
	sess, err := m.cfg.OperatorSessions.Validate(token, ws)
	switch {
	case err == nil:
	case errors.Is(err, browserauth.ErrOperatorSessionExpired):
		writeError(w, http.StatusUnauthorized, CodeOperatorSessionExpired, "the desktop browser session expired")
		return browserauth.Principal{}, false
	case errors.Is(err, domain.ErrBrowserForbidden):
		writeError(w, http.StatusForbidden, CodeForbidden, "the desktop browser session belongs to another workspace")
		return browserauth.Principal{}, false
	default:
		writeError(w, http.StatusUnauthorized, CodeOperatorSessionRequired, "the desktop browser session is not active")
		return browserauth.Principal{}, false
	}
	return browserauth.Principal{
		Kind:                   domain.BrowserPrincipalWorkspaceOperator,
		Subject:                sess.Subject(),
		AuthMode:               domain.BrowserAuthModeLocalDesktop,
		LocalOperatorSessionID: sess.ID,
	}, true
}

// ---------------------------------------------------------------------------
// Shared checks
// ---------------------------------------------------------------------------

// validateTarget confirms name is a current interactive agent in ws. It is
// the one browser target validator for both principals.
func (m *Module) validateTarget(w http.ResponseWriter, ctx context.Context, ws, name string) bool {
	if m.cfg.Store == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "agent store unavailable")
		return false
	}
	if ws == "" || !service.IsValidAgentName(name) {
		writeError(w, http.StatusNotFound, CodeAgentNotFound, "interactive agent not found")
		return false
	}
	agent, err := m.cfg.Store.Agents().Get(ctx, ws, name)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && agent == nil) {
		writeError(w, http.StatusNotFound, CodeAgentNotFound, "interactive agent not found")
		return false
	}
	if err != nil {
		m.log.Error("browser target lookup failed", "workspace", ws, "agent", name, "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "agent store unavailable")
		return false
	}
	role, err := m.cfg.Store.Roles().Get(ctx, ws, agent.RoleName)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		m.log.Error("browser target role lookup failed", "workspace", ws, "agent", name, "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "role store unavailable")
		return false
	}
	if err != nil {
		role = nil
	}
	if domain.ResolveRoleKind(role, agent.RoleName) != domain.RoleKindInteractive {
		writeError(w, http.StatusNotFound, CodeAgentNotFound, "interactive agent not found")
		return false
	}
	return true
}

func (m *Module) backendReady(w http.ResponseWriter) bool {
	if m.cfg.Backend == nil || m.cfg.Signer == nil {
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "durable browser service is not configured on this Loom server")
		return false
	}
	return true
}

func (m *Module) writeBackendError(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, CodeNotFound, "browser not found")
	case errors.Is(err, domain.ErrBrowserRequestConflict):
		writeError(w, http.StatusConflict, CodeRequestConflict, "request_id was already used with a different browser payload")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, CodeInvalid, "invalid browser request")
	case errors.Is(err, domain.ErrBrowserForbidden):
		writeError(w, http.StatusForbidden, CodeForbidden, "browser access forbidden")
	default:
		// FleetDB outage, or FleetDB rejecting Loom's delegation (key or
		// issuer mismatch): a server-side problem the caller cannot fix, and
		// never a reason to show a locally invented browser.
		m.log.Error("fleetdb browser call failed", "op", op, "err", err)
		writeError(w, http.StatusServiceUnavailable, CodeUnavailable, "durable browser service unavailable")
	}
}

func (m *Module) notify(ws, owner, action string) {
	if m.cfg.Notify == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			m.log.Warn("browser notify panicked", "recover", rec)
		}
	}()
	m.cfg.Notify(ws, owner, action)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	handler.WriteJSON(w, status, map[string]string{"error": msg, "code": code})
}

func nonNil(list []domain.Browser) []domain.Browser {
	if list == nil {
		return []domain.Browser{}
	}
	return list
}
