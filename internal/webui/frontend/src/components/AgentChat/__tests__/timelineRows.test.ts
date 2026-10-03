import { describe, expect, it } from "vitest";

import type { ChatItem } from "@/hooks";
import { deriveTimelineRows, summarizeToolGroup } from "../timelineRows";

const tool = (
  key: string,
  name: string,
  status: "running" | "completed" | "failed" = "completed",
): Extract<ChatItem, { kind: "tool" }> => ({
  key,
  kind: "tool",
  tool: { name },
  status,
});
const text = (key: string): ChatItem => ({ key, kind: "agent", text: key });
const thought = (key: string): ChatItem => ({
  key,
  kind: "reasoning",
  text: key,
});

describe("timelineRows", () => {
  it("summarizes a tool group by what the calls did", () => {
    expect(
      summarizeToolGroup([
        tool("1", "bash"),
        tool("2", "Bash"),
        tool("3", "read"),
      ]),
    ).toBe("Ran 2 commands and read 1 file");
    expect(
      summarizeToolGroup([
        tool("1", "edit"),
        tool("2", "grep"),
        tool("3", "x"),
      ]),
    ).toBe("Changed 1 file, searched code 1 time, and used 1 tool");
    expect(summarizeToolGroup([tool("1", "loom/agent_list")])).toBe(
      "Used 1 tool",
    );
  });

  it("collapses consecutive tool calls into one summary row until expanded", () => {
    const items = [
      text("a"),
      tool("t1", "bash"),
      tool("t2", "read", "failed"),
      text("b"),
    ];
    const rows = deriveTimelineRows(items, new Set());
    expect(rows.map((r) => r.kind)).toEqual(["item", "work-toggle", "item"]);
    const toggle = rows[1] as Extract<
      (typeof rows)[number],
      { kind: "work-toggle" }
    >;
    expect(toggle).toMatchObject({
      count: 2,
      summary: "Ran 1 command and read 1 file",
      hasFailure: true,
      expanded: false,
    });
    const open = deriveTimelineRows(items, new Set([toggle.groupId]));
    expect(open.map((r) => r.kind)).toEqual([
      "item",
      "work-toggle",
      "work",
      "work",
      "item",
    ]);
  });

  it("shows a running group as one live row naming the running call", () => {
    const rows = deriveTimelineRows(
      [tool("t1", "bash"), tool("t2", "read", "running")],
      new Set(),
    );
    expect(rows).toEqual([
      expect.objectContaining({
        kind: "work-live",
        entry: expect.objectContaining({ key: "t2" }),
      }),
    ]);
  });

  it("shows a mixed group's last entry after +N previous log entries", () => {
    const rows = deriveTimelineRows(
      [thought("r1"), tool("t1", "bash"), thought("r2")],
      new Set(),
    );
    expect(rows.map((r) => [r.kind, r.id])).toEqual([
      ["work", "r2"],
      ["work-toggle", "work-toggle:r1"],
    ]);
    expect(rows[1]).toMatchObject({ hiddenCount: 2, onlyToolEntries: false });
  });

  it("shows live reasoning as one Thinking row at the end", () => {
    const rows = deriveTimelineRows(
      [
        text("a"),
        { key: "live:r", kind: "reasoning", text: "x", streaming: true },
      ],
      new Set(),
    );
    expect(rows.map((r) => r.kind)).toEqual(["item", "thinking"]);
  });
});
