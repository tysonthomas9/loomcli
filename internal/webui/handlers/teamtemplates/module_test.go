package teamtemplates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/teamtemplate"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

func TestCatalogReturnsPickerShape(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st := memstore.New()
	mux := http.NewServeMux()
	NewModule(svcimpl.NewTeamTemplateService(st)).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/team-templates", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Templates []map[string]any `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Templates) != 4 {
		t.Fatalf("templates = %d, want 4", len(body.Templates))
	}
	first := body.Templates[0]
	assertJSONKeys(t, first, "id", "label", "description", "revision", "schema_version", "roles", "agents")
	if first["id"] != "fullstack-app" || first["label"] != "Full-Stack App Development" {
		t.Fatalf("first template identity = %q / %q", first["id"], first["label"])
	}
	if first["revision"] != float64(1) || first["schema_version"] != float64(teamtemplate.SchemaVersion) {
		t.Fatalf("first template versions = revision %v / schema %v", first["revision"], first["schema_version"])
	}

	roles, ok := first["roles"].([]any)
	if !ok || len(roles) != 5 {
		t.Fatalf("roles = %#v, want 5 entries", first["roles"])
	}
	architect := roles[0].(map[string]any)
	assertJSONKeys(t, architect, "name", "kind", "display_label", "description")
	if architect["name"] != "app-architect" || architect["kind"] != "worker" || architect["display_label"] != "Architecture" {
		t.Fatalf("first agent role = %#v", architect)
	}

	agents, ok := first["agents"].([]any)
	if !ok || len(agents) != 4 {
		t.Fatalf("agents = %#v, want 4 entries", first["agents"])
	}
	firstAgent := agents[0].(map[string]any)
	assertJSONKeys(t, firstAgent, "name", "role_name")
	if firstAgent["name"] != "app-architect-1" || firstAgent["role_name"] != "app-architect" {
		t.Fatalf("first agent = %#v", firstAgent)
	}
}

func TestApplyCreatesTeamAndReturnsDoneReport(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, mux := newApplyHarness(t, "WS")
	seedLocalRepo(t, st, "WS")

	rec := doApplyRequest(mux, "WS", "fullstack-app")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "done" {
		t.Fatalf("status envelope = %q, want done", body.Status)
	}
	if body.Report.TemplateID != "fullstack-app" || body.Report.WorkspaceKey != "WS" {
		t.Fatalf("report identity = %+v", body.Report)
	}
	if body.Report.Created != 9 || body.Report.Skipped != 0 || body.Report.Failed != 0 {
		t.Fatalf("report counts = %+v", body.Report)
	}
	if body.Report.Materialized != 4 {
		t.Fatalf("materialized = %d, want 4", body.Report.Materialized)
	}
	if len(body.Report.Warnings) != 0 {
		t.Fatalf("warnings = %v", body.Report.Warnings)
	}
	roles, err := st.Roles().List(t.Context(), "WS")
	if err != nil || len(roles) != 5 {
		t.Fatalf("stored agent roles = %d, err = %v", len(roles), err)
	}
	agents, err := st.Agents().List(t.Context(), "WS")
	if err != nil || len(agents) != 4 {
		t.Fatalf("stored agents = %d, err = %v", len(agents), err)
	}
}

func TestApplyAgainReportsEveryEntrySkipped(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, mux := newApplyHarness(t, "WS")
	seedLocalRepo(t, st, "WS")
	if rec := doApplyRequest(mux, "WS", "fullstack-app"); rec.Code != http.StatusOK {
		t.Fatalf("first apply status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec := doApplyRequest(mux, "WS", "fullstack-app")

	if rec.Code != http.StatusOK {
		t.Fatalf("second apply status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "done" || body.Report.Created != 0 || body.Report.Skipped != 9 || body.Report.Diverged != 0 || body.Report.Failed != 0 {
		t.Fatalf("second apply response = %+v", body)
	}
	if body.Report.Materialized != 4 {
		t.Fatalf("second apply materialized = %d, want 4 worktree re-checks", body.Report.Materialized)
	}
	for _, step := range body.Report.Steps {
		if step.Action != teamtemplate.StepSkippedMatch {
			t.Fatalf("step %s/%s action = %q, want skipped_match", step.Entity, step.Name, step.Action)
		}
	}
}

func TestApplyDryRunReturnsPlanWithoutCreatingTeam(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, mux := newApplyHarness(t, "WS")
	seedLocalRepo(t, st, "WS")

	rec := doApplyRequestWithBody(mux, "WS", "fullstack-app", `{"dry_run":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Report.DryRun || body.Report.Created != 9 || body.Report.Materialized != 0 {
		t.Fatalf("dry-run report = %+v", body.Report)
	}
	roles, _ := st.Roles().List(t.Context(), "WS")
	agents, _ := st.Agents().List(t.Context(), "WS")
	if len(roles) != 0 || len(agents) != 0 {
		t.Fatalf("dry run created agent roles=%d agents=%d", len(roles), len(agents))
	}
}

func TestApplyRefusesLocalWorkspaceWithoutRepositories(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st, mux := newApplyHarness(t, "WS")
	if err := bootstrap.MutateWorkspaceLocalState("WS", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = t.TempDir()
		return nil
	}); err != nil {
		t.Fatalf("seed local workspace path: %v", err)
	}

	rec := doApplyRequest(mux, "WS", "fullstack-app")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["kind"] != "validation_error" || !strings.Contains(body["error"], "add a repo first") {
		t.Fatalf("error response = %#v", body)
	}
	roles, _ := st.Roles().List(t.Context(), "WS")
	agents, _ := st.Agents().List(t.Context(), "WS")
	if len(roles) != 0 || len(agents) != 0 {
		t.Fatalf("refusal left agent roles=%d agents=%d", len(roles), len(agents))
	}
}

func TestApplyReturnsCompletedEnvelopeWithPartialFailureReport(t *testing.T) {
	service := stubTeamTemplateService{
		apply: func(context.Context, string, string, bool) (teamtemplate.ApplyReport, error) {
			return teamtemplate.ApplyReport{
				TemplateID: "fullstack-app",
				Failed:     1,
				Steps: []teamtemplate.StepResult{{
					Entity: "agent", Name: "qa-engineer-1", Action: teamtemplate.StepFailed, Error: "worktree unavailable",
				}},
			}, nil
		},
	}
	mux := http.NewServeMux()
	NewModule(service).Register(mux)

	rec := doApplyRequest(mux, "WS", "fullstack-app")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body applyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "done" || body.Report.Failed != 1 || len(body.Report.Steps) != 1 {
		t.Fatalf("partial apply response = %+v", body)
	}
}

func newApplyHarness(t *testing.T, workspaceKey string) (*memstore.Store, *http.ServeMux) {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: workspaceKey, Name: "Workspace"}); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(svcimpl.NewTeamTemplateService(st)).Register(mux)
	return st, mux
}

func seedLocalRepo(t *testing.T, st *memstore.Store, workspaceKey string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "app")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("create repo directory: %v", err)
	}
	runGit(t, repoPath, "init", "-b", "main")
	runGit(t, repoPath, "config", "user.email", "test@example.com")
	runGit(t, repoPath, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write repo fixture: %v", err)
	}
	runGit(t, repoPath, "add", "README.md")
	runGit(t, repoPath, "commit", "-m", "initial")
	if _, err := st.Repos().Create(t.Context(), store.RepoCreate{WorkspaceKey: workspaceKey, Name: "app", DefaultBranch: "main"}); err != nil {
		t.Fatalf("seed repository: %v", err)
	}
	if err := bootstrap.MutateWorkspaceLocalState(workspaceKey, func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"app": repoPath}
		return nil
	}); err != nil {
		t.Fatalf("seed local workspace: %v", err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // Fixed test fixture commands run only inside t.TempDir.
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func doApplyRequest(mux *http.ServeMux, workspaceKey, teamTemplateID string) *httptest.ResponseRecorder {
	return doApplyRequestWithBody(mux, workspaceKey, teamTemplateID, "")
}

func doApplyRequestWithBody(mux *http.ServeMux, workspaceKey, teamTemplateID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspaceKey+"/team-templates/"+teamTemplateID+"/apply", strings.NewReader(body))
	mux.ServeHTTP(rec, req)
	return rec
}

func assertJSONKeys(t *testing.T, value map[string]any, want ...string) {
	t.Helper()
	if len(value) != len(want) {
		t.Fatalf("JSON keys = %v, want %v", value, want)
	}
	for _, key := range want {
		if _, ok := value[key]; !ok {
			t.Fatalf("JSON object %#v missing key %q", value, key)
		}
	}
}

type stubTeamTemplateService struct {
	apply func(context.Context, string, string, bool) (teamtemplate.ApplyReport, error)
}

func (stubTeamTemplateService) CatalogTeamTemplates(context.Context) ([]teamtemplate.TeamTemplate, error) {
	return nil, nil
}

func (s stubTeamTemplateService) ApplyTeamTemplate(ctx context.Context, workspaceKey, teamTemplateID string, dryRun bool) (teamtemplate.ApplyReport, error) {
	return s.apply(ctx, workspaceKey, teamTemplateID, dryRun)
}
