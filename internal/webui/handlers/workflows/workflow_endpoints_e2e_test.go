//go:build e2e
// +build e2e

package workflows

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/netutil"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestE2E_WorkflowEndpointsRunRealFlueEpicRunner(t *testing.T) {
	e2e := newWorkflowEndpointE2E(t)

	e2e.startFleetDB()
	e2e.seedWorkspace()
	dag := e2e.seedEpicDAG()
	e2e.startLoomServe()

	runID := e2e.startWorkflowRun(BuiltinEpicRunnerWorkflowName, map[string]any{
		"epicId":      dag.epicID,
		"requestedBy": "workflow-endpoint-e2e",
	})

	run := e2e.waitForRunCompleted(runID)
	e2e.expectRunPayload(run, dag.epicID)
	e2e.expectRunEvents(runID, "driver_run.create", "driver_run.claim", "driver_run.finish")
	e2e.expectRunStream(runID, "driver_run.create")
	e2e.expectDAGCompleted(dag, runID)
}

type workflowEndpointE2E struct {
	t *testing.T

	repoRoot    string
	fleetDBRepo string
	flueRepo    string

	workspace string
	actor     string
	nodeID    string

	loomBin     string
	fleetDBBin  string
	flueCommand []string

	workDir       string
	configDir     string
	taskRunnerLog string

	fleetURL string
	loomURL  string

	fleetClient  *fleetdb.Client
	issueBackend backend.IssueBackend
	httpClient   *http.Client
}

type workflowEndpointDAG struct {
	epicID string
	taskA  string
	taskB  string
	taskC  string
	taskD  string
}

func newWorkflowEndpointE2E(t *testing.T) *workflowEndpointE2E {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real workflow endpoint E2E under -short")
	}

	repoRoot := workflowEndpointRepoRoot(t)
	workDir := filepath.Join(t.TempDir(), "workflow-repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("create workflow repo: %v", err)
	}

	e2e := &workflowEndpointE2E{
		t:             t,
		repoRoot:      repoRoot,
		fleetDBRepo:   workflowEndpointSiblingRepo(t, repoRoot, "fleet-db", "FLEET_DB_REPO", "cmd/fleet-db"),
		flueRepo:      workflowEndpointSiblingRepo(t, repoRoot, "flue", "FLUE_REPO", "packages/cli"),
		workspace:     "WFLOWE2E",
		actor:         "workflow-endpoint-e2e",
		nodeID:        "workflow-endpoint-e2e-node",
		workDir:       workDir,
		configDir:     filepath.Join(t.TempDir(), "loom-config"),
		taskRunnerLog: filepath.Join(t.TempDir(), "task-runner.log"),
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}

	e2e.loomBin = workflowEndpointBuildGoBinary(t, repoRoot, "./cmd/loom", "loom")
	e2e.fleetDBBin = workflowEndpointBuildGoBinary(t, e2e.fleetDBRepo, "./cmd/fleet-db", "fleet-db")
	e2e.flueCommand = workflowEndpointFlueCommand(t, repoRoot, e2e.flueRepo)
	e2e.prepareWorkflowRepo()
	return e2e
}

func (e *workflowEndpointE2E) startFleetDB() {
	e.t.Helper()
	e.t.Setenv(bootstrap.EnvFleetDBBin, e.fleetDBBin)

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	e.t.Cleanup(cancel)

	embedded, err := bootstrap.StartEmbedded(ctx, filepath.Join(e.t.TempDir(), "fleet-data"), workflowEndpointQuietLogger())
	if err != nil {
		e.t.Fatalf("start embedded fleet-db: %v", err)
	}
	e.t.Cleanup(func() {
		if err := embedded.Stop(); err != nil {
			e.t.Logf("stop embedded fleet-db: %v", err)
		}
	})

	e.fleetURL = embedded.URL()
	e.fleetClient, err = fleetdb.New(fleetdb.Config{
		BaseURL: e.fleetURL,
		Actor:   e.actor,
	})
	if err != nil {
		e.t.Fatalf("create fleet-db client: %v", err)
	}
	e.t.Cleanup(func() {
		if err := e.fleetClient.Close(); err != nil {
			e.t.Logf("close fleet-db client: %v", err)
		}
	})
}

func (e *workflowEndpointE2E) seedWorkspace() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := e.fleetClient.Workspaces().Create(ctx, store.WorkspaceCreate{
		Key:  e.workspace,
		Name: "Workflow endpoint E2E",
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("create workspace: %v", err)
	}

	_, err = e.fleetClient.Nodes().Create(ctx, store.NodeCreate{
		WorkspaceKey:    e.workspace,
		NodeID:          e.nodeID,
		OwnerActor:      e.actor,
		RuntimeProvider: domain.RuntimeProviderLocal,
		DrainState:      domain.NodeDrainActive,
		Capacity:        8,
		TTL:             5 * time.Minute,
	})
	if err != nil && !errors.Is(err, domain.ErrAlreadyExists) {
		e.t.Fatalf("create executor node: %v", err)
	}

	e.issueBackend, err = fleet.New(fleet.Config{
		BaseURL:     e.fleetURL,
		WorkspaceID: e.workspace,
		Actor:       e.actor,
	})
	if err != nil {
		e.t.Fatalf("create issue backend: %v", err)
	}
}

func (e *workflowEndpointE2E) seedEpicDAG() workflowEndpointDAG {
	e.t.Helper()
	epic := e.createIssue(backend.CreateParams{
		Title:     "Workflow endpoint E2E Epic",
		IssueType: "epic",
		Priority:  1,
		CreatedBy: e.actor,
	})
	taskA := e.createIssue(backend.CreateParams{
		Title:     "A",
		IssueType: "task",
		Parent:    epic.ID,
		Priority:  1,
		CreatedBy: e.actor,
	})
	taskB := e.createIssue(backend.CreateParams{
		Title:        "B",
		IssueType:    "task",
		Parent:       epic.ID,
		Priority:     1,
		CreatedBy:    e.actor,
		Dependencies: []string{taskA.ID},
	})
	taskC := e.createIssue(backend.CreateParams{
		Title:        "C",
		IssueType:    "task",
		Parent:       epic.ID,
		Priority:     1,
		CreatedBy:    e.actor,
		Dependencies: []string{taskA.ID},
	})
	taskD := e.createIssue(backend.CreateParams{
		Title:        "D",
		IssueType:    "task",
		Parent:       epic.ID,
		Priority:     1,
		CreatedBy:    e.actor,
		Dependencies: []string{taskB.ID, taskC.ID},
	})
	return workflowEndpointDAG{
		epicID: epic.ID,
		taskA:  taskA.ID,
		taskB:  taskB.ID,
		taskC:  taskC.ID,
		taskD:  taskD.ID,
	}
}

func (e *workflowEndpointE2E) createIssue(params backend.CreateParams) *backend.IssueData {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	issue, err := e.issueBackend.Create(ctx, params)
	if err != nil {
		e.t.Fatalf("create issue %q: %v", params.Title, err)
	}
	return issue
}

func (e *workflowEndpointE2E) startLoomServe() {
	e.t.Helper()
	_, port, err := netutil.PickFreeLoopbackPort()
	if err != nil {
		e.t.Fatalf("pick loom port: %v", err)
	}
	e.loomURL = "http://127.0.0.1:" + strconv.Itoa(port)

	taskRunnerCommand := e.writeTaskRunner()
	flueCommandJSON, err := json.Marshal(e.flueCommand)
	if err != nil {
		e.t.Fatalf("encode Flue command: %v", err)
	}

	cmd := exec.Command(e.loomBin, "serve",
		"--no-daemon",
		"--bind", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--frontend-url", "http://127.0.0.1:9",
	)
	cmd.Dir = e.workDir
	cmd.Env = workflowEndpointEnv(map[string]string{
		"HOME":                             filepath.Join(e.configDir, "home"),
		"LOOM_CONFIG_DIR":                  e.configDir,
		"LOOM_WORKSPACE":                   e.workspace,
		"LOOM_FLEET_DB_URL":                e.fleetURL,
		"LOOM_FLEET_URL":                   "",
		"LOOM_SERVER_URL":                  "",
		"LOOM_DISABLE_H2C":                 "1",
		"LOOM_DRIVER_EXECUTOR":             "",
		"LOOM_DRIVER_EXECUTOR_NODE_ID":     e.nodeID,
		"LOOM_DRIVER_TASK_RUNNER_CMD_JSON": taskRunnerCommand,
		"LOOM_REAL_FLUE_CMD_JSON":          string(flueCommandJSON),
		"LOOM_SDK_ROOT":                    filepath.Join(e.repoRoot, "sdk"),
		"LOOM_ISSUE_BACKEND":               "",
		bootstrap.EnvFleetDBBin:            e.fleetDBBin,
		bootstrap.EnvFleetDBAPIKey:         "",
		bootstrap.EnvFleetDBActor:          e.actor,
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		e.t.Fatalf("start loom serve: %v", err)
	}
	e.t.Cleanup(func() {
		workflowEndpointStopProcess(e.t, cmd)
		if e.t.Failed() {
			e.t.Logf("loom serve stdout:\n%s", strings.TrimSpace(stdout.String()))
			e.t.Logf("loom serve stderr:\n%s", strings.TrimSpace(stderr.String()))
		}
	})

	e.waitForLoomHealth(&stderr)
}

func (e *workflowEndpointE2E) startWorkflowRun(name string, payload map[string]any) string {
	e.t.Helper()
	var run domain.DriverRun
	e.postJSONWithHeaders(
		"/api/workspaces/"+e.workspace+"/workflows/"+name,
		payload,
		map[string]string{"Idempotency-Key": "workflow-endpoint-e2e-" + e.workspace},
		http.StatusAccepted,
		&run,
	)
	if run.RunID == "" {
		e.t.Fatalf("workflow run response missing run_id: %+v", run)
	}
	return run.RunID
}

func (e *workflowEndpointE2E) waitForRunCompleted(runID string) domain.DriverRun {
	e.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var run domain.DriverRun
		e.getJSON("/api/workspaces/"+e.workspace+"/runs/"+runID, http.StatusOK, &run)
		switch run.Status {
		case domain.DriverRunCompleted:
			return run
		case domain.DriverRunFailed, domain.DriverRunNeedsReview, domain.DriverRunCancelled:
			e.t.Fatalf("workflow run %s reached terminal status %s: %+v", runID, run.Status, run)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var run domain.DriverRun
	e.getJSON("/api/workspaces/"+e.workspace+"/runs/"+runID, http.StatusOK, &run)
	e.t.Fatalf("workflow run %s did not complete before deadline: %+v", runID, run)
	return run
}

func (e *workflowEndpointE2E) expectRunPayload(run domain.DriverRun, epicID string) {
	e.t.Helper()
	var payload struct {
		EpicID      string `json:"epicId"`
		RequestedBy string `json:"requestedBy"`
	}
	if err := json.Unmarshal(run.Payload, &payload); err != nil {
		e.t.Fatalf("decode run payload %s: %v", run.Payload, err)
	}
	if payload.EpicID != epicID || payload.RequestedBy != "workflow-endpoint-e2e" {
		e.t.Fatalf("run payload = %+v, want epicId=%s requestedBy=workflow-endpoint-e2e", payload, epicID)
	}
	if run.DriverID != BuiltinEpicRunnerWorkflowName {
		e.t.Fatalf("run driver = %s, want %s", run.DriverID, BuiltinEpicRunnerWorkflowName)
	}
	if run.Output["logs_ref"] != "driver-run://"+run.RunID+"/flue-local" {
		e.t.Fatalf("run output logs_ref = %q", run.Output["logs_ref"])
	}
	if !strings.Contains(run.Output["flue_stderr_tail"], "epic-runner-start "+epicID) {
		e.t.Fatalf("run output missing workflow log marker: %+v", run.Output)
	}
}

func (e *workflowEndpointE2E) expectRunEvents(runID string, requiredActions ...string) {
	e.t.Helper()
	var page domain.PlatformEventsPage
	e.getJSON("/api/workspaces/"+e.workspace+"/runs/"+runID+"/events?limit=50", http.StatusOK, &page)
	actions := map[string]bool{}
	for _, event := range page.Events {
		actions[event.Action] = true
	}
	for _, action := range requiredActions {
		if !actions[action] {
			e.t.Fatalf("run events missing %s; got %v", action, sortedKeys(actions))
		}
	}
}

func (e *workflowEndpointE2E) expectRunStream(runID, requiredAction string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.loomURL+"/api/workspaces/"+e.workspace+"/runs/"+runID+"/stream?after=0", nil)
	if err != nil {
		e.t.Fatalf("create stream request: %v", err)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("open run stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		e.t.Fatalf("GET run stream status = %d body=%s", resp.StatusCode, string(body))
	}
	reader := bufio.NewReader(resp.Body)
	var sample strings.Builder
	for ctx.Err() == nil {
		line, readErr := reader.ReadString('\n')
		sample.WriteString(line)
		if strings.Contains(sample.String(), `"action":"`+requiredAction+`"`) {
			return
		}
		if readErr != nil {
			e.t.Fatalf("read run stream: %v\nsample:\n%s", readErr, sample.String())
		}
	}
	e.t.Fatalf("run stream did not include %s before deadline\nsample:\n%s", requiredAction, sample.String())
}

func (e *workflowEndpointE2E) expectDAGCompleted(dag workflowEndpointDAG, runID string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	children, err := e.issueBackend.GetChildren(ctx, dag.epicID)
	if err != nil {
		e.t.Fatalf("list epic children: %v", err)
	}
	closed := 0
	for _, child := range children {
		if child.Status == "closed" {
			closed++
		}
	}
	if closed != 4 {
		e.t.Fatalf("closed child task count = %d, want 4; children=%+v", closed, children)
	}

	taskRuns, err := e.fleetClient.TaskRuns().List(ctx, e.workspace, store.TaskRunFilter{DriverRunID: runID})
	if err != nil {
		e.t.Fatalf("list child task runs: %v", err)
	}
	if len(taskRuns) != 4 {
		e.t.Fatalf("task run count = %d, want 4; taskRuns=%+v", len(taskRuns), taskRuns)
	}
	taskRunByTaskID := make(map[string]*domain.TaskRun, len(taskRuns))
	for _, taskRun := range taskRuns {
		if taskRun.Status != domain.TaskRunCompleted || taskRun.LogsRef == "" {
			e.t.Fatalf("task run = %+v, want completed with logs_ref", taskRun)
		}
		taskRunByTaskID[taskRun.TaskID] = taskRun
	}
	e.expectCloseTaskLedger(taskRunByTaskID)

	executed := workflowEndpointReadLines(e.t, e.taskRunnerLog)
	if len(executed) != 4 {
		e.t.Fatalf("executed task count = %d, want 4: %v", len(executed), executed)
	}
	if executed[0] != dag.taskA || executed[3] != dag.taskD {
		e.t.Fatalf("dependency order violation: %v", executed)
	}
	middle := append([]string{}, executed[1], executed[2])
	sort.Strings(middle)
	wantMiddle := []string{dag.taskB, dag.taskC}
	sort.Strings(wantMiddle)
	if strings.Join(middle, ",") != strings.Join(wantMiddle, ",") {
		e.t.Fatalf("middle tasks = %v, want %v", middle, wantMiddle)
	}
}

func (e *workflowEndpointE2E) expectCloseTaskLedger(taskRunByTaskID map[string]*domain.TaskRun) {
	e.t.Helper()
	var listed struct {
		Actions []struct {
			ActionID    string `json:"action_id"`
			ActionType  string `json:"action_type"`
			TargetRef   string `json:"target_ref"`
			RequestedBy string `json:"requested_by"`
			Status      string `json:"status"`
			RequestRef  string `json:"request_ref"`
			ResponseRef string `json:"response_ref"`
		} `json:"actions"`
		Count int `json:"count"`
	}
	e.getFleetJSON("/api/v1/"+e.workspace+"/action-ledger?action_type=close_task&status=applied&limit=50", http.StatusOK, &listed)
	if listed.Count != len(taskRunByTaskID) {
		e.t.Fatalf("close_task action count = %d, want %d: %+v", listed.Count, len(taskRunByTaskID), listed.Actions)
	}
	seen := make(map[string]bool, len(listed.Actions))
	for _, action := range listed.Actions {
		taskRun := taskRunByTaskID[action.TargetRef]
		if taskRun == nil {
			e.t.Fatalf("close_task action target = %s, want one of task IDs %v", action.TargetRef, sortedTaskIDs(taskRunByTaskID))
		}
		if action.ActionType != "close_task" || action.Status != "applied" {
			e.t.Fatalf("close_task action = %+v, want applied close_task", action)
		}
		if action.RequestedBy != "task-run:"+taskRun.TaskRunID {
			e.t.Fatalf("close_task requested_by = %q, want task-run:%s", action.RequestedBy, taskRun.TaskRunID)
		}
		if action.ResponseRef != "issue://"+taskRun.TaskID+"#closed" {
			e.t.Fatalf("close_task response_ref = %q, want issue://%s#closed", action.ResponseRef, taskRun.TaskID)
		}
		seen[action.TargetRef] = true
	}
	for taskID := range taskRunByTaskID {
		if !seen[taskID] {
			e.t.Fatalf("missing close_task action for %s; actions=%+v", taskID, listed.Actions)
		}
	}
	for _, taskRun := range taskRunByTaskID {
		e.expectTaskRunCompletionReplay(taskRun)
		return
	}
}

func (e *workflowEndpointE2E) expectTaskRunCompletionReplay(taskRun *domain.TaskRun) {
	e.t.Helper()
	var replay struct {
		TaskRun *domain.TaskRun `json:"task_run"`
		Action  *struct {
			ActionID   string `json:"action_id"`
			ActionType string `json:"action_type"`
			TargetRef  string `json:"target_ref"`
			Status     string `json:"status"`
		} `json:"action"`
	}
	e.postFleetJSON("/api/v1/"+e.workspace+"/task-runs/"+taskRun.TaskRunID+"/complete", map[string]any{
		"completion_id": "complete-" + taskRun.TaskRunID,
		"status":        "completed",
		"close_task":    true,
	}, http.StatusOK, &replay)
	if replay.TaskRun == nil || replay.TaskRun.TaskRunID != taskRun.TaskRunID || replay.TaskRun.Status != domain.TaskRunCompleted {
		e.t.Fatalf("completion replay task_run = %+v, want completed %s", replay.TaskRun, taskRun.TaskRunID)
	}
	if replay.Action == nil || replay.Action.ActionType != "close_task" || replay.Action.Status != "applied" || replay.Action.TargetRef != taskRun.TaskID {
		e.t.Fatalf("completion replay action = %+v, want applied close_task for %s", replay.Action, taskRun.TaskID)
	}
}

func (e *workflowEndpointE2E) postJSON(path string, body any, wantStatus int, out any) {
	e.t.Helper()
	e.postJSONWithHeaders(path, body, nil, wantStatus, out)
}

func (e *workflowEndpointE2E) postJSONWithHeaders(path string, body any, headers map[string]string, wantStatus int, out any) {
	e.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("encode POST %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, e.loomURL+path, bytes.NewReader(data))
	if err != nil {
		e.t.Fatalf("create POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", e.actor)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	workflowEndpointDecodeResponse(e.t, resp, wantStatus, out)
}

func (e *workflowEndpointE2E) getJSON(path string, wantStatus int, out any) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.loomURL+path, nil)
	if err != nil {
		e.t.Fatalf("create GET %s: %v", path, err)
	}
	req.Header.Set("X-Actor", e.actor)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	workflowEndpointDecodeResponse(e.t, resp, wantStatus, out)
}

func (e *workflowEndpointE2E) getFleetJSON(path string, wantStatus int, out any) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.fleetURL+path, nil)
	if err != nil {
		e.t.Fatalf("create fleet GET %s: %v", path, err)
	}
	req.Header.Set("X-Actor", e.actor)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("fleet GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	workflowEndpointDecodeResponse(e.t, resp, wantStatus, out)
}

func (e *workflowEndpointE2E) postFleetJSON(path string, body any, wantStatus int, out any) {
	e.t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("encode fleet POST %s: %v", path, err)
	}
	req, err := http.NewRequest(http.MethodPost, e.fleetURL+path, bytes.NewReader(data))
	if err != nil {
		e.t.Fatalf("create fleet POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", e.actor)
	resp, err := e.httpClient.Do(req)
	if err != nil {
		e.t.Fatalf("fleet POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	workflowEndpointDecodeResponse(e.t, resp, wantStatus, out)
}

func workflowEndpointDecodeResponse(t *testing.T, resp *http.Response, wantStatus int, out any) {
	t.Helper()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s response: %v", resp.Request.URL.Path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d body=%s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, wantStatus, string(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s response: %v\n%s", resp.Request.URL.Path, err, string(data))
		}
	}
}

func (e *workflowEndpointE2E) waitForLoomHealth(stderr *bytes.Buffer) {
	e.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.httpClient.Get(e.loomURL + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	e.t.Fatalf("loom serve did not become healthy at %s\nstderr:\n%s", e.loomURL, strings.TrimSpace(stderr.String()))
}

func (e *workflowEndpointE2E) prepareWorkflowRepo() {
	e.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		e.t.Skipf("git not available: %v", err)
	}
	workflowEndpointRun(tOrFatal{e.t}, e.workDir, "git", "init", "-q")
	workflowEndpointRun(tOrFatal{e.t}, e.workDir, "git", "config", "user.email", "workflow-endpoint-e2e@example.test")
	workflowEndpointRun(tOrFatal{e.t}, e.workDir, "git", "config", "user.name", "Workflow Endpoint E2E")
	workflowEndpointRun(tOrFatal{e.t}, e.workDir, "git", "commit", "--allow-empty", "-m", "seed", "-q")
}

func (e *workflowEndpointE2E) writeTaskRunner() string {
	e.t.Helper()
	node := workflowEndpointNode(e.t)
	scriptPath := filepath.Join(e.workDir, "task-runner.mjs")
	script := fmt.Sprintf(`#!/usr/bin/env node
import fs from 'node:fs';

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}');
const logPath = %s;

if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error('task-run lease token did not reach task runner');
  process.exit(3);
}
if (request.runner !== 'local-task-runner') {
  console.error('unexpected runner ' + request.runner);
  process.exit(4);
}

fs.appendFileSync(logPath, request.task_id + '\n');
console.log(JSON.stringify({
  status: 'completed',
  exitCode: 0,
  logsRef: 'logs://' + request.task_run_id,
  runtimeMetadata: {
    task_runner: 'workflow-endpoint-e2e',
    runner: request.runner || '',
    sandbox_provider: request.sandbox_placement?.provider || '',
  },
}));
`, jsonString(e.taskRunnerLog))
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		e.t.Fatalf("write task runner: %v", err)
	}
	command, err := json.Marshal([]string{node, scriptPath})
	if err != nil {
		e.t.Fatalf("encode task runner command: %v", err)
	}
	return string(command)
}

func workflowEndpointRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("resolve loom repo root from %s: go.mod not found", file)
		}
		dir = parent
	}
}

func workflowEndpointSiblingRepo(t *testing.T, repoRoot, sibling, envName, marker string) string {
	t.Helper()
	if override := strings.TrimSpace(os.Getenv(envName)); override != "" {
		if _, err := os.Stat(filepath.Join(override, marker)); err != nil {
			t.Skipf("%s=%s does not contain %s: %v", envName, override, marker, err)
		}
		return override
	}
	path := filepath.Clean(filepath.Join(repoRoot, "..", sibling))
	if _, err := os.Stat(filepath.Join(path, marker)); err != nil {
		t.Skipf("%s repo not found at %s; set %s", sibling, path, envName)
	}
	return path
}

func workflowEndpointBuildGoBinary(t *testing.T, repo, pkg, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), name)
	workflowEndpointRun(tOrFatal{t}, repo, "go", "build", "-o", out, pkg)
	return out
}

func workflowEndpointFlueCommand(t *testing.T, repoRoot, flueRepo string) []string {
	t.Helper()
	if encoded := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil {
			t.Fatalf("decode LOOM_REAL_FLUE_CMD_JSON: %v", err)
		}
		if len(parsed) == 0 {
			t.Fatal("LOOM_REAL_FLUE_CMD_JSON must contain at least one command element")
		}
		return parsed
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD")); raw != "" {
		return []string{raw}
	}
	if path, err := exec.LookPath("flue"); err == nil {
		return []string{path}
	}
	node := workflowEndpointNode(t)
	distCLI := filepath.Join(flueRepo, "packages", "cli", "dist", "flue.js")
	if _, err := os.Stat(distCLI); err == nil {
		return []string{node, distCLI}
	}
	sourceCLI := filepath.Join(flueRepo, "packages", "cli", "bin", "flue.mjs")
	if _, err := os.Stat(sourceCLI); err == nil {
		return []string{node, sourceCLI}
	}
	t.Skipf("Flue CLI not found; set LOOM_REAL_FLUE_CMD_JSON, put flue on PATH, or build %s", filepath.Join(repoRoot, "..", "flue", "packages", "cli"))
	return nil
}

func workflowEndpointNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	return node
}

type tOrFatal struct {
	t *testing.T
}

func workflowEndpointRun(tf tOrFatal, dir, name string, args ...string) {
	tf.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = workflowEndpointEnv(map[string]string{
		"GOCACHE": workflowEndpointGoCache(tf.t),
	})
	output, err := cmd.CombinedOutput()
	if err != nil {
		tf.t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, strings.TrimSpace(string(output)))
	}
}

func workflowEndpointGoCache(t *testing.T) string {
	t.Helper()
	if cache := strings.TrimSpace(os.Getenv("GOCACHE")); cache != "" {
		return cache
	}
	return filepath.Join(os.TempDir(), "go-build-cache")
}

func workflowEndpointQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func workflowEndpointStopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func workflowEndpointEnv(overrides map[string]string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+len(overrides))
	for _, entry := range env {
		name := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			name = entry[:idx]
		}
		if _, ok := overrides[name]; ok {
			continue
		}
		out = append(out, entry)
	}
	for name, value := range overrides {
		out = append(out, name+"="+value)
	}
	return out
}

func workflowEndpointReadLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedTaskIDs(values map[string]*domain.TaskRun) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
