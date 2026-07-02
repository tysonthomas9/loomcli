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

// prompt-agent: the PROMPT-AGENT realization (Phase 4, unified-agent-ux-proposal).
//
// This is the workflow-plane realization of a "role agent": a generic task-runner
// driver CONFIGURED WITH A PROMPT. The role's brain (its prompt) travels as DATA,
// never as code. The workflow itself is a thin orchestrator:
//   1. resolve the role prompt — CONFIG BY REFERENCE. The "config travels as
//      input" era is over: a dispatched run's payload carries only EVENT data
//      (taskId, tick), not the binding's config. When the payload names no role,
//      the agent reads its OWN binding's config from the calling run's verified
//      provenance (loom.binding.config → roleName), then resolves the prompt body
//      through the read-only roles surface (loom.roles.get), so "one prompt edit
//      updates every agent". Precedence: input.prompt (explicit one-off override)
//      > input.roleName > binding.config().roleName;
//   2. claim a REAL ready loom task with the existing task lease. A specific
//      target claims by id (loom.tasks.claim — no claim-and-release race);
//      otherwise pickup is filterless (loom.tasks.claimReady, queue order);
//   3. dispatch the bundled local-task-runner for it, delivering the role prompt
//      verbatim as the task-run Input field `taskPrompt` (the runner's
//      LOOM_TASK_RUN_PROMPT > input.taskPrompt > generic precedence — see
//      local-task-runner.ts "prompt = data, brain stays custom"). The runner
//      execFiles the real backend CLI (codex by default) over a prepared git
//      worktree and fails closed — there is no synthetic completion;
//   4. await the task-run and report its outcome.
//
// There is NO daemon supervisor involved: this is a DriverRun executed by the
// driver-run executor, dispatching a TaskRun executed by the serve task worker.
// The role poll-loop / Go execution leaf are never touched. This same source is
// registerable as a CUSTOM (untrusted) workflow driver: it dispatches the
// trusted builtin local-task-runner through workspace-global runner resolution,
// which the driver plane pins to the runner's OWNING builtin version.
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  // 1. Resolve the role prompt as DATA. Precedence (documented):
  //    input.prompt (explicit one-off override) > input.roleName >
  //    binding.config().roleName (config by reference) > input.taskPrompt /
  //    input.rolePrompt aliases. The named role's prompt body is materialized
  //    via roles.get.
  const resolved = await resolvePromptSource(loom, input);
  const prompt = resolved.prompt;
  const promptSource = resolved.source;
  if (!prompt) {
    return loom.failed({
      summary: resolved.roleResolved
        ? "prompt-agent: role " + resolved.roleName + " has no prompt body (roles.get returned an empty prompt); set the role's prompt or pass input.prompt"
        : "prompt-agent requires a prompt: set input.prompt, or input.roleName referencing a role with a prompt body",
      errorClass: "prompt_agent_missing_prompt",
    });
  }

  const actor = stringValue(input.actor) || "prompt-agent";
  const backend = stringValue(input.backend); // optional; informational only (backend is host-resolved)

  // 2. Claim a real ready task with the existing task lease. Targets a task id
  //    when supplied (claim-by-id), else claims any ready task (queue order).
  //    The id may arrive flat (input.taskId — cron/manual dispatch) or nested in
  //    the InternalSource provenance envelope for a task-ready event
  //    (input.event.taskId — the loopback wraps the emitter payload under
  //    "event"; see internal/trigger issue_journal_bridge_task_ready.go).
  const targetId = resolveTargetTaskId(input);
  const claimed = await claimTargetTask(loom, actor, targetId);
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

  // 3. Dispatch the local-task-runner, delivering the ROLE PROMPT AS DATA.
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

  // 4. Report the outcome. The serve task worker auto-closes the card on task
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

// resolvePromptSource materializes the role prompt with documented precedence:
//   input.prompt > roles.get(effectiveRoleName).prompt > input.taskPrompt > input.rolePrompt,
// where effectiveRoleName = input.roleName > binding.config().roleName (config by
// reference). A named role that carries no prompt body is flagged (roleResolved)
// so the caller fails with a precise message instead of silently falling through.
async function resolvePromptSource(loom, input) {
  if (stringValue(input.prompt)) {
    return { prompt: stringValue(input.prompt), source: "input.prompt" };
  }
  // CONFIG BY REFERENCE: input.roleName wins, else fall back to the roleName
  // configured on THIS run's binding (resolved server-side from provenance).
  const roleName = stringValue(input.roleName) || (await bindingConfigRoleName(loom));
  if (roleName) {
    const record = (await loom.roles.get({ name: roleName })) || {};
    const rolePrompt = stringValue(record.prompt);
    if (rolePrompt) {
      return { prompt: rolePrompt, source: "role:" + roleName };
    }
    return { prompt: "", source: "", roleName, roleResolved: true };
  }
  if (stringValue(input.taskPrompt)) {
    return { prompt: stringValue(input.taskPrompt), source: "input.taskPrompt" };
  }
  if (stringValue(input.rolePrompt)) {
    return { prompt: stringValue(input.rolePrompt), source: "input.rolePrompt" };
  }
  return { prompt: "", source: "" };
}

// bindingConfigRoleName reads the roleName configured on the CALLING run's
// trigger binding via config-by-reference (loom.binding.config). The server
// resolves the binding from the run's verified provenance — never from input —
// so the event payload can carry only event data (taskId/tick). Returns "" when
// the run has no binding (a bare `loom workflow run` — the op 404s) or the
// binding configures no role; either way the caller falls through to the
// explicit-input aliases.
async function bindingConfigRoleName(loom) {
  try {
    const cfg = (await loom.binding.config()) || {};
    return stringValue(cfg.roleName);
  } catch {
    return "";
  }
}

// claimTargetTask claims a ready task via the existing task lease. A specific
// targetId claims by id (loom.tasks.claim) — no claim-and-release race; a
// not-ready / already-claimed target rejects conflict, treated as unclaimable
// (null). Without a targetId it does filterless pickup (loom.tasks.claimReady,
// queue order). Returns the ClaimedTask or null.
async function claimTargetTask(loom, actor, targetId) {
  if (targetId) {
    try {
      return await loom.tasks.claim({ taskId: targetId, actor });
    } catch (e) {
      if (isConflictError(e)) return null;
      throw e;
    }
  }
  return await loom.tasks.claimReady({ actor, limit: 1 });
}

// isConflictError reports whether a rejected driver op is the conflict class the
// claim-by-id endpoint returns when the task is not ready or already claimed.
function isConflictError(e) {
  if (!e) return false;
  if (stringValue(e.code) === "conflict") return true;
  const message = stringValue(e.message);
  return message.indexOf("not ready or already claimed") >= 0 || message.indexOf("already claimed") >= 0;
}

// resolveTargetTaskId reads the claim target from the flat input (input.taskId,
// cron/manual) or the InternalSource envelope of a task-ready event
// (input.event.taskId, then input.event.id as a fallback for a raw issue
// snapshot). Returns "" when none is present (filterless pickup).
function resolveTargetTaskId(input) {
  const flat = stringValue(input.taskId);
  if (flat) return flat;
  const event = input && typeof input.event === "object" && input.event ? input.event : null;
  if (event) {
    return stringValue(event.taskId) || stringValue(event.id) || stringValue(event.ID);
  }
  return "";
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
