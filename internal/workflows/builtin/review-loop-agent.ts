import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// review-loop-agent: WORKFLOW-PLANE code-review loop (golden scenario S2).
//
// Card-driven poll. Each cadence:
//   1. list cards in status "review" carrying external_ref = a PR url (bug-fix-agent's
//      output — the card IS the entry point, sidestepping the fail-loud server-side
//      PR->card list filter);
//   2. per card, enforce a COOPERATIVE cap via a review-cycle:N label — stop at CAP;
//   3. run a REAL codex review over the PR and post a COMMENT review, then
//   4. move the card to "open" so a task agent reclaims it and works the feedback.
//
// The review is performed INLINE here (read PR + diff via the github connector, run
// the github-review-task-runner for the codex review, post via the connector) rather
// than delegating to github-review-agent via workflows.start: a child run inherits no
// trigger binding (composition.go), so a child's connector actions (esp. review.post)
// are unauthorizable. Doing it here runs every connector action under THIS workflow's
// own binding + grants. Activation is DATA: a cron trigger binding fires it.
const DEFAULT_CAP = 2;
const MAX_REVIEW_COMMENTS = 20;

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
  const connectorId = stringValue(input.connectorId) || "github";

  const cards = await loom.issues.list({ status: "review", limit: 50 });
  const linked = (Array.isArray(cards) ? cards : []).filter((c) => prSubject(stringValue(c && c.external_ref)));

  const reviewed = [];
  const skipped = [];
  for (const card of linked) {
    const issueId = stringValue(card.id || card.ID);
    const subject = prSubject(stringValue(card.external_ref));
    if (!issueId || !subject) continue;

    // Cooperative cap: highest review-cycle:N label already on the card.
    const cycles = reviewCycleCount(card.labels);
    if (cycles >= cap) {
      skipped.push({ issueId, pr: subject.slug, reason: "cap_reached", cycles });
      continue;
    }

    const outcome = await reviewPullRequest(loom, connectorId, subject, cycles + 1, issueId);
    if (!outcome.ok) {
      skipped.push({ issueId, pr: subject.slug, reason: outcome.reason });
      continue;
    }

    // Hand off: bump the review-cycle label + move the card to "open" for a task agent.
    try {
      await loom.issues.addLabel({ issueId, label: "review-cycle:" + (cycles + 1) });
      await loom.issues.update({ issueId, status: "open" });
      reviewed.push({ issueId, pr: subject.slug, cycle: cycles + 1, reviewUrl: outcome.reviewUrl || null });
    } catch (_e) {
      skipped.push({ issueId, pr: subject.slug, reason: "handoff_failed" });
    }
  }

  return loom.completed({
    summary: "review-loop: reviewed " + reviewed.length + ", skipped " + skipped.length + " (cap " + cap + ")",
    reviewed,
    skipped,
  });
}

// reviewPullRequest performs one full review under THIS workflow's binding: read
// the live PR (for headSha/baseRef), fetch the diff, run a codex review task-run,
// and post a COMMENT review. Mirrors github-review-agent's steps.
export async function reviewPullRequest(loom, connectorId, subject, cycle, issueId) {
  const github = loom.connectors[connectorId];
  if (!github) return { ok: false, reason: "no connector " + connectorId };
  const resource = "repo:" + subject.repo;

  // 1. Live PR read -> headSha + baseRef.
  let headSha = "";
  let baseRef = "";
  try {
    const pr = await github.readPullRequest({ connectorId, resource, owner: subject.owner, repo: subject.name, number: subject.prNumber });
    const body = (pr && pr.body) || {};
    if (stringValue(body.state) && stringValue(body.state) !== "open") return { ok: false, reason: "pr_not_open" };
    headSha = stringValue(body.headSha || body.head_sha || (body.head && body.head.sha));
    baseRef = stringValue(body.baseRef || body.base_ref || (body.base && body.base.ref)) || "HEAD";
  } catch (e) {
    return { ok: false, reason: "read_pr_failed (connector/grant?): " + errMsg(e) };
  }
  if (!headSha) return { ok: false, reason: "no_head_sha" };

  // 2. Diff (base...head).
  let diff = "";
  try {
    const cmp = await github.compare({ connectorId, resource, owner: subject.owner, repo: subject.name, base: baseRef, head: headSha });
    const body = (cmp && cmp.body) || {};
    diff = stringValue(body.diff);
  } catch (e) {
    return { ok: false, reason: "compare_failed: " + errMsg(e) };
  }
  if (!diff) return { ok: false, reason: "empty_diff" };

  // 3. Codex review task-run (the review engine's runner) -> findings JSON.
  const taskRunId = "review-" + subject.repo.replace(/\//g, "_") + "-" + subject.prNumber + "-c" + cycle;
  let findings = { summary: "", comments: [] };
  try {
    await loom.taskRuns.request({
      // Session/transcript routes are task-scoped and accept Loom issue IDs,
      // not external PR slugs such as "owner/repo#123" (which contain route-
      // invalid '/' and '#' characters). Keep the PR identity in run input.
      taskId: issueId,
      taskRunId,
      runner: "github-review-task-runner",
      input: { kind: "github_pr_review", repo: subject.repo, prNumber: subject.prNumber, headSha, baseRef, diff, rubric: reviewRubric() },
    });
    const run = await loom.taskRuns.await({ taskRunId });
    if (stringValue(run && run.status) !== "completed") return { ok: false, reason: "review_task_" + stringValue(run && run.status) };
    findings = validateFindings(extractFindings(run));
  } catch (e) {
    return { ok: false, reason: "review_task_failed: " + errMsg(e) };
  }

  // 4. Post the COMMENT review through the connector.
  try {
    const res = await github.postReview({
      connectorId,
      resource,
      owner: subject.owner,
      repo: subject.name,
      number: subject.prNumber,
      expectedHeadSha: headSha,
      event: "COMMENT",
      body: findings.summary || "Automated review (cycle " + cycle + ").",
      comments: findings.comments,
    });
    const reviewUrl = stringValue((res && res.body && (res.body.htmlUrl || res.body.html_url)) || "");
    return { ok: true, reviewUrl };
  } catch (e) {
    return { ok: false, reason: "post_review_failed (grant github.review.post?): " + errMsg(e) };
  }
}

// Parse a PR external_ref into {repo, owner, name, prNumber, slug}.
function prSubject(externalRef) {
  if (!externalRef) return null;
  let m = externalRef.match(/github\.com\/([^/]+)\/([^/]+)\/pull\/(\d+)/);
  if (m) return { owner: m[1], name: m[2], repo: m[1] + "/" + m[2], prNumber: Number(m[3]), slug: m[1] + "/" + m[2] + "#" + m[3] };
  m = externalRef.match(/^([^/]+)\/([^/#]+)#(\d+)$/);
  if (m) return { owner: m[1], name: m[2], repo: m[1] + "/" + m[2], prNumber: Number(m[3]), slug: m[1] + "/" + m[2] + "#" + m[3] };
  return null;
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

// The review task-run returns findings JSON on the terminal run's runtimeMetadata.
function extractFindings(run) {
  const meta = (run && (run.runtime_metadata || run.runtimeMetadata)) || {};
  return meta.findings || meta.review || (run && run.output) || "";
}

function validateFindings(raw) {
  let parsed = raw;
  if (typeof raw === "string") {
    if (stringValue(raw) === "") return { summary: "", comments: [] };
    try { parsed = JSON.parse(raw); } catch { return { summary: capStr(raw, 4000), comments: [] }; }
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return { summary: "", comments: [] };
  const comments = [];
  for (const e of Array.isArray(parsed.comments) ? parsed.comments : []) {
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
  return { summary: capStr(stringValue(parsed.summary), 4000), comments };
}

function reviewRubric() {
  return [
    "Review this pull request diff. Return ONLY a JSON object:",
    '{ "summary": "<overall assessment>", "comments": [ { "path": "<file>", "line": <n>, "body": "<comment>" } ] }',
    "Be concise; flag correctness, security, and obvious bugs. Do not approve or request changes — comment only.",
  ].join("\n");
}

function capStr(s, n) {
  const v = stringValue(s);
  return v.length > n ? v.slice(0, n) : v;
}
function errMsg(e) {
  return stringValue(e && (e.message || e.code)) || String(e);
}
function stringValue(v) {
  if (typeof v === "string") return v;
  if (v === undefined || v === null) return "";
  return String(v);
}
