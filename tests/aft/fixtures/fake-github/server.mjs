// GitHub-shaped REST fixture for the live PR-review tier.
//
// The existing deploy/podman-stack/stub-upstream/server.mjs is a generic echo
// recorder: it answers every request with {ok, recorded, echo}, which the connector
// rejects as "missing head sha". This one answers in GitHub's shape, so
// pullRequestRead/compare actually parse.
//
// Field names are taken from the connector, not from memory:
//   pullSummary  (internal/connector/providers/github.go) reads
//     number, state, title, draft, merged_at|merged, user.login, updated_at,
//     head.sha, head.ref, base.sha, base.ref
//   compareFiles reads files[].{filename,status,additions,deletions,patch}
//
// Beyond the happy path it exposes a control plane so a test can force states the
// real API would only produce under rate limiting or drift:
//   GET  /__requests  every request, INCLUDING the query string (a reviewer test
//                     asserts state/per_page/page were sent), with auth redacted
//   POST /__reset     clear the log and restore fixture defaults
//   POST /__fixture   patch the PR fixture, or force {status, headers, body} on the
//                     next matching response
//
// Read-only proof depends on /__requests: LP-1 asserts NO mutating call was made.
// That is only meaningful because writes are recorded here even though they succeed.
import { createServer } from "node:http";

const PORT = Number(process.env.FAKE_GH_PORT || 0);

const DEFAULTS = () => ({
  number: 7,
  state: "open",
  title: "Add authorization check",
  draft: false,
  merged_at: null,
  user: { login: "octocat" },
  updated_at: "2026-01-01T00:00:00Z",
  head: { sha: "0000000000000000000000000000000000000001", ref: "feature/auth" },
  base: { sha: "0000000000000000000000000000000000000000", ref: "main" },
});

let pr = DEFAULTS();
let files = [];
let forced = null; // {status, headers, body}
const requests = [];

const REDACT = new Set(["authorization", "cookie", "x-github-token"]);

function record(req, bodyText) {
  const headers = {};
  for (const [k, v] of Object.entries(req.headers)) {
    headers[k] = REDACT.has(k.toLowerCase()) ? "<redacted>" : v;
  }
  // The full URL, not just the pathname: query params are part of the contract.
  requests.push({ method: req.method, url: req.url, headers, body: bodyText || "" });
}

function send(res, status, payload, headers = {}) {
  const body = typeof payload === "string" ? payload : JSON.stringify(payload);
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(body);
}

async function readBody(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

const server = createServer(async (req, res) => {
  const bodyText = await readBody(req);
  const url = new URL(req.url, "http://127.0.0.1");
  const path = url.pathname;

  if (path === "/__requests" && req.method === "GET") return send(res, 200, requests);
  if (path === "/__reset" && req.method === "POST") {
    requests.length = 0;
    pr = DEFAULTS();
    files = [];
    forced = null;
    return send(res, 200, { ok: true });
  }
  if (path === "/__fixture" && req.method === "POST") {
    const patch = bodyText ? JSON.parse(bodyText) : {};
    if (patch.pr) pr = { ...pr, ...patch.pr };
    if (patch.files) files = patch.files;
    forced = patch.force || null;
    return send(res, 200, { ok: true, pr, files: files.length });
  }

  record(req, bodyText);

  // Forced response wins, so a test can produce 403 + Retry-After, or a 200 whose
  // body omits head.sha, without needing the real API to misbehave.
  if (forced) {
    const { status = 200, headers = {}, body = {} } = forced;
    forced = null;
    return send(res, status, body, headers);
  }

  const pulls = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls$/);
  if (pulls && req.method === "GET") return send(res, 200, [pr]);

  const onePull = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/);
  if (onePull && req.method === "GET") {
    if (Number(onePull[3]) !== pr.number) return send(res, 404, { message: "Not Found" });
    return send(res, 200, pr);
  }

  const compare = path.match(/^\/repos\/([^/]+)\/([^/]+)\/compare\/(.+)$/);
  if (compare && req.method === "GET") {
    return send(res, 200, {
      status: "ahead",
      ahead_by: 1,
      behind_by: 0,
      total_commits: 1,
      files,
    });
  }

  const reviews = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/reviews$/);
  if (reviews && req.method === "POST") return send(res, 201, { id: 1, state: "COMMENTED" });

  const comments = path.match(/^\/repos\/([^/]+)\/([^/]+)\/issues\/(\d+)\/comments$/);
  if (comments && req.method === "POST") return send(res, 201, { id: 2 });

  return send(res, 404, { message: "Not Found", path });
});

server.listen(PORT, "127.0.0.1", () => {
  // The harness reads this line to learn the port when PORT=0.
  process.stdout.write(`fake-github listening ${server.address().port}\n`);
});
