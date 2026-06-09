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
  const workerPrefix = stringValue(input.workerPrefix || input.worker_prefix || slug(epicId));
  const targetNodeId = stringValue(input.targetNodeId || input.target_node_id);
  const intervalMs = Math.max(1000, numberValue(input.intervalSeconds || input.interval_seconds, 5) * 1000);
  const completed = [];

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
      const suffix = completed.length > 0 ? ": " + completed.join(",") : "";
      return loom.completed({ summary: "Epic drained " + epicId + suffix });
    }

    if (snapshot.readyCount === 0 && snapshot.blockedCount > 0 && activeCount === 0) {
      return loom.needsHuman({
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
        return loom.needsHuman({
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
      providerProfile,
      targetNodeId,
    })));
    for (const result of results) {
      if (!result.ok) {
        return loom.needsHuman({
          summary: result.summary,
          errorClass: result.errorClass || "child_task_failed",
          taskRunId: result.taskRunId,
          logsRef: result.logsRef || "",
          artifactsRef: result.artifactsRef || "",
        });
      }
      completed.push(result.taskId);
    }
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
    return {
      ok: true,
      state: "resumed",
      leadName,
      orchestratorSessionId,
      deliveryState: "delivered",
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
  return {
    ok: true,
    state: "assigned",
    leadName,
    orchestratorSessionId,
    deliveryState: "pending",
  };
}

async function runChildTask(loom, opts) {
  const task = opts.task;
  const taskRunId = deterministicTaskRunId(opts.driverRunId, task.id);
  let result;
  try {
    result = await loom.taskRuns.request({
      taskId: task.id,
      taskRunId,
      providerProfile: opts.providerProfile,
      workerProfileId: opts.workerPrefix + "-" + slug(task.id),
      nodeId: opts.targetNodeId || "",
      supportedProviders: [opts.providerProfile],
      sandboxPlacement: { provider: opts.providerProfile },
    });
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
    try {
      await loom.tasks.complete({
        taskId: task.id,
        taskRunId: result.taskRunId || result.id || taskRunId,
        leaseToken: result.leaseToken || "",
        logsRef: result.logsRef || "",
        artifactsRef: result.artifactsRef || "",
        artifactIds: result.artifactIds || [],
        reason: "completed by epic-runner workflow",
      });
    } catch (err) {
      return {
        ok: false,
        taskId: task.id,
        taskRunId: result.taskRunId || result.id || taskRunId,
        logsRef: result.logsRef || "",
        artifactsRef: result.artifactsRef || "",
        errorClass: "child_task_completion_failed",
        summary: "Task completion failed: " + task.id + " - " + errorMessage(err),
      };
    }
    return { ok: true, taskId: task.id };
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
