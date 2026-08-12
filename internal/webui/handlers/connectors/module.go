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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	vault "github.com/tysonthomas9/loomcli/internal/connector"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

const maxConnectorBodyBytes = 1 << 20

// Module registers connector + grant create routes. It holds the store
// directly (the same shape as the roles/webhooks/triggerbindings modules) plus
// the localsettings data dir needed to bridge the Settings runtime credential.
type Module struct {
	store            store.Store
	localSettingsDir string
}

func NewModule(st store.Store, localSettingsDir string) *Module {
	return &Module{store: st, localSettingsDir: localSettingsDir}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/connectors", m.createConnector)
	mux.HandleFunc("POST /api/workspaces/{ws}/connectors/{id}/grants", m.createGrant)
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
	ws := strings.TrimSpace(r.PathValue("ws"))
	if ws == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace is required")
		return
	}

	var req createConnectorRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	source := domain.ConnectorSourceKind(strings.TrimSpace(req.Source))
	if !source.Valid() {
		handler.RespondError(w, http.StatusBadRequest, "source must be one of github, slack, datadog, internal")
		return
	}
	connectorID := strings.TrimSpace(req.ConnectorID)
	if connectorID == "" {
		connectorID = string(source)
	}

	// Fast idempotent path: an already-provisioned connector is returned
	// untouched (200), so re-activating the same template is safe — and the vault
	// bridge below is skipped entirely, since the existing connector already holds
	// its sealed outbound credential.
	fetch := func() (*domain.Connector, bool) {
		c, err := m.store.Connectors().Get(r.Context(), ws, connectorID)
		return c, err == nil && c != nil
	}
	if handler.WriteExistingIfFound(w, fetch) {
		return
	}

	in := store.ConnectorCreate{
		WorkspaceKey: ws,
		ConnectorID:  connectorID,
		SourceKind:   source,
		DisplayName:  strings.TrimSpace(req.DisplayName),
	}

	// Bridge the Settings token: unseal from localsettings, re-seal under the
	// connector vault bound to (ws, connectorID). Fails closed on any gap.
	if req.ReuseRuntimeCredential {
		sealed, status, err := m.bridgeRuntimeCredential(ws, connectorID, string(source))
		if err != nil {
			handler.RespondError(w, status, err.Error())
			return
		}
		in.OutboundCredentialSealed = sealed
	}

	conn, err := m.store.Connectors().Create(r.Context(), in)
	handler.WriteCreatedOrExisting(w, conn, err, fetch, "create connector failed")
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
	plaintext, err := localsettings.UnsealRuntimeCredential(m.localSettingsDir, settings, provider)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	// NewVaultFromEnv fails when LOOM_CONNECTOR_VAULT_KEY is unset/invalid — a
	// server-side misconfiguration (500). Without the key sealed credentials can
	// never be opened, so refuse the create entirely.
	sealer, err := vault.NewVaultFromEnv()
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("connector vault: %w", err)
	}
	sealed, err := sealer.Seal([]byte(plaintext), vault.CredentialAAD(ws, connectorID))
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
	ws := strings.TrimSpace(r.PathValue("ws"))
	connectorID := strings.TrimSpace(r.PathValue("id"))
	if ws == "" || connectorID == "" {
		handler.RespondError(w, http.StatusBadRequest, "workspace and connector id are required")
		return
	}

	var req createGrantRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConnectorBodyBytes)).Decode(&req); err != nil {
		handler.RespondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	bindingID := strings.TrimSpace(req.BindingID)
	action := strings.TrimSpace(req.Action)
	resourcePattern := strings.TrimSpace(req.ResourcePattern)
	if bindingID == "" || action == "" || resourcePattern == "" {
		handler.RespondError(w, http.StatusBadRequest, "binding_id, action and resource_pattern are required")
		return
	}
	grantID := strings.TrimSpace(req.GrantID)
	if grantID == "" {
		grantID = "grant-" + bindingID + "-" + strings.ReplaceAll(action, ".", "-")
	}

	// Fast idempotent path: a grant with this id already authorizes the action,
	// so re-activating the same template is safe. Grants have no single-key Get,
	// so the fetch scans the connector's active grants for the derived id.
	fetch := func() (*domain.ConnectorGrant, bool) {
		g := m.findGrant(r.Context(), ws, connectorID, grantID)
		return g, g != nil
	}
	if handler.WriteExistingIfFound(w, fetch) {
		return
	}

	grant, err := m.store.ConnectorGrants().Create(r.Context(), store.ConnectorGrantCreate{
		WorkspaceKey:    ws,
		GrantID:         grantID,
		ConnectorID:     connectorID,
		BindingID:       bindingID,
		Action:          action,
		ResourcePattern: resourcePattern,
	})
	handler.WriteCreatedOrExisting(w, grant, err, fetch, "create connector grant failed")
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
