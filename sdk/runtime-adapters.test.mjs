import assert from "node:assert/strict";
import test from "node:test";

import {
  createFlueTranscriptCollector,
  flueEventsToTaskUsage,
  flueUsageToTaskUsage,
  redactTranscriptEntries,
  serializeTranscriptJSONL,
} from "./runtime-adapters.js";

test("runtime adapter emits canonical transcript entries", () => {
  const collector = createFlueTranscriptCollector();
  collector.push({
    type: "turn_request",
    timestamp: "2026-06-09T12:00:00Z",
    purpose: "agent",
    operationId: "op-1",
    input: { messages: [{ role: "user", content: "Implement TASK-1" }] },
  });
  collector.push({
    type: "turn_request",
    timestamp: "2026-06-09T12:00:01Z",
    purpose: "agent",
    operationId: "op-1",
    input: { messages: [{ role: "user", content: "Implement TASK-1" }] },
  });
  collector.push({
    type: "turn_request",
    timestamp: "2026-06-09T12:00:02Z",
    purpose: "compaction",
    input: { messages: [{ role: "user", content: "internal" }] },
  });
  collector.push({ type: "text_delta", timestamp: "2026-06-09T12:00:03Z", turnId: "turn-1", text: "Working" });
  collector.push({ type: "tool_start", timestamp: "2026-06-09T12:00:04Z", turnId: "turn-1", toolName: "bash", toolCallId: "tool-1", args: "npm test" });
  collector.push({ type: "tool_call", timestamp: "2026-06-09T12:00:05Z", toolName: "bash", toolCallId: "tool-1", result: { content: [{ type: "text", text: "ok" }] } });
  collector.push({ type: "task_start", timestamp: "2026-06-09T12:00:06Z", taskId: "TASK-2", agent: "worker", cwd: "/repo" });
  collector.push({ type: "task", timestamp: "2026-06-09T12:00:07Z", taskId: "TASK-2", agent: "worker", isError: false, durationMs: 123 });
  collector.push({ type: "operation_start", timestamp: "2026-06-09T12:00:08Z", operationId: "op-2", operationKind: "shell" });
  collector.push({ type: "operation", timestamp: "2026-06-09T12:00:09Z", operationId: "op-2", operationKind: "shell", isError: false, durationMs: 456 });
  collector.push({ type: "compaction", timestamp: "2026-06-09T12:00:10Z", messagesBefore: 20, messagesAfter: 8, durationMs: 99 });
  collector.push({ type: "log", timestamp: "2026-06-09T12:00:11Z", level: "info", message: "done" });

  assert.deepEqual(collector.entries.map((entry) => [entry.seq, entry.role, entry.type]), [
    [1, "user", "text"],
    [2, "assistant", "text"],
    [3, "assistant", "tool_use"],
    [4, "tool", "tool_result"],
    [5, "system", "session_meta"],
    [6, "system", "session_meta"],
    [7, "system", "session_meta"],
    [8, "system", "session_meta"],
    [9, "system", "session_meta"],
    [10, "system", "text"],
  ]);
  assert.deepEqual(collector.entries[2].tool_input, { value: "npm test" });
  assert.equal(collector.entries[3].output, "ok");
  assert.equal(collector.entries[3].text, undefined);
  assert.ok(!serializeTranscriptJSONL(collector.entries).includes('"type":"turn_request"'));
});

test("runtime adapter maps usage with explicit USD cost unit", () => {
  assert.deepEqual(flueUsageToTaskUsage({
    input: 12,
    output: 34,
    cacheRead: 56,
    cacheWrite: 78,
    cost: { total: 0.123 },
  }, { costUnit: "usd" }), {
    input_tokens: 12,
    output_tokens: 34,
    cache_read_tokens: 56,
    cache_write_tokens: 78,
    estimated_cost_usd: 0.123,
  });
  assert.deepEqual(flueUsageToTaskUsage({ input: 1, output: 2, cost: { total: 3 } }), {
    input_tokens: 1,
    output_tokens: 2,
  });
});

test("runtime adapter sums leaf turn usage once", () => {
  assert.deepEqual(flueEventsToTaskUsage([
    { type: "turn", turnId: "t1", usage: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4, cost: { total: 0.01 } } },
    { type: "turn", turnId: "t1", usage: { input: 100, output: 200, cacheRead: 300, cacheWrite: 400, cost: { total: 5 } } },
    { type: "operation", usage: { input: 1000, output: 1000, cacheRead: 0, cacheWrite: 0, cost: { total: 10 } } },
    { type: "turn", turnId: "t2", usage: { input: 5, output: 6, cacheRead: 7, cacheWrite: 8, cost: { total: 0.02 } } },
  ], { costUnit: "usd" }), {
    input_tokens: 6,
    output_tokens: 8,
    cache_read_tokens: 10,
    cache_write_tokens: 12,
    estimated_cost_usd: 0.03,
  });
});

test("runtime adapter redacts structured entries", () => {
  const entries = redactTranscriptEntries([
    {
      seq: 1,
      timestamp: "2026-06-09T12:00:00Z",
      role: "assistant",
      type: "tool_use",
      tool_name: "bash",
      tool_use_id: "tool-1",
      tool_input: { env: "secret-token" },
    },
  ], ["secret-token"]);
  assert.deepEqual(entries[0].tool_input, { env: "[redacted]" });
  assert.ok(!serializeTranscriptJSONL(entries).includes("secret-token"));
});
