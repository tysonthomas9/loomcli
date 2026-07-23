import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, describe, it } from "node:test";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "local-review-agent.ts");

let stageRoot;
let mod;

function stub(dir, relFile, contents = "export default {};\n") {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-local-review-stage-"));
  const nm = path.join(stageRoot, "node_modules");
  const flue = path.join(nm, "@flue", "runtime");
  const loom = path.join(nm, "@loom", "sdk");

  stub(flue, "index.js", "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (def) => def;\n");
  fs.writeFileSync(path.join(flue, "package.json"), JSON.stringify({ name: "@flue/runtime", type: "module", main: "index.js" }));
  stub(loom, "driver.js", "export const createLoomDriverClient = () => globalThis.__loomLocalReviewClient;\n");
  fs.writeFileSync(path.join(loom, "package.json"), JSON.stringify({
    name: "@loom/sdk",
    type: "module",
    exports: { "./driver": "./driver.js" },
  }));

  const copy = path.join(stageRoot, "local-review-agent.ts");
  fs.copyFileSync(SOURCE, copy);
  mod = await import(pathToFileURL(copy).href);
});

after(() => {
  delete globalThis.__loomLocalReviewClient;
  try {
    fs.rmSync(stageRoot, { recursive: true, force: true });
  } catch {
    // best-effort cleanup
  }
});

function card(id, labels = [], sourceRepo = "") {
  return {
    id,
    external_ref: `local-branch:loom/${id}@abcdef1234567890`,
    labels,
    source_repo: sourceRepo,
  };
}

function codedError(code, message) {
  return Object.assign(new Error(message), { code });
}

function testLoom(options = {}) {
  const calls = [];
  const cards = options.cards || [card("TASK-1")];
  const loom = {
    driverRunId: options.driverRunId || "driver-run-1",
    issues: {
      async list(input) {
        calls.push(["list", input]);
        return cards;
      },
      async comment(input) {
        calls.push(["comment", input]);
        if (options.comment) return options.comment(input);
      },
      async addLabel(input) {
        calls.push(["addLabel", input]);
        if (options.addLabel) return options.addLabel(input);
      },
      async update(input) {
        calls.push(["update", input]);
        if (options.update) return options.update(input);
      },
    },
    tasks: {
      async diff(input) {
        calls.push(["diff", input]);
        if (options.diff) return options.diff(input);
        return {
          baseRef: "localmode",
          baseSha: "base1234567890",
          resolvedHead: "abcdef1234567890",
          diff: "diff --git a/README.md b/README.md\n+review me\n",
        };
      },
      async claimReview(input) {
        calls.push(["claimReview", input]);
        if (options.claimReview) return options.claimReview(input);
        return { id: input.taskId, claimActionId: `claim:${input.taskId}` };
      },
      async releaseReview(input) {
        calls.push(["releaseReview", input]);
        if (options.releaseReview) return options.releaseReview(input);
        return { id: input.taskId, released: true, status: "review" };
      },
      async handoffReview(input) {
        calls.push(["handoffReview", input]);
        if (options.handoffReview) return options.handoffReview(input);
        return { id: input.taskId, released: true, status: input.status };
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
            review_findings: { summary: "No blocking findings.", comments: [] },
          },
        };
      },
    },
    completed(input) {
      return { status: "completed", ...input };
    },
  };
  return { loom, calls };
}

async function runWith(loom, payload = {}) {
  globalThis.__loomLocalReviewClient = loom;
  return mod.run({ payload });
}

function callNames(calls) {
  return calls.map(([name]) => name);
}

describe("local-review Work Item ownership", () => {
  it("honors the UI-selected workspace repository", async () => {
    const { loom, calls } = testLoom({
      cards: [card("OTHER-1", [], "other"), card("TASK-1", [], "alpha")],
    });
    await runWith(loom, { targetRepo: "alpha" });
    assert.deepEqual(
      calls.filter(([name]) => name === "diff").map(([, input]) => input.taskId),
      ["TASK-1"],
    );
  });

  it("claims the review card before requesting a certified child", async () => {
    const { loom, calls } = testLoom({
      request(input) {
        return { taskRunId: input.taskRunId, status: "queued", replay: true };
      },
    });

    const result = await runWith(loom);

    assert.equal(result.approved.length, 1);
    const names = callNames(calls);
    assert.ok(names.indexOf("diff") < names.indexOf("claimReview"));
    assert.ok(names.indexOf("claimReview") < names.indexOf("request"));
    assert.ok(names.indexOf("request") < names.indexOf("await"));
    const request = calls.find(([name]) => name === "request")[1];
    assert.equal(request.closeTask, false);
    assert.equal(request.retainWorkItemClaim, true);
    assert.equal(request.taskRunId, "local-review-driver-run-1-TASK-1-c1");
    assert.deepEqual(calls.find(([name]) => name === "handoffReview")[1], {
      taskId: "TASK-1",
      taskRunId: "local-review-driver-run-1-TASK-1-c1",
      status: "closed",
      reason: "local review approved",
    });
    assert.equal(calls.some(([name]) => name === "update"), false);
    assert.ok(names.indexOf("await") < names.indexOf("comment"));
    assert.ok(names.indexOf("comment") < names.indexOf("handoffReview"));
  });

  it("scopes TaskRun identity to a safe DriverRun id while preserving same-parent determinism", async () => {
    const first = testLoom({ driverRunId: "automation/run#1" });
    await runWith(first.loom);
    const firstID = first.calls.find(([name]) => name === "request")[1].taskRunId;

    const replay = testLoom({ driverRunId: "automation/run#1" });
    await runWith(replay.loom);
    const replayID = replay.calls.find(([name]) => name === "request")[1].taskRunId;

    const successor = testLoom({ driverRunId: "automation/run#2" });
    await runWith(successor.loom);
    const successorID = successor.calls.find(([name]) => name === "request")[1].taskRunId;

    assert.equal(firstID, "local-review-automation-run-1-TASK-1-c1");
    assert.equal(replayID, firstID);
    assert.equal(successorID, "local-review-automation-run-2-TASK-1-c1");
    assert.notEqual(successorID, firstID);
  });

  it("isolates a claim conflict to one card and continues the sweep", async () => {
    const cards = [card("TASK-A"), card("TASK-B")];
    const { loom, calls } = testLoom({
      cards,
      claimReview({ taskId }) {
        if (taskId === "TASK-A") throw codedError("conflict", "review card already claimed");
        return { id: taskId, claimActionId: `claim:${taskId}` };
      },
    });

    const result = await runWith(loom);

    assert.deepEqual(result.skipped.map((entry) => [entry.issueId, entry.reason, entry.errorClass]), [
      ["TASK-A", "claim_failed", "local_review_claim_conflict"],
    ]);
    assert.deepEqual(result.approved.map((entry) => entry.issueId), ["TASK-B"]);
    assert.equal(calls.filter(([name]) => name === "request").length, 1);
    assert.equal(calls.find(([name]) => name === "request")[1].taskId, "TASK-B");
  });

  it("does not dispatch from an invalid ownership receipt", async () => {
    const { loom, calls } = testLoom({
      claimReview() {
        return {};
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_claim_receipt_invalid");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.match(result.skipped[0].detail, /ownership retained for recovery/);
    assert.equal(calls.some(([name]) => name === "request"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("does not guess at cleanup after an ambiguous claim response", async () => {
    const { loom, calls } = testLoom({
      claimReview() {
        throw codedError("timeout", "claim response timed out");
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_claim_ambiguous");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.match(result.skipped[0].detail, /ownership may have committed/);
    assert.equal(calls.some(([name]) => name === "request"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("does not claim cards that already reached the cooperative cap", async () => {
    const cards = [
      card("TASK-CAPPED", ["review-cycle:2", "review-cycle-cap-noted"]),
      card("TASK-READY"),
    ];
    const { loom, calls } = testLoom({ cards });

    const result = await runWith(loom, { cap: 2 });

    assert.deepEqual(result.skipped.map((entry) => [entry.issueId, entry.reason]), [
      ["TASK-CAPPED", "cap_reached"],
    ]);
    assert.deepEqual(
      calls.filter(([name]) => name === "claimReview").map(([, input]) => input.taskId),
      ["TASK-READY"],
    );
  });

  it("isolates cap comment and label failures and continues to an eligible sibling", async () => {
    const cards = [
      card("TASK-CAP-COMMENT", ["review-cycle:2"]),
      card("TASK-CAP-LABEL", ["review-cycle:2"]),
      card("TASK-READY"),
    ];
    const { loom, calls } = testLoom({
      cards,
      comment({ issueId }) {
        if (issueId === "TASK-CAP-COMMENT") throw codedError("unavailable", "comment store unavailable");
      },
      addLabel({ issueId }) {
        if (issueId === "TASK-CAP-LABEL") throw codedError("conflict", "label version changed");
      },
    });

    const result = await runWith(loom, { cap: 2 });

    assert.deepEqual(result.skipped.map((entry) => [entry.issueId, entry.reason, entry.mutation, entry.errorClass]), [
      ["TASK-CAP-COMMENT", "cap_note_failed", "cap_comment", "local_review_cap_comment_unavailable"],
      ["TASK-CAP-LABEL", "cap_note_failed", "cap_label", "local_review_cap_label_conflict"],
    ]);
    assert.deepEqual(result.approved.map((entry) => entry.issueId), ["TASK-READY"]);
    assert.deepEqual(
      calls.filter(([name]) => name === "claimReview").map(([, input]) => input.taskId),
      ["TASK-READY"],
    );
  });

  it("isolates completed-child comment, handoff, and label failures while retaining exact ownership", async () => {
    const cards = [
      card("TASK-COMMENT"),
      card("TASK-HANDOFF"),
      card("TASK-LABEL"),
      card("TASK-GOOD"),
    ];
    const { loom, calls } = testLoom({
      cards,
      await({ taskRunId }) {
        const blocking = taskRunId.includes("TASK-LABEL") || taskRunId.includes("TASK-GOOD");
        return {
          status: "completed",
          runtime_metadata: {
            review_findings: {
              summary: blocking ? "Changes requested." : "No blocking findings.",
              comments: blocking ? [{ path: "main.go", line: 7, body: "Fix this." }] : [],
            },
          },
        };
      },
      comment({ issueId }) {
        if (issueId === "TASK-COMMENT") throw codedError("unavailable", "comment write failed");
      },
      handoffReview({ taskId }) {
        if (taskId === "TASK-HANDOFF") throw codedError("conflict", "claim generation changed");
      },
      addLabel({ issueId }) {
        if (issueId === "TASK-LABEL") throw codedError("unavailable", "label write failed");
      },
    });

    const result = await runWith(loom);

    assert.deepEqual(result.skipped.map((entry) => [entry.issueId, entry.reason, entry.mutation, entry.errorClass]), [
      ["TASK-COMMENT", "handoff_failed", "result_comment", "local_review_result_comment_unavailable"],
      ["TASK-HANDOFF", "handoff_failed", "result_handoff", "local_review_result_handoff_conflict"],
      ["TASK-LABEL", "handoff_failed", "result_label", "local_review_result_label_unavailable"],
    ]);
    assert.deepEqual(result.skipped.map((entry) => entry.claimRetained), [true, true, true]);
    assert.deepEqual(result.reviewed.map((entry) => entry.issueId), ["TASK-GOOD"]);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
    assert.deepEqual(
      calls.filter(([name]) => name === "handoffReview").map(([, input]) => [input.taskId, input.status]),
      [["TASK-HANDOFF", "closed"], ["TASK-GOOD", "open"]],
    );
    assert.deepEqual(
      calls.filter(([name]) => name === "request").map(([, input]) => input.taskId),
      ["TASK-COMMENT", "TASK-HANDOFF", "TASK-LABEL", "TASK-GOOD"],
    );
  });

  it("keeps the retained claim when a completed child returns malformed findings", async () => {
    const { loom, calls } = testLoom({
      await() {
        return { status: "completed", runtime_metadata: { review_findings: "not json" } };
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_findings_invalid");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.match(result.skipped[0].detail, /claim retained for parent recovery/);
    assert.equal(calls.some(([name]) => name === "handoffReview"), false);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("atomically restores review after a definitive request conflict and never awaits a phantom run", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("conflict", "TaskRun lineage conflicts with the committed envelope");
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_task_dispatch_failed");
    assert.equal(result.skipped[0].claimRetained, undefined);
    assert.deepEqual(callNames(calls).filter((name) => name === "releaseReview"), ["releaseReview"]);
    assert.equal(calls.some(([name]) => name === "await"), false);
    assert.ok(callNames(calls).indexOf("request") < callNames(calls).indexOf("releaseReview"));
  });

  it("retains ownership when a request response is ambiguous", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("unavailable", "response disconnected");
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_task_dispatch_ambiguous");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.equal(result.skipped[0].taskRunId, "local-review-driver-run-1-TASK-1-c1");
    assert.match(result.skipped[0].detail, /claim retained for recovery/);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
    assert.equal(calls.some(([name]) => name === "await"), false);
  });

  it("retains ownership for child recovery when await fails after a certified request", async () => {
    const { loom, calls } = testLoom({
      await() {
        throw codedError("not_found", "TaskRun read projection is unavailable");
      },
    });

    const result = await runWith(loom);

    assert.equal(result.skipped[0].errorClass, "local_review_task_await_failed");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.equal(result.skipped[0].taskRunId, "local-review-driver-run-1-TASK-1-c1");
    assert.match(result.skipped[0].detail, /claim retained for child recovery/);
    assert.equal(calls.some(([name]) => name === "releaseReview"), false);
  });

  it("surfaces atomic review restoration failure without processing the card as successful", async () => {
    const { loom, calls } = testLoom({
      request() {
        throw codedError("unschedulable", "no capable worker");
      },
      releaseReview() {
        throw codedError("unavailable", "review release unavailable");
      },
    });

    const result = await runWith(loom);

    assert.equal(result.approved.length, 0);
    assert.equal(result.reviewed.length, 0);
    assert.equal(result.skipped[0].errorClass, "local_review_claim_restore_failed");
    assert.equal(result.skipped[0].claimRetained, true);
    assert.match(result.skipped[0].detail, /atomic review release also failed/);
    assert.equal(calls.some(([name]) => name === "await"), false);
  });
});
