// Rides flue's own adapter contract suite (the acceptance gate the FLUE-DURABILITY
// proposal mandates: "do not hand-write parallel tests") to prove the loom on-host
// durability adapter satisfies flue's SessionStore + AgentSubmissionStore contract.
//
// Each test gets a fresh FILE-backed store keyed by a unique task-run id under a
// temp dir, so the contract is exercised against the real on-disk path the runner
// uses in production (not just :memory:).
//
// Run via scripts/test-flue-durability-store.sh (vitest, resolved from the sibling
// flue repo). NOT a node:test file — named *.spec.mjs so the node --test globs that
// run the other builtin tests never pick it up.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { defineStoreContractTests } from "@flue/runtime/test-utils";
import { loomTaskRunAdapter, taskRunDurabilityPath } from "./flue-durability-store.mjs";

// Loom-specific path derivation (the durability mechanism: a store keyed by the
// reclaim-stable task-run id, so a relaunched runner re-opens the same store).
describe("taskRunDurabilityPath", () => {
  it("keys the durable store by task-run id under the base dir", () => {
    expect(taskRunDurabilityPath("tr-abc", "/base")).toBe("/base/tr-abc.sqlite");
  });
  it("sanitizes unsafe characters in the id", () => {
    expect(taskRunDurabilityPath("a/b c:d", "/base")).toBe("/base/a_b_c_d.sqlite");
  });
  it("requires a non-empty id", () => {
    expect(() => taskRunDurabilityPath("", "/base")).toThrow("task-run id is required");
  });
  it("honors LOOM_TASK_RUN_DURABILITY_DIR when no baseDir is given", () => {
    const prev = process.env.LOOM_TASK_RUN_DURABILITY_DIR;
    process.env.LOOM_TASK_RUN_DURABILITY_DIR = "/env-root";
    try {
      expect(taskRunDurabilityPath("tr-1")).toBe("/env-root/tr-1.sqlite");
    } finally {
      if (prev === undefined) delete process.env.LOOM_TASK_RUN_DURABILITY_DIR;
      else process.env.LOOM_TASK_RUN_DURABILITY_DIR = prev;
    }
  });
});

const testRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-flue-store-"));
let seq = 0;
let current;

defineStoreContractTests("loom task-run durability store (file-backed, reclaim-stable)", {
  async create() {
    const taskRunId = `tr-${++seq}`;
    current = loomTaskRunAdapter({ taskRunId, baseDir: testRoot });
    await current.migrate?.();
    // The built @flue/runtime adapter's connect() returns the AgentExecutionStore
    // ({ sessions, submissions }) directly; run-store/registry are separate methods.
    return await current.connect();
  },
  async cleanup() {
    await current?.close?.();
    current = undefined;
  },
});
