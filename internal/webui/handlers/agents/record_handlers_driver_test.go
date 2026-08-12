package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

const promptAgentDriverTestVersion = "prompt-agent-version-1"

// seedExecutablePromptAgentDriver installs the smallest active builtin fixture
// that satisfies the same on-disk availability checks as production. Prompt
// agent creation must not mistake a FleetDB-only driver row for an executable
// workflow, so success fixtures need a real staged manifest and server module.
func seedExecutablePromptAgentDriver(t *testing.T, st store.Store) {
	t.Helper()
	ctx := context.Background()
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)

	bundleRef := filepath.ToSlash(filepath.Join(
		".loom", "drivers", workflowdefs.BuiltinPromptAgentWorkflowName, promptAgentDriverTestVersion,
	))
	bundleRoot := filepath.Join(runtimeDir, filepath.FromSlash(bundleRef))
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("create prompt-agent bundle fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write prompt-agent manifest fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write prompt-agent server fixture: %v", err)
	}

	runners, err := json.Marshal([]driverpkg.DriverRunnerSpec{{
		Name:       "local-task-runner",
		Kind:       driverpkg.RunnerKindFlueWorkflow,
		Entrypoint: "local-task-runner",
	}})
	if err != nil {
		t.Fatalf("encode prompt-agent runner manifest: %v", err)
	}
	spec, ok := workflowdefs.BuiltinWorkflow(workflowdefs.BuiltinPromptAgentWorkflowName)
	if !ok {
		t.Fatal("prompt-agent builtin is not registered")
	}
	digest := workflowdefs.SourceDigest(spec.Files)
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey:    agentRecordTestWS,
		DriverID:        workflowdefs.BuiltinPromptAgentWorkflowName,
		Name:            workflowdefs.BuiltinPromptAgentWorkflowName,
		OwnerType:       domain.DriverOwnerSystem,
		ActiveVersionID: promptAgentDriverTestVersion,
		Status:          domain.DriverStatusActive,
		TrustLevel:      domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create prompt-agent driver fixture: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: agentRecordTestWS,
		VersionID:    promptAgentDriverTestVersion,
		DriverID:     workflowdefs.BuiltinPromptAgentWorkflowName,
		Version:      1,
		SourceRef:    "builtin://workflows/prompt-agent/versions/" + digest,
		SourceDigest: digest,
		BundleRef:    bundleRef,
		BundleDigest: "sha256:prompt-agent-test-bundle",
		Runtime:      "node",
		Manifest:     map[string]string{"runners": string(runners)},
		CreatedBy:    "test",
	}); err != nil {
		t.Fatalf("create prompt-agent driver version fixture: %v", err)
	}
}

func TestPromptAgentCreateWithVanishedActiveBundleFailsAtomically(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	driverBefore, err := st.Drivers().Get(ctx, agentRecordTestWS, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	versionBefore, err := st.DriverVersions().Get(ctx, agentRecordTestWS, driverBefore.ActiveVersionID)
	if err != nil {
		t.Fatalf("get prompt-agent version: %v", err)
	}
	bundleRoot := filepath.Join(os.Getenv("LOOM_WORKSPACE_RUNTIME_DIR"), filepath.FromSlash(versionBefore.BundleRef))
	if err := os.RemoveAll(bundleRoot); err != nil {
		t.Fatalf("remove staged prompt-agent bundle: %v", err)
	}
	t.Setenv("LOOM_SDK_ROOT", filepath.Join(t.TempDir(), "missing-sdk"))

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
	if !strings.Contains(rec.Body.String(), "workflow build toolchain is unavailable") ||
		!strings.Contains(rec.Body.String(), "Rolldown native binding") {
		t.Fatalf("POST /agents body = %s, want actionable unavailable-toolchain guidance", rec.Body.String())
	}

	if _, err := st.Roles().Get(ctx, agentRecordTestWS, "docs-assistant"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("role was persisted before stale-driver preflight: %v", err)
	}
	records, err := st.AgentServices().List(ctx, agentRecordTestWS, store.AgentServiceFilter{})
	if err != nil || len(records) != 0 {
		t.Fatalf("agent records after stale-driver preflight = %+v err=%v, want none", records, err)
	}
	bindings, err := st.TriggerBindings().List(ctx, agentRecordTestWS, store.TriggerBindingFilter{})
	if err != nil || len(bindings) != 0 {
		t.Fatalf("bindings after stale-driver preflight = %+v err=%v, want none", bindings, err)
	}
	driverAfter, err := st.Drivers().Get(ctx, agentRecordTestWS, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver after failure: %v", err)
	}
	if driverAfter.ActiveVersionID != driverBefore.ActiveVersionID {
		t.Fatalf("active version changed after failed preflight: got %q want %q", driverAfter.ActiveVersionID, driverBefore.ActiveVersionID)
	}
	versions, err := st.DriverVersions().List(ctx, agentRecordTestWS, store.DriverVersionFilter{
		DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
	})
	if err != nil || len(versions) != 1 || versions[0].VersionID != versionBefore.VersionID {
		t.Fatalf("driver versions after failed preflight = %+v err=%v, want only %q", versions, err, versionBefore.VersionID)
	}
}

func TestPromptAgentCreateReusesExecutableActiveBundleWithoutBuildToolchain(t *testing.T) {
	st := newAgentRecordStore(t)
	ctx := context.Background()
	driverBefore, err := st.Drivers().Get(ctx, agentRecordTestWS, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver: %v", err)
	}
	t.Setenv("LOOM_SDK_ROOT", filepath.Join(t.TempDir(), "missing-sdk"))

	body := `{
		"kind":"prompt",
		"name":"Docs assistant",
		"behavior":{"role_name":"docs-assistant","role_create":{"description":"Docs"}},
		"trigger":{"source_kind":"internal"},
		"enabled":true
	}`
	rec := doAgentRequest(t, newAgentsMux(st), http.MethodPost, "/api/workspaces/WS/agents", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /agents status = %d body=%s, want reusable active bundle to avoid rebuild", rec.Code, rec.Body.String())
	}
	driverAfter, err := st.Drivers().Get(ctx, agentRecordTestWS, workflowdefs.BuiltinPromptAgentWorkflowName)
	if err != nil {
		t.Fatalf("get prompt-agent driver after create: %v", err)
	}
	if driverAfter.ActiveVersionID != driverBefore.ActiveVersionID {
		t.Fatalf("valid active version was rebuilt: got %q want %q", driverAfter.ActiveVersionID, driverBefore.ActiveVersionID)
	}
	versions, err := st.DriverVersions().List(ctx, agentRecordTestWS, store.DriverVersionFilter{
		DriverID: workflowdefs.BuiltinPromptAgentWorkflowName,
	})
	if err != nil || len(versions) != 1 {
		t.Fatalf("driver versions after valid reuse = %+v err=%v, want exactly one", versions, err)
	}
}
