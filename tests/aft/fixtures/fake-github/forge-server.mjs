import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";

const pulls = [];
const requests = [];
const stacks = [];
const merges = new Map();
let remote = "";
let nativeStacks = false;
// Multi-repo mode: one bare remote per owner/repo; pulls and GraphQL are per repo.
let remotes = {};
const prStatus = new Map();
const statuses = [];

function remoteFor(key) {
  return (key && remotes[key]) || remote;
}

function branchSha(ref, key) {
  const dir = remoteFor(key);
  if (!dir) return "";
  try {
    return execFileSync("git", [`--git-dir=${dir}`, "rev-parse", `refs/heads/${ref}`], { encoding: "utf8" }).trim();
  } catch {
    return "";
  }
}

function currentPull(pull) {
  pull.head.sha = branchSha(pull.head.ref, pull.repo) || pull.head.sha || "";
  pull.base.sha = branchSha(pull.base.ref, pull.repo) || pull.base.sha || "";
  return pull;
}

function inRepo(pull, owner, repo) {
  return Object.keys(remotes).length === 0 || pull.repo === `${owner}/${repo}`;
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
  if (path === "/__pulls" && request.method === "GET") return send(response, 200, pulls.filter((pull) => !url.searchParams.has("workspace") || pull.head.ref.includes(`/ws/${url.searchParams.get("workspace")}/`)));
  if (path === "/__reset" && request.method === "POST") {
    if (!body.preserve) pulls.length = 0;
    requests.length = 0;
    if (!body.preserve) {
      stacks.length = 0;
      merges.clear();
    }
    remote = body.remote || "";
    remotes = body.remotes || {};
    nativeStacks = body.native_stacks === true;
    if (!body.preserve) prStatus.clear();
    statuses.length = 0;
    return send(response, 200, { ok: true });
  }
  if (path === "/__pr_status" && request.method === "POST") {
    prStatus.set(body.number, { ...(prStatus.get(body.number) || {}), ...body });
    return send(response, 200, prStatus.get(body.number));
  }
  if (path === "/__statuses" && request.method === "GET") return send(response, 200, statuses.filter((item) => !url.searchParams.has("sha") || item.sha === url.searchParams.get("sha")));
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
  if (list && request.method === "GET") return send(response, 200, pulls.filter((pull) => inRepo(pull, list[1], list[2])).map(currentPull));
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
      repo: `${list[1]}/${list[2]}`,
      head: { ref: body.head, sha: branchSha(body.head, `${list[1]}/${list[2]}`) },
      base: { ref: body.base, sha: branchSha(body.base, `${list[1]}/${list[2]}`) },
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
      if (remoteFor(target.repo)) {
        try {
          execFileSync("git", [`--git-dir=${remoteFor(target.repo)}`, "update-ref", "refs/heads/main", sha]);
        } catch (error) {
          return send(response, 409, { message: `merge head ${sha} is not in ${remoteFor(target.repo)}: ${String(error.stderr || error.message).trim()}` });
        }
      }
      for (const number of numbers) {
        const pull = pulls.find((item) => item.number === number);
        pull.state = "closed";
        pull.merged_at = new Date().toISOString();
        pull.merge_commit_sha = currentPull(pull).head.sha;
      }
      const successor = stack && pulls.find((item) => item.number === stack.numbers[numbers.length]);
      if (successor) successor.base.ref = "main";
      merge.status = "merged";
      merge.details = { sha };
    }
    return send(response, 200, { status: merge.status, details: merge.details });
  }
  if (path === "/graphql" && request.method === "POST") {
    const vars = body.variables || {};
    return send(response, 200, { data: { repository: { pullRequests: {
      nodes: pulls.filter((pull) => pull.state === "open" && (!vars.owner || inRepo(pull, vars.owner, vars.repo))).map((pull) => {
        const override = prStatus.get(pull.number) || {};
        const node = {
          number: pull.number, headRefName: pull.head.ref, mergeable: "MERGEABLE",
          reviewDecision: override.review || "APPROVED", mergeQueueEntry: null,
          commits: { nodes: [{ commit: { statusCheckRollup: { state: override.checks || "SUCCESS" } } }] },
        };
        if (override.merge_state) node.mergeStateStatus = override.merge_state;
        return node;
      }),
      pageInfo: { hasNextPage: false, endCursor: null },
    } } } });
  }
  if (/^\/repos\/[^/]+\/[^/]+\/commits\/[^/]+\/check-runs$/.test(path) && request.method === "GET") {
    return send(response, 200, { check_runs: [] });
  }
  if (/^\/repos\/[^/]+\/[^/]+\/commits\/[^/]+\/status$/.test(path) && request.method === "GET") {
    return send(response, 200, { statuses: [] });
  }
  const commitStatus = path.match(/^\/repos\/([^/]+)\/([^/]+)\/statuses\/([^/]+)$/);
  if (commitStatus && request.method === "POST") {
    const posted = { repo: `${commitStatus[1]}/${commitStatus[2]}`, sha: commitStatus[3], state: body.state, context: body.context, description: body.description, at: new Date().toISOString() };
    statuses.push(posted);
    return send(response, 201, posted);
  }
  const deletedRef = path.match(/^\/repos\/[^/]+\/[^/]+\/git\/refs\/heads\/(.+)$/);
  if (deletedRef && request.method === "DELETE") {
    if (remoteFor(path.split("/").slice(2, 4).join("/"))) {
      try { execFileSync("git", [`--git-dir=${remoteFor(path.split("/").slice(2, 4).join("/"))}`, "update-ref", "-d", `refs/heads/${decodeURIComponent(deletedRef[1])}`]); } catch { return send(response, 404, { message: "ref not found" }); }
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
