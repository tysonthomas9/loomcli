import { describe, expect, it } from "vitest";

import { parseTranscript } from "../transcript";

const realSampleFixture = [
  "workspace root: /root/.loom/workspaces/LOCALMODE (LOOM_WORKTREE_PATH)",
  '…\\":\\"rs_0008f76a2ac4f0d7016a7fa5a97ed8819ab6861c4972f53a23\\",\\"summary\\":[]}',
  '{"type":"item.started","item":{"id":"item_5","type":"command_execution","command":"/bin/bash -lc \\"git status\\"","aggregated_output":"","exit_code":null,"status":"in_progress"}}',
  '{"type":"item.updated","item":{"id":"item_5","type":"command_execution","status":"in_progress"}}',
  '{"type":"item.completed","item":{"id":"item_5","type":"command_execution","command":"/bin/bash -lc \\"git status\\"","aggregated_output":"clean\\n","exit_code":0,"status":"completed"}}',
  '{"type":"item.started","item":{"id":"item_7","type":"file_change","changes":[{"path":"/root/.loom/result.json","kind":"add"}],"status":"in_progress"}}',
  '{"type":"item.completed","item":{"id":"item_7","type":"file_change","changes":[{"path":"/root/.loom/result.json","kind":"add"}],"status":"completed"}}',
  '{"type":"item.completed","item":{"id":"item_9","type":"agent_message","text":"{\\"recommendations\\":[]}"}}',
  '{"type":"turn.completed","usage":{"input_tokens":349798,"cached_input_tokens":305920,"cache_write_input_tokens":0,"output_tokens":3342,"reasoning_output_tokens":1118}}',
].join("\n");

describe("parseTranscript", () => {
  it("turns the real persisted event shapes into readable rows", () => {
    const parsed = parseTranscript(realSampleFixture);

    expect(parsed.codexEventCount).toBe(7);
    expect(parsed.rows.map((row) => row.kind)).toEqual([
      "plain",
      "unparsed",
      "command",
      "fileChange",
      "message",
      "turnCompleted",
    ]);
    expect(parsed.rows[1]).toEqual({
      kind: "unparsed",
      text: expect.stringContaining("rs_0008"),
    });
    expect(parsed.rows[2]).toMatchObject({
      kind: "command",
      command: '/bin/bash -lc "git status"',
      exitCode: 0,
      status: "completed",
      output: "clean\n",
    });
    expect(parsed.rows[3]).toEqual({
      kind: "fileChange",
      status: "completed",
      changes: [{ path: "/root/.loom/result.json", kind: "add" }],
    });
    expect(parsed.rows[4]).toEqual({
      kind: "message",
      text: '{"recommendations":[]}',
    });
    expect(parsed.rows[5]).toEqual({
      kind: "turnCompleted",
      usage: {
        inputTokens: 349_798,
        cachedInputTokens: 305_920,
        outputTokens: 3_342,
      },
    });
  });

  it("keeps plain-only logs on the raw rendering path", () => {
    expect(parseTranscript("repo discovery\n\nanalysis: complete")).toEqual({
      rows: [
        { kind: "plain", text: "repo discovery" },
        { kind: "plain", text: "analysis: complete" },
      ],
      codexEventCount: 0,
    });
  });

  it("returns no rows for empty and whitespace-only content", () => {
    expect(parseTranscript("")).toEqual({ rows: [], codexEventCount: 0 });
    expect(parseTranscript(" \n\t\n")).toEqual({
      rows: [],
      codexEventCount: 0,
    });
  });

  it("handles failures, reasoning summaries, and unknown item types", () => {
    const parsed = parseTranscript(
      [
        '{"type":"item.completed","item":{"type":"reasoning","summary":[{"text":"Check the existing conventions."},{"text":"Keep the change local."}]}}',
        '{"type":"turn.failed","error":{"message":"model context exhausted"}}',
        '{"type":"item.completed","item":{"type":"todo_list","items":[]}}',
      ].join("\n"),
    );

    expect(parsed.codexEventCount).toBe(3);
    expect(parsed.rows).toEqual([
      {
        kind: "reasoning",
        text: "Check the existing conventions.\nKeep the change local.",
      },
      { kind: "turnFailed", message: "model context exhausted" },
      {
        kind: "other",
        label: "Todo list",
        raw: expect.stringContaining('"type":"todo_list"'),
      },
    ]);
  });

  it("never throws on malformed JSON or missing event fields", () => {
    const parsed = parseTranscript(
      [
        '{"type":"item.completed","item":{"type":"command_execution","exit_code":"1"}}',
        '{"not_type":"item.completed"}',
        '{"type":"turn.completed","usage":null}',
        "{broken",
      ].join("\n"),
    );

    expect(parsed.codexEventCount).toBe(2);
    expect(parsed.rows).toEqual([
      {
        kind: "command",
        command: "",
        exitCode: null,
        status: "unknown",
        output: "",
      },
      { kind: "unparsed", text: '{"not_type":"item.completed"}' },
      {
        kind: "turnCompleted",
        usage: { inputTokens: 0, cachedInputTokens: 0, outputTokens: 0 },
      },
      { kind: "unparsed", text: "{broken" },
    ]);
  });
});
