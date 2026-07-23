// Package connectors exposes a webui HTTP surface for creating per-source
// connectors and their deny-by-default egress grants. Until now connector +
// grant creation was CLI-only (`loom connector create` / `loom connector grant
// create`); this module is the backend keystone that lets the web UI self-serve
// a review agent's github connector and grants.
//
// The load-bearing piece is the Settings-token bridge: the desktop Settings
// github token is sealed in the localsettings vault (a DIFFERENT vault than
// connectors read from). When reuse_runtime_credential is set, this module
// unseals that token from localsettings and re-seals it under the connector
// vault (LOOM_CONNECTOR_VAULT_KEY) bound to (workspace, connectorID) — exactly
// like internal/cli/connector sealCredential — so stores only ever see
// ciphertext. It fails closed (4xx/5xx) when either vault key is unavailable or
// the Settings token isn't configured.
package connectors

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	vault "github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxConnectorBodyBytes = 1 << 20

// Module registers connector + grant create routes. It holds the store
// directly (the same shape as the roles/webhooks/triggerbindings modules) plus
// the localsettings data dir needed to bridge the Settings runtime credential.
type Module struct {
	store             store.Store
	localSettingsDir  string
	grantSets         *vault.GrantSetReconciler
	grantMu           sync.Mutex
	credentialMu      sync.Mutex
	operatorAuthority workflowcataloghttp.OperatorAuthorityResolver
}

func NewModule(
	st store.Store,
	localSettingsDir string,
	operatorAuthorities ...workflowcataloghttp.OperatorAuthorityResolver,
) *Module {
	module := &Module{store: st, localSettingsDir: localSettingsDir}
	if len(operatorAuthorities) > 0 {
		module.operatorAuthority = operatorAuthorities[0]
	}
	if st != nil {
		module.grantSets = vault.NewGrantSetReconciler(
			st.TriggerBindings(),
			st.Connectors(),
			st.ConnectorGrants(),
		)
	}
	return module
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/connectors", m.createConnector)
	mux.HandleFunc("POST /api/workspaces/{ws}/connectors/{id}/grants", m.createGrant)
	mux.HandleFunc("PUT /api/workspaces/{ws}/connectors/{id}/bindings/{bindingID}/grants", m.replaceBindingGrants)
}

func workspaceFromRequest(r *http.Request) string {
	if r == nil || strings.TrimSpace(r.PathValue("ws")) == "" {
		return ""
	}
	if workspace := strings.TrimSpace(middleware.WorkspaceFromContext(r.Context())); workspace != "" {
		return workspace
	}
	return strings.TrimSpace(r.PathValue("ws"))
}

type createConnectorRequest struct {
	Source      string `json:"source"`
	ConnectorID string `json:"connector_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	// ReuseRuntimeCredential bridges the Settings-stored runtime credential for
	// the connector's source kind (e.g. the github token) into the connector's
	// sealed outbound credential.
	ReuseRuntimeCredential bool `json:"reuse_runtime_credential,omitempty"`
}

func (m *Module) createConnector(w http.ResponseWriter, r *http.Request) {
	ws := workspaceFromRequest(r)
	if ws == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}

	req, in, ok := decodeCreateConnectorRequest(w, r, ws)
	if !ok {
		return
	}
	if m.respondWithExistingConnector(w, r, in, req.ReuseRuntimeCredential) {
		return
	}
	if req.ReuseRuntimeCredential {
		sealed, status, err := m.bridgeRuntimeCredential(ws, in.ConnectorID, string(in.SourceKind))
		if err != nil {
			handler.RespondError(w, status, err.Error())
			return
		}
		in.OutboundCredentialSealed = sealed
	}
	m.createNewConnector(w, r, in, req.ReuseRuntimeCredential)
}

func decodeCreateConnectorRequest(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
) (createConnectorRequest, store.ConnectorCreate, bool) {
	var req createConnectorRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return createConnectorRequest{}, store.ConnectorCreate{}, false
	}

	source := domain.ConnectorSourceKind(strings.TrimSpace(req.Source))
	if !source.Valid() {
		handler.RespondError(w, http.StatusBadRequest, "source must be one of github, slack, datadog, internal")
		return createConnectorRequest{}, store.ConnectorCreate{}, false
	}
	connectorID := strings.TrimSpace(req.ConnectorID)
	if connectorID == "" {
		connectorID = string(source)
	}

	return req, store.ConnectorCreate{
		WorkspaceKey: ws,
		ConnectorID:  connectorID,
		SourceKind:   source,
		DisplayName:  strings.TrimSpace(req.DisplayName),
	}, true
}

func (m *Module) respondWithExistingConnector(
	w http.ResponseWriter,
	r *http.Request,
	in store.ConnectorCreate,
	requireCredential bool,
) bool {
	// Exact idempotent ensure: the stable connector id may be reused only when
	// it still names the requested active source and, when credential reuse was
	// requested, already holds a nonempty sealed outbound credential. Returning
	// an arbitrary/credential-less row here would let UI activation succeed and
	// defer the real failure to the first workflow run.
	existing, err := m.store.Connectors().Get(r.Context(), in.WorkspaceKey, in.ConnectorID)
	if err == nil {
		ensured, ensureErr := m.ensureExistingConnector(r.Context(), existing, in, requireCredential)
		if ensureErr != nil {
			handler.WriteDomainError(w, ensureErr, "validate existing connector failed")
			return true
		}
		if ensured == nil {
			handler.WriteDomainError(w, domain.ErrConflict, "validate existing connector failed")
			return true
		}
		handler.WriteJSON(w, http.StatusOK, ensured)
		return true
	}
	if !errors.Is(err, domain.ErrNotFound) {
		handler.WriteDomainError(w, err, "get connector failed")
		return true
	}
	return false
}

func (m *Module) ensureExistingConnector(
	ctx context.Context,
	existing *domain.Connector,
	in store.ConnectorCreate,
	requireCredential bool,
) (*domain.Connector, error) {
	if err := validateExistingConnector(existing, in.SourceKind); err != nil {
		return nil, err
	}
	if !requireCredential {
		return existing, nil
	}
	return m.synchronizeRuntimeCredential(ctx, existing)
}

func validateExistingConnector(existing *domain.Connector, source domain.ConnectorSourceKind) error {
	switch {
	case existing == nil:
		return fmt.Errorf("connector store returned no record: %w", domain.ErrConflict)
	case existing.SourceKind != source:
		return fmt.Errorf("existing connector id belongs to a different source: %w", domain.ErrConflict)
	case existing.Status != domain.ConnectorStatusActive:
		return fmt.Errorf("existing connector is not active: %w", domain.ErrConflict)
	default:
		return nil
	}
}

func (m *Module) synchronizeRuntimeCredential(
	ctx context.Context,
	existing *domain.Connector,
) (*domain.Connector, error) {
	m.credentialMu.Lock()
	defer m.credentialMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		current, err := m.store.Connectors().Get(ctx, existing.WorkspaceKey, existing.ConnectorID)
		if err != nil {
			return nil, err
		}
		if err := validateExistingConnector(current, existing.SourceKind); err != nil {
			return nil, err
		}
		synchronized, err := m.synchronizeRuntimeCredentialOnce(ctx, current)
		if err == nil {
			return synchronized, nil
		}
		if !errors.Is(err, domain.ErrConflict) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("connector credential changed during synchronization: %w", domain.ErrConflict)
}

func (m *Module) synchronizeRuntimeCredentialOnce(
	ctx context.Context,
	existing *domain.Connector,
) (*domain.Connector, error) {
	settings, err := localsettings.Load(m.localSettingsDir)
	if err != nil {
		return nil, fmt.Errorf("load local settings: %w", err)
	}
	desired, err := localsettings.UnsealRuntimeCredentialBytes(
		m.localSettingsDir,
		settings,
		string(existing.SourceKind),
	)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(desired)

	sealer, err := vault.NewVaultFromEnvOrKeyFile(m.localSettingsDir)
	if err != nil {
		return nil, fmt.Errorf("open connector vault: %w", err)
	}
	same, err := m.connectorCredentialMatches(ctx, existing, desired, sealer)
	if err != nil {
		return nil, err
	}
	if same {
		return existing, nil
	}

	inboundSecret, err := m.connectorRotationInboundSecret(ctx, existing)
	if err != nil {
		return nil, err
	}
	return m.rotateRuntimeCredential(ctx, existing, inboundSecret, sealer, desired)
}

func (m *Module) rotateRuntimeCredential(
	ctx context.Context,
	existing *domain.Connector,
	inboundSecret string,
	sealer vault.Sealer,
	desired []byte,
) (*domain.Connector, error) {
	credential := append([]byte(nil), desired...)
	zeroBytes(desired)
	defer zeroBytes(credential)
	rotated, err := vault.Rotate(
		ctx,
		m.store.Connectors(),
		m.store.ConnectorCalls(),
		sealer,
		vault.RotateRequest{
			WorkspaceKey:      existing.WorkspaceKey,
			ConnectorID:       existing.ConnectorID,
			NewInboundSecret:  inboundSecret,
			NewCredential:     credential,
			ExpectedUpdatedAt: existing.UpdatedAt,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("synchronize connector runtime credential: %w", err)
	}
	return rotated, nil
}

func (m *Module) connectorRotationInboundSecret(
	ctx context.Context,
	existing *domain.Connector,
) (string, error) {
	inbound, err := m.store.Connectors().ResolveInboundSecret(
		ctx,
		existing.WorkspaceKey,
		existing.ConnectorID,
	)
	if err != nil {
		return "", fmt.Errorf("resolve connector inbound secret: %w", err)
	}
	if inbound != nil && inbound.Current != "" {
		return inbound.Current, nil
	}
	return randomConnectorSecret()
}

func (m *Module) connectorCredentialMatches(
	ctx context.Context,
	existing *domain.Connector,
	desired []byte,
	sealer vault.Sealer,
) (bool, error) {
	sealed, err := m.store.Connectors().ResolveOutboundCredentialSealed(
		ctx,
		existing.WorkspaceKey,
		existing.ConnectorID,
	)
	if err != nil {
		return false, fmt.Errorf("resolve existing connector credential: %w", err)
	}
	if len(sealed) == 0 {
		return false, nil
	}
	current, err := sealer.Unseal(
		sealed,
		vault.CredentialAAD(existing.WorkspaceKey, existing.ConnectorID),
	)
	if err != nil {
		if errors.Is(err, vault.ErrUnseal) {
			return false, nil
		}
		return false, fmt.Errorf("unseal existing connector credential: %w", err)
	}
	defer zeroBytes(current)
	return subtle.ConstantTimeCompare(current, desired) == 1, nil
}

func randomConnectorSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate connector rotation secret: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func (m *Module) createNewConnector(
	w http.ResponseWriter,
	r *http.Request,
	in store.ConnectorCreate,
	requireCredential bool,
) {
	conn, err := m.store.Connectors().Create(r.Context(), in)
	if err == nil {
		if requireCredential {
			conn, err = m.synchronizeRuntimeCredential(r.Context(), conn)
			if err != nil {
				handler.WriteDomainError(w, err, "synchronize created connector credential failed")
				return
			}
		}
		handler.WriteJSON(w, http.StatusCreated, conn)
		return
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		existing, fetchErr := m.store.Connectors().Get(r.Context(), in.WorkspaceKey, in.ConnectorID)
		if fetchErr != nil {
			handler.WriteDomainError(w, fetchErr, "get concurrently created connector failed")
			return
		}
		ensured, validateErr := m.ensureExistingConnector(r.Context(), existing, in, requireCredential)
		if validateErr != nil {
			handler.WriteDomainError(w, validateErr, "validate concurrently created connector failed")
			return
		}
		handler.WriteJSON(w, http.StatusOK, ensured)
		return
	}
	handler.WriteDomainError(w, err, "create connector failed")
}

// bridgeRuntimeCredential loads the Settings-stored runtime credential for the
// given provider (the connector's source kind), unseals it from the
// localsettings vault, and re-seals it under the connector vault
// (LOOM_CONNECTOR_VAULT_KEY) bound to (ws, connectorID). The returned int is the
// HTTP status to fail the request with when err is non-nil.
func (m *Module) bridgeRuntimeCredential(ws, connectorID, provider string) ([]byte, int, error) {
	settings, err := localsettings.Load(m.localSettingsDir)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("load local settings: %w", err)
	}
	// UnsealRuntimeCredential fails when the Settings credential for this
	// provider isn't configured — a client-actionable precondition (400).
	plaintext, err := localsettings.UnsealRuntimeCredentialBytes(m.localSettingsDir, settings, provider)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	defer zeroBytes(plaintext)
	// Use the same env-or-persisted-key resolution as serve's connector
	// dispatcher. Otherwise local key-file mode could seal with material the
	// runtime never opens (or reject a correctly configured local stack).
	sealer, err := vault.NewVaultFromEnvOrKeyFile(m.localSettingsDir)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("connector vault: %w", err)
	}
	sealed, err := sealer.Seal(plaintext, vault.CredentialAAD(ws, connectorID))
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("seal outbound credential: %w", err)
	}
	return sealed, http.StatusOK, nil
}

type createGrantRequest struct {
	BindingID       string `json:"binding_id"`
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
	// GrantID is optional; it defaults to the CLI's derived id
	// (grant-<binding>-<action>) so the UI need not supply one.
	GrantID string `json:"grant_id,omitempty"`
}

func (m *Module) createGrant(w http.ResponseWriter, r *http.Request) {
	ws := workspaceFromRequest(r)
	connectorID := strings.TrimSpace(r.PathValue("id"))
	if ws == "" || connectorID == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and connector id are required")
		return
	}

	expected, _, ok := decodeCreateGrantRequest(w, r, ws, connectorID)
	if !ok {
		return
	}

	m.grantMu.Lock()
	defer m.grantMu.Unlock()

	// A derived grant id is stable across template re-activation. It is
	// idempotent only when every authority-bearing field is identical: treating
	// the same id as sufficient would silently retain the old repository scope
	// when a singleton workflow is retargeted.
	if existing := m.findGrant(r.Context(), ws, connectorID, expected.GrantID); existing != nil {
		m.writeExistingGrant(w, existing, expected)
		return
	}

	grant, err := m.store.ConnectorGrants().Create(r.Context(), expected)
	if err == nil {
		handler.WriteJSON(w, http.StatusCreated, grant)
		return
	}

	// Close the create race without weakening the exact-authority check. A
	// concurrent identical ensure returns the winner; a different winner fails
	// closed so callers cannot enable a binding against stale scope.
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		if existing := m.findGrant(r.Context(), ws, connectorID, expected.GrantID); existing != nil {
			m.writeExistingGrant(w, existing, expected)
			return
		}
	}
	handler.WriteDomainError(w, err, "create connector grant failed")
}

func decodeCreateGrantRequest(
	w http.ResponseWriter,
	r *http.Request,
	ws string,
	connectorID string,
) (store.ConnectorGrantCreate, bool, bool) {
	var req createGrantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return store.ConnectorGrantCreate{}, false, false
	}
	bindingID := strings.TrimSpace(req.BindingID)
	action := strings.TrimSpace(req.Action)
	resourcePattern := strings.TrimSpace(req.ResourcePattern)
	if bindingID == "" || action == "" || resourcePattern == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding_id, action and resource_pattern are required")
		return store.ConnectorGrantCreate{}, false, false
	}
	grantID := strings.TrimSpace(req.GrantID)
	explicitGrantID := grantID != ""
	if grantID == "" {
		grantID = "grant-" + bindingID + "-" + strings.ReplaceAll(action, ".", "-")
	}

	return store.ConnectorGrantCreate{
		WorkspaceKey:    ws,
		GrantID:         grantID,
		ConnectorID:     connectorID,
		BindingID:       bindingID,
		Action:          action,
		ResourcePattern: resourcePattern,
	}, explicitGrantID, true
}

// findGrant returns the active grant with the given id on the connector, or nil
// if none exists (revoked grants are filtered out by ListByConnector). It backs
// the idempotent "ensure" semantics so re-activating a template does not 409.
func (m *Module) findGrant(ctx context.Context, ws, connectorID, grantID string) *domain.ConnectorGrant {
	grants, err := m.store.ConnectorGrants().ListByConnector(ctx, ws, connectorID)
	if err != nil {
		return nil
	}
	for _, g := range grants {
		if g != nil && g.GrantID == grantID {
			return g
		}
	}
	return nil
}

func (m *Module) writeExistingGrant(w http.ResponseWriter, existing *domain.ConnectorGrant, expected store.ConnectorGrantCreate) {
	if existing.BindingID == expected.BindingID &&
		existing.Action == expected.Action &&
		existing.ResourcePattern == expected.ResourcePattern {
		handler.WriteJSON(w, http.StatusOK, existing)
		return
	}
	handler.RespondError(
		w,
		http.StatusConflict,
		fmt.Sprintf(
			"connector grant %q already exists with different binding, action, or resource scope; refusing to reuse stale authority",
			expected.GrantID,
		),
	)
}

type replaceBindingGrantRequest struct {
	Action          string `json:"action"`
	ResourcePattern string `json:"resource_pattern"`
}

type replaceBindingGrantsRequest struct {
	// Both timestamps are required. CreatedAt fences delete/recreate ABA;
	// UpdatedAt binds this authority set to the exact disabled configuration
	// (including the repository stored in run_input) that the caller observed.
	ExpectedBindingCreatedAt string                       `json:"expected_binding_created_at"`
	ExpectedBindingUpdatedAt string                       `json:"expected_binding_updated_at"`
	Grants                   []replaceBindingGrantRequest `json:"grants"`
}

type replaceBindingGrantsResponse = vault.ReplaceGrantSetResult

// replaceBindingGrants decodes the exact binding snapshot and complete desired
// set, then delegates the restartable replacement ceremony to Connectors.
func (m *Module) replaceBindingGrants(w http.ResponseWriter, r *http.Request) {
	ws := workspaceFromRequest(r)
	connectorID := strings.TrimSpace(r.PathValue("id"))
	bindingID := strings.TrimSpace(r.PathValue("bindingID"))
	if ws == "" || connectorID == "" || bindingID == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace, connector id and binding id are required")
		return
	}
	if !m.authorizeGrantReplacement(w, r, ws) {
		return
	}

	replacement, ok := decodeReplaceBindingGrantsRequest(w, r, ws, connectorID, bindingID)
	if !ok {
		return
	}
	// Serialize with the legacy one-grant POST surface as well as with other
	// complete-set requests in this serve process. The connector owner still
	// performs exact persisted generation/revision checks around every write.
	m.grantMu.Lock()
	defer m.grantMu.Unlock()
	result, err := m.grantSets.Replace(r.Context(), replacement)
	if err != nil {
		handler.WriteDomainError(w, err, "replace connector grants failed")
		return
	}
	handler.WriteJSON(w, http.StatusOK, result)
}

func decodeReplaceBindingGrantsRequest(
	w http.ResponseWriter,
	r *http.Request,
	ws, connectorID, bindingID string,
) (vault.ReplaceGrantSetRequest, bool) {
	var req replaceBindingGrantsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return vault.ReplaceGrantSetRequest{}, false
	}
	expectedCreatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.ExpectedBindingCreatedAt))
	if err != nil {
		handler.RespondError(w, http.StatusBadRequest, "expected_binding_created_at must be an RFC3339 timestamp")
		return vault.ReplaceGrantSetRequest{}, false
	}
	expectedUpdatedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.ExpectedBindingUpdatedAt))
	if err != nil {
		handler.RespondError(w, http.StatusBadRequest, "expected_binding_updated_at must be an RFC3339 timestamp")
		return vault.ReplaceGrantSetRequest{}, false
	}
	var desired []vault.DesiredGrant
	if req.Grants != nil {
		desired = make([]vault.DesiredGrant, 0, len(req.Grants))
		for _, grant := range req.Grants {
			desired = append(desired, vault.DesiredGrant{
				Action:          grant.Action,
				ResourcePattern: grant.ResourcePattern,
			})
		}
	}

	return vault.ReplaceGrantSetRequest{
		WorkspaceKey:             ws,
		ConnectorID:              connectorID,
		BindingID:                bindingID,
		ExpectedBindingCreatedAt: expectedCreatedAt,
		ExpectedBindingUpdatedAt: expectedUpdatedAt,
		Grants:                   desired,
	}, true
}

func (m *Module) authorizeGrantReplacement(w http.ResponseWriter, r *http.Request, workspace string) bool {
	if m == nil || m.operatorAuthority == nil {
		handler.RespondError(w, http.StatusServiceUnavailable, "connector grant authority is unavailable")
		return false
	}
	auth, err := m.operatorAuthority.ResolveOperatorAuthority(r, workspace, automation.ActionUpdateBinding)
	if err != nil {
		switch {
		case errors.Is(err, workflowcataloghttp.ErrUnauthenticated),
			errors.Is(err, authority.ErrInvalidPrincipal),
			errors.Is(err, authority.ErrPrincipalExpired):
			handler.RespondError(w, http.StatusUnauthorized, "authentication required")
		case errors.Is(err, authority.ErrWorkspaceMismatch),
			errors.Is(err, authority.ErrActionNotAllowed),
			errors.Is(err, authority.ErrAdmissionDenied),
			errors.Is(err, authority.ErrPrincipalClass):
			handler.RespondError(w, http.StatusForbidden, "forbidden")
		default:
			handler.RespondError(w, http.StatusServiceUnavailable, "connector grant authority is unavailable")
		}
		return false
	}
	if auth.Workspace() != workspace ||
		auth.Action() != automation.ActionUpdateBinding ||
		strings.TrimSpace(auth.Subject()) == "" ||
		!time.Now().Before(auth.ExpiresAt()) {
		handler.RespondError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}
