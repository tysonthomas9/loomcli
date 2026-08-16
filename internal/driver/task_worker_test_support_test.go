package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	workspaceowner "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type driverRunFixtureOptions struct {
	WorkspaceKey     string
	DriverID         string
	DriverVersionID  string
	EpicID           string
	RunID            string
	IdempotencyKey   string
	Entrypoint       string
	SourceKind       string
	SourceRef        string
	TriggerBindingID string
	Payload          json.RawMessage
}

func createDriverRunFixture(ctx context.Context, st *memstore.Store, options driverRunFixtureOptions) (*execution.DriverRunRecord, error) {
	versionID := options.DriverVersionID
	if versionID == "" {
		driver, err := st.Drivers().Get(ctx, options.WorkspaceKey, options.DriverID)
		if err != nil {
			return nil, err
		}
		versionID = driver.ActiveVersionID
	}
	entrypoint := options.Entrypoint
	if entrypoint == "" {
		entrypoint = EntrypointRun
	}
	sourceKind := options.SourceKind
	if sourceKind == "" {
		sourceKind = "test"
	}
	sourceRef := options.SourceRef
	if sourceRef == "" {
		sourceRef = "test-fixture"
	}
	payload := append(json.RawMessage(nil), options.Payload...)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	return st.DriverRuns().Create(ctx, execution.DriverRunCreate{
		WorkspaceKey: options.WorkspaceKey, RunID: options.RunID,
		DriverID: options.DriverID, DriverVersionID: versionID,
		Entrypoint: entrypoint, SourceKind: sourceKind, SourceRef: sourceRef,
		EpicID: options.EpicID, TriggerBindingID: options.TriggerBindingID,
		IdempotencyKey: options.IdempotencyKey, Payload: payload,
	})
}

func setupRunningDriverRun(t *testing.T) (context.Context, *memstore.Store, *execution.DriverRunRecord) {
	t.Helper()
	ctx, st, run := setupQueuedDriverRun(t)
	registerTaskWorkerNode(t, ctx, st, "node-1", []string{"codex-default", "local-noop", "noop", "remote-sandbox", "daytona", "flue-local"}, []string{"git", "shell"})
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	return ctx, st, claimed
}

func setupQueuedDriverRun(t *testing.T) (context.Context, *memstore.Store, *execution.DriverRunRecord) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, workspaceowner.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := SeedFlueDriverFixture(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		CreatedBy:    "tester",
		Activate:     true,
		RunnerSpecs: []DriverRunnerSpec{
			{Name: "local-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "local-task-runner"},
			{Name: "daytona-task-runner", Kind: RunnerKindFlueWorkflow, Entrypoint: "daytona-task-runner"},
		},
	})
	if err != nil {
		t.Fatalf("SeedFlueDriverFixture: %v", err)
	}
	run, err := createDriverRunFixture(ctx, st, driverRunFixtureOptions{
		WorkspaceKey: "TEST",
		DriverID:     registered.Driver.DriverID,
		EpicID:       "TEST-EPIC",
		RunID:        "run-1",
	})
	if err != nil {
		t.Fatalf("create driver-run fixture: %v", err)
	}
	return ctx, st, run
}

func registerTaskWorkerNode(t *testing.T, ctx context.Context, st *memstore.Store, nodeID string, providers, capabilities []string) {
	t.Helper()
	nodeCapabilities := append([]string{"driver-runner", "task-runner", "flue-local"}, providers...)
	nodeCapabilities = append(nodeCapabilities, capabilities...)
	if _, err := st.Nodes().Create(ctx, execution.NodeCreate{
		WorkspaceKey:    "TEST",
		NodeID:          nodeID,
		RuntimeProvider: execution.RuntimeProviderLocal,
		Capabilities:    normalizeStringList(nodeCapabilities),
		DrainState:      execution.WorkerNodeActive,
		TTL:             time.Minute,
	}); err != nil && !errors.Is(err, persistence.ErrAlreadyExists) {
		t.Fatalf("Create task worker node %s: %v", nodeID, err)
	}
}

type recordingTaskExecutor struct {
	req    TaskExecRequest
	result TaskExecResult
	err    error
}

func (executor *recordingTaskExecutor) ExecuteTask(_ context.Context, request TaskExecRequest) (TaskExecResult, error) {
	executor.req = request
	return executor.result, executor.err
}
