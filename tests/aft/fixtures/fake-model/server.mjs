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
// A step with gate: "name" answers only once that gate is opened, so a turn
// stays mid model call (for example across a loom serve restart) until the
// test lets it go.
// A step with next: "tool" answers only a request whose last message is a
// tool result (a turn's follow-up after its tool call), and next: "prompt"
// only one whose last message is not (a new prompt); a request takes the
// first step it may. So a lead's follow-up and its child's first prompt,
// which race for the shared queue, each take their own step. With no step
// it may take, a turn gets the text "ok". OpenCode's title requests
// (system prompt "You are a title generator") get "Title" and take no step.
//
// Control plane:
//   POST /__script    {steps: [...]} appends steps to the queue
//   GET  /__requests  every chat request body received, in order
//   GET  /__held      how many replies wait on a gate, and the oldest one's age (oldest_ms)
//   POST /__open      {gate: "name"} opens the gate: its waiting and later replies go
//   POST /__reset     clear the queue, the request log and the open gates, and
//                     answer every reply still waiting on a gate
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";

export function createFakeModel() {
  let steps = [];
  let requests = [];
  let calls = 0;
  let open = new Set();
  let held = []; // {gate, at, answer} for each reply waiting on a gate

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
      if (req.method === "POST" && path === "/__open") {
        let body;
        try { body = JSON.parse(raw); } catch { return json(400, { error: "invalid JSON" }); }
        if (typeof body?.gate !== "string" || !body.gate) return json(400, { error: "gate must be a name" });
        open.add(body.gate);
        const go = held.filter((h) => h.gate === body.gate);
        held = held.filter((h) => h.gate !== body.gate);
        go.forEach((h) => h.answer());
        return json(200, { released: go.length });
      }
      if (req.method === "GET" && path === "/__requests") return json(200, { requests, queued: steps.length });
      if (req.method === "GET" && path === "/__held") return json(200, { held: held.length, oldest_ms: held.length ? Date.now() - Math.min(...held.map((h) => h.at)) : 0 });
      if (req.method === "POST" && path === "/__reset") {
        const go = held;
        steps = []; requests = []; open = new Set(); held = [];
        go.forEach((h) => h.answer());
        return json(200, { ok: true });
      }
      if (req.method === "POST" && path.endsWith("/chat/completions")) {
        let body = {};
        try { body = JSON.parse(raw); } catch { return json(400, { error: "invalid JSON" }); }
        requests.push(body);
        const system = (body.messages ?? []).filter((m) => m.role === "system").map((m) => JSON.stringify(m.content)).join("\n");
        if (system.includes("You are a title generator")) return reply(res, { text: "Title" });
        const kind = (body.messages ?? []).at(-1)?.role === "tool" ? "tool" : "prompt";
        const i = steps.findIndex((s) => !s.next || s.next === kind);
        const step = i < 0 ? { text: "ok" } : steps.splice(i, 1)[0];
        if (step.gate !== undefined && !open.has(step.gate)) {
          // A caller that gave up (its client timed out) no longer waits.
          const h = { gate: step.gate, at: Date.now(), answer: () => reply(res, step) };
          held.push(h);
          return void res.on("close", () => { held = held.filter((x) => x !== h); });
        }
        return reply(res, step);
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
