package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	testAuthorityHeader = "X-Test-Authority"
	testWorkspaceHeader = "X-Test-Workspace"
)

type commandCall struct {
	action  authority.Action
	command workflowcatalog.VersionCommand
	subject string
}

type catalogFake struct {
	driver   *workflowcatalog.Driver
	versions map[string]*workflowcatalog.DriverVersion
	order    []string
	calls    []string
	commands []commandCall
}

func newCatalogFake() *catalogFake {
	return &catalogFake{
		driver: &workflowcatalog.Driver{
			WorkspaceKey: "TEST", DriverID: "driver-1", Name: "demo",
			ActiveVersionID: "v1", Status: workflowcatalog.DriverStatusActive,
			TrustLevel: workflowcatalog.DriverTrustUntrusted, Revision: 7,
			Metadata: map[string]string{
				workflowcatalog.ApprovedVersionMetadataKey("v1"): "digest-v1",
				"unrelated": "keep",
			},
		},
		versions: map[string]*workflowcatalog.DriverVersion{
			"v1": {
				WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v1", Version: 1,
				SourceDigest: "digest-v1", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
				Manifest: map[string]string{workflowcatalog.ManifestTrustLevelKey: string(workflowcatalog.DriverTrustUntrusted)},
			},
			"v2": {
				WorkspaceKey: "TEST", DriverID: "driver-1", VersionID: "v2", Version: 2,
				SourceDigest: "digest-v2", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
				Manifest: map[string]string{workflowcatalog.ManifestTrustLevelKey: string(workflowcatalog.DriverTrustUntrusted)},
			},
		},
		order: []string{"v2", "v1"},
	}
}

func (f *catalogFake) GetDriver(_ context.Context, workspace, driverRef string) (*workflowcatalog.Driver, error) {
	f.calls = append(f.calls, "get-driver")
	if workspace != f.driver.WorkspaceKey {
		return nil, workflowcatalog.ErrWrongWorkspace
	}
	if driverRef != f.driver.DriverID && driverRef != f.driver.Name {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneTestDriver(f.driver), nil
}

func (f *catalogFake) ListDrivers(_ context.Context, workspace string) ([]*workflowcatalog.Driver, error) {
	f.calls = append(f.calls, "list-drivers")
	if workspace != f.driver.WorkspaceKey {
		return nil, workflowcatalog.ErrWrongWorkspace
	}
	return []*workflowcatalog.Driver{cloneTestDriver(f.driver)}, nil
}

func (f *catalogFake) GetVersion(_ context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	f.calls = append(f.calls, "get-version")
	version := f.versions[versionID]
	if version == nil {
		return nil, workflowcatalog.ErrNotFound
	}
	if workspace != version.WorkspaceKey {
		return nil, workflowcatalog.ErrWrongWorkspace
	}
	return cloneTestVersion(version), nil
}

func (f *catalogFake) ListVersions(_ context.Context, workspace, driverRef string) (*workflowcatalog.VersionSet, error) {
	f.calls = append(f.calls, "list-versions")
	if workspace != f.driver.WorkspaceKey {
		return nil, workflowcatalog.ErrWrongWorkspace
	}
	if driverRef != f.driver.DriverID && driverRef != f.driver.Name {
		return nil, workflowcatalog.ErrNotFound
	}
	versions := make([]*workflowcatalog.DriverVersion, 0, len(f.order))
	for _, id := range f.order {
		versions = append(versions, cloneTestVersion(f.versions[id]))
	}
	return &workflowcatalog.VersionSet{Driver: cloneTestDriver(f.driver), Versions: versions}, nil
}

func (f *catalogFake) ResolveEffectiveVersion(context.Context, authority.SystemAuthority, string, string) (*workflowcatalog.EffectiveVersion, error) {
	return nil, errors.New("unexpected ResolveEffectiveVersion call")
}

func (f *catalogFake) ResolveRequestedVersion(context.Context, authority.OperatorAuthority, string, string, string) (*workflowcatalog.RequestedVersion, error) {
	return nil, errors.New("unexpected ResolveRequestedVersion call")
}

func (f *catalogFake) ApproveVersion(_ context.Context, auth authority.OperatorAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	return f.apply(workflowcatalog.ActionApproveVersion, auth, command)
}

func (f *catalogFake) UnapproveVersion(_ context.Context, auth authority.OperatorAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	return f.apply(workflowcatalog.ActionUnapproveVersion, auth, command)
}

func (f *catalogFake) ActivateVersion(_ context.Context, auth authority.OperatorAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	return f.apply(workflowcatalog.ActionActivateVersion, auth, command)
}

func (f *catalogFake) apply(action authority.Action, auth authority.OperatorAuthority, command workflowcatalog.VersionCommand) (*workflowcatalog.VersionResult, error) {
	f.calls = append(f.calls, string(action))
	f.commands = append(f.commands, commandCall{action: action, command: command, subject: auth.Subject()})
	if command.WorkspaceKey != f.driver.WorkspaceKey || auth.Workspace() != command.WorkspaceKey {
		return nil, workflowcatalog.ErrWrongWorkspace
	}
	if auth.Action() != action {
		return nil, &authority.AdmissionError{Reason: authority.DenialWrongAction, Action: action, Workspace: command.WorkspaceKey, Class: authority.ClassOperator}
	}
	if command.DriverID != f.driver.DriverID {
		return nil, workflowcatalog.ErrNotFound
	}
	version := f.versions[command.VersionID]
	if version == nil {
		return nil, workflowcatalog.ErrNotFound
	}
	if command.ExpectedRevision != f.driver.Revision {
		return nil, workflowcatalog.ErrStaleRevision
	}
	switch action {
	case workflowcatalog.ActionApproveVersion:
		f.driver.Metadata[workflowcatalog.ApprovedVersionMetadataKey(version.VersionID)] = version.SourceDigest
	case workflowcatalog.ActionUnapproveVersion:
		delete(f.driver.Metadata, workflowcatalog.ApprovedVersionMetadataKey(version.VersionID))
	case workflowcatalog.ActionActivateVersion:
		if !workflowcatalog.VersionApproved(f.driver, version) {
			return nil, workflowcatalog.ErrVersionNotApproved
		}
		f.driver.ActiveVersionID = version.VersionID
		f.driver.Status = workflowcatalog.DriverStatusActive
	}
	f.driver.Revision++
	impact := workflowcatalog.SemanticImpactVersionTrustChanged
	if action == workflowcatalog.ActionActivateVersion {
		impact = workflowcatalog.SemanticImpactEffectiveVersionChanged
	}
	return &workflowcatalog.VersionResult{
		Action: action, Driver: cloneTestDriver(f.driver), Version: cloneTestVersion(version),
		Active:            f.driver.ActiveVersionID == version.VersionID,
		Approved:          workflowcatalog.VersionApproved(f.driver, version),
		EffectiveTrust:    workflowcatalog.EffectiveTrust(f.driver, version),
		CommittedRevision: f.driver.Revision, SemanticImpact: impact,
	}, nil
}

type resolverCall struct {
	workspace string
	action    authority.Action
}

type resolverFake struct {
	issuer *authority.Issuer
	now    time.Time
	calls  []resolverCall
}

func newResolverFake(t *testing.T) *resolverFake {
	t.Helper()
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewIssuerWithClock: %v", err)
	}
	return &resolverFake{issuer: issuer, now: now}
}

func (f *resolverFake) ResolveOperatorAuthority(r *http.Request, workspace string, action authority.Action) (authority.OperatorAuthority, error) {
	f.calls = append(f.calls, resolverCall{workspace: workspace, action: action})
	class := authority.Class(strings.TrimSpace(r.Header.Get(testAuthorityHeader)))
	if class == "" {
		return authority.OperatorAuthority{}, ErrUnauthenticated
	}
	verifiedWorkspace := strings.TrimSpace(r.Header.Get(testWorkspaceHeader))
	if verifiedWorkspace == "" {
		verifiedWorkspace = workspace
	}
	principal, err := f.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "verified-user", Class: class, Workspace: verifiedWorkspace,
		Actions: []authority.Action{action}, ExpiresAt: f.now.Add(time.Hour),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return f.issuer.IssueOperator(principal, workspace, action)
}

func newHandler(t *testing.T) (*http.ServeMux, *catalogFake, *resolverFake) {
	t.Helper()
	catalog := newCatalogFake()
	resolver := newResolverFake(t)
	mux := http.NewServeMux()
	New(catalog, resolver, func(context.Context) string { return "TEST" }, func(string) bool { return false }).Register(mux)
	return mux, catalog, resolver
}

func TestRoutesUseCanonicalWorkspaceContextInsteadOfRouteAlias(t *testing.T) {
	mux, catalog, resolver := newHandler(t)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/display-name/workflows/demo/versions/v2/approve", `{}`, string(authority.ClassOperator), "TEST")
	if response.Code != http.StatusOK {
		t.Fatalf("alias route status=%d body=%s", response.Code, response.Body.String())
	}
	if len(resolver.calls) != 1 || resolver.calls[0].workspace != "TEST" {
		t.Fatalf("authority workspace = %+v, want canonical TEST", resolver.calls)
	}
	if len(catalog.commands) != 1 || catalog.commands[0].command.WorkspaceKey != "TEST" {
		t.Fatalf("catalog command = %+v, want canonical TEST", catalog.commands)
	}
}

func TestRoutesRejectMissingCanonicalWorkspaceContext(t *testing.T) {
	catalog := newCatalogFake()
	resolver := newResolverFake(t)
	mux := http.NewServeMux()
	New(catalog, resolver, func(context.Context) string { return "" }, func(string) bool { return false }).Register(mux)
	response := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflow-catalog/drivers", "", "", "")
	assertErrorResponse(t, response, http.StatusBadRequest, errorCodeInvalidRequest)
	if len(catalog.calls) != 0 || len(resolver.calls) != 0 {
		t.Fatalf("missing canonical context reached dependencies: catalog=%v resolver=%v", catalog.calls, resolver.calls)
	}
}

func TestListDriversUsesAuthoritativeBuiltInClassifier(t *testing.T) {
	catalog := newCatalogFake()
	resolver := newResolverFake(t)
	var classified []string
	mux := http.NewServeMux()
	New(catalog, resolver, func(context.Context) string { return "TEST" }, func(driverID string) bool {
		classified = append(classified, driverID)
		return driverID == "driver-1"
	}).Register(mux)

	response := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflow-catalog/drivers", "", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list drivers status=%d body=%s", response.Code, response.Body.String())
	}
	var list workflowListResponse
	decodeResponse(t, response, &list)
	if len(list.Workflows) != 1 || !list.Workflows[0].BuiltIn {
		t.Fatalf("driver list = %+v, want authoritative built_in=true", list)
	}
	if len(classified) != 1 || classified[0] != "driver-1" {
		t.Fatalf("classified driver IDs = %v, want exact driver-1", classified)
	}
}

func TestListDriversFailsClosedWithoutBuiltInClassifier(t *testing.T) {
	catalog := newCatalogFake()
	mux := http.NewServeMux()
	New(catalog, newResolverFake(t), func(context.Context) string { return "TEST" }, nil).Register(mux)

	response := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflow-catalog/drivers", "", "", "")
	assertErrorResponse(t, response, http.StatusServiceUnavailable, errorCodeUnavailable)
	if len(catalog.calls) != 0 {
		t.Fatalf("missing classifier reached catalog: %v", catalog.calls)
	}
}

func TestReadRoutesArePureAndPreserveCompatibilityShapes(t *testing.T) {
	mux, catalog, resolver := newHandler(t)

	drivers := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflow-catalog/drivers", "", "", "")
	if drivers.Code != http.StatusOK {
		t.Fatalf("list drivers status=%d body=%s", drivers.Code, drivers.Body.String())
	}
	var list workflowListResponse
	decodeResponse(t, drivers, &list)
	if len(list.Workflows) != 1 || list.Workflows[0].DriverID != "driver-1" || list.Workflows[0].Name != "demo" || list.Workflows[0].Revision != 7 {
		t.Fatalf("driver list = %+v", list)
	}
	if list.Workflows[0].BuiltIn {
		t.Fatalf("catalog route claimed builtin ownership: %+v", list.Workflows[0])
	}

	catalog.calls = nil
	versions := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflows/demo/versions", "", "", "")
	if versions.Code != http.StatusOK {
		t.Fatalf("list versions status=%d body=%s", versions.Code, versions.Body.String())
	}
	var versionList workflowVersionsResponse
	decodeResponse(t, versions, &versionList)
	if versionList.Driver == nil || versionList.Driver.DriverID != "driver-1" || versionList.Driver.Revision != 7 || versionList.DriverID != "driver-1" || versionList.ActiveVersionID != "v1" || versionList.Revision != 7 || len(versionList.Versions) != 2 {
		t.Fatalf("version list = %+v", versionList)
	}
	if len(resolver.calls) != 0 || len(catalog.commands) != 0 {
		t.Fatalf("read routes resolved authority or mutated: resolver=%+v commands=%+v", resolver.calls, catalog.commands)
	}
	if len(catalog.calls) != 1 || catalog.calls[0] != "list-versions" {
		t.Fatalf("version read calls = %v", catalog.calls)
	}

	legacyStatic := serveRequest(mux, http.MethodGet, "/api/workspaces/TEST/workflows", "", "", "")
	if legacyStatic.Code != http.StatusNotFound {
		t.Fatalf("httpapi claimed legacy static workflow route: status=%d", legacyStatic.Code)
	}
}

func TestApproveThenActivateJourneyUsesCurrentRevisionFallback(t *testing.T) {
	mux, catalog, resolver := newHandler(t)

	approved := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{}`, string(authority.ClassOperator), "TEST")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	var approveResult versionLifecycleResponse
	decodeResponse(t, approved, &approveResult)
	if approveResult.Action != "approve" || approveResult.Driver == nil || approveResult.Driver.Revision != 8 || approveResult.Version == nil || approveResult.Version.VersionID != "v2" {
		t.Fatalf("approve response = %+v", approveResult)
	}
	if !workflowcatalog.VersionApproved(catalog.driver, catalog.versions["v2"]) {
		t.Fatal("approve route did not approve v2")
	}

	activated := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/activate", `{}`, string(authority.ClassOperator), "TEST")
	if activated.Code != http.StatusOK {
		t.Fatalf("activate status=%d body=%s", activated.Code, activated.Body.String())
	}
	var activateResult versionLifecycleResponse
	decodeResponse(t, activated, &activateResult)
	if activateResult.Action != "activate" || activateResult.Driver == nil || activateResult.Driver.Revision != 9 || activateResult.Driver.ActiveVersionID != "v2" {
		t.Fatalf("activate response = %+v", activateResult)
	}
	if len(catalog.commands) != 2 || catalog.commands[0].command.ExpectedRevision != 7 || catalog.commands[1].command.ExpectedRevision != 8 {
		t.Fatalf("fallback revisions = %+v", catalog.commands)
	}
	if catalog.commands[0].command.DriverID != "driver-1" || catalog.commands[0].subject != "verified-user" {
		t.Fatalf("derived command = %+v", catalog.commands[0])
	}
	if len(resolver.calls) != 2 || resolver.calls[0].action != workflowcatalog.ActionApproveVersion || resolver.calls[1].action != workflowcatalog.ActionActivateVersion {
		t.Fatalf("resolver calls = %+v", resolver.calls)
	}
}

func TestUnapproveKeepsActiveVersionAndUsesLegacyResponse(t *testing.T) {
	mux, catalog, _ := newHandler(t)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v1/unapprove", "", string(authority.ClassOperator), "TEST")
	if response.Code != http.StatusOK {
		t.Fatalf("unapprove status=%d body=%s", response.Code, response.Body.String())
	}
	var result versionLifecycleResponse
	decodeResponse(t, response, &result)
	if result.Action != "unapprove" || result.Driver == nil || result.Version == nil || result.Driver.ActiveVersionID != "v1" || result.Driver.Revision != 8 {
		t.Fatalf("unapprove response = %+v", result)
	}
	if workflowcatalog.VersionApproved(catalog.driver, catalog.versions["v1"]) {
		t.Fatal("unapprove route left v1 approved")
	}
}

func TestStaleRevisionReturnsStableConflict(t *testing.T) {
	mux, catalog, _ := newHandler(t)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{"expected_revision":6}`, string(authority.ClassOperator), "TEST")
	assertErrorResponse(t, response, http.StatusConflict, errorCodeStaleRevision)
	if catalog.driver.Revision != 7 || workflowcatalog.VersionApproved(catalog.driver, catalog.versions["v2"]) {
		t.Fatalf("stale request mutated driver: %+v", catalog.driver)
	}
	if len(catalog.commands) != 1 || catalog.commands[0].command.ExpectedRevision != 6 {
		t.Fatalf("stale command = %+v", catalog.commands)
	}
}

func TestMutationRoutesRejectUnauthenticatedAndNonOperatorCallers(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		mux, catalog, _ := newHandler(t)
		response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{}`, "", "")
		assertErrorResponse(t, response, http.StatusUnauthorized, errorCodeUnauthenticated)
		if len(catalog.calls) != 0 {
			t.Fatalf("unauthenticated request reached catalog: %v", catalog.calls)
		}
	})

	for _, class := range []authority.Class{authority.ClassExecution, authority.ClassSession, authority.ClassWebhook} {
		t.Run(string(class), func(t *testing.T) {
			mux, catalog, _ := newHandler(t)
			response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{}`, string(class), "TEST")
			assertErrorResponse(t, response, http.StatusForbidden, errorCodeForbidden)
			if len(catalog.calls) != 0 {
				t.Fatalf("%s request reached catalog: %v", class, catalog.calls)
			}
		})
	}
}

func TestMutationRouteRejectsWrongWorkspaceAuthority(t *testing.T) {
	mux, catalog, _ := newHandler(t)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{}`, string(authority.ClassOperator), "OTHER")
	assertErrorResponse(t, response, http.StatusForbidden, errorCodeForbidden)
	if len(catalog.calls) != 0 {
		t.Fatalf("wrong-workspace request reached catalog: %v", catalog.calls)
	}
}

func TestMutationRequestValidationIsStable(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "zero revision", body: `{"expected_revision":0}`},
		{name: "revision cannot advance", body: `{"expected_revision":9223372036854775807}`},
		{name: "unknown field", body: `{"actor":"forged"}`},
		{name: "trailing JSON", body: `{} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			mux, catalog, _ := newHandler(t)
			response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", test.body, string(authority.ClassOperator), "TEST")
			assertErrorResponse(t, response, http.StatusBadRequest, errorCodeInvalidRequest)
			if len(catalog.calls) != 0 {
				t.Fatalf("invalid request reached catalog: %v", catalog.calls)
			}
		})
	}
}

func TestMutationRequestRejectsSaturatedPersistedRevisionBeforeMutation(t *testing.T) {
	mux, catalog, _ := newHandler(t)
	catalog.driver.Revision = uint64(math.MaxInt64)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/v2/approve", `{}`, string(authority.ClassOperator), "TEST")
	assertErrorResponse(t, response, http.StatusBadRequest, errorCodeInvalidRequest)
	if len(catalog.calls) != 1 || catalog.calls[0] != "get-driver" {
		t.Fatalf("saturated revision calls = %v, want only required driver read", catalog.calls)
	}
	if len(catalog.commands) != 0 {
		t.Fatalf("saturated revision reached lifecycle mutation: %+v", catalog.commands)
	}
}

func TestMutationRouteRejectsNoncanonicalVersionID(t *testing.T) {
	mux, catalog, _ := newHandler(t)
	response := serveRequest(mux, http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions/%20v2%20/approve", `{}`, string(authority.ClassOperator), "TEST")
	assertErrorResponse(t, response, http.StatusBadRequest, errorCodeInvalidRequest)
	if len(catalog.calls) != 0 {
		t.Fatalf("noncanonical version id reached catalog: %v", catalog.calls)
	}
}

func serveRequest(handler http.Handler, method, path, body, class, workspace string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if class != "" {
		request.Header.Set(testAuthorityHeader, class)
	}
	if workspace != "" {
		request.Header.Set(testWorkspaceHeader, workspace)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, value any) {
	t.Helper()
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if err := json.Unmarshal(response.Body.Bytes(), value); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d, want=%d body=%s", response.Code, status, response.Body.String())
	}
	var payload errorResponse
	decodeResponse(t, response, &payload)
	if payload.Code != code || strings.TrimSpace(payload.Error) == "" {
		t.Fatalf("error response = %+v, want code=%q", payload, code)
	}
}

func cloneTestDriver(in *workflowcatalog.Driver) *workflowcatalog.Driver {
	if in == nil {
		return nil
	}
	out := *in
	if in.Metadata != nil {
		out.Metadata = make(map[string]string, len(in.Metadata))
		for key, value := range in.Metadata {
			out.Metadata[key] = value
		}
	}
	return &out
}

func cloneTestVersion(in *workflowcatalog.DriverVersion) *workflowcatalog.DriverVersion {
	if in == nil {
		return nil
	}
	out := *in
	if in.Manifest != nil {
		out.Manifest = make(map[string]string, len(in.Manifest))
		for key, value := range in.Manifest {
			out.Manifest[key] = value
		}
	}
	return &out
}
