// OpenAI-compatible streaming chat model for AFT runs against real OpenCode.
//
// Replies come only from a script set out of band; user text never selects a
// scenario. Each agent turn request takes the next step from the queue:
//   {text: "..."}                          stream an assistant message
//   {tool_calls: [{name, arguments}]}      stream tool calls (arguments: object)
//   {bash: "cmd"}                          a call to OpenCode 2.x's shell tool; under
//                                          a bash "ask" rule OpenCode stops for approval
//   {error: "message"}                     answer HTTP 400 with an OpenAI error, so the
//                                          turn fails with that message
// With the queue empty a turn gets the text "ok". OpenCode's title requests
// (system prompt "You are a title generator") get "Title" and take no step.
//
// Control plane:
//   POST /__script    {steps: [...]} appends steps to the queue
//   GET  /__requests  every chat request body received, in order
//   POST /__reset     clear the queue and the request log
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";

export function createFakeModel() {
  let steps = [];
  let requests = [];
  let calls = 0;

  function stream(res, deltas, finish) {
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    const chunk = (delta, finish_reason = null) =>
      res.write(`data: ${JSON.stringify({ id: "c1", object: "chat.completion.chunk", created: 1, model: "m",
        choices: [{ index: 0, delta, finish_reason }] })}\n\n`);
    chunk({ role: "assistant" });
    for (const d of deltas) chunk(d);
    chunk({}, finish);
    res.end("data: [DONE]\n\n");
  }

  function reply(res, step) {
    if (step.error !== undefined) {
      res.writeHead(400, { "Content-Type": "application/json" });
      return res.end(JSON.stringify({ error: { message: String(step.error), type: "invalid_request_error", code: null } }));
    }
    if (step.bash !== undefined) {
      step = { tool_calls: [{ name: "shell", arguments: { command: step.bash } }] };
    }
    if (step.tool_calls) {
      const deltas = step.tool_calls.map((c, index) => ({ tool_calls: [{ index, id: `call_${++calls}`, type: "function",
        function: { name: c.name, arguments: JSON.stringify(c.arguments ?? {}) } }] }));
      return stream(res, deltas, "tool_calls");
    }
    return stream(res, [{ content: String(step.text ?? "") }], "stop");
  }

  return createServer((req, res) => {
    let raw = "";
    req.on("data", (c) => (raw += c));
    req.on("end", () => {
      const json = (status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(body)); };
      const path = req.url.split("?")[0];
      if (req.method === "POST" && path === "/__script") {
        let body;
        try { body = JSON.parse(raw); } catch { return json(400, { error: "invalid JSON" }); }
        if (!Array.isArray(body.steps)) return json(400, { error: "steps must be an array" });
        steps.push(...body.steps);
        return json(200, { queued: steps.length });
      }
      if (req.method === "GET" && path === "/__requests") return json(200, { requests, queued: steps.length });
      if (req.method === "POST" && path === "/__reset") { steps = []; requests = []; return json(200, { ok: true }); }
      if (req.method === "POST" && path.endsWith("/chat/completions")) {
        let body = {};
        try { body = JSON.parse(raw); } catch { return json(400, { error: "invalid JSON" }); }
        requests.push(body);
        const system = (body.messages ?? []).filter((m) => m.role === "system").map((m) => JSON.stringify(m.content)).join("\n");
        if (system.includes("You are a title generator")) return reply(res, { text: "Title" });
        return reply(res, steps.shift() ?? { text: "ok" });
      }
      json(404, { error: `no route ${req.method} ${path}` });
    });
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const server = createFakeModel();
  server.listen(Number(process.env.FAKE_MODEL_PORT || 0), "127.0.0.1", () => {
    // The harness reads this line to learn the port when the port is 0.
    process.stdout.write(`fake-model listening ${server.address().port}\n`);
  });
}
