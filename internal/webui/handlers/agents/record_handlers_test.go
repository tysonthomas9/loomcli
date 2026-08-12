package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/triggerbindings"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
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
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "agt-scripted-x8", Name: "Scripted assistant",
		Kind: domain.AgentServiceKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
		DriverID: "driver-1", DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("create scripted agent service: %v", err)
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
	if kinds[agentRecordKindScripted]["id"] != "agt-scripted-x8" {
		t.Fatalf("scripted item = %+v", kinds[agentRecordKindScripted])
	}
	scriptedBehavior, ok := kinds[agentRecordKindScripted]["behavior"].(map[string]any)
	if !ok || scriptedBehavior["driver_id"] != "driver-1" || scriptedBehavior["driver_version_id"] != "version-1" {
		t.Fatalf("scripted behavior = %#v, want driver-1/version-1", kinds[agentRecordKindScripted]["behavior"])
	}
	if bindings, ok := kinds[agentRecordKindPrompt]["bindings"].([]any); !ok || len(bindings) != 1 {
		t.Fatalf("prompt bindings = %#v, want one attached binding", kinds[agentRecordKindPrompt]["bindings"])
	}
	if kinds[agentRecordKindBinding]["id"] != "legacy-review" {
		t.Fatalf("legacy binding item = %+v", kinds[agentRecordKindBinding])
	}
}

func TestUnifiedLegacyBindingFallbackSupportsDetailRenameAndDelete(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedDriverVersion(t, st, "legacy-driver", "legacy-version")
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "legacy-review", Name: "Legacy review",
		SourceKind: store.CronSourceKind, DriverID: "legacy-driver", DriverVersionID: "legacy-version",
		Schedule: "*/10 * * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("create legacy binding: %v", err)
	}
	if _, err := st.ConnectorGrants().Create(ctx, store.ConnectorGrantCreate{
		WorkspaceKey: agentRecordTestWS, GrantID: "legacy-grant", ConnectorID: "github",
		BindingID: "legacy-review", Action: "pulls.comment", ResourcePattern: "repo:o/r",
	}); err != nil {
		t.Fatalf("create legacy grant: %v", err)
	}
	mux := newAgentsMux(st)

	rec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents/legacy-review", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy GET status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got legacyBindingAgentDTO
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.ID != "legacy-review" || got.Kind != agentRecordKindBinding || got.Name != "Legacy review" {
		t.Fatalf("legacy GET = %+v", got)
	}

	rec = doAgentRequest(t, mux, http.MethodPatch, "/api/workspaces/WS/agents/legacy-review", `{"name":"Renamed review"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy PATCH status = %d body=%s", rec.Code, rec.Body.String())
	}
	binding, err := st.TriggerBindings().Get(ctx, agentRecordTestWS, "legacy-review")
	if err != nil || binding.Name != "Renamed review" {
		t.Fatalf("binding after PATCH = %+v err=%v", binding, err)
	}

	rec = doAgentRequest(t, mux, http.MethodDelete, "/api/workspaces/WS/agents/legacy-review", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy DELETE status = %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := st.TriggerBindings().Get(ctx, agentRecordTestWS, "legacy-review"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("binding after DELETE err = %v, want ErrNotFound", err)
	}
	grants, err := st.ConnectorGrants().ListByBinding(ctx, agentRecordTestWS, "legacy-review")
	if err != nil || len(grants) != 0 {
		t.Fatalf("legacy grants after DELETE = %+v err=%v", grants, err)
	}
}

func TestUnifiedLegacyBindingFallbackRejectsAttachedBinding(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	seedDriverVersion(t, st, "driver-1", "version-1")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "agt-docs", Name: "Docs",
		Kind: domain.AgentServiceKindEvent, RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "attached-binding", Name: "Attached",
		SourceKind: store.InternalSourceKind, DriverID: "driver-1", DriverVersionID: "version-1",
		TargetAgentServiceID: "agt-docs", Enabled: true,
	}); err != nil {
		t.Fatalf("create attached binding: %v", err)
	}
	mux := newAgentsMux(st)

	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		body := ""
		if method == http.MethodPatch {
			body = `{"name":"Must not apply"}`
		}
		rec := doAgentRequest(t, mux, method, "/api/workspaces/WS/agents/attached-binding", body)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s attached fallback status = %d body=%s, want 404", method, rec.Code, rec.Body.String())
		}
	}
	if _, err := st.TriggerBindings().Get(ctx, agentRecordTestWS, "attached-binding"); err != nil {
		t.Fatalf("attached binding mutated or deleted: %v", err)
	}
}

func TestUnifiedSupervisedCreateRejectsAgentRecordIDCollision(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "agt-reserved", Name: "Reserved",
		Kind: domain.AgentServiceKindEvent, RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create agent record: %v", err)
	}
	service := svcimpl.NewAgentService(nil, nil, nil, st)
	mux := http.NewServeMux()
	NewModule(service, st, nil).Register(mux)

	rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", `{"name":"agt-reserved","role_name":"task","kind":"worker","backend":"codex"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by an agent record") {
		t.Fatalf("collision status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if _, err := st.Agents().Get(ctx, agentRecordTestWS, "agt-reserved"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("colliding supervised agent persisted, err=%v", err)
	}
}

func TestUnifiedSupervisedCreateRejectsLegacyBindingIDCollision(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "task")
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "legacy-reserved", Name: "Legacy reserved",
		SourceKind: store.InternalSourceKind, DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
		DriverVersionID: "prompt-agent-version-1", Enabled: true,
	}); err != nil {
		t.Fatalf("create legacy binding: %v", err)
	}
	service := svcimpl.NewAgentService(nil, nil, nil, st)
	mux := http.NewServeMux()
	NewModule(service, st, nil).Register(mux)

	rec := doAgentRequest(t, mux, http.MethodPost, "/api/workspaces/WS/agents", `{"name":"legacy-reserved","role_name":"task","kind":"worker","backend":"codex"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already used by a legacy binding agent") {
		t.Fatalf("collision status = %d body=%s, want 409", rec.Code, rec.Body.String())
	}
	if _, err := st.Agents().Get(ctx, agentRecordTestWS, "legacy-reserved"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("colliding supervised agent persisted, err=%v", err)
	}
	if _, err := st.TriggerBindings().Get(ctx, agentRecordTestWS, "legacy-reserved"); err != nil {
		t.Fatalf("legacy binding mutated: %v", err)
	}
}

func TestUnifiedItemRoutesFailClosedOnExistingCrossKindCollision(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	seedRole(t, st, "docs-assistant")
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: agentRecordTestWS, ServiceID: "collision", Name: "Record",
		Kind: domain.AgentServiceKindEvent, RoleName: "docs-assistant",
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: agentRecordTestWS, Name: "collision", RoleName: "task",
	}); err != nil {
		t.Fatalf("create supervised agent: %v", err)
	}
	mux := newAgentsMux(st)

	listRec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents", "")
	if listRec.Code != http.StatusConflict {
		t.Fatalf("list collision status = %d body=%s, want 409", listRec.Code, listRec.Body.String())
	}
	assertAgentErrorWireResponse(t, listRec, false)
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		body := ""
		if method == http.MethodPatch {
			body = `{"name":"Must not apply"}`
		}
		rec := doAgentRequest(t, mux, method, "/api/workspaces/WS/agents/collision", body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s collision status = %d body=%s, want 409", method, rec.Code, rec.Body.String())
		}
	}
	record, err := st.AgentServices().Get(ctx, agentRecordTestWS, "collision")
	if err != nil || record.Name != "Record" || record.DeletedAt != nil {
		t.Fatalf("colliding record mutated: %+v err=%v", record, err)
	}
	if _, err := st.Agents().Get(ctx, agentRecordTestWS, "collision"); err != nil {
		t.Fatalf("colliding supervised agent mutated: %v", err)
	}
}

func TestUnifiedAgentCreateRejectsOversizedBodyWithServiceErrorEnvelope(t *testing.T) {
	rec := doAgentRequest(t, newAgentsMux(newAgentRecordStore(t)), http.MethodPost, "/api/workspaces/WS/agents", strings.Repeat("x", handler.MaxRequestBody+1))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized create status = %d body=%s, want 413", rec.Code, rec.Body.String())
	}
	assertAgentErrorWireResponse(t, rec, true)
}

func TestUnifiedRoutesFailClosedOnLegacyBindingIdentityCollision(t *testing.T) {
	for _, ownerKind := range []string{"supervised", "record"} {
		t.Run(ownerKind, func(t *testing.T) {
			st := newAgentRecordStore(t)
			ctx := context.Background()
			seedRole(t, st, "task")
			seedRole(t, st, "docs-assistant")
			if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
				WorkspaceKey: agentRecordTestWS, BindingID: "collision", Name: "Legacy binding",
				SourceKind: store.InternalSourceKind, DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
				DriverVersionID: "prompt-agent-version-1", Enabled: true,
			}); err != nil {
				t.Fatalf("create legacy binding: %v", err)
			}
			switch ownerKind {
			case "supervised":
				if _, err := st.Agents().Create(ctx, store.AgentCreate{
					WorkspaceKey: agentRecordTestWS, Name: "collision", RoleName: "task",
				}); err != nil {
					t.Fatalf("create supervised agent: %v", err)
				}
			case "record":
				if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
					WorkspaceKey: agentRecordTestWS, ServiceID: "collision", Name: "Record",
					Kind: domain.AgentServiceKindEvent, RoleName: "docs-assistant",
				}); err != nil {
					t.Fatalf("create agent record: %v", err)
				}
			}

			mux := newAgentsMux(st)
			listRec := doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents", "")
			if listRec.Code != http.StatusConflict {
				t.Fatalf("list collision status = %d body=%s, want 409", listRec.Code, listRec.Body.String())
			}
			for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
				body := ""
				if method == http.MethodPatch {
					body = `{"name":"Must not apply"}`
				}
				rec := doAgentRequest(t, mux, method, "/api/workspaces/WS/agents/collision", body)
				if rec.Code != http.StatusConflict {
					t.Fatalf("%s collision status = %d body=%s, want 409", method, rec.Code, rec.Body.String())
				}
			}

			binding, err := st.TriggerBindings().Get(ctx, agentRecordTestWS, "collision")
			if err != nil || binding.Name != "Legacy binding" {
				t.Fatalf("colliding legacy binding mutated: %+v err=%v", binding, err)
			}
			if ownerKind == "supervised" {
				if _, err := st.Agents().Get(ctx, agentRecordTestWS, "collision"); err != nil {
					t.Fatalf("colliding supervised agent mutated: %v", err)
				}
			} else {
				record, err := st.AgentServices().Get(ctx, agentRecordTestWS, "collision")
				if err != nil || record.Name != "Record" || record.DeletedAt != nil {
					t.Fatalf("colliding record mutated: %+v err=%v", record, err)
				}
			}
		})
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

func TestPromptAgentCreateWithoutBuildToolchainFailsAtomically(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: agentRecordTestWS, Name: "Test Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_SDK_ROOT", filepath.Join(runtimeDir, "missing-sdk"))

	body := `{
		"kind":"prompt",
		"name":"Docs assistant",
		"behavior":{"role_name":"docs-assistant","role_create":{"description":"Docs"}},
		"trigger":{"source_kind":"internal"},
		"enabled":true
	}`
	rec := doAgentRequest(t, newAgentsMux(st), http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /agents status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "workflow build toolchain is unavailable") {
		t.Fatalf("POST /agents body = %s, want actionable toolchain error", rec.Body.String())
	}
	if _, err := st.Roles().Get(ctx, agentRecordTestWS, "docs-assistant"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("role was persisted before driver preflight: %v", err)
	}
	records, err := st.AgentServices().List(ctx, agentRecordTestWS, store.AgentServiceFilter{})
	if err != nil || len(records) != 0 {
		t.Fatalf("agent records after unavailable preflight = %+v err=%v, want none", records, err)
	}
	bindings, err := st.TriggerBindings().List(ctx, agentRecordTestWS, store.TriggerBindingFilter{})
	if err != nil || len(bindings) != 0 {
		t.Fatalf("bindings after unavailable preflight = %+v err=%v, want none", bindings, err)
	}
}

func TestPromptAgentCreateWithMissingRolldownNativeBindingReturns503Atomically(t *testing.T) {
	st := memstore.New()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: agentRecordTestWS, Name: "Test Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	configureMissingRolldownBuild(t)

	body := `{
		"kind":"prompt",
		"name":"Docs assistant",
		"behavior":{"role_name":"docs-assistant","role_create":{"description":"Docs"}},
		"trigger":{"source_kind":"internal"},
		"enabled":true
	}`
	rec := doAgentRequest(t, newAgentsMux(st), http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST /agents status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "target-platform Rolldown native binding") {
		t.Fatalf("POST /agents body = %s, want native-binding remediation", rec.Body.String())
	}
	if _, err := st.Roles().Get(ctx, agentRecordTestWS, "docs-assistant"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("role was persisted before failed driver build: %v", err)
	}
	records, err := st.AgentServices().List(ctx, agentRecordTestWS, store.AgentServiceFilter{})
	if err != nil || len(records) != 0 {
		t.Fatalf("agent records after failed driver build = %+v err=%v, want none", records, err)
	}
	bindings, err := st.TriggerBindings().List(ctx, agentRecordTestWS, store.TriggerBindingFilter{})
	if err != nil || len(bindings) != 0 {
		t.Fatalf("bindings after failed driver build = %+v err=%v, want none", bindings, err)
	}
	drivers, err := st.Drivers().List(ctx, agentRecordTestWS, store.DriverFilter{})
	if err != nil || len(drivers) != 0 {
		t.Fatalf("drivers after failed driver build = %+v err=%v, want none", drivers, err)
	}
	versions, err := st.DriverVersions().List(ctx, agentRecordTestWS, store.DriverVersionFilter{})
	if err != nil || len(versions) != 0 {
		t.Fatalf("driver versions after failed driver build = %+v err=%v, want none", versions, err)
	}
}

func configureMissingRolldownBuild(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	sdkRoot := filepath.Join(root, "sdk")
	runtimeRoot := filepath.Join(root, "runtime")
	for _, path := range []string{
		sdkRoot,
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	for path, name := range map[string]string{sdkRoot: "@loom/sdk", runtimeRoot: "@flue/runtime"} {
		if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"name":"`+name+`"}`), 0o644); err != nil {
			t.Fatalf("write package.json for %s: %v", path, err)
		}
	}
	flue := filepath.Join(root, "missing-native-flue.sh")
	if err := os.WriteFile(flue, []byte("#!/bin/sh\necho \"Error: Cannot find module './rolldown-binding.linux-arm64-gnu.node'\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write failing fake flue: %v", err)
	}
	command, err := json.Marshal([]string{flue})
	if err != nil {
		t.Fatalf("encode failing fake flue command: %v", err)
	}
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", filepath.Join(root, "runtime-data"))
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
	t.Setenv("DAYTONA_SDK_ROOT", "")
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", string(command))
	t.Setenv("LOOM_REAL_FLUE_CMD", "")
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
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: agentRecordTestWS, BindingID: "legacy-binding", Name: "Legacy binding",
		SourceKind: store.InternalSourceKind, DriverID: binding.DriverID, DriverVersionID: binding.DriverVersionID,
		Enabled: true,
	}); err != nil {
		t.Fatalf("create legacy binding: %v", err)
	}
	time.Sleep(time.Millisecond)
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: agentRecordTestWS, RunID: "run-legacy", DriverID: binding.DriverID,
		DriverVersionID: binding.DriverVersionID, TriggerBindingID: "legacy-binding",
	}); err != nil {
		t.Fatalf("create legacy run: %v", err)
	}
	legacyTarget := created.ID
	if _, err := st.TriggerBindings().Update(ctx, agentRecordTestWS, "legacy-binding", store.TriggerBindingUpdate{TargetAgentServiceID: &legacyTarget}); err != nil {
		t.Fatalf("attach legacy binding: %v", err)
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
	if len(out.Runs) != 3 || out.Runs[0].RunID != "run-legacy" || out.Runs[1].RunID != "run-new" || out.Runs[2].RunID != "run-old" {
		t.Fatalf("runs = %+v, want legacy compatibility plus deduplicated direct runs", out.Runs)
	}

	rec = doAgentRequest(t, mux, http.MethodDelete, "/api/workspaces/WS/agents/"+created.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = doAgentRequest(t, mux, http.MethodGet, "/api/workspaces/WS/agents/"+created.ID+"/runs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("archived runs status = %d body=%s", rec.Code, rec.Body.String())
	}
	out = struct {
		AgentID string              `json:"agent_id"`
		Runs    []*domain.DriverRun `json:"runs"`
	}{}
	decodeJSON(t, rec.Body.Bytes(), &out)
	if len(out.Runs) != 2 || out.Runs[0].RunID != "run-new" || out.Runs[1].RunID != "run-old" {
		t.Fatalf("archived runs = %+v, want durable run-new then run-old", out.Runs)
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
	seedExecutablePromptAgentDriver(t, st)
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

func assertAgentErrorWireResponse(t *testing.T, rec *httptest.ResponseRecorder, wantKind bool) {
	t.Helper()
	var body map[string]any
	decodeJSON(t, rec.Body.Bytes(), &body)
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("agent error response has no string error: %s", rec.Body.String())
	}
	if _, ok := body["success"]; ok {
		t.Fatalf("agent error response unexpectedly contains success: %s", rec.Body.String())
	}
	_, hasKind := body["kind"].(string)
	if hasKind != wantKind {
		t.Fatalf("agent error response kind presence = %v, want %v: %s", hasKind, wantKind, rec.Body.String())
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
