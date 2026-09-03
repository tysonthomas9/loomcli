import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, afterEach, before, beforeEach, describe, it } from "node:test";

import { run } from "./github-review-task-runner.ts";

const ENV_KEYS = [
  "LOOM_TASK_RUN_API_URL", "LOOM_WORKSPACE", "LOOM_TASK_RUN_ID", "LOOM_TASK_ID",
  "LOOM_TASK_RUN_NODE_ID", "LOOM_TASK_RUN_LEASE_ID", "LOOM_TASK_RUN_LEASE_TOKEN",
  "LOOM_TASK_RUN_FENCING_TOKEN", "LOOM_TASK_RUN_REQUEST_JSON", "PATH", "FAKE_REVIEW_OUTPUT",
];

const FAKE_CODEX = `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
const output = args[args.indexOf("--output-last-message") + 1];
fs.writeFileSync(output, process.env.FAKE_REVIEW_OUTPUT || JSON.stringify({ summary: "looks good", comments: [] }));
process.stdout.write(JSON.stringify({ type: "turn.completed", usage: { input_tokens: 10, output_tokens: 5 } }) + "\\n");
`;

let apiURL;
let requests;
let tmp;
let saved;
let savedFetch;

before(() => {
  savedFetch = globalThis.fetch;
  globalThis.fetch = async (url, init = {}) => {
      const requestURL = new URL(url);
      const raw = String(init.body || "");
      let body = {};
      try {
        body = raw ? JSON.parse(raw) : {};
      } catch {
        // Transcript uploads are NDJSON rather than a single JSON object.
      }
      requests.push({ url: requestURL.pathname, body });
      const reply = (payload) => {
        return new Response(JSON.stringify(payload), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      };
      if (requestURL.pathname.endsWith("/session-open")) return reply({ sessionId: "review-session-1", attempt: 1 });
      if (requestURL.pathname.endsWith("/artifact-declare")) return reply({ artifactId: body.artifactId, durableStatus: "declared" });
      if (requestURL.pathname.includes("/artifacts/") && requestURL.pathname.endsWith("/content")) return reply({ artifactId: "transcript", durableStatus: "uploaded" });
      if (requestURL.pathname.endsWith("/artifact-finalize")) return reply({ artifactId: body.artifactId, durableStatus: "finalized" });
      if (requestURL.pathname.endsWith("/session-close")) return reply({ sessionId: body.sessionId, status: body.status });
      if (requestURL.pathname.endsWith("/heartbeat")) return reply({ status: "running" });
      return new Response("not found", { status: 404 });
  };
  apiURL = "http://task-run-api.test";
});

after(() => {
  globalThis.fetch = savedFetch;
});

beforeEach(() => {
  saved = Object.fromEntries(ENV_KEYS.map((key) => [key, process.env[key]]));
  requests = [];
  tmp = fs.mkdtempSync(path.join(os.tmpdir(), "loom-review-test-"));
  const bin = path.join(tmp, "bin");
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, "codex"), FAKE_CODEX, { mode: 0o755 });
  process.env.PATH = bin + path.delimiter + saved.PATH;
  process.env.LOOM_TASK_RUN_API_URL = apiURL;
  process.env.LOOM_WORKSPACE = "WS";
  process.env.LOOM_TASK_RUN_ID = "review-run-1";
  process.env.LOOM_TASK_ID = "review-task-1";
  process.env.LOOM_TASK_RUN_NODE_ID = "node-1";
  process.env.LOOM_TASK_RUN_LEASE_ID = "lease-1";
  process.env.LOOM_TASK_RUN_LEASE_TOKEN = "lease-token";
  process.env.LOOM_TASK_RUN_FENCING_TOKEN = "1";
  process.env.LOOM_TASK_RUN_REQUEST_JSON = JSON.stringify({
    input: { diff: "--- a/a.js\\n+++ b/a.js\\n", model: "gpt-test" },
  });
});

afterEach(() => {
  for (const key of ENV_KEYS) {
    if (saved[key] === undefined) delete process.env[key];
    else process.env[key] = saved[key];
  }
  fs.rmSync(tmp, { recursive: true, force: true });
});

describe("github-review-task-runner task-plane sessions", () => {
  it("opens review and closes it after recording successful domain outcome", async () => {
    const out = await run();

    assert.equal(out.status, "completed");
    const open = requests.find((request) => request.url.endsWith("/session-open"));
    assert.equal(open.body.invocationKey, "review");
    assert.equal(out.runtimeMetadata.review_session_id, "review-session-1");
    const close = requests.find((request) => request.url.endsWith("/session-close"));
    assert.equal(close.body.status, "completed");
    assert.match(close.body.summary, /review produced 0 comment/);
  });

  it("finalizes the deferred review session on codex_no_findings", async () => {
    process.env.FAKE_REVIEW_OUTPUT = "not JSON";

    const out = await run();

    assert.equal(out.errorClass, "codex_no_findings");
    const close = requests.find((request) => request.url.endsWith("/session-close"));
    assert.equal(close.body.status, "failed");
    assert.equal(close.body.metadata.error_class, "codex_no_findings");
  });
});
