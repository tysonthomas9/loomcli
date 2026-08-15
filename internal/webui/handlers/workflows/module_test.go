package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agentprovision"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskrunlogs"
	workflowdefs "github.com/tysonthomas9/loomcli/internal/workflows"
)

func TestGetDriverRunLogHappyMissingTruncatedAndRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	for _, runID := range []string{"run-happy", "run-missing", "run-large"} {
		if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
			WorkspaceKey: "TEST", RunID: runID, DriverID: "demo", DriverVersionID: "version-1",
		}); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
	}
	happyContent := "===== stdout =====\nworkflow output\n\n===== stderr =====\n"
	happyRef, err := taskrunlogs.PutRun(ctx, st, "TEST", "run-happy", happyContent)
	if err != nil {
		t.Fatalf("PutRun happy: %v", err)
	}
	tail := strings.Repeat("z", 1<<20)
	largeRef, err := taskrunlogs.PutRun(ctx, st, "TEST", "run-large", "discarded"+tail)
	if err != nil {
		t.Fatalf("PutRun large: %v", err)
	}
	finishDriverRunWithLogRef(t, st, "run-happy", happyRef)
	finishDriverRunWithLogRef(t, st, "run-large", largeRef)
	module := NewModule(st)

	t.Run("happy", func(t *testing.T) {
		rec := getDriverRunLog(t, module, "run-happy")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response testPersistedLogResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Success || response.Data.Content != happyContent || response.Data.ModifiedAt.IsZero() || response.Data.Truncated {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("missing", func(t *testing.T) {
		rec := getDriverRunLog(t, module, "run-missing")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "run log is not available") {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("truncated", func(t *testing.T) {
		rec := getDriverRunLog(t, module, "run-large")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response testPersistedLogResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Data.Truncated || response.Data.Content != tail {
			t.Fatalf("truncated = %v content bytes = %d, want true/%d", response.Data.Truncated, len(response.Data.Content), len(tail))
		}
	})

	for _, runID := range []string{"../evil", "..\\evil", "nested/run"} {
		t.Run("reject "+runID, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("ws", "TEST")
			req.SetPathValue("runId", runID)
			rec := httptest.NewRecorder()
			module.getRunLog(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid run id") {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

type testPersistedLogResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Content    string    `json:"content"`
		ModifiedAt time.Time `json:"modifiedAt"`
		Truncated  bool      `json:"truncated"`
	} `json:"data"`
}

func finishDriverRunWithLogRef(t testing.TB, st store.Store, runID, ref string) {
	t.Helper()
	claimed, err := st.DriverRuns().Claim(context.Background(), "TEST", runID, "node-"+runID, "lease-"+runID)
	if err != nil {
		t.Fatalf("claim run %s: %v", runID, err)
	}
	if _, err := st.DriverRuns().Finish(context.Background(), "TEST", runID, store.DriverRunFinish{
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
		Status:       domain.DriverRunCompleted,
		Output:       map[string]string{"logs_ref": ref},
	}); err != nil {
		t.Fatalf("finish run %s: %v", runID, err)
	}
}

func getDriverRunLog(t *testing.T, module *Module, runID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	module.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/runs/"+runID+"/log", nil))
	return rec
}

func TestCreateWorkflowRunPassesRawPayload(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(`{"nested":{"ok":true},"items":[1,2]}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("get stored run: %v", err)
	}
	if string(stored.Payload) != `{"nested":{"ok":true},"items":[1,2]}` {
		t.Fatalf("payload = %s, want raw request JSON", stored.Payload)
	}
}

func TestCreateWorkflowRunRegistersBuiltinEpicRunner(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.DriverID != BuiltinEpicRunnerWorkflowName || string(run.Payload) != `{"epicId":"EPIC-1","requestedBy":"ui"}` {
		t.Fatalf("run = %+v payload=%s, want built-in epic runner with raw payload", run, run.Payload)
	}
	driverRecord, err := st.Drivers().Get(ctx, "TEST", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get built-in driver: %v", err)
	}
	if driverRecord.Status != domain.DriverStatusActive || driverRecord.ActiveVersionID == "" {
		t.Fatalf("driver = %+v, want active built-in driver", driverRecord)
	}
	version, err := st.DriverVersions().Get(ctx, "TEST", driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get built-in version: %v", err)
	}
	if !strings.HasPrefix(version.SourceRef, "builtin://workflows/"+BuiltinEpicRunnerWorkflowName+"/versions/") || version.CreatedBy != "system" {
		t.Fatalf("version = %+v, want system built-in source", version)
	}
}

func TestCreateScoutWorkflowRunProvisionsAndAttributesAgentService(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinScoutWorkflowName, stringsReader(`{"requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.AgentServiceID != "scout" || run.DriverID != BuiltinScoutWorkflowName {
		t.Fatalf("run = %#v, want scout service attribution", run)
	}
	stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("get stored run: %v", err)
	}
	if stored.AgentServiceID != "scout" {
		t.Fatalf("stored AgentServiceID = %q, want scout", stored.AgentServiceID)
	}
	svc, err := st.AgentServices().Get(ctx, "TEST", "scout")
	if err != nil {
		t.Fatalf("get scout service: %v", err)
	}
	if svc.RoleName != scriptedroles.ScoutRoleName || svc.DriverID != "" || svc.DriverVersionID != "" {
		t.Fatalf("service = %#v, want role_name=scout and empty driver refs", svc)
	}
	role, err := st.Roles().Get(ctx, "TEST", scriptedroles.ScoutRoleName)
	if err != nil {
		t.Fatalf("get scout role: %v", err)
	}
	if role.Kind != domain.RoleKindWorker || strings.TrimSpace(role.PromptFile) == "" || strings.TrimSpace(role.Prompt) != "" {
		t.Fatalf("role = %#v, want worker with a PromptFile seed", role)
	}
	binding, err := st.TriggerBindings().Get(ctx, "TEST", "binding-cron-scout-weekly")
	if err != nil {
		t.Fatalf("get scout binding: %v", err)
	}
	if binding.TargetAgentServiceID != "scout" || binding.RouteKey != "cron.scout.weekly" ||
		binding.DriverID != run.DriverID || binding.DriverVersionID != run.DriverVersionID {
		t.Fatalf("binding = %#v, want attached scout cron", binding)
	}
}

func TestCreateWorkflowRunUsesRequestedAgentServiceInstance(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	defaultReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinScoutWorkflowName, stringsReader(`{"requestedBy":"ui"}`))
	defaultRec := httptest.NewRecorder()
	mux.ServeHTTP(defaultRec, defaultReq)
	if defaultRec.Code != http.StatusAccepted {
		t.Fatalf("default status = %d body=%s", defaultRec.Code, defaultRec.Body.String())
	}
	if _, err := st.AgentServices().Create(ctx, store.AgentServiceCreate{
		WorkspaceKey: "TEST", ServiceID: "scout-west", Name: "Scout West",
		TriggerKind: domain.AgentServiceTriggerKindCron, DesiredState: domain.AgentServiceDesiredRunning,
		RoleName: scriptedroles.ScoutRoleName,
	}); err != nil {
		t.Fatalf("create requested instance: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinScoutWorkflowName,
		stringsReader(`{"agentServiceId":"scout-west","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.AgentServiceID != "scout-west" || run.DriverID != BuiltinScoutWorkflowName {
		t.Fatalf("run = %#v", run)
	}
}

func TestCreateScoutWorkflowRunLeavesArchivedDefaultUnattributed(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	firstReq := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinScoutWorkflowName, stringsReader(`{"requestedBy":"ui"}`))
	firstRec := httptest.NewRecorder()
	mux.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusAccepted {
		t.Fatalf("initial status = %d body=%s", firstRec.Code, firstRec.Body.String())
	}
	if err := agentprovision.DeleteAgentInstance(ctx, st, "TEST", scriptedroles.ScoutRoleName); err != nil {
		t.Fatalf("delete default scout instance: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinScoutWorkflowName, stringsReader(`{"requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	var run domain.DriverRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.AgentServiceID != "" || run.DriverID != BuiltinScoutWorkflowName {
		t.Fatalf("run = %#v, want unattributed scout workflow run", run)
	}
	services, err := st.AgentServices().List(ctx, "TEST", store.AgentServiceFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list agent services: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != scriptedroles.ScoutRoleName || services[0].DeletedAt == nil {
		t.Fatalf("services = %#v, want one archived scout tombstone", services)
	}
}

func TestCreateWorkflowRunRejectsMissingOrMismatchedAgentService(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		st := memstore.New()
		mux := http.NewServeMux()
		NewModule(st).Register(mux)
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/scout", stringsReader(`{"agentServiceId":"missing"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "agent service not found") {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("workflow mismatch", func(t *testing.T) {
		st := memstore.New()
		if _, err := st.Roles().Create(t.Context(), store.RoleCreate{
			WorkspaceKey: "TEST", Name: scriptedroles.EpicRunnerRoleName, Kind: string(domain.RoleKindWorker),
		}); err != nil {
			t.Fatalf("create role: %v", err)
		}
		if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
			WorkspaceKey: "TEST", ServiceID: "epic-one", Name: "Epic one",
			TriggerKind: domain.AgentServiceTriggerKindEvent, DesiredState: domain.AgentServiceDesiredRunning,
			RoleName: scriptedroles.EpicRunnerRoleName,
		}); err != nil {
			t.Fatalf("create service: %v", err)
		}
		mux := http.NewServeMux()
		NewModule(st).Register(mux)
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/scout", stringsReader(`{"agentServiceId":"epic-one"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "does not run workflow") {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestCreateWorkflowRunRefreshesStaleBuiltinRunnerManifest(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	installFakeFlueBuild(t)
	t.Chdir(t.TempDir())

	spec, ok := workflowdefs.BuiltinWorkflow(BuiltinEpicRunnerWorkflowName)
	if !ok {
		t.Fatal("epic-runner builtin missing")
	}
	digest := workflowdefs.SourceDigest(spec.Files)
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey:    "TEST",
		DriverID:        BuiltinEpicRunnerWorkflowName,
		Name:            BuiltinEpicRunnerWorkflowName,
		OwnerType:       domain.DriverOwnerUser,
		ActiveVersionID: "stale-version",
		Status:          domain.DriverStatusActive,
		TrustLevel:      domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create stale built-in driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "stale-version",
		DriverID:         BuiltinEpicRunnerWorkflowName,
		Version:          1,
		SourceRef:        "builtin://workflows/" + BuiltinEpicRunnerWorkflowName + "/versions/" + digest,
		SourceDigest:     digest,
		BundleDigest:     "sha256:stale",
		Runtime:          driver.RuntimeFlueNode,
		Manifest:         map[string]string{"workflow_name": BuiltinEpicRunnerWorkflowName},
		ValidationStatus: domain.DriverVersionValidationPassed,
		CreatedBy:        "system",
	}); err != nil {
		t.Fatalf("create stale built-in version: %v", err)
	}

	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/"+BuiltinEpicRunnerWorkflowName, stringsReader(`{"epicId":"EPIC-1","requestedBy":"ui"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
	}
	driverRecord, err := st.Drivers().Get(ctx, "TEST", BuiltinEpicRunnerWorkflowName)
	if err != nil {
		t.Fatalf("get refreshed built-in driver: %v", err)
	}
	if driverRecord.ActiveVersionID == "stale-version" {
		t.Fatalf("active version was not refreshed")
	}
	version, err := st.DriverVersions().Get(ctx, "TEST", driverRecord.ActiveVersionID)
	if err != nil {
		t.Fatalf("get refreshed built-in version: %v", err)
	}
	if !strings.Contains(version.Manifest["runners"], "local-task-runner") {
		t.Fatalf("refreshed manifest runners = %q, want local-task-runner", version.Manifest["runners"])
	}
}

// TestCreateWorkflowRunPromotesPayloadEpicID proves the HTTP-triggered run
// path mirrors the `loom epic run` CLI: payload.epicId is promoted onto the
// DriverRun. Without it, terminal task transitions never fire the lead-task
// outbox (createLeadTaskOutbox gates on the run's EpicID), so webhook/HTTP
// epics silently skip lead notifications. The outbox assertion is end-to-end:
// a row only appears when createWorkflowRun set EpicID on the run.
func TestCreateWorkflowRunPromotesPayloadEpicID(t *testing.T) {
	cases := []struct {
		name       string
		payload    string
		wantEpicID string
		leadEpicID string // "" means no lead agent is bound
		wantOutbox int
	}{
		{
			name:       "epicId with lead bound creates lead-task outbox",
			payload:    `{"epicId":"EPIC-42","requestedBy":"webhook"}`,
			wantEpicID: "EPIC-42",
			leadEpicID: "EPIC-42",
			wantOutbox: 1,
		},
		{
			name:       "epicId without lead skips outbox",
			payload:    `{"epicId":"EPIC-42"}`,
			wantEpicID: "EPIC-42",
			leadEpicID: "",
			wantOutbox: 0,
		},
		{
			name:       "no epicId leaves run unbound and skips outbox",
			payload:    `{"requestedBy":"webhook"}`,
			wantEpicID: "",
			leadEpicID: "EPIC-42",
			wantOutbox: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := seededWorkflowStore(t, ctx)
			mux := http.NewServeMux()
			NewModule(st).Register(mux)

			req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo", stringsReader(tc.payload))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d body=%s, want 202", rec.Code, rec.Body.String())
			}
			var run domain.DriverRun
			if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
				t.Fatalf("decode run: %v", err)
			}
			stored, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
			if err != nil {
				t.Fatalf("get stored run: %v", err)
			}
			if stored.EpicID != tc.wantEpicID {
				t.Fatalf("stored run EpicID = %q, want %q", stored.EpicID, tc.wantEpicID)
			}

			if tc.leadEpicID != "" {
				bindWorkflowEpicLead(t, ctx, st, "epic-lead-1", tc.leadEpicID)
			}
			driveTerminalTaskRun(t, ctx, st, stored.RunID)

			rows := listWorkflowOutboxRows(t, ctx, st)
			if len(rows) != tc.wantOutbox {
				t.Fatalf("outbox rows = %d (%+v), want %d", len(rows), rows, tc.wantOutbox)
			}
			if tc.wantOutbox == 0 {
				return
			}
			row := rows[0]
			if row.Kind != domain.OutboxKindLeadTaskMessage || row.TargetAgent != "epic-lead-1" || row.EpicID != tc.wantEpicID {
				t.Fatalf("outbox row = %+v, want leadTaskMessage targeting epic-lead-1 under %q", row, tc.wantEpicID)
			}
		})
	}
}

// driveTerminalTaskRun enqueues a queued task run under the given driver run,
// registers a worker node, then claims and executes it to a completed terminal
// transition — the path that resolves the epic via the parent run and creates
// the lead-task outbox.
func driveTerminalTaskRun(t *testing.T, ctx context.Context, st store.Store, driverRunID string) {
	t.Helper()
	if _, err := st.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    "TEST",
		NodeID:          "wf-node-1",
		RuntimeProvider: domain.RuntimeProviderLocal,
		Capabilities:    []string{"driver-runner", "task-runner", "local-noop"},
		DrainState:      domain.NodeDrainActive,
		TTL:             time.Minute,
	}); err != nil {
		t.Fatalf("create worker node: %v", err)
	}
	taskRunID := "wf-task-run-1"
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "TEST",
		TaskRunID:       taskRunID,
		DriverRunID:     driverRunID,
		TaskID:          "WF-TASK-1",
		ProviderProfile: "local-noop",
		Status:          domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("create queued task run: %v", err)
	}
	if _, err := driver.ClaimAndExecuteTaskRunWithResult(ctx, st, driver.TaskRunWorkerOptions{
		WorkspaceKey:       "TEST",
		TaskRunID:          taskRunID,
		NodeID:             "wf-node-1",
		SupportedProviders: []string{"local-noop"},
		HeartbeatInterval:  -1,
	}, nil); err != nil {
		t.Fatalf("claim and execute task run: %v", err)
	}
}

func bindWorkflowEpicLead(t *testing.T, ctx context.Context, st store.Store, name, epicID string) {
	t.Helper()
	if _, err := st.Agents().Create(ctx, store.AgentCreate{
		WorkspaceKey: "TEST",
		Name:         name,
		RoleName:     "lead",
		Parent:       epicID,
	}); err != nil {
		t.Fatalf("create lead agent: %v", err)
	}
}

func listWorkflowOutboxRows(t *testing.T, ctx context.Context, st store.Store) []*domain.OutboxRecord {
	t.Helper()
	rows, err := st.Outbox().ListDue(ctx, "TEST", store.OutboxDueFilter{Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("list due outbox: %v", err)
	}
	return rows
}

func TestGetRunEventsReturnsDriverRunEvents(t *testing.T) {
	ctx := context.Background()
	st := seededWorkflowStore(t, ctx)
	run, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "TEST",
		RunID:           "run-1",
		DriverID:        "demo",
		DriverVersionID: "version-1",
		Payload:         json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/TEST/runs/"+run.RunID+"/events", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var page domain.PlatformEventsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode events page: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].EntityID != "run-1" || page.Events[0].EntityType != "driver_run" {
		t.Fatalf("events page = %+v, want one driver_run event", page)
	}
}

func TestCreateWorkflowVersionRejectsPackageManifest(t *testing.T) {
	st := memstore.New()
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	body := `{"files":{"package.json":"{}","workflows/demo.ts":"export async function run(){ return {}; }"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/TEST/workflows/demo/versions", stringsReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func seededWorkflowStore(t *testing.T, ctx context.Context) store.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey:    "TEST",
		DriverID:        "demo",
		Name:            "demo",
		OwnerType:       domain.DriverOwnerUser,
		ActiveVersionID: "version-1",
		Status:          domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "TEST",
		VersionID:        "version-1",
		DriverID:         "demo",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	return st
}

func stringsReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

func installFakeFlueBuild(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-flue")
	body := `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output" ]; then
    shift
    out="$1"
  fi
  shift
done
if [ "$out" = "" ]; then
  echo "missing --output" >&2
  exit 1
fi
mkdir -p "$out"
cat > "$out/server.mjs" <<'EOF'
export async function run() { return {}; }
EOF
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake flue: %v", err)
	}
	sdkRoot := filepath.Join(dir, "sdk")
	if err := os.MkdirAll(sdkRoot, 0o755); err != nil {
		t.Fatalf("create fake sdk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "package.json"), []byte(`{"name":"@loom/sdk"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake sdk package: %v", err)
	}
	runtimeRoot := filepath.Join(dir, "runtime")
	for _, dep := range []string{
		runtimeRoot,
		filepath.Join(runtimeRoot, "node_modules", "@hono", "node-server"),
		filepath.Join(runtimeRoot, "node_modules", "hono"),
	} {
		if err := os.MkdirAll(dep, 0o755); err != nil {
			t.Fatalf("create fake runtime dependency %s: %v", dep, err)
		}
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, "package.json"), []byte(`{"name":"@flue/runtime"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write fake runtime package: %v", err)
	}
	t.Setenv("LOOM_REAL_FLUE_CMD", script)
	t.Setenv("LOOM_REAL_FLUE_CMD_JSON", "")
	t.Setenv("LOOM_SDK_ROOT", sdkRoot)
	t.Setenv("LOOM_FLUE_RUNTIME_ROOT", runtimeRoot)
	t.Setenv("FLUE_RUNTIME_ROOT", "")
	t.Setenv("FLUE_REPO", "")
}
