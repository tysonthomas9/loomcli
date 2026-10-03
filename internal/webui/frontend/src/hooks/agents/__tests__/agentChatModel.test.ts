import { describe, expect, it } from "vitest";

import type { AgentEvent } from "@/api/agentsv1";
import { addDelta, chatItems, settle } from "../agentChatModel";

let seq = 0;
function ev(kind: string, payload: object = {}): AgentEvent {
  seq++;
  return {
    agent_id: "a1",
    seq,
    event_id: `${kind}:${seq}`,
    kind,
    turn_id: "t1",
    payload,
    created_at: "",
  };
}

describe("chatModel", () => {
  it("skips a completed turn and marks a cancelled one", () => {
    const items = chatItems(
      [
        ev("agent.turn_completed", { stopReason: "completed" }),
        ev("agent.turn_completed", { stopReason: "cancelled" }),
        ev("agent.state_changed"),
      ],
      new Map(),
    );
    expect(items.map((i) => i.kind)).toEqual(["turn_end"]);
  });

  it("never shows a tool as running once its completion is saved, even if its start notice comes after", () => {
    // A subscriber still replaying history can get a tool's saved
    // item.completed before the live tool.started that preceded it.
    const done = ev("item.completed", {
      itemId: "m/tool/1",
      itemKind: "tool",
      tool: { name: "bash", input: '{"command":"ls"}', output: "a.go" },
    });
    let s = settle(new Map(), [done]);
    s = addDelta(
      s,
      ev("tool.started", {
        itemId: "m/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"ls"}' },
      }),
    );
    s = addDelta(s, ev("delta", { itemId: "m/msg", text: "late" }));
    const items = chatItems(
      [
        done,
        ev("item.completed", {
          itemId: "m/msg",
          itemKind: "message",
          text: "late",
        }),
      ],
      s,
    );
    expect(items.filter((i) => i.kind === "tool")).toEqual([
      expect.objectContaining({ status: "completed" }),
    ]);
    expect(items.filter((i) => i.key.startsWith("live:"))).toEqual([]);
  });

  it("settles only items that completed", () => {
    let s = addDelta(new Map(), ev("delta", { itemId: "a", text: "x" }));
    s = addDelta(s, ev("delta", { itemId: "b", text: "y" }));
    s = settle(s, [ev("item.completed", { itemId: "a" })]);
    expect([...s.keys()]).toEqual(["b"]);
  });
});

describe("chatModel tool calls and reasoning", () => {
  it("keeps a completed tool call's name, input, output and failure", () => {
    const items = chatItems(
      [
        ev("item.completed", {
          itemId: "x/tool/1",
          itemKind: "tool",
          tool: { name: "bash", input: '{"command":"ls"}', output: "a.go" },
        }),
        ev("item.completed", {
          itemId: "x/tool/2",
          itemKind: "tool",
          tool: { name: "read", output: "no such file", failed: true },
        }),
      ],
      new Map(),
    );
    expect(items).toEqual([
      expect.objectContaining({
        kind: "tool",
        status: "completed",
        tool: { name: "bash", input: '{"command":"ls"}', output: "a.go" },
      }),
      expect.objectContaining({ kind: "tool", status: "failed" }),
    ]);
  });

  it("shows a started tool as running and live reasoning until they complete", () => {
    let s = addDelta(
      new Map(),
      ev("tool.started", {
        itemId: "x/tool/1",
        itemKind: "tool",
        tool: { name: "bash", input: '{"command":"sleep 1"}' },
      }),
    );
    s = addDelta(
      s,
      ev("delta", { itemId: "r", itemKind: "reasoning", text: "hm" }),
    );
    s = addDelta(
      s,
      ev("delta", { itemId: "r", itemKind: "reasoning", text: "m" }),
    );
    expect(chatItems([], s)).toEqual([
      {
        key: "live:x/tool/1",
        kind: "tool",
        status: "running",
        tool: { name: "bash", input: '{"command":"sleep 1"}' },
      },
      { key: "live:r", kind: "reasoning", text: "hmm", streaming: true },
    ]);
    s = settle(s, [ev("item.completed", { itemId: "x/tool/1" })]);
    expect([...s.keys()]).toEqual(["r"]);
  });
});
