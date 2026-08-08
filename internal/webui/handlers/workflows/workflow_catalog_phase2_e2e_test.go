//go:build e2e
// +build e2e

package workflows

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestE2E_WorkflowCatalogPhase2RealFleetDBLoomHTTPAndCLI is the Phase 2
// acceptance proof. It runs the real fleet-db and loom binaries over a fresh
// ephemeral Redis backend. Direct FleetDB access is limited to fixture setup
// and the final durability read; every lifecycle intent crosses loom serve.
func TestE2E_WorkflowCatalogPhase2RealFleetDBLoomHTTPAndCLI(t *testing.T) {
	e2e := newWorkflowCatalogPhase2E2E(t)
	e2e.startFleetDB()
	e2e.seedPrerequisites()
	e2e.startLoomServe()

	list := e2e.listDriversHTTP()
	assertWorkflowCatalogDriverListed(t, list, e2e.driverID, 1)
	versions := e2e.listVersionsHTTP(e2e.workspace)
	assertWorkflowCatalogVersionSet(t, versions, e2e.driverID, e2e.versionID, 1, "", false)

	var cliList workflowCatalogCLIList
	e2e.runCLIJSON(&cliList, "workflow", "list", "--json")
	// The stable CLI list shape intentionally omits the internal CAS revision;
	// the HTTP version read below is the mutation precondition source.
	assertWorkflowCatalogDriverListed(t, cliList, e2e.driverID, 0)
	// Intentionally launch a second standalone command immediately. Each
	// invocation gets one /api/config discovery; redundant root/adapter
	// discovery would exhaust the real config endpoint's token bucket and
	// surface here as HTTP 429.
	var cliVersions workflowCatalogCLIVersions
	e2e.runCLIJSON(&cliVersions, "workflow", "versions", e2e.driverID, "--json")
	if cliVersions.DriverID != e2e.driverID || len(cliVersions.Versions) != 1 {
		t.Fatalf("CLI versions = %+v, want one version for %s", cliVersions, e2e.driverID)
	}
	if got := cliVersions.Versions[0]; got.Version == nil || got.Version.VersionID != e2e.versionID || got.Approved || got.Active || got.EffectiveTrust != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("initial CLI version = %+v, want inactive, unapproved, untrusted %s", got, e2e.versionID)
	}

	mutationPath := e2e.lifecyclePath(e2e.workspace, "approve")
	var approved workflowCatalogHTTPAction
	e2e.doLoomJSON(http.MethodPost, mutationPath, map[string]uint64{"expected_revision": 1}, "", http.StatusOK, &approved)
	if approved.Action != "approve" || approved.Driver == nil || approved.Version == nil || approved.Driver.Revision != 2 || !workflowcatalog.VersionApproved(approved.Driver, approved.Version) {
		t.Fatalf("HTTP approve = %+v, want approved revision 2", approved)
	}

	var stale workflowCatalogHTTPError
	e2e.doLoomJSON(http.MethodPost, e2e.lifecyclePath(e2e.workspace, "activate"), map[string]uint64{"expected_revision": 1}, "", http.StatusConflict, &stale)
	if stale.Code != "stale_revision" {
		t.Fatalf("stale activate error = %+v, want stale_revision", stale)
	}
	versions = e2e.listVersionsHTTP(e2e.workspace)
	assertWorkflowCatalogVersionSet(t, versions, e2e.driverID, e2e.versionID, 2, "", true)

	var activated workflowCatalogCLIAction
	e2e.runCLIJSON(&activated, "workflow", "activate", e2e.driverID, "--version", e2e.versionID, "--json")
	if activated.Version == nil || activated.Version.VersionID != e2e.versionID || !activated.Active || !activated.Approved || activated.EffectiveTrust != workflowcatalog.DriverTrustTrusted {
		t.Fatalf("CLI activate = %+v, want active, approved, trusted", activated)
	}
	versions = e2e.listVersionsHTTP(e2e.workspace)
	assertWorkflowCatalogVersionSet(t, versions, e2e.driverID, e2e.versionID, 3, e2e.versionID, true)

	var unapproved workflowCatalogCLIAction
	e2e.runCLIJSON(&unapproved, "workflow", "unapprove", e2e.driverID, "--version", e2e.versionID, "--json")
	if unapproved.Version == nil || unapproved.Version.VersionID != e2e.versionID || !unapproved.Active || unapproved.Approved || unapproved.EffectiveTrust != workflowcatalog.DriverTrustUntrusted {
		t.Fatalf("CLI unapprove = %+v, want still-active, unapproved, untrusted", unapproved)
	}

	versions = e2e.listVersionsHTTP(e2e.workspace)
	assertWorkflowCatalogVersionSet(t, versions, e2e.driverID, e2e.versionID, 4, e2e.versionID, false)
	e2e.assertFinalDurableState()
}

type workflowCatalogPhase2E2E struct {
	t *testing.T

	repoRoot    string
	fleetDBRepo string
	loomBin     string
	fleetDBBin  string

	workspace string
	driverID  string
	versionID string
	actor     string

	workDir     string
	runtimeDir  string
	configDir   string
	homeDir     string
	fleetURL    string
	fleetAPIKey string
	loomURL     string

	fleetClient *fleetdb.Client
	httpClient  *http.Client
}

type workflowCatalogHTTPList struct {
	Workflows []workflowCatalogHTTPListItem `json:"workflows"`
}

type workflowCatalogHTTPListItem struct {
	DriverID        string                       `json:"driver_id"`
	Name            string                       `json:"name"`
	Status          workflowcatalog.DriverStatus `json:"status"`
	ActiveVersionID string                       `json:"active_version_id"`
	Revision        uint64                       `json:"revision"`
}

type workflowCatalogHTTPVersions struct {
	Driver   *workflowcatalog.Driver          `json:"driver"`
	Versions []*workflowcatalog.DriverVersion `json:"versions"`
}

type workflowCatalogHTTPAction struct {
	Action  string                         `json:"action"`
	Driver  *workflowcatalog.Driver        `json:"driver"`
	Version *workflowcatalog.DriverVersion `json:"version"`
}

type workflowCatalogHTTPError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

type workflowCatalogCLIList = workflowCatalogHTTPList

type workflowCatalogCLIVersions struct {
	DriverID string                     `json:"driver_id"`
	Versions []workflowCatalogCLIAction `json:"versions"`
}

type workflowCatalogCLIAction struct {
	Version        *workflowcatalog.DriverVersion   `json:"version"`
	Active         bool                             `json:"active"`
	Approved       bool                             `json:"approved"`
	EffectiveTrust workflowcatalog.DriverTrustLevel `json:"effective_trust"`
}

func newWorkflowCatalogPhase2E2E(t *testing.T) *workflowCatalogPhase2E2E {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real Workflow Catalog Phase 2 E2E under -short")
	}
	repoRoot := workflowEndpointRepoRoot(t)
	fleetDBRepo := workflowCatalogPhase2FleetRepo(t, repoRoot)
	e2e := &workflowCatalogPhase2E2E{
		t:           t,
		repoRoot:    repoRoot,
		fleetDBRepo: fleetDBRepo,
		workspace:   "WFCATPHASE2",
		driverID:    "phase2-catalog-e2e",
		versionID:   "phase2-version-1",
		actor:       "workflow-catalog-phase2-e2e",
		workDir:     filepath.Join(t.TempDir(), "work"),
		runtimeDir:  filepath.Join(t.TempDir(), "runtime"),
		configDir:   filepath.Join(t.TempDir(), "config"),
		homeDir:     filepath.Join(t.TempDir(), "home"),
		httpClient:  &http.Client{Timeout: 5 * time.Second},
	}
	for _, dir := range []string{e2e.workDir, e2e.runtimeDir, e2e.configDir, e2e.homeDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create E2E directory %s: %v", dir, err)
		}
	}
	e2e.loomBin = workflowEndpointBuildGoBinary(t, repoRoot, "./cmd/loom", "loom-phase2-e2e")
	e2e.fleetDBBin = workflowEndpointBuildGoBinary(t, fleetDBRepo, "./cmd/fleet-db", "fleet-db-phase2-e2e")
	return e2e
}

func workflowCatalogPhase2FleetRepo(t *testing.T, repoRoot string) string {
	t.Helper()
	if override := strings.TrimSpace(os.Getenv("FLEET_DB_REPO")); override != "" {
		if _, err := os.Stat(filepath.Join(override, "cmd", "fleet-db")); err != nil {
			t.Skipf("FLEET_DB_REPO=%s does not contain cmd/fleet-db: %v", override, err)
		}
		return override
	}
	for _, sibling := range []string{"fleet-db-modular-monolith-phase2", "fleet-db"} {
		candidate := filepath.Clean(filepath.Join(repoRoot, "..", sibling))
		if _, err := os.Stat(filepath.Join(candidate, "cmd", "fleet-db")); err == nil {
			return candidate
		}
	}
	t.Skip("paired fleet-db checkout not found; set FLEET_DB_REPO")
	return ""
}

func (e *workflowCatalogPhase2E2E) startFleetDB() {
	e.t.Helper()
	e.t.Setenv(bootstrap.EnvFleetDBBin, e.fleetDBBin)
	e.t.Setenv("FLEET_RATE_LIMIT_ENABLED", "false")
	// Preserve an explicit mode for later capability E2Es that reuse this real
	// process harness. Phase 2 itself has no design behavior and retains its
	// faster inline default when the caller does not select a mode.
	if strings.TrimSpace(os.Getenv("FLEETDB_ISSUE_DESIGN_STORAGE")) == "" {
		e.t.Setenv("FLEETDB_ISSUE_DESIGN_STORAGE", "inline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.t.Cleanup(cancel)
	dataDir := filepath.Join(e.t.TempDir(), "fleet-data")
	embedded, err := bootstrap.StartEmbedded(ctx, dataDir, workflowEndpointQuietLogger())
	if err != nil {
		e.t.Fatalf("start real embedded fleet-db: %v", err)
	}
	e.t.Cleanup(func() {
		if err := embedded.Stop(); err != nil {
			e.t.Logf("stop embedded fleet-db: %v", err)
		}
	})
	e.fleetURL = embedded.URL()
	e.fleetAPIKey, err = authority.ReadLocalFleetDBServiceCredential(filepath.Join(dataDir, "fleet-db", "auth"))
	if err != nil {
		e.t.Fatalf("read embedded FleetDB service credential: %v", err)
	}
	// A caller-controlled actor header alone must never authenticate to the
	// product's embedded FleetDB profile.
	request, err := http.NewRequest(http.MethodGet, e.fleetURL+fleetdb.CapabilitiesAPIPath, nil)
	if err != nil {
		e.t.Fatalf("build unauthenticated capability request: %v", err)
	}
	request.Header.Set("X-Actor", e.actor)
	response, err := e.httpClient.Do(request)
	if err != nil {
		e.t.Fatalf("unauthenticated capability request: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		e.t.Fatalf("spoofed X-Actor capability status = %d, want 401", response.StatusCode)
	}
	e.fleetClient, err = embedded.NewClient(fleetdb.Config{Actor: e.actor})
	if err != nil {
		e.t.Fatalf("create fixture fleet-db client: %v", err)
	}
	e.t.Cleanup(func() { _ = e.fleetClient.Close() })
}

func (e *workflowCatalogPhase2E2E) seedPrerequisites() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := e.fleetClient.Workspaces().Create(ctx, store.WorkspaceCreate{Key: e.workspace, Name: "Workflow Catalog Phase 2 E2E"}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("seed workspace: %v", err)
	}
	// A real second workspace lets the request pass the server's workspace
	// existence guard so the catalog's workspace-bound operator authority is
	// what denies the cross-workspace mutation.
	if _, err := e.fleetClient.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "OTHER", Name: "Workflow Catalog Negative Scope"}); err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("seed wrong-workspace authority fixture: %v", err)
	}
	driver, err := e.fleetClient.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: e.workspace,
		DriverID:     e.driverID,
		Name:         e.driverID,
		OwnerType:    workflowcatalog.DriverOwnerUser,
		OwnerRef:     e.actor,
		Status:       workflowcatalog.DriverStatusDraft,
		TrustLevel:   workflowcatalog.DriverTrustUntrusted,
		Metadata:     map[string]string{"fixture_metadata": "preserve-me"},
	})
	if err != nil {
		e.t.Fatalf("seed driver: %v", err)
	}
	if driver.Revision != 1 {
		e.t.Fatalf("seed driver revision = %d, want 1", driver.Revision)
	}
	version, err := e.fleetClient.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     e.workspace,
		DriverID:         e.driverID,
		VersionID:        e.versionID,
		Version:          1,
		SourceRef:        "fixture://phase2/source",
		SourceDigest:     "sha256:phase2-source",
		BundleRef:        "fixture://phase2/bundle",
		BundleDigest:     "sha256:phase2-bundle",
		Runtime:          "node",
		Manifest:         map[string]string{workflowcatalog.ManifestTrustLevelKey: string(workflowcatalog.DriverTrustUntrusted)},
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
		CreatedBy:        e.actor,
	})
	if err != nil {
		e.t.Fatalf("seed validated driver version: %v", err)
	}
	if version.DriverID != e.driverID || version.ValidationStatus != workflowcatalog.DriverVersionValidationPassed {
		e.t.Fatalf("seeded version = %+v", version)
	}
}

func (e *workflowCatalogPhase2E2E) startLoomServe() {
	e.t.Helper()
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		e.t.Fatalf("pick loom serve port: %v", err)
	}
	e.loomURL = "http://127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(e.loomBin, "serve", "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--frontend-url", "http://127.0.0.1:9")
	cmd.Dir = e.workDir
	cmd.Env = workflowEndpointEnv(map[string]string{
		"HOME":                          e.homeDir,
		"LOOM_CONFIG_DIR":               e.configDir,
		"LOOM_WORKSPACE":                e.workspace,
		"LOOM_WORKSPACE_RUNTIME_DIR":    e.runtimeDir,
		"LOOM_FLEET_DB_URL":             e.fleetURL,
		"LOOM_FLEET_URL":                "",
		"LOOM_SERVER_URL":               "",
		"LOOM_DISABLE_H2C":              "1",
		"LOOM_DRIVER_EXECUTOR":          "0",
		"LOOM_WORKFLOW_CATALOG_ENABLED": "true",
		bootstrap.EnvFleetDBBin:         e.fleetDBBin,
		bootstrap.EnvFleetDBAPIKey:      e.fleetAPIKey,
		bootstrap.EnvFleetDBActor:       e.actor,
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start real loom serve: %v", err)
	}
	e.t.Cleanup(func() {
		workflowEndpointStopProcess(e.t, cmd)
		if e.t.Failed() {
			e.t.Logf("loom serve stdout:\n%s", strings.TrimSpace(stdout.String()))
			e.t.Logf("loom serve stderr:\n%s", strings.TrimSpace(stderr.String()))
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, requestErr := e.httpClient.Get(e.loomURL + "/health")
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("loom serve did not become healthy at %s\nstdout:\n%s\nstderr:\n%s", e.loomURL, stdout.String(), stderr.String())
}

func (e *workflowCatalogPhase2E2E) listDriversHTTP() workflowCatalogHTTPList {
	e.t.Helper()
	var out workflowCatalogHTTPList
	e.doLoomJSON(http.MethodGet, "/api/workspaces/"+e.workspace+"/workflow-catalog/drivers", nil, "", http.StatusOK, &out)
	return out
}

func (e *workflowCatalogPhase2E2E) listVersionsHTTP(workspace string) workflowCatalogHTTPVersions {
	e.t.Helper()
	var out workflowCatalogHTTPVersions
	path := "/api/workspaces/" + workspace + "/workflows/" + e.driverID + "/versions"
	e.doLoomJSON(http.MethodGet, path, nil, "", http.StatusOK, &out)
	return out
}

func (e *workflowCatalogPhase2E2E) lifecyclePath(workspace, action string) string {
	return "/api/workspaces/" + workspace + "/workflows/" + e.driverID + "/versions/" + e.versionID + "/" + action
}

func (e *workflowCatalogPhase2E2E) doLoomJSON(method, path string, input any, bearer string, wantStatus int, output any) {
	e.t.Helper()
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			e.t.Fatalf("encode %s %s: %v", method, path, err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, e.loomURL+path, body)
	if err != nil {
		e.t.Fatalf("create %s %s: %v", method, path, err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	workflowEndpointDecodeResponse(e.t, resp, wantStatus, output)
}

func (e *workflowCatalogPhase2E2E) runCLIJSON(output any, args ...string) {
	e.t.Helper()
	commandArgs := append([]string{"--server", e.loomURL, "--workspace", e.workspace}, args...)
	cmd := exec.Command(e.loomBin, commandArgs...)
	cmd.Dir = e.workDir
	cmd.Env = workflowEndpointEnv(map[string]string{
		"HOME":                       e.homeDir,
		"LOOM_CONFIG_DIR":            e.configDir,
		"LOOM_WORKSPACE_RUNTIME_DIR": e.runtimeDir,
		"LOOM_SERVER_URL":            "",
		"LOOM_WORKSPACE":             "",
		"LOOM_FLEET_DB_URL":          "http://127.0.0.1:1",
		"LOOM_FLEET_URL":             "",
		bootstrap.EnvFleetDBBin:      filepath.Join(e.t.TempDir(), "must-not-start-fleet-db"),
		bootstrap.EnvFleetDBAPIKey:   "",
		bootstrap.EnvFleetDBActor:    "must-not-reach-fleet-db",
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		e.t.Fatalf("standalone CLI loom %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(commandArgs, " "), err, stdout, stderr.String())
	}
	if err := json.Unmarshal(stdout, output); err != nil {
		e.t.Fatalf("decode standalone CLI loom %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(commandArgs, " "), err, stdout, stderr.String())
	}
}

func (e *workflowCatalogPhase2E2E) assertFinalDurableState() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	driver, err := e.fleetClient.Drivers().Get(ctx, e.workspace, e.driverID)
	if err != nil {
		e.t.Fatalf("read final durable driver: %v", err)
	}
	version, err := e.fleetClient.DriverVersions().Get(ctx, e.workspace, e.versionID)
	if err != nil {
		e.t.Fatalf("read final durable version: %v", err)
	}
	if driver.Revision != 4 || driver.ActiveVersionID != e.versionID || driver.Metadata["fixture_metadata"] != "preserve-me" || workflowcatalog.VersionApproved(driver, version) || workflowcatalog.EffectiveTrust(driver, version) != workflowcatalog.DriverTrustUntrusted {
		e.t.Fatalf("final durable state driver=%+v version=%+v, want revision 4, active-but-unapproved, preserved metadata", driver, version)
	}
	if _, exists := driver.Metadata[workflowcatalog.ApprovedVersionMetadataKey(e.versionID)]; exists {
		e.t.Fatalf("final durable driver retained approval metadata: %+v", driver.Metadata)
	}
}

func assertWorkflowCatalogDriverListed(t *testing.T, list workflowCatalogHTTPList, driverID string, revision uint64) {
	t.Helper()
	for _, item := range list.Workflows {
		if item.DriverID == driverID {
			if item.Name != driverID || item.Revision != revision {
				t.Fatalf("listed driver = %+v, want name %s revision %d", item, driverID, revision)
			}
			return
		}
	}
	t.Fatalf("registered driver %s not found in %+v", driverID, list.Workflows)
}

func assertWorkflowCatalogVersionSet(t *testing.T, set workflowCatalogHTTPVersions, driverID, versionID string, revision uint64, activeVersionID string, approved bool) {
	t.Helper()
	if set.Driver == nil || set.Driver.DriverID != driverID || set.Driver.Revision != revision || set.Driver.ActiveVersionID != activeVersionID || len(set.Versions) != 1 || set.Versions[0] == nil || set.Versions[0].VersionID != versionID {
		t.Fatalf("version set = %+v, want driver=%s version=%s revision=%d active=%s", set, driverID, versionID, revision, activeVersionID)
	}
	if got := workflowcatalog.VersionApproved(set.Driver, set.Versions[0]); got != approved {
		t.Fatalf("version approval = %t, want %t; driver=%+v version=%+v", got, approved, set.Driver, set.Versions[0])
	}
}
