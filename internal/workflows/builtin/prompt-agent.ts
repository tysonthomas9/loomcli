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

  // A repository-less task in a zero- or multi-repo workspace cannot be
  // checked out safely. This is an admission decision, so it deliberately
  // precedes role/config/prompt resolution: an explanatory no-claim result must
  // not turn into prompt_agent_missing_prompt because an unrelated role record
  // is stale. Automation dispatches the TriggerEvent payload flat; legacy
  // InternalSource fixtures may still carry it under input.event.
  const event = eventPayload(input);
  if (event && event.repositoryRequired === true) {
    const blockedTaskId = resolveTargetTaskId(input);
    if (!blockedTaskId) {
      return loom.failed({
        summary: "prompt-agent: repository-required event is missing its target task id",
        errorClass: "prompt_agent_repository_block_target_missing",
      });
    }
    // Production blocks this condition at the task-ready boundary before a
    // DriverRun is created. Keep the workflow-side guard authoritative for
    // alternate/manual hosts, using the same conditional Work Items command:
    // a stale event must never overwrite a concurrent claim or repository
    // assignment. It deliberately precedes role/prompt resolution.
    try {
      await loom.issues.blockRepositoryRequired({ issueId: blockedTaskId });
    } catch (err) {
      return loom.failed({
        summary: "prompt-agent: could not move repository-required task " + blockedTaskId
          + " to blocked: " + errorMessage(err),
        errorClass: "prompt_agent_repository_block_failed",
      });
    }
    return loom.completed({
      summary: "prompt-agent: target task " + (blockedTaskId || "?")
        + " requires a repository before it can run",
      issueId: blockedTaskId,
      claimed: false,
      skipped: true,
      blocker: "repository_required",
    });
  }

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
  // coder (implement + close on success, today's behavior). UI-created roles
  // historically persisted either an empty filter or "any"; treat both as the
  // safe coder phase so they cannot claim work that still needs planning.
  // Unknown named-role filters fail closed before any claim. A non-role prompt
  // (input.prompt / taskPrompt) remains intentionally filterless.
  const rolePhase = resolveRolePhase(resolved);
  if (!rolePhase.supported) {
    return loom.failed({
      summary: "prompt-agent: role " + (stringValue(resolved.roleName) || "?")
        + " has unsupported task_filter " + JSON.stringify(rolePhase.rawTaskFilter)
        + "; expected needs_plan or has_design",
      errorClass: "prompt_agent_unsupported_task_filter",
      roleName: stringValue(resolved.roleName),
      taskFilter: rolePhase.rawTaskFilter,
      claimed: false,
    });
  }
  const taskFilter = rolePhase.taskFilter;
  const isPlanner = taskFilter === "needs_plan";
  const isReadOnly = resolved.readOnly === true;

  // 2a. WS2b event-path gate. A task.ready event carries a definite hasDesign;
  //     decide the phase BEFORE claiming so a mismatch costs zero dispatch (no
  //     codex spend). An old emitter without hasDesign falls through to the
  //     post-claim check below rather than guessing.
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
  requestInput.deliveryMode = isPlanner || isReadOnly ? "patch-back" : "local-branch";
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
  const sourceRepo = stringValue(claimed && (claimed.sourceRepo || claimed.source_repo));
  if (sourceRepo) {
    requestParams.repoRef = sourceRepo;
  }
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
      // has now retired its live Work Item claim. Verify the claimed card really
      // carries that durable output before reporting a design handoff: a child
      // process exiting successfully is not proof that it persisted the design.
      let plannedCard;
      try {
        plannedCard = (await loom.issues.get({ issueId })) || {};
      } catch (err) {
        return loom.needsReview({
          summary: "prompt-agent: planner TaskRun " + taskRunId
            + " completed, but the host could not verify a persisted design for "
            + issueId + ": " + errorMessage(err),
          errorClass: "prompt_agent_planner_design_read_failed",
          issueId,
          taskRunId,
        });
      }
      const hasPersistedDesign = stringValue(plannedCard.design).trim() !== "";

      // The post-terminal lifecycle transition remains host-owned even when the
      // planner produced no design: move the now-unclaimed card to review and
      // surface a typed needs-review outcome instead of silently reopening it
      // into an automatic spend loop. The idempotent handoff still tolerates an
      // older/custom planner that already left the card there.
      let reviewed;
      try {
        reviewed = await ensureCardInReview(loom, issueId, plannedCard);
      } catch (err) {
        return loom.needsReview({
          summary: "prompt-agent: planner TaskRun " + taskRunId
            + " completed, but the host could not hand " + issueId + " to review: " + errorMessage(err),
          errorClass: "prompt_agent_planner_handoff_failed",
          issueId,
          taskRunId,
        });
      }
      if (!hasPersistedDesign) {
        return loom.needsReview({
          summary: "prompt-agent: planner TaskRun " + taskRunId + " completed, but "
            + issueId + " has no persisted design (handed to " + reviewed + " for review)",
          errorClass: "prompt_agent_planner_design_missing",
          issueId,
          taskRunId,
          outcome: "design-missing",
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
    if (isReadOnly) {
      try {
        await ensureCardInReview(loom, issueId, {});
      } catch (err) {
        return loom.needsReview({
          summary: "prompt-agent: read-only TaskRun " + taskRunId
            + " completed, but the host could not hand " + issueId + " to review: " + errorMessage(err),
          errorClass: "prompt_agent_read_only_handoff_failed",
          issueId,
          taskRunId,
        });
      }
      return loom.completed({
        summary: "prompt-agent: " + issueId + " completed read-only analysis via "
          + (runBackend || "backend") + " and was handed to review",
        issueId,
        taskRunId,
        promptSource,
        backend: runBackend,
        outcome: "read-only-review",
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
    const role = record.role || {};
    // The role record (record.role) carries the TaskFilter that drives phase
    // gating and planner-vs-coder outcome. Go domain wire is snake_case.
    const taskFilter = stringValue(role.task_filter || role.taskFilter);
    const readOnly = role.read_only === true || role.readOnly === true;
    if (rolePrompt) {
      return { prompt: rolePrompt, source: "role:" + roleName, roleName, taskFilter, readOnly };
    }
    return { prompt: "", source: "", roleName, roleResolved: true, taskFilter, readOnly };
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

// resolveRolePhase makes every named role choose one of the two supported
// lifecycle owners before it can claim work. Empty and legacy "any" filters
// came from the UI's custom-role path; preserving them as ungated would let a
// coder-shaped lifecycle steal needs-plan work, so both migrate in place to
// has_design. Explicit one-off prompts have no roleName and remain filterless.
function resolveRolePhase(resolved) {
  if (!stringValue(resolved && resolved.roleName)) {
    return { supported: true, taskFilter: "", rawTaskFilter: "" };
  }
  const rawTaskFilter = stringValue(resolved.taskFilter).trim();
  if (rawTaskFilter === "needs_plan" || rawTaskFilter === "has_design") {
    return { supported: true, taskFilter: rawTaskFilter, rawTaskFilter };
  }
  if (rawTaskFilter === "" || rawTaskFilter === "any") {
    return { supported: true, taskFilter: "has_design", rawTaskFilter };
  }
  return { supported: false, taskFilter: "", rawTaskFilter };
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

// eventPayload returns either the legacy InternalSource envelope (input.event)
// or Automation's real flat TriggerEvent payload. A flat manual/run-now input
// is not treated as task.ready merely because it targets a task: it must also
// carry an open status or a typed task-ready gate. This preserves explicit
// one-off taskId dispatch while making already-persisted flat events (including
// Phase 4's repositoryRequired failure) retry correctly.
function eventPayload(input) {
  if (!input || typeof input !== "object") return null;
  if (typeof input.event === "object" && input.event) return input.event;
  if (!stringValue(input.taskId)) return null;
  const openStatus = stringValue(input.status).toLowerCase() === "open";
  const typedTaskReadyGate = typeof input.repositoryRequired === "boolean"
    || typeof input.hasDesign === "boolean";
  return openStatus || typedTaskReadyGate ? input : null;
}

// isGatingFilter reports whether a role's TaskFilter participates in phase
// gating. Named roles have already been normalized to plan (needs_plan) or
// coder (has_design); only an explicit non-role prompt remains filterless.
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
async function ensureCardInReview(loom, issueId, after) {
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
