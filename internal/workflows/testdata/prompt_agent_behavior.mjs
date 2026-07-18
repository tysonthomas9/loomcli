import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";

const workflowPath = process.argv[2];
if (!workflowPath) {
  throw new Error("usage: node prompt_agent_behavior.mjs <prompt-agent.mjs>");
}
const { run } = await import(pathToFileURL(workflowPath).href);

const TASK_ID = "TASK-42";

function error(code, message) {
  return Object.assign(new Error(message), { code });
}

function makeLoom(options = {}) {
  const calls = [];
  const record = (name, value) => {
    calls.push({ name, value });
  };
  const call = async (name, params, result, failure) => {
    record(name, params);
    if (failure) throw failure;
    return result;
  };

  const loom = {
    driverRunId: options.driverRunId || "driver-run-7",
    binding: {
      config: async () => call("binding.config", undefined, options.bindingConfig || {}, options.bindingError),
    },
    roles: {
      get: async (params) => {
        record("roles.get", params);
        if (options.roleError) throw options.roleError;
        return options.role || {
          prompt: "perform the role",
          role: { task_filter: options.taskFilter || "has_design" },
        };
      },
    },
    tasks: {
      claim: async (params) => call(
        "tasks.claim",
        params,
        options.claimResult === undefined ? { id: TASK_ID } : options.claimResult,
        options.claimError,
      ),
      claimReady: async (params) => call(
        "tasks.claimReady",
        params,
        options.claimReadyResult === undefined ? { id: TASK_ID } : options.claimReadyResult,
        options.claimReadyError,
      ),
      release: async (params) => call("tasks.release", params, undefined, options.releaseError),
    },
    issues: {
      get: async (params) => call(
        "issues.get",
        params,
        options.issue === undefined ? { design: "a design", labels: [] } : options.issue,
        options.issueError,
      ),
      addLabel: async (params) => call("issues.addLabel", params, options.addLabelResult || {}, options.addLabelError),
      update: async (params) => call("issues.update", params, options.updateResult || {}, options.updateError),
    },
    taskRuns: {
      request: async (params) => call("taskRuns.request", params, options.requestResult || {}, options.requestError),
      await: async (params) => call(
        "taskRuns.await",
        params,
        options.awaitResult || { status: "completed", runtime_metadata: { delivery: "patch_back" } },
        options.awaitError,
      ),
    },
    completed: (payload) => {
      record("completed", payload);
      return { disposition: "completed", ...payload };
    },
    failed: (payload) => {
      record("failed", payload);
      return { disposition: "failed", ...payload };
    },
    needsReview: (payload) => {
      record("needsReview", payload);
      return { disposition: "needs_review", ...payload };
    },
  };
  return { loom, calls };
}

function named(calls, name) {
  return calls.filter((call) => call.name === name);
}

function one(calls, name) {
  const matches = named(calls, name);
  assert.equal(matches.length, 1, `expected exactly one ${name} call, got ${matches.length}`);
  return matches[0].value;
}

function none(calls, ...names) {
  for (const name of names) {
    assert.equal(named(calls, name).length, 0, `expected no ${name} call`);
  }
}

function taskRunRequest(calls) {
  const request = one(calls, "taskRuns.request");
  assert.equal(request.closeTask, false, "every prompt-agent TaskRun must keep lifecycle authority with the host");
  return request;
}

function callIndex(calls, name) {
  const index = calls.findIndex((call) => call.name === name);
  assert.notEqual(index, -1, `expected a ${name} call`);
  return index;
}

async function invoke(options, payload) {
  const fixture = makeLoom(options);
  globalThis.__promptAgentMockLoom = fixture.loom;
  try {
    return { result: await run({ payload }), ...fixture };
  } catch (caught) {
    return { caught, ...fixture };
  } finally {
    delete globalThis.__promptAgentMockLoom;
  }
}

const tests = [];
function test(name, body) {
  tests.push({ name, body });
}

test("coder rejects an event carrying needs-revision before claiming", async () => {
  const { result, calls, caught } = await invoke({}, {
    roleName: "coder",
    event: { taskId: TASK_ID, hasDesign: true, labels: ["needs-revision"] },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  none(calls, "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("coder rechecks needs-revision after a stale event and releases the typed claim", async () => {
  const { result, calls, caught } = await invoke({
    issue: { design: "stale design", labels: ["needs-revision"] },
  }, {
    roleName: "coder",
    event: { taskId: TASK_ID, hasDesign: true, labels: [] },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.equal(result.released, true);
  assert.deepEqual(one(calls, "tasks.claim"), { taskId: TASK_ID, actor: "prompt-agent" });
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  none(calls, "issues.update", "taskRuns.request", "taskRuns.await");
});

test("a failed post-claim issue read releases the typed claim before surfacing the error", async () => {
  const readFailure = error("unavailable", "issue read failed");
  const { caught, calls } = await invoke({ issueError: readFailure }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.equal(caught, readFailure);
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  none(calls, "issues.update", "taskRuns.request", "taskRuns.await", "completed", "needsReview");
});

test("a filterless coder closes patch-back work only after the terminal receipt", async () => {
  const { result, calls, caught } = await invoke({}, {
    prompt: "one-off prompt",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  none(calls, "roles.get", "issues.get", "tasks.release");
  const request = taskRunRequest(calls);
  assert.equal(request.taskId, TASK_ID);
  assert.equal(request.runner, "local-task-runner");
  assert.equal(request.input.taskPrompt, "one-off prompt");
  assert.equal(request.input.deliveryMode, "local-branch");
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "closed",
    assignee: "",
  });
  assert.ok(
    callIndex(calls, "taskRuns.await") < callIndex(calls, "issues.update"),
    "the host must not close the card before observing the terminal TaskRun receipt",
  );
});

test("a certified TaskRun request conflict releases the typed claim and never awaits", async () => {
  const conflict = error("conflict", "TaskRun request envelope conflicts with its durable slot");
  const { caught, calls } = await invoke({ requestError: conflict }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.equal(caught, conflict);
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  taskRunRequest(calls);
  none(calls, "issues.update", "taskRuns.await", "completed", "needsReview");
});

for (const code of ["timeout", "unavailable", "internal"]) {
  test(`an ambiguous ${code} request error retains its claim and never awaits`, async () => {
    const ambiguous = error(code, `${code} while reading TaskRun request receipt`);
    const { caught, calls } = await invoke({ requestError: ambiguous }, {
      prompt: "one-off prompt",
      taskId: TASK_ID,
    });

    assert.equal(caught, ambiguous);
    one(calls, "tasks.claim");
    taskRunRequest(calls);
    none(calls, "tasks.release", "issues.get", "issues.update", "taskRuns.await", "completed", "needsReview");
  });
}

test("local-branch completion stamps review and unassigned in one host mutation", async () => {
  const { result, calls, caught } = await invoke({
    awaitResult: {
      status: "completed",
      runtime_metadata: {
        delivery: "local_branch",
        local_branch: "loom/TASK-42",
        head_sha: "abc123",
      },
    },
  }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.delivery, "local_branch");
  assert.equal(result.external_ref, "local-branch:loom/TASK-42@abc123");
  taskRunRequest(calls);
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "review",
    assignee: "",
    externalRef: "local-branch:loom/TASK-42@abc123",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < callIndex(calls, "issues.update"));
  none(calls, "issues.addLabel", "tasks.release", "needsReview");
});

test("a cancelled needs_revision child is labeled before its open and unassigned handoff", async () => {
  const { result, calls, caught } = await invoke({
    awaitResult: {
      status: "cancelled",
      error_class: "runner_cancelled",
      error_message: "cancelled by operator",
      runtime_metadata: { task_outcome: "needs_revision" },
    },
  }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "runner_cancelled");
  assert.match(result.summary, /returned to open\+unassigned/);
  taskRunRequest(calls);
  assert.deepEqual(one(calls, "issues.addLabel"), {
    issueId: TASK_ID,
    label: "needs-revision",
  });
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "open",
    assignee: "",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < callIndex(calls, "issues.addLabel"));
  assert.ok(callIndex(calls, "issues.addLabel") < callIndex(calls, "issues.update"));
  none(calls, "tasks.release", "completed");
});

test("a failed child remains blocked after retry exhaustion", async () => {
  const { result, calls, caught } = await invoke({
    awaitResult: {
      status: "failed",
      error_class: "local_agent_failed",
      error_message: "retry budget exhausted",
      runtime_metadata: { scheduler_state: "blocked" },
    },
  }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "local_agent_failed");
  assert.match(result.summary, /left blocked for review/);
  taskRunRequest(calls);
  none(calls, "issues.addLabel", "issues.update", "tasks.release", "completed");
});

test("a coder host-close failure returns needs_review instead of false completion", async () => {
  const closeFailure = error("internal", "close write failed");
  const { result, calls, caught } = await invoke({ updateError: closeFailure }, {
    prompt: "one-off prompt",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_coder_handoff_failed");
  assert.equal(result.issueId, TASK_ID);
  assert.match(result.summary, /host could not close/);
  assert.match(result.summary, /close write failed/);
  taskRunRequest(calls);
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "closed",
    assignee: "",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < callIndex(calls, "issues.update"));
  none(calls, "completed", "tasks.release", "issues.addLabel");
});

test("a phase-mismatch release failure cannot report a successful handback", async () => {
  const releaseFailure = error("internal", "typed release failed");
  const { caught, calls } = await invoke({
    issue: { design: "stale design", labels: ["needs-revision"] },
    releaseError: releaseFailure,
  }, {
    roleName: "coder",
    event: { taskId: TASK_ID, hasDesign: true, labels: [] },
  });

  assert.equal(caught, releaseFailure);
  one(calls, "tasks.release");
  none(calls, "completed", "needsReview", "taskRuns.request", "taskRuns.await", "issues.update");
});

test("a read failure plus release failure surfaces both errors and no false outcome", async () => {
  const readFailure = error("unavailable", "issue read failed");
  const releaseFailure = error("internal", "typed release failed");
  const { caught, calls } = await invoke({
    issueError: readFailure,
    releaseError: releaseFailure,
  }, {
    roleName: "coder",
    taskId: TASK_ID,
  });

  assert.match(caught.message, /failed to read the claimed task/);
  assert.match(caught.message, /issue read failed/);
  assert.match(caught.message, /typed release also failed: typed release failed/);
  one(calls, "tasks.release");
  none(calls, "completed", "needsReview", "taskRuns.request", "taskRuns.await", "issues.update");
});

let passed = 0;
for (const { name, body } of tests) {
  try {
    await body();
    passed += 1;
    process.stdout.write(`ok ${passed} - ${name}\n`);
  } catch (failure) {
    process.stderr.write(`not ok ${passed + 1} - ${name}\n`);
    throw failure;
  }
}
process.stdout.write(`prompt-agent behavioral scenarios passed: ${passed}\n`);
