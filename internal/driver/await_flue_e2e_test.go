// Real-Flue SDK await smoke (chunk AW12): the full workflow-facing loop —
// a workflow built by the actual Flue CLI calling loom.events.await through
// the real driver-op HTTP API, the Node launcher mapping the
// WorkflowSuspended sentinel to the suspended result shape, the
// session-authenticated approval endpoint resuming the run, and a SECOND
// executor pass fast-forwarding the satisfied await inline to completion.
//
// Env-gated like the other real-Flue smokes (LOOM_REAL_FLUE_TEST=1 plus a
// Flue CLI via LOOM_REAL_FLUE_CMD_JSON / LOOM_REAL_FLUE_CMD / PATH and the
// @flue/runtime package via FLUE_REPO or the sibling checkout). It lives in
// the external driver_test package — unlike flue_integration_test.go — so
// it can mount the real driverapi/approvals modules, which import
// internal/driver; the project-scaffold helpers are therefore mirrored here.
package driver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// The two AW11-documented suspension styles, one workflow source each:
//
//   - "return-result": the workflow catches the sentinel and returns
//     err.result (the suspended completion shape) — the launcher recognizes
//     the suspended-status result directly.
//   - "propagate": the workflow lets the sentinel propagate. The Flue
//     runtime serializes unknown errors into a GENERIC internal_error (the
//     "workflow_suspended:" message does NOT survive), so the launcher
//     reports failed while the run is already suspended server-side — the
//     executor's settleDisownedFinish acknowledges the authoritative
//     suspension instead of erroring (the AW12 hardening this smoke pins).
const awaitSmokeReturnResultSource = `import { createLoomDriverClient, isWorkflowSuspended } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: () => run({ payload: JSON.parse(process.env.LOOM_FLUE_INVOKE_PAYLOAD || '{}') }),
});

async function run(ctx) {
  const loom = createLoomDriverClient({ input: ctx.payload || {} });
  let res;
  try {
    res = await loom.events.await({
      pattern: 'approval:acme/widgets#1@shaA',
      actor: ['alice@example.com'],
      timeoutMs: 60000,
    });
  } catch (err) {
    if (isWorkflowSuspended(err)) return err.result;
    throw err;
  }
  const decision = (res.event && res.event.payload && res.event.payload.decision) || 'missing';
  return { status: 'completed', summary: 'decision=' + decision + ' status=' + res.status };
}
`

const awaitSmokePropagateSource = `import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: () => run({ payload: JSON.parse(process.env.LOOM_FLUE_INVOKE_PAYLOAD || '{}') }),
});

async function run(ctx) {
  const loom = createLoomDriverClient({ input: ctx.payload || {} });
  const res = await loom.events.await({
    pattern: 'approval:acme/widgets#1@shaA',
    actor: ['alice@example.com'],
    timeoutMs: 60000,
  });
  const decision = (res.event && res.event.payload && res.event.payload.decision) || 'missing';
  return { status: 'completed', summary: 'decision=' + decision + ' status=' + res.status };
}
`

func TestRealFlueAwaitSuspendResumeSmoke(t *testing.T) {
	if os.Getenv("LOOM_REAL_FLUE_TEST") != "1" {
		t.Skip("set LOOM_REAL_FLUE_TEST=1 to run the real Flue await smoke")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not available: %v", err)
	}
	flueCommand := awaitSmokeFlueCommand(t)
	cases := []struct {
		name     string
		workflow string
		source   string
	}{
		{name: "return suspended result", workflow: "await-return", source: awaitSmokeReturnResultSource},
		{name: "propagate sentinel", workflow: "await-propagate", source: awaitSmokePropagateSource},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runRealFlueAwaitSmoke(t, flueCommand, tc.workflow, tc.source)
		})
	}
}

func runRealFlueAwaitSmoke(t *testing.T, flueCommand []string, workflow, source string) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	const ws = "TEST"
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	root := t.TempDir()
	writeAwaitSmokeProject(t, root, workflow, source)
	buildAwaitSmokeProject(t, root, flueCommand)
	registered, err := driver.RegisterFlueDriver(ctx, st, driver.RegisterFlueOptions{
		WorkspaceKey: ws, WorkDir: root, DistPath: "dist", DriverName: workflow,
		WorkflowName: workflow, SourceRef: "workflows/" + workflow + ".ts",
		CreatedBy: "aw12", Activate: true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver after real Flue build %q: %v", strings.Join(flueCommand, " "), err)
	}
	server := newAwaitFlowsServer(t, st)
	h := &awaitFlows{ctx: ctx, st: st, ws: ws, root: root, api: server, driverID: registered.Driver.DriverID}
	const runID = "run-flue-gate"
	h.createRun(t, runID)

	runExecutor := func(node string) *driver.ExecutionResult {
		result, err := (&driver.Executor{
			Store: st, WorkspaceKey: ws, RunID: runID, WorkDir: root,
			NodeID: node, LeaseID: node + "-lease", APIBaseURL: server.URL,
			HeartbeatInterval: -1,
		}).RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce on %s: %v", node, err)
		}
		return result
	}

	// Pass 1: the real SDK awaits and the server suspends the run; whichever
	// shape the runtime reports, the executor settles on the authoritative
	// server-side suspension.
	res1 := runExecutor("node-flue-1")
	if res1.Final == nil || res1.Final.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("first pass = %+v, want suspended_awaiting_event", res1.Final)
	}

	// The verified approver resumes the run through the approval endpoint.
	status, decoded := h.approveAs(t, "user-alice", awaitE2EApprover,
		map[string]any{"subjectRef": "acme/widgets#1@shaA"})
	if status != http.StatusOK {
		t.Fatalf("approval = %d %v, want 200", status, decoded)
	}
	eventID, _ := decoded["eventId"].(string)
	h.requireRun(t, runID, domain.DriverRunQueued, eventID)

	// Pass 2 on a second executor: the replayed await returns the recorded
	// decision inline and the workflow completes.
	res2 := runExecutor("node-flue-2")
	if res2.Final == nil || res2.Final.Status != domain.DriverRunCompleted ||
		!strings.Contains(res2.Final.Summary, "decision=approved") ||
		!strings.Contains(res2.Final.Summary, "status=satisfied") {
		t.Fatalf("final = %+v, want completed with the replayed approval decision", res2.Final)
	}
	if res2.Claimed.NodeID != "node-flue-2" {
		t.Fatalf("resumed run claimed by %q, want the second executor", res2.Claimed.NodeID)
	}
}

// --- real-Flue project scaffolding (mirrors flue_integration_test.go) ------

func awaitSmokeFlueCommand(t *testing.T) []string {
	t.Helper()
	if encoded := strings.TrimSpace(os.Getenv("LOOM_REAL_FLUE_CMD_JSON")); encoded != "" {
		var parsed []string
		if err := json.Unmarshal([]byte(encoded), &parsed); err != nil || len(parsed) == 0 {
			t.Fatalf("decode LOOM_REAL_FLUE_CMD_JSON: %v (need at least one element)", err)
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

func awaitSmokeFlueRuntimeRoot(t *testing.T) string {
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

func writeAwaitSmokeProject(t *testing.T, root, workflowName, source string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "workflows"), 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	pkg := `{"type":"module","dependencies":{"@loom/sdk":"file:./node_modules/@loom/sdk","@flue/runtime":"file:./node_modules/@flue/runtime"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	sdkRoot, err := filepath.Abs("../../sdk")
	if err != nil {
		t.Fatalf("resolve sdk root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sdkRoot, "package.json")); err != nil {
		t.Fatalf("local @loom/sdk package not found at %s: %v", sdkRoot, err)
	}
	links := map[string]string{
		filepath.Join(root, "node_modules", "@loom", "sdk"):     sdkRoot,
		filepath.Join(root, "node_modules", "@flue", "runtime"): awaitSmokeFlueRuntimeRoot(t),
	}
	for link, target := range links {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(link), err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("link %s: %v", link, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", workflowName+".ts"), []byte(source), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
}

func buildAwaitSmokeProject(t *testing.T, root string, flueCommand []string) {
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
