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

  const cards = await loom.issues.list({ status: "review", limit: numberValue(input.limit, 50) });
  const linked = (Array.isArray(cards) ? cards : []).filter((c) => localBranchSubject(stringValue(c && c.external_ref)));

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
        await loom.issues.comment({
          issueId,
          body: "Local review reached the configured cap (" + cap + "). Leaving this card in review for human attention.",
        });
        await loom.issues.addLabel({ issueId, label: CAP_NOTED_LABEL });
      }
      skipped.push({ issueId, branch: subject.branch, reason: "cap_reached", cycles });
      continue;
    }

    const cycle = cycles + 1;
    // Per-card failures SKIP, never abort the sweep: a single un-diffable card
    // (too-large diff, pruned branch, transient dispatch failure) must not
    // starve every sibling review card behind it — one poisoned card would
    // otherwise block the whole lane on every sweep, silently. Mirror the cap
    // path: record the reason, move on; the next sweep retries transient
    // classes naturally.
    const diffResult = await acquireDiffOrFail(loom, issueId, externalRef);
    if (isWorkflowResult(diffResult)) {
      skipped.push({ issueId, branch: subject.branch, reason: "diff_failed", errorClass: stringValue(diffResult.errorClass), detail: stringValue(diffResult.summary) });
      continue;
    }
    const findings = await runReviewOrFail(loom, issueId, subject, diffResult, cycle, numberValue(input.timeoutMs, 10 * 60 * 1000));
    if (isWorkflowResult(findings)) {
      skipped.push({ issueId, branch: subject.branch, reason: "review_failed", errorClass: stringValue(findings.errorClass), detail: stringValue(findings.summary) });
      continue;
    }

    if (findings.comments.length > 0) {
      await loom.issues.comment({ issueId, body: blockingReviewComment(findings, cycle, subject, diffResult) });
      await loom.issues.addLabel({ issueId, label: "review-cycle:" + cycle });
      await loom.issues.update({ issueId, status: "open" });
      reviewed.push({ issueId, branch: subject.branch, headSha: subject.sha, cycle, findings: findings.comments.length });
      continue;
    }

    await loom.issues.comment({ issueId, body: approvalComment(findings, cycle, subject, diffResult) });
    await loom.issues.update({ issueId, status: "closed" });
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

async function runReviewOrFail(loom, issueId, subject, diffResult, cycle, timeoutMs) {
  const taskRunId = "local-review-" + safeID(issueId) + "-c" + cycle;
  try {
    await loom.taskRuns.request({
      taskId: issueId,
      taskRunId,
      runner: "github-review-task-runner",
      closeTask: false,
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
    if (!isConflictError(err)) {
      return fail("local_review_task_dispatch_failed", "local-review: failed to dispatch review task for " + issueId + ": " + errorMessage(err), { issueId, taskRunId });
    }
    // Deterministic taskRunId: a resumed workflow may re-issue the same request.
    // The task-run exists, so continue to await it rather than double-enqueue.
  }

  let runResult;
  try {
    runResult = await loom.taskRuns.await({ taskRunId, timeoutMs });
  } catch (err) {
    return fail("local_review_task_await_failed", "local-review: failed awaiting review task " + taskRunId + ": " + errorMessage(err), { issueId, taskRunId });
  }
  if (stringValue(runResult && runResult.status) !== "completed") {
    return fail("local_review_task_" + (stringValue(runResult && runResult.status) || "unknown"), "local-review: review task " + taskRunId + " ended " + stringValue(runResult && runResult.status), { issueId, taskRunId });
  }
  const parsed = parseFindings(extractFindings(runResult));
  if (!parsed.ok) {
    return fail("local_review_findings_invalid", "local-review: review task " + taskRunId + " did not return valid findings JSON", { issueId, taskRunId });
  }
  return validateFindings(parsed.value);
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

function isConflictError(e) {
  return e && (e.code === "conflict" || String(e.message || "").includes("conflict"));
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
