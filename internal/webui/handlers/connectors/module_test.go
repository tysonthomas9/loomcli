package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	connectorvault "github.com/tysonthomas9/loomcli/internal/infra/connectorsvault"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localsettings"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// newTestServer wires the connectors module over a fresh memstore. No
// localSettingsDir is needed: these tests never set reuse_runtime_credential, so
// the Settings-token vault bridge is not exercised.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerWithStore(t, memstore.New())
}

func newTestServerWithStore(t *testing.T, st store.Store) *httptest.Server {
	return newTestServerWithStoreAndSettings(t, st, "")
}

func newTestServerWithStoreAndSettings(t *testing.T, st store.Store, localSettingsDir string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	NewModule(st, localSettingsDir, &connectorBindingQueries{store: st.TriggerBindings()}, connectorTestOperatorResolver{}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type connectorTestOperatorResolver struct{}

type connectorBindingQueries struct {
	store store.TriggerBindingStore
}

func (queries *connectorBindingQueries) GetBinding(
	ctx context.Context,
	workspace, bindingID string,
) (*automation.Binding, error) {
	binding, err := queries.store.Get(ctx, workspace, bindingID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, errors.Join(automation.ErrNotFound, err)
		}
		return nil, err
	}
	if binding == nil {
		return nil, nil
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	var projected automation.Binding
	if err := json.Unmarshal(raw, &projected); err != nil {
		return nil, err
	}
	return &projected, nil
}

func (connectorTestOperatorResolver) ResolveOperatorAuthority(
	_ *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	issuer := authority.NewIssuer()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "connector-test-operator",
		Class:     authority.ClassOperator,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return issuer.IssueOperator(principal, workspace, action)
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	return resp.StatusCode, out
}

func putJSON(t *testing.T, srv *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut, srv.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build PUT %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	return resp.StatusCode, out
}

// TestCreateConnectorIsIdempotent pins the "ensure" contract the create-agent
// gallery relies on: the first create returns 201, and re-activating the same
// template (same connector_id) returns 200 with the existing connector rather
// than a 409 that would fail the whole activation.
func TestCreateConnectorIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	const path = "/api/workspaces/WS/connectors"
	req := map[string]any{"source": "github", "connector_id": "github"}

	status, raw := postJSON(t, srv, path, req)
	if status != http.StatusCreated {
		t.Fatalf("first create: status = %d, want 201 (body %s)", status, raw)
	}

	status, raw = postJSON(t, srv, path, req)
	if status != http.StatusOK {
		t.Fatalf("re-create: status = %d, want 200 ensure (body %s)", status, raw)
	}
	var conn struct {
		ConnectorID string `json:"connector_id"`
	}
	if err := json.Unmarshal(raw, &conn); err != nil {
		t.Fatalf("decode connector: %v (body %s)", err, raw)
	}
	if conn.ConnectorID != "github" {
		t.Fatalf("ensure returned connector_id = %q, want github", conn.ConnectorID)
	}
}

func TestCreateConnectorSynchronizesRotatedRuntimeCredential(t *testing.T) {
	t.Setenv(connectorvault.VaultKeyEnvVar, "")
	dir := t.TempDir()
	st := memstore.New()
	saveGitHubCredential := func(value string, at time.Time) {
		t.Helper()
		credential, err := localsettings.SealRuntimeCredential(
			dir,
			localsettings.RuntimeCredentialProviderGitHub,
			value,
			at,
		)
		if err != nil {
			t.Fatalf("seal runtime credential: %v", err)
		}
		settings := localsettings.Default()
		settings.RuntimeCredentials.GitHub = credential
		if err := localsettings.Save(dir, settings); err != nil {
			t.Fatalf("save runtime credential: %v", err)
		}
	}

	now := time.Now().UTC()
	saveGitHubCredential("github-token-a", now)
	srv := newTestServerWithStoreAndSettings(t, st, dir)
	const path = "/api/workspaces/WS/connectors"
	request := map[string]any{
		"source":                   "github",
		"connector_id":             "github",
		"reuse_runtime_credential": true,
	}
	status, raw := postJSON(t, srv, path, request)
	if status != http.StatusCreated {
		t.Fatalf("initial create: status = %d body=%s", status, raw)
	}

	saveGitHubCredential("github-token-b", now.Add(time.Minute))
	status, raw = postJSON(t, srv, path, request)
	if status != http.StatusOK {
		t.Fatalf("rotated ensure: status = %d body=%s", status, raw)
	}
	rotated, err := st.Connectors().Get(context.Background(), "WS", "github")
	if err != nil {
		t.Fatalf("get rotated connector: %v", err)
	}
	sealed, err := st.Connectors().ResolveOutboundCredentialSealed(context.Background(), "WS", "github")
	if err != nil {
		t.Fatalf("resolve rotated credential: %v", err)
	}
	sealer, err := connectorvault.NewVaultFromEnvOrKeyFile(dir)
	if err != nil {
		t.Fatalf("open connector vault: %v", err)
	}
	plaintext, err := sealer.Unseal(sealed, connectorvault.CredentialAAD("WS", "github"))
	if err != nil {
		t.Fatalf("unseal rotated credential: %v", err)
	}
	if string(plaintext) != "github-token-b" {
		t.Fatalf("rotated credential = %q, want github-token-b", plaintext)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}

	status, raw = postJSON(t, srv, path, request)
	if status != http.StatusOK {
		t.Fatalf("idempotent rotated ensure: status = %d body=%s", status, raw)
	}
	unchanged, err := st.Connectors().Get(context.Background(), "WS", "github")
	if err != nil {
		t.Fatalf("get unchanged connector: %v", err)
	}
	if !unchanged.UpdatedAt.Equal(rotated.UpdatedAt) {
		t.Fatalf("matching credential was rotated again: %s -> %s", rotated.UpdatedAt, unchanged.UpdatedAt)
	}
}

// TestCreateGrantIsIdempotent pins the same ensure contract for grants: a
// repeated grant returns 200 (not 409), while a genuinely different action is
// still created (201) — guarding findGrant's match-by-derived-id logic.
func TestCreateGrantIsIdempotent(t *testing.T) {
	srv := newTestServer(t)
	const grantsPath = "/api/workspaces/WS/connectors/github/grants"
	grant := map[string]any{
		"binding_id":       "s2-review-loop",
		"action":           "github.pull_request.read",
		"resource_pattern": "repo:octocat/hello",
	}

	status, raw := postJSON(t, srv, grantsPath, grant)
	if status != http.StatusCreated {
		t.Fatalf("first grant: status = %d, want 201 (body %s)", status, raw)
	}
	var first struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("decode grant: %v (body %s)", err, raw)
	}
	if first.GrantID != "grant-s2-review-loop-github-pull_request-read" {
		t.Fatalf("derived grant_id = %q, want grant-s2-review-loop-github-pull_request-read", first.GrantID)
	}

	// Re-activating the same grant is exists-ok, not a 409.
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusOK {
		t.Fatalf("re-grant: status = %d, want 200 ensure (body %s)", status, raw)
	}
	var second struct {
		GrantID string `json:"grant_id"`
	}
	if err := json.Unmarshal(raw, &second); err != nil {
		t.Fatalf("decode ensure grant: %v (body %s)", err, raw)
	}
	if second.GrantID != first.GrantID {
		t.Fatalf("ensure returned grant_id = %q, want the existing %q", second.GrantID, first.GrantID)
	}

	// A different action is a distinct grant (distinct derived id) — created.
	grant["action"] = "github.review.post"
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusCreated {
		t.Fatalf("distinct-action grant: status = %d, want 201 (body %s)", status, raw)
	}
}

// TestCreateGrantRejectsDifferentResourceForSameID pins the authority half of
// the ensure contract. Singleton workflow templates derive a stable grant id
// from binding+action, so changing the target repo must fail closed instead of
// returning the stale repo-scoped grant as a successful ensure.
func TestCreateGrantRejectsDifferentResourceForSameID(t *testing.T) {
	srv := newTestServer(t)
	const grantsPath = "/api/workspaces/WS/connectors/github/grants"
	grant := map[string]any{
		"binding_id":       "s2-review-loop",
		"action":           "github.pull_request.read",
		"resource_pattern": "repo:octocat/hello",
	}

	status, raw := postJSON(t, srv, grantsPath, grant)
	if status != http.StatusCreated {
		t.Fatalf("first grant: status = %d, want 201 (body %s)", status, raw)
	}

	grant["resource_pattern"] = "repo:octocat/goodbye"
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusConflict {
		t.Fatalf("retargeted grant: status = %d, want 409 (body %s)", status, raw)
	}
	var conflict struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &conflict); err != nil {
		t.Fatalf("decode conflict: %v (body %s)", err, raw)
	}
	if !strings.Contains(conflict.Error, "refusing to reuse stale authority") {
		t.Fatalf("conflict error = %q, want stale-authority explanation", conflict.Error)
	}

	// The conflict neither replaces nor corrupts the original authority: an
	// exact retry remains idempotent.
	grant["resource_pattern"] = "repo:octocat/hello"
	status, raw = postJSON(t, srv, grantsPath, grant)
	if status != http.StatusOK {
		t.Fatalf("original grant retry: status = %d, want 200 (body %s)", status, raw)
	}
}

func seedGrantReplacementFixture(t *testing.T) (store.Store, *automation.Binding) {
	t.Helper()
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "review-loop", Name: "review-loop",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "review-loop-v1", DriverID: "review-loop", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	if _, err := st.Connectors().Create(ctx, store.ConnectorCreate{
		WorkspaceKey: "WS", ConnectorID: "github", SourceKind: domain.ConnectorSourceGitHub,
	}); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	binding, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "s2-review-loop", Name: "review-loop",
		SourceKind: store.CronSourceKind, Schedule: "*/10 * * * *",
		DriverID: "review-loop", DriverVersionID: "review-loop-v1", Enabled: false,
	})
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	return st, binding
}

func replaceGrantSetBody(binding *automation.Binding, repo string) map[string]any {
	return map[string]any{
		"expected_binding_created_at": binding.CreatedAt.Format(time.RFC3339Nano),
		"expected_binding_updated_at": binding.UpdatedAt.Format(time.RFC3339Nano),
		"grants": []map[string]string{
			{"action": "github.pull_request.read", "resource_pattern": "repo:" + repo},
			{"action": "github.compare.read", "resource_pattern": "repo:" + repo},
			{"action": "github.review.post", "resource_pattern": "repo:" + repo},
		},
	}
}

func TestReplaceBindingGrantsRetargetsWithoutRetainingOldScope(t *testing.T) {
	st, binding := seedGrantReplacementFixture(t)
	srv := newTestServerWithStore(t, st)
	const path = "/api/workspaces/WS/connectors/github/bindings/s2-review-loop/grants"

	status, raw := putJSON(t, srv, path, replaceGrantSetBody(binding, "acme/alpha"))
	if status != http.StatusOK {
		t.Fatalf("initial replace: status = %d, want 200 (body %s)", status, raw)
	}
	var first replaceBindingGrantsResponse
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("decode initial replace: %v (body %s)", err, raw)
	}
	if len(first.Grants) != 3 || first.GrantsRevoked != 0 {
		t.Fatalf("initial replace = %+v, want 3 grants and 0 revoked", first)
	}
	firstIDs := make(map[string]struct{}, len(first.Grants))
	for _, grant := range first.Grants {
		firstIDs[grant.GrantID] = struct{}{}
		if grant.ResourcePattern != "repo:acme/alpha" || !strings.Contains(grant.GrantID, "-g") {
			t.Fatalf("initial grant = %+v, want alpha scope on current generation", grant)
		}
	}

	// An exact repeat preserves the same active rows: no 409, no authority
	// churn, and no duplicate grants.
	status, raw = putJSON(t, srv, path, replaceGrantSetBody(binding, "acme/alpha"))
	if status != http.StatusOK {
		t.Fatalf("idempotent replace: status = %d, want 200 (body %s)", status, raw)
	}
	var repeated replaceBindingGrantsResponse
	if err := json.Unmarshal(raw, &repeated); err != nil {
		t.Fatalf("decode repeated replace: %v", err)
	}
	if repeated.GrantsRevoked != 0 || len(repeated.Grants) != 3 {
		t.Fatalf("repeated replace = %+v, want same 3 grants and 0 revoked", repeated)
	}
	for _, grant := range repeated.Grants {
		if _, ok := firstIDs[grant.GrantID]; !ok {
			t.Fatalf("idempotent replace minted unexpected grant %q", grant.GrantID)
		}
	}

	// The UI patches run_input before reconciling grants. Retargeting to beta
	// therefore carries a new exact binding revision while preserving CreatedAt.
	sourceConfig := `{"targetRepo":"beta","githubRepo":"acme/beta"}`
	updated, err := st.TriggerBindings().Update(context.Background(), "WS", binding.BindingID, store.TriggerBindingUpdate{
		SourceConfigRef: &sourceConfig,
	})
	if err != nil {
		t.Fatalf("update binding target: %v", err)
	}
	status, raw = putJSON(t, srv, path, replaceGrantSetBody(updated, "acme/beta"))
	if status != http.StatusOK {
		t.Fatalf("retargeted replace: status = %d, want 200 (body %s)", status, raw)
	}
	var retargeted replaceBindingGrantsResponse
	if err := json.Unmarshal(raw, &retargeted); err != nil {
		t.Fatalf("decode retargeted replace: %v", err)
	}
	if retargeted.GrantsRevoked != 3 || len(retargeted.Grants) != 3 {
		t.Fatalf("retargeted replace = %+v, want 3 replacements and 3 revoked", retargeted)
	}
	active, err := st.ConnectorGrants().ListByBinding(context.Background(), "WS", binding.BindingID)
	if err != nil {
		t.Fatalf("list retargeted grants: %v", err)
	}
	if len(active) != 3 {
		t.Fatalf("active grants after retarget = %d, want 3", len(active))
	}
	for _, grant := range active {
		if grant.ResourcePattern != "repo:acme/beta" {
			t.Fatalf("retarget left stale authority active: %+v", grant)
		}
		if _, reused := firstIDs[grant.GrantID]; reused {
			t.Fatalf("retarget reused revoked alpha grant id %q", grant.GrantID)
		}
	}
}

func TestReplaceBindingGrantsFencesRecreatedBindingGeneration(t *testing.T) {
	st, oldBinding := seedGrantReplacementFixture(t)
	srv := newTestServerWithStore(t, st)
	const path = "/api/workspaces/WS/connectors/github/bindings/s2-review-loop/grants"
	status, raw := putJSON(t, srv, path, replaceGrantSetBody(oldBinding, "acme/alpha"))
	if status != http.StatusOK {
		t.Fatalf("seed old generation grants: status = %d (body %s)", status, raw)
	}
	var oldSet replaceBindingGrantsResponse
	if err := json.Unmarshal(raw, &oldSet); err != nil {
		t.Fatalf("decode old generation grants: %v", err)
	}
	oldIDs := make(map[string]struct{}, len(oldSet.Grants))
	for _, grant := range oldSet.Grants {
		oldIDs[grant.GrantID] = struct{}{}
	}

	// Simulate an interrupted delete that left orphan grants, then recreate the
	// singleton id. Old request timestamps must not authorize this new row.
	if err := st.TriggerBindings().Delete(context.Background(), "WS", oldBinding.BindingID); err != nil {
		t.Fatalf("delete old binding: %v", err)
	}
	time.Sleep(time.Millisecond)
	newBinding, err := st.TriggerBindings().Create(context.Background(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "s2-review-loop", Name: "review-loop",
		SourceKind: store.CronSourceKind, Schedule: "*/10 * * * *",
		DriverID: "review-loop", DriverVersionID: "review-loop-v1", Enabled: false,
	})
	if err != nil {
		t.Fatalf("recreate binding: %v", err)
	}
	status, raw = putJSON(t, srv, path, replaceGrantSetBody(oldBinding, "acme/alpha"))
	if status != http.StatusConflict {
		t.Fatalf("stale generation replace: status = %d, want 409 (body %s)", status, raw)
	}

	// The current generation may reconcile the same logical scope. It revokes
	// the orphan rows rather than adopting authority minted for the old row.
	status, raw = putJSON(t, srv, path, replaceGrantSetBody(newBinding, "acme/alpha"))
	if status != http.StatusOK {
		t.Fatalf("new generation replace: status = %d, want 200 (body %s)", status, raw)
	}
	var replaced replaceBindingGrantsResponse
	if err := json.Unmarshal(raw, &replaced); err != nil {
		t.Fatalf("decode new generation replace: %v", err)
	}
	if replaced.GrantsRevoked != 3 || len(replaced.Grants) != 3 {
		t.Fatalf("new generation replace = %+v, want old 3 revoked and new 3 active", replaced)
	}
	for _, grant := range replaced.Grants {
		if _, staleID := oldIDs[grant.GrantID]; staleID || !strings.Contains(grant.GrantID, "-g") {
			t.Fatalf("grant reused authority from the deleted generation: %+v", grant)
		}
	}
}

func TestReplaceBindingGrantsRequiresExactDisabledRevisionBeforeMutation(t *testing.T) {
	st, binding := seedGrantReplacementFixture(t)
	srv := newTestServerWithStore(t, st)
	const path = "/api/workspaces/WS/connectors/github/bindings/s2-review-loop/grants"
	status, raw := putJSON(t, srv, path, replaceGrantSetBody(binding, "acme/alpha"))
	if status != http.StatusOK {
		t.Fatalf("seed grants: status = %d (body %s)", status, raw)
	}
	enabled := true
	enabledBinding, err := st.TriggerBindings().Update(context.Background(), "WS", binding.BindingID, store.TriggerBindingUpdate{
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("enable binding: %v", err)
	}

	status, raw = putJSON(t, srv, path, replaceGrantSetBody(enabledBinding, "acme/beta"))
	if status != http.StatusConflict {
		t.Fatalf("enabled replace: status = %d, want 409 (body %s)", status, raw)
	}
	active, err := st.ConnectorGrants().ListByBinding(context.Background(), "WS", binding.BindingID)
	if err != nil {
		t.Fatalf("list after rejected replace: %v", err)
	}
	if len(active) != 3 {
		t.Fatalf("rejected enabled replace changed grant count to %d", len(active))
	}
	for _, grant := range active {
		if grant.ResourcePattern != "repo:acme/alpha" {
			t.Fatalf("rejected enabled replace changed authority: %+v", grant)
		}
	}
}

func TestReplaceBindingGrantsUsesCanonicalWorkspaceContext(t *testing.T) {
	st, binding := seedGrantReplacementFixture(t)
	mux := http.NewServeMux()
	NewModule(st, "", &connectorBindingQueries{store: st.TriggerBindings()}, connectorTestOperatorResolver{}).Register(mux)
	raw, err := json.Marshal(replaceGrantSetBody(binding, "acme/alpha"))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/workspaces/WORKSPACE-ALIAS/connectors/github/bindings/s2-review-loop/grants",
		bytes.NewReader(raw),
	)
	req = req.WithContext(middleware.WithWorkspace(req.Context(), "WS"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("canonical workspace replace: status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	grants, err := st.ConnectorGrants().ListByBinding(context.Background(), "WS", binding.BindingID)
	if err != nil || len(grants) != 3 {
		t.Fatalf("canonical workspace grants = %+v err=%v, want 3", grants, err)
	}
	aliasGrants, err := st.ConnectorGrants().ListByBinding(context.Background(), "WORKSPACE-ALIAS", binding.BindingID)
	if err != nil || len(aliasGrants) != 0 {
		t.Fatalf("alias workspace grants = %+v err=%v, want none", aliasGrants, err)
	}
}
