import assert from "node:assert/strict";
import test from "node:test";

import {
  createTranscriptCollector,
  flueEventsToTaskUsage,
  flueUsageToTaskUsage,
  redactTranscriptEntries,
  serializeTranscriptJSONL,
} from "./flue-event-transcript.mjs";

test("converts Flue events to canonical Loom transcript entries", () => {
  const collector = createTranscriptCollector();
  collector.push({
    type: "turn_request",
    timestamp: "2026-06-09T12:00:00Z",
    purpose: "agent",
    operationId: "op-1",
    input: {
      messages: [
        { role: "system", content: "ignore" },
        { role: "user", content: [{ type: "text", text: "Implement TASK-1" }] },
      ],
    },
  });
  collector.push({ type: "turn_request", timestamp: "2026-06-09T12:00:00.500Z", purpose: "agent", operationId: "op-1", input: { messages: [{ role: "user", content: "Implement TASK-1" }] } });
  collector.push({ type: "turn_request", timestamp: "2026-06-09T12:00:00.750Z", purpose: "compaction", input: { messages: [{ role: "user", content: "internal summary" }] } });
  collector.push({ type: "text_delta", timestamp: "2026-06-09T12:00:01Z", turnId: "turn-1", text: "Working" });
  collector.push({ type: "tool_start", timestamp: "2026-06-09T12:00:02Z", turnId: "turn-1", toolName: "bash", toolCallId: "tool-1", args: { command: "npm test" } });
  collector.push({ type: "tool_call", timestamp: "2026-06-09T12:00:03Z", toolName: "bash", toolCallId: "tool-1", result: { content: [{ type: "text", text: "ok" }] } });
  collector.push({ type: "task_start", timestamp: "2026-06-09T12:00:04Z", taskId: "TASK-2", agent: "worker", cwd: "/repo" });
  collector.push({ type: "task", timestamp: "2026-06-09T12:00:05Z", taskId: "TASK-2", agent: "worker", isError: false, durationMs: 123 });
  collector.push({ type: "operation_start", timestamp: "2026-06-09T12:00:06Z", operationId: "op-2", operationKind: "shell" });
  collector.push({ type: "operation", timestamp: "2026-06-09T12:00:07Z", operationId: "op-2", operationKind: "shell", isError: false, durationMs: 456 });
  collector.push({ type: "compaction", timestamp: "2026-06-09T12:00:08Z", messagesBefore: 20, messagesAfter: 8, durationMs: 99 });
  collector.push({ type: "log", timestamp: "2026-06-09T12:00:09Z", level: "info", message: "done" });

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
  assert.equal(collector.entries[0].text, "Implement TASK-1");
  assert.match(collector.entries[0].timestamp, /^\d{4}-\d{2}-\d{2}T/);
  assert.equal(collector.entries[2].tool_name, "bash");
  assert.equal(collector.entries[2].uuid, "turn-1");
  assert.deepEqual(collector.entries[2].tool_input, { command: "npm test" });
  assert.equal(collector.entries[3].tool_use_id, "tool-1");
  assert.equal(collector.entries[3].output, "ok");
  assert.equal(collector.entries[3].text, undefined);
  assert.match(collector.entries[4].text, /task TASK-2 started/);
  assert.match(collector.entries[8].text, /compaction completed/);

  const serialized = serializeTranscriptJSONL(collector.entries);
  assert.equal(serialized.split("\n").filter(Boolean).length, collector.entries.length);
  assert.ok(!serialized.includes('"type":"turn_request"'));
});

test("normalizes Flue prompt usage to task-run usage fields when cost unit is USD", () => {
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
  assert.deepEqual(flueUsageToTaskUsage({
    input: 1,
    output: 2,
    cost: { total: 3 },
  }, { costUnit: "credits" }), {
    input_tokens: 1,
    output_tokens: 2,
  });
});

test("reconstructs usage from turn events without summing rollups", () => {
  assert.deepEqual(flueEventsToTaskUsage([
    { type: "turn", turnId: "t1", usage: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4, cost: { total: 0.01 } } },
    { type: "turn", turnId: "t1", usage: { input: 100, output: 200, cacheRead: 300, cacheWrite: 400, cost: { total: 5 } } },
    { type: "operation", operationId: "op", usage: { input: 1000, output: 1000, cacheRead: 0, cacheWrite: 0, cost: { total: 10 } } },
    { type: "compaction", usage: { input: 1000, output: 1000, cacheRead: 0, cacheWrite: 0, cost: { total: 10 } } },
    { type: "turn", turnId: "t2", usage: { input: 5, output: 6, cacheRead: 7, cacheWrite: 8, cost: { total: 0.02 } } },
  ], { costUnit: "usd" }), {
    input_tokens: 6,
    output_tokens: 8,
    cache_read_tokens: 10,
    cache_write_tokens: 12,
    estimated_cost_usd: 0.03,
  });
});

test("redacts transcript entries before serialization", () => {
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
