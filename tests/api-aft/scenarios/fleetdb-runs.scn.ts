// Scenario: the fleet-db execution spine -- node, driver, driver version, driver run,
// driver steps, task runs.
//
// Why this family matters. Everything loom does at runtime lands here: `loom epic run`
// registers a node, admits a driver run, opens a step per unit of work, and a task run
// per agent invocation. It is also the only family in either spec whose path parameters
// ({driver_id}, {version_id}, {run_id}, {step_id}, {task_run_id}, {node_id}) cannot be
// bound by any issue-shaped scenario, so ~20 safe GETs behind them were unreachable to
// the read sweep and scored as an honest gap. This pack creates that state for real and
// binds it.
//
// Concurrency note: loom serve runs background task-worker loops that poll
// POST /task-runs/claim against this same workspace. A pack that left a queued task run
// claimable by anyone would be flaky by construction. Isolation here is not a sleep or a
// retry -- it is a matching predicate. storage/task_run_claim.go:131 rejects a run whose
// provider_profile is absent from the claimer's supported_providers, and
// BindTaskRunClaimToNode (:84) restricts a claimer to the providers ITS node advertises.
// So a task run stamped provider_profile:"aft-sandbox", claimable only from a node whose
// capabilities advertise "aft-sandbox", is invisible to every other worker in the stack.
//
// No assertions (scripts/check-no-asserts.mjs). State moves; the invariant registry
// checks. Bodies below come from the Go request structs, not the spec: e.g. a driver
// version's required fields are source_digest + bundle_digest + a positive version
// (internal/models/platform.go:154), which the spec does not mark required.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";

export const name = "fleet-db execution spine: node -> driver -> driver run -> steps -> task runs";

function str(body: unknown, key: string): string {
  const o = body as Record<string, unknown> | null;
  const v = o && typeof o === "object" ? o[key] : undefined;
  return typeof v === "string" ? v : "";
}

function int(body: unknown, key: string): number {
  const o = body as Record<string, unknown> | null;
  const v = o && typeof o === "object" ? o[key] : undefined;
  return typeof v === "number" ? v : 0;
}

// Stable ids: the stack's config dir (and therefore fleet-db's snapshot) is wiped on
// every start (src/stack.ts), so deterministic ids cannot collide across runs and they
// keep transcript diffs readable in a way that random suffixes would not.
const DRIVER = "aft-runs-driver";
const VERSION = "aft-runs-driver-v1";
const NODE = "aft-runs-node";
const RUN = "aft-runs-drun-1";
const STEP_TASK = "aft-runs-dstep-task";
const STEP_PROBE = "aft-runs-dstep-probe";
const TRUN_A = "aft-runs-trun-a";
const TRUN_B = "aft-runs-trun-b";
const RUN_LEASE = "aft-runs-lease-drun";
// The provider profile is the isolation key described in the header comment.
const PROVIDER = "aft-sandbox";

export async function run(w: World): Promise<void> {
  const ws = w.workspace;

  // The pack must stand alone: `api-aft run fleetdb-runs` loads only this file, so the
  // workspace another pack would have created may not exist yet.
  await w.loom("POST", "/api/workspaces", { name: ws, type: "empty" });

  // A task run's task_id is a real issue id, not a synthetic string. TaskRun.Validate
  // (internal/models/platform.go:1173) only requires it to be non-empty, so a made-up
  // value would be accepted -- and would then quietly test nothing, since the whole
  // point of task_id is the join back to the work item an operator can see.
  const issue = await w.loom("POST", `/api/workspaces/${ws}/issues`, {
    title: "api-aft: fleet-db execution spine",
    issue_type: "task",
    priority: 2,
    description: "Task backing the driver run and task runs this pack creates.",
  });
  const taskId = str(unwrap(issue.body, "issue"), "id");
  if (taskId) w.track(taskId);

  await w.checkpoint("runs-workspace-ready");

  // --- node ------------------------------------------------------------------
  // capabilities carries PROVIDER because nodeAdvertisedProviders() (task_run_claim.go:153)
  // is runtime_provider plus capabilities, and a claim may only ask for providers its own
  // node advertises. runtime_provider stays "local" -- it is a closed enum
  // (models/control_plane.go:39) and inventing a value there would 400.
  const node = await w.fleet("POST", `/api/v1/${ws}/nodes`, {
    node_id: NODE,
    owner_actor: "api-aft",
    runtime_provider: "local",
    labels: ["api-aft"],
    capabilities: [PROVIDER],
    tool_inventory: ["git", "node"],
    version: "api-aft-1",
    capacity: 4,
    drain_state: "active",
    ttl_seconds: 900,
  });
  if (str(node.body, "node_id")) w.bind("nodes.node_id", str(node.body, "node_id"));

  await w.fleet("GET", `/api/v1/${ws}/nodes`);
  await w.fleet("GET", `/api/v1/${ws}/nodes/${NODE}`);
  // ttl is a query parameter, not a body field (internal/api/control_plane.go:1256).
  await w.fleet("POST", `/api/v1/${ws}/nodes/${NODE}/heartbeat?ttl_seconds=900`, {});
  // NodeUpdate is all-pointer (internal/storage/storage.go:314): an omitted field is
  // "leave alone", so this patch must not blank labels or capabilities.
  await w.fleet("PATCH", `/api/v1/${ws}/nodes/${NODE}`, { capacity: 8, version: "api-aft-2" });

  await w.checkpoint("runs-node-registered");

  // --- driver + version ------------------------------------------------------
  const driver = await w.fleet("POST", `/api/v1/${ws}/drivers`, {
    driver_id: DRIVER,
    name: "api-aft runs pack driver",
    owner_type: "system",
    owner_ref: "api-aft",
    description: "Driver registered by the api-aft fleetdb-runs pack.",
    status: "active",
    // Empty trust_level is stamped untrusted by Validate (models/platform.go:105).
    // Sending it explicitly is what the registering control plane does.
    trust_level: "trusted",
    metadata: { source: "api-aft" },
  });
  if (str(driver.body, "driver_id")) w.bind("drivers.driver_id", str(driver.body, "driver_id"));

  await w.fleet("GET", `/api/v1/${ws}/drivers`);
  await w.fleet("GET", `/api/v1/${ws}/drivers/${DRIVER}`);
  await w.fleet("PATCH", `/api/v1/${ws}/drivers/${DRIVER}`, {
    description: "Patched by the api-aft fleetdb-runs pack.",
    metadata: { source: "api-aft", phase: "patched" },
  });

  const version = await w.fleet("POST", `/api/v1/${ws}/drivers/${DRIVER}/versions`, {
    version_id: VERSION,
    version: 1,
    source_ref: "git:api-aft",
    source_digest: "sha256:api-aft-source",
    bundle_ref: "file:///dev/null",
    bundle_digest: "sha256:api-aft-bundle",
    runtime: "node",
    manifest: { entrypoint: "main" },
    validation_status: "passed",
    created_by: "api-aft",
  });
  if (str(version.body, "version_id")) w.bind("driver-versions.version_id", str(version.body, "version_id"));

  await w.fleet("GET", `/api/v1/${ws}/drivers/${DRIVER}/versions`);
  await w.fleet("GET", `/api/v1/${ws}/driver-versions`);
  await w.fleet("GET", `/api/v1/${ws}/driver-versions/${VERSION}`);

  // A driver whose active version points at a version that now exists: the create call
  // above could not set it, because CreateDriverVersion requires the driver first
  // (internal/storage/platform.go:95).
  await w.fleet("PATCH", `/api/v1/${ws}/drivers/${DRIVER}`, { active_version_id: VERSION });

  await w.checkpoint("runs-driver-registered");

  // --- driver run ------------------------------------------------------------
  // CreateDriverRun refuses any status but queued (storage/platform.go:975) and resolves
  // driver_version_id, cross-checking that the version belongs to driver_id -- a mismatch
  // is a 404, not a 400.
  const drun = await w.fleet("POST", `/api/v1/${ws}/driver-runs`, {
    run_id: RUN,
    driver_id: DRIVER,
    driver_version_id: VERSION,
    entrypoint: "main",
    source_kind: "api-aft",
    source_ref: "scenarios/fleetdb-runs.scn.ts",
    idempotency_key: "aft-runs-idem-1",
    payload: { reason: "api-aft execution spine" },
  });
  if (str(drun.body, "run_id")) w.bind("driver-runs.run_id", str(drun.body, "run_id"));

  await w.fleet("GET", `/api/v1/${ws}/driver-runs`);
  await w.fleet("GET", `/api/v1/${ws}/driver-runs/${RUN}`);

  // Replaying the same idempotency_key is the admission path a retrying dispatcher takes:
  // the Lua script returns EXISTING:<run_id> and the handler answers with the original
  // run rather than a conflict (storage/platform.go:1019).
  await w.fleet("POST", `/api/v1/${ws}/driver-runs`, {
    run_id: "aft-runs-drun-1-retry",
    driver_id: DRIVER,
    driver_version_id: VERSION,
    entrypoint: "main",
    idempotency_key: "aft-runs-idem-1",
  });

  const claimed = await w.fleet("POST", `/api/v1/${ws}/driver-runs/${RUN}/claim`, {
    node_id: NODE,
    lease_id: RUN_LEASE,
  });
  // The fencing token is minted by the claim; every later mutation on this run must carry
  // the exact triple (node, lease, token) or ValidateDriverRunOwnerForMutation
  // (storage/platform_types.go:412) rejects it as not-owner.
  const fence = int(claimed.body, "fencing_token");
  const owner = { node_id: NODE, lease_id: RUN_LEASE, fencing_token: fence };

  await w.fleet("POST", `/api/v1/${ws}/driver-runs/${RUN}/heartbeat`, owner);
  await w.fleet("GET", `/api/v1/${ws}/driver-runs/${RUN}/events`);

  await w.checkpoint("runs-driver-run-claimed");

  // --- driver steps ----------------------------------------------------------
  // Two creation surfaces exist for the same entity: nested under the run (run_id from the
  // path) and flat with driver_run_id in the body. Both funnel through
  // createDriverStepWithRun (internal/api/platform.go:2196), so driving both is how a
  // divergence between them would surface.
  const stepTask = await w.fleet("POST", `/api/v1/${ws}/driver-runs/${RUN}/steps`, {
    step_id: STEP_TASK,
    step_kind: "task",
    status: "running",
    input_ref: "issue:" + taskId,
    ...owner,
  });
  if (str(stepTask.body, "step_id")) w.bind("driver-steps.step_id", str(stepTask.body, "step_id"));

  await w.fleet("POST", `/api/v1/${ws}/driver-steps`, {
    step_id: STEP_PROBE,
    driver_run_id: RUN,
    step_kind: "probe",
    status: "queued",
    external_ref: "api-aft",
    ...owner,
  });

  await w.fleet("GET", `/api/v1/${ws}/driver-runs/${RUN}/steps`);
  await w.fleet("GET", `/api/v1/${ws}/driver-steps?driver_run_id=${RUN}`);
  await w.fleet("GET", `/api/v1/${ws}/driver-steps/${STEP_TASK}`);

  await w.checkpoint("runs-driver-steps-created");

  // --- task run A: claim -> heartbeat -> log -> requeue -> reclaim -> finish ---
  const trunA = await w.fleet("POST", `/api/v1/${ws}/task-runs`, {
    task_run_id: TRUN_A,
    driver_run_id: RUN,
    driver_step_id: STEP_TASK,
    task_id: taskId,
    runner: "claude",
    runner_kind: "process",
    runner_entrypoint: "loom-runner",
    provider_profile: PROVIDER,
    status: "queued",
    runtime_metadata: { pack: "fleetdb-runs" },
    input: { prompt: "api-aft" },
  });
  if (str(trunA.body, "task_run_id")) w.bind("task-runs.task_run_id", str(trunA.body, "task_run_id"));

  // The back-link can only be written once the task run exists: validateDriverStepReferences
  // (storage/platform.go:2073) requires the referenced task run to point at BOTH this step
  // and this run, so step -> task_run_id is unwritable before the task run is created.
  await w.fleet("PATCH", `/api/v1/${ws}/driver-steps/${STEP_TASK}`, {
    task_run_id: TRUN_A,
    output_ref: "logs://aft-runs-trun-a",
    ...owner,
  });

  const claimA = await w.fleet("POST", `/api/v1/${ws}/task-runs/claim`, {
    task_run_id: TRUN_A,
    node_id: NODE,
    runner_id: "aft-runner-1",
    lease_id: "aft-runs-lease-trun-a",
    supported_providers: [PROVIDER],
  });
  const fenceA = int(claimA.body, "fencing_token");
  const ownerA = { node_id: NODE, lease_id: "aft-runs-lease-trun-a", fencing_token: fenceA };

  await w.fleet("GET", `/api/v1/${ws}/task-runs`);
  await w.fleet("GET", `/api/v1/${ws}/task-runs/${TRUN_A}`);
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_A}/heartbeat`, {
    ...ownerA,
    runtime_metadata: { phase: "running" },
    logs_ref: "logs://aft-runs-trun-a",
  });
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_A}/logs`, {
    ...ownerA,
    stream: "stdout",
    text: "api-aft: task run A is alive",
  });
  await w.fleet("GET", `/api/v1/${ws}/task-runs/${TRUN_A}/logs`);

  await w.checkpoint("runs-task-run-claimed");

  // Requeue returns the run to queued and drops the lease; a retrying runner then reclaims
  // with a NEW lease id, and the fencing token it gets back must differ from fenceA or the
  // fence is not fencing anything.
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_A}/requeue`, {
    ...ownerA,
    error_class: "transient",
    error_message: "api-aft synthetic requeue",
  });
  const reclaimA = await w.fleet("POST", `/api/v1/${ws}/task-runs/claim`, {
    task_run_id: TRUN_A,
    node_id: NODE,
    runner_id: "aft-runner-1",
    lease_id: "aft-runs-lease-trun-a2",
    supported_providers: [PROVIDER],
  });
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_A}/finish`, {
    node_id: NODE,
    lease_id: "aft-runs-lease-trun-a2",
    fencing_token: int(reclaimA.body, "fencing_token"),
    status: "completed",
    exit_code: 0,
    logs_ref: "logs://aft-runs-trun-a",
    input_tokens: 120,
    output_tokens: 45,
    estimated_cost_usd: 0.0012,
  });

  await w.checkpoint("runs-task-run-finished");

  // --- task run B: the idempotent completion path ----------------------------
  // finish and complete are NOT the same endpoint. complete additionally writes a
  // completion record keyed by completion_id and is replay-safe: a second call with the
  // same id returns the first result instead of a conflict (storage/platform.go:2396).
  await w.fleet("POST", `/api/v1/${ws}/task-runs`, {
    task_run_id: TRUN_B,
    driver_run_id: RUN,
    driver_step_id: STEP_PROBE,
    task_id: taskId,
    runner: "claude",
    provider_profile: PROVIDER,
    status: "queued",
  });
  const claimB = await w.fleet("POST", `/api/v1/${ws}/task-runs/claim`, {
    task_run_id: TRUN_B,
    node_id: NODE,
    runner_id: "aft-runner-2",
    lease_id: "aft-runs-lease-trun-b",
    supported_providers: [PROVIDER],
  });
  const completeBody = {
    completion_id: "aft-runs-completion-1",
    node_id: NODE,
    lease_id: "aft-runs-lease-trun-b",
    fencing_token: int(claimB.body, "fencing_token"),
    status: "completed",
    exit_code: 0,
    // close_task stays false: closing the backing issue belongs to the issue lifecycle
    // pack, and doing it here would move state another pack's invariants read.
    close_task: false,
    input_tokens: 10,
    output_tokens: 5,
  };
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_B}/complete`, completeBody);
  // Replay: same completion_id, same run. The idempotent branch must not re-terminate.
  await w.fleet("POST", `/api/v1/${ws}/task-runs/${TRUN_B}/complete`, completeBody);

  await w.checkpoint("runs-task-run-completed");

  // --- recovery sweeps + run teardown ----------------------------------------
  // Both sweepers default to a 5-minute staleness window (internal/api/platform.go:31),
  // so with no max_age_seconds they are genuine no-ops against state this young. That is
  // deliberate: driving them with a short window would reap the live task runs of any
  // pack sharing this workspace.
  await w.fleet("POST", `/api/v1/${ws}/driver-runs/${RUN}/recover-stale-tasks`, {
    error_class: "api-aft-sweep",
  });
  await w.fleet("POST", `/api/v1/${ws}/driver-runs/recover-stale`, { limit: 5, summary: "api-aft sweep" });

  await w.fleet("PATCH", `/api/v1/${ws}/driver-steps/${STEP_TASK}`, { status: "completed", ...owner });
  await w.fleet("PATCH", `/api/v1/${ws}/driver-steps/${STEP_PROBE}`, { status: "skipped", ...owner });

  await w.fleet("POST", `/api/v1/${ws}/driver-runs/${RUN}/finish`, {
    ...owner,
    status: "completed",
    summary: "api-aft execution spine completed",
    output: { task_runs: "2" },
  });

  await w.checkpoint("runs-driver-run-finished");
}
