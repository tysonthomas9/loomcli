package workflows

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	workflowcataloghttp "github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog/httpapi"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type nativeHandlerCatalog struct {
	workflowcatalog.API
	workflowcatalog.VersionAuthoringAPI

	driver  *workflowcatalog.Driver
	version *workflowcatalog.DriverVersion

	authorCommands   []workflowcatalog.AuthorVersionCommand
	approveCommands  []workflowcatalog.VersionCommand
	activateCommands []workflowcatalog.VersionCommand
	actors           []string
}

func (catalog *nativeHandlerCatalog) GetDriver(
	_ context.Context,
	workspace, driverRef string,
) (*workflowcatalog.Driver, error) {
	if catalog.driver == nil {
		return nil, workflowcatalog.ErrNotFound
	}
	if workspace != catalog.driver.WorkspaceKey ||
		(driverRef != catalog.driver.DriverID && driverRef != catalog.driver.Name) {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneNativeHandlerDriver(catalog.driver), nil
}

func (catalog *nativeHandlerCatalog) GetVersion(
	_ context.Context,
	workspace, versionID string,
) (*workflowcatalog.DriverVersion, error) {
	if catalog.version == nil ||
		workspace != catalog.version.WorkspaceKey ||
		versionID != catalog.version.VersionID {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneNativeHandlerVersion(catalog.version), nil
}

func (catalog *nativeHandlerCatalog) AuthorVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.AuthorVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	catalog.authorCommands = append(catalog.authorCommands, command)
	catalog.actors = append(catalog.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionAuthorVersion ||
		auth.Workspace() != command.WorkspaceKey {
		return nil, authority.ErrAdmissionDenied
	}
	catalog.driver = &workflowcatalog.Driver{
		WorkspaceKey: command.WorkspaceKey,
		DriverID:     command.DriverID,
		Name:         command.DriverName,
		Status:       workflowcatalog.DriverStatusDraft,
		TrustLevel:   workflowcatalog.DriverTrustUntrusted,
		Metadata:     map[string]string{},
		Revision:     1,
	}
	catalog.version = &workflowcatalog.DriverVersion{
		WorkspaceKey:     command.WorkspaceKey,
		DriverID:         command.DriverID,
		VersionID:        command.VersionID,
		Version:          1,
		SourceRef:        command.SourceRef,
		SourceDigest:     command.SourceDigest,
		BundleRef:        command.BundleRef,
		BundleDigest:     command.BundleDigest,
		Runtime:          command.Runtime,
		Manifest:         cloneNativeHandlerMap(command.Manifest),
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		CreatedBy:        auth.Subject(),
	}
	catalog.version.Manifest[workflowcatalog.ManifestTrustLevelKey] = string(workflowcatalog.DriverTrustUntrusted)
	return &workflowcatalog.AuthorVersionResult{
		Action:            workflowcatalog.ActionAuthorVersion,
		Driver:            cloneNativeHandlerDriver(catalog.driver),
		Version:           cloneNativeHandlerVersion(catalog.version),
		CreatedDriver:     true,
		CreatedVersion:    true,
		CommittedRevision: 1,
		SemanticImpact:    workflowcatalog.SemanticImpactVersionAuthored,
	}, nil
}

func (*nativeHandlerCatalog) AuthorManagedVersion(
	context.Context,
	authority.SystemAuthority,
	workflowcatalog.AuthorManagedVersionCommand,
) (*workflowcatalog.AuthorVersionResult, error) {
	return nil, errors.New("unexpected managed authoring")
}

func (catalog *nativeHandlerCatalog) ApproveVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	catalog.approveCommands = append(catalog.approveCommands, command)
	catalog.actors = append(catalog.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionApproveVersion ||
		auth.Workspace() != command.WorkspaceKey ||
		command.ExpectedRevision != catalog.driver.Revision {
		return nil, authority.ErrAdmissionDenied
	}
	catalog.driver.Metadata[workflowcatalog.ApprovedVersionMetadataKey(catalog.version.VersionID)] = catalog.version.SourceDigest
	catalog.driver.Revision++
	return catalog.nativeHandlerVersionResult(workflowcatalog.ActionApproveVersion), nil
}

func (*nativeHandlerCatalog) UnapproveVersion(
	context.Context,
	authority.OperatorAuthority,
	workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	return nil, errors.New("unexpected unapprove")
}

func (catalog *nativeHandlerCatalog) ActivateVersion(
	_ context.Context,
	auth authority.OperatorAuthority,
	command workflowcatalog.VersionCommand,
) (*workflowcatalog.VersionResult, error) {
	catalog.activateCommands = append(catalog.activateCommands, command)
	catalog.actors = append(catalog.actors, auth.Subject())
	if auth.Action() != workflowcatalog.ActionActivateVersion ||
		auth.Workspace() != command.WorkspaceKey ||
		command.ExpectedRevision != catalog.driver.Revision {
		return nil, authority.ErrAdmissionDenied
	}
	if !workflowcatalog.VersionApproved(catalog.driver, catalog.version) {
		return nil, workflowcatalog.ErrVersionNotApproved
	}
	catalog.driver.ActiveVersionID = catalog.version.VersionID
	catalog.driver.Status = workflowcatalog.DriverStatusActive
	catalog.driver.Revision++
	return catalog.nativeHandlerVersionResult(workflowcatalog.ActionActivateVersion), nil
}

func (catalog *nativeHandlerCatalog) nativeHandlerVersionResult(action authority.Action) *workflowcatalog.VersionResult {
	return &workflowcatalog.VersionResult{
		Action:            action,
		Driver:            cloneNativeHandlerDriver(catalog.driver),
		Version:           cloneNativeHandlerVersion(catalog.version),
		Active:            catalog.driver.ActiveVersionID == catalog.version.VersionID,
		Approved:          workflowcatalog.VersionApproved(catalog.driver, catalog.version),
		EffectiveTrust:    workflowcatalog.EffectiveTrust(catalog.driver, catalog.version),
		CommittedRevision: catalog.driver.Revision,
	}
}

type nativeHandlerAuthorityResolver struct {
	issuer      *authority.Issuer
	now         time.Time
	allowOpen   bool
	denyAction  authority.Action
	calls       []authority.Action
	workspaces  []string
	credentials []string
}

func newNativeHandlerAuthorityResolver(t *testing.T, allowOpen bool) *nativeHandlerAuthorityResolver {
	t.Helper()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return &nativeHandlerAuthorityResolver{issuer: issuer, now: now, allowOpen: allowOpen}
}

func (resolver *nativeHandlerAuthorityResolver) ResolveOperatorAuthority(
	r *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	resolver.calls = append(resolver.calls, action)
	resolver.workspaces = append(resolver.workspaces, workspace)
	credential := strings.TrimSpace(r.Header.Get("Authorization"))
	resolver.credentials = append(resolver.credentials, credential)
	if resolver.denyAction == action {
		return authority.OperatorAuthority{}, authority.ErrActionNotAllowed
	}
	subject := ""
	switch {
	case credential == "Bearer oidc-token":
		subject = "oidc-user"
	case credential == "" && resolver.allowOpen:
		subject = "open-operator"
	default:
		return authority.OperatorAuthority{}, workflowcataloghttp.ErrUnauthenticated
	}
	principal, err := resolver.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: subject, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: resolver.now.Add(time.Hour),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return resolver.issuer.IssueOperator(principal, workspace, action)
}

func TestRegisterNativeDriverUsesCanonicalRequestAuthorityAndTypedLifecycle(t *testing.T) {
	runtimeRoot := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeRoot)
	catalog := &nativeHandlerCatalog{}
	resolver := newNativeHandlerAuthorityResolver(t, false)
	handler := nativeDriverHandler(catalog, resolver, "display-name", "CANONICAL")
	request := registerNativeDriverRequest{
		Archive:    nativeHandlerArchive(t, nativeArchiveEntry{name: "server.mjs", body: "export {};\n"}),
		DriverName: "Demo",
		DriverID:   "demo",
		Activate:   true,
		Trust:      domain.DriverTrustTrusted,
	}
	response := serveNativeDriverRequest(t, handler, request, "Bearer oidc-token")
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"root"`) ||
		strings.Contains(response.Body.String(), `"bundle"`) {
		t.Fatalf("server-local bundle coordinate leaked: %s", response.Body.String())
	}
	if len(catalog.authorCommands) != 1 ||
		catalog.authorCommands[0].WorkspaceKey != "CANONICAL" ||
		len(catalog.approveCommands) != 1 ||
		len(catalog.activateCommands) != 1 {
		t.Fatalf(
			"typed commands author=%+v approve=%+v activate=%+v",
			catalog.authorCommands, catalog.approveCommands, catalog.activateCommands,
		)
	}
	if got := strings.Join(catalog.actors, ","); got != "oidc-user,oidc-user,oidc-user" {
		t.Fatalf("audit actors = %q", got)
	}
	wantActions := []authority.Action{
		workflowcatalog.ActionAuthorVersion,
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionActivateVersion,
	}
	if len(resolver.calls) != len(wantActions) {
		t.Fatalf("authority calls = %v", resolver.calls)
	}
	for index, action := range wantActions {
		if resolver.calls[index] != action ||
			resolver.workspaces[index] != "CANONICAL" ||
			resolver.credentials[index] != "Bearer oidc-token" {
			t.Fatalf(
				"authority call %d = action %q workspace %q credential %q",
				index, resolver.calls[index], resolver.workspaces[index], resolver.credentials[index],
			)
		}
	}
	if catalog.driver.Status != workflowcatalog.DriverStatusActive ||
		catalog.driver.ActiveVersionID != catalog.version.VersionID ||
		!workflowcatalog.VersionApproved(catalog.driver, catalog.version) {
		t.Fatalf("durable lifecycle state driver=%+v version=%+v", catalog.driver, catalog.version)
	}
	if _, err := os.Stat(filepath.Join(runtimeRoot, filepath.FromSlash(catalog.version.BundleRef), "dist", "server.mjs")); err != nil {
		t.Fatalf("durable native bundle: %v", err)
	}
}

func TestRegisterNativeDriverOpenModeDerivesServerOwnedActor(t *testing.T) {
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	catalog := &nativeHandlerCatalog{}
	resolver := newNativeHandlerAuthorityResolver(t, true)
	handler := nativeDriverHandler(catalog, resolver, "TEST", "TEST")
	response := serveNativeDriverRequest(t, handler, registerNativeDriverRequest{
		Archive:    nativeHandlerArchive(t, nativeArchiveEntry{name: "server.mjs", body: "export {};\n"}),
		DriverName: "demo",
		Trust:      domain.DriverTrustUntrusted,
	}, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if len(catalog.actors) != 1 || catalog.actors[0] != "open-operator" ||
		len(resolver.calls) != 1 || resolver.calls[0] != workflowcatalog.ActionAuthorVersion {
		t.Fatalf("open-mode actor=%v actions=%v", catalog.actors, resolver.calls)
	}
}

func TestRegisterNativeDriverResolvesEveryRequiredAuthorityBeforeExtraction(t *testing.T) {
	for _, denied := range []authority.Action{
		workflowcatalog.ActionAuthorVersion,
		workflowcatalog.ActionApproveVersion,
		workflowcatalog.ActionActivateVersion,
	} {
		t.Run(string(denied), func(t *testing.T) {
			runtimeRoot := t.TempDir()
			t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeRoot)
			catalog := &nativeHandlerCatalog{}
			resolver := newNativeHandlerAuthorityResolver(t, false)
			resolver.denyAction = denied
			handler := nativeDriverHandler(catalog, resolver, "TEST", "TEST")
			// If extraction happened before all required actions were admitted,
			// this deliberately invalid gzip payload would produce 400.
			response := serveNativeDriverRequest(t, handler, registerNativeDriverRequest{
				Archive:  []byte("not gzip"),
				Activate: true,
				Trust:    domain.DriverTrustTrusted,
			}, "Bearer oidc-token")
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d body=%s, want authorization denial before extraction", response.Code, response.Body.String())
			}
			if len(catalog.authorCommands) != 0 {
				t.Fatalf("forbidden request authored %d versions", len(catalog.authorCommands))
			}
			if _, err := os.Stat(filepath.Join(runtimeRoot, ".loom")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("forbidden request promoted bundle content: stat err=%v", err)
			}
		})
	}
}

func TestRegisterNativeDriverRejectsUnknownFieldsAndTrustPolicyBeforeAuthoring(t *testing.T) {
	validArchive := nativeHandlerArchive(t, nativeArchiveEntry{name: "server.mjs", body: "export {};\n"})
	tests := []struct {
		name string
		body any
	}{
		{
			name: "unknown field",
			body: map[string]any{
				"archive":    validArchive,
				"trust":      domain.DriverTrustUntrusted,
				"created_by": "forged",
			},
		},
		{
			name: "unknown trust",
			body: registerNativeDriverRequest{Archive: validArchive, Trust: domain.DriverTrustLevel("root")},
		},
		{
			name: "untrusted activate",
			body: registerNativeDriverRequest{
				Archive: validArchive, Trust: domain.DriverTrustUntrusted, Activate: true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := &nativeHandlerCatalog{}
			resolver := newNativeHandlerAuthorityResolver(t, true)
			handler := nativeDriverHandler(catalog, resolver, "TEST", "TEST")
			response := serveNativeDriverRequest(t, handler, test.body, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if len(resolver.calls) != 0 || len(catalog.authorCommands) != 0 {
				t.Fatalf("invalid request reached authority/catalog: actions=%v author=%d", resolver.calls, len(catalog.authorCommands))
			}
		})
	}
}

func TestExtractNativeDriverArchiveRejectsUnsafeAndAmbiguousEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries []nativeArchiveEntry
	}{
		{
			name: "duplicate",
			entries: []nativeArchiveEntry{
				{name: "server.mjs", body: "one"},
				{name: "server.mjs", body: "two"},
			},
		},
		{name: "traversal", entries: []nativeArchiveEntry{{name: "../escape", body: "bad"}}},
		{name: "symlink", entries: []nativeArchiveEntry{{name: "link", typeflag: tar.TypeSymlink, linkname: "../escape"}}},
		{name: "hardlink", entries: []nativeArchiveEntry{{name: "link", typeflag: tar.TypeLink, linkname: "server.mjs"}}},
		{name: "sparse", entries: []nativeArchiveEntry{{name: "sparse", typeflag: tar.TypeGNUSparse}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := nativeHandlerArchive(t, test.entries...)
			if err := extractNativeDriverArchive(data, filepath.Join(t.TempDir(), "dist")); err == nil {
				t.Fatalf("%s archive was accepted", test.name)
			}
		})
	}
}

func TestExtractNativeDriverArchiveRejectsAdvertisedOversizedFileBeforeCopy(t *testing.T) {
	data := nativeHandlerDeclaredSizeArchive(t, "server.mjs", (64<<20)+1)
	err := extractNativeDriverArchive(data, filepath.Join(t.TempDir(), "dist"))
	if err == nil || !strings.Contains(err.Error(), "extraction limits") {
		t.Fatalf("oversized extraction error = %v", err)
	}
}

func nativeDriverHandler(
	catalog *nativeHandlerCatalog,
	resolver workflowcataloghttp.OperatorAuthorityResolver,
	requested, canonical string,
) http.Handler {
	mux := http.NewServeMux()
	NewModule(Config{
		Catalog: catalog, Authoring: catalog, CatalogOperatorAuthority: resolver,
	}).Register(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ref := middleware.WorkspaceRef{RequestedID: requested, CanonicalID: canonical}
		mux.ServeHTTP(w, r.WithContext(middleware.WithWorkspaceRef(r.Context(), ref)))
	})
}

func serveNativeDriverRequest(
	t *testing.T,
	handler http.Handler,
	body any,
	authorization string,
) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/display-name/workflow-catalog/native-drivers",
		bytes.NewReader(data),
	)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type nativeArchiveEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func nativeHandlerArchive(t *testing.T, entries ...nativeArchiveEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name: entry.name, Typeflag: typeflag, Linkname: entry.linkname,
			Mode: 0o644, Size: int64(len(entry.body)),
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatalf("write archive header: %v", err)
		}
		if len(entry.body) > 0 {
			if _, err := io.WriteString(archive, entry.body); err != nil {
				t.Fatalf("write archive body: %v", err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return output.Bytes()
}

func nativeHandlerDeclaredSizeArchive(t *testing.T, name string, size int64) []byte {
	t.Helper()
	var output bytes.Buffer
	compressed := gzip.NewWriter(&output)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{
		Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: size,
	}); err != nil {
		t.Fatal(err)
	}
	// Deliberately omit the advertised body. The extractor must account the
	// header and reject the limit before attempting to read it.
	_ = archive.Close()
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func cloneNativeHandlerDriver(input *workflowcatalog.Driver) *workflowcatalog.Driver {
	if input == nil {
		return nil
	}
	output := *input
	output.Metadata = cloneNativeHandlerMap(input.Metadata)
	return &output
}

func cloneNativeHandlerVersion(input *workflowcatalog.DriverVersion) *workflowcatalog.DriverVersion {
	if input == nil {
		return nil
	}
	output := *input
	output.Manifest = cloneNativeHandlerMap(input.Manifest)
	return &output
}

func cloneNativeHandlerMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
