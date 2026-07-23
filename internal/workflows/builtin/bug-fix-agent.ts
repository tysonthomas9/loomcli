import { createLoomDriverClient } from '@loom/sdk/driver';
import { defineAgent, defineWorkflow } from '@flue/runtime';

// Flue HEAD (durable-streams) requires every workflow module to default-export a
// defineWorkflow() definition; a bare `export function run` no longer normalizes.
// bug-fix-agent orchestrates via the loom driver SDK (it is not itself an LLM
// agent — codex runs inside the local-task-runner it dispatches), so the bound
// agent is a credential-free stub (model: false). The invocation payload arrives
// via env: the driver launcher sets LOOM_FLUE_INVOKE_PAYLOAD (sandbox/launcher.go).
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

// Flue HEAD validates the return value with a strict JSON check (rejecting
// undefined/function/symbol/bigint); round-trip through JSON so optional result
// fields left undefined never throw.
function toJsonResult(value) {
  return value === undefined ? null : JSON.parse(JSON.stringify(value));
}

// bug-fix-agent: WORKFLOW-PLANE bug-fix (golden scenario S1).
//
// A single linear pass over ONE ready bug ticket:
//   1. claim a ready bug via claim-ready — fleet-db's ready queue is NOT
//      design-gated, so a designless bug is claimable DIRECTLY (no plan/design
//      phase, which is the whole point of S1);
//   2. run codex with a custom fix prompt through a task-run whose runner is the
//      local-task-runner, delivered as a PR (openPullRequest). The prompt and PR
//      metadata travel as task-run Input (the P0-2 runner Input port) so the
//      brain stays custom without forking the runner;
//   3. stamp external_ref = the opened PR onto the card (P0-3 write path), so the
//      code-review loop can later resolve the card from the PR.
//
// Activation is DATA: bind a cron (or internal.issue.created) trigger to this
// workflow. The workflow itself is registered as data (a workflow version), so a
// sibling agent is a new workflow + binding — no Go change. The loom.issue.*
// SDK surface it uses is generated from sdk/op-spec.mjs.
export async function run(ctx) {
  const input = ctx.payload || {};
  const loom = createLoomDriverClient({ input });

  const actor = stringValue(input.actor) || "bug-fix-agent";

  // 1. Claim a ready bug ticket, then fetch the canonical card. claim-ready
  //    returns a flat ClaimedTask (camelCase, no description); issues.get returns
  //    the full IssueData (snake_case). Read field values against that ONE
  //    guaranteed shape rather than guessing casings across two contracts.
  //
  //    type: "bug" narrows the ready queue server-side so we only ever claim a
  //    bug (the BELT). The post-claim issue_type guard below stays as the
  //    SUSPENDERS — and now RELEASES the lease on a non-bug instead of parking
  //    an arbitrary task under this agent's claim until TTL.
  const targetRepo = stringValue(input.targetRepo);
  const claimed = await loom.tasks.claimReady({
    actor,
    limit: 1,
    type: "bug",
    ...(targetRepo ? { sourceRepo: targetRepo } : {}),
  });
  const issueId = claimed && (claimed.id || claimed.ID);
  if (!issueId) {
    return loom.completed({ summary: "bug-fix: no ready bug to claim", claimed: false });
  }
  const card = (await loom.issues.get({ issueId })) || {};

  const issueType = stringValue(card.issue_type);
  if (issueType && issueType !== "bug") {
    // Release the claim we took on a task that is not ours to work — parking it
    // under our lease would starve its real owner until the lock TTL expires.
    // Best-effort: a release failure falls back to TTL recovery and must not
    // mask the skip outcome.
    try {
      await loom.tasks.release({ taskId: issueId });
    } catch (_releaseErr) {
      // fall back to TTL expiry
    }
    return loom.completed({
      summary: "bug-fix: claimed " + issueId + " is type '" + issueType + "', not a bug",
      issueId,
      skipped: true,
      released: true,
    });
  }

  // 2. Run codex with a custom fix prompt, delivered as a PR. The deterministic
  //    taskRunId is scoped to THIS driver run: the same bug may legitimately be
  //    retried by a later workflow run, while a resumed copy of this run must
  //    still replay the same child instead of double-enqueuing.
  const taskRunId = "bugfix-" + (stringValue(loom.driverRunId) || "run") + "-" + issueId;
  // Prefer an explicit githubRepo input (the PR target) over card.source_repo:
  // in local-mode fleet-db forces source_repo to the mapped workspace repo name
  // (e.g. "source-repo"), which is not a valid owner/repo PR slug. In a real
  // fleet no githubRepo input is passed, so card.source_repo (the slug) wins.
  const repo = stringValue(input.githubRepo || card.source_repo);
  // openPullRequest defaults to true (deliver as a PR); a caller can pass
  // openPullRequest=false (e.g. a dry run) to get a patch-back diff with no PR.
  const openPullRequest = input.openPullRequest === undefined ? true : booleanValue(input.openPullRequest);
  await loom.taskRuns.request({
    taskId: issueId,
    taskRunId,
    runner: "local-task-runner",
    input: {
      taskPrompt: buildFixPrompt(card, issueId), // P0-2: read by the runner as data
      openPullRequest, // deliver as a PR (reuses deliverPullRequest) unless the caller opts out
      githubRepo: repo,
      prTitle: "fix: " + stringValue(card.title || issueId),
      prBody: "Automated bug fix by bug-fix-agent for " + issueId + ".",
    },
  });
  const result = await loom.taskRuns.await({ taskRunId });
  const status = stringValue(result && result.status) || "unknown";
  if (status !== "completed") {
    return loom.needsReview({
      summary: "bug-fix: task-run " + taskRunId + " for " + issueId + " ended " + status
        + (result && result.error_message ? " - " + stringValue(result.error_message) : ""),
      errorClass: stringValue(result && (result.error_class || result.errorClass)) || "bug_fix_task_" + status,
      taskRunId,
    });
  }

  // 3. Move the card to "review" + stamp external_ref = PR url (P0-3), so the
  //    code-review loop can link back to the PR. The daemon task worker auto-CLOSES
  //    the card on task success (task_worker.go CloseTaskOnSuccess is hardcoded), so
  //    it is terminal here. Reopen it first — update({status:"open"}) routes
  //    closed -> POST /reopen, which skips fleet-db's terminal guard — THEN set
  //    review + external_ref on the now-modifiable card.
  //
  //    The PR and its review handoff are one workflow outcome. Never report the
  //    workflow completed when the PR exists but the card is still closed/open
  //    or unlinked: that hides the card from review-loop forever.
  const prUrl = extractPrUrl(result);
  if (!prUrl && openPullRequest) {
    return loom.needsReview({
      summary: "bug-fix: task-run " + taskRunId + " completed for " + issueId
        + " but PR delivery returned no github_pr_url; review handoff is incomplete",
      errorClass: "bug_fix_pr_link_missing",
      issueId,
      taskRunId,
      prUrl: null,
      handoffStep: "pr-link",
      reopenAcknowledged: false,
    });
  }
  if (prUrl) {
    try {
      await loom.issues.update({ issueId, status: "open" });
    } catch (reopenErr) {
      return loom.needsReview({
        summary: "bug-fix: " + issueId + " delivered PR " + prUrl
          + " but the closed card could not be reopened for review: " + errorMessage(reopenErr),
        errorClass: "bug_fix_review_reopen_failed",
        issueId,
        taskRunId,
        prUrl,
        handoffStep: "reopen",
        reopenAcknowledged: false,
      });
    }
    try {
      await loom.issues.update({ issueId, status: "review", externalRef: prUrl });
    } catch (stampErr) {
      return loom.needsReview({
        summary: "bug-fix: " + issueId + " delivered PR " + prUrl
          + " and reopened the card, but review status/linkage failed: " + errorMessage(stampErr),
        errorClass: "bug_fix_review_stamp_failed",
        issueId,
        taskRunId,
        prUrl,
        handoffStep: "review-link",
        reopenAcknowledged: true,
      });
    }
  }

  return loom.completed({
    summary: "bug-fix: " + issueId + (prUrl ? " -> PR " + prUrl : " -> delivered (no PR url)"),
    issueId,
    prUrl: prUrl || null,
  });
}

// The PR url lands in the task-run's runtime_metadata.github_pr_url. taskRuns.await
// returns the DriverTaskRun (domain/platform.go:530); the host bridge emits both
// runtime_metadata and runtimeMetadata (task_bridge.go), so probe both containers.
function extractPrUrl(result) {
  if (!result || typeof result !== "object") return "";
  const meta = result.runtime_metadata || result.runtimeMetadata || {};
  return stringValue(meta.github_pr_url);
}

function buildFixPrompt(card, issueId) {
  const title = stringValue(card.title || issueId);
  const description = stringValue(card.description);
  return [
    "You are a bug-fix agent. Fix the following bug directly in the current repository worktree.",
    "",
    "Bug ticket: " + issueId,
    "Title: " + title,
    description ? "Description:\n" + description : "",
    "",
    "Make the minimal code change that fixes the bug. Do NOT design or plan first — this is a direct fix.",
    "If you change frontend/UI code, build the frontend and, if available, use `agent-browser` to capture a screenshot of the fix.",
    "Run relevant validation/tests before finishing, and return a concise summary of the files changed and validation results.",
    "The workflow opens the pull request for you — do not open one yourself.",
  ]
    .filter(Boolean)
    .join("\n");
}

function stringValue(v) {
  if (typeof v === "string") return v;
  if (v === undefined || v === null) return "";
  return String(v);
}

function errorMessage(err) {
  return stringValue(err && (err.message || err.code)) || String(err);
}

// booleanValue mirrors the local-task-runner helper: workflow-run --input
// arrives as strings, so openPullRequest="false" (or 0/no/off) must read false.
function booleanValue(v) {
  if (typeof v === "boolean") return v;
  return ["1", "true", "yes", "on"].includes(stringValue(v).trim().toLowerCase());
}
