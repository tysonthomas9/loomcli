import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition. prompt-agent orchestrates via the loom driver SDK
// (it is not itself an LLM agent — the backend CLI runs inside the
// local-task-runner it dispatches), so the bound agent is a credential-free stub
// (model: false). The invocation payload arrives via env: the driver launcher
// sets LOOM_FLUE_INVOKE_PAYLOAD (sandbox/launcher.go).
export default defineWorkflow({
  agent: defineAgent(() => ({ model: false })),
  run: async () => toJsonResult(await run({ payload: builtinInvokePayload() })),
});

function builtinInvokePayload() {
  const raw = process.env.LOOM_FLUE_INVOKE_PAYLOAD || process.env.LOOM_TASK_RUN_REQUEST_JSON || "{}";
  try {
    return JSON.parse(raw);
  } catch {
    return {};
  }
}

function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// prompt-agent: the PROMPT-AGENT spike (Phase 4, unified-agent-ux-proposal).
//
// This is the workflow-plane realization of a "role agent": a generic task-runner
// driver CONFIGURED WITH A PROMPT. The role's brain (its prompt) travels as DATA
// on the workflow input (input.prompt / input.taskPrompt), never as code. The
// workflow itself is a thin orchestrator:
//   1. claim a REAL ready loom task with the existing task lease (claim-ready);
//   2. dispatch the bundled local-task-runner for it, delivering the role prompt
//      verbatim as the task-run Input field `taskPrompt` (the runner's
//      LOOM_TASK_RUN_PROMPT > input.taskPrompt > generic precedence — see
//      local-task-runner.ts "prompt = data, brain stays custom"). The runner
//      execFiles the real backend CLI (codex by default) over a prepared git
//      worktree and fails closed — there is no synthetic completion;
//   3. await the task-run and report its outcome.
//
// There is NO daemon supervisor involved: this is a DriverRun executed by the
// driver-run executor, dispatching a TaskRun executed by the serve task worker.
// The role poll-loop / Go execution leaf are never touched.
//
// GAP (reported by the spike): the driver SDK has no role-read surface, so the
// prompt cannot be resolved from a Role record here — it must be passed as input
// (which is exactly "prompt = config on the binding"). And claim-ready has no
// claim-by-task-id, so targeting a specific task is emulated by claiming ready
// tasks and releasing non-matches (see claimTargetTask).
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  // The role prompt as DATA. input.prompt is the canonical field; taskPrompt /
  // rolePrompt are accepted aliases. input.roleName is NOT resolvable (no role
  // SDK surface) — surfaced as a gap rather than silently ignored.
  const prompt = stringValue(input.prompt || input.taskPrompt || input.rolePrompt);
  const promptSource = input.prompt ? "input.prompt"
    : input.taskPrompt ? "input.taskPrompt"
      : input.rolePrompt ? "input.rolePrompt" : "";
  if (!prompt) {
    return loom.failed({
      summary: stringValue(input.roleName)
        ? "prompt-agent: input.roleName=" + stringValue(input.roleName) + " but no prompt was supplied; the driver SDK cannot read a Role record — pass the role prompt as input.prompt"
        : "prompt-agent requires a prompt (input.prompt); the role's prompt is carried as workflow data",
      errorClass: "prompt_agent_missing_prompt",
    });
  }

  const actor = stringValue(input.actor) || "prompt-agent";
  const backend = stringValue(input.backend); // optional; informational only (backend is host-resolved)

  // 1. Claim a real ready task with the existing task lease. Targets input.taskId
  //    when supplied (release-non-matches emulation), else claims any ready task.
  const targetId = stringValue(input.taskId);
  const claimed = await claimTargetTask(loom, actor, targetId, 10);
  const issueId = claimed && stringValue(claimed.id || claimed.ID);
  if (!issueId) {
    return loom.completed({
      summary: targetId
        ? "prompt-agent: target task " + targetId + " was not claimable (not ready or already claimed)"
        : "prompt-agent: no ready task to claim",
      claimed: false,
    });
  }
  const card = (await loom.issues.get({ issueId })) || {};

  // 2. Dispatch the local-task-runner, delivering the ROLE PROMPT AS DATA.
  //    Deterministic taskRunId keeps a resumed run from double-enqueuing.
  //    openPullRequest=false => patch-back delivery (no GitHub needed).
  const taskRunId = "promptagent-" + issueId;
  const requestInput = { taskPrompt: prompt, openPullRequest: false };
  if (backend) {
    // Informational: the backend is resolved host-side (resolveTaskRunnerBackend);
    // carry it so it shows in the task-run input for observability.
    requestInput.backend = backend;
  }
  await loom.taskRuns.request({
    taskId: issueId,
    taskRunId,
    runner: "local-task-runner",
    input: requestInput,
  });

  const result = await loom.taskRuns.await({ taskRunId, timeoutMs: numberValue(input.timeoutMs, 20 * 60 * 1000) });
  const status = stringValue(result && result.status) || "unknown";
  const meta = (result && (result.runtime_metadata || result.runtimeMetadata)) || {};
  const filesChanged = stringValue(meta.files_changed);
  const patchBack = stringValue(meta.patch_back_status);
  const runBackend = stringValue(meta.backend);

  // 3. Report the outcome. The serve task worker auto-closes the card on task
  //    success (task_worker.go CloseTaskOnSuccess), so a completed task-run
  //    transitions the card claimed -> done with no supervisor involvement.
  if (status === "completed") {
    return loom.completed({
      summary: "prompt-agent: " + issueId + " completed via " + (runBackend || "backend")
        + " (files_changed=" + (filesChanged || "0") + ", patch_back=" + (patchBack || "n/a") + ")",
      issueId,
      taskRunId,
      promptSource,
      backend: runBackend,
      filesChanged,
    });
  }
  return loom.needsReview({
    summary: "prompt-agent: task-run " + taskRunId + " for " + issueId + " ended " + status
      + (result && result.error_message ? " - " + stringValue(result.error_message) : ""),
    errorClass: stringValue(result && (result.error_class || result.errorClass)) || "prompt_agent_task_failed",
    taskRunId,
  });
}

// claimTargetTask claims a ready task via the existing task lease. When targetId
// is set it emulates claim-by-id (which the SDK lacks): it claims ready tasks one
// at a time, keeps the match, and releases every non-match at the end (a claimed
// task is no longer ready, so this cannot re-grab the same non-match and loop).
// Bounded by maxAttempts. Returns the matched ClaimedTask or null.
async function claimTargetTask(loom, actor, targetId, maxAttempts) {
  const mistaken = [];
  let match = null;
  for (let i = 0; i < Math.max(1, maxAttempts); i++) {
    const claimed = await loom.tasks.claimReady({ actor, limit: 1 });
    if (!claimed) break;
    const id = stringValue(claimed.id || claimed.ID);
    if (!id) break;
    if (!targetId || id === targetId) {
      match = claimed;
      break;
    }
    mistaken.push(id);
  }
  for (const id of mistaken) {
    try { await loom.tasks.release(id); } catch (_e) { /* best-effort release */ }
  }
  return match;
}

function stringValue(v) {
  if (typeof v === "string") return v;
  if (v === undefined || v === null) return "";
  return String(v);
}

function numberValue(v, fallback) {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}
