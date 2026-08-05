import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, describe, it } from "node:test";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "bug-fix-agent.ts");

let stageRoot;
let mod;

function stub(dir, relFile, contents = "export default {};\n") {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-bug-fix-stage-"));
  const nm = path.join(stageRoot, "node_modules");
  const flue = path.join(nm, "@flue", "runtime");
  const loom = path.join(nm, "@loom", "sdk");

  stub(flue, "index.js", "export const defineAgent = (fn) => ({ __agent: fn });\nexport const defineWorkflow = (def) => def;\n");
  fs.writeFileSync(path.join(flue, "package.json"), JSON.stringify({ name: "@flue/runtime", type: "module", main: "index.js" }));
  stub(loom, "driver.js", "export const createLoomDriverClient = () => globalThis.__loomBugFixTestClient;\n");
  fs.writeFileSync(path.join(loom, "package.json"), JSON.stringify({
    name: "@loom/sdk",
    type: "module",
    exports: { "./driver": "./driver.js" },
  }));

  const copy = path.join(stageRoot, "bug-fix-agent.ts");
  fs.copyFileSync(SOURCE, copy);
  mod = await import(pathToFileURL(copy).href);
});

after(() => {
  delete globalThis.__loomBugFixTestClient;
  try {
    fs.rmSync(stageRoot, { recursive: true, force: true });
  } catch {
    // best-effort cleanup
  }
});

function testLoom(options = {}) {
  const calls = [];
  let updateCall = 0;
  const loom = {
    driverRunId: options.driverRunId || "automation-run-1",
    tasks: {
      async claimReady(input) {
        calls.push(["claimReady", input]);
        return { id: "BUG-7" };
      },
      async release(input) {
        calls.push(["release", input]);
        return { id: input.taskId, released: true };
      },
    },
    issues: {
      async get(input) {
        calls.push(["get", input]);
        return {
          id: input.issueId,
          issue_type: "bug",
          title: "Fix the widget",
          description: "The widget crashes.",
          source_repo: "acme/widgets",
        };
      },
      async update(input) {
        calls.push(["update", input]);
        updateCall += 1;
        if (options.update) return options.update(input, updateCall);
        return input;
      },
    },
    taskRuns: {
      async request(input) {
        calls.push(["request", input]);
        return { taskRunId: input.taskRunId, status: "queued" };
      },
      async await(input) {
        calls.push(["await", input]);
        return options.awaitResult === undefined
          ? { status: "completed", runtime_metadata: { github_pr_url: "https://github.test/acme/widgets/pull/9" } }
          : options.awaitResult;
      },
    },
    completed(input) {
      calls.push(["completed", input]);
      return { status: "completed", ...input };
    },
    needsReview(input) {
      calls.push(["needsReview", input]);
      return { status: "needs_review", ...input };
    },
  };
  return { loom, calls };
}

async function runWith(loom, payload = {}) {
  globalThis.__loomBugFixTestClient = loom;
  return mod.run({ payload });
}

function callsNamed(calls, name) {
  return calls.filter(([callName]) => callName === name);
}

describe("bug-fix child TaskRun contract", () => {
  it("passes the UI-selected repository into atomic ready selection", async () => {
    const fixture = testLoom();
    await runWith(fixture.loom, { targetRepo: "workspace-repo" });
    assert.equal(callsNamed(fixture.calls, "claimReady")[0][1].sourceRepo, "workspace-repo");
  });

  it("scopes the deterministic child ID to the driver run and issue", async () => {
    const first = testLoom({ driverRunId: "automation-run-alpha" });
    await runWith(first.loom);
    const second = testLoom({ driverRunId: "automation-run-beta" });
    await runWith(second.loom);

    assert.equal(
      callsNamed(first.calls, "request")[0][1].taskRunId,
      "bugfix-automation-run-alpha-BUG-7",
    );
    assert.equal(
      callsNamed(second.calls, "request")[0][1].taskRunId,
      "bugfix-automation-run-beta-BUG-7",
    );
  });

  for (const testCase of [
    { name: "failed", run: { status: "failed", error_class: "local_agent_failed", error_message: "codex exited 1" } },
    { name: "cancelled", run: { status: "cancelled" } },
    { name: "unknown", run: {} },
  ]) {
    it("does not stamp or report success when the child status is " + testCase.name, async () => {
      const { loom, calls } = testLoom({ awaitResult: testCase.run });

      const result = await runWith(loom);

      assert.equal(result.status, "needs_review");
      assert.equal(result.taskRunId, "bugfix-automation-run-1-BUG-7");
      assert.match(result.summary, new RegExp("ended " + testCase.name));
      assert.doesNotMatch(result.summary, /delivered|-> PR/);
      assert.equal(callsNamed(calls, "update").length, 0);
      assert.equal(callsNamed(calls, "completed").length, 0);
    });
  }

  it("preserves PR stamping after a completed child", async () => {
    const { loom, calls } = testLoom();

    const result = await runWith(loom);

    assert.equal(result.status, "completed");
    assert.match(result.summary, /BUG-7 -> PR https:\/\/github\.test\/acme\/widgets\/pull\/9/);
    assert.deepEqual(
      callsNamed(calls, "update").map(([, input]) => input),
      [
        { issueId: "BUG-7", status: "open" },
        {
          issueId: "BUG-7",
          status: "review",
          externalRef: "https://github.test/acme/widgets/pull/9",
        },
      ],
    );
  });

  it("requires PR linkage metadata when PR delivery was requested", async () => {
    const { loom, calls } = testLoom({
      awaitResult: { status: "completed", runtime_metadata: {} },
    });

    const result = await runWith(loom);

    assert.equal(result.status, "needs_review");
    assert.equal(result.errorClass, "bug_fix_pr_link_missing");
    assert.equal(result.taskRunId, "bugfix-automation-run-1-BUG-7");
    assert.equal(result.prUrl, null);
    assert.equal(result.handoffStep, "pr-link");
    assert.equal(result.reopenAcknowledged, false);
    assert.match(result.summary, /review handoff is incomplete/);
    assert.equal(callsNamed(calls, "update").length, 0);
    assert.equal(callsNamed(calls, "completed").length, 0);
  });

  it("keeps explicit no-PR delivery as a valid completed sibling behavior", async () => {
    const { loom, calls } = testLoom({
      awaitResult: { status: "completed", runtime_metadata: {} },
    });

    const result = await runWith(loom, { openPullRequest: false });

    assert.equal(result.status, "completed");
    assert.match(result.summary, /delivered \(no PR url\)/);
    assert.equal(result.prUrl, null);
    assert.equal(callsNamed(calls, "update").length, 0);
    assert.equal(callsNamed(calls, "needsReview").length, 0);
  });

  it("reports needs_review when the completed PR cannot reopen the closed card", async () => {
    const { loom, calls } = testLoom({
      update(_input, call) {
        if (call === 1) throw Object.assign(new Error("reopen denied"), { code: "conflict" });
      },
    });

    const result = await runWith(loom);

    assert.equal(result.status, "needs_review");
    assert.equal(result.errorClass, "bug_fix_review_reopen_failed");
    assert.equal(result.taskRunId, "bugfix-automation-run-1-BUG-7");
    assert.equal(result.prUrl, "https://github.test/acme/widgets/pull/9");
    assert.equal(result.handoffStep, "reopen");
    assert.equal(result.reopenAcknowledged, false);
    assert.match(result.summary, /reopen denied/);
    assert.deepEqual(
      callsNamed(calls, "update").map(([, input]) => input),
      [{ issueId: "BUG-7", status: "open" }],
    );
    assert.equal(callsNamed(calls, "completed").length, 0);
  });

  it("reports the acknowledged partial state when review linkage fails after reopen", async () => {
    const { loom, calls } = testLoom({
      update(_input, call) {
        if (call === 2) throw Object.assign(new Error("link update unavailable"), { code: "unavailable" });
      },
    });

    const result = await runWith(loom);

    assert.equal(result.status, "needs_review");
    assert.equal(result.errorClass, "bug_fix_review_stamp_failed");
    assert.equal(result.taskRunId, "bugfix-automation-run-1-BUG-7");
    assert.equal(result.prUrl, "https://github.test/acme/widgets/pull/9");
    assert.equal(result.handoffStep, "review-link");
    assert.equal(result.reopenAcknowledged, true);
    assert.match(result.summary, /reopened the card/);
    assert.match(result.summary, /link update unavailable/);
    assert.deepEqual(
      callsNamed(calls, "update").map(([, input]) => input),
      [
        { issueId: "BUG-7", status: "open" },
        {
          issueId: "BUG-7",
          status: "review",
          externalRef: "https://github.test/acme/widgets/pull/9",
        },
      ],
    );
    assert.equal(callsNamed(calls, "completed").length, 0);
  });
});
