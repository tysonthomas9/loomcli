/**
 * @vitest-environment jsdom
 */

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import "@testing-library/jest-dom";

import type { TranscriptEntry } from "@/types/agent";

import { TranscriptView, groupEvents } from "../TranscriptView";

function entry(overrides: Partial<TranscriptEntry>): TranscriptEntry {
  return {
    seq: 1,
    timestamp: "2026-07-17T12:00:00Z",
    role: "assistant",
    type: "text",
    text: "ok",
    ...overrides,
  };
}

describe("groupEvents", () => {
  it("promotes the first user text to the prompt and later user text to interjections", () => {
    const grouped = groupEvents([
      entry({ seq: 1, role: "user", type: "text", text: "start" }),
      entry({ seq: 2, role: "assistant", type: "text", text: "working" }),
      entry({ seq: 3, role: "user", type: "text", text: "also do this" }),
    ]);

    expect(grouped.prompt?.text).toBe("start");
    expect(grouped.blocks).toHaveLength(2);
    expect(grouped.blocks[0]).toMatchObject({ kind: "turn", seq: 2 });
    expect(grouped.blocks[1]).toMatchObject({
      kind: "interjection",
      seq: 3,
      text: "also do this",
    });
  });

  it("pairs tool_result entries with their matching tool_use item", () => {
    const grouped = groupEvents([
      entry({
        seq: 1,
        type: "tool_use",
        tool_name: "Read",
        tool_use_id: "tool-1",
        tool_input: { file_path: "src/main.ts" },
      }),
      entry({
        seq: 2,
        role: "tool",
        type: "tool_result",
        tool_use_id: "tool-1",
        output: "contents",
      }),
    ]);

    const turn = grouped.blocks[0];
    expect(turn?.kind).toBe("turn");
    if (turn?.kind !== "turn") return;
    expect(turn.items[0]).toMatchObject({
      kind: "tool",
      name: "Read",
      result: "contents",
    });
  });

  it("keeps assistant reasoning entries in grouped turns", () => {
    const grouped = groupEvents([
      entry({
        seq: 1,
        type: "text",
        text: "I will inspect this.",
        uuid: "msg-1",
      }),
      entry({
        seq: 2,
        type: "reasoning",
        text: "The failing check likely points at the session route.",
        uuid: "msg-1",
      }),
    ]);

    const turn = grouped.blocks[0];
    expect(turn?.kind).toBe("turn");
    if (turn?.kind !== "turn") return;
    expect(turn.items).toEqual([
      { kind: "text", seq: 1, text: "I will inspect this." },
      {
        kind: "reasoning",
        seq: 2,
        text: "The failing check likely points at the session route.",
      },
    ]);
  });
});

describe("TranscriptView", () => {
  it("renders reasoning entries with a distinct label", () => {
    render(
      <TranscriptView
        entries={[
          entry({
            seq: 1,
            type: "reasoning",
            text: "I need to compare the route params first.",
          }),
        ]}
      />,
    );

    expect(screen.getByTestId("transcript-reasoning")).toHaveTextContent(
      "Reasoning",
    );
    expect(screen.getByTestId("transcript-reasoning")).toHaveTextContent(
      "compare the route params",
    );
  });
});
