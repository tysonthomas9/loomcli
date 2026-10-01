import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";

const pulls = [];
const requests = [];
const stacks = [];
const merges = new Map();
let remote = "";
let nativeStacks = false;

function branchSha(ref) {
  if (!remote) return "";
  try {
    return execFileSync("git", [`--git-dir=${remote}`, "rev-parse", `refs/heads/${ref}`], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

function currentPull(pull) {
  pull.head.sha = branchSha(pull.head.ref) || pull.head.sha || "";
  pull.base.sha = branchSha(pull.base.ref) || pull.base.sha || "";
  return pull;
}

function stackView(stack) {
  return { number: stack.number, base: { ref: "main" }, open: true, pull_requests: stack.numbers.map((number) => currentPull(pulls.find((pull) => pull.number === number))) };
}

function send(response, status, value) {
  response.writeHead(status, { "Content-Type": "application/json" });
  response.end(JSON.stringify(value));
}

async function bodyOf(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  const body = Buffer.concat(chunks).toString("utf8");
  return body ? JSON.parse(body) : {};
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url, "http://127.0.0.1");
  const body = await bodyOf(request);
  const path = url.pathname;

  if (path === "/__requests" && request.method === "GET") return send(response, 200, requests);
  if (path === "/__pulls" && request.method === "GET") return send(response, 200, pulls);
  if (path === "/__reset" && request.method === "POST") {
    pulls.length = 0;
    requests.length = 0;
    stacks.length = 0;
    merges.clear();
    remote = body.remote || "";
    nativeStacks = body.native_stacks === true;
    return send(response, 200, { ok: true });
  }
  if (path === "/__merge" && request.method === "POST") {
    const pull = pulls.find((item) => item.number === body.number);
    if (!pull || pull.state !== "open" || !body.sha) return send(response, 409, { message: "open pull and merge sha required" });
    pull.state = "closed";
    pull.merged_at = new Date().toISOString();
    pull.merge_commit_sha = body.sha;
    return send(response, 200, pull);
  }

  requests.push({ method: request.method, path: url.pathname, body });
  const list = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls$/);
  if (list && request.method === "GET") return send(response, 200, pulls.map(currentPull));
  if (list && request.method === "POST") {
    if (!body.head || !body.base) return send(response, 422, { message: "head and base required" });
    if (pulls.some((item) => item.head.ref === body.head && item.state === "open")) return send(response, 422, { message: "pull already exists" });
    const pull = {
      number: pulls.length + 1,
      state: "open",
      title: body.title || "",
      body: body.body || "",
      html_url: `http://fake-github.local/pulls/${pulls.length + 1}`,
      merged_at: null,
      merge_commit_sha: "",
      head: { ref: body.head, sha: branchSha(body.head) },
      base: { ref: body.base, sha: branchSha(body.base) },
    };
    pulls.push(pull);
    return send(response, 201, pull);
  }
  const one = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/);
  if (one) {
    const pull = pulls.find((item) => item.number === Number(one[3]));
    if (!pull) return send(response, 404, { message: "pull not found" });
    if (request.method === "GET") return send(response, 200, currentPull(pull));
    if (request.method === "PATCH") {
      if (body.base) pull.base.ref = body.base;
      if (body.body !== undefined) pull.body = body.body;
      if (body.state) pull.state = body.state;
      return send(response, 200, pull);
    }
  }
  const stackList = path.match(/^\/repos\/([^/]+)\/([^/]+)\/stacks$/);
  if (stackList && nativeStacks) {
    if (request.method === "GET") {
      const selected = url.searchParams.get("pull_request");
      return send(response, 200, stacks.filter((stack) => !selected || stack.numbers.includes(Number(selected))).map(stackView));
    }
    if (request.method === "POST") {
      if (!validStack(body.pull_requests)) return send(response, 422, { message: "pull requests cannot form a stack" });
      const stack = { number: stacks.length + 1, numbers: [...body.pull_requests] };
      stacks.push(stack);
      return send(response, 201, stackView(stack));
    }
  }
  const stackAdd = path.match(/^\/repos\/([^/]+)\/([^/]+)\/stacks\/(\d+)\/add$/);
  if (stackAdd && nativeStacks && request.method === "POST") {
    const stack = stacks.find((item) => item.number === Number(stackAdd[3]));
    if (!stack || !validStack([...stack.numbers, ...body.pull_requests])) return send(response, 422, { message: "pull requests cannot form a stack" });
    stack.numbers.push(...body.pull_requests);
    return send(response, 200, stackView(stack));
  }
  const mergeRequest = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/merge-async$/);
  if (mergeRequest && request.method === "PUT") {
    const pull = pulls.find((item) => item.number === Number(mergeRequest[3]));
    if (!pull || pull.state !== "open") return send(response, 400, { status: "failed", details: { message: "pull request is not open" } });
    if (body.sha !== currentPull(pull).head.sha || body.bypass_rules === true) return send(response, 422, { message: "head changed or rules bypass requested" });
    const existing = [...merges.values()].find((item) => item.number === pull.number && item.status === "pending");
    if (existing) return send(response, 409, { status: "pending", details: existing.details });
    const uuid = randomUUID();
    const details = { uuid, expected_head_sha: body.sha, merge_action: body.merge_action || "default", bypass_rules: false };
    merges.set(uuid, { number: pull.number, status: "pending", details });
    return send(response, 202, { status: "pending", details });
  }
  const mergeResult = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/merge-async\/([^/]+)$/);
  if (mergeResult && request.method === "GET") {
    const merge = merges.get(mergeResult[4]);
    if (!merge || merge.number !== Number(mergeResult[3])) return send(response, 404, { message: "merge request not found" });
    if (merge.status === "pending") {
      const stack = stacks.find((item) => item.numbers.includes(merge.number));
      const numbers = stack ? stack.numbers.slice(0, stack.numbers.indexOf(merge.number) + 1) : [merge.number];
      const target = pulls.find((item) => item.number === merge.number);
      const sha = currentPull(target).head.sha;
      if (remote) execFileSync("git", [`--git-dir=${remote}`, "update-ref", "refs/heads/main", sha]);
      for (const number of numbers) {
        const pull = pulls.find((item) => item.number === number);
        pull.state = "closed";
        pull.merged_at = new Date().toISOString();
        pull.merge_commit_sha = sha;
      }
      const successor = stack && pulls.find((item) => item.number === stack.numbers[numbers.length]);
      if (successor) successor.base.ref = "main";
      merge.status = "merged";
      merge.details = { sha };
    }
    return send(response, 200, { status: merge.status, details: merge.details });
  }
  if (path === "/graphql" && request.method === "POST") {
    return send(response, 200, { data: { repository: { pullRequests: {
      nodes: pulls.filter((pull) => pull.state === "open").map((pull) => ({
        number: pull.number, headRefName: pull.head.ref, mergeable: "MERGEABLE",
        reviewDecision: "APPROVED", mergeQueueEntry: null,
        commits: { nodes: [{ commit: { statusCheckRollup: { state: "SUCCESS" } } }] },
      })),
      pageInfo: { hasNextPage: false, endCursor: null },
    } } } });
  }
  if (/^\/repos\/[^/]+\/[^/]+\/commits\/[^/]+\/check-runs$/.test(path) && request.method === "GET") {
    return send(response, 200, { check_runs: [] });
  }
  if (/^\/repos\/[^/]+\/[^/]+\/commits\/[^/]+\/status$/.test(path) && request.method === "GET") {
    return send(response, 200, { statuses: [] });
  }
  const deletedRef = path.match(/^\/repos\/[^/]+\/[^/]+\/git\/refs\/heads\/(.+)$/);
  if (deletedRef && request.method === "DELETE") {
    if (remote) {
      try { execFileSync("git", [`--git-dir=${remote}`, "update-ref", "-d", `refs/heads/${decodeURIComponent(deletedRef[1])}`]); } catch { return send(response, 404, { message: "ref not found" }); }
    }
    return send(response, 204, "");
  }
  const associated = path.match(/^\/repos\/([^/]+)\/([^/]+)\/commits\/([^/]+)\/pulls$/);
  if (associated && request.method === "GET") {
    return send(response, 200, pulls.filter((item) => item.merge_commit_sha === associated[3]));
  }
  return send(response, 404, { message: "not found", path });
});

function validStack(numbers) {
  return Array.isArray(numbers) && numbers.length > 0 && numbers.every((number, index) => {
    const pull = pulls.find((item) => item.number === number);
    const previous = pulls.find((item) => item.number === numbers[index - 1]);
    return pull && pull.state === "open" && pull.base.ref === (previous ? previous.head.ref : "main");
  });
}

server.listen(0, "127.0.0.1", () => {
  process.stdout.write(`fake-github listening ${server.address().port}\n`);
});
