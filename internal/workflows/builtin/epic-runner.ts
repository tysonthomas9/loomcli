import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD (durable-streams) requires every workflow module to default-export
// a defineWorkflow() definition; a bare `export function run` no longer
// normalizes. These builtin runners are not LLM agents — epic-runner
// orchestrates via the loom driver SDK — so the bound agent is a credential-free
// stub (model: false, no harness usage) and the invocation payload arrives via
// env, not flue's input channel: the driver launcher sets LOOM_FLUE_INVOKE_PAYLOAD
// (sandbox/launcher.go) and the task-runner host-bridge sets
// LOOM_TASK_RUN_REQUEST_JSON (driver/task_bridge.go). The launchers keep sending
// the legacy `payload` IPC field (flue HEAD ignores it and passes input=undefined,
// which validates with no input schema), so the workflow body needs no input
// schema. The one required Go-side change is the launcher fork env: flue HEAD
// gates one-shot IPC mode behind FLUE_INTERNAL_CLI_IPC=1 (else the generated
// entry serves HTTP on :3000 instead of doing the invoke/result handshake).
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}';
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

// Flue HEAD validates the workflow return value with a strict JSON check that
// rejects undefined/function/symbol/bigint (json-snapshot.cloneJsonSerializable);
// the old runtime instead JSON-encoded the result for IPC transport, silently
// dropping undefined. Round-trip through JSON to restore that behavior so optional
// result fields left undefined (e.g. needsReview's taskRunId/logsRef) never throw.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// epic-runner: watch-driven epic drain loop.
//
// The workflow is edge-triggered: it claims ready tasks up to maxConcurrency,
// enqueues each as a TaskRun (the serve task workers execute and close them),
// and consumes the epic watch stream for terminal TaskRun events to top the
// pipeline back up. There is no polling cadence, no per-batch barrier, and
// no workflow-side notification machinery: lead assignment + task-completion
// messages are delivered by the server-side outbox dispatcher, and stale-run
// recovery is owned by the server-side sweeper.
//
// The loop is naive + re-entrant: deterministic task run ids and the watch
// handshake snapshot re-derive in-flight state after a restart, and
// completionId keeps re-completion idempotent.
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const epicId = stringValue(input.epicId);
  if (!epicId) {
    return loom.failed({
      summary: "epic-runner requires epicId",
      errorClass: "invalid_epic_runner_input",
    });
  }

  const started = await startEpicRun(loom, input, epicId);
  if (!started.ok) {
    return loom.failed({
      summary: started.summary,
      errorClass: started.errorClass || "epic_start_failed",
    });
  }
  if (booleanValue(input.dryRun)) {
    return loom.completed({
      summary: "Dry-run validated epic " + epicId + (started.leadName ? " for lead " + started.leadName : ""),
    });
  }

  return runEpicWatchLoop(loom, input, epicId, started);
}

async function runEpicWatchLoop(loom, input, epicId, started) {
  const maxConcurrency = Math.max(1, numberValue(input.maxConcurrency, 2));
  const requestDefaults = {
    runner: stringValue(input.runner || "local-task-runner"),
    workerPrefix: stringValue(input.workerPrefix),
    workerProfileId: stringValue(input.workerProfileId),
    targetNodeId: stringValue(input.targetNodeId),
    parentSessionId: started.orchestratorSessionId || stringValue(input.parentSessionId),
    childInput: childTaskInputDefaults(input),
  };
  const inFlight = new Map(); // taskId -> taskRunId
  const completed = [];
  const blockedFailures = [];

  // topUp claims ready tasks and enqueues a TaskRun for each until the
  // in-flight set reaches maxConcurrency or the ready queue is empty.
  async function topUp() {
    while (inFlight.size < maxConcurrency) {
      const task = await loom.tasks.claimReady({ epicId });
      if (!task) {
        return;
      }
      const enqueued = await enqueueChildTask(loom, task, requestDefaults);
      if (enqueued.ok) {
        inFlight.set(task.id, enqueued.taskRunId);
      } else {
        blockedFailures.push(enqueued.blocked);
        return;
      }
    }
  }

  // finishedResult evaluates the end conditions against a fresh snapshot
  // once nothing is in flight locally. Returns null while the epic should
  // keep running.
  async function finishedResult() {
    if (inFlight.size > 0) {
      return null;
    }
    const snapshot = await loom.epics.snapshot({ epicId });
    const active = await loom.taskRuns.active({ epicId });
    return endStateResult(loom, epicId, snapshot, numberValue(active && active.activeCount, 0), completed, blockedFailures);
  }

  // Seed the pipeline before connecting; the handshake snapshot reconciles
  // anything this first pass cannot see (e.g. runs left active by a restart).
  await topUp();

  for await (const event of loom.epics.watch({ epicId })) {
    if (event.type === "closed") {
      return loom.failed({
        summary: "epic watch for " + epicId + " closed: " + (stringValue(event.data && event.data.code) || "unknown"),
        errorClass: "epic_watch_closed",
      });
    }
    if (event.type === "snapshot") {
      const data = event.data || {};
      reconcileInFlight(inFlight, data.active);
      const fromSnapshot = endStateResult(loom, epicId, data.epic, activeCountOf(data.active, inFlight), completed, blockedFailures);
      if (fromSnapshot) {
        return fromSnapshot;
      }
      await topUp();
      const result = await finishedResult();
      if (result) {
        return result;
      }
      continue;
    }
    if (event.type !== "taskRun") {
      continue;
    }
    const data = event.data || {};
    // Journal events are epic-scoped; only this run's children drive the
    // loop (a retry run re-derives prior progress from snapshots instead).
    if (stringValue(data.driverRunID) !== stringValue(loom.driverRunId)) {
      continue;
    }
    const taskId = stringValue(data.taskID);
    switch (stringValue(data.type)) {
      case "taskRunCompleted": {
        const completion = await completeChildTask(loom, data);
        if (completion.ok) {
          completed.push(taskId);
        } else {
          blockedFailures.push(completion.blocked);
        }
        break;
      }
      case "taskRunFailed":
      case "taskRunCancelled":
        // The server scheduler already retried the run and, on retry
        // exhaustion, blocked the underlying issue; record it and keep
        // draining any independent DAG branches.
        blockedFailures.push({
          taskId,
          taskRunId: stringValue(data.taskRunID),
          errorClass: stringValue(data.errorClass) || "child_task_failed",
          summary: "Task failed: " + taskId + (stringValue(data.errorMessage) ? " - " + stringValue(data.errorMessage) : ""),
          logsRef: stringValue(data.logsRef),
          artifactsRef: stringValue(data.artifactsRef),
        });
        break;
      default:
        // queued/claimed/requeued lifecycle events need no action: the run
        // stays in flight until a terminal event or snapshot reconciles it.
        continue;
    }
    inFlight.delete(taskId);
    await topUp();
    const result = await finishedResult();
    if (result) {
      return result;
    }
  }

  // The watch iterator reconnects internally, so reaching stream end without
  // a closed event means the consumer was aborted out from under us.
  return loom.failed({
    summary: "epic watch stream for " + epicId + " ended unexpectedly",
    errorClass: "epic_watch_ended",
  });
}

// endStateResult ports the I1 terminal conditions onto snapshot data:
// drained -> completed; retry-exhausted child failures -> needsReview;
// blocked with nothing runnable -> needsReview; open-but-unsatisfiable -> needsReview.
function endStateResult(loom, epicId, snapshot, activeCount, completed, blockedFailures) {
  if (!snapshot || activeCount > 0) {
    return null;
  }
  const ready = numberValue(snapshot.readyCount, 0);
  const blocked = numberValue(snapshot.blockedCount, 0);
  const open = numberValue(snapshot.openChildrenCount, 0);
  if (open === 0 && blockedFailures.length === 0) {
    const suffix = completed.length > 0 ? ": " + completed.join(",") : "";
    return loom.completed({ summary: "Epic drained " + epicId + suffix });
  }
  if (ready > 0) {
    return null;
  }
  if (blockedFailures.length > 0) {
    const entries = blockedFailures;
    return loom.needsReview({
      summary: entries.length + " blocked task(s): " + summarizeBlockedTasks(entries),
      errorClass: "epic_tasks_blocked",
    });
  }
  if (blocked > 0) {
    return loom.needsReview({
      summary: "Epic " + epicId + " blocked with " + blocked + " child task(s): " + summarizeTasks(snapshot.blocked),
      errorClass: "epic_blocked",
    });
  }
  if (open > 0) {
    return loom.needsReview({
      summary: "Epic " + epicId + " has " + open + " open child task(s), but none are ready, blocked, or active",
      errorClass: "epic_no_progress",
    });
  }
  return null;
}

// reconcileInFlight aligns the local in-flight map with the snapshot's
// active-run list: adopt active runs this process does not know (restart
// re-entry) and drop entries whose runs are no longer active (their terminal
// journal events, or the next snapshot's end-state check, settle the rest).
function reconcileInFlight(inFlight, active) {
  const runs = active && Array.isArray(active.taskRuns) ? active.taskRuns : [];
  const activeRunIds = new Set();
  for (const run of runs) {
    const taskId = stringValue(run && run.taskId);
    const taskRunId = stringValue(run && (run.taskRunId || run.id));
    if (taskRunId) {
      activeRunIds.add(taskRunId);
    }
    if (taskId && taskRunId && !inFlight.has(taskId)) {
      inFlight.set(taskId, taskRunId);
    }
  }
  for (const [taskId, taskRunId] of Array.from(inFlight)) {
    if (!activeRunIds.has(taskRunId)) {
      inFlight.delete(taskId);
    }
  }
}

function activeCountOf(active, inFlight) {
  return Math.max(numberValue(active && active.activeCount, 0), inFlight ? inFlight.size : 0);
}

// enqueueChildTask requests an enqueue-only TaskRun with the deterministic
// run id. Conflicts mean the run already exists from a previous pass of this
// driver run, so the task is simply back in flight.
async function enqueueChildTask(loom, task, defaults) {
  const taskRunId = deterministicTaskRunId(loom.driverRunId, task.id);
  const request = {
    taskId: task.id,
    taskRunId,
    runner: defaults.runner,
    parentSessionId: defaults.parentSessionId || "",
    nodeId: defaults.targetNodeId || "",
  };
  const sourceRepo = stringValue(task && (task.sourceRepo || task.source_repo));
  if (sourceRepo) {
    request.repoRef = sourceRepo;
  }
  const childInput = childTaskInput(defaults.childInput, loom, task);
  if (Object.keys(childInput).length > 0) {
    request.input = childInput;
  }
  const workerProfileId = defaults.workerProfileId || (defaults.workerPrefix ? defaults.workerPrefix + "-" + slug(task.id) : "");
  if (workerProfileId) {
    request.workerProfileId = workerProfileId;
  }
  try {
    await loom.taskRuns.request(request);
    return { ok: true, taskRunId };
  } catch (err) {
    if (isConflictError(err)) {
      return { ok: true, taskRunId };
    }
    // Pre-execution request failure: nothing is running, so release the
    // claim instead of stranding the task until the lock TTL expires.
    await safeRelease(loom, task.id);
    return {
      ok: false,
      blocked: {
        taskId: task.id,
        taskRunId,
        errorClass: "child_task_request_failed",
        summary: "Task request failed: " + task.id + " - " + errorMessage(err),
      },
    };
  }
}

function childTaskInputDefaults(input) {
  const out = {};
  const nested = input && input.childInput && typeof input.childInput === "object" && !Array.isArray(input.childInput)
    ? input.childInput
    : {};
  for (const [key, value] of Object.entries(nested)) {
    if (value !== undefined && value !== null && value !== "") {
      out[key] = value;
    }
  }
  for (const key of [
    "mode",
    "repoUrl",
    "githubRepo",
    "repositoryUrl",
    "baseBranch",
    "targetBranch",
    "openPullRequest",
    "stackedPullRequests",
    "refreshCodexAuth",
  ]) {
    if (input && input[key] !== undefined && input[key] !== null && input[key] !== "") {
      out[key] = input[key];
    }
  }
  return out;
}

function childTaskInput(defaults, loom, task) {
  const out = {};
  for (const [key, value] of Object.entries(defaults || {})) {
    if (value !== undefined && value !== null && value !== "") {
      out[key] = value;
    }
  }
  if (loom && loom.driverRunId) {
    out.driverRunId = loom.driverRunId;
  }
  if (task && task.id && !out.taskId) {
    out.taskId = task.id;
  }
  const sourceRepo = stringValue(task && (task.sourceRepo || task.source_repo));
  if (sourceRepo && !out.sourceRepo) {
    out.sourceRepo = sourceRepo;
  }
  return out;
}

// completeChildTask finalizes a completed run observed on the watch stream.
// The serve task worker completes-and-closes successful runs itself, so a
// conflict here means the work is already done; completionId keeps genuine
// deferred completions idempotent across replays.
async function completeChildTask(loom, data) {
  const taskId = stringValue(data.taskID);
  const taskRunId = stringValue(data.taskRunID);
  try {
    await loom.tasks.complete({
      taskId,
      taskRunId,
      completionId: "complete-" + taskRunId,
      leaseToken: stringValue(data.leaseToken),
      logsRef: stringValue(data.logsRef),
      artifactsRef: stringValue(data.artifactsRef),
      reason: "completed by epic-runner workflow",
    });
    return { ok: true };
  } catch (err) {
    if (isConflictError(err)) {
      return { ok: true };
    }
    return {
      ok: false,
      blocked: {
        taskId,
        taskRunId,
        errorClass: "child_task_completion_failed",
        summary: "Task completion failed: " + taskId + " - " + errorMessage(err),
        logsRef: stringValue(data.logsRef),
        artifactsRef: stringValue(data.artifactsRef),
      },
    };
  }
}

function isConflictError(err) {
  switch (stringValue(err && err.code)) {
    case "conflict":
    case "already_exists":
    case "invalid_transition":
      return true;
    default:
      return false;
  }
}

async function startEpicRun(loom, input, epicId) {
  const dryRun = booleanValue(input.dryRun);
  const epic = await loom.epics.get({ epicId });
  if (!epic) {
    return {
      ok: false,
      errorClass: "epic_not_found",
      summary: "epic " + epicId + " was not found",
    };
  }
  const issueType = stringValue(epic.issue_type || epic.issueType);
  if (issueType && issueType !== "epic") {
    return {
      ok: false,
      errorClass: "invalid_epic",
      summary: "issue " + epicId + " has type " + JSON.stringify(issueType) + "; epic-runner requires an epic",
    };
  }

  const leadName = stringValue(input.leadName);
  const requestedOrchestrator = stringValue(input.orchestratorSessionId);
  if (!leadName) {
    return {
      ok: true,
      state: "unassigned",
      orchestratorSessionId: requestedOrchestrator,
    };
  }

  const agents = await loom.agents.list();
  const lead = findAgent(agents, leadName);
  if (!lead) {
    return {
      ok: false,
      errorClass: "lead_not_found",
      summary: "lead agent " + JSON.stringify(leadName) + " was not found",
    };
  }
  const roleName = stringValue(lead.role_name || lead.roleName);
  if (!isLeadRole(roleName)) {
    return {
      ok: false,
      errorClass: "invalid_lead_role",
      summary: "agent " + JSON.stringify(leadName) + " has role " + JSON.stringify(roleName) + "; epic-runner requires a lead agent",
    };
  }

  const leadParent = stringValue(lead.parent);
  if (leadParent && leadParent !== epicId) {
    return {
      ok: false,
      errorClass: "lead_already_running_epic",
      summary: "lead " + leadName + " is already running epic " + leadParent + "; clear or finish that epic before running " + epicId,
    };
  }

  const conflictingOwner = findConflictingLeadOwner(agents, leadName, epicId);
  if (conflictingOwner) {
    return {
      ok: false,
      errorClass: "epic_already_claimed",
      summary: "epic " + epicId + " is already claimed by lead " + conflictingOwner,
    };
  }

  const session = requestedOrchestrator
    ? { orchestratorSessionId: requestedOrchestrator }
    : await loom.agents.orchestrationSession({ agent: leadName });
  const orchestratorSessionId = stringValue(session && (session.orchestratorSessionId || session.orchestrator_session_id));

  if (leadParent === epicId) {
    const delivery = dryRun ? { state: "pending" } : await attemptLeadDelivery(loom, leadName);
    return {
      ok: true,
      state: "resumed",
      leadName,
      orchestratorSessionId,
      deliveryState: delivery.state || "pending",
      deliveryReason: delivery.reason || "",
    };
  }

  if (dryRun) {
    return {
      ok: true,
      state: "dry_run",
      leadName,
      orchestratorSessionId,
      deliveryState: "pending",
    };
  }

  const updated = await loom.agents.updateParent({
    agent: leadName,
    parent: epicId,
    expectParent: leadParent,
  });
  if (stringValue(updated && updated.parent) !== epicId) {
    return {
      ok: false,
      errorClass: "lead_bind_conflict",
      summary: "lead " + leadName + " could not be bound to epic " + epicId,
    };
  }
  const delivery = await attemptLeadDelivery(loom, leadName);
  return {
    ok: true,
    state: "assigned",
    leadName,
    orchestratorSessionId,
    deliveryState: delivery.state || "pending",
    deliveryReason: delivery.reason || "",
  };
}

// attemptLeadDelivery fires the assignment delivery op exactly once: the
// server attempts an inline delivery and durably enqueues an outbox row for
// anything short of delivered/unsupported, so retries live server-side.
async function attemptLeadDelivery(loom, leadName) {
  const agent = stringValue(leadName);
  if (!agent) {
    return { state: "none" };
  }
  try {
    const delivery = await loom.agents.deliverAssignment({ agent });
    return {
      state: stringValue(delivery && delivery.state) || "pending",
      reason: stringValue(delivery && delivery.reason),
    };
  } catch (err) {
    return {
      state: "pending",
      reason: errorMessage(err),
    };
  }
}

async function safeRelease(loom, taskId) {
  try {
    await loom.tasks.release(taskId);
  } catch (_err) {
  }
}

function findAgent(agents, name) {
  const list = Array.isArray(agents) ? agents : [];
  return list.find((agent) => stringValue(agent && agent.name) === name) || null;
}

function findConflictingLeadOwner(agents, leadName, epicId) {
  const list = Array.isArray(agents) ? agents : [];
  for (const agent of list) {
    if (!agent || stringValue(agent.name) === leadName) {
      continue;
    }
    const roleName = stringValue(agent.role_name || agent.roleName);
    if (isLeadRole(roleName) && stringValue(agent.parent) === epicId) {
      return stringValue(agent.name);
    }
  }
  return "";
}

function isLeadRole(roleName) {
  switch (stringValue(roleName).toLowerCase()) {
    case "lead":
    case "orchestrator":
      return true;
    default:
      return false;
  }
}

function deterministicTaskRunId(driverRunId, taskId) {
  return "task-run-" + slug(driverRunId || "run") + "-" + slug(taskId || "task");
}

function summarizeTasks(tasks) {
  const list = Array.isArray(tasks) ? tasks : [];
  const parts = list.slice(0, 5).map((task) => task.title ? task.id + " (" + task.title + ")" : task.id);
  if (list.length > 5) {
    parts.push("+" + (list.length - 5) + " more");
  }
  return parts.join(", ");
}

function summarizeBlockedTasks(blocked) {
  const list = Array.isArray(blocked) ? blocked : [];
  return summarizeTasks(list.map((entry) => ({
    id: entry.taskId,
    title: entry.summary || entry.errorMessage || entry.errorClass,
  })));
}

function slug(value) {
  return stringValue(value).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "item";
}

function stringValue(value) {
  return value === undefined || value === null ? "" : String(value).trim();
}

function numberValue(value, fallback) {
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
}

function booleanValue(value) {
  if (typeof value === "boolean") {
    return value;
  }
  switch (stringValue(value).toLowerCase()) {
    case "1":
    case "true":
    case "yes":
      return true;
    default:
      return false;
  }
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}
