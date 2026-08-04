import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { normalizeBridgeResult, validateBridgeResult } from "./loom-task-runner-invoker.mjs";

const request = { runner: "local-task-runner" };

describe("validateBridgeResult strict validation (design §4.1)", () => {
  it("rejects null as invalid_task_result", () => {
    const out = validateBridgeResult(null);
    assert.equal(out.status, "failed");
    assert.equal(out.exit_code, 1);
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects undefined as invalid_task_result", () => {
    const out = validateBridgeResult(undefined);
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects an empty object {} as invalid_task_result (never defaults to completed)", () => {
    const out = validateBridgeResult({});
    assert.equal(out.status, "failed");
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects an array as invalid_task_result", () => {
    const out = validateBridgeResult([{ status: "completed" }]);
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects a missing status as invalid_task_result", () => {
    const out = validateBridgeResult({ logs: "x" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects an empty status string as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects an unknown status as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "done" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects a non-terminal status (queued) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "queued" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects a non-terminal status (running) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "running" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects completed + nonzero exit_code as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: 2 });
    assert.equal(out.error_class, "invalid_task_result");
    assert.equal(out.exit_code, 1);
  });

  it("rejects completed + nonzero exitCode (camelCase) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exitCode: 3 });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects completed + stringized nonzero exit_code as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: "1" });
    assert.equal(out.error_class, "invalid_task_result");
    assert.equal(out.exit_code, 1);
  });

  it("rejects completed + boolean exit_code (true) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: true });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects completed + uncoercible exit_code (abc) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: "abc" });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("rejects completed + uncoercible exit_code (object) as invalid_task_result", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: {} });
    assert.equal(out.error_class, "invalid_task_result");
  });

  it("coerces a stringized nonzero exit on a failed result", () => {
    const out = validateBridgeResult({ status: "failed", exit_code: "2" });
    assert.equal(out.status, "failed");
    assert.equal(out.exit_code, 2);
  });

  it("accepts completed + stringized exit_code 0", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: "0" });
    assert.equal(out.status, "completed");
    assert.equal(out.exit_code, 0);
  });

  it("accepts completed with exit 0 and defaults missing exit to 0", () => {
    const out = validateBridgeResult({ status: "completed" });
    assert.equal(out.status, "completed");
    assert.equal(out.exit_code, 0);
  });

  it("accepts completed with explicit exit 0", () => {
    const out = validateBridgeResult({ status: "completed", exit_code: 0 });
    assert.equal(out.status, "completed");
    assert.equal(out.exit_code, 0);
  });

  it("defaults failed exit to 1 when missing", () => {
    const out = validateBridgeResult({ status: "failed" });
    assert.equal(out.status, "failed");
    assert.equal(out.exit_code, 1);
  });

  it("preserves a failed nonzero exit code", () => {
    const out = validateBridgeResult({ status: "failed", exit_code: 7 });
    assert.equal(out.status, "failed");
    assert.equal(out.exit_code, 7);
  });

  it("accepts cancelled and defaults exit to 1", () => {
    const out = validateBridgeResult({ status: "cancelled" });
    assert.equal(out.status, "cancelled");
    assert.equal(out.exit_code, 1);
  });

  it("maps camelCase exitCode to exit_code for failed and drops exitCode", () => {
    const out = validateBridgeResult({ status: "failed", exitCode: 4 });
    assert.equal(out.exit_code, 4);
    assert.equal(out.exitCode, undefined);
  });
});

describe("normalizeBridgeResult attaches invoker metadata", () => {
  it("invalid result still carries runner/kind/entrypoint metadata", () => {
    const out = normalizeBridgeResult(null, request, "flue-workflow", "local-task-runner.ts");
    assert.equal(out.error_class, "invalid_task_result");
    assert.equal(out.runtime_metadata.task_runner_invoker, "loom-task-runner-invoker");
    assert.equal(out.runtime_metadata.runner, "local-task-runner");
    assert.equal(out.runtime_metadata.runner_kind, "flue-workflow");
    assert.equal(out.runtime_metadata.runner_entrypoint, "local-task-runner.ts");
  });

  it("valid completed result preserves status and stringifies metadata", () => {
    const out = normalizeBridgeResult(
      { status: "completed", exit_code: 0, runtime_metadata: { backend: "codex" } },
      request,
      "flue-workflow",
      "local-task-runner.ts",
    );
    assert.equal(out.status, "completed");
    assert.equal(out.exit_code, 0);
    assert.equal(out.runtime_metadata.backend, "codex");
  });
});
