import assert from "node:assert/strict";
import test from "node:test";

import {
  createTranscriptCollector,
  flueUsageToTaskUsage,
} from "./flue-event-transcript.mjs";

test("converts Flue events to canonical Loom transcript entries", () => {
  const collector = createTranscriptCollector();
  collector.push({
    type: "turn_request",
    timestamp: "2026-06-09T12:00:00Z",
    input: {
      messages: [
        { role: "system", content: "ignore" },
        { role: "user", content: [{ type: "text", text: "Implement TASK-1" }] },
      ],
    },
  });
  collector.push({ type: "text_delta", timestamp: "2026-06-09T12:00:01Z", text: "Working" });
  collector.push({ type: "tool_start", timestamp: "2026-06-09T12:00:02Z", toolName: "bash", toolCallId: "tool-1", args: { command: "npm test" } });
  collector.push({ type: "tool_call", timestamp: "2026-06-09T12:00:03Z", toolName: "bash", toolCallId: "tool-1", result: { content: [{ type: "text", text: "ok" }] } });
  collector.push({ type: "log", timestamp: "2026-06-09T12:00:04Z", level: "info", message: "done" });

  assert.deepEqual(collector.entries.map((entry) => [entry.seq, entry.role, entry.type]), [
    [1, "user", "text"],
    [2, "assistant", "text"],
    [3, "assistant", "tool_use"],
    [4, "tool", "tool_result"],
    [5, "system", "text"],
  ]);
  assert.equal(collector.entries[0].text, "Implement TASK-1");
  assert.equal(collector.entries[2].tool_name, "bash");
  assert.deepEqual(collector.entries[2].tool_input, { command: "npm test" });
  assert.equal(collector.entries[3].output, "ok");
});

test("normalizes Flue prompt usage to task-run usage fields", () => {
  assert.deepEqual(flueUsageToTaskUsage({
    input: 12,
    output: 34,
    cacheRead: 56,
    cacheWrite: 78,
    cost: { total: 0.123 },
  }), {
    input_tokens: 12,
    output_tokens: 34,
    cache_read_tokens: 56,
    cache_write_tokens: 78,
    estimated_cost_usd: 0.123,
  });
});
