package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

func setExpectedIndexDigestHTTP(t *testing.T, value string) {
	t.Helper()
	prev := packaged.ExpectedIndexDigest
	packaged.ExpectedIndexDigest = value
	t.Cleanup(func() { packaged.ExpectedIndexDigest = prev })
}

// installPackagedEpicHTTP writes a verified packaged epic-runner tree, points
// LOOM_BUILTIN_ARTIFACTS_DIR at it, bakes the index digest, clears the desktop
// marker, and resets the packaged-artifact cache. LOOM_WORKSPACE_RUNTIME_DIR
// (the data dir where bundles stage) must be set by the caller so it stays
// stable across successive installs.
func installPackagedEpicHTTP(t *testing.T, serverSource string) {
	t.Helper()
	root := t.TempDir()
	name := BuiltinEpicRunnerWorkflowName
	dist := filepath.Join(root, name, "dist")
	sdkDir := filepath.Join(dist, "node_modules", "@loom", "sdk")
	if err := os.MkdirAll(sdkDir, 0o755); err != nil {
		t.Fatalf("create packaged dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(serverSource), 0o644); err != nil {
		t.Fatalf("write server.mjs: %v", err)
	}
	for _, f := range packaged.LoomSDKRuntimeFiles {
		content := "export {};\n"
		if f == "package.json" {
			content = `{"name":"@loom/sdk"}` + "\n"
		}
		if err := os.WriteFile(filepath.Join(sdkDir, f), []byte(content), 0o644); err != nil {
			t.Fatalf("write @loom/sdk %s: %v", f, err)
		}
	}
	artifactDigest, err := driverpkg.DigestDirectory(dist)
	if err != nil {
		t.Fatalf("digest dist: %v", err)
	}
	spec, ok := workflowdefs.BuiltinWorkflow(name)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	sourceDigest, runners, ok := workflowdefs.BuiltinArtifactExpectation(name)
	if !ok {
		t.Fatal("epic-runner expectation missing")
	}
	idx := packaged.Index{
		SchemaVersion: packaged.SchemaVersion,
		FlueCommit:    workflowdefs.PinnedFlueCommit,
		NodeVersion:   workflowdefs.PinnedNodeVersion,
		Target:        packaged.HostTargetTriple(),
		Builtins: map[string]packaged.Entry{
			name: {
				Path:           name,
				Entrypoint:     spec.Entrypoint,
				SourceDigest:   sourceDigest,
				ArtifactDigest: artifactDigest,
				Runners:        runners,
			},
		},
	}
	raw, err := packaged.EncodeIndex(idx)
	if err != nil {
		t.Fatalf("encode index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, packaged.IndexFileName), raw, 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
	t.Setenv("LOOM_BUILTIN_ARTIFACTS_DIR", root)
	t.Setenv("LOOM_LOCAL_RUNTIME", "")
	setExpectedIndexDigestHTTP(t, packaged.IndexDigest(raw))
	workflowdefs.ResetPackagedCacheForTest()
}

func newVersioningMux(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	return mux
}

func TestSyncBuiltinWorkflowHTTPHappyPath(t *testing.T) {
	st := memstore.New()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	installPackagedEpicHTTP(t, "export {};\n")
	mux := newVersioningMux(st)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName+"/builtin/sync", stringsReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var res workflowdefs.BuiltinSyncResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode sync result: %v", err)
	}
	if !res.Packaged.RegisteredNew || !res.Activated || res.Track != driverpkg.BuiltinTrackAuto {
		t.Fatalf("sync result = %+v, want registered+activated auto", res)
	}
}

func TestSyncBuiltinWorkflowHTTPRejectsCustom(t *testing.T) {
	st := memstore.New()
	mux := newVersioningMux(st)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/my-custom-wf/builtin/sync", stringsReader(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "not_builtin_workflow") {
		t.Fatalf("body = %s, want not_builtin_workflow", body)
	}
}

func TestSyncBuiltinWorkflowHTTPPinnedUpdateAvailable(t *testing.T) {
	st := memstore.New()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	installPackagedEpicHTTP(t, "export {};\n")
	mux := newVersioningMux(st)
	name := BuiltinEpicRunnerWorkflowName

	// First sync registers + activates vA (auto).
	syncA := doSync(t, mux, name, `{}`)
	vA := syncA.ActiveVersionID
	if vA == "" {
		t.Fatal("first sync produced no active version")
	}
	// Pin to vA via the activate route (default track pinned).
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/"+name+"/versions/"+vA+"/activate", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate pin status = %d body=%s", rec.Code, rec.Body.String())
	}
	// A new app build ships tree B.
	installPackagedEpicHTTP(t, "export const build = 2;\n")
	syncB := doSync(t, mux, name, `{}`)
	if syncB.Activated || !syncB.UpdateAvailable || syncB.Track != driverpkg.BuiltinTrackPinned {
		t.Fatalf("pinned sync = %+v, want update_available, not activated, pinned", syncB)
	}
	if syncB.ActiveVersionID != vA {
		t.Fatalf("pinned sync changed active to %q, want vA %q", syncB.ActiveVersionID, vA)
	}
}

func TestActivateWorkflowVersionTrackAutoNonPackaged400(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	mux := newVersioningMux(st)
	workflowdefs.ResetPackagedCacheForTest()
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName+"/versions/version-2/activate", `{"track":"auto"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !contains(body, "builtin_track_invalid") {
		t.Fatalf("body = %s, want builtin_track_invalid", body)
	}
}

func TestRollbackWorkflowHTTP(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	// Activate v2 then v1's previous is recorded by activating v1 first: set the
	// driver active on v2 with recorded previous v1.
	if _, _, err := driverpkg.ActivateDriverVersion(ctx, st, "TEST", BuiltinEpicRunnerWorkflowName, "version-1"); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	if _, _, err := driverpkg.ActivateDriverVersion(ctx, st, "TEST", BuiltinEpicRunnerWorkflowName, "version-2"); err != nil {
		t.Fatalf("activate v2: %v", err)
	}
	mux := newVersioningMux(st)

	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName+"/rollback", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var out struct {
		Active  bool `json:"active"`
		Version struct {
			VersionID string `json:"version_id"`
		} `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode rollback: %v", err)
	}
	if !out.Active || out.Version.VersionID != "version-1" {
		t.Fatalf("rollback out = %+v, want active version-1", out)
	}
}

func TestRollbackWorkflowHTTPNoRecord409(t *testing.T) {
	ctx := context.Background()
	st := seedEpicDriver(t, ctx)
	// Active on version-1 with no recorded previous.
	if _, _, err := driverpkg.ActivateDriverVersion(ctx, st, "TEST", BuiltinEpicRunnerWorkflowName, "version-1"); err != nil {
		t.Fatalf("activate v1: %v", err)
	}
	mux := newVersioningMux(st)
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName+"/rollback", `{}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !contains(body, "rollback_target_missing") {
		t.Fatalf("body = %s, want rollback_target_missing", body)
	}
}

func TestCreateWorkflowVersionDoesNotActivateByDefault(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	mux := newVersioningMux(st)

	body := `{"files":{"workflows/my-wf.ts":"export async function run(){return {status:\"completed\"};}\n"}}`
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/my-wf/versions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var out struct {
		Activated bool           `json:"activated"`
		Driver    *domain.Driver `json:"driver"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Activated {
		t.Fatalf("version activated by default; D5 requires inactive")
	}
	if out.Driver == nil || out.Driver.ActiveVersionID != "" {
		t.Fatalf("driver active_version_id = %v, want empty", out.Driver)
	}
}

func TestCreateWorkflowVersionActivatesWhenRequested(t *testing.T) {
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	mux := newVersioningMux(st)

	body := `{"activate":true,"files":{"workflows/my-wf.ts":"export async function run(){return {status:\"completed\"};}\n"}}`
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/my-wf/versions", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s, want 201", rec.Code, rec.Body.String())
	}
	var out struct {
		Activated bool `json:"activated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Activated {
		t.Fatalf("version not activated despite activate:true")
	}
}

// --- helpers ---

func doRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func doSync(t *testing.T, mux *http.ServeMux, name, body string) workflowdefs.BuiltinSyncResult {
	t.Helper()
	rec := doRequest(t, mux, http.MethodPost, "/api/workspaces/TEST/workflows/"+name+"/builtin/sync", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var res workflowdefs.BuiltinSyncResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode sync result: %v", err)
	}
	return res
}

// seedEpicDriver creates an epic-runner driver with two passed versions and no
// active version.
func seedEpicDriver(t *testing.T, ctx context.Context) store.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST",
		DriverID:     BuiltinEpicRunnerWorkflowName,
		Name:         BuiltinEpicRunnerWorkflowName,
		Status:       domain.DriverStatusDraft,
		TrustLevel:   domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	trusted := map[string]string{driverpkg.ManifestTrustLevelKey: string(domain.DriverTrustTrusted)}
	for _, in := range []store.DriverVersionCreate{
		{WorkspaceKey: "TEST", VersionID: "version-1", DriverID: BuiltinEpicRunnerWorkflowName, Version: 1, SourceDigest: "sha256:s1", BundleDigest: "sha256:b1", Manifest: cloneMap(trusted), ValidationStatus: domain.DriverVersionValidationPassed},
		{WorkspaceKey: "TEST", VersionID: "version-2", DriverID: BuiltinEpicRunnerWorkflowName, Version: 2, SourceDigest: "sha256:s2", BundleDigest: "sha256:b2", Manifest: cloneMap(trusted), ValidationStatus: domain.DriverVersionValidationPassed},
	} {
		if _, err := st.DriverVersions().Create(ctx, in); err != nil {
			t.Fatalf("create version %s: %v", in.VersionID, err)
		}
	}
	return st
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
