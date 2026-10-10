import { test } from "node:test";
import assert from "node:assert/strict";
import { createFakeModel } from "./server.mjs";

async function start(t) {
  const server = createFakeModel();
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  t.after(() => server.close());
  const base = `http://127.0.0.1:${server.address().port}`;
  const post = (path, body) => fetch(base + path, { method: "POST", body: JSON.stringify(body) });
  const chat = async (messages) => {
    const text = await (await post("/v1/chat/completions", { model: "m", stream: true, messages })).text();
    const chunks = text.split("\n\n").filter((l) => l.startsWith("data: {")).map((l) => JSON.parse(l.slice(6)));
    return {
      content: chunks.map((c) => c.choices[0].delta.content ?? "").join(""),
      toolCalls: chunks.flatMap((c) => c.choices[0].delta.tool_calls ?? []),
      finish: chunks.at(-1).choices[0].finish_reason,
      done: text.trimEnd().endsWith("data: [DONE]"),
    };
  };
  return { base, post, chat };
}

test("scripted steps play in order, regardless of user text", async (t) => {
  const { post, chat } = await start(t);
  await post("/__script", { steps: [{ text: "hello" }, { bash: "rm -rf x" }, { tool_calls: [{ name: "read", arguments: { filePath: "a" } }] }] });
  const user = [{ role: "user", content: "please say bash and tool_calls" }];

  const a = await chat(user);
  assert.deepEqual([a.content, a.finish, a.done], ["hello", "stop", true]);

  const b = await chat(user);
  assert.equal(b.finish, "tool_calls");
  assert.equal(b.toolCalls[0].function.name, "shell");
  assert.deepEqual(JSON.parse(b.toolCalls[0].function.arguments), { command: "rm -rf x" });

  const c = await chat(user);
  assert.deepEqual([c.toolCalls[0].function.name, c.toolCalls[0].function.arguments], ["read", '{"filePath":"a"}']);
  assert.notEqual(c.toolCalls[0].id, b.toolCalls[0].id);

  assert.equal((await chat(user)).content, "ok");
});

test("next picks a step for a tool follow-up or a new prompt, in queue order", async (t) => {
  const { post, chat } = await start(t);
  await post("/__script", { steps: [{ next: "tool", text: "follow-up" }, { next: "prompt", text: "fresh" }, { text: "any" }] });
  const followUp = [{ role: "user", content: "x" }, { role: "assistant", tool_calls: [] }, { role: "tool", content: "out" }];
  assert.equal((await chat([{ role: "user", content: "x" }])).content, "fresh");
  assert.equal((await chat(followUp)).content, "follow-up");
  assert.equal((await chat(followUp)).content, "any");
  assert.equal((await chat(followUp)).content, "ok");
});

test("title requests take no step; requests are logged and reset clears all", async (t) => {
  const { base, post, chat } = await start(t);
  await post("/__script", { steps: [{ text: "scripted" }] });
  assert.equal((await chat([{ role: "system", content: "You are a title generator." }, { role: "user", content: "x" }])).content, "Title");
  assert.equal((await chat([{ role: "user", content: "x" }])).content, "scripted");

  const log = await (await fetch(base + "/__requests")).json();
  assert.equal(log.requests.length, 2);
  assert.equal(log.queued, 0);

  await post("/__script", { steps: [{ text: "left over" }] });
  await post("/__reset", {});
  assert.deepEqual(await (await fetch(base + "/__requests")).json(), { requests: [], queued: 0 });
  assert.equal((await chat([{ role: "user", content: "x" }])).content, "ok");
});

test("bad script bodies are refused", async (t) => {
  const { post } = await start(t);
  assert.equal((await post("/__script", { steps: "nope" })).status, 400);
  assert.equal((await post("/__script", "{")).status, 400);
});

test("an error step answers 400 with an OpenAI error", async (t) => {
  const { base, post } = await start(t);
  await post("/__script", { steps: [{ error: "model refused" }] });
  const res = await post("/v1/chat/completions", { model: "m", stream: true, messages: [{ role: "user", content: "x" }] });
  assert.equal(res.status, 400);
  assert.equal((await res.json()).error.message, "model refused");
  assert.equal((await (await fetch(base + "/__requests")).json()).queued, 0);
});

test("a gated step waits until its gate opens; reset answers what still waits", async (t) => {
  const { base, post, chat } = await start(t);
  const held = async () => (await (await fetch(base + "/__held")).json()).held;
  const until = async (n) => {
    for (let i = 0; i < 200; i++) if ((await held()) >= n) return;
    assert.fail(`held stayed at ${await held()}; want ${n}`);
  };
  await post("/__script", { steps: [{ gate: "g", text: "late" }, { gate: "g", text: "later" }, { gate: "h", text: "never" }] });
  const user = [{ role: "user", content: "x" }];
  let done = 0;
  const a = chat(user).then((r) => (done++, r));
  const b = chat(user).then((r) => (done++, r));
  await until(2);
  await new Promise((r) => setTimeout(r, 100));
  assert.equal(done, 0);
  assert.deepEqual(await (await post("/__open", { gate: "g" })).json(), { released: 2 });
  assert.deepEqual([(await a).content, (await b).content].sort(), ["late", "later"]);
  assert.equal(await held(), 0);
  await post("/__script", { steps: [{ gate: "g", text: "open already" }] });
  const c = chat(user);
  await until(1);
  assert.equal((await chat(user)).content, "open already");
  await post("/__reset", {});
  assert.equal((await c).content, "never");
  assert.equal(await held(), 0);
  assert.equal((await post("/__open", {})).status, 400);
  assert.equal((await post("/__open", null)).status, 400);
  await post("/__script", { steps: [{ gate: "k", text: "gone" }] });
  const ac = new AbortController();
  const gone = fetch(base + "/v1/chat/completions", { method: "POST", body: JSON.stringify({ messages: user }), signal: ac.signal }).catch(() => "aborted");
  await until(1);
  ac.abort();
  assert.equal(await gone, "aborted");
  for (let i = 0; i < 200 && (await held()) > 0; i++);
  assert.equal(await held(), 0);
  assert.deepEqual(await (await post("/__open", { gate: "k" })).json(), { released: 0 });
});
