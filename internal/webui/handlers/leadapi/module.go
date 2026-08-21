// Package leadapi serves the lead HTTP API on loom serve: the lead operations
// a sandboxed interactive orchestrator would otherwise perform against
// fleet-db directly, re-hosted behind serve so in-sandbox lead runtimes never
// hold fleet-db credentials.
//
// This is the occupant-authenticated in-sandbox half of lead control. The
// driverapi package already hosts serve-side lead messaging for workflow
// drivers; this package is not a duplicate of that surface. Surface shape
// mirrors driverapi/taskrunapi: camelCase JSON on the wire, structured errors
// {code, message, retryable}.
package leadapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// IssueBackendFn resolves the per-request issue backend. The occupant actor
// must already be on ctx; serve keys its backend cache on (workspace, actor)
// so an occupant's reads carry the placement identity, never serve's actor.
type IssueBackendFn func(context.Context) backend.IssueBackend

const (
	maxLeadOpBodyBytes       = 8 << 20
	occupantTokenRenewWindow = 30 * time.Minute
)

// Config wires the module's dependencies.
type Config struct {
	Store store.Store
	// TokenKey is the HS256 signing key shared with driver run tokens
	// (LOOM_RUN_TOKEN_SIGNING_KEY, or the same ephemeral per-process key).
	TokenKey     []byte
	Data         *DataRoutes
	IssueBackend IssueBackendFn
	// OpenAuthMode records whether the general serve origin accepts requests
	// without user authentication. AllowOpenAuthMode is the explicit POC-only
	// override for mounting occupant data and dispatch routes in that posture.
	OpenAuthMode      bool
	AllowOpenAuthMode bool
}

// Module serves workspace-scoped lead routes.
type Module struct {
	store             store.Store
	tokenKey          []byte
	ops               map[string]leadOp
	data              *DataRoutes
	issueBackend      IssueBackendFn
	openAuthMode      bool
	allowOpenAuthMode bool
	limiter           *placementLimiter
	now               func() time.Time

	sessionEnsureMu sync.Mutex
}

// NewModule constructs the lead API module. Nil-safe: with a nil store,
// Register registers nothing.
func NewModule(cfg Config) *Module {
	m := &Module{
		store:             cfg.Store,
		tokenKey:          resolveTokenKey(cfg.TokenKey),
		data:              cfg.Data,
		issueBackend:      cfg.IssueBackend,
		openAuthMode:      cfg.OpenAuthMode,
		allowOpenAuthMode: cfg.AllowOpenAuthMode,
		limiter:           newPlacementLimiter(),
		now:               func() time.Time { return time.Now().UTC() },
	}
	m.ops = map[string]leadOp{
		"agent-get":      {handler: m.agentGet, cap: leadtoken.CapLeadAssignment},
		"heartbeat":      {handler: m.heartbeat, cap: leadtoken.CapLeadSession},
		"inbox-claim":    {handler: m.inboxClaim, cap: leadtoken.CapLeadInbox},
		"inbox-complete": {handler: m.inboxComplete, cap: leadtoken.CapLeadInbox},
		"inbox-list":     {handler: m.inboxList, cap: leadtoken.CapLeadInbox},
		"session-ensure": {handler: m.sessionEnsure, cap: leadtoken.CapLeadSession},
		"session-get":    {handler: m.sessionGet, cap: leadtoken.CapLeadSession},
		"session-update": {handler: m.sessionUpdate, cap: leadtoken.CapLeadSession},
	}
	return m
}

func resolveTokenKey(configured []byte) []byte {
	if len(configured) > 0 {
		return configured
	}
	key, err := leadtoken.ResolveSigningKey()
	if err != nil {
		slog.Error("lead occupant-token auth disabled: resolve signing key", "err", err)
		return nil
	}
	return key
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/lead/{op}", m.handleOp)
	if m.openAuthMode && !m.allowOpenAuthMode {
		slog.Error("lead occupant data + dispatch mounts disabled in open auth mode; set LOOM_LEAD_DATA_ALLOW_OPEN_AUTH=1 to override (POC only)")
		return
	}
	m.registerDataRoutes(mux)
	m.registerDispatchRoutes(mux)
}

type occupantIdentity struct {
	claims *leadtoken.OccupantClaims
	node   *domain.Node
}

type opHandler func(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error)

type leadOp struct {
	handler opHandler
	cap     string
}

func (m *Module) handleOp(w http.ResponseWriter, r *http.Request) {
	id, ok := m.authenticate(w, r)
	if !ok {
		return
	}
	op := strings.TrimSpace(r.PathValue("op"))
	entry, ok := m.ops[op]
	if !ok {
		writeOpErrorDetails(w, http.StatusNotFound, "unknown_op", fmt.Sprintf("unknown lead op %q", op), false, map[string]any{"op": op})
		return
	}
	if !m.authorizeOp(w, op, entry.cap, id.claims) {
		return
	}
	body, err := readOpBody(w, r)
	if err != nil {
		writeReadBodyError(w, err)
		return
	}
	result, err := entry.handler(r.Context(), r.PathValue("ws"), id, body)
	if err != nil {
		writeDomainOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (m *Module) authenticate(w http.ResponseWriter, r *http.Request) (occupantIdentity, bool) {
	id, err := m.authenticateRequest(r.Context(), r)
	if err != nil {
		writeDomainOpError(w, err)
		return occupantIdentity{}, false
	}
	return id, true
}

func (m *Module) authenticateRequest(ctx context.Context, r *http.Request) (occupantIdentity, error) {
	claims, err := m.parseBearer(r)
	if err != nil {
		return occupantIdentity{}, err
	}
	ws := r.PathValue("ws")
	if claims.WorkspaceKey != ws {
		msg := fmt.Sprintf("occupant token is scoped to workspace %q, not %q", claims.WorkspaceKey, ws)
		return occupantIdentity{}, newStatusError(http.StatusUnauthorized, "identity_mismatch", msg, false)
	}
	node, err := m.placementForClaims(ctx, claims)
	if err != nil {
		return occupantIdentity{}, err
	}
	return occupantIdentity{claims: claims, node: node}, nil
}

func (m *Module) parseBearer(r *http.Request) (*leadtoken.OccupantClaims, error) {
	token := bearerCredential(r)
	if token == "" {
		return nil, newStatusError(http.StatusUnauthorized, "unauthenticated", "Authorization: Bearer <occupant token> required", false)
	}
	claims, err := leadtoken.ParseOccupantToken(token, m.tokenKey)
	switch {
	case err == nil:
		return claims, nil
	case leadtoken.IsOccupantTokenExpired(err):
		return nil, newStatusError(http.StatusUnauthorized, "token_expired", "occupant token expired", true)
	default:
		return nil, newStatusError(http.StatusUnauthorized, "unauthenticated", "invalid occupant token", false)
	}
}

func (m *Module) placementForClaims(ctx context.Context, claims *leadtoken.OccupantClaims) (*domain.Node, error) {
	node, err := m.store.Nodes().Get(ctx, claims.WorkspaceKey, claims.PlacementID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, newStatusError(http.StatusUnauthorized, "placement_absent", "placement record not found", false)
		}
		return nil, newStatusError(http.StatusServiceUnavailable, "unavailable", "placement store unavailable", true)
	}
	if err := validatePlacementForClaims(node, claims); err != nil {
		return nil, err
	}
	return node, nil
}

func validatePlacementForClaims(node *domain.Node, claims *leadtoken.OccupantClaims) error {
	if node == nil || node.Placement == nil {
		return newStatusError(http.StatusUnauthorized, "placement_absent", "placement record missing node placement", false)
	}
	if !placementStateAllowsLead(node.Placement.State) {
		msg := fmt.Sprintf("placement state %q does not allow lead operations", node.Placement.State)
		return newStatusError(http.StatusUnauthorized, "placement_released", msg, false)
	}
	if node.Placement.Generation != claims.Generation {
		return newStatusError(http.StatusUnauthorized, "generation_fenced", "placement generation no longer owns this lead", false)
	}
	return nil
}

func placementStateAllowsLead(state domain.PlacementState) bool {
	switch state {
	case domain.PlacementStateProvisioning, domain.PlacementStateActive:
		return true
	default:
		return false
	}
}

func (m *Module) authorizeOp(w http.ResponseWriter, op, capability string, claims *leadtoken.OccupantClaims) bool {
	if err := hasCapOrError(op, capability, claims); err != nil {
		writeDomainOpError(w, err)
		return false
	}
	return true
}

func hasCapOrError(op, capability string, claims *leadtoken.OccupantClaims) error {
	if strings.TrimSpace(capability) == "" {
		return newStatusError(http.StatusForbidden, "cap_denied", fmt.Sprintf("lead op %q has no configured capability", op), false)
	}
	if leadtoken.HasCap(claims, capability) {
		return nil
	}
	return newStatusError(http.StatusForbidden, "cap_denied", fmt.Sprintf("lead op %q requires cap %q", op, capability), false)
}

func (m *Module) heartbeat(ctx context.Context, ws string, id occupantIdentity, body []byte) (any, error) {
	if err := decodeEmptyParams(body); err != nil {
		return nil, err
	}
	node, err := m.store.Nodes().Heartbeat(ctx, ws, id.node.NodeID, 0)
	if err != nil {
		return nil, fmt.Errorf("heartbeat placement node: %w", err)
	}
	if err := validatePlacementForClaims(node, id.claims); err != nil {
		return nil, err
	}
	session, err := m.heartbeatLeadSession(ctx, ws, node)
	if err != nil {
		return nil, err
	}
	return m.heartbeatResult(id.claims, node, session)
}

func (m *Module) heartbeatLeadSession(ctx context.Context, ws string, node *domain.Node) (*domain.AgentSession, error) {
	session, err := m.resolveLeadSession(ctx, ws, node)
	if err != nil {
		return nil, err
	}
	refreshed, err := m.store.AgentSessions().Heartbeat(ctx, ws, session.SessionID)
	if err != nil {
		return nil, fmt.Errorf("heartbeat lead session: %w", err)
	}
	return refreshed, nil
}

func (m *Module) resolveLeadSession(ctx context.Context, ws string, node *domain.Node) (*domain.AgentSession, error) {
	sessions, err := m.store.AgentSessions().List(ctx, ws, store.AgentSessionFilter{
		NodeID: node.NodeID,
		Kind:   domain.AgentSessionKindOrchestration,
	})
	if err != nil {
		return nil, fmt.Errorf("list lead sessions for placement node: %w", err)
	}
	session, err := oneActiveSession(sessions, node.NodeID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("lead orchestration session for node %q: %w", node.NodeID, domain.ErrNotFound)
	}
	return session, nil
}

func oneActiveSession(sessions []*domain.AgentSession, nodeID string) (*domain.AgentSession, error) {
	var active *domain.AgentSession
	count := 0
	for _, session := range sessions {
		if !activeOrchestrationSession(session) {
			continue
		}
		active = session
		count++
	}
	if count > 1 {
		return nil, fmt.Errorf("lead orchestration session invariant violated for node %q: %d active orchestration sessions", nodeID, count)
	}
	return active, nil
}

func activeOrchestrationSession(session *domain.AgentSession) bool {
	if session == nil || session.FinishedAt != nil {
		return false
	}
	switch session.Status {
	case "", domain.AgentSessionLeased, domain.AgentSessionStarting,
		domain.AgentSessionRunning, domain.AgentSessionIdle, domain.AgentSessionYielded:
		return true
	default:
		return false
	}
}

func (m *Module) heartbeatResult(claims *leadtoken.OccupantClaims, node *domain.Node, session *domain.AgentSession) (any, error) {
	result := leadHeartbeatResult{
		Node:    nodeResult{NodeID: node.NodeID, LastHeartbeat: node.LastHeartbeat},
		Session: sessionResult{SessionID: session.SessionID, AgentID: session.AgentID, LastHeartbeat: session.LastHeartbeat},
	}
	// Serve decides renewal because sandbox clocks can drift. The response
	// carries credentials and must not be logged.
	if m.shouldRenew(claims) {
		token, err := leadtoken.MintOccupantToken(renewalClaims(claims), m.tokenKey, leadtoken.DefaultOccupantTokenTTL)
		if err != nil {
			return nil, fmt.Errorf("renew occupant token: %w", err)
		}
		result.OccupantToken = token
	}
	return result, nil
}

func (m *Module) shouldRenew(claims *leadtoken.OccupantClaims) bool {
	if claims == nil || claims.ExpiresAt == nil {
		return false
	}
	return claims.ExpiresAt.Time.Sub(m.now()) < occupantTokenRenewWindow
}

func renewalClaims(claims *leadtoken.OccupantClaims) leadtoken.OccupantClaims {
	return leadtoken.OccupantClaims{
		WorkspaceKey: claims.WorkspaceKey,
		PlacementID:  claims.PlacementID,
		Generation:   claims.Generation,
		Caps:         append([]string(nil), claims.Caps...),
	}
}

type leadHeartbeatResult struct {
	Node          nodeResult    `json:"node"`
	Session       sessionResult `json:"session"`
	OccupantToken string        `json:"occupantToken,omitempty"`
}

type nodeResult struct {
	NodeID        string    `json:"nodeId"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}

type sessionResult struct {
	SessionID       string                    `json:"sessionId"`
	AgentID         string                    `json:"agentId"`
	NodeID          string                    `json:"nodeId,omitempty"`
	Kind            domain.AgentSessionKind   `json:"kind,omitempty"`
	TaskID          string                    `json:"taskId,omitempty"`
	TerminalID      string                    `json:"terminalId,omitempty"`
	ParentSessionID string                    `json:"parentSessionId,omitempty"`
	Status          domain.AgentSessionStatus `json:"status,omitempty"`
	Phase           string                    `json:"phase,omitempty"`
	Attempt         int                       `json:"attempt,omitempty"`
	StartedAt       *time.Time                `json:"startedAt,omitempty"`
	LastHeartbeat   time.Time                 `json:"lastHeartbeat"`
	FinishedAt      *time.Time                `json:"finishedAt,omitempty"`
	Summary         string                    `json:"summary,omitempty"`
	ErrorClass      string                    `json:"errorClass,omitempty"`
	ExitCode        *int                      `json:"exitCode,omitempty"`
	Metadata        map[string]string         `json:"metadata,omitempty"`
	CreatedAt       *time.Time                `json:"createdAt,omitempty"`
	UpdatedAt       *time.Time                `json:"updatedAt,omitempty"`
}

func decodeEmptyParams(body []byte) error {
	var params map[string]any
	if err := json.Unmarshal(body, &params); err != nil {
		return fmt.Errorf("decode lead op params: %s: %w", err.Error(), domain.ErrInvalid)
	}
	return nil
}

func readOpBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := readAll(w, r, maxLeadOpBodyBytes)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	return body, nil
}

func readAll(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// bearerCredential extracts the Bearer credential, "" when absent.
func bearerCredential(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) < len("Bearer ")+1 {
		return ""
	}
	if !strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

// opError is the structured v2 error envelope, shape-identical to the
// driver-op API: {code, message, retryable} plus an optional additive details
// object.
type opError struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type opStatusError struct {
	status    int
	code      string
	message   string
	retryable bool
	details   map[string]any
}

func (e *opStatusError) Error() string {
	return e.message
}

func newStatusError(status int, code, message string, retryable bool) *opStatusError {
	return &opStatusError{status: status, code: code, message: message, retryable: retryable}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeOpError(w http.ResponseWriter, status int, code, message string, retryable bool) {
	writeOpErrorDetails(w, status, code, message, retryable, nil)
}

func writeOpErrorDetails(w http.ResponseWriter, status int, code, message string, retryable bool, details map[string]any) {
	writeJSON(w, status, map[string]any{"error": opError{Code: code, Message: message, Retryable: retryable, Details: details}})
}

func writeReadBodyError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		msg := fmt.Sprintf("lead op payload exceeds %d bytes", maxLeadOpBodyBytes)
		writeOpError(w, http.StatusRequestEntityTooLarge, "invalid", msg, false)
		return
	}
	writeOpError(w, http.StatusBadRequest, "invalid", "read lead op payload: "+err.Error(), false)
}

// writeDomainOpError maps domain and lead-op errors onto the structured error
// envelope.
func writeDomainOpError(w http.ResponseWriter, err error) {
	var statusErr *opStatusError
	if errors.As(err, &statusErr) {
		writeOpErrorDetails(w, statusErr.status, statusErr.code, statusErr.message, statusErr.retryable, statusErr.details)
		return
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeOpError(w, http.StatusNotFound, "not_found", err.Error(), false)
	case errors.Is(err, domain.ErrNotOwner):
		writeOpError(w, http.StatusForbidden, "not_owner", err.Error(), false)
	case errors.Is(err, domain.ErrInvalidTransition):
		writeOpError(w, http.StatusConflict, "invalid_transition", err.Error(), false)
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		writeOpError(w, http.StatusConflict, "conflict", err.Error(), false)
	case errors.Is(err, domain.ErrInvalid):
		writeOpError(w, http.StatusBadRequest, "invalid", err.Error(), false)
	case errors.Is(err, context.DeadlineExceeded):
		writeOpError(w, http.StatusGatewayTimeout, "timeout", err.Error(), true)
	case errors.Is(err, context.Canceled):
		writeOpError(w, 499, "canceled", err.Error(), true)
	default:
		writeOpError(w, http.StatusInternalServerError, "internal", err.Error(), false)
	}
}
