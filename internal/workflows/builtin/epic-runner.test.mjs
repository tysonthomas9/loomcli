import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { after, before, describe, it } from "node:test";

const here = path.dirname(fileURLToPath(import.meta.url));
const SOURCE = path.join(here, "epic-runner.ts");

let stageRoot;
let mod;

function stub(dir, relFile, contents = "export default {};\n") {
  const file = path.join(dir, relFile);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, contents);
}

before(async () => {
  stageRoot = fs.mkdtempSync(path.join(os.tmpdir(), "loom-epic-runner-stage-"));
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

  const copy = path.join(stageRoot, "epic-runner.ts");
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

describe("epic-runner stacked lineage payload", () => {
  it("injects the projected lineage carrier for the claimed child task", () => {
    const input = mod.childTaskInput(
      { openPullRequest: true, repoUrl: "https://github.com/acme/widgets.git" },
      { driverRunId: "run-1" },
      { id: "T-B", sourceRepo: "acme/widgets" },
      {
        "T-B": {
          stackId: "epic:E",
          baseRef: "loom/stack/epic-E/T-A",
          outputBranch: "loom/stack/epic-E/T-B",
        },
      },
    );

    assert.equal(input.openPullRequest, true);
    assert.equal(input.repoUrl, "https://github.com/acme/widgets.git");
    assert.equal(input.driverRunId, "run-1");
    assert.equal(input.taskId, "T-B");
    assert.equal(input.sourceRepo, "acme/widgets");
    assert.deepEqual(input.lineage, {
      stackId: "epic:E",
      baseRef: "loom/stack/epic-E/T-A",
      outputBranch: "loom/stack/epic-E/T-B",
    });
  });

  it("preserves an explicit child lineage over the projected map", () => {
    const input = mod.childTaskInput(
      {
        lineage: {
          stackId: "manual",
          baseRef: "manual-base",
          outputBranch: "manual-head",
        },
      },
      { driverRunId: "run-1" },
      { id: "T-B" },
      {
        "T-B": {
          stackId: "epic:E",
          baseRef: "loom/stack/epic-E/T-A",
          outputBranch: "loom/stack/epic-E/T-B",
        },
      },
    );

    assert.deepEqual(input.lineage, {
      stackId: "manual",
      baseRef: "manual-base",
      outputBranch: "manual-head",
    });
  });
});
