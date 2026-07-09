package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const agentRecordTestWS = "WS"

func TestUnifiedAgentsListMergesSupervisedRecordsAndLegacyBindings(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	seedDriverVersion(t, st, "driver-1", "version-1")
	if _, err := st.Agents().Create(ctx, store.AgentCreate{WorkspaceKey: agentRecordTestWS, Name: "falcon", RoleName: "task"}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "agt-docs-x7", Name: "Docs assistant",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning, RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create agent service: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "agt-docs-x7-1", Name: "Docs assistant",
		SourceKind: store.InternalSourceKind, DriverID: "driver-1", DriverVersionID: "version-1",
		TargetAgentServiceID: "agt-docs-x7", Enabled: true,
	}); err != nil {
		t.Fatalf("create attached binding: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "legacy-review", Name: "Legacy review",
		SourceKind: store.CronSourceKind, DriverID: "driver-1", DriverVersionID: "version-1",
		Schedule: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("create legacy binding: %v", err)
	}

	rec := doAgentRequest(t, newAgentsMux(st), http.MethodGet, "/api/workspaces/WS/agents", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /agents status = %d body=%s", rec.Code, rec.Body.String())
	}
	items := decodeListItems(t, rec.Body.Bytes())
	kinds := map[string]map[string]any{}
	for _, item := range items {
		kinds[item["kind"].(string)] = item
	}
	if kinds[agentRecordKindSupervised]["id"] != "falcon" {
		t.Fatalf("supervised item = %+v", kinds[agentRecordKindSupervised])
	}
	if kinds[agentRecordKindPrompt]["id"] != "agt-docs-x7" {
		t.Fatalf("prompt item = %+v", kinds[agentRecordKindPrompt])
	}
	if bindings, ok := kinds[agentRecordKindPrompt]["bindings"].([]any); !ok || len(bindings) != 1 {
		t.Fatalf("prompt bindings = %#v, want one attached binding", kinds[agentRecordKindPrompt]["bindings"])
	}
	if kinds[agentRecordKindBinding]["id"] != "legacy-review" {
		t.Fatalf("legacy binding item = %+v", kinds[agentRecordKindBinding])
	}
}

func TestPromptAgentCreateTransactionCreatesRecordBindingAndRole(t *testing.T) {
	st := newAgentRecordStore(t)
	body := `{
		"kind":"prompt",
		"name":"Docs assistant",
		"backend":"codex",
		"behavior":{"role_name":"docs-assistant","role_create":{"description":"Docs","task_filter":"docs"}},
		"trigger":{"source_kind":"internal","event_type_patterns":["internal.task.ready"]},
		"enabled":true
	}`
	rec := doAgentRequest(t, newAgentsMux(st), http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /agents status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created agentRecordDTO
	decodeJSON(t, rec.Body.Bytes(), &created)
	if !strings.HasPrefix(created.ID, "agt-docs-assistant-") || created.Kind != agentRecordKindPrompt || !created.Enabled {
		t.Fatalf("created record = %+v", created)
	}
	if created.Behavior.RoleName != "docs-assistant" {
		t.Fatalf("behavior role_name = %q", created.Behavior.RoleName)
	}
	if len(created.Bindings) != 1 || created.Bindings[0].TargetAgentServiceID != created.ID {
		t.Fatalf("created bindings = %+v", created.Bindings)
	}
	role, err := st.Roles().Get(context.Background(), agentRecordTestWS, "docs-assistant")
	if err != nil || role.Description != "Docs" || role.TaskFilter != "docs" {
		t.Fatalf("ensured role = %+v err=%v", role, err)
	}
	var runInput map[string]string
	if err := json.Unmarshal([]byte(created.Bindings[0].SourceConfigRef), &runInput); err != nil {
		t.Fatalf("source_config_ref is not JSON: %v", err)
	}
	if runInput["roleName"] != "docs-assistant" || runInput["backend"] != "codex" {
		t.Fatalf("run input = %+v", runInput)
	}
}

func TestPromptAgentCreateBindingFailureDeletesAgentRecord(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	driver, err := st.Drivers().Get(ctx, agentRecordTestWS, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "taken", Name: "Taken",
		SourceKind: store.InternalSourceKind, DriverID: driver.DriverID, DriverVersionID: driver.ActiveVersionID,
		Enabled: true,
	}); err != nil {
		t.Fatalf("seed taken binding: %v", err)
	}

	body := `{
		"kind":"prompt",
		"name":"Docs assistant",
		"behavior":{"role_name":"docs-assistant"},
		"trigger":{"source_kind":"internal","binding_id":"taken"},
		"enabled":true
	}`
	rec := doAgentRequest(t, newAgentsMux(st), http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /agents status = %d body=%s", rec.Code, rec.Body.String())
	}
	records, err := st.AgentServices().List(ctx, agentRecordTestWS, store.AgentServiceFilter{})
	if err != nil {
		t.Fatalf("list agent services: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("agent records after compensated failure = %+v, want none", records)
	}
}

func TestAgentEnableDisableFanoutAndBindingGuard(t *testing.T) {
	st := newAgentRecordStore(t)
	mux := http.NewServeMux()
	NewModule(nil, st, nil).Register(mux)
	triggerbindings.NewModule(st).Register(mux)
	created := createPromptAgentForTest(t, mux)
	bindingID := created.Bindings[0].BindingID

	rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents/"+created.ID+"/disable", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d body=%s", rec.Code, rec.Body.String())
	}
	record, err := st.AgentServices().Get(context.Background(), agentRecordTestWS, created.ID)
	if err != nil || record.DesiredState != domain.AgentServiceDesiredPaused {
		t.Fatalf("record after disable = %+v err=%v", record, err)
	}
	binding, err := st.TriggerBindings().Get(context.Background(), agentRecordTestWS, bindingID)
	if err != nil || binding.Enabled {
		t.Fatalf("binding after disable = %+v err=%v", binding, err)
	}

	rec = doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/trigger-bindings/"+bindingID+"/enable", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "managed by agent "+created.ID) {
		t.Fatalf("direct binding enable status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents/"+created.ID+"/enable", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("enable status = %d body=%s", rec.Code, rec.Body.String())
	}
	binding, err = st.TriggerBindings().Get(context.Background(), agentRecordTestWS, bindingID)
	if err != nil || !binding.Enabled {
		t.Fatalf("binding after enable = %+v err=%v", binding, err)
	}
}

func TestAgentDeleteDeletesBindingsRevokesGrantsAndArchivesRecord(t *testing.T) {
	st := newAgentRecordStore(t)
	mux := newAgentsMux(st)
	created := createPromptAgentForTest(t, mux)
	bindingID := created.Bindings[0].BindingID
	if _, err := st.ConnectorGrants().Create(context.Background(), store.ConnectorGrantCreate{
		WorkspaceKey: agentRecordTestWS, GrantID: "grant-1", ConnectorID: "github",
		BindingID: bindingID, Action: "github.comment", ResourcePattern: "repo:o/r",
	}); err != nil {
		t.Fatalf("create grant: %v", err)
	}

	rec := doAgentRequest(t, mux, http.MethodDelete, "/api/workspaces/WS/agents/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.TriggerBindings().Get(context.Background(), agentRecordTestWS, bindingID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("binding get err = %v, want ErrNotFound", err)
	}
	grants, err := st.ConnectorGrants().ListByBinding(context.Background(), agentRecordTestWS, bindingID)
	if err != nil || len(grants) != 0 {
		t.Fatalf("active grants after delete = %+v err=%v", grants, err)
	}
	record, err := st.AgentServices().Get(context.Background(), agentRecordTestWS, created.ID)
	if err != nil {
		t.Fatalf("get archived record: %v", err)
	}
	// Wave B: deleted_at is the archive signal (the Wave-A metadata marker is
	// superseded); desired_state is parked stopped before archiving.
	if record.DesiredState != domain.AgentServiceDesiredStopped || record.DeletedAt == nil {
		t.Fatalf("archived record = %+v", record)
	}

	rec = doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents", "")
	if listContainsID(t, rec.Body.Bytes(), created.ID) {
		t.Fatalf("default list includes archived record: %s", rec.Body.String())
	}
	rec = doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents?include=archived", "")
	if !listContainsID(t, rec.Body.Bytes(), created.ID) {
		t.Fatalf("include=archived list omitted archived record: %s", rec.Body.String())
	}
}

func TestAgentRunsNewestFirstAndExcludesUnattributedRuns(t *testing.T) {
	st := newAgentRecordStore(t)
	mux := newAgentsMux(st)
	created := createPromptAgentForTest(t, mux)
	binding := created.Bindings[0].TriggerBinding
	ctx := context.Background()
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: agentRecordTestWS, RunID: "run-old", DriverID: binding.DriverID,
		DriverVersionID: binding.DriverVersionID, TriggerBindingID: binding.BindingID,
	}); err != nil {
		t.Fatalf("create old run: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: agentRecordTestWS, RunID: "run-unattributed", DriverID: binding.DriverID,
		DriverVersionID: binding.DriverVersionID,
	}); err != nil {
		t.Fatalf("create unattributed run: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: agentRecordTestWS, RunID: "run-new", DriverID: binding.DriverID,
		DriverVersionID: binding.DriverVersionID, TriggerBindingID: binding.BindingID,
	}); err != nil {
		t.Fatalf("create new run: %v", err)
	}

	rec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents/"+created.ID+"/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("runs status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		AgentID string              `json:"agent_id"`
		Runs    []*domain.DriverRun `json:"runs"`
	}
	decodeJSON(t, rec.Body.Bytes(), &out)
	if out.AgentID != created.ID {
		t.Fatalf("agent_id = %q, want %q", out.AgentID, created.ID)
	}
	if len(out.Runs) != 2 || out.Runs[0].RunID != "run-new" || out.Runs[1].RunID != "run-old" {
		t.Fatalf("runs = %+v, want run-new then run-old only", out.Runs)
	}
}

func newAgentRecordStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: agentRecordTestWS, Name: "Test Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	seedPromptAgentDriver(t, st)
	return st
}

func newAgentsMux(st store.Store) *http.ServeMux {
	mux := http.NewServeMux()
	NewModule(nil, st, nil).Register(mux)
	return mux
}

func seedRole(t *testing.T, st store.Store, name string) {
	t.Helper()
	if _, err := st.Roles().Create(context.Background(), store.RoleCreate{WorkspaceKey: agentRecordTestWS, Name: name}); err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
}

func seedDriverVersion(t *testing.T, st store.Store, driverID, versionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: agentRecordTestWS, DriverID: driverID, Name: driverID, ActiveVersionID: versionID,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: agentRecordTestWS, VersionID: versionID, DriverID: driverID, Version: 1,
		SourceDigest: "src-" + versionID, BundleDigest: "bundle-" + versionID,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
}

func seedPromptAgentDriver(t *testing.T, st store.Store) {
	t.Helper()
	seedDriverVersion(t, st, workflowdefs.BuiltinPromptAgentWorkflowName, "prompt-agent-version-1")
}

func createPromptAgentForTest(t *testing.T, mux *http.ServeMux) agentRecordDTO {
	t.Helper()
	body := `{"kind":"prompt","name":"Docs assistant","backend":"codex","behavior":{"role_name":"docs-assistant","role_create":{"description":"Docs"}},"trigger":{"source_kind":"internal"},"enabled":true}`
	rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create prompt agent status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created agentRecordDTO
	decodeJSON(t, rec.Body.Bytes(), &created)
	return created
}

func doAgentRequest(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, data []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode JSON %s: %v", string(data), err)
	}
}

func decodeListItems(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out struct {
		Data []map[string]any `json:"data"`
	}
	decodeJSON(t, data, &out)
	return out.Data
}

func listContainsID(t *testing.T, data []byte, id string) bool {
	t.Helper()
	for _, item := range decodeListItems(t, data) {
		if item["id"] == id {
			return true
		}
	}
	return false
}
