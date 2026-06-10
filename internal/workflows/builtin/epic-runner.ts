import { createLoomDriverClient } from '@loom/sdk/flue';

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const epicId = stringValue(input.epicId || input.epic_id);
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
  if (booleanValue(input.dryRun || input.dry_run)) {
    return loom.completed({
      summary: "Dry-run validated epic " + epicId + (started.leadName ? " for lead " + started.leadName : ""),
    });
  }

  const maxConcurrency = Math.max(1, numberValue(input.maxConcurrency || input.max_concurrency, 2));
  const providerProfile = stringValue(input.providerProfile || input.provider_profile || "flue-local");
  const workerPrefix = stringValue(input.workerPrefix || input.worker_prefix);
  const workerProfileId = stringValue(input.workerProfileId || input.worker_profile_id);
  const targetNodeId = stringValue(input.targetNodeId || input.target_node_id);
  const intervalMs = Math.max(1000, numberValue(input.intervalSeconds || input.interval_seconds, 5) * 1000);
  const leadNotificationDrainMs = Math.max(0, numberValue(input.leadNotificationDrainSeconds || input.lead_notification_drain_seconds, 30) * 1000);
  const completed = [];
  const leadDelivery = startLeadDeliveryRetry(loom, started.leadName, started.deliveryState, intervalMs);
  const taskNotifications = startLeadMessageDeliveryRetry(loom, started.leadName, intervalMs, leadDelivery);

  try {
    while (true) {
      await loom.taskRuns.recoverStale({
        maxAgeSeconds: numberValue(input.staleTaskRunMaxAgeSeconds || input.stale_task_run_max_age_seconds, 300),
        errorClass: "stale_task_run",
        errorMessage: "task run heartbeat is stale",
      });

      const snapshot = await loom.epics.snapshot({ epicId });
      const active = await loom.taskRuns.active({ epicId });
      const activeCount = numberValue(active && active.activeCount, 0);

      if (snapshot.openChildrenCount === 0 && activeCount === 0) {
        await leadDelivery.flush();
        await taskNotifications.drain(leadNotificationDrainMs);
        const suffix = completed.length > 0 ? ": " + completed.join(",") : "";
        return loom.completed({ summary: "Epic drained " + epicId + suffix });
      }

      if (snapshot.readyCount === 0 && snapshot.blockedCount > 0 && activeCount === 0) {
        await leadDelivery.flush();
        await taskNotifications.drain(leadNotificationDrainMs);
        return loom.needsReview({
          summary: "Epic " + epicId + " blocked with " + snapshot.blockedCount + " child task(s): " + summarizeTasks(snapshot.blocked),
          errorClass: "epic_blocked",
        });
      }

      const slots = maxConcurrency - activeCount;
      if (slots <= 0) {
        await sleep(intervalMs);
        continue;
      }

      const claimed = [];
      for (let i = 0; i < slots; i++) {
        const task = await loom.tasks.claimReady({ epicId });
        if (!task) {
          break;
        }
        claimed.push(task);
      }

      if (claimed.length === 0) {
        if (activeCount === 0 && snapshot.readyCount === 0 && snapshot.blockedCount === 0 && snapshot.openChildrenCount > 0) {
          await leadDelivery.flush();
          await taskNotifications.drain(leadNotificationDrainMs);
          return loom.needsReview({
            summary: "Epic " + epicId + " has " + snapshot.openChildrenCount + " open child task(s), but none are ready, blocked, or active",
            errorClass: "epic_no_progress",
          });
        }
        await sleep(intervalMs);
        continue;
      }

      const results = await Promise.all(claimed.map((task) => runChildTask(loom, {
        task,
        driverRunId: loom.driverRunId,
        workerPrefix,
        workerProfileId,
        providerProfile,
        targetNodeId,
        parentSessionId: started.orchestratorSessionId || stringValue(input.parentSessionId || input.parent_session_id),
      })));
      for (const result of results) {
        if (!result.ok) {
          await leadDelivery.flush();
          await taskNotifications.drain(leadNotificationDrainMs);
          return loom.needsReview({
            summary: result.summary,
            errorClass: result.errorClass || "child_task_failed",
            taskRunId: result.taskRunId,
            logsRef: result.logsRef || "",
            artifactsRef: result.artifactsRef || "",
          });
        }
        completed.push(result.taskId);
        taskNotifications.enqueue(formatTaskCompleteLeadMessage({
          epicId,
          taskId: result.taskId,
          taskTitle: result.taskTitle,
          taskRunId: result.taskRunId,
          logsRef: result.logsRef,
          artifactsRef: result.artifactsRef,
        }));
      }
    }
  } finally {
    leadDelivery.stop();
    taskNotifications.stop();
    await leadDelivery.done;
    await taskNotifications.done;
  }
}

async function startEpicRun(loom, input, epicId) {
  const dryRun = booleanValue(input.dryRun || input.dry_run);
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

  const leadName = stringValue(input.leadName || input.lead_name);
  const requestedOrchestrator = stringValue(input.orchestratorSessionId || input.orchestrator_session_id);
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

function startLeadDeliveryRetry(loom, leadName, initialState, intervalMs) {
  const state = {
    leadName: stringValue(leadName),
    done: !stringValue(leadName) || initialState === "delivered" || initialState === "unsupported",
    deliveryState: stringValue(initialState) || "pending",
    stopped: false,
    inFlight: null,
  };
  async function tryOnce() {
    if (state.done) {
      return { state: "delivered" };
    }
    if (state.inFlight) {
      return state.inFlight;
    }
    state.inFlight = (async () => {
      const delivery = await attemptLeadDelivery(loom, state.leadName);
      state.deliveryState = delivery.state || "pending";
      if (delivery.state === "delivered" || delivery.state === "unsupported" || delivery.state === "none") {
        state.done = true;
      }
      return delivery;
    })();
    try {
      return await state.inFlight;
    } finally {
      state.inFlight = null;
    }
  }
  async function loop() {
    while (!state.stopped && !state.done) {
      await tryOnce();
      if (!state.stopped && !state.done) {
        await sleep(Math.max(1000, Math.min(intervalMs, 5000)));
      }
    }
  }
  return {
    stop() {
      state.stopped = true;
    },
    flush: tryOnce,
    isDone() {
      return state.done;
    },
    deliveryState() {
      return state.deliveryState;
    },
    done: loop(),
  };
}

async function attemptLeadMessageDelivery(loom, leadName, message) {
  const agent = stringValue(leadName);
  const text = stringValue(message);
  if (!agent || !text) {
    return { state: "none" };
  }
  try {
    const delivery = await loom.agents.message({ agent, message: text });
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

function startLeadMessageDeliveryRetry(loom, leadName, intervalMs, assignmentDelivery) {
  const state = {
    leadName: stringValue(leadName),
    messages: [],
    unsupported: false,
    stopped: false,
    inFlight: null,
  };
  async function tryOnce() {
    if (!state.leadName || state.unsupported || state.messages.length === 0) {
      return { state: state.unsupported ? "unsupported" : "none" };
    }
    if (state.inFlight) {
      return state.inFlight;
    }
    state.inFlight = (async () => {
      if (assignmentDelivery && assignmentDelivery.deliveryState() === "unsupported") {
        state.unsupported = true;
        state.messages = [];
        return { state: "unsupported" };
      }
      if (assignmentDelivery && !assignmentDelivery.isDone()) {
        const delivery = await assignmentDelivery.flush();
        if ((delivery && delivery.state === "unsupported") || assignmentDelivery.deliveryState() === "unsupported") {
          state.unsupported = true;
          state.messages = [];
          return { state: "unsupported" };
        }
        if (!assignmentDelivery.isDone()) {
          return { state: "pending", reason: "lead assignment delivery is pending" };
        }
      }
      const delivery = await attemptLeadMessageDelivery(loom, state.leadName, state.messages[0]);
      if (delivery.state === "delivered" || delivery.state === "none") {
        state.messages.shift();
      } else if (delivery.state === "unsupported") {
        state.unsupported = true;
        state.messages = [];
      }
      return delivery;
    })();
    try {
      return await state.inFlight;
    } finally {
      state.inFlight = null;
    }
  }
  async function loop() {
    while (!state.stopped) {
      await tryOnce();
      if (!state.stopped) {
        await sleep(Math.max(1000, Math.min(intervalMs, 5000)));
      }
    }
  }
  return {
    enqueue(message) {
      const text = stringValue(message);
      if (state.leadName && text && !state.unsupported) {
        if (state.messages.length > 0 && !state.inFlight) {
          state.messages[state.messages.length - 1] = state.messages[state.messages.length - 1] + "\n\n" + text;
        } else {
          state.messages.push(text);
        }
      }
    },
    flush: tryOnce,
    async drain(timeoutMs) {
      const startedAt = Date.now();
      let last = { state: state.messages.length > 0 ? "pending" : "none" };
      while (!state.stopped && !state.unsupported && state.messages.length > 0) {
        last = await tryOnce();
        if (state.messages.length === 0 || state.unsupported) {
          return last;
        }
        if (timeoutMs <= 0 || Date.now() - startedAt >= timeoutMs) {
          return last;
        }
        await sleep(Math.min(1000, Math.max(0, timeoutMs - (Date.now() - startedAt))));
      }
      return last;
    },
    stop() {
      state.stopped = true;
    },
    done: loop(),
  };
}

async function runChildTask(loom, opts) {
  const task = opts.task;
  const taskRunId = deterministicTaskRunId(opts.driverRunId, task.id);
  const request = {
    taskId: task.id,
    taskRunId,
    providerProfile: opts.providerProfile,
    parentSessionId: opts.parentSessionId || "",
    nodeId: opts.targetNodeId || "",
    supportedProviders: [opts.providerProfile],
    sandboxPlacement: { provider: opts.providerProfile },
  };
  const workerProfileId = opts.workerProfileId || (opts.workerPrefix ? opts.workerPrefix + "-" + slug(task.id) : "");
  if (workerProfileId) {
    request.workerProfileId = workerProfileId;
  }
  let result;
  try {
    result = await loom.taskRuns.request(request);
  } catch (err) {
    await safeRelease(loom, task.id);
    return {
      ok: false,
      taskId: task.id,
      taskRunId,
      errorClass: "child_task_request_failed",
      summary: "Task request failed: " + task.id + " - " + errorMessage(err),
    };
  }

  if (result && result.status === "completed") {
    const completedTaskRunId = result.taskRunId || result.id || taskRunId;
    const logsRef = result.logsRef || "";
    const artifactsRef = result.artifactsRef || "";
    try {
      await loom.tasks.complete({
        taskId: task.id,
        taskRunId: completedTaskRunId,
        leaseToken: result.leaseToken || "",
        logsRef,
        artifactsRef,
        artifactIds: result.artifactIds || [],
        reason: "completed by epic-runner workflow",
      });
    } catch (err) {
      return {
        ok: false,
        taskId: task.id,
        taskRunId: completedTaskRunId,
        logsRef,
        artifactsRef,
        errorClass: "child_task_completion_failed",
        summary: "Task completion failed: " + task.id + " - " + errorMessage(err),
      };
    }
    return { ok: true, taskId: task.id, taskTitle: stringValue(task.title), taskRunId: completedTaskRunId, logsRef, artifactsRef };
  }

  await safeRelease(loom, task.id);
  return {
    ok: false,
    taskId: task.id,
    taskRunId: result ? result.taskRunId || result.id || taskRunId : taskRunId,
    logsRef: result ? result.logsRef || "" : "",
    artifactsRef: result ? result.artifactsRef || "" : "",
    errorClass: result ? result.errorClass || "child_task_failed" : "child_task_failed",
    summary: "Task failed: " + task.id + (result && result.errorMessage ? " - " + result.errorMessage : ""),
  };
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

function formatTaskCompleteLeadMessage(input) {
  const taskTitle = stringValue(input.taskTitle);
  const taskLine = stringValue(input.taskId) + (taskTitle ? " - " + taskTitle : "");
  const lines = [
    "Loom completed a child task under the active epic-runner workflow.",
    "",
    "epic: " + stringValue(input.epicId),
    "task: " + taskLine,
    "task_run: " + stringValue(input.taskRunId),
  ];
  if (stringValue(input.logsRef)) {
    lines.push("logs: " + stringValue(input.logsRef));
  }
  if (stringValue(input.artifactsRef)) {
    lines.push("artifacts: " + stringValue(input.artifactsRef));
  }
  lines.push("");
  lines.push("Acknowledge this completion in the visible conversation, update your epic status summary, and continue monitoring the remaining child tasks. Do not start another epic runner.");
  return lines.join("\n");
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

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function errorMessage(err) {
  return err && err.message ? err.message : String(err);
}
