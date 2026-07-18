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

  // Role phase (TaskFilter) drives BOTH claim gating and the completion
  // outcome: needs_plan => planner (design + review, no close); has_design =>
  // coder (implement + close on success, today's behavior). A non-role prompt
  // (input.prompt / taskPrompt) carries no filter and is never phase-gated.
  const taskFilter = stringValue(resolved.taskFilter);
  const isPlanner = taskFilter === "needs_plan";

  // 2a. WS2b event-path gate. A task.ready event carries a definite hasDesign;
  //     decide the phase BEFORE claiming so a mismatch costs zero dispatch (no
  //     codex spend). An old emitter without hasDesign falls through to the
  //     post-claim check below rather than guessing.
  const event = eventPayload(input);
  const eventHasDesign = event && typeof event.hasDesign === "boolean" ? event.hasDesign : undefined;
  const gatedByEvent = isGatingFilter(taskFilter) && eventHasDesign !== undefined;
  if (gatedByEvent && !phaseAllows(taskFilter, eventHasDesign, eventLabels(event))) {
    return loom.completed({
      summary: notMyPhaseSummary(resolved.roleName, taskFilter, resolveTargetTaskId(input), eventHasDesign),
      claimed: false,
      skipped: true,
    });
  }

  // 2b. Claim a real ready task with the existing task lease. Targets a task id
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
  // 2c. WS2b post-claim gate for the cron / run-now / explicit path (or a stale
  //     event with no hasDesign): check the phase against the REAL card and, on
  //     mismatch, hand the claim back so the true owner (planner vs coder) can
  //     take it — parking it under our lease would starve them until TTL.
  if (isGatingFilter(taskFilter)) {
    let card;
    try {
      card = (await loom.issues.get({ issueId })) || {};
    } catch (err) {
      // The claim is already durable. A failed follow-up read must return that
      // exact typed generation before surfacing the read error; otherwise the
      // task looks owned until its lock TTL even though no child was requested.
      await releaseClaimAfterError(loom, issueId, "read the claimed task", err);
      throw err;
    }
    const cardHasDesign = stringValue(card.design) !== "";
    if (!phaseAllows(taskFilter, cardHasDesign, cardLabels(card))) {
      await unclaimTask(loom, issueId);
      return loom.completed({
        summary: notMyPhaseSummary(resolved.roleName, taskFilter, issueId, cardHasDesign),
        issueId,
        claimed: false,
        released: true,
        skipped: true,
      });
    }
  }

  // 3. Dispatch the local-task-runner, delivering the ROLE PROMPT AS DATA.
  //    The taskRunId is deterministic PER DRIVER-RUN (not per issue): the same
  //    issue legitimately gets multiple dispatches across its life (planner,
  //    then coder, then rework), so scoping by driver-run keeps those distinct
  //    while an enqueue conflict still means "THIS run already enqueued it" —
  //    the durable-resume case, handled below by proceeding to await.
  //    openPullRequest=false keeps the GitHub PR path off. Coder runs ask for
  //    local-branch delivery, but local-task-runner strictly gates that on a
  //    filesystem origin; GitHub/http/ssh origins therefore keep today's
  //    patch-back behavior. Planner runs opt out because their deliverable is
  //    issue design/status data, not a reviewable code branch.
  //    Every role run passes closeTask=false. The trusted runner may return the
  //    typed needs_revision disposition, and the workflow host must observe the
  //    terminal receipt before deciding close vs review vs re-plan. This also
  //    means an interrupted host fails safe in Fleet's non-closing terminal
  //    state, never with a falsely closed card.
  const taskRunId = "promptagent-" + (stringValue(loom.driverRunId) || "run") + "-" + issueId;
  const requestInput = { taskPrompt: prompt, openPullRequest: false };
  requestInput.deliveryMode = isPlanner ? "patch-back" : "local-branch";
  if (backend) {
    // Informational: the backend is resolved host-side (resolveTaskRunnerBackend);
    // carry it so it shows in the task-run input for observability.
    requestInput.backend = backend;
  }
  const requestParams = {
    taskId: issueId,
    taskRunId,
    runner: "local-task-runner",
    input: requestInput,
  };
  requestParams.closeTask = false;
  try {
    await loom.taskRuns.request(requestParams);
  } catch (e) {
    if (!isAmbiguousTaskRunRequestError(e)) {
      // A certified pre-commit rejection can safely hand the task back. An
      // ambiguous timeout/disconnect/internal response may follow a committed
      // TaskRun receipt, so it must retain the claim instead of creating an
      // open Work Item with a queued child.
      // Exact durable request replay is resolved inside the SDK/service before
      // returning. A 409 here is therefore a real lineage/envelope conflict,
      // not evidence that a resumable child exists; never await a phantom run.
      await releaseClaimAfterError(loom, issueId, "request the TaskRun", e);
    }
    throw e;
  }

  const result = await loom.taskRuns.await({ taskRunId, timeoutMs: numberValue(input.timeoutMs, 20 * 60 * 1000) });
  const status = stringValue(result && result.status) || "unknown";
  const meta = (result && (result.runtime_metadata || result.runtimeMetadata)) || {};
  const filesChanged = stringValue(meta.files_changed);
  const patchBack = stringValue(meta.patch_back_status);
  const runBackend = stringValue(meta.backend);
  const delivery = stringValue(meta.delivery);
  const localBranch = stringValue(meta.local_branch);
  const headSha = stringValue(meta.head_sha);
  const taskOutcome = stringValue(meta.task_outcome);

  // 4. Report the outcome.
  if (status === "completed") {
    if (isPlanner) {
      // The planner backend writes the design only. The TaskRun terminal receipt
      // has now retired its live Work Item claim, so this host-owned mutation can
      // safely hand the card to review. The idempotent read still tolerates an
      // older/custom planner that already left the card there.
      let reviewed;
      try {
        reviewed = await ensureCardInReview(loom, issueId);
      } catch (err) {
        return loom.needsReview({
          summary: "prompt-agent: planner TaskRun " + taskRunId
            + " completed, but the host could not hand " + issueId + " to review: " + errorMessage(err),
          errorClass: "prompt_agent_planner_handoff_failed",
          issueId,
          taskRunId,
        });
      }
      return loom.completed({
        summary: "prompt-agent: " + issueId + " planned via " + (runBackend || "backend")
          + " (design handoff, status=" + reviewed + ")",
        issueId,
        taskRunId,
        promptSource,
        backend: runBackend,
        outcome: "design-review",
      });
    }
    // Coder outcome: closeTask=false kept lifecycle authority with this host.
    // For local-branch delivery, the pushed branch is the review artifact; stamp
    // review + external_ref only after the terminal receipt retired the typed
    // generation. Patch-back completion is closed explicitly below.
    if (delivery === "local_branch") {
      if (!localBranch || !headSha) {
        return loom.needsReview({
          summary: "prompt-agent: task-run " + taskRunId + " reported local_branch delivery without local_branch/head_sha metadata",
          errorClass: "prompt_agent_local_branch_metadata_missing",
          issueId,
          taskRunId,
        });
      }
      const externalRef = "local-branch:" + localBranch + "@" + headSha;
      try {
        await stampLocalBranchReview(loom, issueId, externalRef);
      } catch (err) {
        return loom.needsReview({
          summary: "prompt-agent: " + issueId + " pushed local branch " + localBranch
            + " @ " + headSha + " but failed to stamp review external_ref: " + errorMessage(err),
          errorClass: "prompt_agent_review_stamp_failed",
          issueId,
          taskRunId,
          delivery,
          local_branch: localBranch,
          head_sha: headSha,
          external_ref: externalRef,
        });
      }
      return loom.completed({
        summary: "prompt-agent: " + issueId + " completed via " + (runBackend || "backend")
          + " (local branch " + localBranch + " pushed, in review)",
        issueId,
        taskRunId,
        promptSource,
        backend: runBackend,
        filesChanged,
        delivery,
        local_branch: localBranch,
        head_sha: headSha,
        external_ref: externalRef,
      });
    }
    try {
      await closeTerminalCard(loom, issueId);
    } catch (err) {
      return loom.needsReview({
        summary: "prompt-agent: task-run " + taskRunId + " completed, but the host could not close "
          + issueId + ": " + errorMessage(err),
        errorClass: "prompt_agent_coder_handoff_failed",
        issueId,
        taskRunId,
      });
    }
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
  // Cancellation retires the typed generation and atomically releases the Work
  // Item to open+unassigned. Reconcile that state and the typed needs-revision
  // label after the terminal receipt. A failed TaskRun is different: retry
  // exhaustion atomically blocks its Work Item, and reopening it here would
  // erase that safety policy and create an automatic spend loop.
  if (status === "cancelled") {
    try {
      await releaseTerminalCard(loom, issueId, taskOutcome);
    } catch (err) {
      return loom.needsReview({
        summary: "prompt-agent: task-run " + taskRunId + " for " + issueId + " ended " + status
          + ", but the host could not reconcile the terminal card: " + errorMessage(err),
        errorClass: "prompt_agent_terminal_handoff_failed",
        taskRunId,
      });
    }
  }
  return loom.needsReview({
    summary: "prompt-agent: task-run " + taskRunId + " for " + issueId + " ended " + status
      + (result && result.error_message ? " - " + stringValue(result.error_message) : "")
      + (status === "cancelled" ? " (returned to open+unassigned)" : " (left blocked for review)"),
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
    // The role record (record.role) carries the TaskFilter that drives phase
    // gating and planner-vs-coder outcome. Go domain wire is snake_case.
    const taskFilter = stringValue(record.role && (record.role.task_filter || record.role.taskFilter));
    if (rolePrompt) {
      return { prompt: rolePrompt, source: "role:" + roleName, roleName, taskFilter };
    }
    return { prompt: "", source: "", roleName, roleResolved: true, taskFilter };
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

function isAmbiguousTaskRunRequestError(e) {
  switch (stringValue(e && e.code)) {
    case "timeout":
    case "unavailable":
    case "internal":
      return true;
    default:
      return false;
  }
}

// resolveTargetTaskId reads the claim target from the flat input (input.taskId,
// cron/manual) or the InternalSource envelope of a task-ready event
// (input.event.taskId, then input.event.id as a fallback for a raw issue
// snapshot). Returns "" when none is present (filterless pickup).
function resolveTargetTaskId(input) {
  const flat = stringValue(input.taskId);
  if (flat) return flat;
  const event = eventPayload(input);
  if (event) {
    return stringValue(event.taskId) || stringValue(event.id) || stringValue(event.ID);
  }
  return "";
}

// eventPayload returns the InternalSource task-ready envelope (input.event) when
// present — the pinned payload contract is { taskId, status, hasDesign?,
// labels?, issueType? } — else null (cron / run-now / explicit dispatch).
function eventPayload(input) {
  return input && typeof input.event === "object" && input.event ? input.event : null;
}

// isGatingFilter reports whether a role's TaskFilter participates in phase
// gating. Only the plan (needs_plan) and coder (has_design) filters do; a
// filterless role or a non-role prompt is never gated.
function isGatingFilter(taskFilter) {
  return taskFilter === "needs_plan" || taskFilter === "has_design";
}

// phaseAllows applies the role/phase predicate against a task's design state:
//   needs_plan (planner) — proceed only when there is NO design yet, OR the card
//     is flagged needs-revision (a rejected design to redo);
//   has_design (coder)   — proceed only when a design is present and is NOT
//     flagged needs-revision (the planner owns that rework phase).
// A non-gating filter always allows.
function phaseAllows(taskFilter, hasDesign, labels) {
  const hasRevision = labelList(labels).includes("needs-revision");
  if (taskFilter === "needs_plan") return hasDesign !== true || hasRevision;
  if (taskFilter === "has_design") return hasDesign === true && !hasRevision;
  return true;
}

// labelList normalizes a labels value (array, or absent) to an array of strings.
function labelList(labels) {
  if (!Array.isArray(labels)) return [];
  return labels.map((l) => stringValue(l));
}

// eventLabels / cardLabels read the labels array off the two payload shapes:
// the task-ready event envelope and the full issue card (issues.get).
function eventLabels(event) {
  return event ? labelList(event.labels) : [];
}

function cardLabels(card) {
  return card ? labelList(card.labels) : [];
}

// unclaimTask returns the typed DriverRun Work Item claim to the ready pool.
// The release command atomically restores open+unassigned, clears the exact
// claim generation, releases the actor lock, and records the durable action.
// A generic issue.update is intentionally forbidden while that generation is
// live. Release failure is authoritative: callers must not claim a successful
// handback while the Work Item remains owned.
async function unclaimTask(loom, issueId) {
  await loom.tasks.release({ taskId: issueId });
}

// releaseClaimAfterError preserves both halves of a post-claim failure. When
// typed release succeeds the caller rethrows the original operation error. If
// release itself fails, surface that authoritative cleanup failure with the
// original cause in the message rather than pretending the Work Item is free.
async function releaseClaimAfterError(loom, issueId, operation, originalError) {
  try {
    await unclaimTask(loom, issueId);
  } catch (releaseError) {
    throw new Error("prompt-agent: failed to " + operation + " for " + issueId
      + " (" + errorMessage(originalError) + ") and typed release also failed: "
      + errorMessage(releaseError));
  }
}

// releaseTerminalCard runs only after taskRuns.await returned a terminal
// non-success result. Fleet has retired the exact typed generation at that
// point, so this host-owned lifecycle handoff is no longer fenced.
async function releaseTerminalCard(loom, issueId, taskOutcome) {
  // If this is the validated needs_revision disposition, make the label
  // idempotently host-owned as well. The agent may already have written richer
  // notes/content, but it never owns status or assignee transitions.
  if (taskOutcome === "needs_revision") {
    await loom.issues.addLabel({ issueId, label: "needs-revision" });
  }
  await loom.issues.update({ issueId, status: "open", assignee: "" });
}

async function closeTerminalCard(loom, issueId) {
  await loom.issues.update({ issueId, status: "closed", assignee: "" });
}

// notMyPhaseSummary is the honest skip summary for a phase mismatch — the run
// completes without dispatching any backend work.
function notMyPhaseSummary(roleName, taskFilter, taskId, hasDesign) {
  return "prompt-agent: not my phase (role " + (stringValue(roleName) || "?")
    + ", filter " + taskFilter + "): task " + (stringValue(taskId) || "?")
    + " hasDesign=" + String(hasDesign) + " — skipped, no dispatch";
}

// ensureCardInReview reconciles a completed planner run's card to review after
// the TaskRun terminal receipt retires its live Work Item claim. An older/custom
// planner may already have moved it; if so we do not double-move. A failed
// handoff is surfaced to the caller so the DriverRun cannot report completion
// while the Work Item remains in_progress.
async function ensureCardInReview(loom, issueId) {
  const after = (await loom.issues.get({ issueId })) || {};
  if (stringValue(after.status) === "review" && stringValue(after.assignee) === "") {
    return "review";
  }
  await loom.issues.update({ issueId, status: "review", assignee: "" });
  return "review";
}

// stampLocalBranchReview performs the S2 handoff for local review. The task
// worker deliberately left the coder card open on success. This is not
// best-effort: without the stamp, WS5 has no published ref to review.
async function stampLocalBranchReview(loom, issueId, externalRef) {
  await loom.issues.update({ issueId, status: "review", assignee: "", externalRef });
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

function errorMessage(err) {
  if (!err) return "";
  if (err instanceof Error && err.message) return err.message;
  return stringValue(err);
}
