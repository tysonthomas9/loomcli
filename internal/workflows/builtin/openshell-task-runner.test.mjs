import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { run } from "./openshell-task-runner.ts";

describe("openshell-task-runner fail-closed stub", () => {
  it("always returns a terminal failed result with openshell_runner_unimplemented", async () => {
    const out = await run();
    assert.equal(out.status, "failed");
    assert.equal(out.exitCode, 1);
    assert.equal(out.errorClass, "openshell_runner_unimplemented");
    assert.equal(out.errorMessage, "OpenShell task runner is not implemented");
    assert.equal(out.runtimeMetadata.task_runner, "openshell-task-runner");
  });

  it("never returns a completed status (no fake completion path)", async () => {
    const out = await run();
    assert.notEqual(out.status, "completed");
    assert.ok(!JSON.stringify(out).includes("Completed by the built-in openshell task runner"));
  });
});
