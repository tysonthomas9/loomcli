import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD requires every workflow module to default-export defineWorkflow();
// a bare `export function run` no longer normalizes. Keep the named run export
// (the hand-rolled write_review_dist shim path calls it directly) AND add the
// flue-native default export so the loom driver executor can invoke it too.
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

// github-review-agent: trigger-driven COMMENT-only PR review.
//
// The workflow is fired by a github.pull_request.* trigger. It runs a single
// linear pass — there is no watch loop and no polling cadence of its own:
//
//   1. parse the trigger envelope (repo / pr_number / head_sha / base_ref /
//      draft) from the A1-1 webhook attrs or the raw pull_request payload;
//   2. draft PRs are skipped (completed, output.skipped="draft") — the
//      frozen result statuses carry no "skipped" status, so the skip reason
//      is encoded in the output (the later ready_for_review event triggers
//      the real review, A1-7);
//   3. PRE-FLIGHT liveness: read the PR through the github connector with the
//      expectedHeadSha + open preconditions; a closed/moved PR ends
//      completed, output.skipped="stale_subject" (the supersede policy means
//      a newer push already owns the subject, A1-5);
//   4. fetch the diff via the github compare connector (base...head) — the
//      credential never enters the sandbox and the repo is never checked out;
//   5. enqueue a review TaskRun with a deterministic id and await it; the
//      worker's injected task-runner command (see the host task bridge) runs
//      the LLM and the retry policy lives server-side;
//   6. validate + cap the findings JSON the runner returned;
//   7. RE-CHECK liveness, then post the COMMENT review through the github
//      connector with the expectedHeadSha precondition and a deterministic
//      idempotency call (runId#post); a stale subject here skips the post;
//   8. completed with output.reviewUrl.
//
// Re-entry is deterministic: the task run id, the connector call sequence
// (auto-incremented per action in call order), and the linear control flow
// re-derive the same identities after a restart, so a resumed run never
// double-posts or double-enqueues.
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  const subject = parseReviewSubject(input);
  if (!subject.ok) {
    return loom.failed({
      summary: subject.summary,
      errorClass: subject.errorClass || "invalid_review_input",
    });
  }

  if (subject.draft) {
    return skipped(loom, "draft", "PR " + subject.subjectRef + " is a draft; deferring review to ready_for_review");
  }

  const preflight = await readLivePullRequest(loom, subject);
  if (preflight.stale) {
    return skipped(loom, "stale_subject", "PR " + subject.subjectRef + " is not live at " + subject.headSha + ": " + preflight.reason);
  }
  if (!preflight.ok) {
    return loom.failed({ summary: preflight.summary, errorClass: preflight.errorClass || "preflight_read_failed" });
  }

  const diff = await fetchDiff(loom, subject);
  if (!diff.ok) {
    return loom.failed({ summary: diff.summary, errorClass: diff.errorClass || "diff_fetch_failed" });
  }

  const review = await runReviewTask(loom, subject, diff);
  if (!review.ok) {
    return loom.failed({ summary: review.summary, errorClass: review.errorClass || "review_task_failed" });
  }

  const findings = validateFindings(review.findings);
  if (!findings.ok) {
    return loom.failed({ summary: findings.summary, errorClass: "invalid_review_findings" });
  }

  return await postReview(loom, subject, findings.value);
}

// parseReviewSubject pulls the review subject out of either the A1-1 webhook
// attrs (camelCase convenience fields the trigger envelope carries) or the
// raw GitHub pull_request payload. repo + prNumber + headSha are required;
// baseRef defaults to the PR base and draft is a plain boolean.
function parseReviewSubject(input) {
  const pr = (input && typeof input.pull_request === "object" && input.pull_request) || {};
  const repo = stringValue(input.repo || input.repoFullName || nested(input, "repository", "full_name"));
  const prNumber = numberValue(input.prNumber ?? input.pr_number ?? pr.number, 0);
  const headSha = stringValue(input.headSha || input.head_sha || nested(pr, "head", "sha"));
  const baseRef = stringValue(input.baseRef || input.base_ref || nested(pr, "base", "ref"));
  const draft = booleanValue(input.draft ?? pr.draft);
  if (!repo) {
    return { ok: false, errorClass: "invalid_review_input", summary: "github-review-agent requires repo (owner/name)" };
  }
  if (prNumber <= 0) {
    return { ok: false, errorClass: "invalid_review_input", summary: "github-review-agent requires a positive prNumber" };
  }
  if (!headSha) {
    return { ok: false, errorClass: "invalid_review_input", summary: "github-review-agent requires headSha" };
  }
  const slashAt = repo.indexOf("/");
  if (slashAt <= 0 || slashAt === repo.length - 1) {
    return { ok: false, errorClass: "invalid_review_input", summary: "repo must be owner/name, got " + JSON.stringify(repo) };
  }
  return {
    ok: true,
    owner: repo.slice(0, slashAt),
    repo: repo.slice(slashAt + 1),
    repoFullName: repo,
    prNumber,
    headSha,
    baseRef,
    draft,
    // connectorId names the workspace connector the dispatch egress flows
    // through. The webhook payload carries no connector id, so default to the
    // conventional "github" connector (overridable via input.connectorId);
    // dispatch validation rejects an empty connector id.
    connectorId: stringValue(input.connectorId) || "github",
    subjectRef: repo + "#" + prNumber,
  };
}

// readLivePullRequest is the pre-egress liveness read (steps 3 + the re-check
// before posting share it): the PR must still be open at the expected head
// sha. A connector stale_subject — or a read showing a closed/moved PR —
// returns {stale:true}; the caller ends the run skipped rather than failed.
async function readLivePullRequest(loom, subject) {
  try {
    const result = await loom.connectors.github.readPullRequest({
      connectorId: subject.connectorId,
      resource: "repo:" + subject.repoFullName,
      owner: subject.owner,
      repo: subject.repo,
      number: subject.prNumber,
      expectedHeadSha: subject.headSha,
    });
    const body = (result && result.body) || {};
    if (stringValue(body.state) !== "open") {
      return { stale: true, reason: "pull request state " + JSON.stringify(stringValue(body.state)) };
    }
    if (stringValue(body.headSha) && stringValue(body.headSha) !== subject.headSha) {
      return { stale: true, reason: "head sha moved to " + stringValue(body.headSha) };
    }
    return { ok: true };
  } catch (err) {
    if (isStaleSubjectError(err)) {
      return { stale: true, reason: errorMessage(err) };
    }
    return { ok: false, errorClass: connectorErrorClass(err, "preflight_read_failed"), summary: "preflight read failed for " + subject.subjectRef + ": " + errorMessage(err) };
  }
}

// fetchDiff reads the base...head comparison through the connector. The diff
// rides into the sandbox as task-runner input; no credential and no clone
// ever reach the workflow.
async function fetchDiff(loom, subject) {
  const base = subject.baseRef || subject.subjectRef;
  try {
    const result = await loom.connectors.github.compare({
      connectorId: subject.connectorId,
      resource: "repo:" + subject.repoFullName,
      owner: subject.owner,
      repo: subject.repo,
      base: subject.baseRef || "HEAD",
      head: subject.headSha,
    });
    // The compare connector returns the change summary plus the actual
    // unified diff (body.diff) stitched from files[].patch. The review
    // runner reasons over the raw diff text, so hand it the diff string
    // (falling back to re-stitching the file patches when only files[] is
    // present). A diff-less compare means an empty changeset.
    const body = (result && result.body) || {};
    const diffText = stringValue(body.diff) || stitchDiff(body.files);
    if (!diffText) {
      return { ok: false, errorClass: "empty_diff", summary: "diff compare for " + subject.subjectRef + " produced no changes" };
    }
    return { ok: true, base, text: diffText };
  } catch (err) {
    if (isStaleSubjectError(err)) {
      return { ok: false, errorClass: "stale_subject", summary: "diff compare for " + subject.subjectRef + " is stale: " + errorMessage(err) };
    }
    return { ok: false, errorClass: connectorErrorClass(err, "diff_fetch_failed"), summary: "diff fetch failed for " + subject.subjectRef + ": " + errorMessage(err) };
  }
}

// runReviewTask enqueues the review TaskRun with a deterministic id and awaits
// it. The runner command is injected on the worker (never hardcode a runner
// binary here); the LLM consumes the diff + PR metadata + the review rubric,
// the worker's injected task-runner runs it, and the run returns the
// findings JSON on the terminal run's runtimeMetadata. retry policy
// (maxAttempts/backoff) is applied server-side from the binding overrides.
async function runReviewTask(loom, subject, diff) {
  const taskRunId = deterministicTaskRunId(loom.driverRunId, "review");
  const runner = stringValue(loom.input.runner || "github-review-task-runner");
  const request = {
    taskId: reviewTaskId(subject),
    taskRunId,
    runner,
    input: {
      kind: "github_pr_review",
      repo: subject.repoFullName,
      prNumber: subject.prNumber,
      headSha: subject.headSha,
      baseRef: subject.baseRef,
      diff: diff.text,
      rubric: reviewRubric(),
    },
  };
  try {
    await loom.taskRuns.request(request);
  } catch (err) {
    if (!isConflictError(err)) {
      return { ok: false, errorClass: connectorErrorClass(err, "review_task_request_failed"), summary: "review task request failed: " + errorMessage(err) };
    }
  }
  const run = await loom.taskRuns.await({ taskRunId });
  const status = stringValue(run && run.status);
  if (status !== "completed") {
    return {
      ok: false,
      errorClass: stringValue(run && (run.errorClass || run.error_class)) || "review_task_" + (status || "incomplete"),
      summary: "review task " + taskRunId + " ended " + (status || "without a terminal status"),
    };
  }
  return { ok: true, taskRunId, findings: extractFindings(run) };
}

// validateFindings parses + caps the findings the runner returned. The shape
// is { summary, comments:[{path, line, body}] }; an unparseable or wrongly
// typed payload fails the run with errorClass invalid_review_findings.
function validateFindings(raw) {
  let parsed = raw;
  if (typeof raw === "string") {
    if (stringValue(raw) === "") {
      return { ok: false, summary: "review task returned no findings" };
    }
    try {
      parsed = JSON.parse(raw);
    } catch (err) {
      return { ok: false, summary: "review findings were not valid JSON: " + errorMessage(err) };
    }
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return { ok: false, summary: "review findings must be a JSON object" };
  }
  const rawComments = Array.isArray(parsed.comments) ? parsed.comments : [];
  const comments = [];
  for (const entry of rawComments) {
    if (comments.length >= MAX_REVIEW_COMMENTS) {
      break;
    }
    if (!entry || typeof entry !== "object") {
      continue;
    }
    const path = stringValue(entry.path);
    const body = stringValue(entry.body);
    if (!path || !body) {
      continue;
    }
    const comment = { path, body: capString(body, MAX_COMMENT_BODY) };
    const line = numberValue(entry.line, 0);
    if (line > 0) {
      comment.line = line;
    }
    comments.push(comment);
  }
  return { ok: true, value: { summary: capString(stringValue(parsed.summary), MAX_REVIEW_BODY), comments } };
}

// postReview re-checks liveness, then posts the COMMENT review through the
// connector with the expectedHeadSha precondition. event is always COMMENT —
// v1 never APPROVEs or REQUEST_CHANGESes (no merge-gating authority). The
// connector call sequence is deterministic (one post per run, callSeq=1), so
// a re-entered run re-derives the same idempotency key (runId#post) and the
// server collapses the duplicate.
async function postReview(loom, subject, findings) {
  const recheck = await readLivePullRequest(loom, subject);
  if (recheck.stale) {
    return skipped(loom, "stale_subject", "PR " + subject.subjectRef + " went stale before posting: " + recheck.reason);
  }
  if (!recheck.ok) {
    return loom.failed({ summary: recheck.summary, errorClass: recheck.errorClass || "preflight_read_failed" });
  }
  try {
    const result = await loom.connectors.github.postReview({
      connectorId: subject.connectorId,
      resource: "repo:" + subject.repoFullName,
      owner: subject.owner,
      repo: subject.repo,
      number: subject.prNumber,
      event: "COMMENT",
      body: renderReviewBody(findings, subject),
      comments: findings.comments,
      expectedHeadSha: subject.headSha,
    });
    const body = (result && result.body) || {};
    return loom.completed({
      summary: "Posted COMMENT review on " + subject.subjectRef + " (" + findings.comments.length + " inline finding(s))",
      skipped: "",
      reviewUrl: stringValue(body.htmlUrl || body.html_url || body.url),
      reviewId: stringValue(body.id),
      headSha: subject.headSha,
    });
  } catch (err) {
    if (isStaleSubjectError(err)) {
      return skipped(loom, "stale_subject", "PR " + subject.subjectRef + " went stale during post: " + errorMessage(err));
    }
    return loom.failed({ summary: "review post failed for " + subject.subjectRef + ": " + errorMessage(err), errorClass: connectorErrorClass(err, "review_post_failed") });
  }
}

// renderReviewBody composes the human-facing review summary. The github
// connector's review post carries the body + event but not the per-line
// comments array, so each inline finding is also rendered into the body as a
// `path:line — body` bullet; that way every finding is visible on the posted
// COMMENT review even when the provider does not attach line comments.
function renderReviewBody(findings, subject) {
  const summary = stringValue(findings.summary) || "Automated review of " + subject.subjectRef + " at " + subject.headSha + ".";
  const count = findings.comments.length;
  if (count === 0) {
    return summary + "\n\nNo blocking issues found.";
  }
  const lines = findings.comments.map((c) => {
    const loc = c.line ? c.path + ":" + c.line : c.path;
    return "- **" + loc + "** — " + stringValue(c.body);
  });
  return summary + "\n\n" + count + " inline finding(s):\n" + lines.join("\n");
}

// stitchDiff re-builds a unified-diff string from a compare files[] array when
// the connector did not already provide body.diff. Each file's patch is
// prefixed with the synthesized diff/--- /+++ header lines.
function stitchDiff(files) {
  if (!Array.isArray(files)) {
    return "";
  }
  const parts = [];
  for (const f of files) {
    if (!f || typeof f !== "object") {
      continue;
    }
    const filename = stringValue(f.filename);
    const patch = stringValue(f.patch);
    if (!filename || !patch) {
      continue;
    }
    parts.push("diff --git a/" + filename + " b/" + filename + "\n--- a/" + filename + "\n+++ b/" + filename + "\n" + patch);
  }
  return parts.join("\n");
}

// reviewRubric is the static review instruction set handed to the runner.
function reviewRubric() {
  return [
    "Review the pull request diff for correctness bugs, security issues, and clear regressions.",
    "Return JSON {summary, comments:[{path, line, body}]} only.",
    "Do not approve or request changes; this is a COMMENT-only advisory review.",
  ].join(" ");
}

// extractFindings pulls the findings payload off the terminal task run's
// runtime metadata (the task bridge surfaces runner runtimeMetadata there).
function extractFindings(run) {
  const metadata = (run && (run.runtimeMetadata || run.runtime_metadata)) || {};
  return metadata.review_findings ?? metadata.reviewFindings ?? "";
}

// skipped returns a completed result that records why the review was not
// posted: the frozen statuses have no "skipped" status, so the reason lands
// in output.skipped for the caller (and A1-5/A1-7 vet flows) to branch on.
function skipped(loom, reason, summary) {
  return loom.completed({ summary, skipped: reason });
}

function reviewTaskId(subject) {
  return "review-" + slug(subject.subjectRef);
}

function deterministicTaskRunId(driverRunId, label) {
  return "task-run-" + slug(driverRunId || "run") + "-" + slug(label || "task");
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

function isStaleSubjectError(err) {
  return stringValue(err && err.code) === "stale_subject";
}

function connectorErrorClass(err, fallback) {
  return stringValue(err && err.code) || fallback;
}

function capString(value, max) {
  const s = stringValue(value);
  return s.length > max ? s.slice(0, max) : s;
}

function nested(obj, ...keys) {
  let cur = obj;
  for (const key of keys) {
    if (!cur || typeof cur !== "object") {
      return "";
    }
    cur = cur[key];
  }
  return cur;
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

const MAX_REVIEW_COMMENTS = 50;
const MAX_COMMENT_BODY = 4000;
const MAX_REVIEW_BODY = 16000;
