package serve

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	catalogfleetdb "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/webhooks"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

func TestNewWorkflowCatalogModuleDisabledConstructsNothing(t *testing.T) {
	module, err := NewWorkflowCatalogModule(WorkflowCatalogConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	if module != nil {
		t.Fatalf("disabled module = %#v, want nil", module)
	}
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
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	capability := module.AutomationCapability()
	if capability == nil || capability.BindingOperations() == nil ||
		capability.WebhookWorkflow() == nil || capability.WorkflowEventing() == nil ||
		capability.WorkflowBinding() == nil ||
		capability.IssueJournalEmitter() == nil || capability.ApprovalJournal() == nil ||
		capability.ApprovalAuthorityProvider() == nil || capability.RunOutcomePublisher() == nil {
		t.Fatalf("Automation capability = %#v", capability)
	}
	registrations := capability.RuntimeRegistrations()
	if len(registrations) != 2 || registrations[0].Component.ID() != automation.CronSchedulerComponentID ||
		registrations[1].Component.ID() != automation.DeliverySweeperComponentID {
		t.Fatalf("runtime registrations = %#v", registrations)
	}
}

func TestConfiguredWorkflowTargetPreparerProjectsTarget(t *testing.T) {
	preparer := NewWorkflowTargetPreparer(func(_ context.Context, workspace, workflow string) (WorkflowTarget, error) {
		if workspace == "TEST" && workflow == "custom-workflow" {
			return WorkflowTarget{DriverID: "driver-1", DriverVersionID: "version-2"}, nil
		}
		return WorkflowTarget{}, domain.ErrNotFound
	})
	target, err := preparer.PrepareWorkflowTarget(t.Context(), "TEST", "custom-workflow")
	if err != nil {
		t.Fatalf("PrepareWorkflowTarget: %v", err)
	}
	if target.DriverID != "driver-1" || target.DriverVersionID != "version-2" {
		t.Fatalf("target = %+v", target)
	}
	if _, err := NewWorkflowTargetPreparer(nil).PrepareWorkflowTarget(t.Context(), "TEST", "custom-workflow"); !errors.Is(err, workflowbinding.ErrUnavailable) {
		t.Fatalf("nil preparation error = %v, want %v", err, workflowbinding.ErrUnavailable)
	}
	if _, err := preparer.PrepareWorkflowTarget(t.Context(), "TEST", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing workflow error = %v, want domain.ErrNotFound", err)
	}
}

func TestNewWorkflowCatalogModuleRejectsNilSharedFleetDBClient(t *testing.T) {
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:   true,
		Workspace: "TEST",
	}))
	if module != nil {
		t.Fatalf("module = %#v, want nil", module)
	}
	if !errors.Is(err, workflowcatalog.ErrUnavailable) {
		t.Fatalf("error = %v, want workflowcatalog.ErrUnavailable", err)
	}
}

func TestWorkflowCatalogCompositionStartsUnscopedAndExposesNarrowSystemResolver(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client,
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
		Enabled: true, FleetDBClient: client,
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

func TestWorkflowCatalogManagedBuiltinAuthorityIsExactPurpose(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client,
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	granted, err := module.ManagedBuiltinAuthoringAuthority("TEST", "refresh embedded prompt-agent")
	if err != nil {
		t.Fatalf("ManagedBuiltinAuthoringAuthority: %v", err)
	}
	if granted.Subject() != string(workflowCatalogBuiltinDistributionComponent) ||
		granted.Workspace() != "TEST" ||
		granted.Action() != workflowcatalog.ActionAuthorManagedVersion ||
		granted.Reason() != "refresh embedded prompt-agent" {
		t.Fatalf("managed builtin authority = subject:%q workspace:%q action:%q reason:%q",
			granted.Subject(), granted.Workspace(), granted.Action(), granted.Reason())
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
		{name: "authoring conflict", in: infrafleetdb.ErrWorkflowCatalogAuthoringConflict, want: catalogfleetdb.ErrTransportAuthoringConflict},
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

	authored, err := translateWorkflowCatalogFleetDBAuthoringResult(&infrafleetdb.WorkflowCatalogAuthorVersionResult{
		Driver: driver, Version: version,
		CreatedDriver: true, CreatedVersion: true, ReusedVersion: false,
		Activated: true, Replayed: true, CommittedRevision: 4,
		SemanticImpact: workflowcatalog.SemanticImpactVersionAuthored,
	}, nil)
	if err != nil {
		t.Fatalf("translate authoring result: %v", err)
	}
	if authored.Driver != driver || authored.Version != version ||
		!authored.CreatedDriver || !authored.CreatedVersion || authored.ReusedVersion ||
		!authored.Activated || !authored.Replayed || authored.CommittedRevision != 4 ||
		authored.SemanticImpact != workflowcatalog.SemanticImpactVersionAuthored {
		t.Fatalf("translated authoring result = %#v", authored)
	}
}

func TestLocalOpenWorkflowCatalogDerivesExactAuthorityWithoutCredential(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:       true,
		FleetDBClient: client,
		Workspace:     "TEST",
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	if module == nil {
		t.Fatal("NewWorkflowCatalogModule returned nil module")
	}

	mux := http.NewServeMux()
	module.Register(mux)

	// A path alias cannot widen authority or persistence scope; middleware's
	// canonical workspace is the only value admitted and sent to FleetDB.
	response := assertCatalogStatusWithCanonical(t, mux, "alias", "TEST", "", nil, http.StatusOK)
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

func TestWorkflowCatalogCompositionUsesAtomicAuthoringAndAuthorityDerivedAuditActor(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client, Workspace: "TEST",
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	authoring := module.VersionAuthoringAPI()
	if authoring == nil {
		t.Fatal("composed VersionAuthoringAPI is nil")
	}

	operatorRequest := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions", nil)
	operatorAuth, err := module.operatorResolver.ResolveOperatorAuthority(
		operatorRequest,
		"TEST",
		workflowcatalog.ActionAuthorVersion,
	)
	if err != nil {
		t.Fatalf("resolve operator authoring authority: %v", err)
	}
	operatorCommand := workflowcatalog.AuthorVersionCommand{
		WorkspaceKey: "TEST", RequestID: "operator-request-1", ExpectedRevision: 0,
		DriverID: "demo", DriverName: "demo", VersionID: "demo-v-bbbbbbbbbbbb",
		SourceRef:    "api://workflows/demo/versions/source",
		SourceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BundleRef:    ".loom/drivers/demo/demo-v-bbbbbbbbbbbb",
		BundleDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Runtime:      "flue-node", Manifest: map[string]string{"entrypoint": "run"},
		BuildDiagnostics: "built",
	}
	operatorResult, err := authoring.AuthorVersion(t.Context(), operatorAuth, operatorCommand)
	if err != nil {
		t.Fatalf("AuthorVersion: %v", err)
	}
	if operatorResult.Version.CreatedBy != localOpenOperatorSubject ||
		operatorResult.Version.Manifest[workflowcatalog.ManifestTrustLevelKey] != string(workflowcatalog.DriverTrustUntrusted) ||
		operatorResult.Activated {
		t.Fatalf("operator result = %+v", operatorResult)
	}

	managedCommand := operatorCommand
	managedCommand.RequestID = "managed-request-1"
	managedCommand.ExpectedRevision = 1
	managedCommand.DriverID = workflowcatalog.BuiltinEpicRunnerWorkflowName
	managedCommand.DriverName = managedCommand.DriverID
	managedCommand.SourceRef = workflowcatalog.BuiltinSourceRef(managedCommand.DriverID, managedCommand.SourceDigest)
	managedCommand.VersionID = workflowcatalog.BuiltinVersionID(managedCommand.DriverID, managedCommand.BundleDigest)
	managedCommand.BundleRef = workflowcatalog.BuiltinBundleRef(managedCommand.DriverID, managedCommand.VersionID)
	managedCommand.Manifest = map[string]string{
		"driver_id": managedCommand.DriverID, "driver_name": managedCommand.DriverName,
		"workflow_name": managedCommand.DriverID, "source_ref": managedCommand.SourceRef,
		"source_digest": managedCommand.SourceDigest, "runtime": managedCommand.Runtime,
		"provenance": workflowcatalog.ManagedBuiltinProvenance, "entrypoint": "run",
	}
	principal, err := module.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "builtin-distribution", Class: authority.ClassSystem, Workspace: "TEST",
		Actions:   []authority.Action{workflowcatalog.ActionAuthorManagedVersion},
		ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	systemAuth, err := module.issuer.IssueSystem(
		principal,
		"TEST",
		workflowcatalog.ActionAuthorManagedVersion,
		"refresh embedded builtin",
	)
	if err != nil {
		t.Fatal(err)
	}
	managedResult, err := authoring.AuthorManagedVersion(
		t.Context(),
		systemAuth,
		workflowcatalog.AuthorManagedVersionCommand{
			AuthorVersionCommand: managedCommand,
			Activate:             true,
		},
	)
	if err != nil {
		t.Fatalf("AuthorManagedVersion: %v", err)
	}
	if managedResult.Version.CreatedBy != "builtin-distribution" ||
		managedResult.Version.Manifest[workflowcatalog.ManifestTrustLevelKey] != string(workflowcatalog.DriverTrustTrusted) ||
		!managedResult.Activated {
		t.Fatalf("managed result = %+v", managedResult)
	}

	calls := fleet.Calls()
	if len(calls) != 2 ||
		!strings.Contains(calls[0], "/drivers/demo/versions/author") ||
		!strings.Contains(calls[1], "/drivers/epic-runner/versions/author-managed") {
		t.Fatalf("FleetDB authoring calls = %v", calls)
	}
}

func TestLocalOpenWorkflowCatalogHasNoOperatorSessionRoutes(t *testing.T) {
	client, _ := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled: true, FleetDBClient: client, Workspace: "TEST",
	}))
	if err != nil {
		t.Fatalf("NewWorkflowCatalogModule: %v", err)
	}
	mux := http.NewServeMux()
	module.Register(mux)

	for _, suffix := range []string{"launch", "exchange"} {
		request := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/operator-sessions/"+suffix, nil)
		request = request.WithContext(middleware.WithWorkspace(request.Context(), "TEST"))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("operator session %s status = %d, want 404", suffix, response.Code)
		}
	}
}

func TestLocalOpenResolverAllowsEveryRegisteredOperatorActionOnly(t *testing.T) {
	_, resolver, err := composeWorkflowCatalogAuthority(WorkflowCatalogConfig{
		Enabled: true, Workspace: "TEST", AutomationEnabled: true,
	}, authority.NewIssuer())
	if err != nil {
		t.Fatalf("composeWorkflowCatalogAuthority: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/trigger-bindings", nil)
	actions := append(workflowCatalogOperatorActions(),
		execution.ActionSubmitDriverRun,
		execution.ActionCreateWorkerProfile,
		execution.ActionUpdateWorkerProfile,
		execution.ActionDeleteWorkerProfile,
	)
	actions = append(actions, automationOperatorActions()...)
	for _, action := range actions {
		value, resolveErr := resolver.ResolveOperatorAuthority(request, "TEST", action)
		if resolveErr != nil {
			t.Errorf("ResolveOperatorAuthority(%q): %v", action, resolveErr)
			continue
		}
		if value.Subject() != localOpenOperatorSubject || value.Workspace() != "TEST" || value.Action() != action {
			t.Errorf("operator authority for %q = subject:%q workspace:%q action:%q", action, value.Subject(), value.Workspace(), value.Action())
		}
	}
	if _, err := resolver.ResolveOperatorAuthority(request, "TEST", automation.ActionAdmitEvent); !errors.Is(err, authority.ErrActionNotAllowed) {
		t.Fatalf("local open event-ingestion authority error = %v, want %v", err, authority.ErrActionNotAllowed)
	}
	if _, err := resolver.ResolveOperatorAuthority(nil, "TEST", workflowcatalog.ActionApproveVersion); !errors.Is(err, httpapi.ErrUnauthenticated) {
		t.Fatalf("nil request authority error = %v, want %v", err, httpapi.ErrUnauthenticated)
	}
}

func TestExternalWorkflowCatalogCompositionTrustsOnlyVerifiedMiddlewareIdentity(t *testing.T) {
	client, fleet := newCatalogFleetClient(t)
	module, err := NewWorkflowCatalogModule(testWorkflowCatalogConfig(WorkflowCatalogConfig{
		Enabled:       true,
		FleetDBClient: client,
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
		Enabled: true, Workspace: "TEST", ExternalAuth: true,
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
	case r.Method == http.MethodPost &&
		(r.URL.Path == "/api/v1/TEST/drivers/demo/versions/author" ||
			r.URL.Path == "/api/v1/TEST/drivers/epic-runner/versions/author-managed"):
		s.serveAuthorVersion(w, r)
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

func (s *catalogFleetStub) serveAuthorVersion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID        string            `json:"request_id"`
		ExpectedRevision uint64            `json:"expected_revision"`
		DriverName       string            `json:"driver_name"`
		VersionID        string            `json:"version_id"`
		SourceRef        string            `json:"source_ref"`
		SourceDigest     string            `json:"source_digest"`
		BundleRef        string            `json:"bundle_ref"`
		BundleDigest     string            `json:"bundle_digest"`
		Runtime          string            `json:"runtime"`
		Manifest         map[string]string `json:"manifest"`
		BuildDiagnostics string            `json:"build_diagnostics"`
		Activate         bool              `json:"activate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Errorf("decode author-version request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	managed := strings.HasSuffix(r.URL.Path, "/author-managed")
	if body.RequestID == "" || body.VersionID == "" || body.DriverName == "" ||
		body.Activate != managed {
		s.t.Errorf("author-version body = %+v, managed=%v", body, managed)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	actor := r.Header.Get(infrafleetdb.FleetDelegatedActorHeader)
	if actor == "" {
		s.t.Error("author-version request missing delegated actor")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	driverID := "demo"
	trust := workflowcatalog.DriverTrustUntrusted
	ownerType := workflowcatalog.DriverOwnerUser
	status := workflowcatalog.DriverStatusDraft
	activeVersionID := ""
	if managed {
		driverID = workflowcatalog.BuiltinEpicRunnerWorkflowName
		trust = workflowcatalog.DriverTrustTrusted
		ownerType = workflowcatalog.DriverOwnerSystem
		status = workflowcatalog.DriverStatusActive
		activeVersionID = body.VersionID
	}
	manifest := cloneWorkflowCatalogBridgeMap(body.Manifest)
	manifest[workflowcatalog.ManifestTrustLevelKey] = string(trust)
	writeCatalogFleetJSON(w, infrafleetdb.WorkflowCatalogAuthorVersionResult{
		Driver: &workflowcatalog.Driver{
			WorkspaceKey: "TEST", DriverID: driverID, Name: body.DriverName,
			OwnerType: ownerType, Status: status, ActiveVersionID: activeVersionID,
			TrustLevel: trust, Revision: body.ExpectedRevision + 1,
		},
		Version: &workflowcatalog.DriverVersion{
			WorkspaceKey: "TEST", DriverID: driverID, VersionID: body.VersionID,
			Version: 1, SourceRef: body.SourceRef, SourceDigest: body.SourceDigest,
			BundleRef: body.BundleRef, BundleDigest: body.BundleDigest,
			Runtime: body.Runtime, Manifest: manifest, BuildDiagnostics: body.BuildDiagnostics,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed, CreatedBy: actor,
		},
		CreatedDriver: true, CreatedVersion: true, Activated: body.Activate,
		CommittedRevision: body.ExpectedRevision + 1,
		SemanticImpact:    workflowcatalog.SemanticImpactVersionAuthored,
	})
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
