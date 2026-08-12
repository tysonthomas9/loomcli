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
  const linked = (Array.isArray(cards) ? cards : []).filter((card) => reviewCardMatchesTarget(card, input));

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
      skipped.push({
        issueId,
        pr: subject.slug,
        reason: outcome.reason,
        ...(outcome.taskRunId ? { taskRunId: outcome.taskRunId } : {}),
        ...(outcome.claimRetained === true ? { claimRetained: true } : {}),
      });
      continue;
    }

    reviewed.push({ issueId, pr: subject.slug, cycle: cycles + 1, reviewUrl: outcome.reviewUrl || null });
  }

  return loom.completed({
    summary: "review-loop: reviewed " + reviewed.length + ", skipped " + skipped.length + " (cap " + cap + ")",
    reviewed,
    skipped,
  });
}

export function reviewCardMatchesTarget(card, input = {}) {
  const subject = prSubject(stringValue(card && card.external_ref));
  if (!subject) return false;
  const githubRepo = stringValue(input.githubRepo).toLowerCase();
  if (githubRepo) return subject.repo.toLowerCase() === githubRepo;
  const targetRepo = stringValue(input.targetRepo);
  return !targetRepo || stringValue(card && card.source_repo) === targetRepo;
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

  // 3. Fence the review Work Item before creating the child TaskRun. The
  // request service binds the child to this exact claim generation.
  const claimFailure = await claimReviewOrFail(loom, issueId);
  if (claimFailure) return claimFailure;

  // 4. Codex review task-run (the review engine's runner) -> findings JSON.
  // TaskRun IDs are workspace-global, while an exact request replay is scoped
  // to one parent DriverRun. Include both the parent and the Loom issue so two
  // cards for one PR cannot collide and a later cadence gets a fresh child.
  const taskRunId = deterministicTaskRunId(loom.driverRunId, issueId, cycle);
  let findings = { summary: "", comments: [] };
  try {
    await loom.taskRuns.request({
      // Session/transcript routes are task-scoped and accept Loom issue IDs,
      // not external PR slugs such as "owner/repo#123" (which contain route-
      // invalid '/' and '#' characters). Keep the PR identity in run input.
      taskId: issueId,
      taskRunId,
      runner: "github-review-task-runner",
      // The review-loop host owns the final review -> open handoff. A
      // successful child terminalizes without releasing the exact parent
      // claim; connector egress and the final atomic handoff remain fenced.
      closeTask: false,
      retainWorkItemClaim: true,
      input: { kind: "github_pr_review", repo: subject.repo, prNumber: subject.prNumber, headSha, baseRef, diff, rubric: reviewRubric() },
    });
  } catch (e) {
    if (isAmbiguousOwnershipError(e)) {
      // timeout/unavailable/internal can arrive after the immutable child
      // receipt committed. Releasing here could expose the card beside a live
      // child, so terminal/stale recovery retains authority.
      return {
        ok: false,
        reason: "review_task_dispatch_ambiguous: " + errMsg(e),
        taskRunId,
        claimRetained: true,
      };
    }
    // Exact request replay is resolved by the SDK/service and returns 2xx.
    // Therefore every surfaced 409 is a real conflict, never proof that this
    // deterministic TaskRun exists. Restore review atomically and do not await
    // a phantom run.
    try {
      await loom.tasks.releaseReview({ taskId: issueId });
    } catch (releaseError) {
      return {
        ok: false,
        reason: "review_claim_restore_failed: " + errMsg(e) + "; release: " + errMsg(releaseError),
        taskRunId,
        claimRetained: true,
      };
    }
    return { ok: false, reason: "review_task_dispatch_failed: " + errMsg(e), taskRunId };
  }

  let reviewRun;
  try {
    reviewRun = await loom.taskRuns.await({ taskRunId });
  } catch (e) {
    // request() returned a certified receipt. Keep the claim fenced to that
    // child and let terminal/stale recovery converge it.
    return {
      ok: false,
      reason: "review_task_await_failed: " + errMsg(e),
      taskRunId,
      claimRetained: true,
    };
  }
  if (stringValue(reviewRun && reviewRun.status) !== "completed") {
    return { ok: false, reason: "review_task_" + stringValue(reviewRun && reviewRun.status), taskRunId };
  }
  findings = validateFindings(extractFindings(reviewRun));

  // 5. Persist the cooperative cycle while the exact claim is still live.
  // This keeps a lost connector response from making a successor cadence
  // repeat the same logical cycle.
  try {
    await loom.issues.addLabel({ issueId, label: "review-cycle:" + cycle });
  } catch (e) {
    return {
      ok: false,
      reason: "review_cycle_label_failed: " + errMsg(e),
      taskRunId,
      claimRetained: true,
    };
  }

  // 6. Post the COMMENT review through the connector while the retained claim
  // prevents another cadence from observing this card in Review.
  let reviewUrl = "";
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
    reviewUrl = stringValue((res && res.body && (res.body.htmlUrl || res.body.html_url)) || "");
  } catch (e) {
    if (!isAmbiguousConnectorPostError(e)) {
      // The cycle marker is written before connector egress so an ambiguous
      // response loss can never make a successor cadence duplicate a review
      // that may already exist. A certified non-retryable refusal is different:
      // no review committed, so compensate the marker while this exact claim
      // still excludes successors, then restore the card to Review.
      try {
        await loom.issues.removeLabel({ issueId, label: "review-cycle:" + cycle });
      } catch (restoreLabelError) {
        return {
          ok: false,
          reason: "post_review_failed (grant github.review.post?): " + errMsg(e)
            + "; review_cycle_restore_failed: " + errMsg(restoreLabelError),
          taskRunId,
          claimRetained: true,
        };
      }
      try {
        await loom.tasks.releaseReview({ taskId: issueId });
      } catch (releaseError) {
        return {
          ok: false,
          reason: "post_review_failed (grant github.review.post?): " + errMsg(e)
            + "; review_claim_restore_failed: " + errMsg(releaseError),
          taskRunId,
          claimRetained: true,
        };
      }
      return {
        ok: false,
        reason: "post_review_failed (grant github.review.post?): " + errMsg(e),
        taskRunId,
      };
    }
    return {
      ok: false,
      reason: "post_review_ambiguous (review may have committed): " + errMsg(e),
      taskRunId,
      claimRetained: true,
    };
  }

  // 7. Publish the lifecycle handoff and retire the exact retained claim as
  // one owner-fenced Fleet command. No Review-visible gap exists between the
  // child completion, connector side effect, and this transition.
  try {
    await loom.tasks.handoffReview({
      taskId: issueId,
      taskRunId,
      status: "open",
      reason: "review cycle " + cycle + " posted",
    });
  } catch (e) {
    return {
      ok: false,
      reason: "review_handoff_failed: " + errMsg(e),
      taskRunId,
      claimRetained: true,
    };
  }
  return { ok: true, reviewUrl, taskRunId };
}

async function claimReviewOrFail(loom, issueId) {
  try {
    const claimed = await loom.tasks.claimReview({ taskId: issueId });
    if (!claimed) {
      return { ok: false, reason: "review_claim_not_acquired" };
    }
    const claimedID = stringValue(claimed.id || claimed.ID);
    const claimActionID = stringValue(claimed.claimActionId || claimed.claim_action_id);
    if (claimedID !== issueId || !claimActionID) {
      // A malformed success may follow a committed claim. Do not dispatch or
      // guess at cleanup; recovery owns the potentially committed generation.
      return {
        ok: false,
        reason: "review_claim_receipt_invalid",
        claimRetained: true,
      };
    }
    return null;
  } catch (e) {
    const ambiguous = isAmbiguousOwnershipError(e);
    return {
      ok: false,
      reason: (ambiguous ? "review_claim_ambiguous: " : "review_claim_failed: ") + errMsg(e),
      ...(ambiguous ? { claimRetained: true } : {}),
    };
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
  return meta.review_findings || meta.reviewFindings || meta.findings || meta.review || (run && run.output) || "";
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
    '{ "summary": "<overall assessment>", "comments": [ { "path": "<file>", "line": <optional locator hint>, "body": "<comment>" } ] }',
    "Include line only when it is useful as a locator hint; omit it for file-wide findings.",
    "Loom preserves every finding in the review body and does not risk the whole review on an uncertified inline anchor.",
    "Be concise; flag correctness, security, and obvious bugs. Do not approve or request changes — comment only.",
  ].join("\n");
}

function deterministicTaskRunId(driverRunId, issueId, cycle) {
  return "review-loop-" + slug(driverRunId || "run") + "-" + slug(issueId || "task") + "-c" + Number(cycle || 0);
}

function slug(value) {
  return stringValue(value).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "") || "item";
}

function capStr(s, n) {
  const v = stringValue(s);
  return v.length > n ? v.slice(0, n) : v;
}
function errMsg(e) {
  return stringValue(e && (e.message || e.code)) || String(e);
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
function isAmbiguousConnectorPostError(e) {
  // Provider network/5xx/rate-limit failures use action-specific error codes
  // such as upstream_error or rate_limited, with retryable=true. Treat those
  // conservatively alongside driver transport failures: the provider may have
  // accepted the review before the response was lost.
  return Boolean(e && e.retryable === true) || isAmbiguousOwnershipError(e);
}
function stringValue(v) {
  if (typeof v === "string") return v;
  if (v === undefined || v === null) return "";
  return String(v);
}
