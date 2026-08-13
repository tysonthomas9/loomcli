package leadapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/leadtoken"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/workflows"
)

type fakeIssueBackend struct {
	backend.IssueBackend
	issues     map[string]*backend.IssueDetailData
	err        error
	actors     []string
	workspaces []string
}

func (f *fakeIssueBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	if actor, ok := middleware.ActorFromContext(ctx); ok {
		f.actors = append(f.actors, actor.BackendActor())
	} else {
		f.actors = append(f.actors, "")
	}
	f.workspaces = append(f.workspaces, middleware.WorkspaceFromContext(ctx))
	if f.err != nil {
		return nil, f.err
	}
	issue := f.issues[id]
	if issue == nil {
		return nil, backend.ErrNotFound("Get", "missing")
	}
	clone := *issue
	return &clone, nil
}

type dispatchHarnessOptions struct {
	workspace     string
	createSession bool
	issueBackend  *fakeIssueBackend
	seedRepo      bool
}

func newDispatchHarness(t *testing.T, opts dispatchHarnessOptions) *dataMountHarness {
	t.Helper()
	if opts.workspace == "" {
		opts.workspace = "WS"
	}
	if opts.issueBackend == nil {
		opts.issueBackend = &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{
			"epic-1": dispatchEpic("epic-1", "repo-1"),
		}}
	}
	h := newDataMountHarness(t, dataMountHarnessOptions{
		workspace: opts.workspace, createNode: true, createSession: opts.createSession,
		issueBackend: func(context.Context) backend.IssueBackend { return opts.issueBackend },
	})
	seedEpicRunner(t, h.store, opts.workspace)
	if opts.seedRepo {
		seedDispatchRepo(t, h.store, opts.workspace, "repo-1", "source-1", "git@github.com:octocat/hello.git", "develop")
	}
	return h
}

func dispatchEpic(id, sourceRepo string) *backend.IssueDetailData {
	return &backend.IssueDetailData{IssueData: backend.IssueData{
		ID: id, Title: "Epic", IssueType: "epic", SourceRepo: sourceRepo,
	}}
}

func seedEpicRunner(t *testing.T, st store.Store, ws string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws, DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Name: workflows.BuiltinEpicRunnerWorkflowName, OwnerType: domain.DriverOwnerSystem,
		ActiveVersionID: "epic-version", Status: domain.DriverStatusActive,
		TrustLevel: domain.DriverTrustTrusted,
	}); err != nil {
		t.Fatalf("create epic-runner driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: ws, VersionID: "epic-version", DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		Version: 1, SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create epic-runner version: %v", err)
	}
}

func seedDispatchRepo(t *testing.T, st store.Store, ws, name, sourceID, remote, branch string) {
	t.Helper()
	if _, err := st.Repos().Create(context.Background(), store.RepoCreate{
		WorkspaceKey: ws, Name: name, SourceRepoID: sourceID,
		RemoteURL: remote, DefaultBranch: branch,
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
}

func dispatchToken(t *testing.T, h *dataMountHarness, placement string) string {
	t.Helper()
	return h.token(t, func(c *leadtoken.OccupantClaims) {
		c.PlacementID = placement
		c.Caps = []string{leadtoken.CapLeadDispatch}
	})
}

func dispatchRequest(t *testing.T, h *dataMountHarness, method, path, token, body string) *dispatchHTTPResult {
	t.Helper()
	rec := h.request(t, dataRouteSpec{method: method, path: path}, token, h.workspace, strings.NewReader(body))
	return &dispatchHTTPResult{status: rec.Code, body: rec.Body.String(), header: rec.Header()}
}

type dispatchHTTPResult struct {
	status int
	body   string
	header http.Header
}

func dispatchEpicRun(t *testing.T, h *dataMountHarness, body string) *dispatchHTTPResult {
	t.Helper()
	return dispatchRequest(t, h, http.MethodPost,
		"/api/workspaces/"+h.workspace+"/lead/dispatch/epic-run",
		dispatchToken(t, h, h.placement), body)
}

func requireDispatchCode(t *testing.T, result *dispatchHTTPResult, status int, code string) {
	t.Helper()
	if result.status != status {
		t.Fatalf("status = %d, want %d; body = %s", result.status, status, result.body)
	}
	if code == "" {
		return
	}
	var envelope dataErrorResponse
	if err := json.Unmarshal([]byte(result.body), &envelope); err != nil || envelope.Code != code || envelope.Success {
		t.Fatalf("envelope = %+v, err = %v, want code %q", envelope, err, code)
	}
}

func TestEpicRunDispatch_BuildsPayloadFromServerFacts(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1","maxConcurrency":3,"runner":"daytona-task-runner"}`)
	requireDispatchCode(t, result, http.StatusAccepted, "")
	runs, err := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %v, err = %v", runs, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(runs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"epicId": "epic-1", "leadName": "lead", "orchestratorSessionId": "lead-session",
		"maxConcurrency": float64(3), "intervalSeconds": float64(5),
		"runner": "daytona-task-runner", "requestedBy": "lead-occupant",
		"repoUrl": "https://github.com/octocat/hello", "baseBranch": "develop",
		"openPullRequest": true,
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %#v, want %#v", payload, want)
	}
}

func TestEpicRunPayloadKeysArePinned(t *testing.T) {
	base := epicRunPlan{epicID: "epic-1", leadName: "lead", sessionID: "session-1",
		runner: occupantDefaultRunner, maxConcurrency: 2}
	tests := []struct {
		name string
		plan epicRunPlan
		want []string
	}{
		{"without repo", base, []string{"epicId", "intervalSeconds", "leadName", "maxConcurrency", "orchestratorSessionId", "requestedBy", "runner"}},
		{"with repo", func() epicRunPlan {
			plan := base
			plan.repoURL, plan.baseBranch = "https://github.com/o/r", "main"
			return plan
		}(), []string{"baseBranch", "epicId", "intervalSeconds", "leadName", "maxConcurrency", "openPullRequest", "orchestratorSessionId", "repoUrl", "requestedBy", "runner"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := tt.plan.payload()
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			keys := mapKeys(object)
			if !reflect.DeepEqual(keys, tt.want) {
				t.Fatalf("keys = %v, want %v", keys, tt.want)
			}
		})
	}
}

func TestEpicRunDispatch_IgnoresForgedClientPayloadFields(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1","leadName":"attacker","dryRun":true,"stackedPullRequests":true,"stackLineage":{},"childInput":{},"targetNodeId":"host","workerPrefix":"evil","orchestratorSessionId":"forged","mode":"parallel","githubRepo":"owner/repo","repositoryUrl":"https://github.com/owner/repo","targetBranch":"main","refreshCodexAuth":true}`)
	requireDispatchCode(t, result, http.StatusBadRequest, "invalid")
	runs, err := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("runs = %v, err = %v; want no side effect", runs, err)
	}
	trailing := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	result = dispatchEpicRun(t, trailing, `{"epicId":"epic-1"} {}`)
	requireDispatchCode(t, result, http.StatusBadRequest, "invalid")
	runs, err = trailing.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("trailing-value runs = %v, err = %v; want no side effect", runs, err)
	}
}

func TestEpicRunDispatch_RejectsOversizedBody(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	body := `{"epicId":"` + strings.Repeat("x", maxLeadDataBodyBytes) + `"}`
	result := dispatchEpicRun(t, h, body)
	requireDispatchCode(t, result, http.StatusRequestEntityTooLarge, "too_large")
}

func TestEpicRunDispatch_RefusesNonSandboxRunners(t *testing.T) {
	if !reflect.DeepEqual(allowedOccupantRunners, []string{"daytona-task-runner"}) {
		t.Fatalf("allowed runners = %v", allowedOccupantRunners)
	}
	for _, runner := range []string{"local-task-runner", "openshell-task-runner", "../evil"} {
		t.Run(runner, func(t *testing.T) {
			h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
			result := dispatchEpicRun(t, h, `{"epicId":"epic-1","runner":`+quoted(runner)+`}`)
			requireDispatchCode(t, result, http.StatusBadRequest, "invalid")
		})
	}
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1","runner":""}`), http.StatusAccepted, "")
}

func TestEpicRunDispatch_RefusesNonEpicWorkflowNames(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1","workflow":"other"}`)
	requireDispatchCode(t, result, http.StatusBadRequest, "invalid")
	result = dispatchEpicRun(t, h, `{"epicId":"epic-1"}`)
	requireDispatchCode(t, result, http.StatusAccepted, "")
	runs, _ := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 || runs[0].DriverID != workflows.BuiltinEpicRunnerWorkflowName {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestEpicRunDispatch_ValidatesEpic(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		be := &fakeIssueBackend{err: backend.ErrNotFound("Get", "missing")}
		h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true, issueBackend: be})
		requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"missing"}`), http.StatusNotFound, "not_found")
	})
	t.Run("task rejected", func(t *testing.T) {
		be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{
			"task-1": {IssueData: backend.IssueData{ID: "task-1", IssueType: "task"}},
		}}
		h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true, issueBackend: be})
		requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"task-1"}`), http.StatusBadRequest, "invalid")
	})
	t.Run("canonical workspace and occupant actor", func(t *testing.T) {
		be := &fakeIssueBackend{issues: map[string]*backend.IssueDetailData{"epic-1": dispatchEpic("epic-1", "repo-1")}}
		h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true, issueBackend: be})
		requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
		if !reflect.DeepEqual(be.actors, []string{"lead-occupant:p1"}) || !reflect.DeepEqual(be.workspaces, []string{"WS"}) {
			t.Fatalf("actors/workspaces = %v/%v", be.actors, be.workspaces)
		}
	})
}

func TestEpicRunDispatch_MaxConcurrencyBounds(t *testing.T) {
	for _, value := range []int{0, -1, 5} {
		t.Run(quotedInt(value), func(t *testing.T) {
			h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
			result := dispatchEpicRun(t, h, `{"epicId":"epic-1","maxConcurrency":`+quotedInt(value)+`}`)
			requireDispatchCode(t, result, http.StatusBadRequest, "invalid")
		})
	}
	for _, value := range []int{1, 4} {
		t.Run(quotedInt(value), func(t *testing.T) {
			h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
			result := dispatchEpicRun(t, h, `{"epicId":"epic-1","maxConcurrency":`+quotedInt(value)+`}`)
			requireDispatchCode(t, result, http.StatusAccepted, "")
		})
	}
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
	runs, _ := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	var payload map[string]any
	_ = json.Unmarshal(runs[0].Payload, &payload)
	if payload["maxConcurrency"] != float64(2) {
		t.Fatalf("default maxConcurrency = %v", payload["maxConcurrency"])
	}
}

func TestEpicRunDispatch_SingleFlightGuard(t *testing.T) {
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunSuspendedAwaitingEvent,
	} {
		t.Run(string(status), func(t *testing.T) {
			h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
			seedDriverRunStatus(t, h.store, "WS", "existing", "epic-1", status, "api", "ui")
			result := dispatchEpicRun(t, h, `{"epicId":"epic-1"}`)
			requireDispatchCode(t, result, http.StatusConflict, "epic_run_active")
			if !strings.Contains(result.body, "existing") {
				t.Fatalf("body = %s, want existing run id", result.body)
			}
		})
	}
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunCompleted, domain.DriverRunFailed, domain.DriverRunCancelled, domain.DriverRunNeedsReview,
	} {
		t.Run(string(status), func(t *testing.T) {
			h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
			seedDriverRunStatus(t, h.store, "WS", "old", "epic-1", status, "api", "ui")
			requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
		})
	}
	t.Run("different epic does not block", func(t *testing.T) {
		h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
		seedDriverRunStatus(t, h.store, "WS", "other", "epic-2", domain.DriverRunQueued, "api", "ui")
		requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
	})
}

func TestEpicRunDispatch_RequiresLeadSession(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{seedRepo: true})
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1"}`)
	requireDispatchCode(t, result, http.StatusConflict, "session_absent")
	runs, _ := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if len(runs) != 0 {
		t.Fatalf("runs = %+v, want none", runs)
	}
}

func TestEpicRunDispatch_StampsServerProvenance(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	requireDispatchCode(t, dispatchEpicRun(t, h, `{"epicId":"epic-1"}`), http.StatusAccepted, "")
	runs, _ := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if len(runs) != 1 || runs[0].SourceKind != domain.DriverRunSourceLeadOccupant ||
		runs[0].SourceRef != "lead-occupant:p1" {
		t.Fatalf("run provenance = %+v", runs)
	}
}

func TestEpicRunStatus_OnlyOwnRuns(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	createNodeRecord(t, context.Background(), h.store, "WS", "p2", "lead-two", 7, domain.PlacementStateActive)
	seedDriverRunStatus(t, h.store, "WS", "own", "status-1", domain.DriverRunQueued,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))
	seedDriverRunStatus(t, h.store, "WS", "api", "status-2", domain.DriverRunQueued, "api", "ui")
	seedEpicRunner(t, h.store, "OTHER")
	seedDriverRunStatus(t, h.store, "OTHER", "other-ws", "status-3", domain.DriverRunQueued,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))

	path := "/api/workspaces/WS/lead/dispatch/runs/"
	requireDispatchCode(t, dispatchRequest(t, h, http.MethodGet, path+"own", dispatchToken(t, h, "p1"), ""), http.StatusOK, "")
	requireDispatchCode(t, dispatchRequest(t, h, http.MethodGet, path+"own", dispatchToken(t, h, "p2"), ""), http.StatusNotFound, "not_found")
	for _, placement := range []string{"p1", "p2"} {
		requireDispatchCode(t, dispatchRequest(t, h, http.MethodGet, path+"api", dispatchToken(t, h, placement), ""), http.StatusNotFound, "not_found")
	}
	requireDispatchCode(t, dispatchRequest(t, h, http.MethodGet, path+"other-ws", dispatchToken(t, h, "p1"), ""), http.StatusNotFound, "not_found")
}

func TestEpicRunStatus_ProjectionOmitsPayload(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	seedDriverRunStatus(t, h.store, "WS", "finished", "epic-1", domain.DriverRunFailed,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))
	result := dispatchRequest(t, h, http.MethodGet, "/api/workspaces/WS/lead/dispatch/runs/finished",
		dispatchToken(t, h, "p1"), "")
	requireDispatchCode(t, result, http.StatusOK, "")
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result.body), &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	want := []string{"epicId", "finishedAt", "startedAt", "status", "terminal", "runId"}
	sort.Strings(want)
	if keys := mapKeys(data); !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for _, forbidden := range []string{"payload", "node_id", "lease_id"} {
		if strings.Contains(result.body, forbidden) {
			t.Fatalf("response contains %q: %s", forbidden, result.body)
		}
	}
	if strings.Contains(result.body, "hidden") {
		t.Fatalf("response contains payload value: %s", result.body)
	}
}

func TestEpicRunStatus_TerminalFlagMatchesDomain(t *testing.T) {
	statuses := []domain.DriverRunStatus{
		domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunCompleted,
		domain.DriverRunFailed, domain.DriverRunNeedsReview, domain.DriverRunCancelled,
		domain.DriverRunSuspendedAwaitingEvent,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			if got := newRunStatusView(&domain.DriverRun{Status: status}).Terminal; got != status.IsTerminal() {
				t.Fatalf("terminal = %t, want %t", got, status.IsTerminal())
			}
		})
	}
}

func TestCreateEpicRun_ReconcilesPersistThenError(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	wrapped := storeWithDriverRuns{Store: h.store, runs: &persistThenErrorRuns{DriverRunStore: h.store.DriverRuns()}}
	h.module.store = wrapped
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1"}`)
	requireDispatchCode(t, result, http.StatusAccepted, "")
}

func TestCreateEpicRun_RejectsSamePlacementRaceWithDifferentRunID(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	seedDriverRunStatus(t, h.store, "WS", "earlier", "epic-1", domain.DriverRunQueued,
		domain.DriverRunSourceLeadOccupant, leadtoken.OccupantActor("p1"))
	plan := epicRunPlan{epicID: "epic-1", leadName: "lead", sessionID: "lead-session",
		runner: occupantDefaultRunner, repoURL: "https://github.com/o/r", baseBranch: "main", maxConcurrency: 2}
	_, err := h.module.createEpicRun(context.Background(), "WS", occupantIdentity{
		claims: &leadtoken.OccupantClaims{WorkspaceKey: "WS", PlacementID: "p1"},
	}, plan)
	var statusErr *opStatusError
	if !errors.As(err, &statusErr) || statusErr.code != "epic_run_active" || !strings.Contains(err.Error(), "earlier") {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateEpicRun_LostCreateIsNotPollable(t *testing.T) {
	h := newDispatchHarness(t, dispatchHarnessOptions{createSession: true, seedRepo: true})
	wrapped := storeWithDriverRuns{Store: h.store, runs: &alwaysErrorRuns{DriverRunStore: h.store.DriverRuns()}}
	h.module.store = wrapped
	result := dispatchEpicRun(t, h, `{"epicId":"epic-1"}`)
	requireDispatchCode(t, result, http.StatusInternalServerError, "internal")
	if strings.Contains(result.body, "forced create error") {
		t.Fatalf("internal error leaked: %s", result.body)
	}
	runs, err := h.store.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("lost-create runs = %+v, err = %v; want none", runs, err)
	}
}

type storeWithDriverRuns struct {
	store.Store
	runs store.DriverRunStore
}

func (s storeWithDriverRuns) DriverRuns() store.DriverRunStore { return s.runs }

type persistThenErrorRuns struct{ store.DriverRunStore }

func (s *persistThenErrorRuns) Create(ctx context.Context, in store.DriverRunCreate) (*domain.DriverRun, error) {
	if _, err := s.DriverRunStore.Create(ctx, in); err != nil {
		return nil, err
	}
	return nil, errors.New("forced create error")
}

type alwaysErrorRuns struct{ store.DriverRunStore }

func (*alwaysErrorRuns) Create(context.Context, store.DriverRunCreate) (*domain.DriverRun, error) {
	return nil, errors.New("forced create error")
}

func seedDriverRunStatus(t *testing.T, st store.Store, ws, runID, epicID string,
	status domain.DriverRunStatus, sourceKind, sourceRef string,
) *domain.DriverRun {
	t.Helper()
	run, err := st.DriverRuns().Create(context.Background(), store.DriverRunCreate{
		WorkspaceKey: ws, RunID: runID, DriverID: workflows.BuiltinEpicRunnerWorkflowName,
		DriverVersionID: "epic-version", EpicID: epicID,
		SourceKind: sourceKind, SourceRef: sourceRef, Payload: json.RawMessage(`{"secret":"hidden"}`),
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if status == domain.DriverRunQueued {
		return run
	}
	claimed, err := st.DriverRuns().Claim(context.Background(), ws, runID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("claim run: %v", err)
	}
	if status == domain.DriverRunRunning {
		return claimed
	}
	if status == domain.DriverRunSuspendedAwaitingEvent {
		run, err = st.DriverRuns().Suspend(context.Background(), ws, runID, claimed.NodeID,
			claimed.LeaseID, claimed.FencingToken, domain.AwaitInstanceKey(runID, 1))
	} else {
		run, err = st.DriverRuns().Finish(context.Background(), ws, runID, store.DriverRunFinish{
			NodeID: claimed.NodeID, LeaseID: claimed.LeaseID, FencingToken: claimed.FencingToken,
			Status: status,
		})
	}
	if err != nil {
		t.Fatalf("set run status %s: %v", status, err)
	}
	return run
}

func mapKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func quoted(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func quotedInt(value int) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
