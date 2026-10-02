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

  it("settles only items that completed", () => {
    let s = addDelta(new Map(), ev("delta", { itemId: "a", text: "x" }));
    s = addDelta(s, ev("delta", { itemId: "b", text: "y" }));
    s = settle(s, [ev("item.completed", { itemId: "a" })]);
    expect([...s.keys()]).toEqual(["b"]);
  });
});
