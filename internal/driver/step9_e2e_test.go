// Package driver_test holds the §7 step-9 acceptance gate (SP3): the
// no-container integration layer proving the sandboxed-runtime + run-scoped
// token invariants hold as ONE system, with every forbidden path observed as
// a denial, not assumed:
//
//   - an untrusted driver never launches outside an isolating sandbox
//     (sandbox_required, no process spawned);
//   - the workflow runtime env assembled by the real executor claim path is
//     EXACTLY the §9.5 locked-down allowlist: LOOM_RUN_TOKEN is the only
//     credential — no static bearer, no lease/fencing identity, no fleet-db
//     coordinates, no inherited secrets;
//   - against the real driver-op HTTP module, a run's token works for its own
//     run only: foreign resources read as 404, impersonation headers are 401,
//     cross-run lease binding is 403, expiry is 401 token_expired, and a
//     terminal run revokes the token regardless of expiry.
//
// The container half of the gate (real podman, serve-only egress, direct
// fleet-db denial) is scripts/test-step9-sandbox.sh.
//
// This file is an external test package: it drives driver + driverapi
// together, which the internal driver package cannot import (cycle).
package driver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/driverapi"
)

var step9TokenKey = bytes.Repeat([]byte{0x42}, 32)

// step9Fixture is one registered driver + memstore workspace, ready to queue
// runs through the real executor claim path.
type step9Fixture struct {
	ctx  context.Context
	st   store.Store
	root string
	reg  *driver.RegisterFlueResult
	exec *appserve.ExecutionCapability
}

type step9TaskRunClaimPort struct{}

func (step9TaskRunClaimPort) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (step9TaskRunClaimPort) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (step9TaskRunClaimPort) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (step9TaskRunClaimPort) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (step9TaskRunClaimPort) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (step9TaskRunClaimPort) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

type step9DriverRunAPI struct {
	execution.DriverRunAPI
}

func (step9DriverRunAPI) CascadeChildDriverRuns(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.CascadeChildDriverRunsCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{ActionID: command.RequestID}, nil
}

func (step9DriverRunAPI) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{ActionID: command.RequestID}, nil
}

func newStep9Fixture(t *testing.T, trust domain.DriverTrustLevel) *step9Fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	step9WriteFlueDist(t, root)
	// Trust is stamped here exactly like the external submission path
	// (workflows.BuildAndRegister) stamps it: server-side, never client input.
	reg, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: "TEST",
		WorkDir:      root,
		DistPath:     "dist",
		DriverName:   "step9-workflow",
		WorkflowName: "step9-workflow",
		CreatedBy:    "step9-gate",
		Activate:     true,
		Trust:        trust,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	repairs, ok := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("step9 memstore lacks terminal DriverStep repair support")
	}
	executionCapability, err := appserve.NewExecutionCapability(appserve.ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), Agents: st.Agents(), Outbox: st.Outbox(), Awaits: st.Awaits(), TriggerEvents: st.TriggerEvents(),
		Workspaces: st.Workspaces(), AtomicTaskRunRequests: step9TaskRunClaimPort{}, AtomicTaskRunClaims: step9TaskRunClaimPort{},
		AtomicTaskRunWorkItemDesign: step9TaskRunClaimPort{},
		AtomicTaskRunRequeues:       step9TaskRunClaimPort{}, AtomicTaskRunRetryExhaustion: step9TaskRunClaimPort{},
		AllowLegacyStoreAdapters: true,
	})
	if err != nil {
		t.Fatalf("compose step9 Execution: %v", err)
	}
	return &step9Fixture{ctx: ctx, st: st, root: root, reg: reg, exec: executionCapability}
}

// step9WriteFlueDist writes the minimal registrable built-Flue dist. The
// server is never forked by these tests (the refusal leg stops pre-launch and
// the env leg runs under a capture launcher); registration only digests it.
func step9WriteFlueDist(t *testing.T, root string) {
	t.Helper()
	dist := filepath.Join(root, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	server := "if (process.send) { process.send({ version: 1, type: 'ready', target: 'workflow', name: 'step9-workflow' }); }\n"
	if err := os.WriteFile(filepath.Join(dist, "server.mjs"), []byte(server), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
}

func (f *step9Fixture) queueRun(t *testing.T, runID, epicID string) {
	t.Helper()
	if _, err := driver.CreateDriverRun(f.ctx, f.st, driver.RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     f.reg.Driver.DriverID,
		EpicID:       epicID,
		RunID:        runID,
	}); err != nil {
		t.Fatalf("CreateDriverRun %s: %v", runID, err)
	}
}

func (f *step9Fixture) executor(runID string, launcher driver.SandboxLauncher) *driver.Executor {
	return &driver.Executor{
		Store:                f.st,
		WorkspaceKey:         "TEST",
		RunID:                runID,
		WorkDir:              f.root,
		NodeID:               "step9-node",
		LeaseID:              "step9-lease-" + runID,
		HeartbeatInterval:    -1,
		APIBaseURL:           "http://127.0.0.1:7777",
		APIToken:             "node-shared-static-bearer",
		RunTokenKey:          step9TokenKey,
		SandboxLauncher:      launcher,
		Execution:            step9DriverRunAPI{DriverRunAPI: f.exec.DriverRunAPI()},
		RunOutcomeQueue:      f.exec.DriverRunOutcomeAPI(),
		ExecutionWorkers:     f.exec.TaskRunWorkerAPI(),
		ExecutionAuthorities: f.exec.DriverRunAuthorityResolver(),
		SystemAuthorities:    f.exec.SystemAuthorityResolver(),
	}
}

// step9CaptureLauncher is an isolating SandboxLauncher that never spawns a
// process: it captures the exact LaunchSpec the runner assembled and reports
// a completed terminal frame, so the test can audit the runtime env without a
// node dependency.
type step9CaptureLauncher struct {
	spec driver.LaunchSpec
}

func (l *step9CaptureLauncher) Isolates() bool { return true }

func (l *step9CaptureLauncher) Launch(_ context.Context, spec driver.LaunchSpec) (driver.SandboxProcess, error) {
	l.spec = spec
	return step9CaptureProcess{}, nil
}

type step9CaptureProcess struct{}

func (step9CaptureProcess) Wait() (driver.SandboxExit, error) {
	return driver.SandboxExit{Stdout: `{"status":"completed","summary":"step9 capture ok"}`}, nil
}

func (step9CaptureProcess) Kill() error { return nil }

func (step9CaptureProcess) Placement() domain.TaskRunPlacement {
	return domain.TaskRunPlacement{Provider: "step9-capture", StartedAt: time.Now().UTC()}
}

// TestStep9UntrustedDriverRefusedOutsideSandbox: the trust placement policy is
// a pre-launch refusal, end to end through the real claim path — an untrusted
// driver resolved onto the default process launcher terminalizes failed with
// the structured sandbox_required result, persisted on the run row.
func TestStep9UntrustedDriverRefusedOutsideSandbox(t *testing.T) {
	f := newStep9Fixture(t, domain.DriverTrustUntrusted)
	f.queueRun(t, "run-refused", "TEST-1")

	result, err := f.executor("run-refused", nil).RunOnce(f.ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunFailed {
		t.Fatalf("final = %+v, want failed", result.Final)
	}
	if result.Final.ErrorClass != driver.ErrorClassSandboxRequired {
		t.Fatalf("error class = %q, want %q", result.Final.ErrorClass, driver.ErrorClassSandboxRequired)
	}
	persisted, err := f.st.DriverRuns().Get(f.ctx, "TEST", "run-refused")
	if err != nil {
		t.Fatalf("Get refused run: %v", err)
	}
	wantOutput := map[string]string{
		driver.ErrorCodeOutputKey:       driver.ErrorClassSandboxRequired,
		driver.RetryableOutputKey:       "false",
		driver.TrustLevelOutputKey:      string(domain.DriverTrustUntrusted),
		driver.SandboxLauncherOutputKey: driver.SandboxProviderProcess,
	}
	for key, want := range wantOutput {
		if got := persisted.Output[key]; got != want {
			t.Fatalf("persisted output[%s] = %q, want %q (output: %v)", key, got, want, persisted.Output)
		}
	}
}

// step9EnvAllowedExact is the complete set of exact env names permitted in a
// locked-down workflow runtime env: the TK5 parent-env allowlist that
// survives scoping plus every key the runner itself injects. Anything else —
// in particular any credential other than LOOM_RUN_TOKEN — fails the gate.
var step9EnvAllowedExact = map[string]bool{
	// Parent allowlist (env.go) — harmless interpreter/shell basics.
	"PATH": true, "HOME": true, "PWD": true, "OLDPWD": true,
	"TMPDIR": true, "TMP": true, "TEMP": true, "TERM": true,
	"USER": true, "LOGNAME": true, "SHELL": true, "TZ": true, "LANG": true,
	"LOOM_CONFIG_DIR": true, "LOOM_FLUE_AGENT_MODEL": true,
	"LOOM_HOST_BRIDGE_HELPER":          true,
	"LOOM_DRIVER_TASK_RUNNER_CMD_JSON": true,
	"LOOM_DRIVER_TASK_RUNNER_CMD":      true,
	// Runner-injected identity + invoke surface.
	"LOOM_DRIVER_WORKSPACE": true, "LOOM_DRIVER_RUN_ID": true, "LOOM_DRIVER_NODE_ID": true,
	"LOOM_FLUE_SERVER_PATH": true, "LOOM_FLUE_BUNDLE_ROOT": true,
	"LOOM_FLUE_WORKFLOW_NAME": true, "LOOM_FLUE_INVOKE_PAYLOAD": true,
	"LOOM_DRIVER_EXEC_TASK_CMD_JSON": true,
	// The driver-op API endpoint and the ONLY credential.
	"LOOM_DRIVER_API_URL": true,
	"LOOM_RUN_TOKEN":      true,
}

// step9EnvForbidden names the credentials the locked-down env must NOT carry:
// the static bearer (cross-run authority), the lease identity pair (auth
// material under header-quad), fleet-db coordinates, the signing key, and the
// hostile inherited secrets the test plants in the parent env.
var step9EnvForbidden = []string{
	"LOOM_DRIVER_API_TOKEN",
	"LOOM_DRIVER_LEASE_ID",
	"LOOM_DRIVER_FENCING_TOKEN",
	"LOOM_FLEET_DB_URL",
	"LOOM_FLEET_DB_API_KEY",
	"LOOM_FLEET_DB_ACTOR",
	"LOOM_FLEETDB_REDIS_URL",
	"LOOM_TASK_RUN_LEASE_TOKEN",
	driver.RunTokenSigningKeyEnv,
	"GITHUB_TOKEN",
	"AWS_SECRET_ACCESS_KEY",
}

// TestStep9WorkflowEnvHoldsOnlyRunToken drives the full executor claim path
// (claim → mint → load → launch) under the §9.5 lockdown with a hostile
// parent environment, and audits the exact env handed across the sandbox
// seam: every key is allowlisted, the only credential is the run token minted
// for THIS claim, and the static bearer + identity pair never appear.
func TestStep9WorkflowEnvHoldsOnlyRunToken(t *testing.T) {
	t.Setenv(driver.LegacyDriverAuthEnvVar, "0")
	for _, hostile := range step9EnvForbidden {
		t.Setenv(hostile, "hostile-parent-value")
	}
	t.Setenv("LOOM_RUN_TOKEN", "stale-parent-token")

	f := newStep9Fixture(t, domain.DriverTrustUntrusted)
	f.queueRun(t, "run-env-audit", "TEST-2")
	capture := &step9CaptureLauncher{}
	result, err := f.executor("run-env-audit", capture).RunOnce(f.ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunCompleted {
		t.Fatalf("final = %+v, want completed through the isolating capture launcher", result.Final)
	}
	env := envPairs(t, capture.spec.Env)
	assertStep9EnvAllowlisted(t, env)
	assertStep9RunTokenBound(t, env, result.Claimed)
	if got := env["LOOM_DRIVER_API_URL"]; got != "http://127.0.0.1:7777" {
		t.Fatalf("LOOM_DRIVER_API_URL = %q, want the serve driver-op endpoint", got)
	}
	assertStep9PlacementAudit(t, result.Final.Output)
}

func envPairs(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("env entry %q is not KEY=VALUE", entry)
		}
		out[key] = value
	}
	return out
}

func assertStep9EnvAllowlisted(t *testing.T, env map[string]string) {
	t.Helper()
	for key := range env {
		if step9EnvAllowedExact[key] || strings.HasPrefix(key, "LC_") {
			continue
		}
		t.Errorf("workflow env carries non-allowlisted key %s", key)
	}
	for _, forbidden := range step9EnvForbidden {
		if value, present := env[forbidden]; present {
			t.Errorf("workflow env carries forbidden credential %s=%q", forbidden, value)
		}
	}
}

// assertStep9RunTokenBound proves the exported LOOM_RUN_TOKEN is the token
// minted at THIS claim — not the stale parent value — and that its claims
// bind the claimed lease tuple with caps reserved-but-empty.
func assertStep9RunTokenBound(t *testing.T, env map[string]string, claimed *domain.DriverRun) {
	t.Helper()
	token := env["LOOM_RUN_TOKEN"]
	if token == "" || token == "stale-parent-token" {
		t.Fatalf("LOOM_RUN_TOKEN = %q, want freshly minted token", token)
	}
	claims, err := driver.ParseRunToken(token, step9TokenKey)
	if err != nil {
		t.Fatalf("ParseRunToken: %v", err)
	}
	if claims.WorkspaceKey != claimed.WorkspaceKey || claims.RunID != claimed.RunID {
		t.Fatalf("token identity = %s/%s, want %s/%s", claims.WorkspaceKey, claims.RunID, claimed.WorkspaceKey, claimed.RunID)
	}
	if claims.NodeID != claimed.NodeID || claims.LeaseID != claimed.LeaseID || claims.FencingToken != claimed.FencingToken {
		t.Fatalf("token lease binding = %s/%s/%d, want %s/%s/%d",
			claims.NodeID, claims.LeaseID, claims.FencingToken, claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
	}
	if len(claims.Caps) != 0 {
		t.Fatalf("token caps = %v, want reserved-but-empty", claims.Caps)
	}
}

func assertStep9PlacementAudit(t *testing.T, output map[string]string) {
	t.Helper()
	if got := output[driver.TrustLevelOutputKey]; got != string(domain.DriverTrustUntrusted) {
		t.Fatalf("output[%s] = %q, want untrusted", driver.TrustLevelOutputKey, got)
	}
	if got := output[driver.SandboxLauncherOutputKey]; got != "custom-isolating" {
		t.Fatalf("output[%s] = %q, want custom-isolating", driver.SandboxLauncherOutputKey, got)
	}
	var placement domain.TaskRunPlacement
	if err := json.Unmarshal([]byte(output[driver.SandboxPlacementOutputKey]), &placement); err != nil {
		t.Fatalf("decode output[%s] %q: %v", driver.SandboxPlacementOutputKey, output[driver.SandboxPlacementOutputKey], err)
	}
	if placement.Provider != "step9-capture" {
		t.Fatalf("placement provider = %q, want step9-capture", placement.Provider)
	}
}

// step9TwoRuns is the cross-run scope rig: two claimed driver runs, each
// owning one task run, fronted by the REAL driver-op HTTP module with a
// static ops bearer configured (which workflows never hold).
type step9TwoRuns struct {
	f      *step9Fixture
	server *httptest.Server
	runA   *domain.DriverRun
	runB   *domain.DriverRun
}

func newStep9TwoRuns(t *testing.T) *step9TwoRuns {
	t.Helper()
	f := newStep9Fixture(t, domain.DriverTrustUntrusted)
	rig := &step9TwoRuns{f: f}
	rig.runA = rig.claimRunWithTaskRun(t, "run-a", "TEST-A", "node-a", "lease-a")
	rig.runB = rig.claimRunWithTaskRun(t, "run-b", "TEST-B", "node-b", "lease-b")
	module := driverapi.NewModule(driverapi.Config{
		Store: f.st, APIToken: "ops-static-token", RunTokenKey: step9TokenKey,
		Execution: f.exec.DriverRunAPI(), ExecutionAuthorities: f.exec.DriverRunAuthorityResolver(),
		TaskRunRequests: f.exec.TaskRunRequestAPI(), TaskRunRecovery: f.exec.TaskRunRecoveryAPI(),
		TaskRuns: f.exec.TaskRunAPI(), TaskRunAuthorities: f.exec.TaskRunAuthorityResolver(),
	})
	mux := http.NewServeMux()
	module.Register(mux)
	rig.server = httptest.NewServer(mux)
	t.Cleanup(rig.server.Close)
	return rig
}

func (r *step9TwoRuns) claimRunWithTaskRun(t *testing.T, runID, epicID, nodeID, leaseID string) *domain.DriverRun {
	t.Helper()
	r.f.queueRun(t, runID, epicID)
	claimed, err := r.f.st.DriverRuns().Claim(r.f.ctx, "TEST", runID, nodeID, leaseID)
	if err != nil {
		t.Fatalf("Claim %s: %v", runID, err)
	}
	if _, err := r.f.st.DriverSteps().Create(r.f.ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST",
		StepID:       "step-" + runID,
		DriverRunID:  runID,
		StepKind:     "task_run",
		Status:       domain.DriverStepQueued,
		NodeID:       claimed.NodeID,
		LeaseID:      claimed.LeaseID,
		FencingToken: claimed.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step for %s: %v", runID, err)
	}
	if _, err := r.f.st.TaskRuns().Create(r.f.ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST",
		TaskRunID:    "task-run-" + runID,
		DriverRunID:  runID,
		DriverStepID: "step-" + runID,
		TaskID:       epicID + "-1",
		Status:       domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("Create task run for %s: %v", runID, err)
	}
	return claimed
}

func (r *step9TwoRuns) mint(t *testing.T, run *domain.DriverRun, ttl time.Duration, mutate func(*driver.RunTokenClaims)) string {
	t.Helper()
	claims := driver.RunTokenClaims{
		WorkspaceKey: run.WorkspaceKey,
		RunID:        run.RunID,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
	}
	if mutate != nil {
		mutate(&claims)
	}
	token, err := driver.MintRunToken(claims, step9TokenKey, ttl)
	if err != nil {
		t.Fatalf("MintRunToken: %v", err)
	}
	return token
}

// doOp posts one driver op and returns (status, error code) — code is ""
// on 2xx responses.
func (r *step9TwoRuns) doOp(t *testing.T, op, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, r.server.URL+"/api/workspaces/TEST/driver/"+op, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new %s request: %v", op, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s request: %v", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var decoded struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded.Error.Code
}

func step9Bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// TestStep9RunTokenScopedToOneRun is the forbidden-path matrix against the
// real driver-op HTTP module: run A's token authenticates run A's surface and
// NOTHING else. Every denial is observed.
func TestStep9RunTokenScopedToOneRun(t *testing.T) {
	rig := newStep9TwoRuns(t)
	tokenA := rig.mint(t, rig.runA, time.Hour, nil)

	cases := []struct {
		name       string
		op         string
		body       string
		headers    func() map[string]string
		wantStatus int
		wantCode   string
	}{
		{
			name: "own run authenticates token-only", op: "task-run-get",
			body:    `{"taskRunId":"task-run-run-a"}`,
			headers: func() map[string]string { return step9Bearer(tokenA) },
			// 200: the only credential on the wire is the run token.
			wantStatus: http.StatusOK,
		},
		{
			name: "foreign run's task run reads as not found", op: "task-run-get",
			body:       `{"taskRunId":"task-run-run-b"}`,
			headers:    func() map[string]string { return step9Bearer(tokenA) },
			wantStatus: http.StatusNotFound, wantCode: "not_found",
		},
		{
			name: "impersonating another run via identity header", op: "list-agents",
			body: `{}`,
			headers: func() map[string]string {
				headers := step9Bearer(tokenA)
				headers[driverapi.HeaderDriverRunID] = rig.runB.RunID
				return headers
			},
			wantStatus: http.StatusUnauthorized, wantCode: "identity_mismatch",
		},
		{
			name: "token claims cannot graft one run's lease onto another", op: "list-agents",
			body: `{}`,
			headers: func() map[string]string {
				grafted := rig.mint(t, rig.runB, time.Hour, func(c *driver.RunTokenClaims) {
					c.NodeID = rig.runA.NodeID
					c.LeaseID = rig.runA.LeaseID
					c.FencingToken = rig.runA.FencingToken
				})
				return step9Bearer(grafted)
			},
			wantStatus: http.StatusForbidden, wantCode: "not_owner",
		},
		{
			name: "expired token is a hard token_expired", op: "list-agents",
			body: `{}`,
			headers: func() map[string]string {
				expired := rig.mint(t, rig.runA, time.Nanosecond, nil)
				time.Sleep(20 * time.Millisecond)
				return step9Bearer(expired)
			},
			wantStatus: http.StatusUnauthorized, wantCode: "token_expired",
		},
		{
			name: "header quad without any bearer fails the ops gate", op: "list-agents",
			body: `{}`,
			headers: func() map[string]string {
				return map[string]string{
					driverapi.HeaderDriverRunID:        rig.runA.RunID,
					driverapi.HeaderDriverNodeID:       rig.runA.NodeID,
					driverapi.HeaderDriverLeaseID:      rig.runA.LeaseID,
					driverapi.HeaderDriverFencingToken: "1",
				}
			},
			wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, code := rig.doOp(t, tc.op, tc.body, tc.headers())
			if status != tc.wantStatus || code != tc.wantCode {
				t.Fatalf("(%d, %q), want (%d, %q)", status, code, tc.wantStatus, tc.wantCode)
			}
		})
	}
}

// TestStep9RunTokenRevokedWhenRunFinishes: terminalizing the run revokes its
// still-unexpired token through fenced run verification — no denylist.
func TestStep9RunTokenRevokedWhenRunFinishes(t *testing.T) {
	rig := newStep9TwoRuns(t)
	tokenA := rig.mint(t, rig.runA, time.Hour, nil)
	if status, _ := rig.doOp(t, "list-agents", `{}`, step9Bearer(tokenA)); status != http.StatusOK {
		t.Fatalf("pre-finish status = %d, want 200", status)
	}
	if _, err := rig.f.st.DriverRuns().Finish(rig.f.ctx, "TEST", rig.runA.RunID, store.DriverRunFinish{
		NodeID:       rig.runA.NodeID,
		LeaseID:      rig.runA.LeaseID,
		FencingToken: rig.runA.FencingToken,
		Status:       domain.DriverRunCompleted,
		Summary:      "step9 revocation",
	}); err != nil {
		t.Fatalf("Finish run A: %v", err)
	}
	status, code := rig.doOp(t, "list-agents", `{}`, step9Bearer(tokenA))
	if status != http.StatusConflict || code != "invalid_transition" {
		t.Fatalf("post-finish = (%d, %q), want (409, invalid_transition)", status, code)
	}
}
