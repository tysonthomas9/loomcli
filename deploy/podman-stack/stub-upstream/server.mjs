// Stub upstream for connector egress / webhook delivery tests.
//
// Behavior:
//   GET  /healthz       -> 200 "ok" (compose healthcheck)
//   GET  /__requests    -> JSON log of every recorded request (newest last)
//   POST /__reset       -> clears the log
//   anything else       -> recorded + echoed back as JSON (status 200, or
//                          STUB_FORCE_STATUS to exercise retry paths)
//
// If STUB_WEBHOOK_SECRET is set, each recorded entry notes whether the
// request carried it (Authorization: Bearer <secret> or X-Webhook-Secret),
// so tests can assert the vault-held credential actually reached the wire.
// The secret VALUE is never included in responses or logs.
//
// Memory is bounded: bodies are capped at 1 MiB, the log at
// STUB_MAX_REQUESTS entries (default 200, oldest evicted).

import http from "node:http";

const PORT = Number(process.env.PORT || 8080);
const MAX_REQUESTS = Number(process.env.STUB_MAX_REQUESTS || 200);
const MAX_BODY_BYTES = 1 << 20;
const FORCE_STATUS = Number(process.env.STUB_FORCE_STATUS || 0);
const SECRET = process.env.STUB_WEBHOOK_SECRET || "";

const requests = [];

function secretPresented(req) {
  if (!SECRET) return null;
  const auth = req.headers["authorization"] || "";
  const hdr = req.headers["x-webhook-secret"] || "";
  return auth === `Bearer ${SECRET}` || hdr === SECRET;
}

function redactedHeaders(headers) {
  const out = {};
  for (const [k, v] of Object.entries(headers)) {
    out[k] = k === "authorization" || k === "x-webhook-secret" ? "<redacted>" : v;
  }
  return out;
}

const server = http.createServer((req, res) => {
  const chunks = [];
  let size = 0;
  let truncated = false;
  req.on("data", (c) => {
    size += c.length;
    if (size <= MAX_BODY_BYTES) chunks.push(c);
    else truncated = true;
  });
  req.on("end", () => {
    const url = req.url || "/";
    if (req.method === "GET" && url === "/healthz") {
      res.writeHead(200, { "content-type": "text/plain" });
      res.end("ok");
      return;
    }
    if (req.method === "GET" && url === "/__requests") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ count: requests.length, requests }));
      return;
    }
    if (req.method === "POST" && url === "/__reset") {
      requests.length = 0;
      res.writeHead(204);
      res.end();
      return;
    }
    const entry = {
      ts: new Date().toISOString(),
      method: req.method,
      url,
      headers: redactedHeaders(req.headers),
      body: Buffer.concat(chunks).toString("utf8"),
      bodyTruncated: truncated,
      secretPresented: secretPresented(req),
    };
    requests.push(entry);
    while (requests.length > MAX_REQUESTS) requests.shift();
    const status = FORCE_STATUS >= 100 && FORCE_STATUS <= 599 ? FORCE_STATUS : 200;
    res.writeHead(status, { "content-type": "application/json" });
    res.end(JSON.stringify({ ok: status < 400, recorded: requests.length, echo: { method: req.method, url } }));
  });
  req.on("error", () => {
    res.writeHead(400);
    res.end();
  });
});

server.listen(PORT, "0.0.0.0", () => {
  console.log(`stub-upstream listening on :${PORT} (max ${MAX_REQUESTS} recorded requests)`);
});

for (const sig of ["SIGTERM", "SIGINT"]) {
  process.on(sig, () => {
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 2000).unref();
  });
}
