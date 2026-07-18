package serve

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/app/workflowbinding"
	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	authorityhttp "github.com/tysonthomas9/loomcli/internal/platform/authority/httpapi"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestNewWorkflowCatalogModuleDisabledConstructsNothing(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	module, err := NewWorkflowCatalogModule(WorkflowCatalogConfig{
		Enabled:    false,
		RuntimeDir: runtimeDir,
	})
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	if module != nil {
		t.Fatalf("disabled module = %#v, want nil", module)
	}
	assertPathDoesNotExist(t, filepath.Join(runtimeDir, ".loom", "operator"))
}

func TestNewWorkflowCatalogModuleRejectsAutomationWithoutCatalog(t *testing.T) {
	module, err := NewWorkflowCatalogModule(WorkflowCatalogConfig{AutomationEnabled: true})
	if module != nil || err == nil || !strings.Contains(err.Error(), "Workflow Catalog is disabled") {
		t.Fatalf("module = %#v, error = %v", module, err)
	}
}

func TestWorkflowCatalogComposesProductionAutomationCapability(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	state := memstore.New()
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, AutomationEnabled: true, FleetDBClient: client,
		AutomationDriverRuns: state.DriverRuns(), AutomationWorkspaces: state.Workspaces(),
		AutomationWebhookVerifier: webhooks.NewCompatibilityVerifier(webhooks.CompatibilityVerifierConfig{
			Bindings: state.TriggerBindings(), Connectors: state.Connectors(),
		}),
		AutomationAwaits: state.Awaits(),
		RuntimeDir:       t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	capability := module.AutomationCapability()
	if capability == nil || capability.BindingOperations() == nil ||
		capability.WebhookWorkflow() == nil || capability.WorkflowEventing() == nil ||
		capability.WorkflowBinding() == nil ||
		capability.IssueJournalEmitter() == nil || capability.RunOutcomePublisher() == nil {
		t.Fatalf("Automation capability = %#v", capability)
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 2 || registrations[0].Component.ID() != automation.CronSchedulerComponentID ||
		registrations[1].Component.ID() != automation.DeliverySweeperComponentID {
		t.Fatalf("runtime registrations = %#v", registrations)
	}
}

func TestConfiguredWorkflowTargetPreparerProjectsTarget(t *testing.T) {
	preparer := &configuredWorkflowTargetPreparer{prepare: func(_ context.Context, workspace, workflow string) (WorkflowTarget, error) {
		if workspace == "TEST" && workflow == "custom-workflow" {
			return WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-2"}, nil
		}
		return WorkflowTarget{}, domain.ErrNotFound
	}}
	target, err := preparer.PrepareWorkflowTarget(t.Context(), "TEST", "custom-workflow")
	if err != nil {
		t.Fatalf("PrepareWorkflowTarget: %v", err)
	}
	if target.DriverID != "driver-1" || target.DriverVersionID != "version-2" {
		t.Fatalf("target = %+v", target)
	}
	if _, err := (&configuredWorkflowTargetPreparer{}).PrepareWorkflowTarget(t.Context(), "TEST", "custom-workflow"); !errors.Is(err, workflowbinding.ErrUnavailable) {
		t.Fatalf("nil preparation error = %v, want %v", err, workflowbinding.ErrUnavailable)
	}
	if _, err := preparer.PrepareWorkflowTarget(t.Context(), "TEST", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workflow error = %v, want domain.ErrNotFound", err)
	}
}

func TestNewWorkflowCatalogModuleRejectsNilSharedFleetDBClient(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:    true,
		RuntimeDir: runtimeDir,
		Workspace:  "TEST",
	}))
	if module != nil {
		t.Fatalf("module = %#v, want nil", module)
	}
	if !errors.Is(err, workflowcatalog.ErrUnavailable) {
		t.Fatalf("error = %v, want workflowcatalog.ErrUnavailable", err)
	}
	assertPathDoesNotExist(t, filepath.Join(runtimeDir, ".loom", "operator"))
}

func TestWorkflowCatalogCompositionStartsUnscopedAndExposesNarrowSystemResolver(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client, RuntimeDir: t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule without startup workspace: %v", err)
	}
	if module.EffectiveVersionResolver() == nil || module.RequestedVersionResolver() == nil {
		t.Fatal("composition did not expose narrow resolvers")
	}
	auth, err := module.issueEffectiveVersionAuthority("TEST", workflowCatalogCronSchedulerComponent, "resolve activated version for dispatch")
	if err != nil {
		t.Fatalf("issueEffectiveVersionAuthority: %v", err)
	}
	resolved, err := module.EffectiveVersionResolver().ResolveEffectiveVersion(context.Background(), auth, "TEST", "demo")
	if err != nil {
		t.Fatalf("ResolveEffectiveVersion: %v", err)
	}
	if resolved.Version == nil || resolved.Version.VersionID != "v1" || resolved.Driver == nil || resolved.Driver.ActiveVersionID != "v1" {
		t.Fatalf("resolved = %+v", resolved)
	}
}

func TestWorkflowCatalogCompositionRejectsUnregisteredSystemAuthorityComponent(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client, RuntimeDir: t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}

	_, err = module.issueEffectiveVersionAuthority(
		"TEST",
		workflowCatalogRuntimeComponent("automation-dispatch"),
		"resolve activated version for dispatch",
	)
	if !errors.Is(err, errUnregisteredWorkflowCatalogRuntimeComponent) {
		t.Fatalf("unregistered component error = %v, want %v", err, errUnregisteredWorkflowCatalogRuntimeComponent)
	}
}

func TestWorkflowCatalogSystemAuthorityComponentsExistInRuntimeInventory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "archtest", "testdata", "runtime-components.yaml"))
	if err != nil {
		t.Fatalf("read runtime component inventory: %v", err)
	}
	var inventory struct {
		Components []struct {
			ID string `yaml:"id"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &inventory); err != nil {
		t.Fatalf("parse runtime component inventory: %v", err)
	}
	registered := make(map[workflowCatalogRuntimeComponent]struct{}, len(inventory.Components))
	for _, component := range inventory.Components {
		registered[workflowCatalogRuntimeComponent(component.ID)] = struct{}{}
	}
	for component := range workflowCatalogEffectiveVersionRuntimeComponents {
		if _, ok := registered[component]; !ok {
			t.Errorf("system authority component %q is absent from runtime-components.yaml", component)
		}
	}
}

func TestWorkflowCatalogFleetDBBridgeOwnsItsDTOAndErrorVocabulary(t *testing.T) {
	if got := newWorkflowCatalogFleetDBTransport(nil); got != nil {
		t.Fatalf("nil client bridge = %#v, want nil", got)
	}

	for _, test := range []struct {
		name string
		in   error
		want error
	}{
		{name: "not found", in: infrafleetdb.ErrWorkflowCatalogNotFound, want: catalogfleetdb.ErrTransportNotFound},
		{name: "invalid", in: infrafleetdb.ErrWorkflowCatalogInvalid, want: catalogfleetdb.ErrTransportInvalid},
		{name: "revision", in: infrafleetdb.ErrWorkflowCatalogRevisionConflict, want: catalogfleetdb.ErrTransportRevisionConflict},
		{name: "ownership", in: infrafleetdb.ErrWorkflowCatalogVersionOwnership, want: catalogfleetdb.ErrTransportVersionOwnership},
		{name: "validation", in: infrafleetdb.ErrWorkflowCatalogVersionNotValidated, want: catalogfleetdb.ErrTransportVersionNotValidated},
		{name: "approval", in: infrafleetdb.ErrWorkflowCatalogVersionNotApproved, want: catalogfleetdb.ErrTransportVersionNotApproved},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := translateWorkflowCatalogFleetDBError(test.in)
			if !errors.Is(got, test.want) || !errors.Is(got, test.in) {
				t.Fatalf("translated error = %v, want adapter sentinel %v and original %v", got, test.want, test.in)
			}
		})
	}

	driver := &workflowcatalog.Driver{WorkspaceKey: "TEST", DriverID: "driver-1", Revision: 4}
	version := &workflowcatalog.DriverVersion{WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1"}
	got, err := translateWorkflowCatalogFleetDBResult(&infrafleetdb.WorkflowCatalogLifecycleResult{
		Driver: driver, Version: version, Replayed: true, CommittedRevision: 3,
		SemanticImpact: workflowcatalog.SemanticImpactVersionTrustChanged,
	}, nil)
	if err != nil {
		t.Fatalf("translate result: %v", err)
	}
	if got.Driver != driver || got.Version != version || !got.Replayed || got.CommittedRevision != 3 || got.SemanticImpact != workflowcatalog.SemanticImpactVersionTrustChanged {
		t.Fatalf("translated result = %#v", got)
	}
}

func TestLocalWorkflowCatalogCompositionCreatesSecureCredentialAndRequiresIt(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:       true,
		FleetDBClient: client,
		RuntimeDir:    runtimeDir,
		Workspace:     "TEST",
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	if module == nil {
		t.Fatal("NewWorkflowCatalogModule returned nil module")
	}

	credentialDir := filepath.Join(runtimeDir, ".loom", "operator")
	dirInfo, err := os.Lstat(credentialDir)
	if err != nil {
		t.Fatalf("stat credential directory: %v", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("credential directory mode = %v/%04o, want directory/0700", dirInfo.Mode().Type(), dirInfo.Mode().Perm())
	}
	tokenPath := filepath.Join(credentialDir, authority.LocalOperatorTokenFileName)
	tokenInfo, err := os.Lstat(tokenPath)
	if err != nil {
		t.Fatalf("stat operator token: %v", err)
	}
	if !tokenInfo.Mode().IsRegular() || tokenInfo.Mode().Perm() != 0o600 {
		t.Fatalf("operator token mode = %v/%04o, want regular/0600", tokenInfo.Mode().Type(), tokenInfo.Mode().Perm())
	}
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read operator token: %v", err)
	}
	if len(tokenBytes) != 64 {
		t.Fatalf("operator token length = %d, want 64", len(tokenBytes))
	}
	if _, err := hex.DecodeString(string(tokenBytes)); err != nil {
		t.Fatalf("operator token is not lowercase-compatible hex: %v", err)
	}
	token := string(tokenBytes)

	mux := http.NewServeMux()
	module.Register(mux)

	assertCatalogStatus(t, mux, "TEST", "", nil, http.StatusUnauthorized)
	assertCatalogStatus(t, mux, "TEST", "Bearer "+differentToken(token), nil, http.StatusUnauthorized)
	if calls := fleet.Calls(); len(calls) != 0 {
		t.Fatalf("unauthorized requests reached FleetDB: %v", calls)
	}

	// A path alias cannot widen authority or persistence scope; middleware's
	// canonical workspace is the only value admitted and sent to FleetDB.
	response := assertCatalogStatusWithCanonical(t, mux, "alias", "TEST", "Bearer "+token, nil, http.StatusOK)
	var result struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode success response: %v", err)
	}
	if result.Action != "approve" {
		t.Fatalf("action = %q, want approve", result.Action)
	}
	if calls := fleet.Calls(); len(calls) != 4 {
		t.Fatalf("authorized request FleetDB calls = %v, want four capability calls", calls)
	}
}

func TestLocalWorkflowCatalogBrowserLaunchIsSingleUseAndActionScoped(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client, RuntimeDir: runtimeDir, Workspace: "TEST",
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	token, err := authority.ReadLocalOperatorToken(filepath.Join(runtimeDir, ".loom", "operator"))
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	mux := http.NewServeMux()
	module.Register(mux)

	launchReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/operator-sessions/launch", nil)
	launchReq.Header.Set("Authorization", "Bearer "+token)
	launchReq = launchReq.WithContext(middleware.WithWorkspace(launchReq.Context(), "TEST"))
	launchRec := httptest.NewRecorder()
	mux.ServeHTTP(launchRec, launchReq)
	if launchRec.Code != http.StatusCreated {
		t.Fatalf("launch status = %d body=%s", launchRec.Code, launchRec.Body.String())
	}
	var launch struct {
		LaunchCode string `json:"launch_code"`
		Workspace  string `json:"workspace"`
	}
	if err := json.Unmarshal(launchRec.Body.Bytes(), &launch); err != nil || launch.LaunchCode == "" || launch.LaunchCode == token || launch.Workspace != "TEST" {
		t.Fatalf("launch response = %+v err=%v", launch, err)
	}
	if calls := fleet.Calls(); len(calls) != 0 {
		t.Fatalf("launch reached FleetDB: %v", calls)
	}

	// A launch code is not a product-operation bearer.
	assertCatalogStatus(t, mux, "TEST", "Bearer "+launch.LaunchCode, nil, http.StatusUnauthorized)

	exchangeBody := `{"launch_code":"` + launch.LaunchCode + `"}`
	exchangeReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/operator-sessions/exchange", strings.NewReader(exchangeBody))
	exchangeReq = exchangeReq.WithContext(middleware.WithWorkspace(exchangeReq.Context(), "TEST"))
	exchangeRec := httptest.NewRecorder()
	mux.ServeHTTP(exchangeRec, exchangeReq)
	if exchangeRec.Code != http.StatusOK {
		t.Fatalf("exchange status = %d body=%s", exchangeRec.Code, exchangeRec.Body.String())
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(exchangeRec.Body.Bytes(), &session); err != nil || session.AccessToken == "" || session.AccessToken == token || session.AccessToken == launch.LaunchCode {
		t.Fatalf("exchange response = token-len %d err=%v", len(session.AccessToken), err)
	}

	replayReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/operator-sessions/exchange", strings.NewReader(exchangeBody))
	replayReq = replayReq.WithContext(middleware.WithWorkspace(replayReq.Context(), "TEST"))
	replayRec := httptest.NewRecorder()
	mux.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d body=%s", replayRec.Code, replayRec.Body.String())
	}

	assertCatalogStatus(t, mux, "TEST", "Bearer "+session.AccessToken, nil, http.StatusOK)
	if calls := fleet.Calls(); len(calls) != 4 {
		t.Fatalf("browser-authorized request FleetDB calls = %v, want four", calls)
	}
}

func TestLocalBrowserBrokerDelegatesEnabledAutomationOperatorActionsOnly(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	_, _, browserSession, err := composeWorkflowCatalogAuthority(WorkflowCatalogConfig{
		Enabled: true, RuntimeDir: runtimeDir, Workspace: "TEST", AutomationEnabled: true,
	}, authority.NewIssuer())
	if err != nil {
		t.Fatalf("composeWorkflowCatalogAuthority: %v", err)
	}
	if browserSession == nil {
		t.Fatal("local browser session broker is nil")
	}
	token, err := authority.ReadLocalOperatorToken(filepath.Join(runtimeDir, ".loom", "operator"))
	if err != nil {
		t.Fatalf("ReadLocalOperatorToken: %v", err)
	}
	launch, err := browserSession.MintLaunchCode("Bearer "+token, "TEST")
	if err != nil {
		t.Fatalf("MintLaunchCode: %v", err)
	}
	session, err := browserSession.ExchangeLaunchCode(launch.Code, "TEST")
	if err != nil {
		t.Fatalf("ExchangeLaunchCode: %v", err)
	}
	value, err := browserSession.IssueOperator(session.Bearer, "TEST", automation.ActionCreateBinding)
	if err != nil {
		t.Fatalf("IssueOperator create binding: %v", err)
	}
	if value.Workspace() != "TEST" || value.Action() != automation.ActionCreateBinding {
		t.Fatalf("automation operator authority = workspace:%q action:%q", value.Workspace(), value.Action())
	}
	if _, err := browserSession.IssueOperator(session.Bearer, "TEST", automation.ActionAdmitEvent); !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("browser event-ingestion authority error = %v, want %v", err, authority.ErrActionNotAllowed)
	}
}

func TestExternalWorkflowCatalogCompositionTrustsOnlyVerifiedMiddlewareIdentity(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:       true,
		FleetDBClient: client,
		RuntimeDir:    runtimeDir,
		Workspace:     "TEST",
		ExternalAuth:  true,
		ExternalOperatorResolverFactory: testExternalOperatorResolverFactory(func(_ context.Context, workspace string, identity middleware.UserIdentity) (string, error) {
			if workspace != "TEST" || identity.UserID != "verified-user-1" {
				return "developer", nil
			}
			return "maintainer", nil
		}),
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	if module == nil {
		t.Fatal("NewWorkflowCatalogModule returned nil module")
	}
	assertPathDoesNotExist(t, filepath.Join(runtimeDir, ".loom", "operator"))

	mux := http.NewServeMux()
	module.Register(mux)

	// A caller-controlled Authorization header is not proof of authentication.
	assertCatalogStatus(t, mux, "TEST", "Bearer caller-controlled", nil, http.StatusUnauthorized)
	emptyIdentity := middleware.UserIdentity{}
	assertCatalogStatus(t, mux, "TEST", "", &emptyIdentity, http.StatusUnauthorized)
	identity := middleware.UserIdentity{UserID: "verified-user-1", Email: "operator@example.test"}
	assertCatalogStatus(t, mux, "OTHER", "", &identity, http.StatusForbidden)
	developer := middleware.UserIdentity{UserID: "verified-developer"}
	assertCatalogStatus(t, mux, "TEST", "", &developer, http.StatusForbidden)
	if calls := fleet.Calls(); len(calls) != 0 {
		t.Fatalf("unverified or wrong-workspace requests reached FleetDB: %v", calls)
	}

	assertCatalogStatus(t, mux, "TEST", "", &identity, http.StatusOK)
	if calls := fleet.Calls(); len(calls) != 4 {
		t.Fatalf("verified request FleetDB calls = %v, want four capability calls", calls)
	}
}

func TestExternalWorkflowCatalogCompositionFailsClosedWithoutRoleResolver(t *testing.T) {
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:      true,
		RuntimeDir:   t.TempDir(),
		Workspace:    "TEST",
		ExternalAuth: true,
	}))
	if err == nil || !strings.Contains(err.Error(), "operator resolver factory is required") {
		t.Fatalf("NewWorkflowCatalogModule error = %v, want missing resolver error", err)
	}
	if module != nil {
		t.Fatalf("module = %#v, want nil", module)
	}
}

func testWorkflowCatalogConfig(config WorkflowCatalogConfig) WorkflowCatalogConfig {
	config.WorkspaceFromContext = middleware.WorkspaceFromContext
	config.BuiltinWorkflow = func(string) bool { return false }
	config.BrowserSessionRouteFactory = func(
		broker *authority.LocalBrowserSessionBroker,
		workspaceFromContext func(context.Context) string,
	) RouteModule {
		return authorityhttp.New(broker, workspaceFromContext)
	}
	if config.PrepareWorkflowTarget == nil {
		config.PrepareWorkflowTarget = func(error) WorkflowTargetPreparation {
			return func(context.Context, string, string) (WorkflowTarget, error) {
				return WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-1"}, nil
			}
		}
	}
	return config
}

type testExternalOperatorResolver struct {
	issuer          *authority.Issuer
	resolveRole     middleware.WorkspaceRoleResolver
	unauthenticated error
}

func testExternalOperatorResolverFactory(resolveRole middleware.WorkspaceRoleResolver) ExternalOperatorResolverFactory {
	return func(issuer *authority.Issuer, unauthenticated error) OperatorAuthorityResolver {
		return &testExternalOperatorResolver{issuer: issuer, resolveRole: resolveRole, unauthenticated: unauthenticated}
	}
}

func (resolver *testExternalOperatorResolver) ResolveOperatorAuthority(
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	if request == nil {
		return authority.OperatorAuthority{}, resolver.unauthenticated
	}
	identity, ok := middleware.UserIdentityFromContext(request.Context())
	if !ok || strings.TrimSpace(identity.UserID) == "" {
		return authority.OperatorAuthority{}, resolver.unauthenticated
	}
	workspace = strings.TrimSpace(workspace)
	if resolver == nil || resolver.issuer == nil || workspace == "" {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	role, err := resolver.resolveRole(request.Context(), workspace, identity)
	if err != nil {
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "admin", "owner", "maintainer":
	default:
		return authority.OperatorAuthority{}, authority.ErrAdmissionDenied
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: identity.UserID, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return resolver.issuer.IssueOperator(principal, workspace, action)
}

func assertCatalogStatus(t *testing.T, handler http.Handler, workspace, authorization string, identity *middleware.UserIdentity, want int) *httptest.ResponseRecorder {
	return assertCatalogStatusWithCanonical(t, handler, workspace, workspace, authorization, identity, want)
}

func assertCatalogStatusWithCanonical(t *testing.T, handler http.Handler, pathWorkspace, canonicalWorkspace, authorization string, identity *middleware.UserIdentity, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+pathWorkspace+"/workflows/demo/versions/v1/approve", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if identity != nil {
		request = request.WithContext(middleware.WithUserIdentity(request.Context(), *identity))
	}
	request = request.WithContext(middleware.WithWorkspace(request.Context(), canonicalWorkspace))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("POST path-workspace=%q canonical-workspace=%q status=%d body=%s, want %d", pathWorkspace, canonicalWorkspace, response.Code, response.Body.String(), want)
	}
	return response
}

func differentToken(token string) string {
	if token == "" {
		return strings.Repeat("0", 64)
	}
	replacement := byte('0')
	if token[0] == replacement {
		replacement = '1'
	}
	return string(replacement) + token[1:]
}

func assertPathDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("path %q unexpectedly exists or returned another error: %v", path, err)
	}
}

type catalogFleetStub struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
}

func newCatalogFleetClient(t *testing.T) (*infrafleetdb.Client, *catalogFleetStub) {
	t.Helper()
	stub := &catalogFleetStub{t: t}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	client, err := infrafleetdb.New(infrafleetdb.Config{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("fleetdb.New: %v", err)
	}
	return client, stub
}

func (s *catalogFleetStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()

	driver := &workflowcatalog.Driver{
		WorkspaceKey:    "TEST",
		DriverID:        "demo",
		Name:            "demo",
		OwnerType:       workflowcatalog.DriverOwnerUser,
		Status:          workflowcatalog.DriverStatusDraft,
		ActiveVersionID: "v1",
		Metadata:        map[string]string{"preserved": "yes"},
		Revision:        1,
	}
	version := &workflowcatalog.DriverVersion{
		WorkspaceKey:     "TEST",
		DriverID:         "demo",
		VersionID:        "v1",
		Version:          1,
		SourceRef:        "src/demo.ts",
		SourceDigest:     "sha256:source",
		BundleRef:        "bundle/demo-v1.tgz",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/TEST/drivers/demo":
		writeCatalogFleetJSON(w, driver)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/TEST/driver-versions/v1":
		writeCatalogFleetJSON(w, version)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/TEST/drivers/demo/versions/v1/approve":
		var body struct {
			ExpectedRevision uint64 `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ExpectedRevision != 1 {
			s.t.Errorf("lifecycle request body = %+v err=%v, want expected_revision=1", body, err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		after := *driver
		after.Revision = 2
		after.Metadata = map[string]string{
			"preserved": "yes",
			workflowcatalog.ApprovedVersionMetadataKey("v1"): version.SourceDigest,
		}
		writeCatalogFleetJSON(w, infrafleetdb.WorkflowCatalogLifecycleResult{
			CommittedRevision: 2,
			SemanticImpact:    workflowcatalog.SemanticImpactVersionTrustChanged,
			Driver:            &after,
			Version:           version,
		})
	default:
		s.t.Errorf("unexpected FleetDB request: %s %s", r.Method, r.URL.RequestURI())
		http.NotFound(w, r)
	}
}

func (s *catalogFleetStub) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func writeCatalogFleetJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
