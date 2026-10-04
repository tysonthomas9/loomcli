import { describe, expect, it } from "vitest";

import type { AgentEvent } from "@/api/agentsv1";
import type { ChatItem } from "@/hooks";
import {
  STEP_MAX,
  deriveTimelineRows,
  stepLabel,
  summarizeToolGroup,
} from "../timelineRows";

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
  it("marks a group failed when any call failed, not only the last", () => {
    const [summary] = deriveTimelineRows(
      [tool("1", "read"), tool("2", "read", "failed"), tool("3", "bash")],
      new Set(),
    );
    expect(summary).toMatchObject({ kind: "work-toggle", hasFailure: true });
    const rows = deriveTimelineRows(
      [tool("1", "bash", "failed"), thought("t"), tool("2", "read")],
      new Set(),
    );
    expect(rows.find((r) => r.kind === "work-toggle")).toMatchObject({
      hiddenCount: 2,
      hasFailure: true,
    });
  });

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

describe("stepLabel", () => {
  const ev = (kind: string, payload: object): AgentEvent => ({
    agent_id: "c",
    seq: 0,
    event_id: "e",
    kind,
    turn_id: "t",
    payload,
    created_at: "",
  });
  const started = (name: string, input?: string) =>
    ev("tool.started", {
      itemKind: "tool",
      tool: input === undefined ? { name } : { name, input },
    });

  it("names a tool by its action and salient argument on every harness", () => {
    for (const name of ["bash", "Bash", "exec_command", "shell"])
      expect(stepLabel(started(name, '{"command":"npm test"}'))).toBe(
        "▸ Ran command · npm test",
      );
    expect(stepLabel(started("read", '{"filePath":"src/a.ts"}'))).toBe(
      "▸ Read file · src/a.ts",
    );
    expect(
      stepLabel(
        ev("item.completed", {
          itemKind: "tool",
          tool: { name: "grep", input: '{"pattern":"TODO"}', output: "x" },
        }),
      ),
    ).toBe("▸ Searched code · TODO");
    expect(stepLabel(started("todowrite"))).toBe("▸ Todowrite");
  });

  it("never shows raw tool input", () => {
    expect(stepLabel(started("todowrite", '{"todos":[{"id":1}]}'))).toBe(
      "▸ Todowrite",
    );
    expect(stepLabel(started("bash", '{"command":"ls"}'), false)).toBe(
      "▸ Ran command",
    );
  });

  it("cuts a long input to about 60 characters", () => {
    const long = `npm test -- ${"x".repeat(200)}`;
    const got = stepLabel(started("bash", JSON.stringify({ command: long })))!;
    expect(got.length).toBeLessThanOrEqual(STEP_MAX);
    expect(got.startsWith("▸ Ran command · npm test -- x")).toBe(true);
    expect(got.endsWith("…")).toBe(true);
  });

  it("shows reasoning's first plain line", () => {
    expect(
      stepLabel(
        ev("item.completed", {
          itemKind: "reasoning",
          text: "**Checking routes**\n\nThe router lives in app.ts.",
        }),
      ),
    ).toBe("💭 Thinking · Checking routes");
    expect(
      stepLabel(ev("item.completed", { itemKind: "reasoning", text: "" })),
    ).toBe("💭 Thinking");
  });

  it("is null for anything but a step", () => {
    expect(
      stepLabel(ev("item.completed", { itemKind: "message", text: "hi" })),
    ).toBeNull();
    expect(stepLabel(ev("delta", { itemKind: "message", text: "hi" }))).toBe(
      null,
    );
  });
});
