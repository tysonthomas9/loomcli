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

describe("child results (DF1)", () => {
  const rec = (attempt: number) => ({
    ...ev("task_completed", { child: "k1", attempt, outcome: "completed" }),
    event_id: `task_completed:k1:${attempt}`,
  });

  it("folds a result's waiting and delivered state into its one card, matched on the named record", () => {
    const created = ev("child.created", { child: "k1", name: "kid" });
    const waiting = [
      {
        sender: "agent:k1",
        text: "task_completed:k1:0 …",
        since: "",
        message: "",
        completions: [{ child: "k1", attempt: 0 }],
      },
    ];
    let items = chatItems([created, rec(0)], new Map(), waiting);
    expect(items.map((i) => i.kind)).toEqual(["started", "completion"]);
    expect(items[1]).toMatchObject({ delivery: "waiting" });
    // Delivered: the delivery's text is only the record, so no bubble.
    items = chatItems(
      [
        created,
        rec(0),
        ev("message.delivered", {
          sender: "agent:k1",
          text: "task_completed:k1:0 …",
          completions: [{ child: "k1", attempt: 0 }],
          message: "",
        }),
      ],
      new Map(),
      [],
    );
    expect(items.map((i) => i.kind)).toEqual(["started", "completion"]);
    expect(items[1]).toMatchObject({ delivery: "delivered" });
    // Free text that looks like a record is never matched.
    items = chatItems([created, rec(0)], new Map(), [
      { sender: "agent:k1", text: "task_completed:k1:0 …", since: "" },
    ]);
    expect(items[1]).not.toHaveProperty("delivery");
  });

  it("merges back-to-back starts into one marker and names an agent's own message", () => {
    const items = chatItems(
      [
        ev("child.created", { child: "k1", name: "api" }),
        ev("child.created", { child: "k2", name: "ui" }),
        ev("message.delivered", {
          sender: "agent:k2",
          text: "hi **there**\ntask_completed:k2:0 …",
          completions: [{ child: "k2", attempt: 0 }],
          message: "hi **there**",
        }),
        ev("child.created", { child: "k3", name: "docs" }),
      ],
      new Map(),
    );
    expect(items).toMatchObject([
      { kind: "started", children: [{ name: "api" }, { name: "ui" }] },
      { kind: "from_agent", agent: "k2", name: "ui", text: "hi **there**" },
      { kind: "started", children: [{ name: "docs" }] },
    ]);
  });
});
