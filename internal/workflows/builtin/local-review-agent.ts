import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// local-review-agent: WORKFLOW-PLANE review loop for local-branch deliveries.
//
// This is the no-GitHub sibling of review-loop-agent. The card is the source of
// truth: prompt-agent stamps external_ref="local-branch:<branch>@<sha>" after a
// local-task-runner push to the workspace filesystem origin. We acquire the diff
// through the task-diff driver op, then dispatch the existing
// github-review-task-runner with that diff AS DATA. No connector or GitHub API
// action participates in this loop.
const DEFAULT_CAP = 2;
const MAX_REVIEW_COMMENTS = 20;
const CAP_NOTED_LABEL = "review-cycle-cap-noted";

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

export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });
  const cap = Number(input.cap) > 0 ? Number(input.cap) : DEFAULT_CAP;
  const targetRepo = stringValue(input.targetRepo);

  const cards = await loom.issues.list({ status: "review", limit: numberValue(input.limit, 50) });
  const linked = (Array.isArray(cards) ? cards : []).filter((card) =>
    localBranchSubject(stringValue(card && card.external_ref))
      && (!targetRepo || stringValue(card && card.source_repo) === targetRepo));

  const reviewed = [];
  const approved = [];
  const skipped = [];
  for (const card of linked) {
    const issueId = stringValue(card.id || card.ID);
    const externalRef = stringValue(card.external_ref);
    const subject = localBranchSubject(externalRef);
    if (!issueId || !subject) continue;

    // Cooperative cap: highest review-cycle:N label already on the card. This
    // function is copied verbatim from review-loop-agent so both lanes honor the
    // same label semantics and never loop forever.
    const cycles = reviewCycleCount(card.labels);
    if (cycles >= cap) {
      const noted = labelList(card.labels).includes(CAP_NOTED_LABEL);
      if (!noted) {
        const noteFailure = await noteReviewCapOrFail(loom, issueId, cap, cycles);
        if (isWorkflowResult(noteFailure)) {
          skipped.push(failureSkip(issueId, subject.branch, "cap_note_failed", noteFailure));
          continue;
        }
      }
      skipped.push({ issueId, branch: subject.branch, reason: "cap_reached", cycles });
      continue;
    }

    const cycle = cycles + 1;
    // Per-card failures SKIP, never abort the sweep: a single un-diffable card
    // (too-large diff, pruned branch, transient dispatch failure) must not
    // starve every sibling review card behind it — one poisoned card would
    // otherwise block the whole lane on every sweep, silently. Mirror the cap
    // path: record the reason, move on. Definitive pre-child failures restore
    // review atomically for a later sweep; ambiguous ownership stays fenced
    // until terminal/stale recovery makes a retry safe.
    const diffResult = await acquireDiffOrFail(loom, issueId, externalRef);
    if (isWorkflowResult(diffResult)) {
      skipped.push({ issueId, branch: subject.branch, reason: "diff_failed", errorClass: stringValue(diffResult.errorClass), detail: stringValue(diffResult.summary) });
      continue;
    }
    const claimed = await claimReviewOrFail(loom, issueId);
    if (isWorkflowResult(claimed)) {
      skipped.push(failureSkip(issueId, subject.branch, "claim_failed", claimed));
      continue;
    }
    const findings = await runReviewOrFail(loom, issueId, subject, diffResult, cycle, numberValue(input.timeoutMs, 10 * 60 * 1000));
    if (isWorkflowResult(findings)) {
      skipped.push(failureSkip(issueId, subject.branch, "review_failed", findings));
      continue;
    }

    const handoffFailure = await persistReviewOutcomeOrFail(loom, issueId, subject, diffResult, findings, cycle);
    if (isWorkflowResult(handoffFailure)) {
      skipped.push(failureSkip(issueId, subject.branch, "handoff_failed", handoffFailure));
      continue;
    }

    if (findings.comments.length > 0) {
      reviewed.push({ issueId, branch: subject.branch, headSha: subject.sha, cycle, findings: findings.comments.length });
      continue;
    }

    approved.push({ issueId, branch: subject.branch, headSha: subject.sha, cycle });
  }

  return {
    ...loom.completed({
      summary: "local-review: reviewed " + reviewed.length + ", approved " + approved.length + ", skipped " + skipped.length + " (cap " + cap + ")",
    }),
    reviewed,
    approved,
    skipped,
  };
}

function failureSkip(issueId, branch, reason, failure) {
  const skipped = {
    issueId,
    branch,
    reason,
    errorClass: stringValue(failure && failure.errorClass),
    detail: stringValue(failure && failure.summary),
  };
  const taskRunId = stringValue(failure && failure.taskRunId);
  if (taskRunId) skipped.taskRunId = taskRunId;
  const mutation = stringValue(failure && failure.mutation);
  if (mutation) skipped.mutation = mutation;
  const cycles = Number(failure && failure.cycles);
  if (Number.isFinite(cycles)) skipped.cycles = cycles;
  if (failure && failure.claimRetained === true) skipped.claimRetained = true;
  return skipped;
}

async function noteReviewCapOrFail(loom, issueId, cap, cycles) {
  try {
    await loom.issues.comment({
      issueId,
      body: "Local review reached the configured cap (" + cap + "). Leaving this card in review for human attention.",
    });
  } catch (err) {
    return mutationFail("cap_comment", issueId, err, { cycles });
  }
  try {
    await loom.issues.addLabel({ issueId, label: CAP_NOTED_LABEL });
  } catch (err) {
    return mutationFail("cap_label", issueId, err, { cycles });
  }
  return null;
}

async function persistReviewOutcomeOrFail(loom, issueId, subject, diffResult, findings, cycle) {
  const blocking = findings.comments.length > 0;
  try {
    await loom.issues.comment({
      issueId,
      body: blocking
        ? blockingReviewComment(findings, cycle, subject, diffResult)
        : approvalComment(findings, cycle, subject, diffResult),
    });
  } catch (err) {
    return mutationFail("result_comment", issueId, err, { cycle, taskRunId: findings.taskRunId, claimRetained: true });
  }

  if (blocking) {
    // Commit the cycle marker before lifecycle publication while the retained
    // exact claim still excludes every successor cadence.
    try {
      await loom.issues.addLabel({ issueId, label: "review-cycle:" + cycle });
    } catch (err) {
      return mutationFail("result_label", issueId, err, { cycle, taskRunId: findings.taskRunId, claimRetained: true });
    }
  }
  try {
    await loom.tasks.handoffReview({
      taskId: issueId,
      taskRunId: findings.taskRunId,
      status: blocking ? "open" : "closed",
      reason: blocking ? "local review found blocking changes" : "local review approved",
    });
  } catch (err) {
    return mutationFail("result_handoff", issueId, err, { cycle, taskRunId: findings.taskRunId, claimRetained: true });
  }
  return null;
}

function mutationFail(mutation, issueId, err, extra) {
  return fail(
    "local_review_" + mutation + "_" + errorCode(err),
    "local-review: failed " + mutation.replace(/_/g, " ") + " mutation for " + issueId + ": " + errorMessage(err),
    { issueId, mutation, ...(extra || {}) },
  );
}

async function acquireDiffOrFail(loom, issueId, externalRef) {
  try {
    const result = await loom.tasks.diff({ taskId: issueId });
    const diff = stringValue(result && result.diff);
    if (!diff.trim()) {
      return fail("local_review_diff_empty", "local-review: task-diff returned an empty diff for " + issueId, { issueId, externalRef });
    }
    return result;
  } catch (err) {
    if (isWorkflowResult(err)) return err;
    return fail(
      "local_review_diff_" + errorCode(err),
      "local-review: failed to acquire diff for " + issueId + ": " + errorMessage(err),
      { issueId, externalRef },
    );
  }
}

async function claimReviewOrFail(loom, issueId) {
  try {
    const claimed = await loom.tasks.claimReview({ taskId: issueId });
    if (!claimed) {
      return fail(
        "local_review_claim_not_acquired",
        "local-review: review claim was not acquired for " + issueId,
        { issueId },
      );
    }
    const claimedID = stringValue(claimed.id || claimed.ID);
    const claimActionID = stringValue(claimed.claimActionId || claimed.claim_action_id);
    if (claimedID !== issueId || !claimActionID) {
      // A malformed/misdirected success may follow a committed claim. Do not
      // guess which Work Item is owned and do not dispatch or release another
      // card; parent-run recovery is the safe authority for this impossible
      // response shape.
      return fail(
        "local_review_claim_receipt_invalid",
        "local-review: review claim for " + issueId + " returned an invalid ownership receipt"
          + " (ownership retained for recovery)",
        { issueId, claimedIssueId: claimedID, claimRetained: true },
      );
    }
    return claimed;
  } catch (err) {
    if (isWorkflowResult(err)) return err;
    const ambiguous = isAmbiguousOwnershipError(err);
    return fail(
      ambiguous ? "local_review_claim_ambiguous" : "local_review_claim_" + errorCode(err),
      "local-review: failed to claim review card " + issueId + ": " + errorMessage(err)
        + (ambiguous ? " (ownership may have committed; retained for recovery)" : ""),
      { issueId, claimRetained: ambiguous },
    );
  }
}

async function runReviewOrFail(loom, issueId, subject, diffResult, cycle, timeoutMs) {
  // TaskRun identity is deterministic for replay inside this DriverRun, while
  // the parent component prevents a later cadence from colliding with the
  // workspace-global TaskRun created by an earlier review attempt.
  const taskRunId = "local-review-" + safeID(loom.driverRunId || "run") + "-" + safeID(issueId) + "-c" + cycle;
  try {
    await loom.taskRuns.request({
      taskId: issueId,
      taskRunId,
      runner: "github-review-task-runner",
      closeTask: false,
      retainWorkItemClaim: true,
      input: {
        kind: "local_branch_review",
        taskId: issueId,
        branch: subject.branch,
        headSha: subject.sha,
        resolvedHead: stringValue(diffResult && diffResult.resolvedHead),
        baseRef: stringValue(diffResult && diffResult.baseRef),
        baseSha: stringValue(diffResult && diffResult.baseSha),
        diff: stringValue(diffResult && diffResult.diff),
        rubric: localReviewRubric(),
      },
    });
  } catch (err) {
    if (isAmbiguousOwnershipError(err)) {
      // timeout/unavailable/internal may follow a committed immutable TaskRun
      // receipt. Releasing would expose a ready review card beside a live
      // child, so retain the exact review claim for terminal/stale recovery.
      return fail(
        "local_review_task_dispatch_ambiguous",
        "local-review: review task request for " + issueId + " had an ambiguous result: "
          + errorMessage(err) + " (claim retained for recovery)",
        { issueId, taskRunId, claimRetained: true },
      );
    }
    // Exact durable request replay is resolved inside the SDK/service and
    // returns the certified child as a successful response. Any surfaced 409
    // is therefore a real lineage/envelope conflict, not evidence that this
    // deterministic ID exists. Restore the exact review claim and never await
    // a phantom TaskRun.
    const restored = await restoreReviewClaimOrFail(loom, issueId, taskRunId, err);
    if (restored) return restored;
    return fail(
      "local_review_task_dispatch_failed",
      "local-review: failed to dispatch review task for " + issueId + ": " + errorMessage(err),
      { issueId, taskRunId },
    );
  }

  let runResult;
  try {
    runResult = await loom.taskRuns.await({ taskRunId, timeoutMs });
  } catch (err) {
    // request() returned a certified receipt, so a child exists even when its
    // read/await path is temporarily unavailable. Keep the review claim fenced
    // to that child; terminal or stale recovery owns the eventual release.
    return fail(
      "local_review_task_await_failed",
      "local-review: failed awaiting review task " + taskRunId + ": " + errorMessage(err)
        + " (claim retained for child recovery)",
      { issueId, taskRunId, claimRetained: true },
    );
  }
  if (stringValue(runResult && runResult.status) !== "completed") {
    return fail("local_review_task_" + (stringValue(runResult && runResult.status) || "unknown"), "local-review: review task " + taskRunId + " ended " + stringValue(runResult && runResult.status), { issueId, taskRunId });
  }
  const parsed = parseFindings(extractFindings(runResult));
  if (!parsed.ok) {
    return fail(
      "local_review_findings_invalid",
      "local-review: review task " + taskRunId + " did not return valid findings JSON (claim retained for parent recovery)",
      { issueId, taskRunId, claimRetained: true },
    );
  }
  return { ...validateFindings(parsed.value), taskRunId };
}

async function restoreReviewClaimOrFail(loom, issueId, taskRunId, originalError) {
  try {
    await loom.tasks.releaseReview({ taskId: issueId });
    return null;
  } catch (releaseError) {
    return fail(
      "local_review_claim_restore_failed",
      "local-review: failed to dispatch review task for " + issueId + " (" + errorMessage(originalError)
        + ") and atomic review release also failed: " + errorMessage(releaseError),
      { issueId, taskRunId, claimRetained: true },
    );
  }
}

function fail(errorClass, summary, extra) {
  return {
    status: "failed",
    summary,
    errorClass,
    ...(extra || {}),
  };
}

function isWorkflowResult(value) {
  return value && typeof value === "object" && stringValue(value.status) === "failed" && stringValue(value.errorClass);
}

function localBranchSubject(externalRef) {
  if (!externalRef || !externalRef.startsWith("local-branch:")) return null;
  const body = externalRef.slice("local-branch:".length);
  const at = body.lastIndexOf("@");
  if (at <= 0 || at === body.length - 1) return null;
  return { branch: body.slice(0, at), sha: body.slice(at + 1), slug: body };
}

function reviewCycleCount(labels) {
  if (!Array.isArray(labels)) return 0;
  let max = 0;
  for (const l of labels) {
    const m = stringValue(l).match(/^review-cycle:(\d+)$/);
    if (m) max = Math.max(max, Number(m[1]));
  }
  return max;
}

function extractFindings(run) {
  const meta = (run && (run.runtime_metadata || run.runtimeMetadata)) || {};
  return meta.review_findings || meta.reviewFindings || meta.findings || meta.review || (run && run.output) || "";
}

function parseFindings(raw) {
  if (typeof raw !== "string") {
    return raw && typeof raw === "object" && !Array.isArray(raw) ? { ok: true, value: raw } : { ok: false, value: null };
  }
  const text = raw.trim();
  if (!text) return { ok: false, value: null };
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch {
    const start = text.indexOf("{");
    const end = text.lastIndexOf("}");
    if (start >= 0 && end > start) {
      try {
        return { ok: true, value: JSON.parse(text.slice(start, end + 1)) };
      } catch {
        return { ok: false, value: null };
      }
    }
    return { ok: false, value: null };
  }
}

function validateFindings(raw) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return { summary: "", comments: [] };
  const comments = [];
  for (const e of Array.isArray(raw.comments) ? raw.comments : []) {
    if (comments.length >= MAX_REVIEW_COMMENTS) break;
    if (!e || typeof e !== "object") continue;
    const path = stringValue(e.path);
    const body = stringValue(e.body);
    if (!path || !body) continue;
    const c = { path, body: capStr(body, 2000) };
    const line = Number(e.line);
    if (line > 0) c.line = line;
    comments.push(c);
  }
  return { summary: capStr(stringValue(raw.summary), 4000), comments };
}

function blockingReviewComment(findings, cycle, subject, diffResult) {
  const lines = [
    "Local review cycle " + cycle + " found blocking findings for `" + subject.branch + "` @ " + shortSHA(subject.sha) + ".",
  ];
  if (findings.summary) lines.push("", findings.summary);
  lines.push("", "Findings:");
  for (const c of findings.comments) {
    lines.push("- " + c.path + (c.line ? ":" + c.line : "") + " - " + c.body);
  }
  lines.push("", "Base: " + stringValue(diffResult && diffResult.baseRef) + " (" + shortSHA(stringValue(diffResult && diffResult.baseSha)) + ")");
  return capStr(lines.join("\n"), 12000);
}

function approvalComment(findings, cycle, subject, diffResult) {
  const lines = [
    "Local review cycle " + cycle + " approved `" + subject.branch + "` @ " + shortSHA(subject.sha) + ".",
  ];
  if (findings.summary) lines.push("", findings.summary);
  lines.push("", "Base: " + stringValue(diffResult && diffResult.baseRef) + " (" + shortSHA(stringValue(diffResult && diffResult.baseSha)) + ")");
  return capStr(lines.join("\n"), 8000);
}

function localReviewRubric() {
  return [
    "Review this local branch diff. Return ONLY a JSON object:",
    '{ "summary": "<overall assessment>", "comments": [ { "path": "<file>", "line": <n>, "body": "<blocking finding>" } ] }',
    "Only include comments for findings that require code changes before the card can close.",
    "Focus on correctness, regressions, security, test gaps, and mismatches with the issue intent.",
    "Use an empty comments array when there are no blocking findings.",
  ].join("\n");
}

function labelList(labels) {
  if (!Array.isArray(labels)) return [];
  return labels.map((l) => stringValue(l));
}

function isAmbiguousOwnershipError(e) {
  switch (stringValue(e && e.code)) {
    case "timeout":
    case "unavailable":
    case "internal":
      return true;
    default:
      return false;
  }
}

function errorCode(err) {
  const code = stringValue(err && err.code);
  return code || "internal";
}

function errorMessage(err) {
  return stringValue(err && (err.message || err.code)) || String(err);
}

function safeID(value) {
  return (stringValue(value).replace(/[^A-Za-z0-9_.-]/g, "-").replace(/^-+|-+$/g, "") || "task").slice(0, 80);
}

function shortSHA(sha) {
  const value = stringValue(sha);
  return value.length > 12 ? value.slice(0, 12) : value;
}

function capStr(s, n) {
  const v = stringValue(s);
  return v.length > n ? v.slice(0, n) : v;
}

function numberValue(v, fallback) {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? n : fallback;
}

function stringValue(v) {
  if (typeof v === "string") return v;
  if (v === undefined || v === null) return "";
  return String(v);
}
