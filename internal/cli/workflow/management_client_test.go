package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	workflowManagementTestWorkspace = "TEST"
)

type workflowManagementFixture struct {
	server                 *httptest.Server
	expectedBearer         string
	mu                     sync.Mutex
	driver                 *workflowcatalog.Driver
	versions               map[string]*workflowcatalog.DriverVersion
	authorizations         []string
	managementPaths        []string
	configRequests         int
	listBareDriver         bool
	rejectedClass          authority.Class
	authoringRequest       *workflowAuthorVersionRequest
	authoringRequestID     string
	authoringFailureStatus int
}

type workflowManagementRunRequest struct {
	CLICommand      string          `json:"cli_command"`
	DriverRef       string          `json:"driver_ref"`
	DriverVersionID string          `json:"driver_version_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	Entrypoint      string          `json:"entrypoint,omitempty"`
	EpicID          string          `json:"epic_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

func setupWorkflowManagementFixture(t *testing.T) *workflowManagementFixture {
	t.Helper()
	fixture := &workflowManagementFixture{
		expectedBearer: "",
		driver: &workflowcatalog.Driver{
			WorkspaceKey:    workflowManagementTestWorkspace,
			DriverID:        workflowcatalog.BuiltinEpicRunnerWorkflowName,
			Name:            workflowcatalog.BuiltinEpicRunnerWorkflowName,
			Status:          workflowcatalog.DriverStatusActive,
			ActiveVersionID: "version-1",
			TrustLevel:      workflowcatalog.DriverTrustTrusted,
			Revision:        1,
			Metadata: map[string]string{
				"approved_version:version-2": "sha256:source-2",
			},
		},
		versions: map[string]*workflowcatalog.DriverVersion{
			"version-1": {
				WorkspaceKey:     workflowManagementTestWorkspace,
				DriverID:         workflowcatalog.BuiltinEpicRunnerWorkflowName,
				VersionID:        "version-1",
				Version:          1,
				SourceDigest:     "sha256:source-1",
				BundleDigest:     "sha256:bundle-1",
				ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
				Manifest:         map[string]string{"trust_level": string(workflowcatalog.DriverTrustUntrusted)},
			},
			"version-2": {
				WorkspaceKey:     workflowManagementTestWorkspace,
				DriverID:         workflowcatalog.BuiltinEpicRunnerWorkflowName,
				VersionID:        "version-2",
				Version:          2,
				SourceDigest:     "sha256:source-2",
				BundleDigest:     "sha256:bundle-2",
				ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
				Manifest:         map[string]string{"trust_level": string(workflowcatalog.DriverTrustUntrusted)},
			},
		},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	configureWorkflowManagementClient(t, fixture.server.URL, workflowManagementTestWorkspace)
	return fixture
}

func configureWorkflowManagementClient(t *testing.T, serverURL, workspace string) {
	t.Helper()
	t.Setenv("LOOM_SERVER_URL", serverURL)
	t.Setenv("LOOM_WORKSPACE", workspace)
}

func (f *workflowManagementFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/config" {
		f.mu.Lock()
		f.configRequests++
		f.mu.Unlock()
		writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]string{"mode": "open"})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.authorizations = append(f.authorizations, r.Header.Get("Authorization"))
	f.managementPaths = append(f.managementPaths, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != f.expectedBearer {
		writeWorkflowManagementTestJSON(w, http.StatusUnauthorized, map[string]string{"error": "operator credential required", "code": "unauthenticated"})
		return
	}
	workspacePrefix := "/api/workspaces/" + workflowManagementTestWorkspace
	if !strings.HasPrefix(r.URL.Path, workspacePrefix+"/") {
		writeWorkflowManagementTestJSON(w, http.StatusForbidden, map[string]string{"error": "workspace authority mismatch", "code": "wrong_workspace"})
		return
	}
	if f.rejectedClass != "" {
		writeWorkflowManagementTestJSON(w, http.StatusForbidden, map[string]string{
			"error": string(f.rejectedClass) + " authority cannot perform Workflow Catalog operator actions",
			"code":  "wrong_authority_class",
		})
		return
	}

	switch {
	case r.Method == http.MethodPost &&
		strings.HasPrefix(r.URL.Path, workspacePrefix+"/workflows/") &&
		strings.HasSuffix(r.URL.Path, "/versions") &&
		strings.Count(strings.TrimPrefix(r.URL.Path, workspacePrefix+"/workflows/"), "/") == 1:
		if f.authoringFailureStatus != 0 {
			writeWorkflowManagementTestJSON(w, f.authoringFailureStatus, map[string]string{"error": "flue build failed"})
			return
		}
		var request workflowAuthorVersionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeWorkflowManagementTestJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, workspacePrefix+"/workflows/"), "/versions")
		f.authoringRequest = &request
		f.authoringRequestID = r.Header.Get("Idempotency-Key")
		digest := "sha256:" + strings.Repeat("1", 64)
		driverRecord := &workflowcatalog.Driver{
			WorkspaceKey: workflowManagementTestWorkspace, DriverID: name, Name: name,
			Status: workflowcatalog.DriverStatusDraft, TrustLevel: workflowcatalog.DriverTrustUntrusted, Revision: 1,
		}
		version := &workflowcatalog.DriverVersion{
			WorkspaceKey: workflowManagementTestWorkspace, DriverID: name, VersionID: name + "-v-test",
			Version: 1, SourceDigest: digest, BundleDigest: digest,
			ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		}
		writeWorkflowManagementTestJSON(w, http.StatusCreated, map[string]any{
			"driver": driverRecord, "version": version,
			"created_driver": true, "created_version": true, "build_diagnostics": "server build passed",
		})
		return
	case r.Method == http.MethodPost && r.URL.Path == workspacePrefix+"/execution/driver-runs":
		var request workflowManagementRunRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeWorkflowManagementTestJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		if request.DriverRef != f.driver.DriverID {
			writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "workflow not found"})
			return
		}
		versionID := strings.TrimSpace(request.DriverVersionID)
		if versionID == "" {
			versionID = f.driver.ActiveVersionID
		}
		if f.versions[versionID] == nil {
			writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "version not found"})
			return
		}
		runID := strings.TrimSpace(request.RunID)
		if runID == "" {
			runID = "run-management-1"
		}
		writeWorkflowManagementTestJSON(w, http.StatusAccepted, &domain.DriverRun{
			WorkspaceKey: workflowManagementTestWorkspace, RunID: runID,
			DriverID: f.driver.DriverID, DriverVersionID: versionID,
			Entrypoint: request.Entrypoint, SourceKind: "cli", SourceRef: "loom workflow run",
			EpicID: request.EpicID, IdempotencyKey: request.IdempotencyKey,
			Status: domain.DriverRunQueued, Payload: append(json.RawMessage(nil), request.Payload...),
		})
		return
	case r.Method == http.MethodGet && r.URL.Path == workspacePrefix+"/workflow-catalog/drivers":
		if f.listBareDriver {
			writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]any{"drivers": []*workflowcatalog.Driver{f.driver}})
			return
		}
		activeVersion := f.versions[f.driver.ActiveVersionID]
		approved := activeVersion != nil && f.driver.Metadata["approved_version:"+activeVersion.VersionID] == activeVersion.SourceDigest
		writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]any{"drivers": []any{map[string]any{
			"driver": f.driver, "version": activeVersion, "built_in": true,
			"approved": approved, "effective_trust": effectiveWorkflowManagementTestTrust(f.driver, activeVersion),
		}}})
		return
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/versions"):
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, workspacePrefix+"/workflows/"), "/versions")
		if name != workflowcatalog.BuiltinEpicRunnerWorkflowName {
			writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "workflow not found", "code": "not_found"})
			return
		}
		writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]any{
			"driver": f.driver, "driver_id": f.driver.DriverID, "active_version_id": f.driver.ActiveVersionID,
			"versions": []*workflowcatalog.DriverVersion{f.versions["version-1"], f.versions["version-2"]},
		})
		return
	case r.Method == http.MethodPost:
		f.applyVersionAction(w, r, workspacePrefix)
		return
	default:
		writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "route not found", "code": "not_found"})
	}
}

func (f *workflowManagementFixture) applyVersionAction(w http.ResponseWriter, r *http.Request, workspacePrefix string) {
	path := strings.TrimPrefix(r.URL.Path, workspacePrefix+"/workflows/"+workflowcatalog.BuiltinEpicRunnerWorkflowName+"/versions/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "route not found", "code": "not_found"})
		return
	}
	version := f.versions[parts[0]]
	if version == nil {
		writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "version not found", "code": "not_found"})
		return
	}
	var request struct {
		ExpectedRevision uint64 `json:"expected_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeWorkflowManagementTestJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request", "code": "invalid"})
		return
	}
	if request.ExpectedRevision != f.driver.Revision {
		writeWorkflowManagementTestJSON(w, http.StatusConflict, map[string]string{"error": "stale driver revision", "code": "revision_conflict"})
		return
	}
	action := parts[1]
	switch action {
	case "approve":
		f.driver.Metadata["approved_version:"+version.VersionID] = version.SourceDigest
	case "unapprove":
		delete(f.driver.Metadata, "approved_version:"+version.VersionID)
	case "activate":
		if f.driver.Metadata["approved_version:"+version.VersionID] != version.SourceDigest {
			writeWorkflowManagementTestJSON(w, http.StatusConflict, map[string]string{"error": "version is not approved", "code": "not_approved"})
			return
		}
		f.driver.ActiveVersionID = version.VersionID
	default:
		writeWorkflowManagementTestJSON(w, http.StatusNotFound, map[string]string{"error": "route not found", "code": "not_found"})
		return
	}
	f.driver.Revision++
	writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]any{"action": action, "driver": f.driver, "version": version})
}

func (f *workflowManagementFixture) lastAuthorization() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.authorizations) == 0 {
		return ""
	}
	return f.authorizations[len(f.authorizations)-1]
}

func (f *workflowManagementFixture) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.managementPaths...)
}

func (f *workflowManagementFixture) authDiscoveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.configRequests
}

func effectiveWorkflowManagementTestTrust(driver *workflowcatalog.Driver, version *workflowcatalog.DriverVersion) workflowcatalog.DriverTrustLevel {
	if version != nil && driver.Metadata["approved_version:"+version.VersionID] == version.SourceDigest {
		return workflowcatalog.DriverTrustTrusted
	}
	return workflowcatalog.DriverTrustUntrusted
}

func writeWorkflowManagementTestJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func TestWorkflowManagementRequiresExplicitEndpoint(t *testing.T) {
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)
	t.Setenv("LOOM_SERVER_URL", "")
	t.Setenv("LOOM_WORKSPACE", workflowManagementTestWorkspace)

	err := runWorkflowList(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "require --server or LOOM_SERVER_URL") {
		t.Fatalf("runWorkflowList error = %v, want explicit endpoint requirement", err)
	}
}

func TestWorkflowManagementRequiresExplicitWorkspace(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	t.Setenv("LOOM_WORKSPACE", "")

	err := runWorkflowList(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "require --workspace or LOOM_WORKSPACE") {
		t.Fatalf("runWorkflowList error = %v, want explicit workspace requirement", err)
	}
	if got := fixture.paths(); len(got) != 0 {
		t.Fatalf("management requests = %v, want none without a workspace", got)
	}
}

func TestWorkflowManagementUnavailableHostFailsClosedWithoutImplicitStartup(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	serverURL := fixture.server.URL
	fixture.server.Close()
	t.Setenv("LOOM_SERVER_URL", serverURL)

	err := runWorkflowList(&cobra.Command{}, nil)
	if err == nil || !strings.Contains(err.Error(), "endpoint discovery") || !strings.Contains(err.Error(), serverURL) {
		t.Fatalf("runWorkflowList error = %v, want unavailable configured endpoint", err)
	}
}

func TestWorkflowManagementCommandsNeverOpenStoreAndSendNoOpenModeCredential(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	workflowListJSON = true
	workflowVersionID = "version-1"
	workflowApproveJSON = true

	if _, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) }); err != nil {
		t.Fatalf("runWorkflowList: %v", err)
	}
	if _, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
	}); err != nil {
		t.Fatalf("runWorkflowApprove: %v", err)
	}
	if got := fixture.lastAuthorization(); got != "" {
		t.Fatalf("Authorization = %q, want no open-mode credential", got)
	}
	paths := strings.Join(fixture.paths(), "\n")
	for _, want := range []string{
		"GET /api/workspaces/TEST/workflow-catalog/drivers",
		"GET /api/workspaces/TEST/workflows/epic-runner/versions",
		"POST /api/workspaces/TEST/workflows/epic-runner/versions/version-1/approve",
	} {
		if !strings.Contains(paths, want) {
			t.Fatalf("management paths =\n%s\nwant %s", paths, want)
		}
	}
}

func TestWorkflowManagementClientReusesAuthenticatedClientDiscovery(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	workflowListJSON = true
	t.Cleanup(func() { workflowListJSON = false })

	if _, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) }); err != nil {
		t.Fatalf("runWorkflowList: %v", err)
	}
	if got := fixture.authDiscoveryCount(); got != 1 {
		t.Fatalf("GET /api/config count = %d, want exactly one authenticated-client discovery", got)
	}
}

func TestWorkflowManagementOpenModeWorksWithoutOperatorCredential(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	if err := runWorkflowList(&cobra.Command{}, nil); err != nil {
		t.Fatalf("runWorkflowList without local credential: %v", err)
	}
	if got := fixture.paths(); len(got) == 0 {
		t.Fatal("open-mode command sent no management request")
	}
}

func TestWorkflowManagementRequestNeverFollowsRedirect(t *testing.T) {
	var (
		leakMu   sync.Mutex
		leakAuth []string
	)
	leak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakMu.Lock()
		leakAuth = append(leakAuth, r.Header.Get("Authorization"))
		leakMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(leak.Close)

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/config" {
			writeWorkflowManagementTestJSON(w, http.StatusOK, map[string]string{"mode": "open"})
			return
		}
		http.Redirect(w, r, leak.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(front.Close)
	configureWorkflowManagementClient(t, front.URL, workflowManagementTestWorkspace)
	workflowListJSON = true
	t.Cleanup(func() { workflowListJSON = false })

	_, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) })
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("runWorkflowList error = %v, want redirect rejection", err)
	}
	leakMu.Lock()
	defer leakMu.Unlock()
	if len(leakAuth) != 0 {
		t.Fatalf("redirect target requests = %v, want none so bearer cannot leave configured origin", leakAuth)
	}
}

func TestWorkflowListEnrichesBareCatalogDriversThroughManagementVersions(t *testing.T) {
	fixture := setupWorkflowManagementFixture(t)
	fixture.mu.Lock()
	fixture.listBareDriver = true
	fixture.mu.Unlock()
	workflowListJSON = true
	t.Cleanup(func() { workflowListJSON = false })

	raw, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runWorkflowList: %v", err)
	}
	var payload struct {
		Workflows []struct {
			Approved       bool                             `json:"approved"`
			EffectiveTrust workflowcatalog.DriverTrustLevel `json:"effective_trust"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode workflow list: %v", err)
	}
	if len(payload.Workflows) != 1 || payload.Workflows[0].Approved || payload.Workflows[0].EffectiveTrust != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("workflow list = %+v, want active version trust derived through versions endpoint", payload.Workflows)
	}
	paths := strings.Join(fixture.paths(), "\n")
	if !strings.Contains(paths, "GET /api/workspaces/TEST/workflows/epic-runner/versions") {
		t.Fatalf("management paths =\n%s\nwant versions enrichment", paths)
	}
}

func TestWorkflowManagementTextOutputCompatibility(t *testing.T) {
	setupWorkflowManagementFixture(t)
	resetWorkflowCommandGlobals()
	t.Cleanup(resetWorkflowCommandGlobals)

	list, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) })
	if err != nil {
		t.Fatalf("runWorkflowList: %v", err)
	}
	if want := "epic-runner\tactive\tversion-1\n"; list != want {
		t.Fatalf("workflow list output = %q, want %q", list, want)
	}

	versions, err := captureWorkflowStdout(t, func() error {
		return runWorkflowVersions(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowVersions: %v", err)
	}
	wantVersions := "version-1\tactive\tapproved=false\ttrust=untrusted\n" +
		"version-2\t\tapproved=true\ttrust=trusted\n"
	if versions != wantVersions {
		t.Fatalf("workflow versions output = %q, want %q", versions, wantVersions)
	}

	workflowVersionID = "version-1"
	approved, err := captureWorkflowStdout(t, func() error {
		return runWorkflowApprove(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowApprove: %v", err)
	}
	if want := "Approved workflow epic-runner version version-1\n"; approved != want {
		t.Fatalf("workflow approve output = %q, want %q", approved, want)
	}

	unapproved, err := captureWorkflowStdout(t, func() error {
		return runWorkflowUnapprove(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowUnapprove: %v", err)
	}
	if want := "Unapproved workflow epic-runner version version-1\n"; unapproved != want {
		t.Fatalf("workflow unapprove output = %q, want %q", unapproved, want)
	}

	workflowVersionID = "version-2"
	activated, err := captureWorkflowStdout(t, func() error {
		return runWorkflowActivate(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
	})
	if err != nil {
		t.Fatalf("runWorkflowActivate: %v", err)
	}
	if want := "Activated workflow epic-runner version version-2\n"; activated != want {
		t.Fatalf("workflow activate output = %q, want %q", activated, want)
	}
}

func TestWorkflowManagementRejectsUnauthenticatedAndWrongWorkspace(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		fixture := setupWorkflowManagementFixture(t)
		fixture.mu.Lock()
		fixture.expectedBearer = "Bearer bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		fixture.mu.Unlock()
		workflowListJSON = true
		t.Cleanup(func() { workflowListJSON = false })

		_, err := captureWorkflowStdout(t, func() error { return runWorkflowList(&cobra.Command{}, nil) })
		if err == nil || !strings.Contains(err.Error(), "unauthorized") || !strings.Contains(err.Error(), "code=unauthenticated") {
			t.Fatalf("runWorkflowList error = %v, want machine-readable unauthenticated failure", err)
		}
	})

	t.Run("wrong workspace", func(t *testing.T) {
		setupWorkflowManagementFixture(t)
		t.Setenv("LOOM_WORKSPACE", "OTHER")
		workflowVersionID = "version-1"
		workflowApproveJSON = true
		t.Cleanup(func() {
			workflowVersionID = ""
			workflowApproveJSON = false
		})

		_, err := captureWorkflowStdout(t, func() error {
			return runWorkflowApprove(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
		})
		if err == nil || !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), "code=wrong_workspace") {
			t.Fatalf("runWorkflowApprove error = %v, want machine-readable wrong-workspace failure", err)
		}
	})
}

func TestWorkflowManagementRejectsNonOperatorAuthorityClasses(t *testing.T) {
	for _, class := range []authority.Class{
		authority.ClassExecution,
		authority.ClassSession,
		authority.ClassWebhook,
	} {
		t.Run(string(class), func(t *testing.T) {
			resetWorkflowCommandGlobals()
			t.Cleanup(resetWorkflowCommandGlobals)
			fixture := setupWorkflowManagementFixture(t)
			fixture.mu.Lock()
			fixture.rejectedClass = class
			fixture.mu.Unlock()
			workflowVersionID = "version-1"
			workflowApproveJSON = true

			_, err := captureWorkflowStdout(t, func() error {
				return runWorkflowApprove(&cobra.Command{}, []string{workflowcatalog.BuiltinEpicRunnerWorkflowName})
			})
			if err == nil || !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), "code=wrong_authority_class") {
				t.Fatalf("runWorkflowApprove error = %v, want %s-class denial", err, class)
			}
			paths := fixture.paths()
			if len(paths) != 1 || paths[0] != "GET /api/workspaces/TEST/workflows/epic-runner/versions" {
				t.Fatalf("management requests = %v, want denial before any lifecycle mutation", paths)
			}
		})
	}
}

func TestWorkflowManagementStatusErrorsPreserveDomainExitClasses(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: domain.ErrInvalid},
		{status: http.StatusNotFound, want: domain.ErrNotFound},
		{status: http.StatusConflict, want: domain.ErrConflict},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			err := workflowManagementStatusError(tt.status, []byte(`{"error":"test failure","code":"test"}`))
			if !strings.Contains(err.Error(), fmt.Sprint(tt.status)) || !strings.Contains(err.Error(), "code=test") {
				t.Fatalf("error = %v, want status and code", err)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, tt.want)
			}
		})
	}
}
