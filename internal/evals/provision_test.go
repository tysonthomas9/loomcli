package evals

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestEnsureEvalCronCreatesEnabledHourlyBinding(t *testing.T) {
	ctx := context.Background()
	st, _ := evalProvisionFixture(t, "version-1")

	result, err := EnsureEvalCron(ctx, st, "WS", "")
	if err != nil {
		t.Fatalf("EnsureEvalCron: %v", err)
	}
	if result.Action != "created" || !result.Enabled || result.Schedule != DefaultEvalCronSchedule || result.RouteKey != EvalCronRouteKey {
		t.Fatalf("result = %+v", result)
	}
	binding, err := st.TriggerBindings().GetByRouteKey(ctx, "WS", EvalCronRouteKey)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if !binding.Enabled || binding.Schedule != DefaultEvalCronSchedule || binding.DriverID != workflowdefs.BuiltinSessionEvalAgentWorkflowName || binding.DriverVersionID != "version-1" {
		t.Fatalf("binding = %+v", binding)
	}
}

func TestEnsureEvalCronRerunPreservesDisabledAndUpdatesSchedule(t *testing.T) {
	ctx := context.Background()
	st, _ := evalProvisionFixture(t, "version-1")
	if _, err := EnsureEvalCron(ctx, st, "WS", "0 * * * *"); err != nil {
		t.Fatalf("first EnsureEvalCron: %v", err)
	}
	binding, err := st.TriggerBindings().GetByRouteKey(ctx, "WS", EvalCronRouteKey)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	disabled := false
	if _, err := st.TriggerBindings().Update(ctx, "WS", binding.BindingID, store.TriggerBindingUpdate{Enabled: &disabled}); err != nil {
		t.Fatalf("disable binding: %v", err)
	}

	result, err := EnsureEvalCron(ctx, st, "WS", "*/5 * * * *")
	if err != nil {
		t.Fatalf("second EnsureEvalCron: %v", err)
	}
	if result.Action != "updated" || result.Enabled {
		t.Fatalf("result = %+v, want updated disabled", result)
	}
	binding, err = st.TriggerBindings().GetByRouteKey(ctx, "WS", EvalCronRouteKey)
	if err != nil {
		t.Fatalf("get updated binding: %v", err)
	}
	if binding.Enabled || binding.Schedule != "*/5 * * * *" {
		t.Fatalf("binding = %+v, want disabled with new schedule", binding)
	}
}

func TestEnsureEvalCronRepinsDriverVersion(t *testing.T) {
	ctx := context.Background()
	st, workDir := evalProvisionFixture(t, "version-1")
	if _, err := EnsureEvalCron(ctx, st, "WS", "0 * * * *"); err != nil {
		t.Fatalf("first EnsureEvalCron: %v", err)
	}
	seedActiveSessionEvalBuiltin(t, st, workDir, "version-2", 2)

	result, err := EnsureEvalCron(ctx, st, "WS", "15 * * * *")
	if err != nil {
		t.Fatalf("second EnsureEvalCron: %v", err)
	}
	if result.DriverVersionID != "version-2" || result.Schedule != "15 * * * *" {
		t.Fatalf("result = %+v, want repinned version-2 and updated schedule", result)
	}
	binding, err := st.TriggerBindings().GetByRouteKey(ctx, "WS", EvalCronRouteKey)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	if binding.DriverVersionID != "version-2" {
		t.Fatalf("binding.DriverVersionID = %q, want version-2", binding.DriverVersionID)
	}
}

func TestEnsureEvalCronBindingDispatchesSessionEvalWorkflow(t *testing.T) {
	ctx := context.Background()
	st, _ := evalProvisionFixture(t, "version-1")
	if _, err := EnsureEvalCron(ctx, st, "WS", "* * * * *"); err != nil {
		t.Fatalf("EnsureEvalCron: %v", err)
	}
	scheduler := &trigger.CronScheduler{Store: st, WorkspaceKey: "WS"}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	if result, err := scheduler.RunOnce(ctx, now); err != nil || result.Fired != 0 {
		t.Fatalf("first RunOnce = %+v err=%v, want primed/no fire", result, err)
	}
	if result, err := scheduler.RunOnce(ctx, now.Add(time.Minute)); err != nil || result.Fired != 1 {
		t.Fatalf("second RunOnce = %+v err=%v, want one fire", result, err)
	}
	runs, err := st.DriverRuns().List(ctx, "WS", store.DriverRunFilter{DriverID: workflowdefs.BuiltinSessionEvalAgentWorkflowName})
	if err != nil {
		t.Fatalf("list driver runs: %v", err)
	}
	if len(runs) != 1 || runs[0].DriverVersionID != "version-1" || runs[0].SourceKind != trigger.CronSourceKind {
		t.Fatalf("runs = %+v, want one cron-launched session-eval-agent run", runs)
	}
}

func evalProvisionFixture(t *testing.T, versionID string) (*memstore.Store, string) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", workDir)
	seedActiveSessionEvalBuiltin(t, st, workDir, versionID, 1)
	return st, workDir
}

func seedActiveSessionEvalBuiltin(t *testing.T, st *memstore.Store, workDir, versionID string, n int) {
	t.Helper()
	ctx := context.Background()
	name := workflowdefs.BuiltinSessionEvalAgentWorkflowName
	if _, err := st.Drivers().Get(ctx, "WS", name); err != nil {
		if _, err := st.Drivers().Create(ctx, store.DriverCreate{
			WorkspaceKey: "WS",
			DriverID:     name,
			Name:         name,
			Status:       domain.DriverStatusActive,
			TrustLevel:   domain.DriverTrustTrusted,
		}); err != nil {
			t.Fatalf("create driver: %v", err)
		}
	}
	bundleRel := filepath.ToSlash(filepath.Join(".loom", "workflow-builds", versionID))
	bundleRoot := filepath.Join(workDir, filepath.FromSlash(bundleRel))
	if err := os.MkdirAll(filepath.Join(bundleRoot, "dist"), 0o755); err != nil {
		t.Fatalf("create bundle root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, "dist", "server.mjs"), []byte("export {};\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	spec, ok := workflowdefs.BuiltinWorkflow(name)
	if !ok {
		t.Fatal("session-eval-agent builtin missing")
	}
	runners, err := json.Marshal([]driverpkg.DriverRunnerSpec{{
		Name:       workflowdefs.BuiltinSessionEvalTaskRunnerName,
		Kind:       driverpkg.RunnerKindFlueWorkflow,
		Entrypoint: workflowdefs.BuiltinSessionEvalTaskRunnerName,
	}})
	if err != nil {
		t.Fatalf("marshal runners: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        versionID,
		DriverID:         name,
		Version:          n,
		SourceRef:        "builtin://workflows/" + name + "/versions/" + versionID,
		SourceDigest:     workflowdefs.SourceDigest(spec.Files),
		BundleRef:        bundleRel,
		BundleDigest:     "sha256:" + versionID,
		Runtime:          driverpkg.RuntimeFlueNode,
		Manifest:         map[string]string{"workflow_name": name, "runners": string(runners)},
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        "system",
	}); err != nil {
		t.Fatalf("create driver version %s: %v", versionID, err)
	}
	if _, err := st.Drivers().Update(ctx, "WS", name, store.DriverUpdate{ActiveVersionID: &versionID}); err != nil {
		t.Fatalf("activate version %s: %v", versionID, err)
	}
}
