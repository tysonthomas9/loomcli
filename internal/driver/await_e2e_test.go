// End-to-end await flows (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW12).
//
// The four canonical flows — approval gate (vet A2), multi-turn re-entry
// (A3), parent/child composition (A5) and the timeout arm — driven through
// the REAL stack end to end: scripted workflow runners speak to the real
// driver-op HTTP API (driverapi module over httptest) with their claimed run
// identity, approvals arrive through the session-authenticated approval
// endpoint with a verified actor, and every suspend/resume crosses executor
// instances (a different node claims each re-entry), proving zero lost
// wakeups across the pending->suspend->resume lifecycle.
//
// External test package: the harness mounts the webui handler modules
// (driverapi, approvals), which import internal/driver.
//
// The suite is store-parameterized: memstore runs unconditionally;
// TestAwaitFlowsE2EFleetDB runs the same scenarios against an ephemeral
// embedded fleet-db behind the LOOM_RUN_EMBEDDED_SMOKE gate (the
// round-trip-suite convention — it needs a freshly built fleet-db binary on
// PATH). Approval-before-registration still depends on a client-side event
// journal append and skips on backends without store.TriggerEventAppender.
// Child-terminal-before-await no longer does: workflows/await registers and
// atomically re-checks child state through the durable AwaitStore contract,
// so the Fleet-backed suite proves that ordering even with no listener.
package driver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
	"github.com/tysonthomas9/loomcli/internal/trigger"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/approvals"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// Test identity headers the harness translates into the verified session
// identity for the approval endpoint (the production auth middleware's job).
const (
	awaitE2EUserHeader  = "X-Test-User"
	awaitE2EEmailHeader = "X-Test-Email"
)

const (
	awaitE2EApprover   = "alice@example.com"
	awaitE2ETimeoutMs  = int64(60_000)
	awaitE2EHumanActor = "human"
	awaitE2EWaitBudget = 5 * time.Second
	awaitE2EWaitPoll   = 20 * time.Millisecond
	awaitE2EShortAwait = int64(250)
)

// awaitE2ELeaseSeq mints unique lease ids across every executor instance the
// suite spins up.
var awaitE2ELeaseSeq atomic.Int64

// awaitFlows is one scenario's harness: a store-backed workspace with a
// registered workflow driver and the real driver-op + approval HTTP surface.
type awaitFlows struct {
	ctx         context.Context
	st          store.Store
	ws          string
	root        string
	api         *httptest.Server
	driverID    string
	execution   *appserve.ExecutionCapability
	runTokenKey []byte
}

func (h *awaitFlows) awaitResolver() store.AtomicAwaitStore {
	return &driver.ExecutionAwaitResolver{
		API: h.execution.DriverRunAPI(), Authorities: h.execution.SystemAuthorityResolver(),
		ComponentID: string(appserve.AwaitEventNotificationComponentID),
	}
}

type awaitE2EClaimPort struct{}

// awaitE2EApprovalAuthorityProvider is the approval endpoint's test-session
// authority bridge. The suite already translates its private identity headers
// into verified middleware identity; this bridge mints only the matching
// short-lived Automation approval authority from that verified actor.
type awaitE2EApprovalAuthorityProvider struct {
	issuer *authority.Issuer
}

func (provider awaitE2EApprovalAuthorityProvider) AuthorityForVerifiedSession(
	ctx context.Context,
	workspace,
	actor string,
) (authority.OperatorAuthority, error) {
	if provider.issuer == nil || ctx == nil || strings.TrimSpace(workspace) == "" ||
		workspace != strings.TrimSpace(workspace) || strings.TrimSpace(actor) == "" || actor != strings.TrimSpace(actor) {
		return authority.OperatorAuthority{}, authority.ErrInvalidScope
	}
	if err := ctx.Err(); err != nil {
		return authority.OperatorAuthority{}, err
	}
	principal, err := provider.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: actor, Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{automation.ActionJournalApproval}, ExpiresAt: time.Now().Add(time.Minute),
	})
	if err != nil {
		return authority.OperatorAuthority{}, err
	}
	return provider.issuer.IssueOperator(principal, workspace, automation.ActionJournalApproval)
}

func (awaitE2EClaimPort) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (awaitE2EClaimPort) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (awaitE2EClaimPort) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (awaitE2EClaimPort) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (awaitE2EClaimPort) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (awaitE2EClaimPort) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

type awaitE2EDriverRunAPI struct {
	execution.DriverRunAPI
	store store.Store
}

func (awaitE2EDriverRunAPI) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	return execution.RecoverTerminalDriverRunWorkResult{ActionID: command.RequestID}, nil
}

func (api awaitE2EDriverRunAPI) StartChildDriverRun(
	ctx context.Context,
	_ authority.ExecutionAuthority,
	command execution.StartChildDriverRunCommand,
) (*execution.DriverRun, error) {
	run, err := driver.StartChildWorkflow(ctx, api.store, driver.StartChildWorkflowOptions{
		WorkspaceKey: command.WorkspaceKey, ParentRunID: command.Owner.ResourceID,
		WorkflowName: command.DriverID, Input: command.Payload, IdempotencyKey: command.ChildKey,
		MaxDepth: command.MaxDepth,
	})
	if err != nil {
		return nil, err
	}
	return &execution.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID,
		DriverVersionID: run.DriverVersionID, Entrypoint: run.Entrypoint,
		SourceKind: run.SourceKind, SourceRef: run.SourceRef, ParentRunID: run.ParentRunID,
		Status: execution.DriverRunStatus(run.Status), Payload: append(json.RawMessage(nil), run.Payload...),
	}, nil
}

func (awaitE2EDriverRunAPI) CascadeChildDriverRuns(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.CascadeChildDriverRunsCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt,
			MaxDepth: command.MaxDepth,
		},
	}, nil
}

func (awaitE2EDriverRunAPI) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey, ParentRunID: command.ParentRunID,
			ParentStatus: command.ParentStatus, Reason: command.Reason,
			ErrorClass: command.ErrorClass, CascadedAt: command.CascadedAt,
			MaxDepth: command.MaxDepth,
		},
	}, nil
}

type awaitE2EWorkflowCatalog struct {
	workflowcatalog.API
	store store.Store
}

func (catalog awaitE2EWorkflowCatalog) GetDriver(ctx context.Context, workspace, driverRef string) (*workflowcatalog.Driver, error) {
	return catalog.store.Drivers().Get(ctx, workspace, driverRef)
}

func (catalog awaitE2EWorkflowCatalog) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	return catalog.store.DriverVersions().Get(ctx, workspace, versionID)
}

func newAwaitFlows(t *testing.T, st store.Store, ws string) *awaitFlows {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	root := t.TempDir()
	writeAwaitFlowsBundle(t, root)
	registered, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: ws, WorkDir: root, DistPath: "dist", DriverName: "await-flows",
		WorkflowName: "await-flows", SourceRef: "workflows/await-flows.ts",
		CreatedBy: "aw12", Activate: true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	runTokenKey := bytes.Repeat([]byte{0x5a}, 32)
	server, executionCapability := newAwaitFlowsServer(t, st, runTokenKey)
	return &awaitFlows{
		ctx: ctx, st: st, ws: ws, root: root, api: server,
		driverID: registered.Driver.DriverID, execution: executionCapability, runTokenKey: runTokenKey,
	}
}

// newAwaitFlowsServer mounts the real driver-op + approval HTTP surface over
// httptest, with a shim that translates the test identity headers into the
// verified session identity (the production auth middleware's job).
func newAwaitFlowsServer(t *testing.T, st store.Store, runTokenKey []byte) (*httptest.Server, *appserve.ExecutionCapability) {
	t.Helper()
	repairs, ok := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		if client, fleet := st.(*fleetdb.Client); fleet {
			repairs, ok = any(client.Execution()).(store.TerminalDriverStepRepairStore)
		}
	}
	if !ok {
		t.Fatal("await E2E store lacks terminal DriverStep repair support")
	}
	executionCapability, err := appserve.NewExecutionCapability(appserve.ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), AgentQueries: testutil.StaticAgentQueries{}, Outbox: st.Outbox(), Awaits: st.Awaits(), TriggerEvents: st.TriggerEvents(), Workspaces: st.Workspaces(),
		AtomicTaskRunRequests: awaitE2EClaimPort{}, AtomicTaskRunClaims: awaitE2EClaimPort{},
		AtomicTaskRunWorkItemDesign: awaitE2EClaimPort{},
		AtomicTaskRunRequeues:       awaitE2EClaimPort{}, AtomicTaskRunRetryExhaustion: awaitE2EClaimPort{},
		AllowLegacyStoreAdapters: true,
	})
	if err != nil {
		t.Fatalf("compose await E2E Execution: %v", err)
	}
	awaitResolver := &driver.ExecutionAwaitResolver{
		API: executionCapability.DriverRunAPI(), Authorities: executionCapability.SystemAuthorityResolver(),
		ComponentID: string(appserve.AwaitEventNotificationComponentID),
	}
	awaitMatcher := trigger.NewAwaitMatcherWithResolver(st.Awaits(), st.DriverRuns(), awaitResolver)
	approvalEvents, ok := st.TriggerEvents().(automation.ApprovalEventStore)
	if !ok {
		t.Fatal("await E2E store lacks Automation approval journal support")
	}
	approvalIssuer := authority.NewIssuer()
	approvalAdmission, err := approvalIssuer.NewAdmission(
		authority.OperatorOnly(automation.ActionJournalApproval),
	)
	if err != nil {
		t.Fatalf("compose await E2E approval admission: %v", err)
	}
	approvalJournal := automation.New(
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, approvalAdmission,
		automation.WithApprovalEventStore(approvalEvents),
	)
	mux := http.NewServeMux()
	driverapi.NewModule(driverapi.Config{
		Store: st, RunTokenKey: runTokenKey,
		Execution:            awaitE2EDriverRunAPI{DriverRunAPI: executionCapability.DriverRunAPI(), store: st},
		ExecutionAuthorities: executionCapability.DriverRunAuthorityResolver(),
		TaskRunRequests:      executionCapability.TaskRunRequestAPI(), TaskRunRecovery: executionCapability.TaskRunRecoveryAPI(),
		TaskRuns: executionCapability.TaskRunAPI(), TaskRunAuthorities: executionCapability.TaskRunAuthorityResolver(),
		WorkflowCatalog: awaitE2EWorkflowCatalog{store: st},
	}).Register(mux)
	approvals.New(approvals.Config{
		Store: st, Awaits: awaitMatcher, Journal: approvalJournal,
		Authority: awaitE2EApprovalAuthorityProvider{issuer: approvalIssuer},
	}).Register(mux)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user := r.Header.Get(awaitE2EUserHeader); user != "" {
			identity := middleware.UserIdentity{UserID: user, Email: r.Header.Get(awaitE2EEmailHeader)}
			r = r.WithContext(middleware.WithUserIdentity(r.Context(), identity))
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, executionCapability
}

// writeAwaitFlowsBundle writes a minimal verifiable Flue dist bundle; the
// scripted runners never execute it, but registration and the executor's
// bundle verification read it like any real workflow bundle.
func writeAwaitFlowsBundle(t *testing.T, root string) {
	t.Helper()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	server := "// await-flows e2e bundle: executed via scripted Go runners only.\n"
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "workflow.mjs"), []byte("export const marker = 'await-flows';\n"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
}

// opRunner adapts a scripted workflow turn into driver.Runner. The function
// IS the workflow: it re-runs from the top on every claim (re-entry) exactly
// like a re-launched bundle.
type opRunner struct {
	fn func(req driver.RunRequest) driver.RunResult
}

func (r opRunner) Run(_ context.Context, req driver.RunRequest) (driver.RunResult, error) {
	return r.fn(req), nil
}

// createRun creates a queued run of the registered workflow driver.
func (h *awaitFlows) createRun(t *testing.T, runID string) *domain.DriverRun {
	t.Helper()
	run, err := driver.CreateDriverRun(h.ctx, h.st, driver.RunOptions{
		WorkspaceKey: h.ws, DriverID: h.driverID, RunID: runID,
	})
	if err != nil {
		t.Fatalf("CreateDriverRun %s: %v", runID, err)
	}
	return run
}

// runExecutorOnce drives one executor instance (a distinct node identity)
// through a single claim->run->settle pass targeting runID.
func (h *awaitFlows) runExecutorOnce(t *testing.T, node, runID string, runner driver.Runner) *driver.ExecutionResult {
	t.Helper()
	driverRuns := awaitE2EDriverRunAPI{DriverRunAPI: h.execution.DriverRunAPI(), store: h.st}
	result, err := (&driver.Executor{
		Store: h.st, WorkspaceKey: h.ws, RunID: runID, WorkDir: h.root,
		NodeID: node, LeaseID: fmt.Sprintf("%s-lease-%d", node, awaitE2ELeaseSeq.Add(1)),
		Runner: runner, HeartbeatInterval: -1, RunTokenKey: h.runTokenKey,
		Execution: driverRuns, RunOutcomeQueue: h.execution.DriverRunOutcomeAPI(), TerminalWorkRecoveryQueue: h.execution.TerminalDriverRunWorkRecoveryQueueAPI(), ExecutionWorkers: h.execution.TaskRunWorkerAPI(),
		ExecutionAuthorities: h.execution.DriverRunAuthorityResolver(), SystemAuthorities: h.execution.SystemAuthorityResolver(),
	}).RunOnce(h.ctx)
	if err != nil {
		t.Fatalf("RunOnce(%s on %s): %v", runID, node, err)
	}
	if result.Skipped || result.Final == nil {
		t.Fatalf("RunOnce(%s on %s) = %+v, want a settled run", runID, node, result)
	}
	return result
}

// requireRun asserts the stored run's status (and resume source when given).
func (h *awaitFlows) requireRun(t *testing.T, runID string, status domain.DriverRunStatus, resumeEventID string) *domain.DriverRun {
	t.Helper()
	run, err := h.st.DriverRuns().Get(h.ctx, h.ws, runID)
	if err != nil {
		t.Fatalf("Get run %s: %v", runID, err)
	}
	if run.Status != status {
		t.Fatalf("run %s status = %s, want %s", runID, run.Status, status)
	}
	if resumeEventID != "" && run.ResumeSourceEventID != resumeEventID {
		t.Fatalf("run %s resumed by %q, want %q", runID, run.ResumeSourceEventID, resumeEventID)
	}
	return run
}

// --- HTTP wire helpers -----------------------------------------------------

// driverOp POSTs one driver op with the claimed run's verified identity.
func (h *awaitFlows) driverOp(t *testing.T, run *domain.DriverRun, runToken, op string, body map[string]any) (int, []byte) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", op, err)
	}
	req, err := http.NewRequest(http.MethodPost,
		h.api.URL+"/api/workspaces/"+h.ws+"/driver/"+op, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build %s request: %v", op, err)
	}
	if strings.TrimSpace(runToken) == "" {
		t.Fatalf("%s runner did not receive a run-scoped bearer token", op)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+runToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", op, err)
	}
	return resp.StatusCode, raw
}

// awaitOpResponse is the camelCase events/await (and workflows/await) wire.
type awaitOpResponse struct {
	Status      string `json:"status"`
	InstanceKey string `json:"instanceKey"`
	Event       *struct {
		ID      string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	} `json:"event"`
	Child *struct {
		RunID      string `json:"runId"`
		Status     string `json:"status"`
		Summary    string `json:"summary"`
		ErrorClass string `json:"errorClass"`
	} `json:"child"`
}

// awaitOp runs one events/await turn for the claimed run.
func (h *awaitFlows) awaitOp(t *testing.T, run *domain.DriverRun, runToken, pattern string, actors []string, timeoutMs int64, index int) awaitOpResponse {
	t.Helper()
	body := map[string]any{"pattern": pattern, "timeoutMs": timeoutMs, "awaitIndex": index}
	if len(actors) > 0 {
		body["actor"] = actors
	}
	status, raw := h.driverOp(t, run, runToken, "events/await", body)
	if status != http.StatusOK {
		t.Fatalf("events/await = %d: %s", status, raw)
	}
	var decoded awaitOpResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode events/await response: %v (%s)", err, raw)
	}
	return decoded
}

// startChild runs one workflows/start for the claimed run.
func (h *awaitFlows) startChild(t *testing.T, run *domain.DriverRun, runToken string, startIndex int) string {
	t.Helper()
	status, raw := h.driverOp(t, run, runToken, "workflows/start", map[string]any{
		"workflowName": h.driverID, "input": map[string]any{"n": 1}, "startIndex": startIndex,
	})
	if status != http.StatusOK {
		t.Fatalf("workflows/start = %d: %s", status, raw)
	}
	var decoded struct {
		ChildRunID string `json:"childRunId"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.ChildRunID == "" {
		t.Fatalf("decode workflows/start response: %v (%s)", err, raw)
	}
	return decoded.ChildRunID
}

// awaitChild runs one workflows/await for the claimed run.
func (h *awaitFlows) awaitChild(t *testing.T, run *domain.DriverRun, runToken, childRunID string, index int) awaitOpResponse {
	t.Helper()
	status, raw := h.driverOp(t, run, runToken, "workflows/await", map[string]any{
		"childRunId": childRunID, "timeoutMs": awaitE2ETimeoutMs, "awaitIndex": index,
	})
	if status != http.StatusOK {
		t.Fatalf("workflows/await = %d: %s", status, raw)
	}
	var decoded awaitOpResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode workflows/await response: %v (%s)", err, raw)
	}
	return decoded
}

// approveAs POSTs one approval decision as the given verified session user.
func (h *awaitFlows) approveAs(t *testing.T, user, email string, body map[string]any) (int, map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal approval body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		h.api.URL+"/api/workspaces/"+h.ws+"/approvals", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build approval request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(awaitE2EUserHeader, user)
	if email != "" {
		req.Header.Set(awaitE2EEmailHeader, email)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST approvals: %v", err)
	}
	defer resp.Body.Close()
	decoded := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode approval response: %v", err)
	}
	return resp.StatusCode, decoded
}

// dispatchEvent feeds one admitted event straight to the dispatch-time await
// matcher — the ingress hook's matcher leg. Deliberately NOT journaled: a
// journaled event on a multi-turn pattern would also satisfy every LATER
// registration on the same key via the registration scan (earliest event
// wins), which is the documented same-pattern behavior; pre-await thread
// buffering is deferred to the Slack agent work per the locked decision.
func (h *awaitFlows) dispatchEvent(t *testing.T, id, eventType, subject, actor string, payload map[string]any) []trigger.AwaitMatchRecord {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event payload: %v", err)
	}
	result, err := trigger.NewAwaitMatcherWithResolver(h.st.Awaits(), h.st.DriverRuns(), h.awaitResolver()).Dispatch(h.ctx, h.ws, trigger.AwaitDispatchEvent{
		EventID: id, EventType: eventType, SubjectRef: subject, ActorRef: actor, Payload: body,
	})
	if err != nil {
		t.Fatalf("Dispatch %s: %v", id, err)
	}
	return result.Records
}

// waitAwaitDue polls until the instance shows up in the due-deadline feed.
func (h *awaitFlows) waitAwaitDue(t *testing.T, instanceKey string) {
	t.Helper()
	deadline := time.Now().Add(awaitE2EWaitBudget)
	for time.Now().Before(deadline) {
		due, err := h.st.Awaits().ListDueAwaitDeadlines(h.ctx, h.ws, time.Now().UTC(), 100)
		if err != nil {
			t.Fatalf("ListDueAwaitDeadlines: %v", err)
		}
		for _, inst := range due {
			if inst.InstanceKey == instanceKey {
				return
			}
		}
		time.Sleep(awaitE2EWaitPoll)
	}
	t.Fatalf("await %s never became due within %s", instanceKey, awaitE2EWaitBudget)
}

// requireJournalAppender gates subcases that pre-journal events client-side.
func requireJournalAppender(t *testing.T, h *awaitFlows) {
	t.Helper()
	if _, ok := h.st.TriggerEvents().(store.TriggerEventAppender); !ok {
		t.Skip("backend has no client-side trigger-event appender: the journal path for unrouted internal/approval events is the AW7/AW8 noted fleet-db gap")
	}
}

// suspendedResult is the runner-side suspension acknowledgment after an
// await op suspended the run server-side.
func suspendedResult() driver.RunResult {
	return driver.RunResult{Status: domain.DriverRunSuspendedAwaitingEvent, Summary: "workflow suspended awaiting event"}
}

func failedResult(reason string) driver.RunResult {
	return driver.RunResult{Status: domain.DriverRunFailed, Summary: reason, ErrorClass: "e2e_unexpected"}
}

// --- suite entry points ----------------------------------------------------

func TestAwaitFlowsE2EMemstore(t *testing.T) {
	runAwaitFlowSuite(t, func(t *testing.T) *awaitFlows {
		return newAwaitFlows(t, memstore.New(), "WS")
	})
}

// TestAwaitFlowsE2EFleetDB runs the same four flows against an ephemeral
// embedded fleet-db through the AW5 HTTP client. Env-gated like the await
// round-trip suite: it needs a fleet-db binary BUILT FROM A TREE THAT
// INCLUDES THE AW2-AW5 AWAIT ROUTES on PATH.
func TestAwaitFlowsE2EFleetDB(t *testing.T) {
	if os.Getenv("LOOM_RUN_EMBEDDED_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_EMBEDDED_SMOKE=1 (with a freshly built fleet-db binary) to run the fleet-db await flows")
	}
	if diag := bootstrap.DiagnoseFleetDBBinary(); diag.Err != nil {
		t.Skipf("fleet-db binary unavailable: %v", diag.Err)
	}
	// The suite's per-scenario HTTP volume exceeds fleet-db's default
	// token-bucket burst; raise the embedded child's limits (the child
	// inherits this process's environment) unless the operator already did.
	for env, value := range map[string]string{
		"FLEET_RATE_LIMIT_RATE":  "5000",
		"FLEET_RATE_LIMIT_BURST": "10000",
	} {
		if os.Getenv(env) == "" {
			t.Setenv(env, value)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	emb, err := bootstrap.StartEmbedded(ctx, t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("StartEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = emb.Stop() })
	client, err := emb.NewClient(fleetdb.Config{Actor: "await-e2e"})
	if err != nil {
		t.Fatalf("fleetdb client: %v", err)
	}
	var wsSeq atomic.Int64
	runAwaitFlowSuite(t, func(t *testing.T) *awaitFlows {
		return newAwaitFlows(t, client, fmt.Sprintf("AWE%d", wsSeq.Add(1)))
	})
}

// runAwaitFlowSuite runs the four canonical flows against one backend. Every
// subtest gets a fresh harness (isolated store or workspace).
func runAwaitFlowSuite(t *testing.T, newHarness func(t *testing.T) *awaitFlows) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, h *awaitFlows)
	}{
		{"ApprovalGate", testApprovalGateFlow},
		{"ApprovalGrantedBeforeRegistration", testApprovalBeforeRegistration},
		{"MultiTurnLoop", testMultiTurnLoopFlow},
		{"Composition", testCompositionFlow},
		{"CompositionChildAlreadyTerminal", testCompositionInlineResolve},
		{"TimeoutArm", testTimeoutArmFlow},
		{"TimeoutRaceExactlyOnce", testTimeoutRaceExactlyOnce},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newHarness(t))
		})
	}
}

// --- flow 1: approval gate (vet A2) ----------------------------------------

// approvalGateRunner is the approval-gate workflow: await one approval on
// the rendered subject key, restricted to the eligible approvers, then
// complete with the recorded decision.
func (h *awaitFlows) approvalGateRunner(t *testing.T, pattern string, approvers []string) driver.Runner {
	return opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		resp := h.awaitOp(t, req.Run, req.RunToken, pattern, approvers, awaitE2ETimeoutMs, 1)
		switch resp.Status {
		case driver.AwaitOutcomeSuspended:
			return suspendedResult()
		case string(domain.AwaitSatisfied):
			summary := "satisfied-by=" + resp.Event.ID
			var payload struct {
				Decision string `json:"decision"`
			}
			if resp.Event != nil && len(resp.Event.Payload) > 0 &&
				json.Unmarshal(resp.Event.Payload, &payload) == nil && payload.Decision != "" {
				summary += " decision=" + payload.Decision
			}
			return driver.RunResult{Status: domain.DriverRunCompleted, Summary: summary}
		case string(domain.AwaitTimedOut):
			return driver.RunResult{Status: domain.DriverRunNeedsReview, Summary: "approval timed out", ErrorClass: "approval_timeout"}
		default:
			return failedResult("unexpected await status " + resp.Status)
		}
	}}
}

func testApprovalGateFlow(t *testing.T, h *awaitFlows) {
	const subject = "acme/widgets#1@shaA"
	pattern := domain.AwaitEventKey(approvals.DefaultApprovalEventType, subject)
	runner := h.approvalGateRunner(t, pattern, []string{awaitE2EApprover})
	h.createRun(t, "run-gate")

	// Turn 1 on executor node-1: the await suspends and the run suspends.
	res1 := h.runExecutorOnce(t, "node-1", "run-gate", runner)
	if res1.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("first pass = %+v, want suspended", res1.Final)
	}

	// RULE 1: approvals for a different repo and a different sha render
	// different subject keys — accepted, journaled, zero pending matches,
	// run untouched.
	for _, miss := range []string{"acme/gadgets#1@shaA", "acme/widgets#1@shaB"} {
		status, decoded := h.approveAs(t, "user-alice", awaitE2EApprover, map[string]any{"subjectRef": miss})
		if status != http.StatusOK || decoded["pendingMatched"] != float64(0) {
			t.Fatalf("near-miss approval %s = %d %v, want 200 with zero pending", miss, status, decoded)
		}
	}
	h.requireRun(t, "run-gate", domain.DriverRunSuspendedAwaitingEvent, "")

	// A suspended run is never re-claimed or stale-swept.
	if _, err := h.st.DriverRuns().Claim(h.ctx, h.ws, "run-gate", "node-thief", "lease-thief"); err == nil {
		t.Fatal("Claim succeeded on a suspended run, want refusal")
	}
	driverRuns := awaitE2EDriverRunAPI{DriverRunAPI: h.execution.DriverRunAPI(), store: h.st}
	if _, err := (&driver.Executor{Store: h.st, WorkspaceKey: h.ws, RunID: "run-gate", WorkDir: h.root,
		NodeID: "node-2", Runner: runner, HeartbeatInterval: -1,
		Execution: driverRuns, RunOutcomeQueue: h.execution.DriverRunOutcomeAPI(), TerminalWorkRecoveryQueue: h.execution.TerminalDriverRunWorkRecoveryQueueAPI(), ExecutionWorkers: h.execution.TaskRunWorkerAPI(),
		ExecutionAuthorities: h.execution.DriverRunAuthorityResolver(), SystemAuthorities: h.execution.SystemAuthorityResolver(),
	}).RunOnce(h.ctx); !errors.Is(err, driver.ErrNoQueuedRun) {
		t.Fatalf("RunOnce on suspended run err = %v, want ErrNoQueuedRun", err)
	}
	recovered, err := (&driver.Executor{
		Store: h.st, WorkspaceKey: h.ws, Execution: h.execution.DriverRunAPI(),
		SystemAuthorities: h.execution.SystemAuthorityResolver(),
	}).RecoverStaleOnce(h.ctx)
	if err != nil || recovered.Recovered != 0 {
		t.Fatalf("RecoverStaleOnce = %+v, %v; want zero recovered (suspended runs are not stale)", recovered, err)
	}
	h.requireRun(t, "run-gate", domain.DriverRunSuspendedAwaitingEvent, "")

	// RULE 4: a verified but ineligible session actor is refused; nothing
	// resolves, nothing resumes.
	status, decoded := h.approveAs(t, "user-mallory", "mallory@example.com", map[string]any{"subjectRef": subject})
	if status != http.StatusForbidden {
		t.Fatalf("ineligible approval = %d %v, want 403", status, decoded)
	}
	h.requireRun(t, "run-gate", domain.DriverRunSuspendedAwaitingEvent, "")
	if pending, err := h.st.Awaits().ListAwaitsByPattern(h.ctx, h.ws, pattern); err != nil || len(pending) != 1 {
		t.Fatalf("pending awaits = %+v, %v; want the gate still suspended", pending, err)
	}

	// The eligible approver resolves the await and re-queues the run.
	status, decoded = h.approveAs(t, "user-alice", awaitE2EApprover, map[string]any{"subjectRef": subject, "note": "ship it"})
	if status != http.StatusOK || decoded["pendingMatched"] != float64(1) {
		t.Fatalf("approval = %d %v, want 200 with one pending match", status, decoded)
	}
	eventID, _ := decoded["eventId"].(string)
	h.requireRun(t, "run-gate", domain.DriverRunQueued, eventID)

	// A SECOND executor instance claims the resumed run; the replayed await
	// returns the recorded decision inline and the run completes.
	res2 := h.runExecutorOnce(t, "node-2", "run-gate", runner)
	if res2.Final.Status != domain.DriverRunCompleted ||
		!strings.Contains(res2.Final.Summary, "satisfied-by="+eventID) ||
		!strings.Contains(res2.Final.Summary, "decision=approved") {
		t.Fatalf("final = %+v, want completed with the approval decision", res2.Final)
	}
	if res2.Claimed.NodeID != "node-2" {
		t.Fatalf("resumed run claimed by %q, want node-2 (second executor)", res2.Claimed.NodeID)
	}
}

// RULE 2 (lost-wakeup proof): an approval granted BEFORE the workflow
// registers its await is found by the registration scan — the run completes
// on its very first pass without ever suspending.
func testApprovalBeforeRegistration(t *testing.T, h *awaitFlows) {
	requireJournalAppender(t, h)
	const subject = "acme/widgets#2@shaB"
	status, decoded := h.approveAs(t, "user-alice", awaitE2EApprover, map[string]any{"subjectRef": subject})
	if status != http.StatusOK || decoded["pendingMatched"] != float64(0) {
		t.Fatalf("pre-approval = %d %v, want 200 with zero pending", status, decoded)
	}
	eventID, _ := decoded["eventId"].(string)

	pattern := domain.AwaitEventKey(approvals.DefaultApprovalEventType, subject)
	runner := h.approvalGateRunner(t, pattern, []string{awaitE2EApprover})
	h.createRun(t, "run-pre")
	res := h.runExecutorOnce(t, "node-1", "run-pre", runner)
	if res.Final.Status != domain.DriverRunCompleted ||
		!strings.Contains(res.Final.Summary, "satisfied-by="+eventID) {
		t.Fatalf("final = %+v, want completed inline by pre-granted approval %s", res.Final, eventID)
	}
	inst, err := h.st.Awaits().GetSatisfiedAwait(h.ctx, h.ws, domain.AwaitInstanceKey("run-pre", 1))
	if err != nil || inst.Status != domain.AwaitSatisfied || inst.SatisfiedByEventID != eventID {
		t.Fatalf("satisfied row = %+v, %v; want satisfied by %s", inst, err, eventID)
	}
}

// --- flow 2: multi-turn loop (A3) ------------------------------------------

// testMultiTurnLoopFlow: three sequential awaits on the SAME thread pattern.
// The executor "dies" between turns (each resume is claimed by a different
// node); re-entry fast-forwards the finished turns from satisfied history
// (RULE 3: awaitIndex = call order) and suspends the next one. The bot's own
// message never resumes the loop (RULE 4 self-trigger guard).
func testMultiTurnLoopFlow(t *testing.T, h *awaitFlows) {
	thread := domain.AwaitEventKey("slack.message", "thread-9")
	var invocations [][]string
	runner := opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		var texts []string
		for turn := 1; turn <= 3; turn++ {
			resp := h.awaitOp(t, req.Run, req.RunToken, thread, []string{awaitE2EHumanActor}, awaitE2ETimeoutMs, turn)
			if resp.Status == driver.AwaitOutcomeSuspended {
				invocations = append(invocations, texts)
				return suspendedResult()
			}
			if resp.Status != string(domain.AwaitSatisfied) || resp.Event == nil {
				return failedResult("unexpected await status " + resp.Status)
			}
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(resp.Event.Payload, &payload); err != nil {
				return failedResult("decode turn payload: " + err.Error())
			}
			texts = append(texts, payload.Text)
		}
		invocations = append(invocations, texts)
		return driver.RunResult{Status: domain.DriverRunCompleted, Summary: strings.Join(texts, ",")}
	}}

	h.createRun(t, "run-loop")
	res := h.runExecutorOnce(t, "node-a", "run-loop", runner)
	if res.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("turn 1 pass = %+v, want suspended", res.Final)
	}

	// Self-trigger guard: the bot's own message is actor-rejected and the
	// loop stays suspended.
	records := h.dispatchEvent(t, "msg-bot", "slack.message", "thread-9", "loom-bot", map[string]any{"text": "bot-echo"})
	if len(records) != 1 || records[0].Outcome != trigger.AwaitMatchActorRejected {
		t.Fatalf("bot dispatch records = %+v, want one actor_rejected", records)
	}
	h.requireRun(t, "run-loop", domain.DriverRunSuspendedAwaitingEvent, "")

	// Three human messages, each resumed by a DIFFERENT executor node.
	steps := []struct {
		eventID string
		text    string
		node    string
	}{
		{"msg-1", "m1", "node-b"},
		{"msg-2", "m2", "node-c"},
		{"msg-3", "m3", "node-d"},
	}
	for i, step := range steps {
		records := h.dispatchEvent(t, step.eventID, "slack.message", "thread-9", awaitE2EHumanActor, map[string]any{"text": step.text})
		if len(records) != 1 || records[0].Outcome != trigger.AwaitMatchResolved {
			t.Fatalf("dispatch %s records = %+v, want one resolved", step.eventID, records)
		}
		h.requireRun(t, "run-loop", domain.DriverRunQueued, step.eventID)
		res := h.runExecutorOnce(t, step.node, "run-loop", runner)
		wantStatus := domain.DriverRunSuspendedAwaitingEvent
		if i == len(steps)-1 {
			wantStatus = domain.DriverRunCompleted
		}
		if res.Final.Status != wantStatus {
			t.Fatalf("pass after %s = %+v, want %s", step.eventID, res.Final, wantStatus)
		}
	}
	final := h.requireRun(t, "run-loop", domain.DriverRunCompleted, "")
	if final.Summary != "m1,m2,m3" {
		t.Fatalf("summary = %q, want the three turns in order", final.Summary)
	}

	// RULE 3 fast-forward proof: every re-entry replayed exactly the turns
	// already satisfied (in order) before registrationing the next index.
	want := [][]string{{}, {"m1"}, {"m1", "m2"}, {"m1", "m2", "m3"}}
	if len(invocations) != len(want) {
		t.Fatalf("invocations = %d (%v), want %d", len(invocations), invocations, len(want))
	}
	for i, texts := range want {
		if strings.Join(invocations[i], ",") != strings.Join(texts, ",") {
			t.Fatalf("invocation %d saw %v, want %v (fast-forward from satisfied history)", i, invocations[i], texts)
		}
	}
	awaits, err := driver.ListRunAwaits(h.ctx, h.st.Awaits(), h.ws, "run-loop")
	if err != nil || len(awaits) != 3 {
		t.Fatalf("ListRunAwaits = %d (%v), want the three turn awaits", len(awaits), err)
	}
	for i, inst := range awaits {
		if inst.Status != domain.AwaitSatisfied || inst.SatisfiedByEventID != steps[i].eventID {
			t.Fatalf("await %d = %+v, want satisfied by %s", i+1, inst, steps[i].eventID)
		}
	}
}

// --- flow 3: composition (A5) ----------------------------------------------

// compositionParentRunner starts the deterministic child and awaits its
// run.finished, recording each invocation's child id.
func (h *awaitFlows) compositionParentRunner(t *testing.T, childIDs *[]string) driver.Runner {
	return opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		childID := h.startChild(t, req.Run, req.RunToken, 1)
		*childIDs = append(*childIDs, childID)
		resp := h.awaitChild(t, req.Run, req.RunToken, childID, 1)
		switch resp.Status {
		case driver.AwaitOutcomeSuspended:
			return suspendedResult()
		case string(domain.AwaitSatisfied):
			if resp.Child == nil {
				return failedResult("satisfied workflows/await without child outcome")
			}
			return driver.RunResult{Status: domain.DriverRunCompleted,
				Summary: "child=" + resp.Child.Status + ":" + resp.Child.Summary}
		default:
			return failedResult("unexpected workflows/await status " + resp.Status)
		}
	}}
}

func (h *awaitFlows) childRunner() driver.Runner {
	return opRunner{fn: func(driver.RunRequest) driver.RunResult {
		return driver.RunResult{Status: domain.DriverRunCompleted, Summary: "child-done"}
	}}
}

func testCompositionFlow(t *testing.T, h *awaitFlows) {
	var childIDs []string
	parent := h.compositionParentRunner(t, &childIDs)
	h.createRun(t, "run-parent")

	// Parent starts the child and suspends on its run.finished.
	res1 := h.runExecutorOnce(t, "node-p1", "run-parent", parent)
	if res1.Final.Status != domain.DriverRunSuspendedAwaitingEvent || len(childIDs) != 1 {
		t.Fatalf("parent pass 1 = %+v (children %v), want suspended after one start", res1.Final, childIDs)
	}
	childID := childIDs[0]
	child := h.requireRun(t, childID, domain.DriverRunQueued, "")
	if child.ParentRunID != "run-parent" {
		t.Fatalf("child parent = %q, want run-parent", child.ParentRunID)
	}

	// A separate executor completes the child; run.finished resumes the
	// parent (no internal binding configured — composition is
	// binding-independent).
	resChild := h.runExecutorOnce(t, "node-c1", childID, h.childRunner())
	if resChild.Final.Status != domain.DriverRunCompleted {
		t.Fatalf("child final = %+v, want completed", resChild.Final)
	}
	finishedEventID := driver.RunFinishedEventID(childID, domain.DriverRunCompleted)
	h.requireRun(t, "run-parent", domain.DriverRunQueued, finishedEventID)

	// Parent re-entry on a SECOND executor: the start replays the SAME
	// deterministic child (no duplicate) and the await replays inline.
	res2 := h.runExecutorOnce(t, "node-p2", "run-parent", parent)
	if res2.Final.Status != domain.DriverRunCompleted || res2.Final.Summary != "child=completed:child-done" {
		t.Fatalf("parent final = %+v, want completed with the child outcome", res2.Final)
	}
	if len(childIDs) != 2 || childIDs[1] != childID {
		t.Fatalf("child ids across re-entry = %v, want the same deterministic child", childIDs)
	}
	runs, err := h.st.DriverRuns().List(h.ctx, h.ws, store.DriverRunFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	children := 0
	for _, run := range runs {
		if run.ParentRunID == "run-parent" {
			children++
		}
	}
	if children != 1 {
		t.Fatalf("child runs of run-parent = %d, want exactly one (deterministic child id)", children)
	}
}

// testCompositionInlineResolve: a child that reaches terminal BEFORE the
// parent registers its await resolves inline from terminal child state (RULE
// 2 for composition) — the parent never suspends on the child.
func testCompositionInlineResolve(t *testing.T, h *awaitFlows) {
	gate := domain.AwaitEventKey("gate.event", "parent2-go")
	var childIDs []string
	suspendedOnChild := false
	parent := opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		childID := h.startChild(t, req.Run, req.RunToken, 1)
		childIDs = append(childIDs, childID)
		gateResp := h.awaitOp(t, req.Run, req.RunToken, gate, nil, awaitE2ETimeoutMs, 1)
		if gateResp.Status == driver.AwaitOutcomeSuspended {
			return suspendedResult()
		}
		if gateResp.Status != string(domain.AwaitSatisfied) {
			return failedResult("unexpected gate status " + gateResp.Status)
		}
		resp := h.awaitChild(t, req.Run, req.RunToken, childID, 2)
		if resp.Status == driver.AwaitOutcomeSuspended {
			suspendedOnChild = true
			return suspendedResult()
		}
		if resp.Status != string(domain.AwaitSatisfied) || resp.Child == nil {
			return failedResult("unexpected workflows/await status " + resp.Status)
		}
		return driver.RunResult{Status: domain.DriverRunCompleted, Summary: "inline-child=" + resp.Child.Status}
	}}

	h.createRun(t, "run-parent2")
	res1 := h.runExecutorOnce(t, "node-p1", "run-parent2", parent)
	if res1.Final.Status != domain.DriverRunSuspendedAwaitingEvent || len(childIDs) != 1 {
		t.Fatalf("parent pass 1 = %+v (children %v), want suspended on the gate", res1.Final, childIDs)
	}
	childID := childIDs[0]

	// The child finishes while the parent is still suspended on the unrelated
	// gate. No internal.run.finished binding is configured.
	if res := h.runExecutorOnce(t, "node-c1", childID, h.childRunner()); res.Final.Status != domain.DriverRunCompleted {
		t.Fatalf("child final = %+v, want completed", res.Final)
	}
	records := h.dispatchEvent(t, "gate-1", "gate.event", "parent2-go", awaitE2EHumanActor, map[string]any{"go": true})
	if len(records) != 1 || records[0].Outcome != trigger.AwaitMatchResolved {
		t.Fatalf("gate dispatch records = %+v, want one resolved", records)
	}

	// Re-entry: the child await registers, re-checks terminal child state, and
	// satisfies INLINE; the parent completes in this same pass.
	res2 := h.runExecutorOnce(t, "node-p2", "run-parent2", parent)
	if res2.Final.Status != domain.DriverRunCompleted || res2.Final.Summary != "inline-child=completed" {
		t.Fatalf("parent final = %+v, want completed inline", res2.Final)
	}
	if suspendedOnChild {
		t.Fatal("parent suspended on the already-terminal child, want inline resolve (lost wakeup)")
	}
	inst, err := h.st.Awaits().GetSatisfiedAwait(h.ctx, h.ws, domain.AwaitInstanceKey("run-parent2", 2))
	if err != nil || inst.SatisfiedByEventID != driver.RunFinishedEventID(childID, domain.DriverRunCompleted) {
		t.Fatalf("child await row = %+v, %v; want satisfied by deterministic run.finished", inst, err)
	}
}

// --- flow 4: timeout arm (RULE 5) ------------------------------------------

// timeoutArmRunner awaits an approval that never comes and lands on its
// timeout arm with needs_review.
func (h *awaitFlows) timeoutArmRunner(t *testing.T, pattern string, timeoutMs int64) driver.Runner {
	return opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		resp := h.awaitOp(t, req.Run, req.RunToken, pattern, []string{awaitE2EApprover}, timeoutMs, 1)
		switch resp.Status {
		case driver.AwaitOutcomeSuspended:
			return suspendedResult()
		case string(domain.AwaitTimedOut):
			var payload struct {
				Timeout   bool   `json:"timeout"`
				EventType string `json:"eventType"`
			}
			if resp.Event == nil || json.Unmarshal(resp.Event.Payload, &payload) != nil || !payload.Timeout {
				return failedResult("timed_out await without the timeout payload")
			}
			return driver.RunResult{Status: domain.DriverRunNeedsReview,
				Summary: "timeout-arm:" + payload.EventType, ErrorClass: "approval_timeout"}
		default:
			return failedResult("unexpected await status " + resp.Status)
		}
	}}
}

func testTimeoutArmFlow(t *testing.T, h *awaitFlows) {
	pattern := domain.AwaitEventKey(approvals.DefaultApprovalEventType, "never#1@shaX")
	runner := h.timeoutArmRunner(t, pattern, awaitE2EShortAwait)
	h.createRun(t, "run-timeout")

	res1 := h.runExecutorOnce(t, "node-t1", "run-timeout", runner)
	if res1.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("first pass = %+v, want suspended", res1.Final)
	}
	key := domain.AwaitInstanceKey("run-timeout", 1)
	h.waitAwaitDue(t, key)

	// The sweeper resumes the run with the synthetic timeout event — the
	// restrictive allow-list does not block the carve-out — and NEVER
	// terminalizes the run itself.
	sweeper := &driver.AwaitTimeoutSweeper{Store: h.st, Resolver: h.awaitResolver(), WorkspaceKey: h.ws}
	swept, err := sweeper.RunOnce(h.ctx)
	if err != nil || swept.TimedOut != 1 {
		t.Fatalf("sweep = %+v, %v; want one timed-out instance", swept, err)
	}
	h.requireRun(t, "run-timeout", domain.DriverRunQueued, domain.AwaitTimeoutEventID(key))

	// A second executor lands the run on its timeout arm.
	res2 := h.runExecutorOnce(t, "node-t2", "run-timeout", runner)
	if res2.Final.Status != domain.DriverRunNeedsReview ||
		res2.Final.Summary != "timeout-arm:"+approvals.DefaultApprovalEventType+".timeout" {
		t.Fatalf("final = %+v, want needs_review on the timeout arm", res2.Final)
	}

	// Replays are idempotent: a second sweep emits nothing.
	swept2, err := sweeper.RunOnce(h.ctx)
	if err != nil || swept2.TimedOut != 0 || swept2.AlreadySatisfied != 0 || swept2.Failed != 0 {
		t.Fatalf("second sweep = %+v, %v; want all zero", swept2, err)
	}
}

// testTimeoutRaceExactlyOnce: an event racing the deadline sweep resolves the
// await exactly once, in both interleavings (RULE 5 proof).
func testTimeoutRaceExactlyOnce(t *testing.T, h *awaitFlows) {
	pattern := domain.AwaitEventKey("race.event", "subject-1")
	sweeper := &driver.AwaitTimeoutSweeper{Store: h.st, Resolver: h.awaitResolver(), WorkspaceKey: h.ws}
	runner := opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		resp := h.awaitOp(t, req.Run, req.RunToken, pattern, nil, awaitE2EShortAwait, 1)
		if resp.Status == driver.AwaitOutcomeSuspended {
			return suspendedResult()
		}
		return failedResult("unexpected await status " + resp.Status)
	}}

	suspendAndAwaitDue := func(runID string) string {
		h.createRun(t, runID)
		res := h.runExecutorOnce(t, "node-r-"+runID, runID, runner)
		if res.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
			t.Fatalf("suspend %s = %+v, want suspended", runID, res.Final)
		}
		key := domain.AwaitInstanceKey(runID, 1)
		h.waitAwaitDue(t, key)
		return key
	}

	// Interleaving 1: the real event lands just before the sweep. The await
	// is satisfied, the run resumes once, and the sweep finds nothing left.
	key1 := suspendAndAwaitDue("run-race1")
	records := h.dispatchEvent(t, "race-evt-1", "race.event", "subject-1", awaitE2EHumanActor, map[string]any{"won": "event"})
	if len(records) != 1 || records[0].Outcome != trigger.AwaitMatchResolved {
		t.Fatalf("event dispatch records = %+v, want one resolved", records)
	}
	swept, err := sweeper.RunOnce(h.ctx)
	if err != nil || swept.TimedOut != 0 || swept.Failed != 0 {
		t.Fatalf("sweep after event = %+v, %v; want nothing to time out", swept, err)
	}
	h.requireRun(t, "run-race1", domain.DriverRunQueued, "race-evt-1")
	if inst, err := h.st.Awaits().GetSatisfiedAwait(h.ctx, h.ws, key1); err != nil ||
		inst.Status != domain.AwaitSatisfied || inst.SatisfiedByEventID != "race-evt-1" {
		t.Fatalf("race1 row = %+v, %v; want satisfied by the event exactly once", inst, err)
	}

	// Interleaving 2: the sweep wins; the late event is a structural no-op
	// (the timed-out instance already left the pending pattern index, so
	// the matcher finds no candidate) and the standing timeout resolution
	// is untouched. The mid-flight resolve race — where a candidate is
	// found but ResolveAwait replays Resume=false as already_resolved — is
	// pinned by the AW7 matcher suite under -race.
	key2 := suspendAndAwaitDue("run-race2")
	swept, err = sweeper.RunOnce(h.ctx)
	if err != nil || swept.TimedOut != 1 {
		t.Fatalf("sweep = %+v, %v; want the race2 instance timed out", swept, err)
	}
	timeoutEventID := domain.AwaitTimeoutEventID(key2)
	h.requireRun(t, "run-race2", domain.DriverRunQueued, timeoutEventID)
	records = h.dispatchEvent(t, "race-evt-2", "race.event", "subject-1", awaitE2EHumanActor, map[string]any{"won": "sweep"})
	if len(records) != 0 {
		t.Fatalf("late event records = %+v, want a structural no-op (nothing pending)", records)
	}
	h.requireRun(t, "run-race2", domain.DriverRunQueued, timeoutEventID)
	if inst, err := h.st.Awaits().GetSatisfiedAwait(h.ctx, h.ws, key2); err != nil ||
		inst.Status != domain.AwaitTimedOut || inst.SatisfiedByEventID != timeoutEventID {
		t.Fatalf("race2 row = %+v, %v; want the timeout resolution standing", inst, err)
	}
}

// --- distorted suspension report guard ---------------------------------------

// A workflow runtime that distorts the suspension sentinel into a failure
// shape (observed with the real Flue runtime: WorkflowSuspended serializes
// into a generic internal error, so the launcher reports failed) must never
// fail the suspended run: the server-side suspension is authoritative and the
// finish that lost ownership is acknowledged (Executor.settleDisownedFinish).
func TestAwaitFlowsDistortedSuspensionReportMemstore(t *testing.T) {
	h := newAwaitFlows(t, memstore.New(), "WS")
	pattern := domain.AwaitEventKey("approval", "distorted#1@sha")
	runner := opRunner{fn: func(req driver.RunRequest) driver.RunResult {
		resp := h.awaitOp(t, req.Run, req.RunToken, pattern, nil, awaitE2ETimeoutMs, 1)
		if resp.Status != driver.AwaitOutcomeSuspended {
			return failedResult("unexpected await status " + resp.Status)
		}
		// The runtime lies: a terminal failure shape despite the suspend.
		return driver.RunResult{Status: domain.DriverRunFailed,
			Summary: "An internal error occurred.", ErrorClass: "internal_error"}
	}}
	h.createRun(t, "run-distorted")
	res := h.runExecutorOnce(t, "node-1", "run-distorted", runner)
	if res.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("final = %+v, want the suspension acknowledged", res.Final)
	}
	h.requireRun(t, "run-distorted", domain.DriverRunSuspendedAwaitingEvent, "")
	// The run is not terminal: no run.finished may have been published.
	events, err := h.st.TriggerEvents().List(h.ctx, h.ws, store.TriggerEventFilter{})
	if err != nil || len(events) != 0 {
		t.Fatalf("journal = %d events (%v), want none after the acknowledged suspension", len(events), err)
	}
	// The suspended run resumes normally afterwards.
	records := h.dispatchEvent(t, "evt-distorted", "approval", "distorted#1@sha", awaitE2EHumanActor, map[string]any{"decision": "approved"})
	if len(records) != 1 || records[0].Outcome != trigger.AwaitMatchResolved {
		t.Fatalf("dispatch records = %+v, want one resolved", records)
	}
	h.requireRun(t, "run-distorted", domain.DriverRunQueued, "evt-distorted")
}
