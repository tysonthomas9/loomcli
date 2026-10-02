import { describe, expect, it } from "vitest";
import type { Agent } from "@/api/agentsv1";
import { childrenByParent, upsert } from "../agentRoster";

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
