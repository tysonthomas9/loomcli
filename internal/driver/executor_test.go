package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestExecutorRunOnceClaimsVerifiesAndFinishes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	source := writeWorkflow(t, root, "complete-epic.ts", `export const run = defineDriver({
  name: "complete-epic",
  async run(ctx) {
    return ctx.run.complete({ summary: "done" });
  },
	});
`)
	published, err := PublishWorkflow(ctx, st, PublishOptions{WorkspaceKey: "TEST", WorkDir: root, SourcePath: source, CreatedBy: "tester", FlueCommand: fakeFlueCommand(t)})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey:   "TEST",
		DriverID:       published.Driver.DriverID,
		EpicID:         "TEST-1",
		RunID:          "run-1",
		IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "driver completed"}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            runner,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Claimed == nil || result.Claimed.Status != domain.DriverRunRunning || result.Claimed.NodeID != "node-1" {
		t.Fatalf("claimed = %+v, want running node-1", result.Claimed)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunCompleted || result.Final.Summary != "driver completed" {
		t.Fatalf("final = %+v, want completed summary", result.Final)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
	if runner.req.Run.RunID != run.RunID || runner.req.Version.VersionID != published.Version.VersionID {
		t.Fatalf("runner req = %+v version=%+v, want pinned run/version", runner.req.Run, runner.req.Version)
	}
	if _, err := os.Stat(runner.req.WorkflowPath); err != nil {
		t.Fatalf("runner workflow path missing: %v", err)
	}
	if _, err := os.Stat(runner.req.ServerPath); err != nil {
		t.Fatalf("runner server path missing: %v", err)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", "run-1")
	if err != nil {
		t.Fatalf("Get stored run: %v", err)
	}
	if stored.Status != domain.DriverRunCompleted || stored.FencingToken == 0 {
		t.Fatalf("stored run = %+v, want completed with fencing token", stored)
	}
}

func TestExecutorRunOnceFailsTamperedBundleBeforeRunner(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	source := writeWorkflow(t, root, "complete-epic.ts", `export async function run(ctx) {
  return ctx.run.complete({ summary: "done" });
}
`)
	published, err := PublishWorkflow(ctx, st, PublishOptions{WorkspaceKey: "TEST", WorkDir: root, SourcePath: source, CreatedBy: "tester", FlueCommand: fakeFlueCommand(t)})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(published.Version.BundleRef), filepath.FromSlash(published.Version.Manifest["source_bundle_ref"])), []byte("export async function run() { return {}; }\n"), 0o644); err != nil {
		t.Fatalf("tamper bundle: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: published.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted}}
	result, err := (&Executor{
		Store:             st,
		WorkspaceKey:      "TEST",
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            runner,
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0 for tampered bundle", runner.calls)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunFailed || result.Final.ErrorClass != "bundle_verification" {
		t.Fatalf("final = %+v, want failed bundle_verification", result.Final)
	}
}

func TestNodeRunnerRunsBuiltFlueServer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	t.Setenv("LOOM_FLEET_DB_URL", "https://fleet.invalid")
	t.Setenv("LOOM_FLEET_DB_API_KEY", "broad-secret")
	t.Setenv("LOOM_TASK_RUN_LEASE_TOKEN", "task-run-token")
	t.Setenv("OPENAI_API_KEY", "model-secret")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret")
	t.Setenv("GITHUB_TOKEN", "github-secret")

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	source := writeWorkflow(t, root, "complete-epic.ts", `export async function run(ctx) {
  return ctx.run.complete({ summary: "done" });
}
`)
	published, err := PublishWorkflow(ctx, st, PublishOptions{WorkspaceKey: "TEST", WorkDir: root, SourcePath: source, CreatedBy: "tester", FlueCommand: fakeFlueCommand(t)})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: published.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	req, err := loadRunRequest(ctx, root, claimed, st)
	if err != nil {
		t.Fatalf("loadRunRequest: %v", err)
	}
	result, err := (NodeRunner{}).Run(ctx, req)
	if err != nil {
		t.Fatalf("NodeRunner.Run: %v", err)
	}
	if result.Status != domain.DriverRunCompleted || result.Summary != "fake flue" {
		t.Fatalf("result = %+v, want completed fake flue", result)
	}
}

func TestExecutorScansWorkspacesAndReportsNoQueuedRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "EMPTY", Name: "empty"}); err != nil {
		t.Fatalf("Create EMPTY workspace: %v", err)
	}
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create TEST workspace: %v", err)
	}
	source := writeWorkflow(t, root, "complete-epic.ts", `export async function run(ctx) {
  return ctx.run.complete({ summary: "done" });
}
`)
	published, err := PublishWorkflow(ctx, st, PublishOptions{WorkspaceKey: "TEST", WorkDir: root, SourcePath: source, CreatedBy: "tester", FlueCommand: fakeFlueCommand(t)})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: published.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	result, err := (&Executor{
		Store:             st,
		WorkDir:           root,
		NodeID:            "node-1",
		LeaseID:           "lease-1",
		Runner:            &recordingRunner{result: RunResult{Status: domain.DriverRunCompleted, Summary: "done"}},
		HeartbeatInterval: -1,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce scan workspaces: %v", err)
	}
	if result.Final == nil || result.Final.WorkspaceKey != "TEST" {
		t.Fatalf("final = %+v, want TEST workspace run", result.Final)
	}
	if _, err := (&Executor{Store: st, WorkDir: root, HeartbeatInterval: -1}).RunOnce(ctx); !errors.Is(err, ErrNoQueuedRun) {
		t.Fatalf("RunOnce after drain err = %v, want ErrNoQueuedRun", err)
	}
}

type recordingRunner struct {
	calls  int
	req    RunRequest
	result RunResult
	err    error
}

func (r *recordingRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	r.calls++
	r.req = req
	return r.result, r.err
}
