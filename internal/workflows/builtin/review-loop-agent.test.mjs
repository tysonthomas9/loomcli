import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, describe, it } from "node:test";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "review-loop-agent.ts");

let stageRoot;
let mod;

function stub(dir, relFile, contents = "export default {};\n") {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-review-loop-stage-"));
  const nm = path.join(stageRoot, "node_modules");
  const flue = path.join(nm, "@flue", "runtime");
  const loom = path.join(nm, "@loom", "sdk");

  stub(flue, "index.js", "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (def) => def;\n");
  fs.writeFileSync(path.join(flue, "package.json"), JSON.stringify({ name: "@flue/runtime", type: "module", main: "index.js" }));
  stub(loom, "driver.js", "export const createLoomDriverClient = () => ({});\n");
  fs.writeFileSync(path.join(loom, "package.json"), JSON.stringify({
    name: "@loom/sdk",
    type: "module",
    exports: { "./driver": "./driver.js" },
  }));

  const copy = path.join(stageRoot, "review-loop-agent.ts");
  fs.copyFileSync(SOURCE, copy);
  mod = await import(pathToFileURL(copy).href);
});

after(() => {
  try {
    fs.rmSync(stageRoot, { recursive: true, force: true });
  } catch {
    // best-effort cleanup
  }
});

const SUBJECT = {
  owner: "acme",
  name: "widgets",
  repo: "acme/widgets",
  prNumber: 123,
  slug: "acme/widgets#123",
};

function codedError(code, message, retryable = false) {
  return Object.assign(new Error(message), { code, retryable });
}

function testLoom(options = {}) {
  const calls = [];
  const loom = {
    driverRunId: options.driverRunId || "automation-run-1",
    connectors: {
      github: {
        async readPullRequest(input) {
          calls.push(["readPullRequest", input]);
          if (options.readPullRequest) return options.readPullRequest(input);
          return { body: { state: "open", headSha: "abc123", baseRef: "main" } };
        },
        async compare(input) {
          calls.push(["compare", input]);
          if (options.compare) return options.compare(input);
          return { body: { diff: "diff --git a/a b/a" } };
        },
        async postReview(input) {
          calls.push(["postReview", input]);
          if (options.postReview) return options.postReview(input);
          return { body: { htmlUrl: "https://github.test/review/1" } };
        },
      },
    },
    tasks: {
      async claimReview(input) {
        calls.push(["claimReview", input]);
        if (options.claimReview) return options.claimReview(input);
        return { id: input.taskId, claimActionId: `claim:${input.taskId}` };
      },
      async releaseReview(input) {
        calls.push(["releaseReview", input]);
        if (options.releaseReview) return options.releaseReview(input);
        return { id: input.taskId, status: "review", released: true };
      },
      async handoffReview(input) {
        calls.push(["handoffReview", input]);
        if (options.handoffReview) return options.handoffReview(input);
        return { id: input.taskId, status: input.status, released: true };
      },
    },
    issues: {
      async addLabel(input) {
        calls.push(["addLabel", input]);
        if (options.addLabel) return options.addLabel(input);
        return { id: input.issueId };
      },
      async removeLabel(input) {
        calls.push(["removeLabel", input]);
        if (options.removeLabel) return options.removeLabel(input);
        return { id: input.issueId };
      },
    },
    taskRuns: {
      async request(input) {
        calls.push(["request", input]);
        if (options.request) return options.request(input);
        return { taskRunId: input.taskRunId, status: "queued" };
      },
      async await(input) {
        calls.push(["await", input]);
        if (options.await) return options.await(input);
        return {
          status: "completed",
          runtime_metadata: {
            review_findings: JSON.stringify({ summary: "Looks good", comments: [] }),
          },
        };
      },
    },
  };
  return { loom, calls };
}

function callNames(calls) {
  return calls.map(([name]) => name);
}

describe("review-loop Work Item ownership", () => {
  it("honors the UI-selected GitHub repository", () => {
    assert.equal(mod.reviewCardMatchesTarget(
      { external_ref: "https://github.com/acme/widgets/pull/123", source_repo: "workspace-widgets" },
      { githubRepo: "acme/widgets", targetRepo: "workspace-widgets" },
    ), true);
    assert.equal(mod.reviewCardMatchesTarget(
      { external_ref: "https://github.com/other/widgets/pull/123", source_repo: "workspace-widgets" },
      { githubRepo: "acme/widgets", targetRepo: "workspace-widgets" },
    ), false);
  });

  it("claims the local Loom issue before requesting a review child", async () => {
    const { loom, calls } = testLoom({
      request(input) {
        return { taskRunId: input.taskRunId, status: "queued", replay: true };
      },
      await() {
        return {
          status: "completed",
          runtime_metadata: {
            review_findings: JSON.stringify({
              summary: "Looks good",
              comments: [
                { path: "internal/worker.go", line: 73, body: "Release the lease on this error path." },
                { path: "internal/deletion.go", body: "Preserve this unanchored finding in the review body." },
              ],
            }),
          },
        };
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, true);
    assert.equal(result.reviewUrl, "https://github.test/review/1");
    const names = callNames(calls);
    assert.ok(names.indexOf("readPullRequest") < names.indexOf("compare"));
    assert.ok(names.indexOf("compare") < names.indexOf("claimReview"));
    assert.ok(names.indexOf("claimReview") < names.indexOf("request"));
    assert.ok(names.indexOf("request") < names.indexOf("await"));
    assert.ok(names.indexOf("await") < names.indexOf("addLabel"));
    assert.ok(names.indexOf("addLabel") < names.indexOf("postReview"));
    assert.ok(names.indexOf("postReview") < names.indexOf("handoffReview"));
    const requested = calls.find(([name]) => name === "request")[1];
    assert.equal(requested.taskId, "LOCALMODE-42");
    assert.equal(requested.closeTask, false);
    assert.equal(requested.retainWorkItemClaim, true);
    assert.equal(requested.input.repo, "acme/widgets");
    assert.equal(requested.input.prNumber, 123);
    assert.equal(requested.taskRunId, "review-loop-automation-run-1-localmode-42-c1");
    const postedReview = calls.find(([name]) => name === "postReview")[1];
    assert.equal(postedReview.body, "Looks good");
    assert.deepEqual(postedReview.comments, [
      { path: "internal/worker.go", line: 73, body: "Release the lease on this error path." },
      { path: "internal/deletion.go", body: "Preserve this unanchored finding in the review body." },
    ]);
    assert.deepEqual(calls.find(([name]) => name === "handoffReview")[1], {
      taskId: "LOCALMODE-42",
      taskRunId: "review-loop-automation-run-1-localmode-42-c1",
      status: "open",
      reason: "review cycle 1 posted",
    });
  });

  it("scopes deterministic child IDs by parent run and Loom issue", async () => {
    const parentA = testLoom({ driverRunId: "automation-parent-a" });
    const parentB = testLoom({ driverRunId: "automation-parent-b" });

    await mod.reviewPullRequest(parentA.loom, "github", SUBJECT, 1, "LOCALMODE-1");
    await mod.reviewPullRequest(parentA.loom, "github", SUBJECT, 1, "LOCALMODE-2");
    await mod.reviewPullRequest(parentA.loom, "github", SUBJECT, 1, "LOCALMODE-1");
    await mod.reviewPullRequest(parentB.loom, "github", SUBJECT, 1, "LOCALMODE-1");

    const parentAIDs = parentA.calls
      .filter(([name]) => name === "request")
      .map(([, input]) => input.taskRunId);
    const parentBID = parentB.calls.find(([name]) => name === "request")[1].taskRunId;
    assert.deepEqual(parentAIDs, [
      "review-loop-automation-parent-a-localmode-1-c1",
      "review-loop-automation-parent-a-localmode-2-c1",
      "review-loop-automation-parent-a-localmode-1-c1",
    ]);
    assert.notEqual(parentAIDs[0], parentAIDs[1], "two cards for one PR must not collide");
    assert.equal(parentAIDs[0], parentAIDs[2], "same-parent replay must remain deterministic");
    assert.notEqual(parentAIDs[0], parentBID, "a successor parent run must get a fresh TaskRun");
  });

  it("preserves connector preflight semantics and does not claim when PR read fails", async () => {
    const { loom, calls } = testLoom({
      readPullRequest() {
        throw codedError("forbidden", "connector grant denied");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^read_pr_failed/);
    assert.equal(calls.some(([name]) => name === "claimReview"), false);
    assert.equal(calls.some(([name]) => name === "request"), false);
  });

  it("does not dispatch when the review claim is already owned", async () => {
    const { loom, calls } = testLoom({
      claimReview() {
        throw codedError("conflict", "review card already claimed");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_claim_failed/);
    assert.equal(result.claimRetained, undefined);
    assert.equal(calls.some(([name]) => name === "request"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("does not guess at cleanup after an ambiguous claim response", async () => {
    const { loom, calls } = testLoom({
      claimReview() {
        throw codedError("timeout", "claim response timed out");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_claim_ambiguous/);
    assert.equal(result.claimRetained, true);
    assert.equal(calls.some(([name]) => name === "request"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("retains recovery authority for a malformed successful claim receipt", async () => {
    const { loom, calls } = testLoom({
      claimReview() {
        return {};
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.equal(result.reason, "review_claim_receipt_invalid");
    assert.equal(result.claimRetained, true);
    assert.equal(calls.some(([name]) => name === "request"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("restores review after a definitive request conflict and never awaits a phantom run", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("conflict", "TaskRun lineage conflicts with the committed envelope");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_task_dispatch_failed/);
    assert.equal(result.claimRetained, undefined);
    assert.equal(result.taskRunId, "review-loop-automation-run-1-localmode-42-c1");
    assert.deepEqual(callNames(calls).filter((name) => name === "releaseReview"), ["releaseReview"]);
    assert.equal(calls.some(([name]) => name === "await"), false);
    assert.ok(callNames(calls).indexOf("request") < callNames(calls).indexOf("releaseReview"));
  });

  it("retains ownership when the request response is ambiguous", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("unavailable", "response disconnected");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_task_dispatch_ambiguous/);
    assert.equal(result.claimRetained, true);
    assert.equal(result.taskRunId, "review-loop-automation-run-1-localmode-42-c1");
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
    assert.equal(calls.some(([name]) => name === "await"), false);
  });

  it("retains ownership when await fails after a certified request", async () => {
    const { loom, calls } = testLoom({
      await() {
        throw codedError("not_found", "TaskRun projection unavailable");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_task_await_failed/);
    assert.equal(result.claimRetained, true);
    assert.equal(result.taskRunId, "review-loop-automation-run-1-localmode-42-c1");
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("retains the exact claim when cycle persistence, an ambiguous connector post, or atomic handoff fails", async () => {
    const cases = [
      {
        name: "cycle label",
        options: { addLabel() { throw codedError("unavailable", "label store unavailable"); } },
        reason: /^review_cycle_label_failed/,
        absent: ["postReview", "handoffReview"],
      },
      {
        name: "ambiguous connector post",
        options: { postReview() { throw codedError("upstream_error", "GitHub response lost", true); } },
        reason: /^post_review_ambiguous/,
        absent: ["handoffReview"],
      },
      {
        name: "handoff",
        options: { handoffReview() { throw codedError("unavailable", "handoff response lost"); } },
        reason: /^review_handoff_failed/,
        absent: [],
      },
    ];

    for (const tc of cases) {
      const { loom, calls } = testLoom(tc.options);
      const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");
      assert.equal(result.ok, false, tc.name);
      assert.match(result.reason, tc.reason, tc.name);
      assert.equal(result.claimRetained, true, tc.name);
      assert.equal(result.taskRunId, "review-loop-automation-run-1-localmode-42-c1", tc.name);
      assert.equal(calls.some(([name]) => name === "releaseReview"), false, tc.name);
      for (const absent of tc.absent) {
        assert.equal(calls.some(([name]) => name === absent), false, `${tc.name}: ${absent}`);
      }
    }
  });

  it("rolls back the cycle marker and Review claim after a definitive connector refusal", async () => {
    const { loom, calls } = testLoom({
      postReview() {
        throw codedError("grant_denied", "github.review.post grant was revoked");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^post_review_failed/);
    assert.equal(result.claimRetained, undefined);
    assert.deepEqual(
      calls.filter(([name]) => name === "removeLabel").map(([, input]) => input),
      [{ issueId: "LOCALMODE-42", label: "review-cycle:1" }],
    );
    assert.deepEqual(
      calls.filter(([name]) => name === "releaseReview").map(([, input]) => input),
      [{ taskId: "LOCALMODE-42" }],
    );
    const names = callNames(calls);
    assert.ok(names.indexOf("addLabel") < names.indexOf("postReview"));
    assert.ok(names.indexOf("postReview") < names.indexOf("removeLabel"));
    assert.ok(names.indexOf("removeLabel") < names.indexOf("releaseReview"));
    assert.equal(calls.some(([name]) => name === "handoffReview"), false);
  });

  it("keeps recovery ownership when definitive-post compensation cannot be certified", async () => {
    const { loom, calls } = testLoom({
      postReview() {
        throw codedError("stale_subject", "pull request head changed");
      },
      removeLabel() {
        throw codedError("unavailable", "cycle marker response lost", true);
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /review_cycle_restore_failed/);
    assert.equal(result.claimRetained, true);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
    assert.equal(calls.some(([name]) => name === "handoffReview"), false);
  });

  it("surfaces a failed atomic review restoration and retains recovery ownership", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("unschedulable", "no capable worker");
      },
      releaseReview() {
        throw codedError("unavailable", "review release unavailable");
      },
    });

    const result = await mod.reviewPullRequest(loom, "github", SUBJECT, 1, "LOCALMODE-42");

    assert.equal(result.ok, false);
    assert.match(result.reason, /^review_claim_restore_failed/);
    assert.equal(result.claimRetained, true);
    assert.equal(calls.some(([name]) => name === "await"), false);
  });
});
