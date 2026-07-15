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

describe("review-loop task-run attribution", () => {
  it("uses the local Loom issue ID so session and diff routes remain addressable", async () => {
    let requested;
    const loom = {
      connectors: {
        github: {
          async readPullRequest() {
            return { body: { state: "open", headSha: "abc123", baseRef: "main" } };
          },
          async compare() {
            return { body: { diff: "diff --git a/a b/a" } };
          },
          async postReview() {
            return { body: { htmlUrl: "https://github.test/review/1" } };
          },
        },
      },
      taskRuns: {
        async request(input) {
          requested = input;
        },
        async await() {
          return { status: "completed", runtime_metadata: { findings: { summary: "Looks good", comments: [] } } };
        },
      },
    };
    const subject = { owner: "acme", name: "widgets", repo: "acme/widgets", prNumber: 123, slug: "acme/widgets#123" };

    const result = await mod.reviewPullRequest(loom, "github", subject, 1, "LOCALMODE-42");

    assert.equal(result.ok, true);
    assert.equal(requested.taskId, "LOCALMODE-42");
    assert.equal(requested.input.repo, "acme/widgets");
    assert.equal(requested.input.prNumber, 123);
    assert.equal(requested.taskRunId, "review-acme_widgets-123-c1");
  });
});
