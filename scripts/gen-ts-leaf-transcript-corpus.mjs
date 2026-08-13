#!/usr/bin/env node
// Regenerates internal/modules/artifacts/transcript/testdata/ts_leaf_corpus.json — the
// fixture the Phase-U/U0 conformance test (transcript.conformance_test.go) uses to
// pin the TypeScript local-task-runner leaf's output to the canonical Go
// transcript.Event schema.
//
// It runs the REAL parser (internal/infra/workflowdistribution/builtin/local-task-runner.ts
// parseStreamJSONTranscript) over representative claude/codex/cursor stream-json so
// the fixture is actual leaf output, not a hand-authored guess. Re-run whenever the
// parser's emitted shape changes:
//
//   node scripts/gen-ts-leaf-transcript-corpus.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseStreamJSONTranscript } from "../internal/infra/workflowdistribution/builtin/local-task-runner.ts";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.join(here, "../internal/modules/artifacts/transcript/testdata/ts_leaf_corpus.json");

// claude covers session_meta + reasoning(thinking) + text + tool_use + tool_result + result(usage+cost).
const claude = [
  JSON.stringify({ type: "assistant", message: { content: [
    { type: "thinking", thinking: "let me plan the change" },
    { type: "text", text: "Working on it." },
    { type: "tool_use", id: "t1", name: "Read", input: { file_path: "hello.md" } },
  ] } }),
  JSON.stringify({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "1\thello\n" }] } }),
  JSON.stringify({ type: "result", is_error: false, total_cost_usd: 0.18, num_turns: 3, usage: { input_tokens: 8000, output_tokens: 300, cache_read_input_tokens: 12, cache_creation_input_tokens: 7 } }),
].join("\n") + "\n";

// codex covers the item.completed shapes (reasoning / agent_message / command_execution) + turn.completed usage.
const codex = [
  JSON.stringify({ type: "item.completed", item: { type: "reasoning", text: "thinking about it" } }),
  JSON.stringify({ type: "item.completed", item: { type: "agent_message", text: "done" } }),
  JSON.stringify({ type: "item.completed", item: { type: "command_execution", command: "ls", aggregated_output: "a\n", exit_code: 0 } }),
  JSON.stringify({ type: "turn.completed", usage: { input_tokens: 123, output_tokens: 45, cached_input_tokens: 6, reasoning_output_tokens: 3 } }),
].join("\n") + "\n";

// cursor covers the user role (assistant/tool/system come from the others).
const cursor = [
  JSON.stringify({ type: "user", message: { content: [{ type: "text", text: "please do X" }] } }),
  JSON.stringify({ type: "assistant", message: { content: [{ type: "text", text: "ok" }] } }),
  JSON.stringify({ type: "result", is_error: false, usage: { inputTokens: 10, outputTokens: 2 } }),
].join("\n") + "\n";

const corpus = {
  claude: parseStreamJSONTranscript("claude", claude),
  codex: parseStreamJSONTranscript("codex", codex),
  cursor: parseStreamJSONTranscript("cursor", cursor),
};
fs.writeFileSync(out, JSON.stringify(corpus, null, 2) + "\n");
const all = [...corpus.claude, ...corpus.codex, ...corpus.cursor];
console.log(`wrote ${out}: ${all.length} entries, types=[${[...new Set(all.map((e) => e.type))].sort().join(",")}], roles=[${[...new Set(all.map((e) => e.role))].sort().join(",")}]`);
