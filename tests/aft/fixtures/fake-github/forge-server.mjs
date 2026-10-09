import { createServer } from "node:http";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";

const pulls = [];
const requests = [];
const stacks = [];
const merges = new Map();
let remote = "";
let nativeStacks = false;
const repoNativeStacks = new Map();
// Repos registered with content_checks mirror the real-GitHub sandbox's required
// Actions check "check": a commit whose tree has a file containing FAIL is red.
const contentChecks = new Set();
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
  return pull.repo === `${owner}/${repo}`;
}

function contentFails(key, sha) {
  if (!contentChecks.has(key) || !sha) return false;
  try {
    execFileSync("git", [`--git-dir=${remoteFor(key)}`, "grep", "-q", "FAIL", sha, "--"]);
    return true;
  } catch {
    return false;
  }
}

function supportsStacks(owner, repo) {
  return repoNativeStacks.get(`${owner}/${repo}`) ?? nativeStacks;
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
  if (path === "/__pulls" && request.method === "GET") return send(response, 200, pulls.filter((pull) => (!url.searchParams.has("workspace") || pull.head.ref.includes(`/ws/${url.searchParams.get("workspace")}/`)) && (!url.searchParams.has("repo") || pull.repo === url.searchParams.get("repo"))).map(currentPull));
  // Fixture registration never resets another journey's remote or evidence.
  if (path === "/__register" && request.method === "POST") {
    if (!/^owner\/[a-zA-Z0-9_-]+$/.test(body.repo || "") || !body.remote?.startsWith("/")) return send(response, 422, { message: "fixture repo and absolute bare remote required" });
    if (remotes[body.repo]) return send(response, 409, { message: "fixture repo already registered" });
    remotes[body.repo] = body.remote;
    repoNativeStacks.set(body.repo, body.native_stacks === true);
    if (body.content_checks === true) contentChecks.add(body.repo);
    return send(response, 201, { repo: body.repo });
  }
  if (path === "/__unregister" && request.method === "POST") {
    if (pulls.some((pull) => pull.repo === body.repo && pull.state === "open")) return send(response, 409, { message: "open product PRs remain; retain fixture for inspection" });
    delete remotes[body.repo];
    repoNativeStacks.delete(body.repo);
    contentChecks.delete(body.repo);
    return send(response, 200, { repo: body.repo });
  }
  if (path === "/__reset" && request.method === "POST") {
    if (!body.preserve) pulls.length = 0;
    requests.length = 0;
    if (!body.preserve) {
      stacks.length = 0;
      merges.clear();
      repoNativeStacks.clear();
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
    if (pulls.some((item) => item.repo === `${list[1]}/${list[2]}` && item.head.ref === body.head && item.state === "open")) return send(response, 422, { message: "pull already exists" });
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
    if (!pull || !inRepo(pull, one[1], one[2])) return send(response, 404, { message: "pull not found" });
    if (request.method === "GET") return send(response, 200, currentPull(pull));
    if (request.method === "PATCH") {
      if (body.base) pull.base.ref = body.base;
      if (body.body !== undefined) pull.body = body.body;
      if (body.state) pull.state = body.state;
      return send(response, 200, pull);
    }
  }
  const stackList = path.match(/^\/repos\/([^/]+)\/([^/]+)\/stacks$/);
  if (stackList && supportsStacks(stackList[1], stackList[2])) {
    if (request.method === "GET") {
      const selected = url.searchParams.get("pull_request");
      return send(response, 200, stacks.filter((stack) => stack.repo === `${stackList[1]}/${stackList[2]}` && (!selected || stack.numbers.includes(Number(selected)))).map(stackView));
    }
    if (request.method === "POST") {
      if (!validStack(body.pull_requests)) return send(response, 422, { message: "pull requests cannot form a stack" });
      const stack = { repo: `${stackList[1]}/${stackList[2]}`, number: stacks.length + 1, numbers: [...body.pull_requests] };
      stacks.push(stack);
      return send(response, 201, stackView(stack));
    }
  }
  const stackAdd = path.match(/^\/repos\/([^/]+)\/([^/]+)\/stacks\/(\d+)\/add$/);
  if (stackAdd && supportsStacks(stackAdd[1], stackAdd[2]) && request.method === "POST") {
    const stack = stacks.find((item) => item.number === Number(stackAdd[3]));
    if (!stack || stack.repo !== `${stackAdd[1]}/${stackAdd[2]}` || !validStack([...stack.numbers, ...body.pull_requests])) return send(response, 422, { message: "pull requests cannot form a stack" });
    stack.numbers.push(...body.pull_requests);
    return send(response, 200, stackView(stack));
  }
  const mergeRequest = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)\/merge-async$/);
  if (mergeRequest && request.method === "PUT") {
    const pull = pulls.find((item) => item.number === Number(mergeRequest[3]));
    if (!pull || !inRepo(pull, mergeRequest[1], mergeRequest[2]) || pull.state !== "open") return send(response, 400, { status: "failed", details: { message: "pull request is not open" } });
    if (body.sha !== currentPull(pull).head.sha || body.bypass_rules === true) return send(response, 422, { message: "head changed or rules bypass requested" });
    if (contentFails(pull.repo, body.sha)) return send(response, 405, { message: 'Required status check "check" is failing.' });
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
    if (!merge || merge.number !== Number(mergeResult[3]) || !inRepo(pulls.find((item) => item.number === merge.number), mergeResult[1], mergeResult[2])) return send(response, 404, { message: "merge request not found" });
    if (merge.status === "pending") {
      const stack = stacks.find((item) => item.numbers.includes(merge.number));
      const numbers = stack ? stack.numbers.slice(0, stack.numbers.indexOf(merge.number) + 1) : [merge.number];
      const target = pulls.find((item) => item.number === merge.number);
      const sha = currentPull(target).head.sha;
      // land_sha (set through /__pr_status) lands the PR with another trunk
      // commit on top of it, as a merge queue would; it must descend from sha.
      const landed = (prStatus.get(target.number) || {}).land_sha || sha;
      if (remoteFor(target.repo)) {
        try {
          if (landed !== sha) execFileSync("git", [`--git-dir=${remoteFor(target.repo)}`, "merge-base", "--is-ancestor", sha, landed]);
          execFileSync("git", [`--git-dir=${remoteFor(target.repo)}`, "update-ref", "refs/heads/main", landed]);
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
        const fails = contentFails(pull.repo, currentPull(pull).head.sha);
        const override = { ...(contentChecks.has(pull.repo) ? { checks: fails ? "FAILURE" : "SUCCESS", merge_state: fails ? "BLOCKED" : "CLEAN" } : {}), ...(prStatus.get(pull.number) || {}) };
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
  const checkRuns = path.match(/^\/repos\/([^/]+)\/([^/]+)\/commits\/([^/]+)\/check-runs$/);
  if (checkRuns && request.method === "GET") {
    const key = `${checkRuns[1]}/${checkRuns[2]}`;
    if (contentChecks.has(key)) return send(response, 200, { check_runs: [{ name: "check", status: "completed", conclusion: contentFails(key, decodeURIComponent(checkRuns[3])) ? "failure" : "success" }] });
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
    return pull && pull.state === "open" && (!previous || pull.repo === previous.repo) && pull.base.ref === (previous ? previous.head.ref : "main");
  });
}

server.listen(0, "127.0.0.1", () => {
  process.stdout.write(`fake-github listening ${server.address().port}\n`);
});
