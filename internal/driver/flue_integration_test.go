//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRealFlueBuildAndBuiltServerSmoke(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 to run the real Flue CLI smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	flueCommand := realFlueCommandForTest(t)

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeRealFlueProject(t, root, "real-flue-smoke", `import { defineAgent, defineWorkflow } from "@flue/runtime";
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: () => {
    const input = JSON.parse(process.env.LOOM_FLUE_INVOKE_PAYLOAD || "{}");
    return { status: "completed", summary: input.message || "real flue smoke" };
  },
});
`)
	buildRealFlueProject(t, root, flueCommand)

	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "real-flue-smoke",
		WorkflowName: "real-flue-smoke",
		SourceRef:    "workflows/real-flue-smoke.ts",
		CreatedBy:    "tester",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver after real Flue build %q: %v", strings.Join(flueCommand, " "), err)
	}
	if got := registered.Version.Manifest["server_ref"]; got != "dist/server.mjs" {
		t.Fatalf("server_ref = %q, want dist/server.mjs", got)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     registered.Driver.DriverID,
		EpicID:       "TEST-1",
		RunID:        "run-1",
		Payload:      json.RawMessage(`{"message":"real flue ok"}`),
	})
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
	if result.Status != domain.DriverRunCompleted || result.Summary != "real flue ok" {
		t.Fatalf("result = %+v, want completed real flue ok", result)
	}
}

// TestRealFlueBuiltinEpicRunnerWatchLoopSmoke builds the actual builtin
// epic-runner workflow source with the real Flue CLI and drives it against a
// fake driver-op HTTP API (including the epic watch SSE stream). The fake
// mirrors the serve worker contract: exec-task executes immediately,
// completes-and-closes (or blocks) the task server-side, and appends the
// terminal journal event the watch stream delivers; the workflow's follow-up
// Terminal watch events are observations of already-committed completion;
// the workflow must not call complete-task or receive a worker lease token.
//
// The smoke runs under the §9.5 locked-down token-only env (TK5): the legacy
// auth fallback is switched off, a run-scoped token is minted for the claimed
// lease, and the fake rejects any request that presents legacy
// X-Loom-Driver-* identity headers or a bad bearer token.
func TestRealFlueBuiltinEpicRunnerWatchLoopSmoke(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 to run the real Flue CLI smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	flueCommand := realFlueCommandForTest(t)
	nodePath := nodePathForTest(t)
	t.Setenv(LegacyDriverAuthEnvVar, "0")
	runTokenKey := bytes.Repeat([]byte{0x42}, 32)

	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeRealFlueProject(t, root, "epic-runner", builtinEpicRunnerSource(t))
	buildRealFlueProject(t, root, flueCommand)
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "epic-runner",
		WorkflowName: "epic-runner",
		SourceRef:    "workflows/epic-runner.ts",
		CreatedBy:    "tester",
		Activate:     true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver after real Flue build %q: %v", strings.Join(flueCommand, " "), err)
	}

	// Each case gets its own epic: a finished NodeRunner invocation leaves the
	// DriverRun row running in the store (the executor normally finishes it),
	// and CreateDriverRun reuses active runs per epic.
	cases := []struct {
		name          string
		epicID        string
		runner        string
		failTask      string
		wantStatus    domain.DriverRunStatus
		wantSummary   string
		wantError     string
		wantExecuted  string
		wantCompleted []string
	}{
		{
			name:          "watch-driven DAG drain completes",
			epicID:        "TEST-1",
			runner:        "local-task-runner",
			wantStatus:    domain.DriverRunCompleted,
			wantSummary:   "Epic drained TEST-1: TEST-A,TEST-B,TEST-C,TEST-D",
			wantExecuted:  "TEST-A,TEST-B,TEST-C,TEST-D",
			wantCompleted: []string{"TEST-A", "TEST-B", "TEST-C", "TEST-D"},
		},
		{
			name:          "blocked branch drains siblings into needs_review",
			epicID:        "TEST-2",
			runner:        "daytona-task-runner",
			failTask:      "TEST-C",
			wantStatus:    domain.DriverRunNeedsReview,
			wantSummary:   "TEST-C",
			wantError:     "epic_tasks_blocked",
			wantExecuted:  "TEST-A,TEST-B,TEST-C,TEST-D",
			wantCompleted: []string{"TEST-A", "TEST-B", "TEST-D"},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeEpicAPI(tc.epicID, tc.failTask)
			// Happy path: A -> (B, C) -> D. Blocked path: C has no dependents so
			// the sibling branch B -> D drains around the injected failure.
			fake.addTask("TEST-A")
			fake.addTask("TEST-B", "TEST-A")
			fake.addTask("TEST-C", "TEST-A")
			if tc.failTask == "" {
				fake.addTask("TEST-D", "TEST-B", "TEST-C")
			} else {
				fake.addTask("TEST-D", "TEST-B")
			}
			server := httptest.NewServer(fake)
			defer server.Close()

			runID := fmt.Sprintf("run-epic-watch-%d", i+1)
			payload := json.RawMessage(`{"epicId":"` + tc.epicID + `","runner":"` + tc.runner + `"}`)
			run, err := CreateDriverRun(ctx, st, RunOptions{
				WorkspaceKey: "TEST",
				DriverID:     registered.Driver.DriverID,
				EpicID:       tc.epicID,
				RunID:        runID,
				Payload:      payload,
			})
			if err != nil {
				t.Fatalf("CreateDriverRun: %v", err)
			}
			claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-"+runID)
			if err != nil {
				t.Fatalf("Claim: %v", err)
			}
			req, err := loadRunRequest(ctx, root, claimed, st)
			if err != nil {
				t.Fatalf("loadRunRequest: %v", err)
			}
			req.RunToken, err = MintRunToken(RunTokenClaims{
				WorkspaceKey: claimed.WorkspaceKey,
				RunID:        claimed.RunID,
				NodeID:       claimed.NodeID,
				LeaseID:      claimed.LeaseID,
				FencingToken: claimed.FencingToken,
			}, runTokenKey, time.Hour)
			if err != nil {
				t.Fatalf("MintRunToken: %v", err)
			}
			fake.requireRunToken(runTokenKey, claimed.RunID)

			runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			result, err := (NodeRunner{
				APIBaseURL: server.URL,
				// Inert: the SDK is HTTP-only; this only keeps the runner from
				// exporting the test binary as a CLI fallback.
				ExecTaskCommand: []string{nodePath},
			}).Run(runCtx, req)
			if err != nil {
				t.Fatalf("NodeRunner.Run: %v", err)
			}
			if result.Status != tc.wantStatus || !strings.Contains(result.Summary, tc.wantSummary) {
				t.Fatalf("result = %+v, want status %q with summary containing %q\nfake state: %s",
					result, tc.wantStatus, tc.wantSummary, fake.debugState())
			}
			if tc.wantError != "" && result.ErrorClass != tc.wantError {
				t.Fatalf("error class = %q, want %q", result.ErrorClass, tc.wantError)
			}
			executed, completes := fake.observed()
			if strings.Join(executed, ",") != tc.wantExecuted {
				t.Fatalf("executed order = %v, want %s", executed, tc.wantExecuted)
			}
			if runners := fake.observedRunners(); len(runners) != len(executed) {
				t.Fatalf("runner requests = %v, want one per executed task %v", runners, executed)
			} else {
				for taskID, runner := range runners {
					if runner != tc.runner {
						t.Fatalf("task %s requested runner %q, want %q", taskID, runner, tc.runner)
					}
				}
			}
			if len(completes) != 0 {
				t.Fatalf("complete-task calls = %+v, want none for committed terminal events", completes)
			}
			if tc.failTask != "" && fake.taskStatus(tc.failTask) != "blocked" {
				t.Fatalf("task %s status = %q, want blocked", tc.failTask, fake.taskStatus(tc.failTask))
			}
			if violations := fake.authViolations(); len(violations) != 0 {
				t.Fatalf("token-only auth violations: %v", violations)
			}
		})
	}
}

// builtinEpicRunnerSource loads the real builtin epic-runner workflow source
// so the smoke covers the exact code loom serve bundles.
func builtinEpicRunnerSource(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "workflows", "builtin", "epic-runner.ts"))
	if err != nil {
		t.Fatalf("resolve builtin epic-runner source: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read builtin epic-runner source: %v", err)
	}
	return string(data)
}

// fakeEpicAPI is an in-test driver-op HTTP API with the watch SSE stream. It
// keeps one epic's DAG state and mimics the serve task-worker contract:
// exec-task runs the task inline, closes (or blocks) it, and appends the
// terminal TaskRun journal event; complete-task answers with the
// already-completed conflict the real worker-closed path produces.
type fakeEpicAPI struct {
	mu        sync.Mutex
	epicID    string
	failTask  string
	order     []string
	tasks     map[string]*fakeEpicTask
	events    []fakeEpicEvent
	executed  []string
	runners   map[string]string
	completes []fakeCompleteCall
	// tokenKey, when set, makes the fake enforce the TK5 token-only
	// transport: every request must carry a parseable run-scoped bearer for
	// wantRunID and no legacy X-Loom-Driver-* identity headers.
	tokenKey   []byte
	wantRunID  string
	violations []string
}

type fakeEpicTask struct {
	deps   []string
	status string // open | claimed | closed | blocked
}

type fakeEpicEvent struct {
	seq  int64
	data map[string]any
}

type fakeCompleteCall struct {
	TaskID       string `json:"taskId"`
	TaskRunID    string `json:"taskRunId"`
	CompletionID string `json:"completionId"`
	LeaseToken   string `json:"leaseToken"`
}

func newFakeEpicAPI(epicID, failTask string) *fakeEpicAPI {
	return &fakeEpicAPI{epicID: epicID, failTask: failTask, tasks: map[string]*fakeEpicTask{}, runners: map[string]string{}}
}

func (f *fakeEpicAPI) addTask(id string, deps ...string) {
	f.order = append(f.order, id)
	f.tasks[id] = &fakeEpicTask{deps: deps, status: "open"}
}

func (f *fakeEpicAPI) observed() ([]string, []fakeCompleteCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.executed...), append([]fakeCompleteCall(nil), f.completes...)
}

func (f *fakeEpicAPI) observedRunners() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.runners))
	for taskID, runner := range f.runners {
		out[taskID] = runner
	}
	return out
}

func (f *fakeEpicAPI) taskStatus(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if task, ok := f.tasks[id]; ok {
		return task.status
	}
	return ""
}

func (f *fakeEpicAPI) debugState() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	state := map[string]any{"executed": f.executed, "completes": f.completes, "events": len(f.events)}
	statuses := map[string]string{}
	for id, task := range f.tasks {
		statuses[id] = task.status
	}
	state["tasks"] = statuses
	encoded, _ := json.Marshal(state)
	return string(encoded)
}

// requireRunToken switches the fake into token-only enforcement for one run.
func (f *fakeEpicAPI) requireRunToken(key []byte, runID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenKey = append([]byte(nil), key...)
	f.wantRunID = runID
	f.violations = nil
}

func (f *fakeEpicAPI) authViolations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.violations...)
}

func (f *fakeEpicAPI) recordViolation(r *http.Request, detail string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.violations = append(f.violations, r.Method+" "+r.URL.Path+": "+detail)
}

// verifyTokenOnlyAuth mirrors the serve-side TK2 contract: a token-only call
// presents Authorization: Bearer <run token> and nothing else — any legacy
// X-Loom-Driver-* identity header alongside it is an identity violation.
func (f *fakeEpicAPI) verifyTokenOnlyAuth(r *http.Request) {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-loom-driver-") {
			f.recordViolation(r, "legacy identity header "+name+" present under token-only transport")
		}
	}
	claims, err := f.bearerRunClaims(r)
	if err != nil {
		f.recordViolation(r, err.Error())
		return
	}
	if claims.RunID != f.wantRunID {
		f.recordViolation(r, "token run id "+claims.RunID+", want "+f.wantRunID)
	}
}

func (f *fakeEpicAPI) bearerRunClaims(r *http.Request) (*RunTokenClaims, error) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("missing run token bearer (Authorization=%q)", auth)
	}
	return ParseRunToken(strings.TrimSpace(token), f.tokenKey)
}

// driverRunID resolves the calling run's identity: from the run token claims
// under token-only enforcement, from the legacy identity header otherwise.
func (f *fakeEpicAPI) driverRunID(r *http.Request) string {
	if len(f.tokenKey) == 0 {
		return r.Header.Get("X-Loom-Driver-Run-Id")
	}
	claims, err := f.bearerRunClaims(r)
	if err != nil {
		return ""
	}
	return claims.RunID
}

func (f *fakeEpicAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(f.tokenKey) > 0 {
		f.verifyTokenOnlyAuth(r)
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/driver/watch/epic") {
		f.handleWatch(w, r)
		return
	}
	op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	var params map[string]any
	_ = json.NewDecoder(r.Body).Decode(&params)
	switch op {
	case "epic-get":
		writeFakeJSON(w, map[string]any{"id": f.epicID, "issue_type": "epic"})
	case "claim-ready":
		writeFakeJSON(w, f.claimReady())
	case "exec-task":
		writeFakeJSON(w, f.execTask(r, params))
	case "complete-task":
		f.recordComplete(params)
		writeFakeError(w, http.StatusConflict, "invalid_transition", "task run already completed by worker")
	case "release-task":
		f.releaseTask(params)
		writeFakeJSON(w, map[string]any{"id": stringParam(params, "taskId"), "released": true})
	case "epic-snapshot":
		writeFakeJSON(w, f.snapshot())
	case "active-task-runs":
		writeFakeJSON(w, map[string]any{"driverRunId": f.driverRunID(r), "activeCount": 0})
	default:
		writeFakeError(w, http.StatusNotFound, "unknown_op", "unknown driver op "+op)
	}
}

func (f *fakeEpicAPI) claimReady() any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.order {
		task := f.tasks[id]
		if task.status != "open" || !f.depsClosedLocked(task) {
			continue
		}
		task.status = "claimed"
		return map[string]any{"id": id, "title": id}
	}
	return nil
}

func (f *fakeEpicAPI) depsClosedLocked(task *fakeEpicTask) bool {
	for _, dep := range task.deps {
		if f.tasks[dep] == nil || f.tasks[dep].status != "closed" {
			return false
		}
	}
	return true
}

// execTask mimics enqueue + instant worker execution: the task transitions to
// closed (or blocked for the configured failure) and the terminal journal
// event is appended for the watch stream before the queued response returns.
func (f *fakeEpicAPI) execTask(r *http.Request, params map[string]any) any {
	taskID := stringParam(params, "taskId")
	taskRunID := stringParam(params, "taskRunId")
	driverRunID := f.driverRunID(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed = append(f.executed, taskID)
	f.runners[taskID] = stringParam(params, "runner")
	event := map[string]any{
		"driverRunID": driverRunID,
		"epicID":      f.epicID,
		"taskID":      taskID,
		"taskRunID":   taskRunID,
		"attempt":     0,
		"logsRef":     "logs://" + taskRunID,
	}
	if task := f.tasks[taskID]; task != nil {
		if taskID == f.failTask {
			task.status = "blocked"
			event["type"] = "taskRunFailed"
			event["status"] = "failed"
			event["schedulerState"] = "blocked"
			event["errorClass"] = "injected_task_failure"
			event["errorMessage"] = "deliberate failure injected by fake epic API"
		} else {
			task.status = "closed"
			event["type"] = "taskRunCompleted"
			event["status"] = "completed"
		}
	}
	f.events = append(f.events, fakeEpicEvent{seq: int64(len(f.events) + 1), data: event})
	return map[string]any{"id": taskRunID, "taskRunId": taskRunID, "taskId": taskID, "status": "queued"}
}

func (f *fakeEpicAPI) recordComplete(params map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completes = append(f.completes, fakeCompleteCall{
		TaskID:       stringParam(params, "taskId"),
		TaskRunID:    stringParam(params, "taskRunId"),
		CompletionID: stringParam(params, "completionId"),
		LeaseToken:   stringParam(params, "leaseToken"),
	})
}

func (f *fakeEpicAPI) releaseTask(params map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if task := f.tasks[stringParam(params, "taskId")]; task != nil && task.status == "claimed" {
		task.status = "open"
	}
}

func (f *fakeEpicAPI) snapshot() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotLocked()
}

func (f *fakeEpicAPI) snapshotLocked() map[string]any {
	ready := []map[string]any{}
	blocked := []map[string]any{}
	open := []map[string]any{}
	for _, id := range f.order {
		task := f.tasks[id]
		if task.status == "closed" {
			continue
		}
		summary := map[string]any{"id": id, "title": id, "status": task.status}
		open = append(open, summary)
		if task.status == "open" {
			if f.depsClosedLocked(task) {
				ready = append(ready, summary)
			} else {
				blocked = append(blocked, summary)
			}
		}
	}
	return map[string]any{
		"epicId":            f.epicID,
		"readyCount":        len(ready),
		"blockedCount":      len(blocked),
		"openChildrenCount": len(open),
		"ready":             ready,
		"blocked":           blocked,
		"openChildren":      open,
	}
}

func (f *fakeEpicAPI) handleWatch(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeFakeError(w, http.StatusInternalServerError, "internal", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	cursor, _ := strconv.ParseInt(strings.TrimSpace(r.Header.Get("Last-Event-ID")), 10, 64)
	f.mu.Lock()
	snapshot := map[string]any{"epic": f.snapshotLocked(), "active": map[string]any{"activeCount": 0}}
	f.mu.Unlock()
	if writeFakeSSE(w, flusher, cursor, "snapshot", snapshot) != nil {
		return
	}
	for {
		f.mu.Lock()
		pending := []fakeEpicEvent{}
		for _, event := range f.events {
			if event.seq > cursor {
				pending = append(pending, event)
			}
		}
		f.mu.Unlock()
		for _, event := range pending {
			if writeFakeSSE(w, flusher, event.seq, "taskRun", event.data) != nil {
				return
			}
			cursor = event.seq
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func writeFakeSSE(w http.ResponseWriter, flusher http.Flusher, id int64, event string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, encoded); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeFakeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeFakeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message, "retryable": false},
	})
}

func stringParam(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return value
}

func writeRealFlueProject(t *testing.T, root, workflowName, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	sdkRoot, err := filepath.Abs("../../sdk")
	if err != nil {
		t.Fatalf("resolve sdk root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sdkRoot, "package.json")); err != nil {
		t.Fatalf("local @loom/sdk package not found at %s: %v", sdkRoot, err)
	}
	loomScope := filepath.Join(root, "node_modules", "@loom")
	if err := os.MkdirAll(loomScope, 0o755); err != nil {
		t.Fatalf("mkdir node_modules/@loom: %v", err)
	}
	if err := os.Symlink(sdkRoot, filepath.Join(loomScope, "sdk")); err != nil {
		t.Fatalf("link @loom/sdk: %v", err)
	}
	// The Flue server entrypoint resolves runtime deps (e.g. @hono/node-server)
	// through the runtime package, so link it like the e2e harness does.
	flueScope := filepath.Join(root, "node_modules", "@flue")
	if err := os.MkdirAll(flueScope, 0o755); err != nil {
		t.Fatalf("mkdir node_modules/@flue: %v", err)
	}
	if err := os.Symlink(flueRuntimeRootForTest(t), filepath.Join(flueScope, "runtime")); err != nil {
		t.Fatalf("link @flue/runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", workflowName+".ts"), []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

// flueRuntimeRootForTest resolves the @flue/runtime package: FLUE_REPO env
// first, then the sibling flue checkout next to this repo.
func flueRuntimeRootForTest(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if repo := strings.TrimSpace(os.Getenv("FLUE_REPO")); repo != "" {
		candidates = append(candidates, filepath.Join(repo, "packages", "runtime"))
	}
	if sibling, err := filepath.Abs(filepath.Join("..", "..", "..", "flue", "packages", "runtime")); err == nil {
		candidates = append(candidates, sibling)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate
		}
	}
	t.Skipf("@flue/runtime package not found (set FLUE_REPO); tried %v", candidates)
	return ""
}

func buildRealFlueProject(t *testing.T, root string, flueCommand []string) {
	t.Helper()
	outputDir := filepath.Join(root, "dist")
	args := append(append([]string{}, flueCommand[1:]...), "build", "--target", "node", "--root", root, "--output", outputDir)
	cmd := exec.Command(flueCommand[0], args...) //nolint:gosec // test command is operator-provided.
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Flue build %q failed: %v\n%s", strings.Join(flueCommand, " "), err, strings.TrimSpace(string(output)))
	}
	if _, err := os.Stat(filepath.Join(outputDir, "server.mjs")); err != nil {
		t.Fatalf("real Flue build missing dist/server.mjs: %v", err)
	}
}

func nodePathForTest(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not available: %v", err)
	}
	return node
}

func realFlueCommandForTest(t *testing.T) []string {
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
	path, err := exec.LookPath("flue")
	if err != nil {
		t.Skip("flue not found on PATH; set LOOM_REAL_FLUE_CMD_JSON or LOOM_REAL_FLUE_CMD")
	}
	return []string{path}
}
