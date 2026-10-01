import { createServer } from "node:http";

const pulls = [];
const requests = [];

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
  if (list && request.method === "GET") return send(response, 200, pulls);
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
      head: { ref: body.head },
      base: { ref: body.base },
    };
    pulls.push(pull);
    return send(response, 201, pull);
  }
  const one = path.match(/^\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/);
  if (one) {
    const pull = pulls.find((item) => item.number === Number(one[3]));
    if (!pull) return send(response, 404, { message: "pull not found" });
    if (request.method === "GET") return send(response, 200, pull);
    if (request.method === "PATCH") {
      if (body.base) pull.base.ref = body.base;
      if (body.body !== undefined) pull.body = body.body;
      if (body.state) pull.state = body.state;
      return send(response, 200, pull);
    }
  }
  const associated = path.match(/^\/repos\/([^/]+)\/([^/]+)\/commits\/([^/]+)\/pulls$/);
  if (associated && request.method === "GET") {
    return send(response, 200, pulls.filter((item) => item.merge_commit_sha === associated[3]));
  }
  return send(response, 404, { message: "not found", path });
});

server.listen(0, "127.0.0.1", () => {
  process.stdout.write(`fake-github listening ${server.address().port}\n`);
});
