import { describe, expect, it } from "vitest";
import type { Agent, AgentEvent } from "@/api/agentsv1";
import {
  ROSTER_KINDS,
  applyActivity,
  childrenByParent,
  upsert,
} from "../agentRoster";

const a = (id: string, parent: string | null, at: string) =>
  ({ agent_id: id, parent_agent_id: parent, created_at: at }) as Agent;

describe("childrenByParent", () => {
  it("nests children oldest first and keeps orphans at the top", () => {
    const r = upsert(new Map(), [
      a("k2", "l", "3"),
      a("l", null, "1"),
      a("k1", "l", "2"),
      a("orphan", "gone", "4"),
    ]);
    const kids = childrenByParent(r);
    expect(kids.get("")!.map((x) => x.agent_id)).toEqual(["l", "orphan"]);
    expect(kids.get("l")!.map((x) => x.agent_id)).toEqual(["k1", "k2"]);
  });
});

describe("applyActivity", () => {
  const ev = (
    agent: string,
    kind: string,
    payload: object,
    at = "",
  ): AgentEvent => ({
    agent_id: agent,
    seq: kind === "tool.started" ? 0 : 1,
    event_id: `${kind}:${agent}`,
    kind,
    turn_id: "t",
    payload,
    created_at: at,
  });
  const state = (agent: string, from: string, to: string, at = "") =>
    ev(agent, "agent.state_changed", { from, to }, at);
  const tool = ev("c", "tool.started", { itemKind: "tool", tool: {} });

  it("asks the stream for tool starts and completed items", () => {
    expect(ROSTER_KINDS).toEqual(
      expect.arrayContaining(["tool.started", "item.completed"]),
    );
  });

  it("starts a turn, keeps the latest step through an ask, and drops it when the turn ends", () => {
    let m = applyActivity(new Map(), [state("c", "idle", "active", "T0")]);
    expect(m.get("c")).toEqual({ turnAt: "T0" });
    m = applyActivity(m, [tool]);
    expect(m.get("c")).toEqual({ turnAt: "T0", step: tool });
    const thought = ev("c", "item.completed", { itemKind: "reasoning" });
    m = applyActivity(m, [
      state("c", "active", "waiting"),
      state("c", "waiting", "active", "T1"),
      thought,
    ]);
    expect(m.get("c")).toEqual({ turnAt: "T0", step: thought });
    m = applyActivity(m, [state("c", "active", "idle")]);
    expect(m.has("c")).toBe(false);
  });

  it("ignores messages and other events without changing the map", () => {
    const m = applyActivity(new Map(), [tool]);
    const same = applyActivity(m, [
      ev("c", "item.completed", { itemKind: "message", text: "hi" }),
      ev("c", "child.created", {}),
    ]);
    expect(same).toBe(m);
  });
});
