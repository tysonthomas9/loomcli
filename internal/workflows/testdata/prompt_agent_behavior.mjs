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

function triageMetadata(overrides = {}) {
  return {
    delivery: "patch_back",
    backend: "codex",
    files_changed: "0",
    task_outcome: "triaged",
    triage_summary: "P2 bug reproduced with a bounded failing case.",
    triage_priority: "2",
    triage_labels_json: JSON.stringify(["triaged", "triage:reproduced"]),
    ...overrides,
  };
}

function makeLoom(options = {}) {
  const calls = [];
  let issueReadIndex = 0;
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
          role: {
            task_filter: options.taskFilter === undefined ? "has_design" : options.taskFilter,
            read_only: options.readOnly === true,
          },
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
      claimReview: async (params) => call(
        "tasks.claimReview",
        params,
        options.claimReviewResult === undefined
          ? { id: TASK_ID, priority: 2 }
          : options.claimReviewResult,
        options.claimReviewError,
      ),
      release: async (params) => call("tasks.release", params, undefined, options.releaseError),
      releaseReview: async (params) => call(
        "tasks.releaseReview",
        params,
        undefined,
        options.releaseReviewError,
      ),
      handoffReview: async (params) => call(
        "tasks.handoffReview",
        params,
        options.handoffReviewResult || {},
        options.handoffReviewError,
      ),
    },
    issues: {
      get: async (params) => {
        const readIndex = issueReadIndex;
        issueReadIndex += 1;
        const result = Array.isArray(options.issueResults)
          ? options.issueResults[readIndex]
          : (options.issue === undefined ? { design: "a design", labels: [] } : options.issue);
        const failure = Array.isArray(options.issueErrors)
          ? options.issueErrors[readIndex]
          : options.issueError;
        return call("issues.get", params, result, failure);
      },
      addLabel: async (params) => call("issues.addLabel", params, options.addLabelResult || {}, options.addLabelError),
      listComments: async (params) => call(
        "issues.listComments",
        params,
        options.commentsResult || [],
        options.commentsError,
      ),
      comment: async (params) => call("issues.comment", params, options.commentResult || {}, options.commentError),
      update: async (params) => call("issues.update", params, options.updateResult || {}, options.updateError),
      blockRepositoryRequired: async (params) => call(
        "issues.blockRepositoryRequired",
        params,
        options.blockRepositoryRequiredResult === undefined
          ? { blocked: true, issue: { status: "blocked" } }
          : options.blockRepositoryRequiredResult,
        options.blockRepositoryRequiredError,
      ),
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

test("task-ready event requiring a repository stops before claiming", async () => {
  const { result, calls, caught } = await invoke({}, {
    roleName: "planner",
    event: { taskId: TASK_ID, hasDesign: false, labels: [], sourceRepo: "", repositoryRequired: true },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.equal(result.blocker, "repository_required");
  assert.match(result.summary, /requires a repository/);
  assert.deepEqual(one(calls, "issues.blockRepositoryRequired"), { issueId: TASK_ID });
  none(calls, "binding.config", "roles.get", "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("flat Automation task-ready payload requiring a repository skips even when role lookup is broken", async () => {
  const { result, calls, caught } = await invoke({
    bindingError: error("unavailable", "binding config unavailable"),
    roleError: error("unavailable", "role unavailable"),
  }, {
    roleName: "planner",
    taskId: TASK_ID,
    status: "open",
    hasDesign: false,
    labels: [],
    sourceRepo: "",
    repositoryRequired: true,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.blocker, "repository_required");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.deepEqual(one(calls, "issues.blockRepositoryRequired"), { issueId: TASK_ID });
  none(calls, "binding.config", "roles.get", "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("repository-required block failure is terminal and never claims", async () => {
  const { result, calls, caught } = await invoke({
    blockRepositoryRequiredError: error("unavailable", "work items unavailable"),
  }, {
    roleName: "planner",
    event: { taskId: TASK_ID, sourceRepo: "", repositoryRequired: true },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_repository_block_failed");
  assert.match(result.summary, /could not move.*to blocked.*work items unavailable/);
  assert.deepEqual(one(calls, "issues.blockRepositoryRequired"), { issueId: TASK_ID });
  none(calls, "binding.config", "roles.get", "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a repository-required Review card that cannot enter Blocked fails closed", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    blockRepositoryRequiredResult: {
      blocked: false,
      outcome: "not_required",
      issue: { status: "review" },
    },
  }, {
    roleName: "documentation",
    event: {
      taskId: TASK_ID,
      status: "review",
      sourceRepo: "",
      repositoryRequired: true,
    },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_repository_block_not_applied");
  assert.equal(result.issueId, TASK_ID);
  assert.equal(result.claimed, false);
  assert.equal(result.skipped, true);
  assert.equal(result.blocker, "repository_required");
  assert.match(result.summary, /could not enter Blocked/);
  assert.deepEqual(one(calls, "issues.blockRepositoryRequired"), { issueId: TASK_ID });
  none(
    calls,
    "binding.config",
    "roles.get",
    "tasks.claim",
    "tasks.claimReady",
    "tasks.claimReview",
    "issues.get",
    "tasks.release",
    "tasks.releaseReview",
    "taskRuns.request",
    "taskRuns.await",
  );
});

test("flat Automation planner payload rejects a completed design before claiming", async () => {
  const { result, calls, caught } = await invoke({ taskFilter: "needs_plan" }, {
    roleName: "planner",
    taskId: TASK_ID,
    status: "open",
    hasDesign: true,
    labels: [],
    repositoryRequired: false,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  none(calls, "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("flat Automation coder payload rejects a missing design before claiming", async () => {
  const { result, calls, caught } = await invoke({ taskFilter: "has_design" }, {
    roleName: "coder",
    taskId: TASK_ID,
    status: "open",
    hasDesign: false,
    labels: [],
    repositoryRequired: false,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  none(calls, "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
});

for (const legacyFilter of ["", "any"]) {
  test(`a named role with legacy ${JSON.stringify(legacyFilter)} filter cannot steal needs-plan work`, async () => {
    const { result, calls, caught } = await invoke({ taskFilter: legacyFilter }, {
      roleName: "custom-coder",
      event: { taskId: TASK_ID, hasDesign: false, labels: [] },
    });

    assert.equal(caught, undefined);
    assert.equal(result.disposition, "completed");
    assert.equal(result.skipped, true);
    assert.equal(result.claimed, false);
    assert.match(result.summary, /filter has_design/);
    none(calls, "tasks.claim", "tasks.claimReady", "issues.get", "tasks.release", "taskRuns.request", "taskRuns.await");
  });

  test(`a named role with legacy ${JSON.stringify(legacyFilter)} filter runs the coder lifecycle for designed work`, async () => {
    const { result, calls, caught } = await invoke({ taskFilter: legacyFilter }, {
      roleName: "custom-coder",
      event: { taskId: TASK_ID, hasDesign: true, labels: [] },
    });

    assert.equal(caught, undefined);
    assert.equal(result.disposition, "completed");
    assert.deepEqual(one(calls, "tasks.claim"), { taskId: TASK_ID, actor: "prompt-agent" });
    assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
    const request = taskRunRequest(calls);
    assert.equal(request.input.deliveryMode, "local-branch");
    assert.deepEqual(one(calls, "issues.update"), {
      issueId: TASK_ID,
      status: "closed",
      assignee: "",
    });
  });
}

test("a named role with an unknown filter fails closed before claiming", async () => {
  const { result, calls, caught } = await invoke({ taskFilter: "unsupported_docs" }, {
    roleName: "custom-reviewer",
    event: { taskId: TASK_ID, hasDesign: true, labels: [] },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_unsupported_task_filter");
  assert.equal(result.claimed, false);
  assert.equal(result.roleName, "custom-reviewer");
  assert.equal(result.taskFilter, "unsupported_docs");
  assert.match(result.summary, /unsupported task_filter "unsupported_docs"/);
  none(calls, "tasks.claim", "tasks.claimReady", "tasks.claimReview", "issues.get", "tasks.release", "tasks.releaseReview", "taskRuns.request", "taskRuns.await");
});

test("a read-only Review role fails before claiming or dispatching", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    readOnly: true,
  }, {
    roleName: "read-only-documentation",
    event: { taskId: TASK_ID, status: "review" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_review_filter_requires_mutating_role");
  assert.equal(result.claimed, false);
  assert.match(result.summary, /must publish a local branch/);
  none(
    calls,
    "issues.get",
    "tasks.claim",
    "tasks.claimReady",
    "tasks.claimReview",
    "tasks.release",
    "tasks.releaseReview",
    "taskRuns.request",
    "taskRuns.await",
  );
});

test("a review role claims the exact Review generation and returns it to Review", async () => {
  const branch = "loom/docs-task-42";
  const previousHead = "1".repeat(40);
  const newHead = "2".repeat(40);
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    issue: {
      externalRef: `local-branch:${branch}@${previousHead}`,
    },
    claimReviewResult: {
      id: TASK_ID,
      priority: 1,
      sourceRepo: "phase4-terra-ui-repo",
      claimActionId: "claim-review-1",
    },
    awaitResult: {
      status: "completed",
      runtime_metadata: {
        delivery: "local_branch",
        backend: "codex",
        files_changed: "1",
        local_branch: branch,
        head_sha: newHead,
      },
    },
  }, {
    roleName: "documentation",
    taskId: TASK_ID,
    status: "review",
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.outcome, "review-role-review");
  assert.equal(result.external_ref, `local-branch:${branch}@${newHead}`);
  assert.deepEqual(one(calls, "tasks.claimReview"), { taskId: TASK_ID });
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  const request = taskRunRequest(calls);
  assert.equal(request.repoRef, "phase4-terra-ui-repo");
  assert.equal(request.input.deliveryMode, "local-branch");
  assert.equal(request.input.requireLocalBranchDelivery, true);
  assert.equal(request.input.localBranchName, branch);
  assert.equal(request.input.localBranchBaseRef, previousHead);
  assert.equal(request.retainWorkItemClaim, true);
  assert.deepEqual(one(calls, "tasks.handoffReview"), {
    taskId: TASK_ID,
    taskRunId: "promptagent-driver-run-7-TASK-42",
    status: "review",
    priority: 1,
    commentBody: "Review-triggered role documentation completed TaskRun promptagent-driver-run-7-TASK-42."
      + "\n\nfiles_changed=1; delivery=local_branch."
      + `\nDelivered branch: ${branch} @ ${newHead}.`,
    reason: "review-triggered role completed",
    externalRef: `local-branch:${branch}@${newHead}`,
  });
  assert.ok(callIndex(calls, "tasks.claimReview") < callIndex(calls, "issues.get"));
  assert.ok(callIndex(calls, "issues.get") < callIndex(calls, "taskRuns.request"));
  assert.ok(callIndex(calls, "taskRuns.await") < callIndex(calls, "tasks.handoffReview"));
  none(
    calls,
    "tasks.claim",
    "tasks.claimReady",
    "tasks.release",
    "tasks.releaseReview",
    "issues.update",
    "needsReview",
  );
});

for (const invalidDelivery of [
  {
    name: "patch-back delivery",
    metadata: { delivery: "patch_back", local_branch: "loom/docs", head_sha: "2".repeat(40) },
  },
  {
    name: "missing local branch",
    metadata: { delivery: "local_branch", head_sha: "2".repeat(40) },
  },
  {
    name: "invalid local branch",
    metadata: { delivery: "local_branch", local_branch: "bad branch", head_sha: "2".repeat(40) },
  },
  {
    name: "non-40-hex head",
    metadata: { delivery: "local_branch", local_branch: "loom/docs", head_sha: "not-a-commit" },
  },
]) {
  test(`a completed Review child with ${invalidDelivery.name} fails the required delivery`, async () => {
    const { result, calls, caught } = await invoke({
      taskFilter: "review",
      claimReviewResult: { id: TASK_ID, priority: 2, sourceRepo: "review-repo" },
      awaitResult: {
        status: "completed",
        runtime_metadata: {
          backend: "codex",
          files_changed: "0",
          ...invalidDelivery.metadata,
        },
      },
    }, {
      roleName: "documentation",
      taskId: TASK_ID,
      status: "review",
    });

    assert.equal(caught, undefined);
    assert.equal(result.disposition, "needs_review");
    assert.equal(result.errorClass, "prompt_agent_review_delivery_invalid");
    assert.match(result.summary, /without valid local-branch delivery evidence/);
    const handoff = one(calls, "tasks.handoffReview");
    assert.equal(handoff.taskId, TASK_ID);
    assert.equal(handoff.taskRunId, "promptagent-driver-run-7-TASK-42");
    assert.equal(handoff.status, "review");
    assert.equal(handoff.priority, 2);
    assert.equal(handoff.reason, "review-triggered role delivery invalid");
    assert.equal(Object.prototype.hasOwnProperty.call(handoff, "reviewTriggerPolicy"), false);
    assert.equal(Object.prototype.hasOwnProperty.call(handoff, "externalRef"), false);
    assert.match(handoff.commentBody, /operator review is required/);
    none(calls, "issues.update", "tasks.releaseReview", "completed");
  });
}

test("review contention skips before child or model dispatch", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    claimReviewError: error("conflict", "already claimed"),
  }, {
    roleName: "documentation",
    event: { taskId: TASK_ID, status: "review" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.claimed, false);
  assert.match(result.summary, /not claimable in Review/);
  one(calls, "tasks.claimReview");
  none(calls, "tasks.claim", "tasks.claimReady", "taskRuns.request", "taskRuns.await", "tasks.handoffReview");
});

test("a claimed Review card read failure restores the exact Review claim", async () => {
  const readError = error("unavailable", "Review projection unavailable");
  const { caught, calls } = await invoke({
    taskFilter: "review",
    issueError: readError,
  }, {
    roleName: "documentation",
    taskId: TASK_ID,
    status: "review",
  });

  assert.equal(caught, readError);
  assert.deepEqual(one(calls, "tasks.claimReview"), { taskId: TASK_ID });
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  assert.deepEqual(one(calls, "tasks.releaseReview"), { taskId: TASK_ID });
  assert.ok(callIndex(calls, "tasks.claimReview") < callIndex(calls, "issues.get"));
  assert.ok(callIndex(calls, "issues.get") < callIndex(calls, "tasks.releaseReview"));
  none(
    calls,
    "tasks.claim",
    "tasks.claimReady",
    "tasks.release",
    "taskRuns.request",
    "taskRuns.await",
    "tasks.handoffReview",
    "issues.update",
  );
});

test("an unsupported Review external_ref restores the exact Review claim without dispatch", async () => {
  const externalRef = "https://example.invalid/reviews/TASK-42";
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    issue: { externalRef },
  }, {
    roleName: "documentation",
    taskId: TASK_ID,
    status: "review",
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_review_external_ref_unsupported");
  assert.equal(result.issueId, TASK_ID);
  assert.equal(result.claimed, false);
  assert.equal(result.released, true);
  assert.match(result.summary, /unsupported external_ref/);
  assert.deepEqual(one(calls, "tasks.claimReview"), { taskId: TASK_ID });
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  assert.deepEqual(one(calls, "tasks.releaseReview"), { taskId: TASK_ID });
  none(
    calls,
    "tasks.claim",
    "tasks.claimReady",
    "tasks.release",
    "taskRuns.request",
    "taskRuns.await",
    "tasks.handoffReview",
    "issues.update",
  );
});

test("a definitive review child request failure restores the exact Review claim", async () => {
  const conflict = error("conflict", "TaskRun request rejected");
  const { caught, calls } = await invoke({
    taskFilter: "review",
    requestError: conflict,
  }, {
    roleName: "documentation",
    taskId: TASK_ID,
    status: "review",
  });

  assert.equal(caught, conflict);
  one(calls, "tasks.claimReview");
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  const request = taskRunRequest(calls);
  assert.equal(request.input.requireLocalBranchDelivery, true);
  assert.deepEqual(one(calls, "tasks.releaseReview"), { taskId: TASK_ID });
  none(calls, "tasks.release", "taskRuns.await", "tasks.handoffReview", "issues.update");
});

test("an ambiguous review child request retains its exact claim", async () => {
  const timeout = error("timeout", "response lost after request");
  const { caught, calls } = await invoke({
    taskFilter: "review",
    requestError: timeout,
  }, {
    roleName: "documentation",
    taskId: TASK_ID,
    status: "review",
  });

  assert.equal(caught, timeout);
  one(calls, "tasks.claimReview");
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  const request = taskRunRequest(calls);
  assert.equal(request.input.requireLocalBranchDelivery, true);
  none(calls, "tasks.releaseReview", "tasks.release", "taskRuns.await", "tasks.handoffReview", "issues.update");
});

test("a failed review child preserves Fleet's terminal policy", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "review",
    awaitResult: {
      status: "failed",
      error_class: "local_agent_failed",
      error_message: "documentation command failed",
    },
  }, {
    roleName: "documentation",
    event: { taskId: TASK_ID, status: "review" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "local_agent_failed");
  assert.match(result.summary, /policy-owned terminal state/);
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  const request = taskRunRequest(calls);
  assert.equal(request.input.requireLocalBranchDelivery, true);
  none(calls, "tasks.handoffReview", "tasks.releaseReview", "tasks.release", "issues.update", "completed");
});

test("a mutating bug-filtered role fails closed before reading or claiming", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: false,
  }, {
    roleName: "unsafe-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_bug_filter_requires_read_only");
  assert.equal(result.claimed, false);
  none(calls, "issues.get", "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a bug-filtered role rejects a known non-bug event before reading or claiming", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "task" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.match(result.summary, /not my issue type/);
  none(calls, "issues.get", "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a bug-filtered event missing type reads a non-bug card before claim and skips", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "task" },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  none(calls, "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a bug-filtered role treats a card with missing type as a non-match", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { title: "malformed projection" },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  none(calls, "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a bug-filtered card read failure is typed and never claims", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issueError: error("unavailable", "issue projection unavailable"),
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_bug_filter_card_read_failed");
  assert.equal(result.claimed, false);
  assert.match(result.summary, /issue projection unavailable/);
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  none(calls, "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("an untargeted bug-filtered run skips instead of using claimReady", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
  }, {
    roleName: "bug-triage",
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.match(result.summary, /skipped filterless pickup/);
  none(calls, "issues.get", "tasks.claim", "tasks.claimReady", "tasks.release", "taskRuns.request", "taskRuns.await");
});

test("a verified bug card is read before claim and runs the read-only lifecycle", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug", priority: 3 },
    claimResult: { id: TASK_ID, issueType: "bug", priority: 3 },
    awaitResult: {
      status: "completed",
      runtime_metadata: {
        delivery: "patch_back",
        backend: "codex",
        files_changed: "0",
        task_outcome: "triaged",
        triage_summary: "P1 regression reproduced in the parser.",
        triage_priority: "1",
        triage_labels_json: JSON.stringify(["triaged", "triage:parser"]),
      },
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.outcome, "bug-triage-review");
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  assert.deepEqual(one(calls, "tasks.claim"), { taskId: TASK_ID, actor: "prompt-agent" });
  const request = taskRunRequest(calls);
  assert.equal(request.input.deliveryMode, "patch-back");
  assert.equal(request.input.taskOutcomeMode, "bug-triage");
  assert.equal(request.retainWorkItemClaim, true);
  assert.ok(callIndex(calls, "issues.get") < callIndex(calls, "tasks.claim"));
  assert.ok(callIndex(calls, "tasks.claim") < callIndex(calls, "taskRuns.request"));
  assert.deepEqual(one(calls, "tasks.handoffReview"), {
    taskId: TASK_ID,
    taskRunId: "promptagent-driver-run-7-TASK-42",
    status: "review",
    priority: 1,
    labels: ["triaged", "triage:parser"],
    commentBody: "P1 regression reproduced in the parser."
      + "\n\nLoom bug-triage TaskRun: promptagent-driver-run-7-TASK-42",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < callIndex(calls, "tasks.handoffReview"));
  none(
    calls,
    "tasks.claimReady",
    "tasks.release",
    "issues.update",
    "issues.addLabel",
    "issues.listComments",
    "issues.comment",
    "needsReview",
  );
});

test("a completed bug triage without typed output is handed to Review as needs-review", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug", priority: 4 },
    // The claim-committed P0 must win over the stale pre-claim P4.
    claimResult: { id: TASK_ID, issueType: "bug", priority: 0 },
    awaitResult: {
      status: "completed",
      runtime_metadata: { delivery: "patch_back", backend: "codex", files_changed: "0" },
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_bug_triage_outcome_missing");
  assert.equal(result.outcome, "bug-triage-needs-review");
  assert.deepEqual(one(calls, "tasks.handoffReview"), {
    taskId: TASK_ID,
    taskRunId: "promptagent-driver-run-7-TASK-42",
    status: "review",
    priority: 0,
    labels: ["triage:needs-review"],
    commentBody: "Bug triage completed without the required typed triage outcome."
      + " No model-authored triage metadata was applied; inspect the TaskRun transcript."
      + "\n\nLoom bug-triage TaskRun: promptagent-driver-run-7-TASK-42",
  });
  none(calls, "issues.update", "issues.addLabel", "issues.listComments", "issues.comment", "completed");
});

test("bug triage rejects raw workflow-control labels instead of minting them", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "bug" },
    awaitResult: {
      status: "completed",
      runtime_metadata: triageMetadata({
        triage_labels_json: JSON.stringify(["triaged", "needs-revision"]),
      }),
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_bug_triage_outcome_invalid");
  assert.deepEqual(one(calls, "tasks.handoffReview"), {
    taskId: TASK_ID,
    taskRunId: "promptagent-driver-run-7-TASK-42",
    status: "review",
    priority: 2,
    labels: ["triage:needs-review"],
    commentBody: "Bug triage reported invalid triage labels."
      + " No model-authored triage metadata was applied; inspect the TaskRun transcript."
      + "\n\nLoom bug-triage TaskRun: promptagent-driver-run-7-TASK-42",
  });
  none(calls, "issues.update", "issues.addLabel", "issues.listComments", "issues.comment", "completed");
});

test("bug triage surfaces a fenced handoff conflict without partial issue writes", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "bug" },
    handoffReviewError: error("conflict", "claim generation changed"),
    awaitResult: {
      status: "completed",
      runtime_metadata: triageMetadata(),
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_bug_triage_handoff_failed");
  assert.match(result.summary, /claim generation changed/);
  one(calls, "tasks.handoffReview");
  none(calls, "issues.update", "issues.addLabel", "issues.listComments", "issues.comment", "completed");
});

test("bug triage cancellation cannot mint needs-revision or requeue the card", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "bug" },
    awaitResult: {
      status: "cancelled",
      error_class: "task_needs_revision",
      runtime_metadata: { task_outcome: "needs_revision" },
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_bug_triage_outcome_invalid");
  assert.match(result.summary, /policy-owned terminal state/);
  none(calls, "tasks.handoffReview", "issues.update", "issues.addLabel", "tasks.release", "completed");
});

test("a runner-rejected bug triage outcome leaves terminal Blocked policy intact", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "bug" },
    awaitResult: {
      status: "failed",
      error_class: "local_task_outcome_invalid",
      error_message: "triaged task outcome priority must be an integer from 0 through 4",
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_bug_triage_outcome_invalid");
  assert.match(result.summary, /left blocked for review/);
  none(
    calls,
    "tasks.handoffReview",
    "issues.update",
    "issues.addLabel",
    "issues.listComments",
    "issues.comment",
    "completed",
  );
});

test("bug triage delegates exact replay idempotency to the atomic handoff receipt", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "bug" },
    handoffReviewResult: { replayed: true, comment: { id: "comment-1" } },
    awaitResult: {
      status: "completed",
      runtime_metadata: triageMetadata(),
    },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.outcome, "bug-triage-review");
  assert.deepEqual(one(calls, "tasks.handoffReview"), {
    taskId: TASK_ID,
    taskRunId: "promptagent-driver-run-7-TASK-42",
    status: "review",
    priority: 2,
    labels: ["triaged", "triage:reproduced"],
    commentBody: "P2 bug reproduced with a bounded failing case."
      + "\n\nLoom bug-triage TaskRun: promptagent-driver-run-7-TASK-42",
  });
  none(calls, "issues.update", "issues.addLabel", "issues.listComments", "issues.comment", "needsReview");
});

test("a bug card that drifts to non-bug at atomic claim is released before a completed skip", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "task" },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.equal(result.released, true);
  assert.equal(result.issueType, "task");
  assert.match(result.summary, /committed claim issueType="task"/);
  assert.deepEqual(one(calls, "issues.get"), { issueId: TASK_ID });
  assert.deepEqual(one(calls, "tasks.claim"), { taskId: TASK_ID, actor: "prompt-agent" });
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  assert.ok(callIndex(calls, "issues.get") < callIndex(calls, "tasks.claim"));
  assert.ok(callIndex(calls, "tasks.claim") < callIndex(calls, "tasks.release"));
  assert.ok(callIndex(calls, "tasks.release") < callIndex(calls, "completed"));
  none(calls, "taskRuns.request", "taskRuns.await", "issues.update", "failed", "needsReview");
});

test("a bug claim receipt missing generated issueType is released and never dispatches", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID },
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.skipped, true);
  assert.equal(result.claimed, false);
  assert.equal(result.released, true);
  assert.equal(result.issueType, "");
  assert.match(result.summary, /committed claim issueType=""/);
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  assert.ok(callIndex(calls, "tasks.claim") < callIndex(calls, "tasks.release"));
  assert.ok(callIndex(calls, "tasks.release") < callIndex(calls, "completed"));
  none(calls, "taskRuns.request", "taskRuns.await", "issues.update", "failed", "needsReview");
});

test("a bug claim type mismatch plus typed-release failure returns a typed failure without dispatch", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "bug",
    readOnly: true,
    issue: { issue_type: "bug" },
    claimResult: { id: TASK_ID, issueType: "feature" },
    releaseError: error("internal", "typed release unavailable"),
  }, {
    roleName: "bug-triage",
    event: { taskId: TASK_ID, issueType: "bug" },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "failed");
  assert.equal(result.errorClass, "prompt_agent_bug_filter_claim_release_failed");
  assert.equal(result.claimed, true);
  assert.equal(result.released, false);
  assert.equal(result.issueType, "feature");
  assert.match(result.summary, /typed release failed: typed release unavailable/);
  assert.deepEqual(one(calls, "tasks.release"), { taskId: TASK_ID });
  assert.ok(callIndex(calls, "tasks.claim") < callIndex(calls, "tasks.release"));
  assert.ok(callIndex(calls, "tasks.release") < callIndex(calls, "failed"));
  none(calls, "taskRuns.request", "taskRuns.await", "issues.update", "completed", "needsReview");
});

test("a read-only custom role hands designed work to review without closing it", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "has_design",
    readOnly: true,
    issue: { design: "approved design", labels: [] },
    awaitResult: {
      status: "completed",
      runtime_metadata: { delivery: "patch_back", backend: "codex", files_changed: "0" },
    },
  }, {
    roleName: "triage",
    event: { taskId: TASK_ID, hasDesign: true, labels: [] },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.outcome, "read-only-review");
  const request = taskRunRequest(calls);
  assert.equal(request.input.deliveryMode, "patch-back");
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "review",
    assignee: "",
  });
  none(calls, "tasks.release", "issues.addLabel", "needsReview");
});

test("task-ready event permits the single-repo empty-source fallback", async () => {
  const { result, calls, caught } = await invoke({}, {
    prompt: "single-repo task",
    event: { taskId: TASK_ID, sourceRepo: "", repositoryRequired: false },
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.deepEqual(one(calls, "tasks.claim"), { taskId: TASK_ID, actor: "prompt-agent" });
  assert.equal(taskRunRequest(calls).taskId, TASK_ID);
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

test("planner completion verifies the persisted design before host review handoff", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "needs_plan",
    issueResults: [
      { design: "", labels: [], status: "in_progress", assignee: "driver-run-7" },
      { design: "persisted planner design", labels: [], status: "in_progress", assignee: "driver-run-7" },
    ],
  }, {
    roleName: "planner",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "completed");
  assert.equal(result.outcome, "design-review");
  assert.match(result.summary, /design handoff/);
  const request = taskRunRequest(calls);
  assert.equal(request.input.deliveryMode, "patch-back");
  const issueReads = named(calls, "issues.get");
  assert.equal(issueReads.length, 2);
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "review",
    assignee: "",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < calls.indexOf(issueReads[1]));
  assert.ok(calls.indexOf(issueReads[1]) < callIndex(calls, "issues.update"));
  none(calls, "tasks.release", "needsReview");
});

test("planner completion without a persisted design is typed needs_review", async () => {
  const { result, calls, caught } = await invoke({
    taskFilter: "needs_plan",
    issueResults: [
      { design: "", labels: [], status: "in_progress", assignee: "driver-run-7" },
      { design: "   ", labels: [], status: "in_progress", assignee: "driver-run-7" },
    ],
  }, {
    roleName: "planner",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_planner_design_missing");
  assert.equal(result.outcome, "design-missing");
  assert.doesNotMatch(result.summary, /design handoff/);
  assert.match(result.summary, /no persisted design/);
  taskRunRequest(calls);
  const issueReads = named(calls, "issues.get");
  assert.equal(issueReads.length, 2);
  assert.deepEqual(one(calls, "issues.update"), {
    issueId: TASK_ID,
    status: "review",
    assignee: "",
  });
  assert.ok(callIndex(calls, "taskRuns.await") < calls.indexOf(issueReads[1]));
  assert.ok(calls.indexOf(issueReads[1]) < callIndex(calls, "issues.update"));
  assert.ok(callIndex(calls, "issues.update") < callIndex(calls, "needsReview"));
  none(calls, "tasks.release", "completed");
});

test("planner completion with a failed design read is typed needs_review", async () => {
  const readFailure = error("unavailable", "post-run issue read failed");
  const { result, calls, caught } = await invoke({
    taskFilter: "needs_plan",
    issueResults: [
      { design: "", labels: [], status: "in_progress", assignee: "driver-run-7" },
      undefined,
    ],
    issueErrors: [undefined, readFailure],
  }, {
    roleName: "planner",
    taskId: TASK_ID,
  });

  assert.equal(caught, undefined);
  assert.equal(result.disposition, "needs_review");
  assert.equal(result.errorClass, "prompt_agent_planner_design_read_failed");
  assert.doesNotMatch(result.summary, /design handoff/);
  assert.match(result.summary, /could not verify a persisted design/);
  assert.match(result.summary, /post-run issue read failed/);
  taskRunRequest(calls);
  const issueReads = named(calls, "issues.get");
  assert.equal(issueReads.length, 2);
  assert.ok(callIndex(calls, "taskRuns.await") < calls.indexOf(issueReads[1]));
  assert.ok(calls.indexOf(issueReads[1]) < callIndex(calls, "needsReview"));
  none(calls, "issues.update", "tasks.release", "completed");
});

for (const [field, label] of [["sourceRepo", "camelCase"], ["source_repo", "snake_case"]]) {
  test(`a claimed task's ${label} repository scopes its child TaskRun`, async () => {
    const { result, calls, caught } = await invoke({
      claimResult: { id: TASK_ID, [field]: "phase4-terra-ui-repo" },
    }, {
      prompt: "one-off prompt",
      taskId: TASK_ID,
    });

    assert.equal(caught, undefined);
    assert.equal(result.disposition, "completed");
    assert.equal(taskRunRequest(calls).repoRef, "phase4-terra-ui-repo");
  });
}

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
